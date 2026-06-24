package region

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/klauspost/compress/zstd"
)

// The .linear region format packs an entire 32x32 region into ONE
// zstd-compressed file, in contrast to the Anvil .mca format's per-chunk zlib
// sectors with 4KB padding. It is a Leaf/LinearPaper community format that
// yields ~50% disk savings on populated Overworld/Nether regions and ~95% on
// sparse End regions. This is an OPT-IN codec that EXTENDS save/region
// in-place — mca.go is unchanged and .mca stays the default.
//
// On-disk layout (all multi-byte integers big-endian), from the canonical
// xymb-endcrystalme/LinearRegionFileFormatTools linear.py:
//
//	[leading signature  uint64 = 0xc3ff13183cca9d9a]
//	[header ">QBQbhIQ":
//	    magic            uint64 = 0xc3ff13183cca9d9a (repeated in the header)
//	    version          uint8  = 1
//	    newestTimestamp  uint64 (unix seconds of the newest chunk)
//	    compressionLevel int8   (informational; the zstd level used)
//	    chunkCount       int16  (number of non-empty chunks)
//	    dataLength       uint32 (length of the zstd-compressed body)
//	    dataHash         uint64 (reserved; 0 when unused)]
//	[zstd-compressed body, dataLength bytes]
//	[trailing signature uint64 = 0xc3ff13183cca9d9a]
//
// The zstd body decompresses to:
//
//	[1024 chunk headers, each ">II":
//	    rawSize   uint32 (length of this cell's raw NBT blob; 0 => empty cell)
//	    timestamp uint32 (unix seconds)]
//	[concatenated raw chunk NBT blobs, in chunk-index order, present cells only]
//
// The chunk index for cell (x, z) within the region is z*32 + x, matching the
// .mca offsets[z][x] convention so a (cx, cz) -> cell mapping is identical
// across both codecs. The per-cell blobs are the SAME save.Chunk NBT blobs
// that mca.go's ReadSector returns, so the loader feeds either codec's output
// into the unchanged save.Chunk.Load -> level.ChunkFromSave path.
const (
	linearSignature uint64 = 0xc3ff13183cca9d9a
	linearVersion   byte   = 1

	// linearChunks is the number of cells in a region (32*32).
	linearChunks = 1024

	// linearHeaderSize is the size of the framed file header described by the
	// ">QBQbhIQ" struct format: magic(8) + version(1) + newestTimestamp(8) +
	// compressionLevel(1) + chunkCount(2) + dataLength(4) + dataHash(8).
	linearHeaderSize = 8 + 1 + 8 + 1 + 2 + 4 + 8 // = 32

	// linearCellHeaderSize is the size of one decompressed per-cell header
	// (">II": rawSize uint32 + timestamp uint32).
	linearCellHeaderSize = 8

	// linearDefaultCompression is the zstd level encoded into the header.
	// It is informational only — the decoder ignores it.
	linearDefaultCompression int8 = 6

	// maxDecompressedSize bounds the decompressed body to defend against a
	// decompression bomb (a tiny .linear whose body inflates to gigabytes).
	// The .linear format permits regions up to 4GB; we enforce a tighter,
	// practical 512MiB ceiling. A region exceeding this is rejected as a
	// corrupt/hostile file (ErrLinearTooLarge), never decoded into an OOM.
	maxDecompressedSize = 512 << 20 // 512 MiB
)

var (
	// ErrLinearCorrupt is returned when a .linear file is structurally
	// invalid: a wrong leading/trailing signature, a bad version, a truncated
	// header/body, or an internally inconsistent cell table.
	ErrLinearCorrupt = errors.New("linear: corrupt region file")

	// ErrLinearTooLarge is returned when a .linear body would decompress
	// beyond maxDecompressedSize (decompression-bomb guard), mirroring mca's
	// ErrTooLarge. The over-large region is rejected, not decoded.
	ErrLinearTooLarge = errors.New("linear: decompressed region too large")
)

// sharedLinearEncoder / sharedLinearDecoder are reusable, concurrency-safe
// zstd codecs (EncodeAll and DecodeAll are reentrant). region.Region itself is
// Not MT-Safe, but these stateless one-shot codecs are shared so callers do not
// pay per-call goroutine-pool setup.
var (
	sharedLinearEncoder *zstd.Encoder
	sharedLinearDecoder *zstd.Decoder
)

func init() {
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(int(linearDefaultCompression))))
	if err != nil {
		panic("linear: build zstd encoder: " + err.Error())
	}
	// DecodeAll does not use the streaming window-memory budget, so we cap the
	// output length manually after decode (see ReadLinear). Concurrency 0 lets
	// the library pick; we only ever call the reentrant DecodeAll.
	dec, err := zstd.NewReader(nil)
	if err != nil {
		panic("linear: build zstd decoder: " + err.Error())
	}
	sharedLinearEncoder = enc
	sharedLinearDecoder = dec
}

