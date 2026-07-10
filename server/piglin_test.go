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
	"github.com/imhinotori/sulfur/world/levelgen"
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

// TestPiglinMeleeUsesHitboxReach: the piglin melee now gates on Mob.isWithinMeleeAttackRange (the inflated
// attack-box vs target-hitbox intersection ~1.43 blocks for two 0.6-wide entities), NOT a fixed 2.0-block
// (dist^2 <= 4.0) proxy. A target at 1.7 blocks -- inside the old 4.0-sq proxy but OUTSIDE the true reach --
// must NOT be hit; a target at 0.9 blocks (inside reach) IS hit. Cite Mob.isWithinMeleeAttackRange.
func TestPiglinMeleeUsesHitboxReach(t *testing.T) {
	loop, _, floorY := piglinLoop(t)
	py := float64(floorY + 1)

	// 1.7 blocks away: dist^2 = 2.89 <= 4.0 (old proxy would swing) but > the ~1.43-block hitbox reach.
	far := combatTestPlayer(loop, 8.5+1.7, py, 8.5, 7060)
	far.health = 20.0
	pgFar := loop.spawnPiglin(8.5, py, 8.5, false)
	pgFar.ai.attackTargetID = far.entityID
	pgFar.piglinAttackTime = 0
	loop.piglinMeleeGoalTick(pgFar)
	if math.Abs(float64(far.health)-20.0) > 1e-6 {
		t.Fatalf("piglin hit a target 1.7 blocks away (dealt %v) -- must use hitbox reach, not the 2.0-block proxy", 20.0-far.health)
	}

	// 0.9 blocks away: within the hitbox reach -> hit.
	near := combatTestPlayer(loop, 8.5+0.9, py, 8.5, 7061)
	near.health = 20.0
	pgNear := loop.spawnPiglin(8.5, py, 8.5, false)
	pgNear.ai.attackTargetID = near.entityID
	pgNear.piglinAttackTime = 0
	loop.piglinMeleeGoalTick(pgNear)
	if math.Abs((20.0-float64(near.health))-5.0) > 1e-6 {
		t.Fatalf("piglin did NOT hit a target 0.9 blocks away (dealt %v, want 5.0) -- inside hitbox reach", 20.0-near.health)
	}
}

// TestPiglinAngersOnHitAndRetaliates: hitting an adult piglin (Piglin.hurtServer -> PiglinAi.wasHurtBy ->
// maybeRetaliate -> setAngerTarget) sets ANGRY_AT to the attacker (600t) and the FIGHT attackTargetID, even
// when the attacker WEARS GOLD ARMOR (ANGRY_AT bypasses the gold-safe sensor). Cite PiglinAi.wasHurtBy.
func TestPiglinAngersOnHitAndRetaliates(t *testing.T) {
	loop, _, floorY := piglinLoop(t)
	py := float64(floorY + 1)
	// A GOLD-ARMORED attacker: normally NEUTRAL (never sensor-targeted), but a hit angers the piglin anyway.
	attacker := combatTestPlayer(loop, 9.0, py, 8.5, 7030)
	inv := ensureInventory(attacker)
	inv.set(5, component.SlotData{Count: 1, ItemID: pk.VarInt(item.GoldenHelmet.ID)})
	if !piglinPlayerWearsGold(attacker) {
		t.Fatal("test setup: attacker should wear gold")
	}
	pg := loop.spawnPiglin(8.5, py, 8.5, false)
	// Sanity: an un-hit piglin does NOT target a gold-armored player (neutrality).
	loop.piglinAcquireNearestPlayer(pg)
	if pg.ai.attackTargetID != 0 {
		t.Fatalf("un-hit piglin targeted a gold-armored player (%d), want 0", pg.ai.attackTargetID)
	}
	// Hit it with a player attack.
	loop.withRegion(loop.regions[globalRegion], func() {
		loop.applyDamageEntity(pg, damageSourcePlayerAttack(attacker.entityID), 1.0)
	})
	if pg.piglinAngeredAt != attacker.entityID {
		t.Fatalf("after hit, ANGRY_AT = %d, want the attacker %d", pg.piglinAngeredAt, attacker.entityID)
	}
	if pg.ai.attackTargetID != attacker.entityID {
		t.Fatalf("after hit, FIGHT target = %d, want the attacker %d (retaliation)", pg.ai.attackTargetID, attacker.entityID)
	}
	if pg.piglinAdmiringDisabled != piglinAdmiringDisabledTime {
		t.Fatalf("after hit, ADMIRING_DISABLED = %d, want %d", pg.piglinAdmiringDisabled, piglinAdmiringDisabledTime)
	}
	// The anger target is preserved through re-acquisition even though the attacker wears gold.
	loop.piglinAcquireNearestPlayer(pg)
	if pg.ai.attackTargetID != attacker.entityID {
		t.Fatalf("re-acquire dropped the gold-armored anger target (%d), want %d", pg.ai.attackTargetID, attacker.entityID)
	}
}

