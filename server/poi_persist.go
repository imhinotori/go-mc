package server

import (
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/imhinotori/sulfur/nbt"
	pk "github.com/imhinotori/sulfur/net/packet"
	regionfile "github.com/imhinotori/sulfur/save/region"
)

// poi_persist.go — SectionStorage persistence for the Point-of-Interest manager (poi.go), ported 1:1
// from the unobfuscated 26.2 jar (net.minecraft.world.entity.ai.village.poi.PoiManager, which extends
// net.minecraft.world.level.chunk.storage.SectionStorage, backed by SimpleRegionStorage == Anvil
// .mca region files under world/poi/). A vanilla 26.2 server reading world/poi/r.x.z.mca expects the
// EXACT per-chunk NBT shape produced here, so this is a compatibility surface.
//
// 1:1 ANCHORS (VERIFIED CFR/javap this session):
//   - PoiManager(RegionStorageInfo, Path folder, ...): super(new SimpleRegionStorage(info, folder,
//     ..., DataFixTypes.POI_CHUNK), PoiSection.Packed.CODEC, ...). The `folder` is the world's "poi"
//     directory; SimpleRegionStorage writes Anvil region files there (world/poi/r.<rx>.<rz>.mca).
//   - SectionStorage.writeChunk(ChunkPos, ops): for each section-Y in [minSection..maxSection] with a
//     present section, encode PoiSection.Packed and put it under the key Integer.toString(sectionY)
//     into a "Sections" map; the per-chunk root compound is
//       { "Sections": { "<sectionY>": <PoiSection.Packed>, ... }, "DataVersion": <int> }.
//     The section long key is SectionPos.asLong(chunkX, sectionY, chunkZ) (getKey), so a chunk column
//     collects every loaded section whose (x,z) match it.
//   - PoiSection.Packed.CODEC (RecordCodecBuilder): fields
//       "Valid"   -> Codec.BOOL.lenientOptionalFieldOf("Valid", false)
//       "Records" -> PoiRecord.Packed.CODEC.listOf().fieldOf("Records")
//   - PoiRecord.Packed.CODEC (RecordCodecBuilder): fields
//       "pos"          -> BlockPos.CODEC (INT_STREAM -> TAG_Int_Array [x,y,z])
//       "type"         -> RegistryFixedCodec(POINT_OF_INTEREST_TYPE) -> the poi-type id STRING
//                         (e.g. "minecraft:home", "minecraft:meeting")
//       "free_tickets" -> Codec.INT.optionalFieldOf("free_tickets", 0)
//
// The fork has no SimpleRegionStorage/SectionStorage type; POI reuses the SAME Anvil region IO the
// entities region uses (save/region: At/In/Open/Create/WriteSector/ReadSector), writing one per-chunk
// sector payload (a compression byte + gzip(NBT)) — the identical sector framing chunk_persist and
// persistence.go use. The runtime poiManager keys sections by SectionPos.asLong (poi.go); the save
// groups those by chunk (cx,cz) to produce one region cell per chunk column, exactly as SectionStorage
// does. On disk this is byte-compatible with the vanilla world/poi/ layout.

// poiDataVersion is SharedConstants.getCurrentVersion().dataVersion().version() for 26.2 (4903), the
// value SectionStorage.writeChunk writes into the "DataVersion" tag.
const poiDataVersion int32 = 4903

// poiDir is the PoiManager region folder (PoiManager's `folder` arg == world/poi). Region files live
// at world/poi/r.<rx>.<rz>.mca, the same r.x.z.mca naming the chunk + entities regions use.
const poiDir = "poi"

// poiCompressionGzip is the sector compression byte (gzip == 1), matching the entities-region framing
// (persistence.go entityCompressionGzip): a leading compression byte, then the gzip'd NBT payload.
const poiCompressionGzip = 1

// poiRecordDisk is PoiRecord.Packed: { pos, type, free_tickets }. Pos is a TAG_Int_Array (BlockPos),
// Type the poi-type id string, FreeTickets the ticket count (optional, default 0).
type poiRecordDisk struct {
	Pos         [3]int32 `nbt:"pos"`          // BlockPos.CODEC (INT_STREAM -> TAG_Int_Array)
	Type        string   `nbt:"type"`         // RegistryFixedCodec(POI_TYPE) -> the type id string
	FreeTickets int32    `nbt:"free_tickets"` // Codec.INT.optionalFieldOf("free_tickets", 0)
}

