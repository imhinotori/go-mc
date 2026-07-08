package server

// horse_test.go -- deterministic pins for the HORSE FAMILY (net.minecraft.world.entity.animal.equine.
// {AbstractHorse,Horse,Donkey,Mule,Llama,TraderLlama}, 1:1 javap this session). Pins the spawn attributes
// (pre-randomize base + per-entity randomize), the EXACT randomized-stat draw order (fixed seed), taming +
// temper, the jump-strength launch, the Llama strength draw, and the breed dispatch + mule sterility.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

func horseLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestHorseSpawnDefaults: spawnHorse renders as entity.Horse.ID, is marked isHorse/isHorseFamily, has a
// randomized MAX_HEALTH in [15,30] (its health seeded from it), a MOVEMENT_SPEED in [0.1125,0.3375], and a
// JUMP_STRENGTH field in [0.4,1.0] -- the createBaseHorseAttributes + Horse.randomizeAttributes envelope.
func TestHorseSpawnDefaults(t *testing.T) {
	loop, floorY := horseLoop(t)
	h := loop.spawnHorse(8.5, float64(floorY+1), 8.5, false)
	if h.typ != entity.Horse.ID {
		t.Fatalf("horse typ = %d, want entity.Horse.ID %d", h.typ, entity.Horse.ID)
	}
	if !h.isHorse || !h.isHorseFamily {
		t.Fatal("horse not marked isHorse/isHorseFamily")
	}
	hp := h.getAttributeValue(attribute.MaxHealth)
	if hp < 15.0 || hp > 30.0 {
		t.Fatalf("horse MAX_HEALTH = %v, want in [15,30] (generateMaxHealth)", hp)
	}
	if math.Abs(float64(h.health)-hp) > 1e-6 {
		t.Fatalf("horse health = %v, want == randomized MAX_HEALTH %v", h.health, hp)
	}
	sp := h.getAttributeValue(attribute.MovementSpeed)
	if sp < 0.1125 || sp > 0.3375 {
		t.Fatalf("horse MOVEMENT_SPEED = %v, want in [0.1125,0.3375] (generateSpeed)", sp)
	}
	if h.horseJumpStrength < 0.4 || h.horseJumpStrength > 1.0 {
		t.Fatalf("horse JUMP_STRENGTH = %v, want in [0.4,1.0] (generateJumpStrength)", h.horseJumpStrength)
	}
	if h.ai == nil || h.ai.rng == nil {
		t.Fatal("horse has no minimal AI / rng")
	}
}

// TestHorseRandomizeDrawOrder pins the EXACT randomized-stat draw for a fixed seed: generateMaxHealth (2
// nextInt), THEN generateSpeed (3 nextDouble), THEN generateJumpStrength (3 nextDouble). A wrong draw ORDER
// (or a wrong formula constant) desyncs every horse, so this asserts the concrete values from seed 12345.
func TestHorseRandomizeDrawOrder(t *testing.T) {
	rng := newEntityRandom(12345)
	if got := generateMaxHealth(rng); got != 20 {
		t.Fatalf("generateMaxHealth(seed 12345) = %v, want 20 (2 nextInt draws first)", got)
	}
	if got := generateSpeed(rng); got != 0.21201809662783477 {
		t.Fatalf("generateSpeed(seed 12345) = %v, want 0.21201809662783477 (3 nextDouble after health)", got)
	}
	if got := generateJumpStrength(rng); got != 0.6409078178195645 {
		t.Fatalf("generateJumpStrength(seed 12345) = %v, want 0.6409078178195645 (3 nextDouble last)", got)
	}
	// A second run on a fresh seed must reproduce (deterministic stream).
	rng2 := newEntityRandom(12345)
	if generateMaxHealth(rng2) != 20 {
		t.Fatal("generateMaxHealth not reproducible for a fixed seed")
	}
}

