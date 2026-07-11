package level

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"os"
	"strconv"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/nbt"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"
)

type ChunkPos [2]int32

func (c ChunkPos) WriteTo(w io.Writer) (n int64, err error) {
	n, err = pk.Int(c[0]).WriteTo(w)
	if err != nil {
		return
	}
	n1, err := pk.Int(c[1]).WriteTo(w)
	return n + n1, err
}

func (c *ChunkPos) ReadFrom(r io.Reader) (n int64, err error) {
	var x, z pk.Int
	if n, err = x.ReadFrom(r); err != nil {
		return n, err
	}
	var n1 int64
	if n1, err = z.ReadFrom(r); err != nil {
		return n + n1, err
	}
	*c = ChunkPos{int32(x), int32(z)}
	return n + n1, nil
}

type Chunk struct {
	Sections    []Section
	HeightMaps  HeightMaps
	BlockEntity []BlockEntity
	Status      ChunkStatus

	// PostProcessFluids holds the LOCAL packed positions the aquifer flagged as unstable
	// fluid borders during fill (NoiseBasedChunkGenerator.fillFromNoise ->
	// ChunkAccess.markPosForPostProcessing). Each entry packs (localY<<8)|(localZ<<4)|localX
	// where localY = worldY - minY (0..secs*16-1; needs >16 bits, hence uint32). When the
	// chunk goes live the server runs FluidState.tick ONCE per entry
	// (LevelChunk.postProcessGeneration) — a one-shot kick that lets generated cave/aquifer
	// water flow into a bordering air gap, NOT a recurring sim. This is the marked-positions
	// equivalent of vanilla's per-section ShortList[] postProcessing array.
	PostProcessFluids []uint32
}

func EmptyChunk(secs int) *Chunk {
	sections := make([]Section, secs)
	for i := range sections {
		sections[i] = Section{
			BlockCount: 0,
			States:     NewStatesPaletteContainer(16*16*16, 0),
			Biomes:     NewBiomesPaletteContainer(4*4*4, 0),
		}
	}
	return &Chunk{
		Sections: sections,
		HeightMaps: HeightMaps{
			WorldSurfaceWG:         NewBitStorage(bits.Len(uint(secs)*16+1), 16*16, nil),
			WorldSurface:           NewBitStorage(bits.Len(uint(secs)*16+1), 16*16, nil),
			OceanFloorWG:           NewBitStorage(bits.Len(uint(secs)*16+1), 16*16, nil),
			OceanFloor:             NewBitStorage(bits.Len(uint(secs)*16+1), 16*16, nil),
			MotionBlocking:         NewBitStorage(bits.Len(uint(secs)*16+1), 16*16, nil),
			MotionBlockingNoLeaves: NewBitStorage(bits.Len(uint(secs)*16+1), 16*16, nil),
		},
		Status: StatusEmpty,
	}
}