// poiSectionDisk is PoiSection.Packed: { Valid, Records }. Valid is the SectionTracker validity flag
// (defaults false); Records is the list of packed records in the section.
type poiSectionDisk struct {
	Valid   bool            `nbt:"Valid"`   // Codec.BOOL.lenientOptionalFieldOf("Valid", false)
	Records []poiRecordDisk `nbt:"Records"` // PoiRecord.Packed.CODEC.listOf().fieldOf("Records")
}

// poiChunkDisk is SectionStorage's per-chunk root compound: { Sections, DataVersion }. Sections maps
// the section-Y (as a decimal string) to its packed section — exactly Integer.toString(sectionY).
type poiChunkDisk struct {
	Sections    map[string]poiSectionDisk `nbt:"Sections"`
	DataVersion int32                     `nbt:"DataVersion"`
}

// encodePoiChunk builds the SectionStorage per-chunk root for chunk (cx,cz) from the poiManager's
// loaded sections: it walks every section whose SectionPos.asLong has this chunk's (x,z), packs its
// records (PoiSection::pack -> PoiRecord::pack), and keys them by section-Y string. Returns the root
// plus whether any section was present (an empty chunk writes no cell). Pure READ over the region-
// owned manager (called on the owner goroutine; only the value crosses the off-tick seam).
func encodePoiChunk(m *poiManager, cx, cz int) (poiChunkDisk, bool) {
	sections := map[string]poiSectionDisk{}
	for secLong, sec := range m.sections {
		sx, sy, sz := unpackSectionLong(secLong)
		if sx != cx || sz != cz {
			continue // a different chunk column
		}
		recs := make([]poiRecordDisk, 0, len(sec.records))
		for _, rec := range sec.records {
			recs = append(recs, poiRecordDisk{
				Pos:         [3]int32{int32(rec.pos.X), int32(rec.pos.Y), int32(rec.pos.Z)},
				Type:        rec.poiType.key, // the "minecraft:home"/"minecraft:meeting" id string
				FreeTickets: int32(rec.freeTickets),
			})
		}
		// SectionStorage.writeChunk only emits a section that exists; an EMPTY section (no records) is
		// still a present section in the runtime map, and vanilla packs it (Valid + empty Records). We
		// mirror that: every loaded section for this chunk is emitted. `Valid` is the SectionTracker
		// validity — the runtime poiSection has no separate validity flag (poi.go folds the byType
		// index into a linear scan and never marks a section invalid), so a loaded/populated section is
		// Valid==true (matches PoiSection's post-refresh state; a fresh-from-disk section is validated).
		sections[strconv.Itoa(sy)] = poiSectionDisk{Valid: true, Records: recs}
	}
	if len(sections) == 0 {
		return poiChunkDisk{}, false
	}
	return poiChunkDisk{Sections: sections, DataVersion: poiDataVersion}, true
}