// TestPiglinAngerBroadcastsToPack: hitting one adult piglin angers nearby adult piglins at the attacker too
// (PiglinAi.broadcastAngerTarget over NEARBY_ADULT_PIGLINS). A far-away piglin (> 16) is NOT angered.
func TestPiglinAngerBroadcastsToPack(t *testing.T) {
	loop, _, floorY := piglinLoop(t)
	py := float64(floorY + 1)
	attacker := combatTestPlayer(loop, 9.0, py, 8.5, 7040)
	hit := loop.spawnPiglin(8.5, py, 8.5, false)
	near := loop.spawnPiglin(11.5, py, 8.5, false) // ~3 blocks away -> in the 16-block pack scan
	loop.withRegion(loop.regions[globalRegion], func() {
		loop.applyDamageEntity(hit, damageSourcePlayerAttack(attacker.entityID), 1.0)
	})
	if near.piglinAngeredAt != attacker.entityID {
		t.Fatalf("nearby pack piglin ANGRY_AT = %d, want the attacker %d (broadcastAngerTarget)", near.piglinAngeredAt, attacker.entityID)
	}
}

// TestPiglinBabyFleesOnHit: a hit baby piglin does NOT retaliate -- it sets AVOID_TARGET (100t) and flees.
func TestPiglinBabyFleesOnHit(t *testing.T) {
	loop, _, floorY := piglinLoop(t)
	py := float64(floorY + 1)
	attacker := combatTestPlayer(loop, 9.0, py, 8.5, 7050)
	baby := loop.spawnPiglin(8.5, py, 8.5, true)
	loop.withRegion(loop.regions[globalRegion], func() {
		loop.applyDamageEntity(baby, damageSourcePlayerAttack(attacker.entityID), 1.0)
	})
	if baby.piglinAvoidTicks != piglinBabyAvoidTime {
		t.Fatalf("hit baby AVOID timer = %d, want %d", baby.piglinAvoidTicks, piglinBabyAvoidTime)
	}
	if baby.piglinAvoidTargetID != attacker.entityID {
		t.Fatalf("hit baby AVOID target = %d, want the attacker %d", baby.piglinAvoidTargetID, attacker.entityID)
	}
	if baby.piglinAngeredAt != 0 {
		t.Fatalf("hit baby has ANGRY_AT %d, want 0 (babies flee, not fight)", baby.piglinAngeredAt)
	}
}