// ChunkFromSave convert save.Chunk to level.Chunk.
func ChunkFromSave(c *save.Chunk) (*Chunk, error) {
	secs := len(c.Sections)
	sections := make([]Section, secs)
	for _, v := range c.Sections {
		i := int32(v.Y) - c.YPos
		if i < 0 || i >= int32(secs) {
			return nil, fmt.Errorf("section Y value %d out of bounds", v.Y)
		}
		var err error
		sections[i].States, err = readStatesPalette(v.BlockStates.Palette, v.BlockStates.Data)
		if err != nil {
			return nil, err
		}
		sections[i].BlockCount = countNoneAirBlocks(&sections[i])
		// FluidCount MUST be recomputed on load too: it is the chunk packet's second short
		// (LevelChunkSection.nonEmptyFluidCount). A reloaded section left at FluidCount 0 tells the
		// client the section is fluid-free even though its palette holds water, so the player does
		// NOT float/swim in reloaded water until a manual block update (the operator's "chunks con
		// agua quedan mal cuando se recarga un chunk guardado"). Generated chunks set this in gen;
		// reloaded chunks dropped it. Mirror countNoneAirBlocks. CITE LevelChunkSection.write.
		sections[i].FluidCount = CountFluidBlocks(&sections[i])
		sections[i].Biomes, err = readBiomesPalette(v.Biomes.Palette, v.Biomes.Data)
		if err != nil {
			return nil, err
		}
		sections[i].SkyLight = v.SkyLight
		sections[i].BlockLight = v.BlockLight
	}

	blockEntities := make([]BlockEntity, len(c.BlockEntities))
	for i, v := range c.BlockEntities {
		var tmp struct {
			ID string `nbt:"id"`
			X  int32  `nbt:"x"`
			Y  int32  `nbt:"y"`
			Z  int32  `nbt:"z"`
		}
		if err := v.Unmarshal(&tmp); err != nil {
			return nil, err
		}
		blockEntities[i].Data = v
		if x, z := int(tmp.X-c.XPos<<4), int(tmp.Z-c.ZPos<<4); !blockEntities[i].PackXZ(x, z) {
			return nil, errors.New("Packing a XZ(" + strconv.Itoa(x) + ", " + strconv.Itoa(z) + ") out of bound")
		}
		blockEntities[i].Y = int16(tmp.Y)
		blockEntities[i].Type = block.EntityTypes[tmp.ID]
	}

	// PostProcessing: decode the vanilla "PostProcessing" tag (ListTag of per-section ShortList)
	// back into the flat PostProcessFluids list the server's postProcessChunkFluids consumes. Each
	// short is ((localY_in_section<<8)|(localZ<<4)|localX) for section index `si`; the full local Y
	// is (si<<4)|in-section-Y. Without this the gen-time aquifer/carver marks would not survive a
	// chunk reload and cave/ravine water would stay static on revisit.
	var postProcessFluids []uint32
	if c.PostProcessing.Type == nbt.TagList && len(c.PostProcessing.Data) > 0 {
		var perSection [][]int16
		if err := c.PostProcessing.Unmarshal(&perSection); err == nil {
			for si, shorts := range perSection {
				for _, s := range shorts {
					us := uint16(s)
					inSecY := int(us>>8) & 0xF
					lz := int(us>>4) & 0xF
					lx := int(us) & 0xF
					localYFull := (si << 4) | inSecY
					postProcessFluids = append(postProcessFluids, uint32(localYFull)<<8|uint32(lz)<<4|uint32(lx))
				}
			}
		}
	}

	bitsForHeight := bits.Len( /* chunk height in blocks */ uint(secs)*16 + 1)
	return &Chunk{
		Sections:          sections,
		PostProcessFluids: postProcessFluids,
		HeightMaps: HeightMaps{
			WorldSurface:           NewBitStorage(bitsForHeight, 16*16, c.Heightmaps["WORLD_SURFACE"]),
			WorldSurfaceWG:         NewBitStorage(bitsForHeight, 16*16, c.Heightmaps["WORLD_SURFACE_WG"]),
			OceanFloorWG:           NewBitStorage(bitsForHeight, 16*16, c.Heightmaps["OCEAN_FLOOR_WG"]),
			OceanFloor:             NewBitStorage(bitsForHeight, 16*16, c.Heightmaps["OCEAN_FLOOR"]),
			MotionBlocking:         NewBitStorage(bitsForHeight, 16*16, c.Heightmaps["MOTION_BLOCKING"]),
			MotionBlockingNoLeaves: NewBitStorage(bitsForHeight, 16*16, c.Heightmaps["MOTION_BLOCKING_NO_LEAVES"]),
		},
		BlockEntity: blockEntities,
		Status:      ChunkStatus(c.Status),
	}, nil
}

func readStatesPalette(palette []save.BlockState, data []uint64) (paletteData *PaletteContainer[BlocksState], err error) {
	statePalette := make([]BlocksState, len(palette))
	for i, v := range palette {
		b, ok := block.FromID[v.Name]
		if !ok {
			return nil, fmt.Errorf("unknown block id: %v", v.Name)
		}
		if v.Properties.Data != nil {
			if err := v.Properties.Unmarshal(&b); err != nil {
				return nil, fmt.Errorf("unmarshal block properties fail: %v", err)
			}
		}
		s, ok := block.ToStateID[b]
		if !ok {
			return nil, fmt.Errorf("unknown block: %v", b)
		}
		statePalette[i] = s
	}
	paletteData = NewStatesPaletteContainerFromSave(16*16*16, data, statePalette)
	return
}

func readBiomesPalette(palette []save.BiomeState, data []uint64) (*PaletteContainer[BiomesState], error) {
	biomesRawPalette := make([]BiomesState, len(palette))
	for i, v := range palette {
		err := biomesRawPalette[i].UnmarshalText([]byte(v))
		if err != nil {
			return nil, err
		}
	}
	return NewBiomesPaletteContainerFromSave(4*4*4, data, biomesRawPalette), nil
}

func countNoneAirBlocks(sec *Section) (blockCount int16) {
	for i := 0; i < 16*16*16; i++ {
		b := sec.GetBlock(i)
		if !block.IsAir(b) {
			blockCount++
		}
	}
	return
}

