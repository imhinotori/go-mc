package server

// death_mob_test.go — Phase 29 Plan 04, TASK 0 (TDD RED scaffold) for the mob death flow
// (the port of net.minecraft.world.entity.LivingEntity.die). The three behaviors below pin:
//
//   - TestMobDeath_Removal (the HARD deliverable): a mob taken to health<=0 via applyDamageEntity
//     is REMOVED from its OWNING region store, and re-running the death is a guarded no-op (the
//     dead/removed guard prevents double-death). Removal auto-broadcasts RemoveEntities via the
//     tracker (A2: near() no longer returns the gone entity).
//   - TestMobDeath_Loot: dieEntity rolls the mob's entity loot table into Item entities spawned in
//     the OWNER region (count > 0: the pig drops 1-3 porkchop). The drops route through
//     regionForEntity(e), NOT cur() (Pitfall 2).
//   - TestMobDeath_XP: a player-kill spawns at least one experience_orb (id 49) carrying the
//     jar-verified pig xpReward (Animal.getBaseExperienceReward == 1 + random.nextInt(3), so 1-3).
//
// The A4 / loot-context DECISION is encoded in TestMobDeath_Loot's expectation: the entity-loot
// conditions are EXTENDED (bounded) with cited-stub defaults equal to the vanilla v1 state (pig
// never on fire, attacker no looting/smelts_loot enchant), so the UNCONDITIONAL set_count pool
// rolls and the gated furnace_smelt / enchanted_count_increase are faithful no-ops — the pig drops
// 1-3 RAW porkchop. (NOT a documented cut: the handlers are real, just reading the v1 default.)
//
// Fixture: a single-region (region 0) Pig at a region-0 column, added via withRegion so the store
// routing matches production. Death is driven event-style (a lethal applyDamageEntity), exactly as
// production drives it — the pig oracle is never killed in its 500-tick window, so this is
// oracle-safe.

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// allEntityEventStatuses decodes the status byte of every ClientboundEntityEvent in ps, in FIFO
// order. The wire layout is encodeEntityEvent's `pk.Int(entityID), pk.Byte(status)`
// (ClientboundEntityEventPacket: writeInt(entityId) then writeByte(eventId)).
func allEntityEventStatuses(ps []pk.Packet) []int8 {
	var out []int8
	for _, p := range ps {
		if p.ID != int32(packetid.ClientboundEntityEvent) {
			continue
		}
		r := bytes.NewReader(p.Data)
		var id pk.Int
		var ev pk.Byte
		if _, err := (pk.Tuple{&id, &ev}).ReadFrom(r); err != nil {
			continue
		}
		out = append(out, int8(ev))
	}
	return out
}

// lethalPigInRegion0 builds a Pig with a tiny health at a region-0 column and adds it to region 0's
// store via withRegion, returning the mob and its owner region. The id is drawn from the loop's
// idAlloc (NOT a literal) so it never collides with the loot-drop / XP-orb ids dieEntity allocates
// from the SAME idAlloc — exactly as production spawns every entity from the one shared id space.
func lethalPigInRegion0(loop *TickLoop) (*Entity, *region) {
	e := NewEntity(loop.idAlloc.AllocID(), entity.Pig, 8.5, 64, 8.0) // column {0,0} -> region 0
	e.health = 4.0
	owner := loop.regionForColumn(columnOf(e.x, e.z))
	loop.withRegion(owner, func() { loop.cur().entities.add(e) })
	return e, owner
}

// countByType counts entities of a given wire type id in a region store.
func countByType(r *region, typ entity.ID) int {
	n := 0
	for _, e := range r.entities.all() {
		if e != nil && e.typ == typ {
			n++
		}
	}
	return n
}

