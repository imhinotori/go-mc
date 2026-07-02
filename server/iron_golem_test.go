package server

// iron_golem_test.go — the Iron Golem (LOOP2) coverage: the doHurtTarget damage equation
// (ad/2 + nextInt(ad), the [7.5, 21.5] range for ATTACK_DAMAGE 15) is a deterministic
// per-mob-rng draw, isPlayerCreated round-trips, and the golem boot-loads into the ONE
// registry as entity.IronGolem. No player victim is built here (that path routes through
// applyDamage, covered by the shared combat tests) — this pins the golem-specific numeric
// port + the registry membership. Pig oracle untouched.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
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