// countFluidBlocks is the per-section fluid-block tally for the chunk packet's SECOND short
// (LevelChunkSection.nonEmptyFluidCount). The client reads it to decide whether the section
// holds fluid it must TICK/simulate; a section whose fluidCount is 0 is treated as fluid-free
// even if its palette contains water, so a player will NOT float in that water until a block
// update wakes the cell. Counting it here (mirroring countNoneAirBlocks for blocks) makes
// generated/aquifer water behave as a real fluid on the client immediately. Cite:
// net.minecraft.world.level.chunk.LevelChunkSection (recalcBlockCounts / write — the two shorts).
func CountFluidBlocks(sec *Section) (fluidCount int16) {
	for i := 0; i < 16*16*16; i++ {
		if block.IsFluid(sec.GetBlock(i)) {
			fluidCount++
		}
	}
	return
}

// ChunkToSave convert level.Chunk to save.Chunk
func ChunkToSave(c *Chunk, dst *save.Chunk) (err error) {
	secs := len(c.Sections)
	sections := make([]save.Section, secs)
	for i, v := range c.Sections {
		s := &sections[i]
		states := &s.BlockStates
		biomes := &s.Biomes
		s.Y = int8(int32(i) + dst.YPos)
		states.Palette, states.Data, err = writeStatesPalette(v.States)
		if err != nil {
			return
		}
		biomes.Palette, biomes.Data, err = writeBiomesPalette(v.Biomes)
		if err != nil {
			return
		}
		s.SkyLight = v.SkyLight
		s.BlockLight = v.BlockLight
	}
	dst.Sections = sections
	if dst.Heightmaps == nil {
		dst.Heightmaps = make(map[string][]uint64)
	}
	dst.Heightmaps["WORLD_SURFACE_WG"] = c.HeightMaps.WorldSurfaceWG.Raw()
	dst.Heightmaps["WORLD_SURFACE"] = c.HeightMaps.WorldSurface.Raw()
	dst.Heightmaps["OCEAN_FLOOR_WG"] = c.HeightMaps.OceanFloorWG.Raw()
	dst.Heightmaps["OCEAN_FLOOR"] = c.HeightMaps.OceanFloor.Raw()
	dst.Heightmaps["MOTION_BLOCKING"] = c.HeightMaps.MotionBlocking.Raw()
	dst.Heightmaps["MOTION_BLOCKING_NO_LEAVES"] = c.HeightMaps.MotionBlockingNoLeaves.Raw()
	dst.Status = string(c.Status)

	// PostProcessing: serialize the chunk's PostProcessFluids list to the vanilla on-disk
	// "PostProcessing" tag (SerializableChunkData.packOffsets) — a ListTag with one ShortList per
	// SECTION, each short = ((localY_in_section<<8)|(localZ<<4)|localX). Without this the
	// aquifer/carver fluid marks are LOST on the first save, so a reloaded chunk's cave/ravine water
	// never flows (the marks only existed in memory at gen time). c.PostProcessFluids packs the
	// FULL local Y (worldY-minY); split it back into (section, in-section Y). CITE
	// SerializableChunkData.packOffsets / ChunkAccess.getPostProcessing.
	if len(c.PostProcessFluids) > 0 {
		perSection := make([][]int16, secs)
		for i := range perSection {
			perSection[i] = []int16{}
		}
		for _, packed := range c.PostProcessFluids {
			localYFull := int(packed >> 8)
			lz := int((packed >> 4) & 0xF)
			lx := int(packed & 0xF)
			sec := localYFull >> 4
			if sec < 0 || sec >= secs {
				continue
			}
			short := int16((localYFull&15)<<8 | (lz << 4) | lx)
			perSection[sec] = append(perSection[sec], short)
		}
		doc, merr := nbt.Marshal(perSection)
		if merr != nil {
			return merr
		}
		dst.PostProcessing = nbt.RawMessage{Type: nbt.TagList, Data: doc[3:]}
	}

	// block_entities: serialize each BlockEntity as a full-metadata compound (the inverse of
	// ChunkFromSave's id/x/y/z decode). Ports BlockEntity.saveWithFullMetadata =
	// saveWithoutMetadata (the BE's own Data payload) + saveMetadata (adds "id" = the block-entity
	// type's registry id, and "x"/"y"/"z" ints = the ABSOLUTE world position). Without this,
	// generated chests/spawners (their {LootTable,LootTableSeed} / {SpawnData} NBT) were dropped on
	// every save and would not survive a chunk reload. CITE: net.minecraft.world.level.block.entity.
	// BlockEntity.saveWithFullMetadata / saveMetadata / addEntityType ("id" string + x,y,z ints).
	if n := len(c.BlockEntity); n > 0 {
		dst.BlockEntities = make([]nbt.RawMessage, 0, n)
		for i := range c.BlockEntity {
			be := c.BlockEntity[i]
			lx, lz := be.UnpackXZ()
			wx := int(dst.XPos)<<4 + lx
			wz := int(dst.ZPos)<<4 + lz
			id := ""
			if t := int(be.Type); t >= 0 && t < len(block.EntityList) {
				id = block.EntityList[t].ID()
			}
			full, merrr := blockEntityFullMetadata(be.Data, id, wx, int(be.Y), wz)
			if merrr != nil {
				return merrr
			}
			dst.BlockEntities = append(dst.BlockEntities, full)
		}
	}
	return
}

