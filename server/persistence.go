package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/nbt"
	"github.com/imhinotori/sulfur/save"
	"github.com/imhinotori/sulfur/save/region"

	"github.com/google/uuid"
)

// persistence.go is ENT-06: Anvil persistence for player and entity state, REUSING the fork's
// save / save/region wholesale (NOT a new format). The shape mirrors the Phase-4 chunk-load
// discipline: the tick takes an IMMUTABLE VALUE snapshot on the owner goroutine (snapshotPlayer
// — no live tick-owned pointer crosses the boundary, TICK-05 / T-6-15), then the IO (NBT encode
// + gzip / region write) runs OFF the tick. A missing/corrupt .dat falls back to spawn defaults
// (loadPlayer ok=false) so a first join never crashes (T-6-16).
//
// CRITICAL — disk-NBT is NOT the wire component codec (Pitfall 6): a player's inventory persists
// as save.Item (Count/Slot/string-ID, disk NBT) — the wire component SlotData (Plan 06-05) is
// NEVER written to disk and the disk NBT is NEVER read as wire. snapshotPlayer translates the
// tick-owned component-slot inventory into []save.Item via the item registry (numeric wire id ->
// "minecraft:<name>" string id) so the two codecs stay cleanly separated.

// playerDataDir / entitiesDir are the per-world subdirectories the vanilla layout uses:
// world/playerdata/<uuid>.dat for players, world/entities/r.x.z.mca for the parallel modern
// entity region (the chunk's legacy in-chunk Entities slot is NOT the modern path).
const (
	playerDataDir = "playerdata"
	entitiesDir   = "entities"
)

// entityCompressionGzip is the sector compression byte the entities region writes (gzip == 1),
// matching save.Chunk.Data's compression-byte-prefixed sector framing so the region file stays
// a faithful Anvil region (a leading compression byte then the compressed NBT payload).
const entityCompressionGzip = 1

// snapshotPlayer takes an IMMUTABLE value copy of the tick-owned player state for off-tick IO
// (TICK-05 / T-6-15). It runs on the OWNER goroutine; every field copied is a plain value
// (position, health, food, saturation, held slot) or is translated into a fresh []save.Item
// (the inventory) so NO live tick-owned pointer crosses into the off-tick IO. Mutating the live
// player after this returns cannot change the returned snapshot — TestSnapshotIsValueCopy proves
// it. The returned save.PlayerData is what savePlayer encodes; the inventory is DISK NBT
// (save.Item), never the wire component codec (Pitfall 6).
func snapshotPlayer(p *tickPlayer) save.PlayerData {
	data := save.PlayerData{
		Dimension:           overworldDimensionName,
		Pos:                 [3]float64{p.x, p.y, p.z},
		Rotation:            [2]float32{p.yaw, p.pitch},
		Health:              p.health,
		FoodLevel:           p.food,
		FoodSaturationLevel: p.saturation,
		PlayerGameType:      gameModeSurvival,
	}
	if p.onGround {
		data.OnGround = 1
	}
	if p.inventory != nil {
		data.SelectedItemSlot = int32(p.inventory.heldSlot)
		data.Inventory = inventoryToItems(p.inventory)
	}
	return data
}

// inventoryToItems translates the tick-owned component-slot inventory into the disk-NBT
// []save.Item form (Pitfall 6: disk NBT, NOT the wire component codec). Only populated slots
// (Count > 0) are persisted; the numeric wire item id is resolved to its "minecraft:<name>"
// string id via the item registry (item.ByID). The wire component bytes (RawComponents) are
// NOT written to disk — v1 persists Count/Slot/ID, which is enough to round-trip a visible item;
// the legacy save.Item.Tag NBT slot is left empty (a later plan maps components to disk tags).
func inventoryToItems(inv *Inventory) []save.Item {
	var items []save.Item
	for i, slot := range inv.slots {
		if slot.Count <= 0 {
			continue // empty slot: not persisted
		}
		items = append(items, save.Item{
			Slot:  byte(i),
			Count: byte(slot.Count),
			ID:    itemName(int32(slot.ItemID)),
		})
	}
	return items
}

