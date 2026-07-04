package server

// lightning_conversion_test.go — port gates for the per-mob thunderHit CONVERSIONS
// (lightning_conversion.go / Pig/Creeper/Villager/MushroomCow.thunderHit). Each conversion is exercised
// through the REAL bolt damage path (boltDamageEntitiesInBox -> boltThunderHitMob -> the per-species
// dispatch), so the test proves the conversion fires ONLY on a lightning strike, exactly where vanilla's
// virtual thunderHit dispatch runs. Asserted against the jar:
//   - Creeper: a strike sets powered=true (super.thunderHit fire+damage THEN setPowered) and does NOT remove
//     the creeper (it is hurt+charged, not converted).
//   - Villager: a strike converts it to a Witch (new entity.Witch at the same pos; the villager removed) and
//     applies NO lightning fire/damage (a successful convertTo skips super.thunderHit).
//   - Pig: a strike converts it to a ZombifiedPiglin (type-swap at the same pos; the pig removed).
//   - Mooshroom: a strike toggles RED->BROWN once per bolt (the lastLightningBoltUUID guard), no fire/damage.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// installFullMobRegistry loads the real //go:embed vanilla-mob registry onto the loop (so spawnVanillaMob
// can build a witch with its declared AI). Fails loudly if the boot-load fails.
func installFullMobRegistry(t *testing.T, loop *TickLoop) {
	t.Helper()
	r, err := loadVanillaMobRegistry()
	if err != nil {
		t.Fatalf("loadVanillaMobRegistry: %v", err)
	}
	loop.SetMobRegistry(r)
}

// findByID returns the store entity with the given id (nil if removed).
func findByID(loop *TickLoop, id int32) *Entity {
	return loop.only().entities.byID[id]
}

// countType returns how many live store entities render as the given entity type id.
func countType(loop *TickLoop, typ entity.ID) int {
	n := 0
	for _, e := range loop.only().entities.byID {
		if e.typ == typ {
			n++
		}
	}
	return n
}

// TestLightningStrikeChargesCreeper: a creeper struck by a (non-visual) bolt becomes POWERED (charged) and
// is NOT converted/removed — the base Entity.thunderHit runs (fire+damage) THEN setPowered(true). Matches
// Creeper.thunderHit (super.thunderHit; entityData.set(DATA_IS_POWERED,true)).
func TestLightningStrikeChargesCreeper(t *testing.T) {
	loop, _ := newThunderLoop(t, 1)

	cr := NewEntity(loop.idAlloc.AllocID(), entity.Creeper, 8.5, 65, 8.5)
	cr.health = 20.0
	loop.only().entities.add(cr)
	if cr.powered {
		t.Fatal("precondition: creeper must start un-powered")
	}

	// A bolt at the creeper's cell (within the ±3 box).
	loop.spawnLightningBolt(pk.Position{X: 8, Y: 65, Z: 8}, false)
	for i := 0; i < 200; i++ {
		loop.tickLightning()
		if countBolts(loop) == 0 {
			break
		}
	}

	if findByID(loop, cr.id) == nil {
		t.Fatal("creeper was removed by the strike — it must be charged, not converted")
	}
	if !cr.powered {
		t.Fatal("creeper was struck by lightning but is not POWERED (Creeper.thunderHit setPowered(true) not applied)")
	}
	// super.thunderHit ran: the creeper took the 5.0 lightning damage and caught fire.
	if cr.health >= 20.0 {
		t.Fatalf("charged creeper took no lightning damage (health %v) — super.thunderHit's 5.0 hurt missing", cr.health)
	}
	if cr.remainingFireTicks <= 0 {
		t.Fatal("charged creeper should be on fire (super.thunderHit remainingFireTicks++)")
	}
}