// blockEntityFullMetadata merges a block entity's bare Data compound payload (its
// saveWithoutMetadata fields — e.g. {LootTable,LootTableSeed} for a chest) with the metadata keys
// BlockEntity.saveMetadata writes ("id" string + "x","y","z" ints, absolute world coords), yielding
// the full on-disk compound ChunkFromSave reads back. The merge is byte-level: a TagCompound payload
// is a run of named tags terminated by a single TagEnd (0x00). The metadata tags are spliced in
// before that terminating byte. CITE: BlockEntity.saveWithFullMetadata = saveWithoutMetadata +
// saveMetadata (addEntityType "id" + putInt x/y/z).
func blockEntityFullMetadata(data nbt.RawMessage, id string, x, y, z int) (nbt.RawMessage, error) {
	// Marshal the metadata fields as their own compound document: [0x0A][0x00 0x00][fields...][0x00].
	metaDoc, err := nbt.Marshal(struct {
		ID string `nbt:"id"`
		X  int32  `nbt:"x"`
		Y  int32  `nbt:"y"`
		Z  int32  `nbt:"z"`
	}{ID: id, X: int32(x), Y: int32(y), Z: int32(z)})
	if err != nil {
		return nbt.RawMessage{}, fmt.Errorf("level: blockEntityFullMetadata: marshal metadata: %w", err)
	}
	// Strip the 3-byte root header ([0x0A][nameLen hi][nameLen lo]) -> [fields...][0x00].
	metaFields := metaDoc[3:]

	// The bare BE data payload is [fields...][0x00]; an empty/absent payload is just the terminator.
	bare := data.Data
	if len(bare) == 0 {
		// No own fields: the full compound is exactly the metadata fields.
		return nbt.RawMessage{Type: nbt.TagCompound, Data: append([]byte(nil), metaFields...)}, nil
	}
	// Drop the bare compound's terminating TagEnd, then append the metadata fields (which carry
	// their own terminating TagEnd). Result: [bareFields...][metaFields...][0x00].
	merged := make([]byte, 0, len(bare)-1+len(metaFields))
	merged = append(merged, bare[:len(bare)-1]...)
	merged = append(merged, metaFields...)
	return nbt.RawMessage{Type: nbt.TagCompound, Data: merged}, nil
}

func writeStatesPalette(paletteData *PaletteContainer[BlocksState]) (palette []save.BlockState, data []uint64, err error) {
	rawPalette := paletteData.palette.export()

	// GLOBAL/DIRECT palette guard (BUG-5): when a section's palette grew past the hash range
	// (bits >= 9 for blocks), the in-memory container switches to the globalPalette, whose
	// export() is EMPTY and whose data array holds DIRECT state ids (not palette indices). The
	// Anvil format has NO direct block-state format — block_states ALWAYS carries an explicit
	// palette that the data array indexes into. Writing the empty export + the direct-id data
	// produced a 0-length palette with a non-empty data array: a structurally invalid section
	// that, on reload, indexed garbage state ids → the client's IndexOutOfBounds in
	// PalettedContainer.read on the next chunk send (the respawn crash). Rebuild an explicit
	// palette from the distinct states actually present and RE-INDEX the data against it, exactly
	// as vanilla's Anvil PalettedContainer.write does for a heavily-edited section.
	if len(rawPalette) == 0 {
		const length = 16 * 16 * 16
		index := make(map[BlocksState]int)
		var distinct []BlocksState
		ids := make([]int, length)
		for i := 0; i < length; i++ {
			st := paletteData.Get(i)
			idx, ok := index[st]
			if !ok {
				idx = len(distinct)
				index[st] = idx
				distinct = append(distinct, st)
			}
			ids[i] = idx
		}
		rawPalette = distinct
		// Anvil block-state bits = max(4, ceil(log2(paletteLen))) (PalettedContainer's strategy
		// floors block storage at 4 bits). calcBitsPerValue on reload re-derives this from the
		// data length, so the BitStorage below must use the SAME width.
		storageBits := 4
		for (1 << storageBits) < len(distinct) {
			storageBits++
		}
		bs := NewBitStorage(storageBits, length, nil)
		for i := 0; i < length; i++ {
			bs.Set(i, ids[i])
		}
		palette, err = encodeStatePalette(rawPalette)
		if err != nil {
			return nil, nil, err
		}
		raw := bs.Raw()
		data = make([]uint64, len(raw))
		copy(data, raw)
		return palette, data, nil
	}

	palette, err = encodeStatePalette(rawPalette)
	if err != nil {
		return nil, nil, err
	}

	data = make([]uint64, len(paletteData.data.Raw()))
	copy(data, paletteData.data.Raw())
	return palette, data, nil
}