// TestHorseGenerateFormulaEnvelope pins the generate* range envelopes over many seeds: MAX_HEALTH in
// [15,30] (15 + [0,7] + [0,8]), SPEED in [0.1125,0.3375] (0.45..0.75 * 0.25 with float rounding), JUMP in
// [0.4,1.0] (0.4 + 3*[0,0.2)). Confirms the per-draw coefficients + the *0.25 speed scale.
func TestHorseGenerateFormulaEnvelope(t *testing.T) {
	for seed := uint64(1); seed < 400; seed++ {
		rng := newEntityRandom(seed)
		h := generateMaxHealth(rng)
		if h < 15 || h > 30 {
			t.Fatalf("seed %d: health %v out of [15,30]", seed, h)
		}
		s := generateSpeed(rng)
		if s < 0.1125 || s > 0.3375+1e-9 {
			t.Fatalf("seed %d: speed %v out of [0.1125,0.3375]", seed, s)
		}
		j := generateJumpStrength(rng)
		if j < 0.4 || j >= 1.0 {
			t.Fatalf("seed %d: jump %v out of [0.4,1.0)", seed, j)
		}
	}
}

// TestHorseAttributeSuppliers pins the pre-randomize base suppliers folded from createBase*Attributes: the
// Horse base (MAX_HEALTH 53, MOVEMENT_SPEED 0.225, SAFE_FALL_DISTANCE 6) and the chested base (MOVEMENT_
// SPEED 0.175 override). Read via a fresh map so the randomize does not perturb the pin.
func TestHorseAttributeSuppliers(t *testing.T) {
	hm := attribute.NewMapForEntity("horse")
	if got := hm.GetValue(attribute.MaxHealth.Name()); got != 53.0 {
		t.Fatalf("horse base MAX_HEALTH = %v, want 53.0", got)
	}
	if got := hm.GetValue(attribute.MovementSpeed.Name()); math.Float64bits(got) != math.Float64bits(0.22499999403953552) {
		t.Fatalf("horse base MOVEMENT_SPEED = %v, want 0.22499999403953552 (bit-exact)", got)
	}
	if got := hm.GetValue(attribute.SafeFallDistance.Name()); got != 6.0 {
		t.Fatalf("horse base SAFE_FALL_DISTANCE = %v, want 6.0", got)
	}
	for _, name := range []string{"donkey", "mule", "llama", "trader_llama"} {
		cm := attribute.NewMapForEntity(name)
		if got := cm.GetValue(attribute.MovementSpeed.Name()); math.Float64bits(got) != math.Float64bits(0.17499999701976776) {
			t.Fatalf("%s base MOVEMENT_SPEED = %v, want 0.17499999701976776 (chested override)", name, got)
		}
		if got := cm.GetValue(attribute.MaxHealth.Name()); got != 53.0 {
			t.Fatalf("%s base MAX_HEALTH = %v, want 53.0 (pre-randomize)", name, got)
		}
	}
}

// TestHorseTameAndTemper pins tameWithName (horseTamed true) + modifyTemper (clamp to [0,getMaxTemper]).
// getMaxTemper is 100 for horse/donkey/mule and 30 for llama.
func TestHorseTameAndTemper(t *testing.T) {
	loop, floorY := horseLoop(t)
	h := loop.spawnHorse(8.5, float64(floorY+1), 8.5, false)
	if h.horseTamed {
		t.Fatal("fresh horse should be untamed")
	}
	if !h.horseTameWithName() || !h.horseTamed {
		t.Fatal("horseTameWithName did not tame")
	}
	if h.horseMaxTemper() != 100 {
		t.Fatalf("horse getMaxTemper = %d, want 100", h.horseMaxTemper())
	}
	if got := h.horseModifyTemper(40); got != 40 || h.horseTemper != 40 {
		t.Fatalf("modifyTemper(40) = %d, temper %d, want 40/40", got, h.horseTemper)
	}
	if got := h.horseModifyTemper(1000); got != 100 {
		t.Fatalf("modifyTemper over-cap = %d, want clamp to 100", got)
	}
	if got := h.horseModifyTemper(-1000); got != 0 {
		t.Fatalf("modifyTemper under-floor = %d, want clamp to 0", got)
	}
	l := loop.spawnLlama(8.5, float64(floorY+1), 8.5, false, false)
	if l.horseMaxTemper() != 30 {
		t.Fatalf("llama getMaxTemper = %d, want 30", l.horseMaxTemper())
	}
}

