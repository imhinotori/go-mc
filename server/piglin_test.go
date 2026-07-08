package server

// piglin_test.go -- deterministic pins for the Piglin (net.minecraft.world.entity.monster.piglin.Piglin,
// 1:1 javap this session). Verifies the spawn attributes (MAX_HEALTH 16 / ATTACK_DAMAGE 5 / MOVEMENT_SPEED
// 0.35 / FOLLOW_RANGE 16), the gold-armor NEUTRALITY (a gold-armored player is never targeted, a bare
// player IS), the off-nether ZOMBIFICATION timer (300 ticks -> zombified_piglin), the gold-ingot BARTER
// (a right-click with a gold ingot consumes 1 ingot and drops a piglin_bartering roll), and the baby flag.

import (
	"math"
	"testing"

	pk "github.com/imhinotori/sulfur/net/packet"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/world"
)

// piglinLoop builds a physics loop with a stone floor + chunk, ready to spawn + tick piglins.
func piglinLoop(t *testing.T) (*TickLoop, *world.ChunkManager, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, mgr, floorY
}

// piglinItemEntityCount counts the live item entities in the global region (the barter drop check).
func piglinItemEntityCount(loop *TickLoop) int {
	n := 0
	for _, e := range loop.regions[globalRegion].entities.byID {
		if e.isItem {
			n++
		}
	}
	return n
}

// TestPiglinSpawnDefaults: spawnPiglin builds a piglin rendering as entity.Piglin.ID with the jar attributes
// (MAX_HEALTH 16 -> health 16, ATTACK_DAMAGE 5.0, MOVEMENT_SPEED 0.3499999940395355, FOLLOW_RANGE 16.0), a
// per-entity rng, and an attached (bounded) brain.
func TestPiglinSpawnDefaults(t *testing.T) {
	loop, _, floorY := piglinLoop(t)
	pg := loop.spawnPiglin(8.5, float64(floorY+1), 8.5, false)
	if pg.typ != entity.Piglin.ID {
		t.Fatalf("piglin typ = %d, want entity.Piglin.ID %d", pg.typ, entity.Piglin.ID)
	}
	if !pg.isPiglin {
		t.Fatal("piglin not marked isPiglin")
	}
	if math.Abs(float64(pg.health)-16.0) > 1e-6 {
		t.Fatalf("piglin health = %v, want 16.0 (MAX_HEALTH)", pg.health)
	}
	if got := pg.getAttributeValue(attribute.MaxHealth); math.Abs(got-16.0) > 1e-9 {
		t.Fatalf("piglin MAX_HEALTH = %v, want 16.0", got)
	}
	if got := pg.getAttributeValue(attribute.AttackDamage); math.Abs(got-5.0) > 1e-9 {
		t.Fatalf("piglin ATTACK_DAMAGE = %v, want 5.0", got)
	}
	if got := pg.getAttributeValue(attribute.MovementSpeed); math.Abs(got-0.3499999940395355) > 1e-12 {
		t.Fatalf("piglin MOVEMENT_SPEED = %v, want 0.3499999940395355", got)
	}
	if got := pg.getAttributeValue(attribute.FollowRange); math.Abs(got-16.0) > 1e-9 {
		t.Fatalf("piglin FOLLOW_RANGE = %v, want 16.0", got)
	}
	if pg.ai == nil || pg.ai.rng == nil {
		t.Fatal("piglin has no minimal AI / rng")
	}
	if pg.brain == nil {
		t.Fatal("piglin has no attached brain (attachPiglinBrain)")
	}
	if pg.isBaby() {
		t.Fatal("a spawnPiglin(baby=false) is an adult, but isBaby() is true")
	}
}

// TestPiglinBabyFlag: spawnPiglin(baby=true) sets the DATA_BABY_ID (breedAge<0 -> isBaby()), and a baby is
// NOT an adult (piglinIsAdult false -> no hunt/attack).
func TestPiglinBabyFlag(t *testing.T) {
	loop, _, floorY := piglinLoop(t)
	baby := loop.spawnPiglin(8.5, float64(floorY+1), 8.5, true)
	if !baby.isBaby() {
		t.Fatal("spawnPiglin(baby=true) is not a baby (isBaby() false)")
	}
	if baby.piglinIsAdult() {
		t.Fatal("a baby piglin reports piglinIsAdult() true (must be false -- babies do not hunt)")
	}
}

