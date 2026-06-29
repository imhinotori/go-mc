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
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

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

// TestMobDeath_Removal: a player-attack hit that takes the pig to <=0 removes it from its owning
// region store (the HARD deliverable). A second dieEntity is a guarded no-op (no double-removal).
func TestMobDeath_Removal(t *testing.T) {
	loop, _ := newN2Loop(t)
	mob, owner := lethalPigInRegion0(loop)

	if _, ok := owner.entities.get(mob.id); !ok {
		t.Fatalf("precondition: mob must be in its owner region store before death")
	}

	src := damageSourcePlayerAttack(42)
	loop.withRegion(owner, func() { loop.applyDamageEntity(mob, src, 100.0) }) // lethal

	if _, ok := owner.entities.get(mob.id); ok {
		t.Fatalf("dead mob still in the owner region store — death REMOVAL (the hard deliverable) failed")
	}

	// Guarded double-death: re-running dieEntity must not panic / not re-spawn / not re-remove.
	loop.withRegion(owner, func() { loop.dieEntity(mob, src) })
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
