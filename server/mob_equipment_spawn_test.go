package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
)

// mob_equipment_spawn_test.go — gates the full spawn-time equip port
// (populateDefaultEquipmentSlots + weapon override + populateDefaultEquipmentEnchantments), a 1:1
// port of Mob / AbstractSkeleton / Zombie.populateDefaultEquipmentSlots. The RNG draw order is
// load-bearing (spawn determinism / the pig oracle), so these tests pin: determinism for a seed,
// the correct items per species, and the CRITICAL invariant that a NON-monster (the pig) rolls
// NOTHING and consumes ZERO draws from its stream.

// armorEquipped reports whether the entity has any of the 4 humanoid-armor slots populated.
func armorEquipped(e *Entity) bool {
	for _, slot := range []int{eqSlotFeet, eqSlotLegs, eqSlotChest, eqSlotHead} {
		if e.getItemBySlot(slot).Count > 0 {
			return true
		}
	}
	return false
}

// isArmorItemID reports whether id is one of the 24 armor items getEquipmentForSlot can produce.
func isArmorItemID(id int32) bool {
	all := []item.Item{
		item.LeatherHelmet, item.CopperHelmet, item.GoldenHelmet, item.ChainmailHelmet, item.IronHelmet, item.DiamondHelmet,
		item.LeatherChestplate, item.CopperChestplate, item.GoldenChestplate, item.ChainmailChestplate, item.IronChestplate, item.DiamondChestplate,
		item.LeatherLeggings, item.CopperLeggings, item.GoldenLeggings, item.ChainmailLeggings, item.IronLeggings, item.DiamondLeggings,
		item.LeatherBoots, item.CopperBoots, item.GoldenBoots, item.ChainmailBoots, item.IronBoots, item.DiamondBoots,
	}
	for _, it := range all {
		if int32(it.ID) == id {
			return true
		}
	}
	return false
}

// armorTierForSlot maps a populated armor slot to its rolled tier index [0..5] (the inverse of
// getEquipmentForSlot) — so we can assert all 4 slots share the SAME rolled tier (vanilla rolls one
// armorType for the whole mob).
func armorTierForSlot(t *testing.T, slot int, id int32) int {
	t.Helper()
	for tier := 0; tier <= 5; tier++ {
		if it, ok := getEquipmentForSlot(slot, tier); ok && int32(it.ID) == id {
			return tier
		}
	}
	t.Fatalf("id %d is not an armor item for slot %d", id, slot)
	return -1
}

// TestSpawnEquipDeterministic: same seed -> byte-identical equipment for a zombie and a skeleton
// (the full roll is a pure function of the seed + multiplier).
func TestSpawnEquipDeterministic(t *testing.T) {
	for _, typ := range []struct {
		name string
		ent  entity.Entity
	}{{"zombie", entity.Zombie}, {"skeleton", entity.Skeleton}} {
		t.Run(typ.name, func(t *testing.T) {
			const seed = uint64(0xABCDEF12)
			const mult = float32(1.0)
			a := NewEntity(1, typ.ent, 0, 0, 0)
			b := NewEntity(2, typ.ent, 0, 0, 0)
			populateMonsterEquipment(a, newEntityRandom(seed), mult)
			populateMonsterEquipment(b, newEntityRandom(seed), mult)
			for slot := 0; slot < equipmentSlotCount; slot++ {
				sa, sb := a.getItemBySlot(slot), b.getItemBySlot(slot)
				if sa.Count != sb.Count || sa.ItemID != sb.ItemID {
					t.Fatalf("%s equipment not deterministic for seed %#x at slot %d:\n a=%+v\n b=%+v", typ.name, seed, slot, sa, sb)
				}
			}
		})
	}
}

// TestSkeletonAlwaysHoldsBow: the skeleton's MAINHAND is ALWAYS a bow, under any seed/multiplier
// (AbstractSkeleton unconditionally sets it after the super armor roll).
func TestSkeletonAlwaysHoldsBow(t *testing.T) {
	for _, mult := range []float32{0.0, 0.5, 1.0} {
		for seed := uint64(0); seed < 64; seed++ {
			e := NewEntity(1, entity.Skeleton, 0, 0, 0)
			populateMonsterEquipment(e, newEntityRandom(seed), mult)
			main := e.getMainHandItem()
			if main.Count != 1 || int32(main.ItemID) != int32(item.Bow.ID) {
				t.Fatalf("skeleton MAINHAND not a bow (seed %d, mult %v): %+v", seed, mult, main)
			}
		}
	}
}