// encodeStatePalette converts a slice of state ids into the Anvil save.BlockState palette
// entries (Name + Properties), shared by the local-palette and rebuilt-global-palette paths.
func encodeStatePalette(rawPalette []BlocksState) ([]save.BlockState, error) {
	palette := make([]save.BlockState, len(rawPalette))
	var buffer bytes.Buffer
	for i, v := range rawPalette {
		b := block.StateList[v]
		palette[i].Name = b.ID()

		buffer.Reset()
		if err := nbt.NewEncoder(&buffer).Encode(b, ""); err != nil {
			return nil, err
		}
		if _, err := nbt.NewDecoder(&buffer).Decode(&palette[i].Properties); err != nil {
			return nil, err
		}
	}
	return palette, nil
}

func writeBiomesPalette(paletteData *PaletteContainer[BiomesState]) (palette []save.BiomeState, data []uint64, err error) {
	rawPalette := paletteData.palette.export()
	palette = make([]save.BiomeState, len(rawPalette))

	var biomeID []byte
	for i, v := range rawPalette {
		biomeID, err = v.MarshalText()
		if err != nil {
			return
		}
		palette[i] = save.BiomeState(biomeID)
	}

	data = make([]uint64, len(paletteData.data.Raw()))
	copy(data, paletteData.data.Raw())
	return
}

func (c *Chunk) WriteTo(w io.Writer) (int64, error) {
	data, err := c.Data()
	if err != nil {
		return 0, err
	}
	light := lightData{
		SkyLightMask:   make(pk.BitSet, (16*16*16-1)>>6+1),
		BlockLightMask: make(pk.BitSet, (16*16*16-1)>>6+1),
		SkyLight:       []pk.ByteArray{},
		BlockLight:     []pk.ByteArray{},
	}
	for i, v := range c.Sections {
		if v.SkyLight != nil {
			light.SkyLightMask.Set(i, true)
			light.SkyLight = append(light.SkyLight, v.SkyLight)
		}
		if v.BlockLight != nil {
			light.BlockLightMask.Set(i, true)
			light.BlockLight = append(light.BlockLight, v.BlockLight)
		}
	}

	// Protocol 774+: heightmaps are serialized as VarInt-typed array entries.
	// Vanilla sends ONLY the 3 Usage.CLIENT (sendToClient) heightmaps —
	// WORLD_SURFACE (1), MOTION_BLOCKING (4), MOTION_BLOCKING_NO_LEAVES (5).
	// The WORLDGEN/LIVE_WORLD ids — WORLD_SURFACE_WG (0), OCEAN_FLOOR_WG (2),
	// OCEAN_FLOOR (3) — are NOT sendToClient and must be dropped from the wire.
	var hmEntries []heightMapEntry
	if bs := c.HeightMaps.WorldSurface; bs != nil {
		hmEntries = append(hmEntries, heightMapEntry{Type: 1, Data: bs.Raw()})
	}
	if bs := c.HeightMaps.MotionBlocking; bs != nil {
		hmEntries = append(hmEntries, heightMapEntry{Type: 4, Data: bs.Raw()})
	}
	if bs := c.HeightMaps.MotionBlockingNoLeaves; bs != nil {
		hmEntries = append(hmEntries, heightMapEntry{Type: 5, Data: bs.Raw()})
	}

	return pk.Tuple{
		pk.Array(hmEntries),
		pk.ByteArray(data),
		pk.Array(c.BlockEntity),
		&light,
	}.WriteTo(w)
}

