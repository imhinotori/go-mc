package server

// tracker.go is the ENT-01 headline: a SYNCHRONOUS entity tracker that makes spawned
// entities visible to nearby players. It FILLS the Phase-3 tracker.Tick() seam (the
// noopTracker is replaced by &entityTracker{loop} in NewTickLoop) WITHOUT changing the
// `tracker interface{ Tick() }` shape or the t.tracker.Tick() call site in tick_phases.go —
// so Phase 8 (OPT-02) can later swap the EXECUTOR off-tick behind this exact seam.
//
// SYNCHRONOUS ONLY (TICK-05 / threat T-6-08): Tick() runs ON the tick goroutine, reads the
// tick-owned entityStore + per-player tracked sets, and emits via p.client.Send (the bounded
// outbound queue; the writeLoop stays the sole socket writer). It spawns NO goroutine and
// uses NO xsync/ants/conc — there is no async subsystem to optimize until Phase 8, per
// CLAUDE.md "Stack Patterns by Variant". All state it touches is owned by the tick goroutine,
// so it is -race clean by construction (proven by the Docker -race gate).

// trackRange is the entity track distance in CHUNK COLUMNS for the near() broad-phase
// (06-RESEARCH A5). Vanilla uses per-type ranges (players ~48 blocks, mobs ~80); v1 uses a
// single conservative range. 6 columns ≈ 96 blocks Chebyshev, comfortably covering both. It
// is the DoS bound on the visible/tracked set (threat T-6-10): near() walks only
// (2*trackRange+1)^2 candidate columns, never the whole world.
const trackRange = 6

// entityTracker is the synchronous tracking executor assigned to TickLoop.tracker. It holds
// only a back-reference to the loop; all the state it reads/writes lives on the loop and is
// tick-owned. Phase 8 replaces this type (behind the unchanged interface) with an async
// executor that computes the same diff off-tick and rejoins via applyAsyncResults.
type entityTracker struct {
	loop *TickLoop
}

// Tick is the synchronous per-player visibility diff (06-RESEARCH Pattern 1). For each
// player it queries the store's near() broad-phase for the in-range entities and diffs them
// against the player's tracked set:
//
//   - newly-in-range  → ClientboundAddEntity (+ ClientboundSetEntityData; + SetEntityMotion
//     if the entity has non-zero velocity) and the id is added to tracked.
//   - still-in-range, tracked, and moved → ClientboundTeleportEntity (absolute; v1 favors
//     teleport over the short-delta MoveEntity* to avoid the overflow edge) + RotateHead.
//   - no-longer-in-range → batched into ONE ClientboundRemoveEntities and removed from tracked.
//
// A player NEVER tracks itself (its own entityID is skipped). Runs entirely on the tick
// goroutine; emits via p.client.Send. No goroutine is spawned.
func (et *entityTracker) Tick() {
	t := et.loop
	if t == nil || t.entities == nil {
		return // defensive: a loop without a store has nothing to track
	}

	for _, p := range t.players {
		if p == nil || p.client == nil {
			continue // a player mid-registration / without a connection: skip
		}
		if p.tracked == nil {
			p.tracked = make(map[int32]bool) // lazy-init: keep registration minimal
		}

		// Broad-phase: the in-range candidate entities (bounded by trackRange, never a full
		// scan). near() returns a fresh slice the tracker may retain.
		visible := t.entities.near(p.x, p.z, trackRange)

		// seen marks which currently-tracked ids are still in range this tick; any tracked id
		// NOT seen has left range and is batched into the single RemoveEntities below.
		seen := make(map[int32]bool, len(visible))

		for _, e := range visible {
			if e == nil || e.id == p.entityID {
				continue // a player never tracks itself
			}
			seen[e.id] = true

			if !p.tracked[e.id] {
				// Newly visible: spawn it. AddEntity, then SetEntityData (always — the 0xFF
				// terminator keeps the stream aligned), then SetEntityMotion only if moving.
				p.client.Send(encodeAddEntity(e))
				p.client.Send(encodeSetEntityData(e))
				if e.vx != 0 || e.vy != 0 || e.vz != 0 {
					p.client.Send(encodeSetEntityMotion(e))
				}
				p.tracked[e.id] = true
				continue
			}

			// Already tracked and still visible: send an absolute teleport (+ head rotation).
			// v1 uses TeleportEntity (absolute Doubles) for every moved entity so a large
			// per-tick delta never overflows the MoveEntity* short. Switching to delta moves
			// for small steps is a later bandwidth optimization (the encoders already exist).
			p.client.Send(encodeTeleportEntity(e))
			p.client.Send(encodeRotateHead(e.id, e.headYaw))
		}

		// Anything tracked but no longer in range left the player's view: batch ALL such ids
		// into ONE ClientboundRemoveEntities (06-RESEARCH Pattern 1) and forget them.
		var gone []int32
		for id := range p.tracked {
			if !seen[id] {
				gone = append(gone, id)
			}
		}
		if len(gone) > 0 {
			p.client.Send(encodeRemoveEntities(gone))
			for _, id := range gone {
				delete(p.tracked, id)
			}
		}
	}
}