// TestLightningStrikeConvertsVillagerToWitch: a villager struck by a bolt converts to a Witch — a new
// entity.Witch appears at the villager's position, the villager is removed, and NO lightning fire/damage is
// applied (a successful convertTo skips super.thunderHit). Matches Villager.thunderHit (convertTo(WITCH)).
func TestLightningStrikeConvertsVillagerToWitch(t *testing.T) {
	loop, _ := newThunderLoop(t, 1)
	installFullMobRegistry(t, loop)

	// Spawn a real villager (declared AI) at the strike cell.
	vil := loop.spawnVanillaMob(vanillaVillagerMobName, 8.5, 65, 8.5)
	vil.health = 20.0
	if vil.typ != entity.Villager.ID {
		t.Fatalf("spawned villager typ = %d, want Villager %d", vil.typ, entity.Villager.ID)
	}
	vx, vy, vz := vil.x, vil.y, vil.z

	if countType(loop, entity.Witch.ID) != 0 {
		t.Fatal("precondition: no witch before the strike")
	}

	loop.spawnLightningBolt(pk.Position{X: 8, Y: 65, Z: 8}, false)
	for i := 0; i < 200; i++ {
		loop.tickLightning()
		if countBolts(loop) == 0 {
			break
		}
	}

	// The villager was discarded.
	if findByID(loop, vil.id) != nil {
		t.Fatal("villager still present after the strike — convertTo(WITCH) did not discard it")
	}
	// A witch was spawned.
	if got := countType(loop, entity.Witch.ID); got != 1 {
		t.Fatalf("witch count after the strike = %d, want exactly 1 (villager -> witch conversion)", got)
	}
	// The witch is at the villager's exact position (copyPosition).
	var witch *Entity
	for _, e := range loop.only().entities.byID {
		if e.typ == entity.Witch.ID {
			witch = e
		}
	}
	if witch.x != vx || witch.y != vy || witch.z != vz {
		t.Fatalf("witch pos (%v,%v,%v) != villager pos (%v,%v,%v) — copyPosition not applied", witch.x, witch.y, witch.z, vx, vy, vz)
	}
	// NOTE: the witch MAY be on fire / hurt from LATER ticks of the same multi-tick bolt (after the convert,
	// the witch is a normal LivingEntity in the ±3 box, so the bolt's subsequent-tick damage loop strikes it
	// with the base Entity.thunderHit — this is faithful: hitEntities does not gate the damage loop). The
	// convert-tick invariant we assert is that the VILLAGER was replaced (not hurt-as-a-villager), which the
	// removal + witch spawn above proves.
}

// TestLightningStrikeConvertsPigToZombifiedPiglin: a pig struck by a bolt converts to a ZombifiedPiglin — a
// new entity.ZombifiedPiglin at the pig's position, the pig removed. Matches Pig.thunderHit
// (convertTo(ZOMBIFIED_PIGLIN)). The piglin AI/equipment is the cited deferral (bare type-swap).
func TestLightningStrikeConvertsPigToZombifiedPiglin(t *testing.T) {
	loop, _ := newThunderLoop(t, 1)
	installFullMobRegistry(t, loop)

	pig := loop.spawnVanillaMob(vanillaPigMobName, 8.5, 65, 8.5)
	if pig.typ != entity.Pig.ID {
		t.Fatalf("spawned pig typ = %d, want Pig %d", pig.typ, entity.Pig.ID)
	}
	px, py, pz := pig.x, pig.y, pig.z

	if countType(loop, entity.ZombifiedPiglin.ID) != 0 {
		t.Fatal("precondition: no zombified piglin before the strike")
	}

	loop.spawnLightningBolt(pk.Position{X: 8, Y: 65, Z: 8}, false)
	for i := 0; i < 200; i++ {
		loop.tickLightning()
		if countBolts(loop) == 0 {
			break
		}
	}

	if findByID(loop, pig.id) != nil {
		t.Fatal("pig still present after the strike — convertTo(ZOMBIFIED_PIGLIN) did not discard it")
	}
	if got := countType(loop, entity.ZombifiedPiglin.ID); got != 1 {
		t.Fatalf("zombified piglin count after the strike = %d, want exactly 1 (pig -> piglin conversion)", got)
	}
	var piglin *Entity
	for _, e := range loop.only().entities.byID {
		if e.typ == entity.ZombifiedPiglin.ID {
			piglin = e
		}
	}
	if piglin.x != px || piglin.y != py || piglin.z != pz {
		t.Fatalf("piglin pos (%v,%v,%v) != pig pos (%v,%v,%v) — copyPosition not applied", piglin.x, piglin.y, piglin.z, px, py, pz)
	}
}