// TestPiglinNeutralToGoldArmor: an adult piglin is NEUTRAL to a player wearing gold armor (never targeted)
// and HOSTILE to a bare player (targeted within FOLLOW_RANGE). The gold-armored player is skipped by the
// isWearingSafeArmor gate (PiglinSpecificSensor).
func TestPiglinNeutralToGoldArmor(t *testing.T) {
	loop, _, floorY := piglinLoop(t)
	py := float64(floorY + 1)

	// A BARE player 5 blocks away -> the piglin acquires it (hostility).
	bare := combatTestPlayer(loop, 13.5, py, 8.5, 7001)
	pg := loop.spawnPiglin(8.5, py, 8.5, false)
	loop.piglinAcquireNearestPlayer(pg)
	if pg.ai.attackTargetID != bare.entityID {
		t.Fatalf("piglin did not target the bare player (target=%d, want %d)", pg.ai.attackTargetID, bare.entityID)
	}

	// Now the SAME player dons a golden helmet -> the piglin drops the target (neutrality mid-scan too).
	inv := ensureInventory(bare)
	inv.set(5, component.SlotData{Count: 1, ItemID: pk.VarInt(item.GoldenHelmet.ID)}) // head armor slot
	if !piglinPlayerWearsGold(bare) {
		t.Fatal("piglinPlayerWearsGold false for a player wearing a golden helmet")
	}
	loop.piglinAcquireNearestPlayer(pg)
	if pg.ai.attackTargetID != 0 {
		t.Fatalf("piglin still targets a gold-armored player (target=%d, want 0 -- neutrality)", pg.ai.attackTargetID)
	}

	// A fresh piglin near ONLY the gold-armored player never acquires it.
	pg2 := loop.spawnPiglin(9.5, py, 8.5, false)
	loop.piglinAcquireNearestPlayer(pg2)
	if pg2.ai.attackTargetID != 0 {
		t.Fatalf("piglin acquired a gold-armored player (target=%d, want 0)", pg2.ai.attackTargetID)
	}
}

// TestPiglinMeleeHurtsBareTarget: an adult piglin adjacent to a bare player deals ATTACK_DAMAGE (5.0) on the
// melee swing (the fight-activity melee reduced to the MeleeAttackGoal adjacency shape).
func TestPiglinMeleeHurtsBareTarget(t *testing.T) {
	loop, _, floorY := piglinLoop(t)
	py := float64(floorY + 1)
	victim := combatTestPlayer(loop, 8.9, py, 8.5, 7010) // ~0.4 away -> within melee range (d LT 4.0)
	victim.health = 20.0
	pg := loop.spawnPiglin(8.5, py, 8.5, false)
	pg.ai.attackTargetID = victim.entityID
	pg.piglinAttackTime = 0 // swing ready
	loop.piglinMeleeGoalTick(pg)
	dealt := 20.0 - float64(victim.health)
	if math.Abs(dealt-5.0) > 1e-6 {
		t.Fatalf("piglin melee dealt %v damage, want 5.0 (ATTACK_DAMAGE)", dealt)
	}
}