// poiChunkKeys returns the distinct chunk (cx,cz) columns the manager has loaded sections for. The
// save iterates these to write one region cell per column (SectionStorage flushes per ChunkPos).
func poiChunkKeys(m *poiManager) [][2]int {
	seen := map[[2]int]struct{}{}
	var out [][2]int
	for secLong := range m.sections {
		sx, _, sz := unpackSectionLong(secLong)
		k := [2]int{sx, sz}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out
}

// poiChunkSnapshot is one column's finished payload: the chunk coords + the encoded root. savePoi
// takes these on the owner goroutine and does the region IO off it (only the value crosses the seam).
type poiChunkSnapshot struct {
	cx, cz int
	data   poiChunkDisk
}

// snapshotPoi collects every non-empty chunk column of the manager into immutable poiChunkSnapshots
// (pure owner-side read). savePoi writes them; keeping the encode on the owner and the IO off-tick is
// the same discipline saveEntities/savePlayer use.
func snapshotPoi(m *poiManager) []poiChunkSnapshot {
	var out []poiChunkSnapshot
	for _, k := range poiChunkKeys(m) {
		if data, ok := encodePoiChunk(m, k[0], k[1]); ok {
			out = append(out, poiChunkSnapshot{cx: k[0], cz: k[1], data: data})
		}
	}
	return out
}

// poiRegionPath returns the poi/r.<rx>.<rz>.mca path for the region containing chunk (cx,cz) and the
// in-region cell indices, reusing regionfile.At/In (the SAME naming the chunk + entities regions use).
func poiRegionPath(dir string, cx, cz int) (regionPath string, ix, iz int) {
	rx, rz := regionfile.At(cx, cz)
	ix, iz = regionfile.In(cx, cz)
	regionPath = filepath.Join(dir, poiDir, "r."+strconv.Itoa(rx)+"."+strconv.Itoa(rz)+".mca")
	return
}

// savePoi writes every non-empty chunk column of the POI manager into world/poi/r.x.z.mca via
// save/region (the SAME Anvil region IO the entities region uses). Each column's per-chunk root is
// encoded (SectionStorage.writeChunk shape) and written as a sector payload (compression byte +
// gzip(NBT)). The poi directory + region files are created on demand. Runs OFF the tick over the
// immutable []poiChunkSnapshot.
func savePoi(dir string, snaps []poiChunkSnapshot) error {
	if len(snaps) == 0 {
		return nil
	}
	poiRoot := filepath.Join(dir, poiDir)
	if err := os.MkdirAll(poiRoot, 0o755); err != nil {
		return err
	}
	// Group columns by region file so each .mca is opened once (regionfile.Region is not MT-safe and
	// re-opening per cell would thrash; SectionStorage likewise batches per region file).
	type cell struct{ ix, iz int }
	byRegion := map[string][]struct {
		cell cell
		data poiChunkDisk
	}{}
	for _, s := range snaps {
		path, ix, iz := poiRegionPath(dir, s.cx, s.cz)
		byRegion[path] = append(byRegion[path], struct {
			cell cell
			data poiChunkDisk
		}{cell{ix, iz}, s.data})
	}
	for path, cells := range byRegion {
		r, err := regionfile.Open(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return err
			}
			r, err = regionfile.Create(path)
			if err != nil {
				return err
			}
		}
		for _, c := range cells {
			payload, err := encodePoiSector(c.data)
			if err != nil {
				r.Close()
				return err
			}
			if err := r.WriteSector(c.cell.ix, c.cell.iz, payload); err != nil {
				r.Close()
				return err
			}
		}
		if err := r.Close(); err != nil {
			return err
		}
	}
	return nil
}

// loadPoi reconstructs a *poiManager from world/poi/*.mca by reading every region cell that carries a
// POI chunk root and rebuilding the section map. On a MISSING poi/ dir it returns (nil, false, nil) —
// a first boot has no POI region and starts with the lazy ensurePoiManager path. A real IO/parse error
// is surfaced. Mirrors SectionStorage.readChunk -> unpack: each Sections entry becomes a poiSection of
// unpacked poiRecords keyed by their section-relative short.
func loadPoi(dir string) (*poiManager, bool, error) {
	poiRoot := filepath.Join(dir, poiDir)
	entries, err := os.ReadDir(poiRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil // no poi/ dir yet: no POI (first boot)
		}
		return nil, false, err
	}
	m := newPoiManager()
	found := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		rx, rz, ok := parseRegionFileName(name)
		if !ok {
			continue // not an r.x.z.mca file
		}
		r, err := regionfile.Open(filepath.Join(poiRoot, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, false, err
		}
		// Walk every in-region cell (32x32) that carries data.
		for ix := 0; ix < 32; ix++ {
			for iz := 0; iz < 32; iz++ {
				data, err := r.ReadSector(ix, iz)
				if err != nil {
					if errors.Is(err, regionfile.ErrNoSector) || errors.Is(err, regionfile.ErrNoData) {
						continue // absent cell
					}
					r.Close()
					return nil, false, err
				}
				root, err := decodePoiSector(data)
				if err != nil {
					r.Close()
					return nil, false, err
				}
				cx := rx*32 + ix
				cz := rz*32 + iz
				if applyPoiChunk(m, cx, cz, root) {
					found = true
				}
			}
		}
		if err := r.Close(); err != nil {
			return nil, false, err
		}
	}
	if !found {
		// The dir existed but held no POI records (all-empty regions): treat as a hit only if any
		// section was rebuilt; otherwise return the fresh manager as a benign no-op miss.
		return nil, false, nil
	}
	return m, true, nil
}