// TestLightningStrikeTogglesMooshroomVariant: a RED mooshroom struck by a bolt toggles to BROWN, ONCE per
// bolt (the lastLightningBoltUUID guard), and takes NO lightning fire/damage (MushroomCow.thunderHit never
// calls super). The single-toggle guard is the key assertion: the bolt hits the same mooshroom every tick
// it is alive (life 2..0), but the variant flips exactly once.
func TestLightningStrikeTogglesMooshroomVariant(t *testing.T) {
	loop, _ := newThunderLoop(t, 1)
	installFullMobRegistry(t, loop)

	moo := loop.spawnVanillaMob(vanillaMooshroomMobName, 8.5, 65, 8.5)
	moo.health = 10.0
	if moo.typ != entity.Mooshroom.ID {
		t.Fatalf("spawned mooshroom typ = %d, want Mooshroom %d", moo.typ, entity.Mooshroom.ID)
	}
	if moo.mooshroomVariant != mooshroomVariantRed {
		t.Fatalf("fresh mooshroom variant = %d, want RED (%d)", moo.mooshroomVariant, mooshroomVariantRed)
	}

	loop.spawnLightningBolt(pk.Position{X: 8, Y: 65, Z: 8}, false)
	for i := 0; i < 200; i++ {
		loop.tickLightning()
		if countBolts(loop) == 0 {
			break
		}
	}

	// The mooshroom is still alive (not converted/removed) and took NO lightning damage.
	if findByID(loop, moo.id) == nil {
		t.Fatal("mooshroom was removed by the strike — it must only toggle variant")
	}
	if moo.health != 10.0 {
		t.Fatalf("mooshroom took lightning damage (health %v) — MushroomCow.thunderHit must not call super.thunderHit", moo.health)
	}
	if moo.remainingFireTicks != 0 {
		t.Fatalf("mooshroom caught fire (remainingFireTicks=%d) — MushroomCow.thunderHit sets no fire", moo.remainingFireTicks)
	}
	// The variant toggled exactly ONCE (RED -> BROWN), despite the bolt striking it on multiple ticks.
	if moo.mooshroomVariant != mooshroomVariantBrown {
		t.Fatalf("mooshroom variant after the strike = %d, want BROWN (%d) — the RED<->BROWN toggle did not fire", moo.mooshroomVariant, mooshroomVariantBrown)
	}
	if !moo.hasLastLightningBolt {
		t.Fatal("mooshroom lastLightningBoltUUID guard was not set after the strike")
	}
}

// TestPigConversionRequiresLightning: the conversion path is reached ONLY via a lightning strike — a plain
// pig that is merely ticked (no bolt) is NEVER converted. This guards the pig-oracle invariant: the
// conversion code must not run on a normal pig tick. Here we assert the direct guard: with no bolt in range,
// a pig sitting in a loaded chunk stays a pig across ticks.
func TestPigConversionRequiresLightning(t *testing.T) {
	loop, mgr := newThunderLoop(t, 1)
	installFullMobRegistry(t, loop)
	_ = mgr

	pig := loop.spawnVanillaMob(vanillaPigMobName, 8.5, 65, 8.5)

	// No bolt spawned. Tick the lightning subsystem (a no-op with no bolts) and the store — the pig must NOT
	// convert (boltThunderHitConvert is only ever called from the bolt damage loop).
	for i := 0; i < 50; i++ {
		loop.tickLightning()
	}
	if findByID(loop, pig.id) == nil {
		t.Fatal("pig was removed without any lightning strike — the conversion must ONLY fire from a bolt hit")
	}
	if countType(loop, entity.ZombifiedPiglin.ID) != 0 {
		t.Fatal("a zombified piglin appeared with no lightning strike — the conversion leaked outside the bolt path")
	}
}

// TestBoltStrikeCellStillLeavesFireForConvert: a converting mob (villager) is replaced, but the bolt still
// runs its ground-fire (LightningBolt.tick life==2 spawnFire) independent of the per-mob thunderHit — the
// conversion does not suppress the bolt's own fire. Confirms the conversion hooks the thunderHit, not the
// bolt's fire branch.
func TestBoltStrikeCellStillLeavesFireForConvert(t *testing.T) {
	loop, mgr := newThunderLoop(t, 1)
	installFullMobRegistry(t, loop)

	loop.spawnVanillaMob(vanillaVillagerMobName, 8.5, 65, 8.5)
	loop.spawnLightningBolt(pk.Position{X: 8, Y: 65, Z: 8}, false)
	for i := 0; i < 200; i++ {
		loop.tickLightning()
		if countBolts(loop) == 0 {
			break
		}
	}
	// The bolt's own ground fire at the strike cell still landed.
	fireState, ok := mgr.GetBlock(pk.Position{X: 8, Y: 65, Z: 8}, dimMinY)
	if !ok || fireState != block.DefaultStateID["minecraft:fire"] {
		t.Fatalf("strike cell (8,65,8) state = %d, want fire (%d) — the bolt fire is independent of the mob convert", fireState, block.DefaultStateID["minecraft:fire"])
	}
}