// TestHorseJumpLaunch pins the jump-strength launch chain: getPlayerJumpPendingScale(charge) (>=90 -> 1.0;
// else 0.4 + 0.4*charge/90), onPlayerJump gates on isSaddled (const-false today, so no pending set), and
// horseGetJumpPower(scale) = JUMP_STRENGTH * scale (blockJumpFactor 1.0, jumpBoostPower 0.0).
func TestHorseJumpLaunch(t *testing.T) {
	if got := horseGetPlayerJumpPendingScale(90); got != 1.0 {
		t.Fatalf("pendingScale(90) = %v, want 1.0", got)
	}
	if got := horseGetPlayerJumpPendingScale(45); math.Abs(float64(got)-0.6) > 1e-6 {
		t.Fatalf("pendingScale(45) = %v, want 0.6 (0.4 + 0.4*45/90)", got)
	}
	if got := horseGetPlayerJumpPendingScale(0); math.Abs(float64(got)-0.4) > 1e-6 {
		t.Fatalf("pendingScale(0) = %v, want 0.4", got)
	}
	loop, floorY := horseLoop(t)
	h := loop.spawnHorse(8.5, float64(floorY+1), 8.5, false)
	// onPlayerJump on an un-saddled horse (isSaddled const-false) must NOT set the pending scale.
	h.horseOnPlayerJump(90)
	if h.horsePlayerJumpPendingScale != 0 {
		t.Fatalf("onPlayerJump set pending %v on an un-saddled horse (isSaddled false)", h.horsePlayerJumpPendingScale)
	}
	// getJumpPower reads the randomized JUMP_STRENGTH field: power == jumpStrength * scale.
	h.horseJumpStrength = 0.8
	if got := h.horseGetJumpPower(1.0); math.Abs(float64(got)-0.8) > 1e-6 {
		t.Fatalf("getJumpPower(1.0) = %v, want 0.8 (JUMP_STRENGTH * scale)", got)
	}
	if got := h.horseGetJumpPower(0.5); math.Abs(float64(got)-0.4) > 1e-6 {
		t.Fatalf("getJumpPower(0.5) = %v, want 0.4", got)
	}
}

// TestLlamaStrength pins setRandomStrength (nextFloat<0.04 ? 5 : 3, then 1+nextInt(bound)) clamped to
// [1,5], and the strength-driven inventory columns (0 without a chest, strength with a chest).
func TestLlamaStrength(t *testing.T) {
	loop, floorY := horseLoop(t)
	l := loop.spawnLlama(8.5, float64(floorY+1), 8.5, false, false)
	if l.llamaStrength < 1 || l.llamaStrength > 5 {
		t.Fatalf("llama strength = %d, want [1,5]", l.llamaStrength)
	}
	// setStrength clamp.
	l.llamaSetStrength(99)
	if l.llamaStrength != 5 {
		t.Fatalf("setStrength(99) = %d, want clamp 5", l.llamaStrength)
	}
	l.llamaSetStrength(-3)
	if l.llamaStrength != 1 {
		t.Fatalf("setStrength(-3) = %d, want clamp 1", l.llamaStrength)
	}
	// Columns: no chest -> 0; with chest -> strength (3).
	l.llamaSetStrength(3)
	if l.horseGetInventoryColumns() != 0 {
		t.Fatalf("llama columns without chest = %d, want 0", l.horseGetInventoryColumns())
	}
	l.horseHasChest = true
	if l.horseGetInventoryColumns() != 3 {
		t.Fatalf("llama columns with chest (strength 3) = %d, want 3", l.horseGetInventoryColumns())
	}
	// setRandomStrength draw pin (seed 12345: nextFloat 0.45 -> bound 3 -> 1 + nextInt(3)).
	rng := newEntityRandom(12345)
	l.llamaSetRandomStrength(rng)
	if l.llamaStrength != 1 {
		t.Fatalf("llamaSetRandomStrength(seed 12345) = %d, want 1", l.llamaStrength)
	}
	// TraderLlama is a flagged llama with the same stats.
	tl := loop.spawnLlama(8.5, float64(floorY+1), 8.5, false, true)
	if !tl.isTraderLlama || tl.typ != entity.TraderLlama.ID {
		t.Fatalf("trader llama typ = %d isTrader=%v, want TraderLlama.ID / true", tl.typ, tl.isTraderLlama)
	}
}