// TestZombificationOffNether: a piglin off the nether (piglinInNether false) increments timeInOverworld each
// tick and, once past CONVERSION_TIME (300), convertTo's a zombified_piglin (the type-swap removes the
// piglin from the store and adds a fresh entity.ZombifiedPiglin). A piglin IN the nether never converts.
func TestZombificationOffNether(t *testing.T) {
	loop, _, floorY := piglinLoop(t)
	py := float64(floorY + 1)

	// OFF the nether (default): the timer runs and converts at > 300.
	pg := loop.spawnPiglin(8.5, py, 8.5, false)
	pgID := pg.id
	loop.withRegion(loop.regions[globalRegion], func() {
		for i := 0; i < piglinConversionTime; i++ {
			loop.piglinZombificationTick(pg)
		}
	})
	if pg.piglinTimeInOverworld != piglinConversionTime {
		t.Fatalf("after %d ticks off-nether, timeInOverworld = %d, want %d", piglinConversionTime, pg.piglinTimeInOverworld, piglinConversionTime)
	}
	// The 301st tick crosses > 300 -> finishConversion.
	loop.withRegion(loop.regions[globalRegion], func() { loop.piglinZombificationTick(pg) })
	if _, present := loop.regions[globalRegion].entities.get(pgID); present {
		t.Fatal("piglin still present after 300+ ticks off-nether (finishConversion did not remove it)")
	}
	zp := false
	for _, e := range loop.regions[globalRegion].entities.byID {
		if e.typ == entity.ZombifiedPiglin.ID {
			zp = true
			break
		}
	}
	if !zp {
		t.Fatal("no zombified_piglin present after the conversion")
	}

	// IN the nether: the timer stays 0, no conversion.
	nether := loop.spawnPiglin(20.5, py, 20.5, false)
	nether.piglinInNether = true
	loop.withRegion(loop.regions[globalRegion], func() {
		for i := 0; i < piglinConversionTime+10; i++ {
			loop.piglinZombificationTick(nether)
		}
	})
	if nether.piglinTimeInOverworld != 0 {
		t.Fatalf("in-nether piglin timeInOverworld = %d, want 0 (no zombification in the nether)", nether.piglinTimeInOverworld)
	}
	if _, present := loop.regions[globalRegion].entities.get(nether.id); !present {
		t.Fatal("in-nether piglin was converted (must NOT zombify in the nether)")
	}
}

// TestBarterGoldIngotDrops: right-clicking an adult piglin with a gold ingot consumes 1 ingot from the
// player hand and drops a piglin_bartering roll (>= 1 item entity spawned). A non-gold-ingot item does not
// barter; a baby piglin does not barter.
func TestBarterGoldIngotDrops(t *testing.T) {
	loop, _, floorY := piglinLoop(t)
	py := float64(floorY + 1)
	pg := loop.spawnPiglin(8.5, py, 8.5, false)

	// Give the player a stack of gold ingots in the selected hotbar slot.
	p := combatTestPlayer(loop, 9.0, py, 8.5, 7020)
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(inv.heldSlot), component.SlotData{Count: 4, ItemID: pk.VarInt(item.GoldIngot.ID)})

	before := piglinItemEntityCount(loop)
	var ok bool
	loop.withRegion(loop.regions[globalRegion], func() { ok = loop.piglinMobInteract(p, pg) })
	if !ok {
		t.Fatal("piglinMobInteract returned false for a gold-ingot barter (want true -- consumed)")
	}
	// The gold ingot stack shrank by 1 (consumeAndReturn).
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if int(held.Count) != 3 {
		t.Fatalf("gold ingot stack = %d after barter, want 3 (consumed 1)", held.Count)
	}
	// At least one barter item entity dropped.
	after := piglinItemEntityCount(loop)
	if after <= before {
		t.Fatalf("no barter item dropped (item entities before=%d after=%d)", before, after)
	}

	// A NON-gold-ingot item does not barter.
	p2 := combatTestPlayer(loop, 9.0, py, 8.5, 7021)
	inv2 := ensureInventory(p2)
	inv2.set(heldWindowSlot(inv2.heldSlot), component.SlotData{Count: 1, ItemID: pk.VarInt(item.Stick.ID)})
	var ok2 bool
	loop.withRegion(loop.regions[globalRegion], func() { ok2 = loop.piglinMobInteract(p2, pg) })
	if ok2 {
		t.Fatal("piglinMobInteract returned true for a non-gold-ingot item (want false -- not a barter)")
	}

	// A BABY piglin does not barter (canAdmire requires isAdult()).
	baby := loop.spawnPiglin(30.5, py, 30.5, true)
	p3 := combatTestPlayer(loop, 31.0, py, 30.5, 7022)
	inv3 := ensureInventory(p3)
	inv3.set(heldWindowSlot(inv3.heldSlot), component.SlotData{Count: 1, ItemID: pk.VarInt(item.GoldIngot.ID)})
	var ok3 bool
	loop.withRegion(loop.regions[globalRegion], func() { ok3 = loop.piglinMobInteract(p3, baby) })
	if ok3 {
		t.Fatal("a baby piglin bartered (want false -- canAdmire requires isAdult())")
	}
}
