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
	"strings"
	"sync"
	"time"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/nbt"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"
	// Aliased to regionfile: the unqualified `region` identifier is now the Phase-27
	// per-region tick-owner type (region.go). This import is the anvil region-FILE IO.
	regionfile "github.com/imhinotori/sulfur/save/region"

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
		// Food/hunger (Plan 17-19): persist FoodData's exhaustionLevel/tickTimer so a player resumes
		// mid-drain/mid-regen on reconnect (vanilla FoodData.addAdditionalSaveData keys
		// foodExhaustionLevel/foodTickTimer).
		FoodExhaustionLevel: p.exhaustion,
		FoodTickTimer:       p.foodTickTimer,
		PlayerGameType:      gameModeSurvival,
	}
	if p.onGround {
		data.OnGround = 1
	}
	if p.inventory != nil {
		data.SelectedItemSlot = int32(p.inventory.heldSlot)
		data.Inventory = inventoryToItems(p.inventory)
	}
	// ENDER CHEST: persist the per-player 27-slot ender inventory to the .dat EnderItems list (the same
	// ItemStackWithSlot codec the main Inventory uses). A never-opened player has a nil/short slice ->
	// enderItemsToDisk skips empties, writing no EnderItems when the ender inventory is empty. CITE
	// Player.addAdditionalSaveData (getEnderChestInventory().storeAsSlots -> "EnderItems").
	if len(p.enderItems) > 0 {
		data.EnderItems = enderItemsToDisk(p.enderItems)
	}
	// Extended fields (SUB-PERSIST): XP, abilities (derived from gameMode), active mob effects, the
	// spawn point, the game type + previous game type, and the dimension. See player_persist_ext.go.
	snapshotPlayerExtras(&data, p)
	// DataVersion stamps the save with the current world version (NbtUtils.addCurrentDataVersion), so a
	// future datafixer knows the schema. 26.2 == 4903 (DetectedVersion). Matches the raids/POI/stats path.
	data.DataVersion = playerDataVersion
	return data
}

// playerDataVersion is SharedConstants.getCurrentVersion().dataVersion().version() for 26.2 (4903), the
// same DetectedVersion the raids/POI/stats saves stamp. Written into the .dat DataVersion so the schema
// is self-describing (NbtUtils.addCurrentDataVersion).
const playerDataVersion int32 = 4903

// inventoryToItems translates the tick-owned component-slot inventory into the modern disk-NBT
// ItemStackWithSlot list (SUB-PERSIST: the 26.2 codec, NOT the legacy save.Item; NOT the wire
// component codec — Pitfall 6). It builds a []save.DiskItem over the inventory slots (resolving the
// numeric wire id → "minecraft:<name>" string id, flagging component-bearing stacks) and runs
// save.SaveAllItems (skip-empty, Slot=index, the ContainerHelper port). keepEmptyTag=false so an
// empty inventory writes no Items list. Component-bearing stacks persist {Slot,id,count} only
// (Phase A drop, metered by dropped); a later Phase B transcodes their components to disk NBT.
//
// CITE: net.minecraft.world.entity.player.Inventory.save — for each non-empty slot, add
// ItemStackWithSlot(i, stack) to the typed list (identical skip-empty + Slot=index to saveAllItems).
func inventoryToItems(inv *Inventory) []save.ItemStackWithSlotDisk {
	disk := make([]save.DiskItem, len(inv.slots))
	for i, slot := range inv.slots {
		if slot.Count <= 0 {
			continue // empty slot: zero DiskItem (skipped on save by index)
		}
		disk[i] = save.DiskItem{
			ID:               itemName(int32(slot.ItemID)),
			Count:            int32(slot.Count),
			HasComponents:    len(slot.RawComponents) > 0,
			WireComponents:   slot.RawComponents, // Phase B: SUPPORTED components transcoded to disk
			WireAddedCount:   int(slot.AddedCount),
			WireRemovedCount: int(slot.RemovedCount),
		}
	}
	items, dropped := save.SaveAllItems(disk, false)
	if dropped > 0 {
		log.Printf("player inventory: %d component-bearing stacks had UNSUPPORTED components dropped (SUB-ITEMNBT Phase B: enchantments/long-tail; supported set transcoded)", dropped)
	}
	return items
}

