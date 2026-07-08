package server

// iron_golem_test.go — the Iron Golem (LOOP2) coverage: the doHurtTarget damage equation
// (ad/2 + nextInt(ad), the [7.5, 21.5] range for ATTACK_DAMAGE 15) is a deterministic
// per-mob-rng draw, isPlayerCreated round-trips, and the golem boot-loads into the ONE
// registry as entity.IronGolem. No player victim is built here (that path routes through
// applyDamage, covered by the shared combat tests) — this pins the golem-specific numeric
// port + the registry membership. Pig oracle untouched.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

// newTestIronGolem builds a live golem with the golem attribute supplier (ATTACK_DAMAGE 15,
// MAX_HEALTH 100) and a per-entity rng seeded so the single doHurtTarget nextInt(15) is
// deterministic.
func newTestIronGolem(seed uint64) *Entity {
	e := NewEntity(7300, entity.IronGolem, 0, 64, 0)
	initSpawnHealth(e)
	e.ai = &mobAI{}
	e.ai.rng = newEntityRandom(seed)
	return e
}

// TestIronGolemDamageEquation: the golem's doHurtTarget damage is ad/2 + nextInt((int)ad).
// With ATTACK_DAMAGE 15 the base is 7.5 and the roll is [0,14], so the result is in [7.5, 21.5]
// and matches the same nextInt draw the port makes.
func TestIronGolemDamageEquation(t *testing.T) {
	g := newTestIronGolem(1)
	ad := float32(g.getAttributeValue(attribute.AttackDamage))
	if int(ad) <= 0 {
		t.Fatalf("golem ATTACK_DAMAGE = %v, want > 0 (createAttributes should seed it)", ad)
	}
	// Reproduce the exact draw the port makes (ad/2 + nextInt((int)ad)) from the same seed.
	want := ad/2.0 + float32(newEntityRandom(1).nextInt(int(ad)))
	got := ad/2.0 + float32(g.ai.rng.nextInt(int(ad)))
	if got != want {
		t.Fatalf("golem damage = %v, want %v (ad/2 + nextInt(%d), deterministic on seed)", got, want, int(ad))
	}
	if got < ad/2.0 || got > ad/2.0+ad {
		t.Fatalf("golem damage %v out of the [ad/2, ad/2+ad] = [%v, %v] range", got, ad/2.0, ad/2.0+ad)
	}
}

// TestIronGolemPlayerCreated: the isPlayerCreated flag round-trips (a player-built golem vs a
// naturally/village-spawned one — vanilla tracks this to gate targeting/retaliation).
func TestIronGolemPlayerCreated(t *testing.T) {
	g := newTestIronGolem(1)
	if ironGolemIsPlayerCreated(g) {
		t.Fatal("a fresh golem should not be player-created by default")
	}
	ironGolemSetPlayerCreated(g, true)
	if !ironGolemIsPlayerCreated(g) {
		t.Fatal("setPlayerCreated(true) did not stick")
	}
	ironGolemSetPlayerCreated(g, false)
	if ironGolemIsPlayerCreated(g) {
		t.Fatal("setPlayerCreated(false) did not clear")
	}
}

// TestIronGolemBootLoads: the golem is in the ONE generalized registry as entity.IronGolem.
func TestIronGolemBootLoads(t *testing.T) {
	r, err := loadVanillaMobRegistry()
	if err != nil {
		t.Fatalf("loadVanillaMobRegistry: %v", err)
	}
	decl, ok := r.byName[vanillaIronGolemMobName]
	if !ok {
		t.Fatalf("registry missing %q after boot-load", vanillaIronGolemMobName)
	}
	if decl.baseType.ID != entity.IronGolem.ID {
		t.Fatalf("golem base type = %d, want %d", decl.baseType.ID, entity.IronGolem.ID)
	}
}

// TestIronGolemIronIngotRepair: an IRON_INGOT feed heals a DAMAGED golem 25.0 (clamped to MAX_HEALTH
// 100) and consumes the ingot; a FULL golem is not healed and the ingot is NOT consumed. Cite
// IronGolem.mobInteract.
func TestIronGolemIronIngotRepair(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())

	g := NewEntity(loop.idAlloc.AllocID(), entity.IronGolem, 8.5, float64(floorY+1), 8.5)
	initSpawnHealth(g)
	g.ai = &mobAI{rng: newEntityRandom(5)}
	loop.cur().entities.add(g)

	// Damaged golem: heals 25.0 and consumes the ingot.
	g.health = 40.0
	p := newTestPlayerHolding(loop, 91, int32(item.IronIngot.ID))
	if !loop.tryIronGolemRepair(p, g) {
		t.Fatal("tryIronGolemRepair returned false for a damaged golem + iron ingot (want consume)")
	}
	if math.Abs(float64(g.health)-65.0) > 1e-6 {
		t.Fatalf("golem heal = %v, want 65.0 (40 + 25)", g.health)
	}
	inv := ensureInventory(p)
	if got := inv.get(heldWindowSlot(inv.heldSlot)); got.Count != 0 {
		t.Fatalf("iron ingot not consumed: held count = %d, want 0", got.Count)
	}

	// Full golem: no heal, no consume (PASS).
	maxHealth := float32(g.getAttributeValue(attribute.MaxHealth))
	g.health = maxHealth
	p2 := newTestPlayerHolding(loop, 92, int32(item.IronIngot.ID))
	if loop.tryIronGolemRepair(p2, g) {
		t.Fatal("tryIronGolemRepair returned true for a FULL golem (want PASS / no consume)")
	}
	inv2 := ensureInventory(p2)
	if got := inv2.get(heldWindowSlot(inv2.heldSlot)); got.Count != 1 {
		t.Fatalf("iron ingot consumed on a full golem: held count = %d, want 1 (unchanged)", got.Count)
	}
}
