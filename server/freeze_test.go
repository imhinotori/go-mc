package server

// freeze_test.go -- behaviour-proving tests for the POWDER-SNOW / FREEZE damage subsystem (freeze.go),
// every assertion pinned to a jar method verified via javap -c -p temp/cache/26.2-inner.jar this
// session:
//
//   - Entity.getTicksRequiredToFreeze() == 140; getTicksFrozen/setTicksFrozen; isFullyFrozen; getPercentFrozen.
//   - Entity.canFreeze() == !is(FREEZE_IMMUNE_ENTITY_TYPES); LivingEntity.canFreeze() armor scan.
//   - InsideBlockEffectType.FREEZE lambda: +1 clamped at 140 while inside powder snow (canFreeze gate).
//   - LivingEntity.aiStep freeze block: -2 clamped at 0 when not; hurtServer(freeze(),1.0) every 40 ticks
//     while isFullyFrozen && canFreeze.
//   - LivingEntity.hurtServer freeze-extra: amount*5 for FREEZE_HURTS_EXTRA_TYPES (wired in combat_mob.go).

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestTicksRequiredToFreezeConstant pins Entity.getTicksRequiredToFreeze() == 140 (sipush 140).
func TestTicksRequiredToFreezeConstant(t *testing.T) {
	if ticksRequiredToFreeze != 140 {
		t.Fatalf("ticksRequiredToFreeze = %d, want 140 (Entity.getTicksRequiredToFreeze sipush 140)", ticksRequiredToFreeze)
	}
	if freezeDamageInterval != 40 {
		t.Fatalf("freezeDamageInterval = %d, want 40 (aiStep tickCount %% 40)", freezeDamageInterval)
	}
	if freezeDamageAmount != 1.0 {
		t.Fatalf("freezeDamageAmount = %v, want 1.0 (aiStep fconst_1)", freezeDamageAmount)
	}
}

// TestFrostAccumulatesTo140 drives a mob standing in powder snow and asserts ticksFrozen climbs by
// exactly +1 per tick (InsideBlockEffectType.FREEZE lambda: min(140, +1)), and CLAMPS at 140.
func TestFrostAccumulatesTo140(t *testing.T) {
	loop, mob := newPowderSnowWorld(t, 1, entity.Zombie, 8, 64, 8)
	mob.health = 20
	if !loop.entityInsidePowderSnow(mob) {
		t.Fatal("setup: mob must read as inside powder snow (feet-block proxy)")
	}
	// The keep-frost branch never decays while inside snow + canFreeze, so ticksFrozen == tick count,
	// clamped at 140. Run 200 ticks; watch it reach and hold 140.
	for i := 1; i <= 200; i++ {
		// keep aiTickCount at a non-40-multiple so no damage fires and confuses the count (i%40!=0 for
		// the first 39 ticks). We assert the pure accumulation curve, not the damage cadence, here.
		mob.ai.aiTickCount = 1
		loop.tickEntityFreeze(mob)
		want := int32(i)
		if want > 140 {
			want = 140
		}
		if got := getTicksFrozen(mob); got != want {
			t.Fatalf("tick %d: ticksFrozen = %d, want %d (+1/tick clamped at 140)", i, got, want)
		}
	}
	if getTicksFrozen(mob) != 140 {
		t.Fatalf("ticksFrozen never clamped at 140: %d", getTicksFrozen(mob))
	}
}

// TestFrostDecaysBy2 pins the aiStep decay branch: when NOT in powder snow, ticksFrozen decays by 2
// per tick, clamped at 0 (Math.max(0, ticksFrozen-2)).
func TestFrostDecaysBy2(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	for cx := -1; cx <= 1; cx++ {
		for cz := -1; cz <= 1; cz++ {
			ch := putChunk(mgr, level.ChunkPos{int32(cx), int32(cz)})
			fillFloor(ch, 63) // solid floor, NO powder snow -> not inside snow
		}
	}
	mob := NewEntity(1, entity.Zombie, 8.5, 64.0, 8.5)
	mob.ai = newPigAI()
	reseedMobAI(mob.ai, mob.id)
	mob.health = 20
	loop.only().entities.add(mob)

	if loop.entityInsidePowderSnow(mob) {
		t.Fatal("setup: mob must NOT be inside powder snow (dry floor)")
	}
	mob.ticksFrozen = 7 // odd start to prove the max(0, x-2) clamp lands exactly at 0
	mob.ai.aiTickCount = 1
	for _, want := range []int32{5, 3, 1, 0, 0} {
		loop.tickEntityFreeze(mob)
		if got := getTicksFrozen(mob); got != want {
			t.Fatalf("decay: ticksFrozen = %d, want %d (max(0, x-2))", got, want)
		}
	}
}