// TestHorseBreedDispatch pins getBreedOffspring species dispatch + mule sterility: Horse+Donkey -> Mule
// (either direction), Horse+Horse -> Horse, Donkey+Donkey -> Donkey, Llama+Llama -> Llama, and any pair
// involving a Mule is INFERTILE (Mule.canMate is the AbstractHorse false base).
func TestHorseBreedDispatch(t *testing.T) {
	loop, floorY := horseLoop(t)
	y := float64(floorY + 1)
	horse := loop.spawnHorse(8.5, y, 8.5, false)
	horse2 := loop.spawnHorse(9.5, y, 8.5, false)
	donkey := loop.spawnDonkey(8.5, y, 9.5, false)
	donkey2 := loop.spawnDonkey(9.5, y, 9.5, false)
	mule := loop.spawnMule(8.5, y, 10.5, false)
	llama := loop.spawnLlama(8.5, y, 11.5, false, false)
	llama2 := loop.spawnLlama(9.5, y, 11.5, false, false)

	check := func(a, b *Entity, wantType entity.ID, wantOK bool, label string) {
		got, ok := horseBreedOffspringType(a, b)
		if ok != wantOK {
			t.Fatalf("%s: canBreed = %v, want %v", label, ok, wantOK)
		}
		if wantOK && got.ID != wantType {
			t.Fatalf("%s: offspring = %d, want %d", label, got.ID, wantType)
		}
		if got, want := horseCanMate(a, b), wantOK; got != want {
			t.Fatalf("%s: horseCanMate = %v, want %v", label, got, want)
		}
	}
	check(horse, donkey, entity.Mule.ID, true, "Horse+Donkey")
	check(donkey, horse, entity.Mule.ID, true, "Donkey+Horse")
	check(horse, horse2, entity.Horse.ID, true, "Horse+Horse")
	check(donkey, donkey2, entity.Donkey.ID, true, "Donkey+Donkey")
	check(llama, llama2, entity.Llama.ID, true, "Llama+Llama")
	// Mule is sterile with everything (including another mule).
	check(mule, horse, 0, false, "Mule+Horse")
	check(horse, mule, 0, false, "Horse+Mule")
	check(mule, donkey, 0, false, "Mule+Donkey")
	// Cross-family mismatches do not breed.
	check(horse, llama, 0, false, "Horse+Llama")
	check(donkey, llama, 0, false, "Donkey+Llama")
	// canMate with self is false.
	if horseCanMate(horse, horse) {
		t.Fatal("horseCanMate(self) should be false")
	}
}

// TestHorseFamilyAiStepNoop confirms the per-tick step is a bounded no-op (no panic, no state change) on a
// living horse -- the mount/eating/tail behaviors are DEFERRED.
func TestHorseFamilyAiStepNoop(t *testing.T) {
	loop, floorY := horseLoop(t)
	h := loop.spawnHorse(8.5, float64(floorY+1), 8.5, false)
	hpBefore := h.health
	loop.horseFamilyAiStep(h)
	if h.health != hpBefore {
		t.Fatalf("horseFamilyAiStep changed health %v -> %v", hpBefore, h.health)
	}
}