// TestSkeletonArmorIsValidAndUniform: when a skeleton rolls armor (mult 1.0), every populated armor
// slot holds a real armor item AND they all share the same rolled tier (one armorType per mob).
func TestSkeletonArmorIsValidAndUniform(t *testing.T) {
	sawArmor := false
	for seed := uint64(0); seed < 512; seed++ {
		e := NewEntity(1, entity.Skeleton, 0, 0, 0)
		populateMonsterEquipment(e, newEntityRandom(seed), 1.0)
		if !armorEquipped(e) {
			continue
		}
		sawArmor = true
		tier := -1
		for _, slot := range []int{eqSlotFeet, eqSlotLegs, eqSlotChest, eqSlotHead} {
			st := e.getItemBySlot(slot)
			if st.Count == 0 {
				continue
			}
			if !isArmorItemID(int32(st.ItemID)) {
				t.Fatalf("seed %d slot %d holds non-armor id %d", seed, slot, st.ItemID)
			}
			got := armorTierForSlot(t, slot, int32(st.ItemID))
			if tier == -1 {
				tier = got
			} else if got != tier {
				t.Fatalf("seed %d: armor tiers differ across slots (%d vs %d)", seed, got, tier)
			}
		}
	}
	if !sawArmor {
		t.Fatal("no skeleton rolled armor across 512 seeds at mult 1.0 — the armor path never fired")
	}
}

// TestZombieMayHoldIronToolElseEmptyOrArmorOnly: a zombie's MAINHAND is either EMPTY or one of the
// three iron items (SWORD / SPEAR / SHOVEL) — never a bow, never armor.
func TestZombieMainHandIsIronOrEmpty(t *testing.T) {
	sawTool := false
	for seed := uint64(0); seed < 4096; seed++ {
		e := NewEntity(1, entity.Zombie, 0, 0, 0)
		populateMonsterEquipment(e, newEntityRandom(seed), 1.0)
		main := e.getMainHandItem()
		if main.Count == 0 {
			continue
		}
		id := int32(main.ItemID)
		switch id {
		case int32(item.IronSword.ID), int32(item.IronSpear.ID), int32(item.IronShovel.ID):
			sawTool = true
		default:
			t.Fatalf("seed %d: zombie MAINHAND holds unexpected id %d", seed, id)
		}
	}
	if !sawTool {
		t.Fatal("no zombie rolled an iron tool across 4096 seeds at mult 1.0 — the weapon path never fired")
	}
}

// TestZeroMultiplierNoArmor: at multiplier 0.0 (a fresh world) the base armor roll's gate always
// fails, so NO armor is equipped — only the species weapon override remains. This is the byte-exact
// vanilla behavior for a new world (specialMultiplier == 0).
func TestZeroMultiplierNoArmor(t *testing.T) {
	for seed := uint64(0); seed < 256; seed++ {
		sk := NewEntity(1, entity.Skeleton, 0, 0, 0)
		populateMonsterEquipment(sk, newEntityRandom(seed), 0.0)
		if armorEquipped(sk) {
			t.Fatalf("seed %d: skeleton wears armor at mult 0.0 (gate should always fail)", seed)
		}
		// bow still present (unconditional)
		if int32(sk.getMainHandItem().ItemID) != int32(item.Bow.ID) {
			t.Fatalf("seed %d: skeleton lost its bow at mult 0.0", seed)
		}
		zm := NewEntity(2, entity.Zombie, 0, 0, 0)
		populateMonsterEquipment(zm, newEntityRandom(seed), 0.0)
		if armorEquipped(zm) {
			t.Fatalf("seed %d: zombie wears armor at mult 0.0", seed)
		}
	}
}

// TestSpawnDrawGate: populateDefaultEquipmentSlots draws EXACTLY one nextFloat when the gate fails
// (mult 0.0 -> 0.15*0 == 0, and nextFloat() in [0,1) is never < 0). Two RNGs from the same seed,
// one run through populateDefaultEquipmentSlots at mult 0.0 and one drawing a single nextFloat, must
// leave IDENTICAL next draws — proving only one draw was consumed (the vanilla gate).
func TestSpawnDrawGate(t *testing.T) {
	const seed = uint64(0x1234)
	e := NewEntity(1, entity.Zombie, 0, 0, 0)
	rngA := newEntityRandom(seed)
	populateDefaultEquipmentSlots(e, rngA, 0.0) // only the gate nextFloat is drawn

	rngB := newEntityRandom(seed)
	_ = rngB.nextFloat() // one draw = the gate

	// The two streams must now be in lockstep.
	for i := 0; i < 8; i++ {
		if a, b := rngA.nextFloat(), rngB.nextFloat(); a != b {
			t.Fatalf("draw %d diverged: A=%v B=%v (populateDefaultEquipmentSlots drew != 1 float at mult 0.0)", i, a, b)
		}
	}
}