// TestIsFullyFrozenGate pins Entity.isFullyFrozen(): true iff ticksFrozen >= 140.
func TestIsFullyFrozenGate(t *testing.T) {
	e := NewEntity(1, entity.Zombie, 0, 0, 0)
	for _, tc := range []struct {
		frozen int32
		full   bool
	}{{0, false}, {139, false}, {140, true}, {200, true}} {
		e.ticksFrozen = tc.frozen
		if got := isFullyFrozen(e); got != tc.full {
			t.Fatalf("isFullyFrozen(ticksFrozen=%d) = %v, want %v (>= 140)", tc.frozen, got, tc.full)
		}
	}
}

// TestPercentFrozenCurve pins Entity.getPercentFrozen(): min(ticksFrozen,140)/140, clamped at 1.0.
func TestPercentFrozenCurve(t *testing.T) {
	e := NewEntity(1, entity.Zombie, 0, 0, 0)
	for _, tc := range []struct {
		frozen int32
		pct    float32
	}{
		{0, 0.0},
		{70, 70.0 / 140.0},
		{140, 1.0},
		{280, 1.0}, // min(280,140)/140 = 1.0 (clamped)
	} {
		e.ticksFrozen = tc.frozen
		if got := getPercentFrozen(e); got != tc.pct {
			t.Fatalf("getPercentFrozen(ticksFrozen=%d) = %v, want %v", tc.frozen, got, tc.pct)
		}
	}
}

// TestFreezeDamageEvery40Ticks: a fully-frozen (ticksFrozen>=140) mob standing in powder snow takes
// FREEZE damage exactly when aiTickCount % 40 == 0 (LivingEntity.aiStep gate), and nothing otherwise.
func TestFreezeDamageEvery40Ticks(t *testing.T) {
	loop, mob := newPowderSnowWorld(t, 1, entity.Zombie, 8, 64, 8)
	mob.health = 20
	mob.ticksFrozen = 140 // already fully frozen; staying in snow keeps it at 140

	// aiTickCount % 40 != 0: no damage (and staying in snow holds ticksFrozen at 140).
	mob.ai.aiTickCount = 39
	loop.tickEntityFreeze(mob)
	if mob.health != 20 {
		t.Fatalf("aiTickCount=39: freeze damage fired off-cadence (health %v, want 20)", mob.health)
	}
	if mob.ticksFrozen != 140 {
		t.Fatalf("staying in snow should hold ticksFrozen at 140, got %d", mob.ticksFrozen)
	}

	// aiTickCount % 40 == 0: exactly 1.0 freeze damage lands (fresh hit, no i-frame in the way).
	mob.invulnerableTime = 0
	mob.lastHurt = 0
	mob.ai.aiTickCount = 40
	loop.tickEntityFreeze(mob)
	if mob.health != 19 {
		t.Fatalf("aiTickCount=40: expected 1.0 freeze damage (health -> 19), got %v", mob.health)
	}
}