func (c *Chunk) ReadFrom(r io.Reader) (int64, error) {
	var (
		hmEntries []heightMapEntry
		data      pk.ByteArray
	)

	n, err := pk.Tuple{
		pk.Array(&hmEntries),
		&data,
		pk.Array(&c.BlockEntity),
		&lightData{
			SkyLightMask:   make(pk.BitSet, (16*16*16-1)>>6+1),
			BlockLightMask: make(pk.BitSet, (16*16*16-1)>>6+1),
			SkyLight:       []pk.ByteArray{},
			BlockLight:     []pk.ByteArray{},
		},
	}.ReadFrom(r)
	if err != nil {
		return n, err
	}

	if os.Getenv("GOMC_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "[GOMC_DEBUG] Chunk ReadFrom: %d heightmap entries, data=%d bytes, %d block entities\n",
			len(hmEntries), len(data), len(c.BlockEntity))
		for i, entry := range hmEntries {
			fmt.Fprintf(os.Stderr, "[GOMC_DEBUG]   heightmap[%d]: type=%d, longs=%d\n", i, entry.Type, len(entry.Data))
		}
		if len(data) > 20 {
			fmt.Fprintf(os.Stderr, "[GOMC_DEBUG]   data first 20 bytes: %x\n", data[:20])
		}
	}

	bitsForHeight := bits.Len( /* chunk height in blocks */ uint(len(c.Sections))*16 + 1)
	for _, entry := range hmEntries {
		bs := NewBitStorage(bitsForHeight, 16*16, entry.Data)
		switch entry.Type {
		case 0:
			c.HeightMaps.WorldSurfaceWG = bs
		case 1:
			c.HeightMaps.WorldSurface = bs
		case 2:
			c.HeightMaps.OceanFloorWG = bs
		case 3:
			c.HeightMaps.OceanFloor = bs
		case 4:
			c.HeightMaps.MotionBlocking = bs
		case 5:
			c.HeightMaps.MotionBlockingNoLeaves = bs
		}
	}

	err = c.PutData(data)
	return n, err
}

func (c *Chunk) Data() ([]byte, error) {
	var buff bytes.Buffer
	for i := range c.Sections {
		_, err := c.Sections[i].WriteTo(&buff)
		if err != nil {
			return nil, err
		}
	}
	return buff.Bytes(), nil
}

func (c *Chunk) PutData(data []byte) error {
	r := bytes.NewReader(data)
	for i := range c.Sections {
		_, err := c.Sections[i].ReadFrom(r)
		if err != nil {
			return err
		}
	}
	return nil
}

type HeightMaps struct {
	WorldSurfaceWG         *BitStorage // test = NOT_AIR
	WorldSurface           *BitStorage // test = NOT_AIR
	OceanFloorWG           *BitStorage // test = MATERIAL_MOTION_BLOCKING
	OceanFloor             *BitStorage // test = MATERIAL_MOTION_BLOCKING
	MotionBlocking         *BitStorage // test = BlocksMotion or isFluid
	MotionBlockingNoLeaves *BitStorage // test = BlocksMotion or isFluid
}

// heightmapOpaque is the per-type isOpaque predicate the four LIVE_WORLD/CLIENT heightmaps
// carry (Heightmap$Types.isOpaque). It is the exact 1:1 of the four predicates:
//
//   - WORLD_SURFACE             = NOT_AIR                    (!state.isAir())
//   - OCEAN_FLOOR (LIVE_WORLD)  = MATERIAL_MOTION_BLOCKING   (state.blocksMotion())
//   - MOTION_BLOCKING           = state.blocksMotion() || !state.getFluidState().isEmpty()
//   - MOTION_BLOCKING_NO_LEAVES = MOTION_BLOCKING && !(getBlock() instanceof LeavesBlock)
//
// CITE: Heightmap$Types (NOT_AIR / MATERIAL_MOTION_BLOCKING / lambda$static$0 / lambda$static$1).
func heightmapOpaqueWorldSurface(st block.StateID) bool { return !block.IsAir(st) }
func heightmapOpaqueOceanFloor(st block.StateID) bool   { return block.BlocksMotion(st) }
func heightmapOpaqueMotionBlocking(st block.StateID) bool {
	return block.BlocksMotion(st) || block.HasFluidState(st)
}
func heightmapOpaqueMotionBlockingNoLeaves(st block.StateID) bool {
	return heightmapOpaqueMotionBlocking(st) && !block.IsLeavesBlockInstance(st)
}