// TestPiglinFinalizeSpawnRolls: piglinFinalizeSpawn performs the natural-spawn RNG rolls in the exact draw
// order (baby nextFloat<0.2, weapon on the entity RNG, armor 4x nextFloat<0.1 on the level RNG). It is
// deterministic for a fixed level seed, a baby never gets a spawn weapon, and across seeds babies (~20%) +
// crossbows (~50%) appear. Cite Piglin.finalizeSpawn.
func TestPiglinFinalizeSpawnRolls(t *testing.T) {
	loop, _, floorY := piglinLoop(t)
	py := float64(floorY + 1)

	// Determinism: the same level seed -> the same baby/weapon/armor outcome.
	loop.regions[globalRegion].levelRandom = levelgen.NewLegacyRandomSource(4242)
	a := loop.spawnPiglin(8.5, py, 8.5, false)
	loop.withRegion(loop.regions[globalRegion], func() { loop.piglinFinalizeSpawn(a, false) })

	loop.regions[globalRegion].levelRandom = levelgen.NewLegacyRandomSource(4242)
	b := loop.spawnPiglin(9.5, py, 8.5, false)
	// Reseed b's entity RNG to a's id-derived seed is not identical, so weapon may differ; but baby+armor use
	// the level RNG and must match. Compare the level-RNG-driven outcomes (baby + armor slots).
	loop.withRegion(loop.regions[globalRegion], func() { loop.piglinFinalizeSpawn(b, false) })
	if a.isBaby() != b.isBaby() {
		t.Fatalf("finalizeSpawn baby roll not deterministic for a fixed level seed (a=%v b=%v)", a.isBaby(), b.isBaby())
	}
	for _, slot := range []int{eqSlotHead, eqSlotChest, eqSlotLegs, eqSlotFeet} {
		ea := a.getItemBySlot(slot).ItemID != 0
		eb := b.getItemBySlot(slot).ItemID != 0
		if ea != eb {
			t.Fatalf("finalizeSpawn armor slot %d roll not deterministic (a=%v b=%v)", slot, ea, eb)
		}
	}

	// A baby never receives a spawn weapon (the createSpawnWeapon branch is else-if isAdult()).
	babies, crossbows, weapons := 0, 0, 0
	for i := 0; i < 400; i++ {
		loop.regions[globalRegion].levelRandom = levelgen.NewLegacyRandomSource(int64(i) * 2654435761)
		pg := loop.spawnPiglin(8.5, py, 8.5, false)
		loop.withRegion(loop.regions[globalRegion], func() { loop.piglinFinalizeSpawn(pg, false) })
		hasWeapon := pg.getItemBySlot(eqSlotMainHand).ItemID != 0
		if pg.isBaby() {
			babies++
			if hasWeapon {
				t.Fatal("a baby piglin got a spawn weapon (createSpawnWeapon is adult-only)")
			}
		} else if hasWeapon {
			weapons++
			if int32(pg.getItemBySlot(eqSlotMainHand).ItemID) == piglinCrossbowItem {
				crossbows++
			}
		}
	}
	// ~20% babies (loose bounds to avoid flakiness).
	if babies < 40 || babies > 130 {
		t.Fatalf("baby rate off: %d/400 (want ~80 @ 0.2)", babies)
	}
	// Every non-baby got a weapon (setItemSlot(MAINHAND, createSpawnWeapon) always runs for an adult).
	if weapons != 400-babies {
		t.Fatalf("adults with a weapon = %d, want %d (all adults)", weapons, 400-babies)
	}
	// ~50% of weapons are crossbows.
	if crossbows < weapons/4 || crossbows > 3*weapons/4 {
		t.Fatalf("crossbow rate off: %d/%d weapons (want ~50%%)", crossbows, weapons)
	}
}

// TestPiglinFinalizeSpawnStructureSkipsBabyWeapon: a STRUCTURE-reason spawn skips the baby/weapon block
// (reason == STRUCTURE guard) but still rolls armor for an adult. Cite Piglin.finalizeSpawn (reason gate).
func TestPiglinFinalizeSpawnStructureSkipsBabyWeapon(t *testing.T) {
	loop, _, floorY := piglinLoop(t)
	py := float64(floorY + 1)
	for i := 0; i < 50; i++ {
		loop.regions[globalRegion].levelRandom = levelgen.NewLegacyRandomSource(int64(5000+i) * 2654435761)
		pg := loop.spawnPiglin(8.5, py, 8.5, false)
		loop.withRegion(loop.regions[globalRegion], func() { loop.piglinFinalizeSpawn(pg, true) })
		if pg.isBaby() {
			t.Fatal("a STRUCTURE-reason piglin became a baby (the reason gate must skip the baby roll)")
		}
		if pg.getItemBySlot(eqSlotMainHand).ItemID != 0 {
			t.Fatal("a STRUCTURE-reason piglin got a spawn weapon (the reason gate must skip createSpawnWeapon)")
		}
	}
}