// itemName resolves a numeric wire item id to its namespaced disk id ("minecraft:<name>"),
// reusing the generated item registry (data/item.ByID). An unknown id falls back to
// "minecraft:air" so a forged/out-of-range id never produces an invalid disk record.
func itemName(id int32) string {
	if it, ok := item.ByID[item.ID(id)]; ok {
		return "minecraft:" + it.Name
	}
	return "minecraft:air"
}

// savePlayer writes a player snapshot to world/playerdata/<uuid>.dat as gzip-wrapped NBT (the
// vanilla path). It runs OFF the tick over the immutable snapshot (snapshotPlayer) so the disk
// IO never touches live tick-owned state. The playerdata directory is created on demand. The
// gzip writer is explicitly closed (flushing its trailer) BEFORE the bytes are written, so the
// .dat is a complete, re-readable gzip stream.
func savePlayer(dir string, id uuid.UUID, data save.PlayerData) error {
	pdDir := filepath.Join(dir, playerDataDir)
	if err := os.MkdirAll(pdDir, 0o755); err != nil {
		return err
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if err := nbt.NewEncoder(gz).Encode(data, ""); err != nil {
		_ = gz.Close()
		return err
	}
	if err := gz.Close(); err != nil { // flush the gzip trailer before writing
		return err
	}

	path := filepath.Join(pdDir, id.String()+".dat")
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// loadPlayer reads world/playerdata/<uuid>.dat back into a save.PlayerData. On a MISSING file
// OR any read/parse error (corrupt gzip, malformed NBT) it returns (spawn defaults, false) and
// NEVER crashes the join (T-6-16) — the .dat is server-written, so corruption is low-risk, and a
// first join legitimately has no file. A clean read returns (data, true). The defaults are a
// full survival player at the world spawn so a defaulted player is immediately playable.
func loadPlayer(dir string, id uuid.UUID) (save.PlayerData, bool) {
	path := filepath.Join(dir, playerDataDir, id.String()+".dat")
	f, err := os.Open(path)
	if err != nil {
		return defaultPlayerData(), false // absent (or unreadable): spawn defaults, no crash
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return defaultPlayerData(), false // not a gzip stream: corrupt -> defaults
	}
	defer gz.Close()

	data, err := save.ReadPlayerData(gz)
	if err != nil {
		return defaultPlayerData(), false // malformed NBT: corrupt -> defaults
	}
	return data, true
}

// RunSaveLoop is the OFF-TICK persistence consumer for player leave-snapshots (ENT-06). It
// drains the leaveSnapshots channel (armed by SetSaveSink) and writes each immutable snapshot to
// world/playerdata/<uuid>.dat OFF the tick goroutine, so a leave never blocks the tick on disk
// IO and no live tick-owned state is read off-thread (TICK-05 / T-6-15). It returns when ctx is
// cancelled. main() runs it in its own goroutine; if no save sink was wired (leaveSnapshots nil)
// it simply blocks on ctx.Done with nothing to drain.
func (t *TickLoop) RunSaveLoop(ctx context.Context, worldDir string) {
	for {
		select {
		case <-ctx.Done():
			return
		case snap := <-t.leaveSnapshots:
			if err := savePlayer(worldDir, snap.uuid, snap.data); err != nil {
				log.Printf("save player %s: %v", snap.uuid, err)
			}
		}
	}
}

// defaultPlayerData is the spawn-default save.PlayerData a missing/corrupt .dat falls back to: a
// full survival player (maxHealth / maxFood / defaultSaturation) at the world spawn column. It
// mirrors the join-bootstrap defaults so a defaulted player and a freshly-bootstrapped one are
// indistinguishable.
func defaultPlayerData() save.PlayerData {
	return save.PlayerData{
		Dimension:           overworldDimensionName,
		Health:              maxHealth,
		FoodLevel:           maxFood,
		FoodSaturationLevel: defaultSaturation,
		PlayerGameType:      gameModeSurvival,
	}
}

// entityRegionPath returns the entities/r.<rx>.<rz>.mca path for the region containing the chunk
// column, reusing region.At (the SAME naming the chunk region uses, under a parallel entities/
// subdir). The directory is created by saveEntities on demand.
func entityRegionPath(dir string, pos level.ChunkPos) (regionPath string, ix, iz int) {
	cx, cz := int(pos[0]), int(pos[1])
	rx, rz := region.At(cx, cz)
	ix, iz = region.In(cx, cz)
	regionPath = filepath.Join(dir, entitiesDir, "r."+strconv.Itoa(rx)+"."+strconv.Itoa(rz)+".mca")
	return
}

// entityRegion is the NBT root the entities region stores per cell: a list of save.Entities. It
// wraps the slice so the region sector holds a single compound (the modern entities region cell
// is a compound with an "Entities" list), mirroring the vanilla entity-region layout.
type entityRegion struct {
	Entities []save.Entities `nbt:"Entities"`
}

// saveEntities writes an entity snapshot for a chunk column into the parallel entities/r.x.z.mca
// region via save/region (ReadSector/WriteSector) — REUSING the Anvil region IO, NOT a new
// format (the chunk's legacy in-chunk Entities slot is not the modern path). The sector payload
// is a compression byte (gzip) + gzip(NBT) so it matches the chunk region's sector framing. The
// entities directory and region file are created on demand; an existing region is opened and the
// cell overwritten. Runs OFF the tick over the immutable []save.Entities snapshot.
func saveEntities(dir string, pos level.ChunkPos, ents []save.Entities) error {
	regionPath, ix, iz := entityRegionPath(dir, pos)
	if err := os.MkdirAll(filepath.Dir(regionPath), 0o755); err != nil {
		return err
	}

	// Open the region (or create it if this is the first write to this region file).
	r, err := region.Open(regionPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		r, err = region.Create(regionPath)
		if err != nil {
			return err
		}
	}
	defer r.Close()

	payload, err := encodeEntitySector(ents)
	if err != nil {
		return err
	}
	return r.WriteSector(ix, iz, payload)
}

// loadEntities reads the entity snapshot for a chunk column back from entities/r.x.z.mca via
// save/region. It returns:
//
//	(nil, false, nil) -> miss (no region file / ErrNoSector / ErrNoData): never saved
//	(nil, false, err) -> a real IO/parse error (corrupt sector)
//	(ents, true, nil) -> hit
//
// A fresh Region is opened and closed per call (region.Region is Not MT-Safe), mirroring the
// chunk-load discipline (world/worker.tryRegion). Runs OFF the tick.
func loadEntities(dir string, pos level.ChunkPos) ([]save.Entities, bool, error) {
	regionPath, ix, iz := entityRegionPath(dir, pos)

	r, err := region.Open(regionPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil // no region file yet -> miss
		}
		return nil, false, err
	}
	defer r.Close()

	data, err := r.ReadSector(ix, iz)
	if err != nil {
		if errors.Is(err, region.ErrNoSector) || errors.Is(err, region.ErrNoData) {
			return nil, false, nil // not-yet-saved cell -> miss
		}
		return nil, false, err // corrupt/real error
	}

	ents, err := decodeEntitySector(data)
	if err != nil {
		return nil, false, err
	}
	return ents, true, nil
}

// encodeEntitySector encodes the entities list as a region sector payload: a compression byte
// (gzip) followed by gzip(NBT) of the entityRegion compound. The gzip writer is closed (trailer
// flushed) before the bytes are returned so the sector is a complete, re-readable stream.
func encodeEntitySector(ents []save.Entities) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte(entityCompressionGzip)
	gz := gzip.NewWriter(&buf)
	if err := nbt.NewEncoder(gz).Encode(entityRegion{Entities: ents}, ""); err != nil {
		_ = gz.Close()
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// decodeEntitySector inverts encodeEntitySector: it reads the compression byte, decompresses
// (gzip), and decodes the entityRegion NBT compound back into the []save.Entities. An unknown
// compression byte or a short payload is a real error (surfaced to the caller).
func decodeEntitySector(data []byte) ([]save.Entities, error) {
	if len(data) < 1 {
		return nil, errors.New("entity sector: empty payload")
	}
	if data[0] != entityCompressionGzip {
		return nil, errors.New("entity sector: unknown compression")
	}
	gz, err := gzip.NewReader(bytes.NewReader(data[1:]))
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	var root entityRegion
	if _, err := nbt.NewDecoder(gz).Decode(&root); err != nil {
		return nil, err
	}
	return root.Entities, nil
}