// columnBlockAt reads the block state at local column (lx,lz) and ABSOLUTE world Y, or Air
// when y is outside the chunk's section range. Used by the live heightmap update's downward
// re-scan closure. minY is the dimension floor.
func (c *Chunk) columnBlockAt(lx, y, lz, minY int) block.StateID {
	sec := (y - minY) >> 4
	if sec < 0 || sec >= len(c.Sections) {
		return block.ToStateID[block.Air{}]
	}
	local := (y&15)<<8 | (lz&15)<<4 | (lx & 15)
	return c.Sections[sec].GetBlock(local)
}

// UpdateHeightmaps ports the four Heightmap.update calls LevelChunk.setBlockState makes on
// every live block set, in the SAME order: MOTION_BLOCKING, MOTION_BLOCKING_NO_LEAVES,
// OCEAN_FLOOR, WORLD_SURFACE. x,z are LOCAL column coords (0..15), y ABSOLUTE world Y, minY
// the dimension floor, newState the just-set block. It delegates each map to the canonical
// HeightmapUpdate (the 1:1 Heightmap.update port), passing that map's Heightmap$Types
// predicate for both the NEW state and the downward-rescan closure. Wiring this at the sole
// live block mutator keeps the heightmaps truthful after players build/break. CITE:
// LevelChunk.setBlockState (heightmaps.get(MOTION_BLOCKING/.../WORLD_SURFACE).update(...)).
func (c *Chunk) UpdateHeightmaps(x, y, z, minY int, newState block.StateID) {
	at := func(yy int) block.StateID { return c.columnBlockAt(x, yy, z, minY) }
	if bs := c.HeightMaps.MotionBlocking; bs != nil {
		HeightmapUpdate(bs, x, y, z, minY, heightmapOpaqueMotionBlocking(newState),
			func(yy int) bool { return heightmapOpaqueMotionBlocking(at(yy)) })
	}
	if bs := c.HeightMaps.MotionBlockingNoLeaves; bs != nil {
		HeightmapUpdate(bs, x, y, z, minY, heightmapOpaqueMotionBlockingNoLeaves(newState),
			func(yy int) bool { return heightmapOpaqueMotionBlockingNoLeaves(at(yy)) })
	}
	if bs := c.HeightMaps.OceanFloor; bs != nil {
		HeightmapUpdate(bs, x, y, z, minY, heightmapOpaqueOceanFloor(newState),
			func(yy int) bool { return heightmapOpaqueOceanFloor(at(yy)) })
	}
	if bs := c.HeightMaps.WorldSurface; bs != nil {
		HeightmapUpdate(bs, x, y, z, minY, heightmapOpaqueWorldSurface(newState),
			func(yy int) bool { return heightmapOpaqueWorldSurface(at(yy)) })
	}
}

// heightMapEntry is a single heightmap in the protocol 774+ chunk format.
// Each entry has a type enum and a VarInt-prefixed array of int64 (packed data).
type heightMapEntry struct {
	Type int32    // 0=world_surface_wg, 1=world_surface, 2=ocean_floor_wg, 3=ocean_floor, 4=motion_blocking, 5=motion_blocking_no_leaves
	Data []uint64 // packed heightmap data
}

func (e heightMapEntry) WriteTo(w io.Writer) (int64, error) {
	longs := make([]pk.Long, len(e.Data))
	for i, v := range e.Data {
		longs[i] = pk.Long(v)
	}
	return pk.Tuple{
		pk.VarInt(e.Type),
		pk.Array(longs),
	}.WriteTo(w)
}

func (e *heightMapEntry) ReadFrom(r io.Reader) (int64, error) {
	var longs []pk.Long
	n, err := pk.Tuple{
		(*pk.VarInt)(&e.Type),
		pk.Array(&longs),
	}.ReadFrom(r)
	if err == nil {
		e.Data = make([]uint64, len(longs))
		for i, v := range longs {
			e.Data[i] = uint64(v)
		}
	}
	return n, err
}

type BlockEntity struct {
	XZ   int8
	Y    int16
	Type block.EntityType
	Data nbt.RawMessage
}

func (b BlockEntity) UnpackXZ() (X, Z int) {
	return int((uint8(b.XZ) >> 4) & 0xF), int(uint8(b.XZ) & 0xF)
}

func (b *BlockEntity) PackXZ(X, Z int) bool {
	if X > 0xF || Z > 0xF || X < 0 || Z < 0 {
		return false
	}
	b.XZ = int8(X<<4 | Z)
	return true
}