// piglinArrowCount counts live arrow entities in the global region (the crossbow-fire check).
func piglinArrowCount(loop *TickLoop) int {
	n := 0
	for _, e := range loop.regions[globalRegion].entities.byID {
		if e.isArrow {
			n++
		}
	}
	return n
}

// TestPiglinCrossbowFires: a crossbow-armed adult piglin (piglinIsCrossbow) with a target in crossbow range
// charges for chargeDuration(25), counts down the 20+nextInt(20) attackDelay, then fires an arrow and returns
// to UNCHARGED. Cite CrossbowAttack.crossbowAttack + Piglin.performRangedAttack.
func TestPiglinCrossbowFires(t *testing.T) {
	loop, _, floorY := piglinLoop(t)
	py := float64(floorY + 1)
	target := combatTestPlayer(loop, 12.5, py, 8.5, 7070) // ~4 blocks -> in crossbow range (8), outside backup? (>5 no)
	target.health = 20.0
	pg := loop.spawnPiglin(8.5, py, 8.5, false)
	pg.piglinIsCrossbow = true
	pg.ai.attackTargetID = target.entityID

	before := piglinArrowCount(loop)
	fired := false
	loop.withRegion(loop.regions[globalRegion], func() {
		// chargeDuration 25 + attackDelay up to 39 + a few = plenty within 80 ticks.
		for i := 0; i < 80 && !fired; i++ {
			loop.piglinCrossbowAttackTick(pg, target)
			if piglinArrowCount(loop) > before {
				fired = true
			}
		}
	})
	if !fired {
		t.Fatalf("crossbow piglin never fired an arrow in 80 ticks (state=%d charge=%d)", pg.piglinCrossbowState, pg.piglinCrossbowCharge)
	}
	if pg.piglinCrossbowState != 0 {
		t.Fatalf("after firing, crossbow state = %d, want 0 (UNCHARGED)", pg.piglinCrossbowState)
	}
}

// TestPiglinCrossbowChargesFirst: the crossbow piglin does NOT fire before the charge completes -- no arrow in
// the first chargeDuration(25) ticks. Cite CrossbowAttack (CHARGING -> release at chargeDuration).
func TestPiglinCrossbowChargesFirst(t *testing.T) {
	loop, _, floorY := piglinLoop(t)
	py := float64(floorY + 1)
	target := combatTestPlayer(loop, 12.5, py, 8.5, 7071)
	pg := loop.spawnPiglin(8.5, py, 8.5, false)
	pg.piglinIsCrossbow = true
	pg.ai.attackTargetID = target.entityID
	before := piglinArrowCount(loop)
	loop.withRegion(loop.regions[globalRegion], func() {
		for i := 0; i < piglinCrossbowChargeDur; i++ {
			loop.piglinCrossbowAttackTick(pg, target)
		}
	})
	if piglinArrowCount(loop) != before {
		t.Fatal("crossbow piglin fired during the charge window (must charge chargeDuration first)")
	}
}