// enderItemsToDisk translates the per-player 27-slot ender inventory into the disk-NBT ItemStackWithSlot
// list (the inventoryToItems twin, over p.enderItems). Skip-empty, Slot=index. CITE
// PlayerEnderChestContainer.storeAsSlots (for each non-empty slot, add ItemStackWithSlot(i, stack)).
func enderItemsToDisk(items []component.SlotData) []save.ItemStackWithSlotDisk {
	disk := make([]save.DiskItem, len(items))
	for i, slot := range items {
		if slot.Count <= 0 {
			continue
		}
		disk[i] = save.DiskItem{
			ID:               itemName(int32(slot.ItemID)),
			Count:            int32(slot.Count),
			HasComponents:    len(slot.RawComponents) > 0,
			WireComponents:   slot.RawComponents,
			WireAddedCount:   int(slot.AddedCount),
			WireRemovedCount: int(slot.RemovedCount),
		}
	}
	items2, _ := save.SaveAllItems(disk, false)
	return items2
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

// itemNameToID is the reverse of itemName: a "minecraft:<name>" (or bare "<name>") disk id → the
// numeric wire item id, for restoring a loaded inventory/container into the tick-owned slot form.
// It is the load-side registry resolver (SUB-PERSIST round-trip). The reverse map is built ONCE
// lazily from item.ByID (data/item has no ByName), under a sync.Once so a join never races the
// build. An unknown/forged id resolves to air (id 0) so a corrupt save never injects an invalid item.
var (
	itemIDByNameOnce sync.Once
	itemIDByName     map[string]item.ID
)

func itemNameToID(name string) int32 {
	itemIDByNameOnce.Do(func() {
		itemIDByName = make(map[string]item.ID, len(item.ByID))
		for id, it := range item.ByID {
			itemIDByName[it.Name] = id
		}
	})
	// Accept both "minecraft:stone" and bare "stone" (the registry stores the bare path).
	name = strings.TrimPrefix(name, "minecraft:")
	if id, ok := itemIDByName[name]; ok {
		return int32(id)
	}
	return 0 // unknown id: air (never inject an invalid item from a corrupt save)
}

// itemsToInventory is the LOAD inverse of inventoryToItems (SUB-PERSIST round-trip): it places a
// loaded ItemStackWithSlot list back into a fresh tick-owned []component.SlotData of size, via
// save.LoadAllItems (bounds-checked, slot-keyed) then resolving each id string → numeric wire id.
// A Phase-A-saved item has no components (RawComponents stays nil); a future Phase B decode would
// re-attach them. Out-of-range slots are silently dropped (isValidInContainer). Returns the slot
// array the join restore copies into the live inventory.
func itemsToInventory(items []save.ItemStackWithSlotDisk, size int) []component.SlotData {
	disk := save.LoadAllItems(items, size)
	out := make([]component.SlotData, size)
	for i, d := range disk {
		if d.IsEmpty() {
			continue
		}
		out[i] = component.SlotData{
			Count:         pk.VarInt(d.Count),
			ItemID:        pk.VarInt(itemNameToID(d.ID)),
			AddedCount:    pk.VarInt(d.WireAddedCount),   // Phase B: rebuilt from the disk components compound
			RemovedCount:  pk.VarInt(d.WireRemovedCount), //
			RawComponents: d.WireComponents,              //
		}
	}
	return out
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
	// Periodic permission-store flush: op/deop mutations mark the store dirty on the tick goroutine;
	// Save() is a cheap no-op when clean, so a coarse ticker persists changes without per-mutation IO.
	// A final flush runs on ctx cancel so a shutdown right after /op is not lost.
	permTick := time.NewTicker(30 * time.Second)
	defer permTick.Stop()
	for {
		select {
		case <-ctx.Done():
			t.drainPlayerSnapshotsOnShutdown(worldDir)
			if t.perms != nil {
				if err := t.perms.Save(); err != nil {
					log.Printf("save permissions (shutdown): %v", err)
				}
			}
			return
		case <-permTick.C:
			if t.perms != nil {
				if err := t.perms.Save(); err != nil {
					log.Printf("save permissions: %v", err)
				}
			}
		case snap := <-t.leaveSnapshots:
			t.writePlayerSnapshot(worldDir, snap)
		}
	}
}

func (t *TickLoop) writePlayerSnapshot(worldDir string, snap playerLeaveSnapshot) {
	if err := savePlayer(worldDir, snap.uuid, snap.data); err != nil {
		log.Printf("save player %s: %v", snap.uuid, err)
	}
	if snap.stats != nil {
		if err := saveStats(worldDir, snap.uuid, snap.stats); err != nil {
			log.Printf("save stats %s: %v", snap.uuid, err)
		}
	}
}

// drainPlayerSnapshotsOnShutdown keeps consuming while the tick publishes its final saveAll
// batch. If no tick producer was ever started (standalone consumer tests), it only drains what is
// already buffered and returns promptly.
func (t *TickLoop) drainPlayerSnapshotsOnShutdown(worldDir string) {
	select {
	case <-t.saveProducerStarted:
		for {
			select {
			case snap := <-t.leaveSnapshots:
				t.writePlayerSnapshot(worldDir, snap)
			case <-t.saveProducerDone:
				for {
					select {
					case snap := <-t.leaveSnapshots:
						t.writePlayerSnapshot(worldDir, snap)
					default:
						return
					}
				}
			}
		}
	default:
		for {
			select {
			case snap := <-t.leaveSnapshots:
				t.writePlayerSnapshot(worldDir, snap)
			default:
				return
			}
		}
	}
}

// enqueuePlayerSnapshot never discards an immutable snapshot. Normal tick work uses an owner-side
// overflow rather than blocking; shutdown uses a blocking handoff while RunSaveLoop drains.
func (t *TickLoop) enqueuePlayerSnapshot(snap playerLeaveSnapshot, durable bool) {
	if t.leaveSnapshots == nil {
		return
	}
	if durable {
		t.leaveSnapshots <- snap
		return
	}
	select {
	case t.leaveSnapshots <- snap:
	default:
		// Coalesce delayed autosaves/leaves per UUID. The newest owner-side snapshot supersedes an
		// older pending value and bounds overflow by distinct players rather than save intervals.
		for i := range t.pendingPlayerSnapshots {
			if t.pendingPlayerSnapshots[i].uuid == snap.uuid {
				t.pendingPlayerSnapshots[i] = snap
				return
			}
		}
		t.pendingPlayerSnapshots = append(t.pendingPlayerSnapshots, snap)
	}
}

func (t *TickLoop) flushPendingPlayerSnapshots(durable bool) {
	for len(t.pendingPlayerSnapshots) > 0 {
		snap := t.pendingPlayerSnapshots[0]
		if durable {
			t.leaveSnapshots <- snap
		} else {
			select {
			case t.leaveSnapshots <- snap:
			default:
				return
			}
		}
		t.pendingPlayerSnapshots[0] = playerLeaveSnapshot{}
		t.pendingPlayerSnapshots = t.pendingPlayerSnapshots[1:]
	}
}

// defaultPlayerData is the spawn-default save.PlayerData a missing/corrupt .dat falls back to: a
// full survival player (maxHealth / maxFood / defaultSaturation) at the world spawn column. It
// mirrors the join-bootstrap defaults so a defaulted player and a freshly-bootstrapped one are
// indistinguishable.
func defaultPlayerData() save.PlayerData {
	return save.PlayerData{
		DataVersion:         playerDataVersion,
		Dimension:           overworldDimensionName,
		Health:              maxHealth,
		FoodLevel:           maxFood,
		FoodSaturationLevel: defaultSaturation,
		PlayerGameType:      gameModeSurvival,
	}
}

// entityRegionPath returns the entities/r.<rx>.<rz>.mca path for the region containing the chunk
// column, reusing regionfile.At (the SAME naming the chunk region uses, under a parallel entities/
// subdir). The directory is created by saveEntities on demand.
func entityRegionPath(dir string, pos level.ChunkPos) (regionPath string, ix, iz int) {
	cx, cz := int(pos[0]), int(pos[1])
	rx, rz := regionfile.At(cx, cz)
	ix, iz = regionfile.In(cx, cz)
	regionPath = filepath.Join(dir, entitiesDir, "r."+strconv.Itoa(rx)+"."+strconv.Itoa(rz)+".mca")
	return
}

// entityRegion is the NBT root the entities region stores per cell. It is the vanilla EntityStorage
// cell compound VERIFIED via javap EntityStorage.storeEntities:
//   - DataVersion : NbtUtils.addCurrentDataVersion (int) -- stamped first
//   - Entities    : ListTag of entity compounds
//   - Position    : ChunkPos.CODEC == int[2] [x,z] -- the owning column, validated on load
//
// storeEntities writes all three; loadEntities reads Position back and rejects a cell whose stored
// position != the requested column ("Chunk file at {} is in the wrong location"). SUB-PERSIST: added
// Position + DataVersion (previously only Entities was written).
type entityRegion struct {
	DataVersion int32           `nbt:"DataVersion"`
	Entities    []save.Entities `nbt:"Entities"`
	Position    [2]int32        `nbt:"Position"`
}

// entityDirsCreated memoizes the entities-dir MkdirAll so it runs ONCE per directory, not once per
// column per save interval. The autosave pass visits every Ready column each interval and each call
// used to os.MkdirAll(filepath.Dir(regionPath)); as a player explores, the Ready set grows without
// bound, so that turned into hundreds of redundant MkdirAll syscalls per pass (the dir already exists
// after the first). A concurrent map guards it because saveEntities is now also called from the
// off-tick entity-save goroutine; the first writer for a dir creates it, the rest hit the fast-path
// load. A creation error is NOT cached (so a transient failure retries next call).
var entityDirsCreated sync.Map // map[string]struct{} — key is the entities-region directory

// ensureEntityDir creates the entities-region directory once. On the first call for a given dir it
// runs os.MkdirAll and records success; subsequent calls for the same dir are a single map load with
// no syscall. Returns any MkdirAll error from the first (uncached) attempt.
func ensureEntityDir(regionDir string) error {
	if _, ok := entityDirsCreated.Load(regionDir); ok {
		return nil // already created this run: skip the syscall
	}
	if err := os.MkdirAll(regionDir, 0o755); err != nil {
		return err // not cached: a transient failure retries on the next call
	}
	entityDirsCreated.Store(regionDir, struct{}{})
	return nil
}

// saveEntities writes an entity snapshot for a chunk column into the parallel entities/r.x.z.mca
// region via save/region (ReadSector/WriteSector) — REUSING the Anvil region IO, NOT a new
// format (the chunk's legacy in-chunk Entities slot is not the modern path). The sector payload
// is a compression byte (gzip) + gzip(NBT) so it matches the chunk region's sector framing. The
// entities directory is created ONCE per dir (ensureEntityDir) and the region file on demand; an
// existing region is opened and the cell overwritten. Runs OFF the tick over the immutable
// []save.Entities snapshot (durable synchronous write — the caller decides on-tick vs off-tick).
func saveEntities(dir string, pos level.ChunkPos, ents []save.Entities) error {
	regionPath, ix, iz := entityRegionPath(dir, pos)
	if err := ensureEntityDir(filepath.Dir(regionPath)); err != nil {
		return err
	}

	// Open the region (or create it if this is the first write to this region file).
	r, err := regionfile.Open(regionPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		r, err = regionfile.Create(regionPath)
		if err != nil {
			return err
		}
	}
	defer r.Close()

	payload, err := encodeEntitySector(pos, ents)
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
// A fresh Region is opened and closed per call (regionfile.Region is Not MT-Safe), mirroring the
// chunk-load discipline (world/worker.tryRegion). Runs OFF the tick.
func loadEntities(dir string, pos level.ChunkPos) ([]save.Entities, bool, error) {
	regionPath, ix, iz := entityRegionPath(dir, pos)

	r, err := regionfile.Open(regionPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil // no region file yet -> miss
		}
		return nil, false, err
	}
	defer r.Close()

	data, err := r.ReadSector(ix, iz)
	if err != nil {
		if errors.Is(err, regionfile.ErrNoSector) || errors.Is(err, regionfile.ErrNoData) {
			return nil, false, nil // not-yet-saved cell -> miss
		}
		return nil, false, err // corrupt/real error
	}

	ents, wantPos, err := decodeEntitySector(data)
	if err != nil {
		return nil, false, err
	}
	// EntityStorage validates the stored Position matches the requested column ("Chunk file at {} is
	// in the wrong location"). A mismatch is a misplaced/corrupt cell -> treat as a miss (drop it)
	// rather than respawning entities into the wrong column.
	if wantPos != [2]int32{int32(pos[0]), int32(pos[1])} {
		return nil, false, nil
	}
	return ents, true, nil
}

// encodeEntitySector encodes the entities list as a region sector payload: a compression byte
// (gzip) followed by gzip(NBT) of the entityRegion compound. It stamps the DataVersion and the
// column Position (ChunkPos [x,z]) EntityStorage.storeEntities writes alongside the Entities list.
// The gzip writer is closed (trailer flushed) before the bytes are returned so the sector is a
// complete, re-readable stream.
func encodeEntitySector(pos level.ChunkPos, ents []save.Entities) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte(entityCompressionGzip)
	gz := gzip.NewWriter(&buf)
	cell := entityRegion{
		DataVersion: playerDataVersion, // NbtUtils.addCurrentDataVersion (4903 for 26.2)
		Entities:    ents,
		Position:    [2]int32{int32(pos[0]), int32(pos[1])}, // ChunkPos.CODEC == int[2] [x,z]
	}
	if err := nbt.NewEncoder(gz).Encode(cell, ""); err != nil {
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
func decodeEntitySector(data []byte) ([]save.Entities, [2]int32, error) {
	if len(data) < 1 {
		return nil, [2]int32{}, errors.New("entity sector: empty payload")
	}
	if data[0] != entityCompressionGzip {
		return nil, [2]int32{}, errors.New("entity sector: unknown compression")
	}
	gz, err := gzip.NewReader(bytes.NewReader(data[1:]))
	if err != nil {
		return nil, [2]int32{}, err
	}
	defer gz.Close()

	var root entityRegion
	if _, err := nbt.NewDecoder(gz).Decode(&root); err != nil {
		return nil, [2]int32{}, err
	}
	return root.Entities, root.Position, nil
}