// LinearRegion is the in-memory form of a .linear region: 1024 cells indexed
// z*32+x. A nil Data slice means the cell is empty (absent chunk), which is
// distinct from a present-but-zero-length blob. This mirrors mca's
// ExistSector/ErrNoSector distinction so an absent chunk reads as absent.
type LinearRegion struct {
	Data       [linearChunks][]byte // raw per-cell NBT blobs; nil => empty cell
	Timestamps [linearChunks]uint32 // unix seconds per cell (0 for empty cells)
}

// cellIndex maps an in-region cell (x, z) to its flat 1024-array index,
// matching the .mca offsets[z][x] convention (z-major).
func cellIndex(x, z int) int { return z*32 + x }

// Get returns the raw NBT blob at in-region cell (x, z) and whether the cell is
// present. An absent cell returns (nil, false).
func (lr *LinearRegion) Get(x, z int) ([]byte, bool) {
	i := cellIndex(x, z)
	if lr.Data[i] == nil {
		return nil, false
	}
	return lr.Data[i], true
}

// Set stores a raw NBT blob at in-region cell (x, z) with the given unix
// timestamp. A nil OR empty blob clears the cell (marks it empty/absent): the
// .linear format encodes presence as a non-zero rawSize, so — like vanilla,
// where a stored chunk always carries non-empty NBT — there is no
// present-but-zero-length cell. An absent cell reads back as absent.
func (lr *LinearRegion) Set(x, z int, data []byte, timestamp uint32) {
	i := cellIndex(x, z)
	if len(data) == 0 {
		lr.Data[i] = nil
		lr.Timestamps[i] = 0
		return
	}
	lr.Data[i] = data
	lr.Timestamps[i] = timestamp
}

// WriteLinear encodes a LinearRegion into the .linear file format and writes it
// to w. The body (1024 ">II" cell headers + concatenated present blobs) is
// zstd-compressed as one blob, framed by the leading/trailing signatures and
// the ">QBQbhIQ" header.
func WriteLinear(w io.Writer, lr *LinearRegion) error {
	// Build the decompressed body: 1024 cell headers followed by the present
	// blobs in cell-index order.
	var body bytes.Buffer

	var (
		chunkCount      int
		newestTimestamp uint32
	)
	for i := 0; i < linearChunks; i++ {
		var rawSize uint32
		if lr.Data[i] != nil {
			rawSize = uint32(len(lr.Data[i]))
			chunkCount++
		}
		ts := lr.Timestamps[i]
		if ts > newestTimestamp {
			newestTimestamp = ts
		}
		var cell [linearCellHeaderSize]byte
		binary.BigEndian.PutUint32(cell[0:4], rawSize)
		binary.BigEndian.PutUint32(cell[4:8], ts)
		body.Write(cell[:])
	}
	for i := 0; i < linearChunks; i++ {
		if lr.Data[i] != nil {
			body.Write(lr.Data[i])
		}
	}

	compressed := sharedLinearEncoder.EncodeAll(body.Bytes(), nil)
	if len(compressed) > 0xFFFFFFFF {
		return fmt.Errorf("%w: compressed body exceeds uint32", ErrLinearTooLarge)
	}

	// Frame: leading signature, header ">QBQbhIQ", compressed body, trailing
	// signature.
	var hdr bytes.Buffer
	writeU64(&hdr, linearSignature) // leading signature
	writeU64(&hdr, linearSignature) // header magic
	hdr.WriteByte(linearVersion)
	writeU64(&hdr, uint64(newestTimestamp))
	hdr.WriteByte(byte(linearDefaultCompression))
	writeU16(&hdr, uint16(int16(chunkCount)))
	writeU32(&hdr, uint32(len(compressed)))
	writeU64(&hdr, 0) // dataHash reserved

	if _, err := w.Write(hdr.Bytes()); err != nil {
		return err
	}
	if _, err := w.Write(compressed); err != nil {
		return err
	}
	var trailer [8]byte
	binary.BigEndian.PutUint64(trailer[:], linearSignature)
	if _, err := w.Write(trailer[:]); err != nil {
		return err
	}
	return nil
}