// TestPiglinPicksUpGoldAndAdmires: a dropped GOLD_INGOT near an adult piglin is picked into its offhand and
// admired (ADMIRING_ITEM 119t). After the admire timer expires, the piglin auto-barters (drops a bartering
// roll and clears the offhand). Cite PiglinAi.pickUpItem + admireGoldItem + StopHoldingItemIfNoLongerAdmiring.
func TestPiglinPicksUpGoldAndAdmires(t *testing.T) {
	loop, _, floorY := piglinLoop(t)
	py := float64(floorY + 1)
	pg := loop.spawnPiglin(8.5, py, 8.5, false)
	// Drop a gold ingot at the piglin's feet (within the 1.0/0.5 pickup box), pickupDelay 0.
	ie := NewItemEntity(loop.idAlloc.AllocID(), 8.5, py, 8.5, component.SlotData{Count: 2, ItemID: pk.VarInt(item.GoldIngot.ID)})
	ie.pickupDelay = 0
	loop.regions[globalRegion].entities.add(ie)

	loop.withRegion(loop.regions[globalRegion], func() { loop.piglinItemPickupTick(pg) })
	if slotIsEmpty(pg.piglinOffhandItem) {
		t.Fatal("piglin did not pick up the dropped gold ingot into its offhand")
	}
	if int32(pg.piglinOffhandItem.ItemID) != piglinBarterItemID {
		t.Fatalf("offhand item = %d, want GOLD_INGOT %d", pg.piglinOffhandItem.ItemID, piglinBarterItemID)
	}
	if pg.piglinAdmireTicks != piglinAdmireDuration {
		t.Fatalf("admire timer = %d, want %d (ADMIRE_DURATION)", pg.piglinAdmireTicks, piglinAdmireDuration)
	}
	// The item entity shrank by 1 (removeOneItemFromItemEntity).
	if ie.itemStack.Count != 1 {
		t.Fatalf("dropped stack = %d after pickup, want 1", ie.itemStack.Count)
	}

	// Run out the admire timer -> auto-barter.
	before := piglinItemEntityCount(loop)
	loop.withRegion(loop.regions[globalRegion], func() {
		for i := 0; i < piglinAdmireDuration; i++ {
			if pg.piglinAdmireTicks > 0 {
				pg.piglinAdmireTicks--
			}
		}
		loop.piglinItemPickupTick(pg) // admire expired -> stopHoldingOffHandItem(true) barter
	})
	if !slotIsEmpty(pg.piglinOffhandItem) {
		t.Fatal("piglin still holds the admired item after the admire timer expired (should barter)")
	}
	if piglinItemEntityCount(loop) <= before {
		t.Fatal("no barter drop after the admire timer expired")
	}
}

// TestPiglinIgnoresRepellent: a PIGLIN_REPELLENTS item (soul torch) is never picked up. Cite PiglinAi.wantsToPickup.
func TestPiglinIgnoresRepellent(t *testing.T) {
	loop, _, floorY := piglinLoop(t)
	py := float64(floorY + 1)
	pg := loop.spawnPiglin(8.5, py, 8.5, false)
	// A PIGLIN_REPELLENTS member (data/tag piglin_repellents == {393, 1395, 1407} -- soul torch/lantern/campfire).
	ie := NewItemEntity(loop.idAlloc.AllocID(), 8.5, py, 8.5, component.SlotData{Count: 1, ItemID: pk.VarInt(393)})
	ie.pickupDelay = 0
	loop.regions[globalRegion].entities.add(ie)
	loop.withRegion(loop.regions[globalRegion], func() { loop.piglinItemPickupTick(pg) })
	if !slotIsEmpty(pg.piglinOffhandItem) {
		t.Fatal("piglin picked up a PIGLIN_REPELLENTS item (must ignore repellents)")
	}
}

// TestPiglinConversionAppliesNausea: the ZombifiedPiglin produced by piglinFinishConversion carries NAUSEA
// 200t (AbstractPiglin.finishConversion AfterConversion callback). Cite AbstractPiglin.finishConversion.
func TestPiglinConversionAppliesNausea(t *testing.T) {
	loop, _, floorY := piglinLoop(t)
	py := float64(floorY + 1)
	pg := loop.spawnPiglin(8.5, py, 8.5, false)
	var zp *Entity
	loop.withRegion(loop.regions[globalRegion], func() { zp = loop.piglinFinishConversion(pg) })
	if zp == nil {
		t.Fatal("piglinFinishConversion returned nil")
	}
	if !entityHasEffect(zp, "minecraft:nausea") {
		t.Fatal("converted zombified piglin has no NAUSEA effect (finishConversion applies NAUSEA 200)")
	}
}