func (b BlockEntity) WriteTo(w io.Writer) (n int64, err error) {
	return pk.Tuple{
		pk.Byte(b.XZ),
		pk.Short(b.Y),
		pk.VarInt(b.Type),
		pk.NBT(b.Data),
	}.WriteTo(w)
}

func (b *BlockEntity) ReadFrom(r io.Reader) (n int64, err error) {
	return pk.Tuple{
		(*pk.Byte)(&b.XZ),
		(*pk.Short)(&b.Y),
		(*pk.VarInt)(&b.Type),
		pk.NBT(&b.Data),
	}.ReadFrom(r)
}

type Section struct {
	BlockCount int16
	// FluidCount is the per-section fluid-block count. It is 0 for an
	// all-stone superflat, but the SHORT must ALWAYS be present on the wire:
	// vanilla LevelChunkSection.write emits two shorts (nonEmptyBlockCount
	// then fluidCount) before the states container. The fork previously wrote
	// only one short — the confirmed proto-776 stripes/void byte-misalignment.
	FluidCount int16
	States     *PaletteContainer[BlocksState]
	Biomes     *PaletteContainer[BiomesState]
	// Half a byte per light value.
	// Could be nil if not exist
	SkyLight   []byte // len() == 2048
	BlockLight []byte // len() == 2048
}

func (s *Section) GetBlock(i int) BlocksState {
	return s.States.Get(i)
}

// HasRandomlyTicking approximates net.minecraft.world.level.chunk.LevelChunkSection.
// isRandomlyTickingBlocks() (`this.tickingBlockCount > 0`). Vanilla maintains a per-section
// tickingBlockCount incremented/decremented as randomly-ticking blocks are set/cleared
// (LevelChunkSection.setBlockState / recalcBlockCounts); this repo does not yet track that counter,
// so this scans the section's PALETTE (not all 4096 cells — the palette holds only the DISTINCT
// states present, typically a handful) for any block whose BlockStateBase.isRandomlyTicking() is
// true. Result-identical to `tickingBlockCount > 0` (a section contains a randomly-ticking block iff
// the driver should sample it); the per-section counter is a perf follow-up, not a correctness gap.
// CITE: LevelChunkSection.isRandomlyTickingBlocks (tickingBlockCount > 0); the tickingBlockCount
// counter (LevelChunkSection.recalcBlockCounts) is the deferred optimization.
func (s *Section) HasRandomlyTicking() bool {
	for _, st := range s.States.Palette() {
		if block.IsRandomlyTicking(st) {
			return true
		}
	}
	return false
}

func (s *Section) SetBlock(i int, v BlocksState) {
	if !block.IsAir(s.States.Get(i)) {
		s.BlockCount--
	}
	if !block.IsAir(v) {
		s.BlockCount++
	}
	s.States.Set(i, v)
}

func (s *Section) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{
		pk.Short(s.BlockCount),
		pk.Short(s.FluidCount), // second short — vanilla LevelChunkSection.write
		s.States,
		s.Biomes,
	}.WriteTo(w)
}

func (s *Section) ReadFrom(r io.Reader) (int64, error) {
	return pk.Tuple{
		(*pk.Short)(&s.BlockCount),
		(*pk.Short)(&s.FluidCount), // symmetric read of the second short
		s.States,
		s.Biomes,
	}.ReadFrom(r)
}

type lightData struct {
	SkyLightMask   pk.BitSet
	BlockLightMask pk.BitSet
	SkyLight       []pk.ByteArray
	BlockLight     []pk.ByteArray
}

func bitSetRev(set pk.BitSet) pk.BitSet {
	rev := make(pk.BitSet, len(set))
	for i := range rev {
		rev[i] = ^set[i]
	}
	return rev
}

func (l *lightData) WriteTo(w io.Writer) (int64, error) {
	return pk.Tuple{
		l.SkyLightMask,
		l.BlockLightMask,
		bitSetRev(l.SkyLightMask),
		bitSetRev(l.BlockLightMask),
		pk.Array(l.SkyLight),
		pk.Array(l.BlockLight),
	}.WriteTo(w)
}

func (l *lightData) ReadFrom(r io.Reader) (int64, error) {
	var RevSkyLightMask, RevBlockLightMask pk.BitSet
	return pk.Tuple{
		&l.SkyLightMask,
		&l.BlockLightMask,
		&RevSkyLightMask,
		&RevBlockLightMask,
		pk.Array(&l.SkyLight),
		pk.Array(&l.BlockLight),
	}.ReadFrom(r)
}