// TestMobDeath_Removal (the death-animation deliverable): a player-attack hit that takes the pig to
// <=0 does NOT remove it immediately — vanilla die() leaves the corpse in the world and marks it dead
// (deathTime=0), so the client can play the ~1s fall-over animation. The mob is removed ONLY once
// tickDeath() has run 20 ticks (deathTime >= 20). A second dieEntity is a guarded no-op.
//
//	[VERIFIED javap LivingEntity.die has NO remove(); LivingEntity.tickDeath removes at deathTime>=20.]
func TestMobDeath_Removal(t *testing.T) {
	loop, _ := newN2Loop(t)
	mob, owner := lethalPigInRegion0(loop)

	if _, ok := owner.entities.get(mob.id); !ok {
		t.Fatalf("precondition: mob must be in its owner region store before death")
	}

	src := damageSourcePlayerAttack(42)
	loop.withRegion(owner, func() { loop.applyDamageEntity(mob, src, 100.0) }) // lethal

	// At death-time 0 the corpse MUST still be in the store (die() does not remove) so trackers keep
	// sending it and the client plays the fall-over. It is marked dead with deathTime seeded to 0.
	if _, ok := owner.entities.get(mob.id); !ok {
		t.Fatalf("mob removed immediately on death — die() must leave the corpse in the world for the death animation")
	}
	if !mob.dead {
		t.Fatalf("dead flag not set after lethal hit")
	}
	if mob.deathTime != 0 {
		t.Fatalf("deathTime = %d after die(), want 0 (the animation countdown is seeded fresh)", mob.deathTime)
	}

	// Tick the death animation: it must STILL be present for ticks 1..19, then removed exactly at 20.
	for i := int32(1); i < deathAnimationTicks; i++ {
		loop.withRegion(owner, func() { loop.tickDeath(mob) })
		if _, ok := owner.entities.get(mob.id); !ok {
			t.Fatalf("mob removed at deathTime %d — must survive until deathTime >= %d", i, deathAnimationTicks)
		}
	}
	// The 20th tickDeath takes deathTime to 20 -> poof + remove.
	loop.withRegion(owner, func() { loop.tickDeath(mob) })
	if _, ok := owner.entities.get(mob.id); ok {
		t.Fatalf("dead mob still in the owner region store at deathTime %d — tickDeath must remove at >= %d", mob.deathTime, deathAnimationTicks)
	}

	// Guarded double-death: re-running dieEntity must not panic / not re-spawn / not re-remove.
	loop.withRegion(owner, func() { loop.dieEntity(mob, src) })
}

// TestMobDeath_Poof (the status-60 deliverable): when tickDeath removes the mob at deathTime>=20 it
// FIRST broadcasts the death-poof entity event (status 60) to the still-tracking players, distinct
// from the status-3 death-animation start that die() broadcasts on the killing blow. This pins the
// full death sequence: status-3 at death (still present), status-60 + removal at deathTime 20.
//
//	[VERIFIED javap LivingEntity.tickDeath: broadcastEntityEvent(this, 60); remove(KILLED).]
func TestMobDeath_Poof(t *testing.T) {
	loop, _ := newN2Loop(t)
	mob, owner := lethalPigInRegion0(loop)

	viewer := &tickPlayer{client: captureClient(64), entityID: 1000, tracked: map[int32]bool{mob.id: true}}
	loop.players = append(loop.players, viewer)

	src := damageSourcePlayerAttack(42)
	loop.withRegion(owner, func() { loop.applyDamageEntity(mob, src, 100.0) }) // lethal

	// Drive the full death animation: 19 ticks where deathTime climbs 1..19 (no poof yet), then the
	// 20th tick (deathTime -> 20) which broadcasts the status-60 poof and removes the mob. The viewer's
	// outbound queue is drained ONCE at the END (drainPackets closes the queue, so it is single-use):
	// the whole sequence's broadcasts (the status-3 death animation on the kill, then the status-60
	// poof at 20) accumulate in FIFO order for a single assertion.
	for i := int32(1); i <= deathAnimationTicks; i++ {
		loop.withRegion(owner, func() { loop.tickDeath(mob) })
	}

	// Removal happened exactly at deathTime >= 20.
	if _, ok := owner.entities.get(mob.id); ok {
		t.Fatalf("mob still in the store after the death animation (deathTime %d) — tickDeath must remove at >= %d", mob.deathTime, deathAnimationTicks)
	}

	got := drainPackets(viewer.client)
	// Exactly two entity events fire across the death: the status-3 death-animation start (die()) and
	// the status-60 poof (tickDeath at 20). No event fires on ticks 1..19.
	statuses := allEntityEventStatuses(got)
	if len(statuses) != 2 {
		t.Fatalf("death broadcast %d ClientboundEntityEvent packets %v, want exactly 2 (status-3 then status-60)", len(statuses), statuses)
	}
	if statuses[0] != int8(entityEventDeath) {
		t.Fatalf("first entity-event status = %d, want %d (death-animation start, broadcast by die())", statuses[0], entityEventDeath)
	}
	if statuses[1] != int8(entityEventDeathPoof) {
		t.Fatalf("second entity-event status = %d, want %d (death poof, broadcast by tickDeath at deathTime>=%d)", statuses[1], entityEventDeathPoof, deathAnimationTicks)
	}
}