// applyPoiChunk unpacks one chunk root (SectionStorage readChunk) into the manager: each Sections
// entry (keyed by section-Y string) becomes a poiSection whose records are re-keyed by their section-
// relative short (PoiSection::new from Packed). Returns whether any record was restored.
func applyPoiChunk(m *poiManager, cx, cz int, root poiChunkDisk) bool {
	any := false
	for yName, sec := range root.Sections {
		sy, err := strconv.Atoi(yName)
		if err != nil {
			continue // a non-numeric key is not a section (SectionStorage keys are Integer.toString)
		}
		secLong := sectionLongFromCoords(cx, sy, cz)
		ps := m.getOrCreate(secLong)
		for _, rd := range sec.Records {
			pt := poiTypeFromKey(rd.Type)
			if pt == nil {
				continue // an unknown/deferred poi type (e.g. a job site) — skipped, not fatal
			}
			pos := pk.Position{X: int(rd.Pos[0]), Y: int(rd.Pos[1]), Z: int(rd.Pos[2])}
			ps.records[sectionRelativePos(pos)] = &poiRecord{
				pos:         pos,
				poiType:     pt,
				freeTickets: int(rd.FreeTickets),
			}
			any = true
		}
	}
	if any {
		// A rebuilt manager's village-distance cache is empty; the first isVillage query recomputes it.
		m.villageDist = map[int64]int{}
	}
	return any
}

// poiTypeFromKey resolves a persisted poi-type id string back to the runtime *poiType (the inverse of
// poiRecordDisk.Type). Only the landed village types (HOME/MEETING) are known; a job-site key (the
// cite-deferred #acquirable_job_site members) resolves to nil and its record is skipped on load — the
// SAME deferral poi.go documents (they slot in behind poiTypeForState when villagers land).
func poiTypeFromKey(key string) *poiType {
	switch key {
	case poiTypeHome.key:
		return poiTypeHome
	case poiTypeMeeting.key:
		return poiTypeMeeting
	default:
		return nil
	}
}

// parseRegionFileName parses "r.<rx>.<rz>.mca" into (rx, rz). Returns ok=false for any other name.
func parseRegionFileName(name string) (rx, rz int, ok bool) {
	if !strings.HasPrefix(name, "r.") || !strings.HasSuffix(name, ".mca") {
		return 0, 0, false
	}
	mid := name[2 : len(name)-4] // "<rx>.<rz>"
	dot := -1
	for i := 0; i < len(mid); i++ {
		if mid[i] == '.' {
			dot = i
			break
		}
	}
	if dot < 0 {
		return 0, 0, false
	}
	x, err1 := strconv.Atoi(mid[:dot])
	z, err2 := strconv.Atoi(mid[dot+1:])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return x, z, true
}

// encodePoiSector encodes one chunk root as a region sector payload: a compression byte (gzip) then
// gzip(NBT) of the poiChunkDisk. The gzip trailer is flushed before the bytes return so the sector is
// a complete, re-readable stream (the same framing encodeEntitySector uses).
func encodePoiSector(root poiChunkDisk) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte(poiCompressionGzip)
	gz := gzip.NewWriter(&buf)
	if err := nbt.NewEncoder(gz).Encode(root, ""); err != nil {
		_ = gz.Close()
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// decodePoiSector inverts encodePoiSector: read the compression byte, gunzip, decode the poiChunkDisk.
func decodePoiSector(data []byte) (poiChunkDisk, error) {
	var root poiChunkDisk
	if len(data) < 1 {
		return root, errors.New("poi sector: empty payload")
	}
	if data[0] != poiCompressionGzip {
		return root, errors.New("poi sector: unknown compression")
	}
	gz, err := gzip.NewReader(bytes.NewReader(data[1:]))
	if err != nil {
		return root, err
	}
	defer gz.Close()
	if _, err := nbt.NewDecoder(gz).Decode(&root); err != nil {
		return root, err
	}
	return root, nil
}
