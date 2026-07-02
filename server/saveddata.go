package server

import "log"

// saveddata.go — the TickLoop wiring for the raid + POI SavedData persistence (raid_persist.go /
// poi_persist.go): the SetPersistDir setter, load-on-boot (LoadPersistedData), the periodic
// save-on-dirty pass (folded into the existing tickChunkSave cadence), and the shutdown flush.
//
// The raid + POI managers are per-region (region.raidsManager / region.poiManager). At N=1 the single
// globalRegion is the overworld, so its managers map to the vanilla per-dimension world/data/raids.dat
// and world/poi/*.mca. The save/load is coordinator-driven (the managers are coordinator/region-owned,
// TICK-05): the encode is a pure owner-side read producing an IMMUTABLE value, and the disk IO runs
// synchronously in the save pass (the raid/POI footprint is tiny — one small .dat + a handful of poi
// cells — so it needs no off-tick channel like the chunk/entity paths; it stays a cheap owner-side
// flush gated by the dirty flag and the save cadence).

// savedDataSaveIntervalTicks is the periodic raid/POI flush cadence. It matches the chunk-save cadence
// (chunkSaveIntervalTicks == 100 ticks ≈ 5s at 20 TPS) so a burst of POI edits / raid mutations
// coalesces into one flush per interval. On top of the cadence the dirty flag gates the actual write:
// a clean manager writes nothing.
const savedDataSaveIntervalTicks = 100

// SetPersistDir wires the world directory the raid + POI SavedData persist under (SUB-PERSIST for
// raids/POI). "" (the default) disables raid/POI persistence — tests and ephemeral runs leave it
// unset and the save/load calls become cheap no-ops. Set once before Run; read only on the tick
// goroutine. main() calls it alongside SetChunkSaver so raids.dat / poi/ live under the SAME worldDir
// the chunk region + player .dat use.
func (t *TickLoop) SetPersistDir(dir string) { t.persistDir = dir }

// LoadPersistedData loads world/data/raids.dat + world/poi/*.mca into the overworld (globalRegion) at
// boot, BEFORE Run (so a reconnecting player joins a world whose raids + POI are already restored). A
// missing file/dir is a clean first-boot no-op (the lazy ensureRaidsManager/ensurePoiManager path
// takes over). A corrupt .dat/region is logged and skipped — a bad save never blocks boot (the SAME
// resilience loadPlayer/loadEntities use). Runs on the boot goroutine before the tick starts, so it
// touches globalRegion's managers without contention. A "" persistDir is a no-op.
func (t *TickLoop) LoadPersistedData() {
	if t.persistDir == "" {
		return
	}
	r := t.regions[globalRegion]

	// Raids: world/data/raids.dat -> region.raidsManager.
	if rm, ok, err := loadRaids(t.persistDir); err != nil {
		log.Printf("load raids.dat: %v (starting with no raids)", err)
	} else if ok {
		r.raidsManager = rm
		log.Printf("loaded %d raid(s) from raids.dat (next_id=%d, tick=%d)", len(rm.raidMap), rm.nextId, rm.tick)
	}

	// POI: world/poi/*.mca -> region.poiManager.
	if pm, ok, err := loadPoi(t.persistDir); err != nil {
		log.Printf("load poi: %v (starting with no POI)", err)
	} else if ok {
		r.poiManager = pm
		total := 0
		for _, s := range pm.sections {
			total += len(s.records)
		}
		log.Printf("loaded %d POI record(s) across %d section(s) from poi/", total, len(pm.sections))
	}
}

// tickSavedData is the periodic raid/POI save pass (SUB-PERSIST), called from the world phase inside
// the fixed tick order (alongside tickChunkSave — no new phase). Every savedDataSaveIntervalTicks it
// flushes any DIRTY per-region raid + POI manager. A "" persistDir makes it a cheap no-op. Runs on the
// owner (coordinator) goroutine — the managers are coordinator/region-owned, so the encode is a pure
// owner-side read.
func (t *TickLoop) tickSavedData() {
	if t.persistDir == "" {
		return
	}
	t.savedDataTickCounter++
	if t.savedDataTickCounter < savedDataSaveIntervalTicks {
		return
	}
	t.savedDataTickCounter = 0
	t.flushSavedData()
}

// flushSavedData writes every dirty raid + POI manager across all regions to disk (raids ->
// data/raids.dat, POI -> poi/*.mca), clearing each manager's dirty flag on a successful write. It is
// the shared body of the periodic pass (tickSavedData) and the shutdown flush (FlushSavedDataNow). A
// write error is logged and leaves the manager dirty so the next pass retries (a dropped save is
// deferred, never lost). Runs on the owner goroutine.
func (t *TickLoop) flushSavedData() {
	if t.persistDir == "" {
		return
	}
	for _, r := range t.regions {
		if rm := r.raidsManager; rm != nil && rm.isDirty() {
			data := encodeRaidsData(rm) // immutable snapshot on the owner
			if err := saveRaids(t.persistDir, data); err != nil {
				log.Printf("save raids.dat: %v (will retry)", err)
			} else {
				rm.clearDirty()
			}
		}
		if pm := r.poiManager; pm != nil && pm.isDirty() {
			snaps := snapshotPoi(pm) // immutable per-column snapshots on the owner
			if err := savePoi(t.persistDir, snaps); err != nil {
				log.Printf("save poi: %v (will retry)", err)
			} else {
				pm.clearDirty()
			}
		}
	}
}

// FlushSavedDataNow forces an immediate raid/POI flush (the shutdown path), bypassing the cadence so a
// clean exit right after a raid/POI mutation is never lost. main() calls it on shutdown after the tick
// loop stops (the managers are quiescent then). A "" persistDir is a no-op.
func (t *TickLoop) FlushSavedDataNow() { t.flushSavedData() }