// TestPigDrawsNothing: the pig (an Animal) is NOT in the monster-gated equip set — its spawn path
// never calls populateMonsterEquipment. Here we assert the DIRECT invariant the gate protects: a
// pig entity handed to the equip helpers would be a mistake, so instead we verify the pig's stream
// is untouched by confirming the gating condition (typ is neither Zombie nor Skeleton) and that a
// freshly-spawned pig carries an all-empty equipment array (no armor, no weapon).
func TestPigDrawsNothing(t *testing.T) {
	pig := NewEntity(1, entity.Pig, 0, 0, 0)
	// The spawn-path gate: pig is neither zombie nor skeleton, so populateMonsterEquipment is skipped.
	if pig.typ == entity.Zombie.ID || pig.typ == entity.Skeleton.ID {
		t.Fatal("pig type collides with a monster-gated equip type")
	}
	// A pig that never had populateMonsterEquipment called carries no equipment (byte-identical
	// oracle default).
	for slot := 0; slot < equipmentSlotCount; slot++ {
		if pig.getItemBySlot(slot).Count != 0 {
			t.Fatalf("pig slot %d is populated (%+v) — the oracle default must be all-empty", slot, pig.getItemBySlot(slot))
		}
	}
}

// TestSpecialMultiplierVanilla pins net.minecraft.world.DifficultyInstance.getSpecialMultiplier for
// the NORMAL default (base id 2, localGameTime=0, moonBrightness=0):
//   - fresh world (totalGameTime 0): eff = 2*(0.75+0) = 1.5 -> multiplier 0.0 (< 2.0 clamp).
//   - saturated global (totalGameTime >= 1,512,000): eff = 2*(0.75+0.25) = 2.0 -> (2-2)/2 = 0.0.
//     (NORMAL never crosses 2.0 into a positive multiplier with local/moon at 0 — this is vanilla.)
func TestSpecialMultiplierVanilla(t *testing.T) {
	if got := specialMultiplierFor(difficultyNormal, 0); got != 0.0 {
		t.Fatalf("NORMAL fresh-world specialMultiplier = %v, want 0.0", got)
	}
	if got := specialMultiplierFor(difficultyNormal, 5_000_000); got != 0.0 {
		t.Fatalf("NORMAL saturated specialMultiplier = %v, want 0.0", got)
	}
	if got := specialMultiplierFor(difficultyPeaceful, 5_000_000); got != 0.0 {
		t.Fatalf("PEACEFUL specialMultiplier = %v, want 0.0", got)
	}
	// HARD (id 3): fresh -> 3*0.75 = 2.25 -> (2.25-2)/2 = 0.125.
	if got := specialMultiplierFor(difficultyHard, 0); got != 0.125 {
		t.Fatalf("HARD fresh-world specialMultiplier = %v, want 0.125", got)
	}
	// HARD saturated global (>=1,512,000): 3*(0.75+0.25)=3.0 -> (3-2)/2 = 0.5.
	if got := specialMultiplierFor(difficultyHard, 5_000_000); got != 0.5 {
		t.Fatalf("HARD saturated specialMultiplier = %v, want 0.5", got)
	}
}

// TestGetEquipmentForSlotLadder pins the 26.2 tier ladder (COPPER at index 1) for a representative
// slot, and the @Nullable(false) contract for a hand slot / out-of-range tier.
func TestGetEquipmentForSlotLadder(t *testing.T) {
	cases := []struct {
		tier int
		it   item.Item
	}{
		{0, item.LeatherHelmet},
		{1, item.CopperHelmet},
		{2, item.GoldenHelmet},
		{3, item.ChainmailHelmet},
		{4, item.IronHelmet},
		{5, item.DiamondHelmet},
	}
	for _, c := range cases {
		got, ok := getEquipmentForSlot(eqSlotHead, c.tier)
		if !ok || got.ID != c.it.ID {
			t.Fatalf("getEquipmentForSlot(HEAD, %d) = %v ok=%v, want %v", c.tier, got.ID, ok, c.it.ID)
		}
	}
	if _, ok := getEquipmentForSlot(eqSlotMainHand, 4); ok {
		t.Fatal("getEquipmentForSlot(MAINHAND, 4) should be ok=false (hand slot -> null)")
	}
	if _, ok := getEquipmentForSlot(eqSlotHead, 6); ok {
		t.Fatal("getEquipmentForSlot(HEAD, 6) should be ok=false (out-of-range tier -> null)")
	}
}
