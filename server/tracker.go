package server

import pk "github.com/imhinotori/sulfur/net/packet"

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

// maxSpawnsPerTick is the per-player, per-tick budget on NEWLY-tracked entity SPAWNS (the
// ClientboundAddEntity + SetEntityData + equipment + motion burst). It is the entity-tracker
// analogue of desiredChunksPerTick (tick_phases.go): chunk streaming is already throttled per-tick,
// but the entity spawn burst was NOT -- on (re)join near a dense area the tracker enqueued a spawn
// packet for EVERY nearby entity in ONE tick, and a large backlog (thousands of mobs) overran the
// bounded per-connection outbound queue (client.go outboundCap == 4096) in a single tick -> the
// drop-and-disconnect "backpressure" kick (client.go Send).
//
// This bounds how many NEW entities are spawned to a player per tick; the rest are simply LEFT out
// of p.tracked this tick and picked up on subsequent ticks (the next diff sees them still
// not-tracked and spawns the next batch), so a 4000-entity backlog drains over many ticks instead
// of overflowing the queue at once. It is the vanilla-shaped incremental behavior (vanilla's
// ChunkMap.tick sends tracker updates a bounded slice at a time). Removals (RemoveEntities) and the
// per-entity movement stream are NOT throttled -- only the spawn burst, which is the unbounded one.
//
// Each newly-tracked entity costs ~2-4 packets (AddEntity + SetEntityData always, + equipment slots
// + motion when moving); 40 spawns/tick is ~80-160 packets/tick, comfortably under the 4096 queue
// alongside the throttled chunk stream, and drains a 4000-entity backlog in ~100 ticks (~5s) with no
// visible pop-in (entities appear as the player settles, exactly as chunks stream in).
const maxSpawnsPerTick = 40

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
	if t == nil {
		return // defensive: a loop without a store has nothing to track
	}

	for _, p := range t.players {
		if p == nil || p.client == nil {
			continue // a player mid-registration / without a connection: skip
		}
		if p.tracked == nil {
			p.tracked = make(map[int32]bool) // lazy-init: keep registration minimal
		}

		// Broad-phase ACROSS REGIONS (Phase-27 STEP-3, Pitfall 2): a player near the region seam must
		// see entities in the ADJACENT region too, so near() spans every region the trackRange touches.
		// The tracker runs at the BARRIER (every region quiescent), so reading multiple region stores
		// is -race clean by construction. near() returns a fresh slice the tracker may retain.
		visible := t.entitiesNearAcrossRegions(p.x, p.z, trackRange)

		// seen marks which currently-tracked ids are still in range this tick; any tracked id
		// NOT seen has left range and is batched into the single RemoveEntities below.
		seen := make(map[int32]bool, len(visible))

		// spawned counts the NEWLY-tracked entities emitted to this player THIS tick. Once it
		// reaches maxSpawnsPerTick the remaining new-in-range entities are NOT spawned this tick
		// (and NOT marked tracked), so the next tick's diff sees them still not-tracked and emits
		// the next batch -- draining a large backlog over ticks instead of overflowing the bounded
		// outbound queue in one (the FIX-A per-tick spawn budget).
		spawned := 0

		for _, e := range visible {
			if e == nil || e.id == p.entityID {
				continue // a player never tracks itself
			}
			seen[e.id] = true

			if !p.tracked[e.id] {
				// FIX-A budget gate: if this player already hit its per-tick spawn budget, DEFER this
				// entity -- do NOT emit and do NOT mark it tracked, so the next tick re-picks it. seen[e.id]
				// is already set above, so a deferred (not-yet-spawned) entity is never mistaken for gone.
				if spawned >= maxSpawnsPerTick {
					continue
				}
				// Newly visible: spawn it. AddEntity, then SetEntityData (always — the 0xFF
				// terminator keeps the stream aligned), then SetEntityMotion only if moving.
				p.client.Send(encodeAddEntity(e))
				p.client.Send(encodeSetEntityData(e))
				// Equipment (SetEquipment per populated slot): a skeleton spawns visibly holding its
				// bow. A mob with no equipment (the pig) yields no packets — the byte-identical default.
				for _, ep := range equipmentSpawnPackets(e) {
					p.client.Send(ep)
				}
				if e.vx != 0 || e.vy != 0 || e.vz != 0 {
					p.client.Send(encodeSetEntityMotion(e))
				}
				p.tracked[e.id] = true
				spawned++
				continue
			}

			// Already tracked and still visible: movement is now handled ONCE per entity by
			// tickEntityMovement (the ServerEntity.sendChanges port — delta MoveEntity* packets,
			// or an EntityPositionSync when a delta would overflow / re-anchor). The tracker no
			// longer sends an absolute teleport per observer per tick (the old 5-10× bandwidth
			// deviation); it now only spawns/despawns.
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

// --- OPT-02: the async tracker (off-tick diff, owner-side emission) --------------------------
//
// asyncTracker is the OPT-02 (08-04) executor swapped in behind the UNCHANGED
// `tracker interface{ Tick() }` seam at the single swap-point in NewTickLoop. It computes the
// SAME per-player visibility diff entityTracker does, but the diff MATH runs OFF the tick (in
// the trackerPool ants pool) over an IMMUTABLE snapshot copied on the owner; only the packet
// EMISSION + the p.tracked bookkeeping stay owner-side (in trackerDiffReady.applyTo). The
// synchronous entityTracker above is KEPT as the golden reference the async path is diffed
// against (TestAsyncTrackerMatchesSync).
//
// THE DISCIPLINE (08-RESEARCH Pitfall 3/5): asyncTracker.Tick() does only CHEAP owner work per
// player — it copies near()'s in-range entities into VALUE Entity structs (no live *Entity
// pointer crosses the boundary) and copies p.tracked into a fresh set — then submits the diff
// closure to trackerPool. The worker diffs the snapshot, builds the []pk.Packet + the tracked
// delta, and rejoins via trackerDiffReady on asyncIn2; applyAsyncResults drains it on the owner.
// The worker NEVER reads the live store, NEVER touches p.tracked, and NEVER calls p.client.Send.
type asyncTracker struct {
	loop *TickLoop
}

// Tick is the OWNER-side cheap half of the async tracker (it satisfies the unchanged tracker
// seam). For each connected player it builds an immutable snapshot (value copies of the in-range
// entities + a copy of the player's tracked set + the player's id) and submits the visibility-
// diff MATH to trackerPool. On pool overload (Pitfall 4) the player is skipped this tick — the
// diff recomputes next tick (a one-tick-late visibility update is harmless, the OPT-01 rationale).
// It performs NO send and NO p.tracked mutation: those happen owner-side in trackerDiffReady.applyTo
// after the worker's result is drained.
func (at *asyncTracker) Tick() {
	t := at.loop
	if t == nil {
		return // defensive: a loop without a store has nothing to track
	}

	for _, p := range t.players {
		if p == nil || p.client == nil {
			continue // a player mid-registration / without a connection: skip
		}

		// Broad-phase ON THE OWNER, ACROSS REGIONS (Phase-27 STEP-3, Pitfall 2): near() spans every
		// region the trackRange touches so a player near the seam sees entities across the boundary.
		// The tracker runs at the BARRIER (quiescent), so the cross-region read is -race clean. We
		// immediately copy out ONLY the value fields the diff + encoders need into worker-owned Entity
		// values, so the closure holds NO pointer into the live store (Pitfall 3). The player's own id
		// is skipped here so the snapshot never contains the player's own entity.
		visible := t.entitiesNearAcrossRegions(p.x, p.z, trackRange)
		snap := make([]Entity, 0, len(visible))
		for _, e := range visible {
			if e == nil || e.id == p.entityID {
				continue // a player never tracks itself
			}
			snap = append(snap, snapshotEntity(e))
		}

		// Copy p.tracked into a fresh set the worker reads; p.tracked itself is mutated ONLY on
		// the owner (in applyTo), so it stays a plain map (Pitfall 1).
		trackedCopy := make(map[int32]bool, len(p.tracked))
		for id := range p.tracked {
			trackedCopy[id] = true
		}

		playerID := p.entityID

		// Submit the diff MATH off-tick. On overload, skip this player (recomputes next tick).
		submitOrDrop(t.trackerPool, func() {
			packets, added, removed := computeTrackerDiff(snap, trackedCopy)
			// Nothing changed AND nothing to send → still rejoin with an empty result so the
			// owner-side drain count is predictable; applyTo is a cheap no-op for an empty diff.
			t.asyncIn2 <- trackerDiffReady{
				playerID: playerID,
				packets:  packets,
				added:    added,
				removed:  removed,
			}
		})
	}
}

// snapshotEntity copies the value fields the tracker diff + the entity encoders read into a
// detached Entity value (08-RESEARCH Pitfall 3). It deliberately copies the plain hot fields and
// the metadata byte slice; it does NOT copy the ai pointer (the encoders never touch it, and a
// live *mobAI must not cross the boundary). The result is owner-built and worker-owned — the
// closure that captures it holds no alias into the live store.
func snapshotEntity(e *Entity) Entity {
	cp := Entity{
		id:       e.id,
		typ:      e.typ,
		uuid:     e.uuid,
		x:        e.x,
		y:        e.y,
		z:        e.z,
		vx:       e.vx,
		vy:       e.vy,
		vz:       e.vz,
		yaw:      e.yaw,
		pitch:    e.pitch,
		headYaw:  e.headYaw,
		onGround: e.onGround,
		width:    e.width,
		height:   e.height,
		// Equipment is a VALUE array — a plain struct copy carries it, so the off-tick worker's
		// equipmentSpawnPackets reads the mob's slots without aliasing the live store (Pitfall 3).
		equipment: e.equipment,
	}
	if len(e.metadata) > 0 {
		// Deep-copy the metadata bytes so the worker never aliases the live slot.
		cp.metadata = make([]byte, len(e.metadata))
		copy(cp.metadata, e.metadata)
	}
	return cp
}

// computeTrackerDiff is the PURE diff math, lifted verbatim from entityTracker.Tick to operate
// over a snapshot value slice instead of the live store. It runs OFF the tick (in the worker).
// It produces the same packet sequence the synchronous tracker emits — newly-in-range →
// AddEntity(+SetEntityData(+SetEntityMotion if moving)); still-in-range+tracked → TeleportEntity
// + RotateHead; gone → ONE batched RemoveEntities — plus the tracked DELTA (added/removed ids)
// the owner applies to p.tracked in applyTo. The encoders are PURE over their *Entity arg, so
// taking the address of a worker-owned snapshot value is safe (no live-store alias).
func computeTrackerDiff(snap []Entity, tracked map[int32]bool) (packets []pk.Packet, added, removed []int32) {
	// seen marks which currently-tracked ids are still in range this diff; any tracked id NOT
	// seen has left range and is batched into the single RemoveEntities below.
	seen := make(map[int32]bool, len(snap))

	// spawned counts the NEWLY-tracked entities this diff emits. The async tracker submits ONE
	// diff per player per tick, so bounding spawns per-diff == bounding them per-tick (the FIX-A
	// budget). A deferred entity is simply left OUT of `added` (its packets are not appended and
	// the owner never marks it tracked in applyTo), so the NEXT tick's diff sees it still
	// not-tracked and spawns the next batch -- the same incremental drain the sync tracker does.
	spawned := 0

	for i := range snap {
		e := &snap[i] // worker-owned value; the encoders read it but never retain it
		seen[e.id] = true

		if !tracked[e.id] {
			// FIX-A budget gate: once this diff has emitted maxSpawnsPerTick new spawns, DEFER the
			// rest -- do not append their packets and do not record them in `added`, so applyTo does
			// not mark them tracked and the next tick's diff re-picks them. seen[e.id] is set above,
			// so a deferred entity is never mistaken for gone (no spurious RemoveEntities).
			if spawned >= maxSpawnsPerTick {
				continue
			}
			// Newly visible: spawn it (AddEntity, then SetEntityData always, then SetEntityMotion
			// only if moving) and record the add in the delta.
			packets = append(packets, encodeAddEntity(e))
			packets = append(packets, encodeSetEntityData(e))
			// Equipment (SetEquipment per populated slot) — the worker reads the snapshot's value
			// equipment array (no live-store alias); a no-equipment mob appends nothing.
			packets = append(packets, equipmentSpawnPackets(e)...)
			if e.vx != 0 || e.vy != 0 || e.vz != 0 {
				packets = append(packets, encodeSetEntityMotion(e))
			}
			added = append(added, e.id)
			spawned++
			continue
		}

		// Already tracked and still visible: movement is handled per-entity by tickEntityMovement
		// (ServerEntity.sendChanges — delta MoveEntity* or EntityPositionSync), NOT here. The diff
		// only spawns/despawns now. Membership is unchanged, so the id appears in neither delta list.
	}

	// Anything tracked but no longer in range left the player's view: batch ALL such ids into ONE
	// RemoveEntities (the same single-batch the sync tracker emits) and record them as removed.
	for id := range tracked {
		if !seen[id] {
			removed = append(removed, id)
		}
	}
	if len(removed) > 0 {
		packets = append(packets, encodeRemoveEntities(removed))
	}
	return packets, added, removed
}
