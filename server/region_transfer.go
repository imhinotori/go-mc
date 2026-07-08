package server

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/plugin/host"
)

// region_transfer.go is the Phase-27 (Folia regionization) STEP-3 core: the static chunk→region
// hash, the per-region current-region resolution that makes the N=2 fan-out route each phase to the
// OWNING region's store WITHOUT touching the ~200 t.only() call sites, the cross-region entity
// TRANSFER at the barrier, and the async-rejoin owning-region lookup. Plans 01/02 extracted the
// region struct + the conc coordinator at N=1; this plan flips to N=2 and makes the four inherently
// cross-region concerns correct (transfer, async-rejoin routing, the cross-region tracker — Task 2 —
// and the region-aware plugin Emit — Task 2).
//
// THE STATIC SPLIT (27-RESEARCH Open Question 1 / A2): regionOf is a PURE function of the chunk
// column only — no global state, no persisted region id — so the same chunk ALWAYS maps to the same
// region, deterministically and STABLY across restarts (chunks are keyed by position, not by a stored
// region id). N=2 is a fixed split (no dynamic merge/split — that is a locked deferral); it exists to
// prove the seam, not to load-balance.

// regionCount is the number of regions the world is statically split into (the N=2 flip). It is a
// const, not a runtime value: the split is fixed (no dynamic merge/split — locked deferral), so the
// coordinator builds exactly regionCount regions and regionOf maps every column into [0, regionCount).
const regionCount = 2

// regionOf is the STATIC chunk→region hash (27-RESEARCH Pattern: a 2-region static split). It maps a
// chunk column to one of regionCount regions deterministically.
//
// THE EXACT FUNCTION + WHY IT IS RESTART-STABLE: regionOf returns the parity of (col.X ^ col.Z) — a
// checkerboard split. It is a PURE function of the column coordinates ONLY: it reads no global state,
// no clock, no persisted region id. Because Sulfur keys chunks by their (X,Z) position (the anvil
// region format / the ChunkManager map), the SAME chunk has the SAME column on every run, so regionOf
// yields the SAME region every restart — the 27-RESEARCH A2 "no region id persisted, stable across
// restarts" requirement is met structurally. The XOR-parity (rather than e.g. a raw half-plane on X)
// guarantees that EVERY chunk has neighbours in the OTHER region (a checkerboard), so the cross-region
// seam — the thing this whole phase proves — is exercised everywhere a player or mob moves one column,
// not only at a single dividing line.
//
// The bit math: (X^Z)&1. Go's & on a (possibly negative) int32 operates on the two's-complement bit
// pattern, and bit 0 is the parity regardless of sign, so negative columns split identically to
// positive ones (no negative-coordinate skew). The result is always 0 or 1 for regionCount==2.
func regionOf(col level.ChunkPos) regionID {
	return regionID((col[0] ^ col[1]) & 1)
}

// regionForColumn returns the region that owns chunk column col (the live *region the coordinator
// built). Pure routing over t.regions via regionOf.
func (t *TickLoop) regionForColumn(col level.ChunkPos) *region {
	return t.regions[regionOf(col)]
}

// regionForEntity returns the region that owns entity e by its CURRENT position (regionOf its column).
// This is the authoritative "which region should own this entity" answer the transfer detector and the
// region-aware handle build use.
func (t *TickLoop) regionForEntity(e *Entity) *region {
	return t.regionForColumn(columnOf(e.x, e.z))
}

// owningRegion finds the region whose store CURRENTLY HOLDS id, scanning the regions (re-resolve by
// id), or nil if no region owns it (the entity despawned or transferred away). It is the cross-region
// "drop if gone" re-resolve the async rejoin (pathReady/spawnCandidatesReady.applyTo) uses: a late
// async result re-resolves the owner here and DROPS if nil. The scan is O(regionCount) — tiny — and
// runs on the OWNER (coordinator) at the barrier, never mid-region-tick.
func (t *TickLoop) owningRegion(id int32) *region {
	for _, r := range t.regions {
		if _, ok := r.entities.get(id); ok {
			return r
		}
	}
	return nil
}

