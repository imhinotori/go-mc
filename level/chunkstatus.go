package level

import "strings"

type ChunkStatus string

// The 26.2 ChunkStatus chain, in dependency order, EXACTLY as registered in
// net.minecraft.world.level.chunk.status.ChunkStatus's static initializer:
// EMPTY, STRUCTURE_STARTS, STRUCTURE_REFERENCES, BIOMES, NOISE, SURFACE, CARVERS,
// FEATURES, INITIALIZE_LIGHT, LIGHT, SPAWN, FULL. Each constant's value is the BARE
// registry name (the String passed to ChunkStatus.register). The pre-1.18 liquid_carvers
// and heightmaps statuses no longer exist in 26.2 and are omitted; initialize_light was
// added (it runs before LIGHT). CITE: ChunkStatus.<clinit> register(...) call order.
const (
	StatusEmpty               ChunkStatus = "empty"
	StatusStructureStarts     ChunkStatus = "structure_starts"
	StatusStructureReferences ChunkStatus = "structure_references"
	StatusBiomes              ChunkStatus = "biomes"
	StatusNoise               ChunkStatus = "noise"
	StatusSurface             ChunkStatus = "surface"
	StatusCarvers             ChunkStatus = "carvers"
	StatusFeatures            ChunkStatus = "features"
	StatusInitializeLight     ChunkStatus = "initialize_light"
	StatusLight               ChunkStatus = "light"
	StatusSpawn               ChunkStatus = "spawn"
	StatusFull                ChunkStatus = "full"
)

// DiskName is the on-disk `Status` string: the NAMESPACED resource location
// (CHUNK_STATUS.getKey(status).toString() == "minecraft:<name>"). SerializableChunkData.write
// stores the status via the DefaultedRegistry key, so the disk form always carries the
// minecraft: namespace. CITE: SerializableChunkData.write (putString "Status",
// BuiltInRegistries.CHUNK_STATUS.getKey(chunkStatus).toString()).
func (s ChunkStatus) DiskName() string {
	if strings.ContainsRune(string(s), ':') {
		return string(s) // already namespaced (defensive)
	}
	return "minecraft:" + string(s)
}

// ChunkStatusFromDisk parses the on-disk `Status` string back into a ChunkStatus, stripping the
// minecraft: namespace so the bare-name enum used throughout worldgen matches. An unknown / empty
// string resolves to StatusEmpty, mirroring the vanilla CODEC read that defaults to
// ChunkStatus.EMPTY on an unresolved key. CITE: SerializableChunkData.getChunkStatusFromTag
// (Status via ChunkStatus.CODEC, orElse(EMPTY)).
func ChunkStatusFromDisk(raw string) ChunkStatus {
	if raw == "" {
		return StatusEmpty
	}
	name := raw
	if i := strings.IndexRune(raw, ':'); i >= 0 {
		name = raw[i+1:]
	}
	return ChunkStatus(name)
}