// TestMobDeath_Loot: a lethal hit spawns Item entities (the rolled pig loot) into the OWNER region.
// The pig table drops 1-3 porkchop -> at least one Item entity appears in region 0's store.
func TestMobDeath_Loot(t *testing.T) {
	loop, _ := newN2Loop(t)
	mob, owner := lethalPigInRegion0(loop)

	itemsBefore := countByType(owner, entity.Item.ID)

	src := damageSourcePlayerAttack(42)
	loop.withRegion(owner, func() { loop.applyDamageEntity(mob, src, 100.0) }) // lethal

	itemsAfter := countByType(owner, entity.Item.ID)
	if itemsAfter <= itemsBefore {
		t.Fatalf("death loot: no Item entities spawned in the owner region (before=%d after=%d) — the pig drops 1-3 porkchop",
			itemsBefore, itemsAfter)
	}
}

// TestMobDeath_XP: a PLAYER kill spawns at least one experience_orb (id 49) in the owner region (the
// dropExperience player-kill gate). The reward is the jar pig value (1 + random.nextInt(3) = 1-3),
// which ExperienceOrb.award splits into >=1 orb.
func TestMobDeath_XP(t *testing.T) {
	loop, _ := newN2Loop(t)
	mob, owner := lethalPigInRegion0(loop)

	orbsBefore := countByType(owner, entity.ExperienceOrb.ID)

	src := damageSourcePlayerAttack(42) // a player attack -> the kill gate passes
	loop.withRegion(owner, func() { loop.applyDamageEntity(mob, src, 100.0) }) // lethal

	orbsAfter := countByType(owner, entity.ExperienceOrb.ID)
	if orbsAfter <= orbsBefore {
		t.Fatalf("death XP: no experience_orb spawned on a player kill (before=%d after=%d) — pig xpReward is 1-3",
			orbsBefore, orbsAfter)
	}
}

// TestMobDeath_XP_NoPlayerNoOrb: an environmental (non-player) lethal source does NOT award XP
// (the lastHurtByPlayer gate, modeled by the player-attack proxy). Pins the gate is real, not always-on.
func TestMobDeath_XP_NoPlayerNoOrb(t *testing.T) {
	loop, _ := newN2Loop(t)
	mob, owner := lethalPigInRegion0(loop)

	orbsBefore := countByType(owner, entity.ExperienceOrb.ID)

	// A non-player source (fall): attacker 0, not a player attack -> no XP.
	src := damageSource{typeTag: damageTypeFall, attacker: 0}
	loop.withRegion(owner, func() { loop.applyDamageEntity(mob, src, 100.0) }) // lethal

	orbsAfter := countByType(owner, entity.ExperienceOrb.ID)
	if orbsAfter != orbsBefore {
		t.Fatalf("death XP: a non-player kill awarded XP (before=%d after=%d) — the player-kill gate must hold",
			orbsBefore, orbsAfter)
	}
}