// emitEntityEvent is the Phase-27 STEP-3 region-scoped plugin dispatch (27-RESEARCH "Pattern 3:
// Region-aware plugin Emit"). An entity-scoped hook fired from inside region R (a spawn/death/damage
// for an entity OWNED by R) goes through here so the dispatch is explicitly region-scoped: the FROZEN
// *host.Manager registry (m.hooks) is GLOBAL + SHARED, and calling Emit from a region goroutine is
// SAFE because the Phase-21 freeze makes the callables immutable + lock-free across threads (only the
// HANDLES the payload carries are region-resolved — and those are built region-bound by
// starlarkGoal.handles). The GLOBAL on_tick stays on the coordinator (region_coordinator.go) — this
// helper is for ENTITY-scoped events only. Nil-guarded: a no-plugin server is unaffected.
//
// THE KEY INVARIANT: the registry is shared (read from every region thread, lock-free by the freeze),
// while the per-entity HANDLES resolve against the calling region's state. That is what makes "an
// entity-scoped event for an entity in region R fires on R's thread and resolves R's store" hold.
func (r *region) emitEntityEvent(event host.EventType, payload host.Event) {
	t := r.coord
	if t == nil || t.plugins == nil {
		return // no plugins / a standalone test region: a cheap no-op
	}
	t.plugins.Emit(event, payload)
}

// withRegion runs fn with r registered as the CURRENT region for the calling goroutine, restoring
// the prior registration (if any) afterwards. It lets a COORDINATOR-side phase (tickItems) process a
// specific region's entities so the deep t.only() call sites (moveEntity's re-bucket, the despawn
// remove) resolve to THAT region's store rather than globalRegion. Used only on the coordinator
// goroutine where there is no concurrent region tick (quiescent), so the temporary registration never
// races a fan-out goroutine's own registration (they key on distinct goroutine ids regardless).
func (t *TickLoop) withRegion(r *region, fn func()) {
	if t.currentRegion == nil {
		fn()
		return
	}
	gid := curGoroutineID()
	prev, had := t.currentRegion.Load(gid)
	t.currentRegion.Store(gid, r)
	defer func() {
		if had {
			t.currentRegion.Store(gid, prev)
		} else {
			t.currentRegion.Delete(gid)
		}
	}()
	fn()
}

// forEachRegion runs fn once per region with THAT region registered as the calling goroutine's
// current region (via withRegion), so a coordinator-side per-region phase drained inside fn resolves
// cur().{entities,fluidSchedule,blockTicks,levelRandom} to the iterated region's OWN store rather than
// blindly globalRegion. It is the fix for the coordinator-phase bug class: a per-region drain
// (tickScheduledBlocks/tickFluids) run once on the coordinator silently skipped regions 1..N-1 because
// cur() fell back to region 0; wrapping each region in turn makes every region's queue drain against
// the shared world. Skips a region whose entities are nil (a not-yet-constructed standalone region).
// Runs on the coordinator at the quiescent barrier (no region is ticking), so the temporary
// per-iteration registration never races a fan-out goroutine. At N=1 it runs exactly once with region
// 0 registered — identical to the pre-fix single drain (behavior-neutral).
func (t *TickLoop) forEachRegion(fn func(r *region)) {
	for _, r := range t.regions {
		if r.entities == nil {
			continue
		}
		t.withRegion(r, func() { fn(r) })
	}
}