// TestPiglinBarterThrowsTowardPlayer: the barter drop is thrown TOWARD the nearest visible player (a nonzero
// velocity aimed at the player), not plopped at the piglin's feet. Cite PiglinAi.throwItems + BehaviorUtils.throwItem.
func TestPiglinBarterThrowsTowardPlayer(t *testing.T) {
	loop, _, floorY := piglinLoop(t)
	py := float64(floorY + 1)
	pg := loop.spawnPiglin(8.5, py, 8.5, false)
	// A player to the +X side -> the thrown item should carry a +X velocity component.
	p := combatTestPlayer(loop, 13.5, py, 8.5, 7080)
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(inv.heldSlot), component.SlotData{Count: 1, ItemID: pk.VarInt(item.GoldIngot.ID)})

	var thrown *Entity
	loop.withRegion(loop.regions[globalRegion], func() {
		loop.piglinMobInteract(p, pg)
		for _, e := range loop.regions[globalRegion].entities.byID {
			if e.isItem && (e.vx != 0 || e.vz != 0) {
				thrown = e
				break
			}
		}
	})
	if thrown == nil {
		t.Fatal("barter item was not thrown with a velocity (aim missing)")
	}
	if thrown.vx <= 0 {
		t.Fatalf("thrown item vx = %v, want > 0 (toward the +X player)", thrown.vx)
	}
}

// TestPiglinAvoidsZombified: an idle adult piglin within 6 blocks of a zombified piglin arms the AVOID flee
// (AVOID_TARGET = the zombified, AVOID_ZOMBIFIED_DURATION 5-7s) and retreats away from it. A zombified piglin
// beyond 6 blocks does not trigger the avoid. Cite PiglinAi.avoidZombified + isNearZombified.
func TestPiglinAvoidsZombified(t *testing.T) {
	loop, _, floorY := piglinLoop(t)
	py := float64(floorY + 1)
	pg := loop.spawnPiglin(8.5, py, 8.5, false)
	// A zombified piglin 3 blocks away (within 6) -> arm the avoid.
	zp := loop.spawnZombifiedPiglin(11.5, py, 8.5)
	loop.withRegion(loop.regions[globalRegion], func() { loop.piglinAvoidZombifiedTick(pg) })
	if pg.piglinAvoidTicks < piglinAvoidZombifiedDurMin || pg.piglinAvoidTicks > piglinAvoidZombifiedDurMin+piglinAvoidZombifiedDurSpan {
		t.Fatalf("avoid timer = %d, want %d..%d (AVOID_ZOMBIFIED_DURATION)", pg.piglinAvoidTicks, piglinAvoidZombifiedDurMin, piglinAvoidZombifiedDurMin+piglinAvoidZombifiedDurSpan)
	}
	if pg.piglinAvoidTargetID != zp.id {
		t.Fatalf("avoid target = %d, want the zombified piglin %d", pg.piglinAvoidTargetID, zp.id)
	}
	// The flee walks AWAY from the zombified piglin (want-target on the -X side, opposite the +X zombified).
	loop.withRegion(loop.regions[globalRegion], func() { loop.piglinAvoidTick(pg) })
	if pg.ai.wantX >= pg.x {
		t.Fatalf("flee want-target x = %v, want < piglin x %v (away from the +X zombified)", pg.ai.wantX, pg.x)
	}

	// A zombified piglin far away (>6) does not arm a fresh piglin.
	pg2 := loop.spawnPiglin(40.5, py, 40.5, false)
	_ = loop.spawnZombifiedPiglin(60.5, py, 60.5)
	loop.withRegion(loop.regions[globalRegion], func() { loop.piglinAvoidZombifiedTick(pg2) })
	if pg2.piglinAvoidTicks != 0 {
		t.Fatalf("far-away zombified armed the avoid (timer=%d, want 0)", pg2.piglinAvoidTicks)
	}
}
