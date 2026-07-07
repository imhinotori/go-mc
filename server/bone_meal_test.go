package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// bone_meal_test.go covers BONE MEAL on crops (bone_meal.go + the handleUseItemOn hook):
//   - a young crop below MAX_AGE advances by the vanilla Mth.nextInt(2,5) amount and consumes 1;
//   - a MAX_AGE crop is not a valid bonemeal target (no age change, no consume);
//   - CREATIVE advances the crop but keeps the bone meal (the count save/restore gate).
// The age-increase RNG is cross-checked against an INDEPENDENT LegacyRandomSource with the same seed,
// so the port is validated rather than tautologically re-run.

// heldCount reads the count of the player's selected hotbar slot (post-use inventory).
func heldCount(p *tickPlayer) int {
	inv := ensureInventory(p)
	return int(inv.get(heldWindowSlot(inv.heldSlot)).Count)
}

// TestBoneMealGrowsWheat: a bone meal used on age-0 wheat advances it by Mth.nextInt(2,5) (clamped to
// MAX_AGE 7) and shrinks the held bone meal stack by 1. The expected increase is computed from an
// independent LegacyRandomSource seeded identically, matching the single nextInt(4)+2 draw.
func TestBoneMealGrowsWheat(t *testing.T) {
	loop, mgr := newBlockLoop()
	const seed = 0xB04E // deterministic seed
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(seed)

	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.BoneMeal.ID, 5)

	cropPos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(cropPos, wheat(0), dimMinY)

	// Independent expected increase: Mth.nextInt(2,5) == r.nextInt(4)+2 with the same seed.
	ref := levelgen.NewLegacyRandomSource(seed)
	wantInc := int(ref.NextIntN(4)) + 2
	wantAge := wantInc
	if wantAge > 7 {
		wantAge = 7
	}

	const seq = 71
	ui := useItemOnPacket(0 /*main hand*/, cropPos, 1 /*UP*/, 0.5, 1.0, 0.5, false, false, seq)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	got := block.CropAge(mustGet(t, mgr, cropPos))
	if got != wantAge {
		t.Fatalf("bone-mealed wheat age = %d, want %d (0 + Mth.nextInt(2,5)=%d)", got, wantAge, wantInc)
	}
	if got <= 0 {
		t.Fatalf("bone meal did not advance the crop (age still %d)", got)
	}
	if c := heldCount(p); c != 4 {
		t.Fatalf("survival bone meal count = %d, want 4 (shrink by 1)", c)
	}
}

// TestBoneMealMaxAgeCropNoOp: a bone meal used on MAX_AGE wheat (age 7) is not a valid bonemeal target
// (CropBlock.isValidBonemealTarget == !isMaxAge -> false), so the crop is unchanged and NO bone meal is
// consumed (growCrop returns false before stack.shrink).
func TestBoneMealMaxAgeCropNoOp(t *testing.T) {
	loop, mgr := newBlockLoop()
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(1)

	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.BoneMeal.ID, 5)

	cropPos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(cropPos, wheat(7), dimMinY) // MAX_AGE

	ui := useItemOnPacket(0, cropPos, 1, 0.5, 1.0, 0.5, false, false, 72)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	if got := block.CropAge(mustGet(t, mgr, cropPos)); got != 7 {
		t.Fatalf("max-age wheat changed to age %d, want it to stay 7 (invalid bonemeal target)", got)
	}
	if c := heldCount(p); c != 5 {
		t.Fatalf("bone meal count = %d after a no-op use, want 5 (not consumed)", c)
	}
}

// TestBoneMealCreativeKeepsItem: in CREATIVE the crop still advances but the bone meal is NOT consumed
// (ServerPlayerGameMode.useItemOn wraps the growCrop shrink in a count save/restore for
// hasInfiniteMaterials -> Sulfur gates the consume on !creative).
func TestBoneMealCreativeKeepsItem(t *testing.T) {
	loop, mgr := newBlockLoop()
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(42)

	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeCreative
	setHeldItem(p, item.BoneMeal.ID, 5)

	cropPos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(cropPos, wheat(0), dimMinY)

	ui := useItemOnPacket(0, cropPos, 1, 0.5, 1.0, 0.5, false, false, 73)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	if got := block.CropAge(mustGet(t, mgr, cropPos)); got <= 0 {
		t.Fatalf("creative bone meal did not advance the crop (age %d)", got)
	}
	if c := heldCount(p); c != 5 {
		t.Fatalf("creative bone meal count = %d, want 5 (creative never consumes)", c)
	}
}

// TestBoneMealBeetrootClampsToMaxAge: a bone meal on beetroots age 2 (MAX_AGE 3) advances by
// Mth.nextInt(2,5) but is clamped by Math.min to 3 -- never past the family max.
func TestBoneMealBeetrootClampsToMaxAge(t *testing.T) {
	loop, mgr := newBlockLoop()
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(7)

	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.BoneMeal.ID, 3)

	cropPos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(cropPos, beetroots(2), dimMinY)

	ui := useItemOnPacket(0, cropPos, 1, 0.5, 1.0, 0.5, false, false, 74)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	if got := block.CropAge(mustGet(t, mgr, cropPos)); got != 3 {
		t.Fatalf("bone-mealed beetroots age = %d, want 3 (clamped to MAX_AGE)", got)
	}
	if c := heldCount(p); c != 2 {
		t.Fatalf("survival beetroot bone meal count = %d, want 2", c)
	}
}

// TestBoneMealNonCropFallsThrough: a bone meal used on a NON-crop block (stone) is not the bonemeal
// path -- tryBoneMealCrop returns false and placement continues (a no-op for the non-block bone_meal
// item), so the bone meal is NOT consumed and the world is unchanged.
func TestBoneMealNonCropFallsThrough(t *testing.T) {
	loop, mgr := newBlockLoop()
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(3)

	p := blockPlayer(loop, 1.5, 66.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.BoneMeal.ID, 5)

	hit := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(hit, block.ToStateID[block.Stone{}], dimMinY)

	ui := useItemOnPacket(0, hit, 1, 0.5, 1.0, 0.5, false, false, 75)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	if c := heldCount(p); c != 5 {
		t.Fatalf("bone meal on stone count = %d, want 5 (not consumed, not a crop)", c)
	}
	// The adjacent cell must remain air (bone meal is not a block item -> nothing placed).
	adj := pk.Position{X: 1, Y: 65, Z: 1}
	if got, ok := mgr.GetBlock(adj, dimMinY); !ok || !block.IsAir(got) {
		t.Fatalf("bone meal placed a block at %v (state %v) -- it must place nothing", adj, got)
	}
}