// entitiesNearAcrossRegions returns every entity within rangeChunks columns of (x,z) across ALL
// regions whose column range the query box intersects (Phase-27 STEP-3, Pitfall 2: the cross-region
// tracker/pickup broad-phase). Because each region owns a disjoint subset of columns (the static
// regionOf split), an entity in range may live in EITHER region, so the query merges each region's
// near() result. It runs at the QUIESCENT barrier (every region joined), so reading multiple region
// stores is -race clean by construction. The returned slice is a fresh merge the caller may retain.
func (t *TickLoop) entitiesNearAcrossRegions(x, z float64, rangeChunks int) []*Entity {
	var out []*Entity
	for _, r := range t.regions {
		if r.entities == nil {
			continue
		}
		out = append(out, r.entities.near(x, z, rangeChunks)...)
	}
	return out
}

// transferIntent is one queued cross-region hand-off: the *Entity whose column now maps to region
// `to`, recorded mid-tick by detectTransfers and applied at the barrier by applyCrossRegionTransfers.
// It carries the live *Entity pointer (NOT a copy): the pointer is adopted by the new region at the
// QUIESCENT barrier point (no region is ticking then), so the ai/nav/plugin-goal scratch travels with
// it and is never aliased across a live tick (27-RESEARCH "Pattern 4: cross-region entity transfer at
// the barrier").
type transferIntent struct {
	ent *Entity
	to  regionID
}

// damageIntent is one queued cross-region damage application: a player (in the SOURCE region) hit a
// mob OWNED by region `to`, recorded by handleAttack and applied at the barrier by
// applyCrossRegionDamage. It mirrors transferIntent (l.160) EXACTLY — the `to` is the OWNER/target
// region (transferIntent.to), and the intent is kept on the SOURCE region's slice (the actor's own
// region), drained centrally so no goroutine appends to another region's slice mid-tick (Assumption
// A3). It carries the damageSource as a PLAIN VALUE (the Folia rule: a value travels across the
// barrier with no aliasing) and the victim's id (re-resolved by owningRegion at drain — drop if gone),
// NOT a live *Entity pointer.
type damageIntent struct {
	victimID int32
	src      damageSource
	amount   float32
	to       regionID // target (owner) region — mirrors transferIntent.to (l.162)
}

// queueDamageIntent records a cross-region hit on the SOURCE region's pendingDamage slice, tagged to
// the OWNER region. It mirrors the transferIntent discipline (detectTransfers appends to THIS region's
// pendingTransfers, l.177): the append targets srcRegion's OWN slice — the actor's own region — NEVER a
// cross-goroutine append to owner's slice, which would race owner's tick (Assumption A3). The actual
// apply happens at the quiescent coordinator barrier (applyCrossRegionDamage). This is a pure
// same-goroutine slice append; it does NOT touch the owner region or the victim's store.
func (t *TickLoop) queueDamageIntent(srcRegion *region, owner regionID, di damageIntent) {
	di.to = owner
	srcRegion.pendingDamage = append(srcRegion.pendingDamage, di)
}

// detectTransfers scans THIS region's entities at the END of its tick (still on the region's
// goroutine — it only QUEUES, never mutates another region's store) and records a transferIntent for
// every entity whose current column now maps to a DIFFERENT region. The physics/AI step inside the
// region's tick may have moved an entity across the seam; the actual hand-off happens at the barrier
// (applyCrossRegionTransfers) when every region is quiescent. Only enumeration + a slice append touch
// the region here, so it is single-owner clean.
func (r *region) detectTransfers() {
	if r.entities == nil {
		return
	}
	for _, e := range r.entities.all() {
		if regionOf(columnOf(e.x, e.z)) != r.id {
			r.pendingTransfers = append(r.pendingTransfers, transferIntent{ent: e, to: regionOf(columnOf(e.x, e.z))})
		}
	}
}