// TestFreezeImmuneTypesTakeNoDamage: an entity type in FREEZE_IMMUNE_ENTITY_TYPES
// (stray/polar_bear/snow_golem/wither) never freezes -- ticksFrozen never accumulates and no damage
// is dealt even at ticksFrozen==140 (Entity.canFreeze == false gates BOTH accumulate and hurt).
func TestFreezeImmuneTypesTakeNoDamage(t *testing.T) {
	// membership sanity.
	for _, typ := range []entity.Entity{entity.Stray, entity.PolarBear, entity.SnowGolem, entity.Wither} {
		if !isFreezeImmuneType(typ.ID) {
			t.Fatalf("%s must be a FREEZE_IMMUNE_ENTITY_TYPES member", typ.Name)
		}
	}
	if isFreezeImmuneType(entity.Zombie.ID) {
		t.Fatal("zombie must NOT be freeze-immune")
	}

	// A polar bear standing in powder snow: no accumulation (canFreeze false), and even if forced to
	// 140 it takes no freeze damage on the %40 tick.
	loop, bear := newPowderSnowWorld(t, 1, entity.PolarBear, 8, 64, 8)
	bear.health = 20
	if loop.entityCanFreeze(bear) {
		t.Fatal("PolarBear.canFreeze must be false (FREEZE_IMMUNE_ENTITY_TYPES)")
	}
	bear.ai.aiTickCount = 1
	loop.tickEntityFreeze(bear)
	if bear.ticksFrozen != 0 {
		t.Fatalf("freeze-immune bear accumulated frost: ticksFrozen %d, want 0", bear.ticksFrozen)
	}
	bear.ticksFrozen = 140 // force fully frozen
	bear.ai.aiTickCount = 40
	loop.tickEntityFreeze(bear)
	if bear.health != 20 {
		t.Fatalf("freeze-immune bear took freeze damage: health %v, want 20", bear.health)
	}
}

// TestLeatherBootsExemption pins the LivingEntity.canFreeze() armor scan: an ARMOR stack in
// FREEZE_IMMUNE_WEARABLES (leather boots/leggings/chestplate/helmet) makes the wearer freeze-immune.
func TestLeatherBootsExemption(t *testing.T) {
	boots := component.SlotData{Count: 1, ItemID: pk.VarInt(item.LeatherBoots.ID)}
	if !wearsFreezeImmuneArmor(boots) {
		t.Fatal("leather boots must satisfy FREEZE_IMMUNE_WEARABLES (leather-boots exemption)")
	}
	// The other leather armor pieces are also members.
	for _, it := range []item.Item{item.LeatherHelmet, item.LeatherChestplate, item.LeatherLeggings, item.LeatherHorseArmor} {
		if !wearsFreezeImmuneArmor(component.SlotData{Count: 1, ItemID: pk.VarInt(it.ID)}) {
			t.Fatalf("%s must be a FREEZE_IMMUNE_WEARABLES member", it.Name)
		}
	}
	// A non-leather boot (iron) is NOT exempt; an empty stack is skipped.
	iron := component.SlotData{Count: 1, ItemID: pk.VarInt(item.IronBoots.ID)}
	if wearsFreezeImmuneArmor(iron) {
		t.Fatal("iron boots must NOT be a FREEZE_IMMUNE_WEARABLES member")
	}
	if wearsFreezeImmuneArmor(component.SlotData{Count: 0, ItemID: pk.VarInt(item.LeatherBoots.ID)}) {
		t.Fatal("an EMPTY stack (Count 0) must be skipped (ItemStack.EMPTY.is == false)")
	}
	if wearsFreezeImmuneArmor() {
		t.Fatal("no armor -> not exempt")
	}
}

// TestFreezeHurtsExtraTypesX5: a strider/blaze/magma_cube takes 5x freeze damage (LivingEntity.hurtServer
// freeze-extra branch, wired in combat_mob.go's applyDamageEntity).
func TestFreezeHurtsExtraTypesX5(t *testing.T) {
	for _, typ := range []entity.Entity{entity.Strider, entity.Blaze, entity.MagmaCube} {
		if !isFreezeHurtsExtraType(typ.ID) {
			t.Fatalf("%s must be a FREEZE_HURTS_EXTRA_TYPES member", typ.Name)
		}
	}
	if isFreezeHurtsExtraType(entity.Zombie.ID) {
		t.Fatal("zombie must NOT be in FREEZE_HURTS_EXTRA_TYPES")
	}

	loop := NewTickLoop(newFakeClock())

	// A strider takes 1.0*5 = 5.0 freeze damage (magma cube/blaze/strider are fire-immune but freeze is
	// NOT a fire source, so the fire-resistance/fire-immune guard does not intercept it).
	strider := NewEntity(1, entity.Strider, 8.5, 100, 8.5)
	strider.health = 20
	loop.applyDamageEntity(strider, damageSourceOf(damageTypeFreeze), freezeDamageAmount)
	if strider.health != 15 {
		t.Fatalf("strider freeze damage: expected 5.0 (1.0*5), health 20 -> got %v, want 15", strider.health)
	}

	// A zombie (not an extra type) takes the base 1.0.
	zombie := NewEntity(2, entity.Zombie, 8.5, 100, 8.5)
	zombie.health = 20
	loop.applyDamageEntity(zombie, damageSourceOf(damageTypeFreeze), freezeDamageAmount)
	if zombie.health != 19 {
		t.Fatalf("zombie freeze damage: expected 1.0, health 20 -> got %v, want 19", zombie.health)
	}
}