// ReadLinear decodes a .linear file from raw, verifying the leading and
// trailing signatures BEFORE decompressing the body (corrupt-file + bomb
// guard), enforcing maxDecompressedSize, and slicing out the 1024 per-cell
// blobs. An absent cell (rawSize 0) stays nil so it reads as absent.
func ReadLinear(raw []byte) (*LinearRegion, error) {
	// Need at least: leading sig(8) + header body(24) + trailing sig(8).
	if len(raw) < 8+linearHeaderSize {
		return nil, fmt.Errorf("%w: file shorter than header", ErrLinearCorrupt)
	}

	off := 0
	if binary.BigEndian.Uint64(raw[off:]) != linearSignature {
		return nil, fmt.Errorf("%w: bad leading signature", ErrLinearCorrupt)
	}
	off += 8

	// Header body ">QBQbhIQ".
	if binary.BigEndian.Uint64(raw[off:]) != linearSignature {
		return nil, fmt.Errorf("%w: bad header magic", ErrLinearCorrupt)
	}
	off += 8
	version := raw[off]
	off++
	if version != linearVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrLinearCorrupt, version)
	}
	// newestTimestamp (8) — informational, skipped.
	off += 8
	// compressionLevel (1) — informational, skipped.
	off++
	chunkCount := int16(binary.BigEndian.Uint16(raw[off:]))
	off += 2
	dataLength := binary.BigEndian.Uint32(raw[off:])
	off += 4
	// dataHash (8) reserved — skipped.
	off += 8

	// Body must fit, with the trailing 8-byte signature following it.
	bodyEnd := off + int(dataLength)
	if dataLength == 0 || bodyEnd+8 > len(raw) || bodyEnd < off {
		return nil, fmt.Errorf("%w: declared body length out of range", ErrLinearCorrupt)
	}

	// Verify the trailing signature BEFORE decompressing (integrity gate).
	if binary.BigEndian.Uint64(raw[bodyEnd:]) != linearSignature {
		return nil, fmt.Errorf("%w: bad trailing signature", ErrLinearCorrupt)
	}

	compressed := raw[off:bodyEnd]

	// Decompression-bomb guard: decode then enforce the size cap. DecodeAll
	// allocates the output, so we also bound how much we will accept by
	// returning an error if it exceeds the cap.
	body, err := sharedLinearDecoder.DecodeAll(compressed, make([]byte, 0, len(compressed)*2))
	if err != nil {
		return nil, fmt.Errorf("%w: zstd decode: %v", ErrLinearCorrupt, err)
	}
	if len(body) > maxDecompressedSize {
		return nil, fmt.Errorf("%w: %d bytes", ErrLinearTooLarge, len(body))
	}

	// The decompressed body must hold the full 1024-cell header table.
	if len(body) < linearChunks*linearCellHeaderSize {
		return nil, fmt.Errorf("%w: body shorter than cell table", ErrLinearCorrupt)
	}

	lr := &LinearRegion{}
	var (
		sizes     [linearChunks]uint32
		present   int
		blobBytes int64
	)
	for i := 0; i < linearChunks; i++ {
		base := i * linearCellHeaderSize
		rawSize := binary.BigEndian.Uint32(body[base : base+4])
		ts := binary.BigEndian.Uint32(body[base+4 : base+8])
		lr.Timestamps[i] = ts
		sizes[i] = rawSize
		if rawSize > 0 {
			present++
			blobBytes += int64(rawSize)
		}
	}

	// Sanity: declared non-empty cells must match the header's chunkCount.
	if int(chunkCount) != present {
		return nil, fmt.Errorf("%w: chunkCount %d != present cells %d", ErrLinearCorrupt, chunkCount, present)
	}

	// The concatenated blobs must exactly fill the remainder of the body.
	blobStart := int64(linearChunks * linearCellHeaderSize)
	if blobStart+blobBytes != int64(len(body)) {
		return nil, fmt.Errorf("%w: blob region size mismatch", ErrLinearCorrupt)
	}

	cursor := blobStart
	for i := 0; i < linearChunks; i++ {
		if sizes[i] == 0 {
			continue // empty cell stays nil (absent chunk)
		}
		end := cursor + int64(sizes[i])
		blob := make([]byte, sizes[i])
		copy(blob, body[cursor:end])
		lr.Data[i] = blob
		cursor = end
	}

	return lr, nil
}

// OpenLinear reads and decodes a .linear file from disk. A fresh file handle is
// opened and closed per call — like region.Open, the result is Not MT-Safe and
// must not be shared across goroutines.
func OpenLinear(name string) (*LinearRegion, error) {
	raw, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return ReadLinear(raw)
}

// SaveLinear encodes lr and writes it to the named file atomically-ish (via a
// temp file rename) so a crash mid-write never leaves a truncated .linear in
// place of a good one.
func SaveLinear(name string, lr *LinearRegion) error {
	dir := name
	if i := lastSep(name); i >= 0 {
		dir = name[:i]
	} else {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, "linear-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := WriteLinear(tmp, lr); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, name); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// ReadSectorLinear returns the raw NBT blob for in-region cell (x, z), giving
// .linear the same single-chunk read shape as mca's ReadSector. It returns
// ErrNoSector for an absent cell so the format-aware loader can treat a .linear
// miss exactly like a .mca miss.
func (lr *LinearRegion) ReadSectorLinear(x, z int) ([]byte, error) {
	data, ok := lr.Get(x, z)
	if !ok {
		return nil, ErrNoSector
	}
	return data, nil
}

// WriteSectorLinear stores a raw NBT blob at in-region cell (x, z), stamping the
// current time, giving .linear the same single-chunk write shape as mca's
// WriteSector.
func (lr *LinearRegion) WriteSectorLinear(x, z int, data []byte) {
	lr.Set(x, z, data, uint32(time.Now().Unix()))
}

func writeU16(b *bytes.Buffer, v uint16) {
	var tmp [2]byte
	binary.BigEndian.PutUint16(tmp[:], v)
	b.Write(tmp[:])
}

func writeU32(b *bytes.Buffer, v uint32) {
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], v)
	b.Write(tmp[:])
}

func writeU64(b *bytes.Buffer, v uint64) {
	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], v)
	b.Write(tmp[:])
}

func lastSep(name string) int {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '/' || name[i] == '\\' {
			return i
		}
	}
	return -1
}