// applyCrossRegionTransfers drains every region's pendingTransfers on the QUIESCENT coordinator
// (post-barrier, no region is ticking) and moves each entity A→B: remove from the source region's
// store, add to the destination region's store. The *Entity pointer (with its ai/nav/scratch) is
// ADOPTED by the new region — NEVER copied, NEVER aliased across a live tick (it moves at the
// quiescent point). It is the FIRST step of the post-phase, BEFORE the cross-region tracker reads
// across regions, so the tracker sees the post-transfer ownership.
//
// 27-RESEARCH "Pattern 4: Cross-region entity transfer at the barrier". The remove-then-add ordering
// guarantees exactly-once ownership: between the remove and the add the entity is momentarily in no
// store, but this runs single-threaded on the coordinator (every region joined), so no tick observes
// the gap — the NEXT tick's fan-out finds it in exactly one region (no double-tick, no drop).
func (t *TickLoop) applyCrossRegionTransfers() {
	// NOTE: no t.trace here — applyCrossRegionTransfers is an internal barrier step, NOT one of the
	// fixed observable phases (TestTickPhaseOrder asserts the exact phase sequence, which this must
	// not perturb). It runs between the barrier and applyAsyncResults each tick.
	for _, src := range t.regions {
		if len(src.pendingTransfers) == 0 {
			continue
		}
		for _, ti := range src.pendingTransfers {
			// Re-confirm the source still holds it (a defensive guard: detectTransfers queued it from
			// THIS src this tick, so it must be present — but a double-queue across regions can never
			// double-move because remove() is a no-op for a missing id and the add targets one region).
			if _, ok := src.entities.get(ti.ent.id); !ok {
				continue
			}
			src.entities.remove(ti.ent.id)        // out of the source region's store
			t.regions[ti.to].entities.add(ti.ent) // into the destination region's store (same *Entity)
		}
		src.pendingTransfers = src.pendingTransfers[:0] // reset for next tick (keep the backing array)
	}
}

// applyCrossRegionDamage drains every region's pendingDamage on the QUIESCENT coordinator (post-barrier,
// no region is ticking) and applies each cross-region hit to its OWNER region — the project's FIRST true
// cross-region WRITE (PITFALLS Pitfall 2 / T-29-02). It mirrors applyCrossRegionTransfers EXACTLY: it
// runs at the same barrier point (called right after applyCrossRegionTransfers in the coordinator), with
// the same defensive owner==nil drop and the same [:0] reset.
//
// For each queued damageIntent the OWNER is RE-RESOLVED BY ID (owningRegion, l.65) at drain time — NOT
// trusted from di.to and NEVER resolved through cur() (the cur()→region-0 silent-fallback trap the whole
// regionization gate guards against). If no region owns the victim (it despawned/transferred since the
// hit was queued), the intent is DROPPED — the same "drop if gone" guard the async rejoin uses. The
// surviving hit is applied inside withRegion(owner) so applyDamageEntity's region-scoped writes
// (the mob's store fields + the on_damage emitEntityEvent) resolve against the OWNER's region. Because
// every region is joined here, the write is -race clean by construction (no region tick observes it).
func (t *TickLoop) applyCrossRegionDamage() {
	// NOTE: no t.trace here — like applyCrossRegionTransfers this is an internal barrier step, NOT one of
	// the fixed observable phases (TestTickPhaseOrder asserts the exact phase sequence, which this must
	// not perturb). It runs immediately after the transfer drain each tick.
	for _, src := range t.regions {
		if len(src.pendingDamage) == 0 {
			continue
		}
		for _, di := range src.pendingDamage {
			// Re-resolve the OWNER by id (drop if gone — the defensive guard mirroring l.205): a victim
			// removed or transferred since the hit was queued has no owning region; skip it (no panic, no
			// phantom write). NEVER resolve via cur() (the silent region-0 fallback trap).
			owner := t.owningRegion(di.victimID)
			if owner == nil {
				continue
			}
			mob, ok := owner.entities.get(di.victimID)
			if !ok {
				continue // owningRegion just confirmed the id, but stay defensive (mirrors the transfer guard)
			}
			// Apply in the OWNER's region context so applyDamageEntity's per-region writes resolve there.
			t.withRegion(owner, func() { t.applyDamageEntity(mob, di.src, di.amount) })
		}
		src.pendingDamage = src.pendingDamage[:0] // reset for next tick (keep the backing array)
	}
}