// TestFreezeSourceIsFreezing: the FREEZE damage source is a member of the is_freezing damage-type tag
// (the guard combat_mob.go's freeze-extra branch reads).
func TestFreezeSourceIsFreezing(t *testing.T) {
	src := damageSourceOf(damageTypeFreeze)
	if !src.is("is_freezing") {
		t.Fatal("DamageSources.freeze() must be a member of the is_freezing tag")
	}
}

// TestSetTicksFrozenNoChangeNoBroadcast: setTicksFrozen is a no-op (no panic, no broadcast) when the
// value is unchanged (SynchedEntityData.set only marks dirty on an actual change).
func TestSetTicksFrozenNoChangeNoBroadcast(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := NewEntity(1, entity.Zombie, 0, 0, 0)
	e.ticksFrozen = 42
	loop.setTicksFrozen(e, 42) // unchanged -> must not touch the field or broadcast
	if e.ticksFrozen != 42 {
		t.Fatalf("setTicksFrozen(same) changed the field to %d", e.ticksFrozen)
	}
	loop.setTicksFrozen(e, 43)
	if e.ticksFrozen != 43 {
		t.Fatalf("setTicksFrozen(43) -> %d, want 43", e.ticksFrozen)
	}
}

// TestPigOracleFreezeUnperturbed proves the pig-oracle guard: a pig NOT in powder snow (the oracle
// setup) draws ZERO RNG and takes ZERO freeze damage through tickEntityFreeze -- ticksFrozen stays 0.
func TestPigOracleFreezeUnperturbed(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	for cx := -1; cx <= 1; cx++ {
		for cz := -1; cz <= 1; cz++ {
			ch := putChunk(mgr, level.ChunkPos{int32(cx), int32(cz)})
			fillFloor(ch, 63) // dry floor -- a pig here is NOT in powder snow
		}
	}
	pig := NewEntity(1, entity.Pig, 8.5, 64.0, 8.5)
	pig.ai = newPigAI()
	reseedMobAI(pig.ai, pig.id)
	pig.health = 10
	loop.only().entities.add(pig)

	if loop.entityInsidePowderSnow(pig) {
		t.Fatal("oracle pig must NOT be inside powder snow")
	}
	ref := cloneMobRNG(pig)
	// Run many ticks across every %40 boundary; nothing should change.
	for i := 1; i <= 120; i++ {
		pig.ai.aiTickCount = i
		loop.tickEntityFreeze(pig)
	}
	if pig.ticksFrozen != 0 {
		t.Fatalf("oracle pig accumulated frost: ticksFrozen %d, want 0", pig.ticksFrozen)
	}
	if pig.health != 10 {
		t.Fatalf("oracle pig took freeze damage: health %v, want 10", pig.health)
	}
	assertRNGUntouched(t, pig, ref, "tickEntityFreeze (dry pig oracle)")
}

// TestPowderSnowStateReadable sanity-checks the block seam the accumulation depends on: the codegen'd
// powder_snow state id round-trips through the world read.
func TestPowderSnowStateReadable(t *testing.T) {
	loop, mob := newPowderSnowWorld(t, 1, entity.Zombie, 8, 64, 8)
	if !loop.entityInsidePowderSnow(mob) {
		t.Fatal("entityInsidePowderSnow must read the placed powder_snow block at the feet")
	}
	_ = block.PowderSnow{} // the state used by entityInsidePowderSnow
}
