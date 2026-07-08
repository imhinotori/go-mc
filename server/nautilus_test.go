package server

// nautilus_test.go -- deterministic pins for the NEW-in-26.2 Nautilus family (AbstractNautilus / Nautilus /
// ZombieNautilus, 1:1 javap this session) and the Mannequin (Avatar display entity). Verifies the spawn
// attributes (createAttributes) and the spawn flags/health seed. The brain / rideable / inventory / skin
// data are DEFERRED, so these pin the ported subset: attributes + spawn + classification.

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
)

func nautilusLoop(t *testing.T) (*TickLoop, int) {
	t.Helper()
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.start(loop.clock.(*fakeClock).Now())
	return loop, floorY
}

// TestNautilusSpawnDefaults: spawnNautilus renders as entity.Nautilus.ID, is marked isWaterMob/isNautilus,
// and carries the AbstractNautilus createAttributes: MAX_HEALTH 15.0 (health seeded from it), MOVEMENT_SPEED
// 1.0, ATTACK_DAMAGE 3.0, KNOCKBACK_RESISTANCE 0.30000001192092896.
func TestNautilusSpawnDefaults(t *testing.T) {
	loop, floorY := nautilusLoop(t)
	n := loop.spawnNautilus(8.5, float64(floorY+1), 8.5)
	if n.typ != entity.Nautilus.ID {
		t.Fatalf("nautilus typ = %d, want entity.Nautilus.ID %d", n.typ, entity.Nautilus.ID)
	}
	if !n.isWaterMob || !n.isNautilus {
		t.Fatal("nautilus not marked isWaterMob/isNautilus")
	}
	if n.isZombieNautilus {
		t.Fatal("nautilus unexpectedly marked isZombieNautilus")
	}
	if hp := n.getAttributeValue(attribute.MaxHealth); hp != 15.0 {
		t.Fatalf("nautilus MAX_HEALTH = %v, want 15.0", hp)
	}
	if n.health != 15.0 {
		t.Fatalf("nautilus health = %v, want == MAX_HEALTH 15.0", n.health)
	}
	if sp := n.getAttributeValue(attribute.MovementSpeed); sp != 1.0 {
		t.Fatalf("nautilus MOVEMENT_SPEED = %v, want 1.0", sp)
	}
	if ad := n.getAttributeValue(attribute.AttackDamage); ad != 3.0 {
		t.Fatalf("nautilus ATTACK_DAMAGE = %v, want 3.0", ad)
	}
	if kb := n.getAttributeValue(attribute.KnockbackResistance); kb != 0.30000001192092896 {
		t.Fatalf("nautilus KNOCKBACK_RESISTANCE = %v, want 0.30000001192092896", kb)
	}
	if n.ai == nil || n.ai.rng == nil {
		t.Fatal("nautilus has no minimal swim AI / rng")
	}
}

// TestZombieNautilusSpawnDefaults: spawnZombieNautilus renders as entity.ZombieNautilus.ID, is marked
// isWaterMob/isNautilus/isZombieNautilus (undead), and overrides MOVEMENT_SPEED to 1.100000023841858 while
// keeping the AbstractNautilus base MAX_HEALTH 15.0 / ATTACK_DAMAGE 3.0.
func TestZombieNautilusSpawnDefaults(t *testing.T) {
	loop, floorY := nautilusLoop(t)
	n := loop.spawnZombieNautilus(8.5, float64(floorY+1), 8.5)
	if n.typ != entity.ZombieNautilus.ID {
		t.Fatalf("zombie_nautilus typ = %d, want entity.ZombieNautilus.ID %d", n.typ, entity.ZombieNautilus.ID)
	}
	if !n.isWaterMob || !n.isNautilus || !n.isZombieNautilus {
		t.Fatal("zombie_nautilus not marked isWaterMob/isNautilus/isZombieNautilus")
	}
	if hp := n.getAttributeValue(attribute.MaxHealth); hp != 15.0 {
		t.Fatalf("zombie_nautilus MAX_HEALTH = %v, want 15.0", hp)
	}
	if sp := n.getAttributeValue(attribute.MovementSpeed); sp != 1.100000023841858 {
		t.Fatalf("zombie_nautilus MOVEMENT_SPEED = %v, want 1.100000023841858", sp)
	}
	if ad := n.getAttributeValue(attribute.AttackDamage); ad != 3.0 {
		t.Fatalf("zombie_nautilus ATTACK_DAMAGE = %v, want 3.0", ad)
	}
	if n.health != 15.0 {
		t.Fatalf("zombie_nautilus health = %v, want == MAX_HEALTH 15.0", n.health)
	}
}

// TestMannequinSpawnDefaults: spawnMannequin renders as entity.Mannequin.ID, is marked isMannequin with the
// immovable flag, has NO goal AI (it is a static Avatar display), and seeds health from the living-default
// MaxHealth.
func TestMannequinSpawnDefaults(t *testing.T) {
	loop, floorY := nautilusLoop(t)
	m := loop.spawnMannequin(8.5, float64(floorY+1), 8.5, true)
	if m.typ != entity.Mannequin.ID {
		t.Fatalf("mannequin typ = %d, want entity.Mannequin.ID %d", m.typ, entity.Mannequin.ID)
	}
	if !m.isMannequin {
		t.Fatal("mannequin not marked isMannequin")
	}
	if !m.mannequinImmovable {
		t.Fatal("mannequin not marked immovable")
	}
	if m.ai != nil {
		t.Fatal("mannequin should have NO goal AI (Avatar display entity)")
	}
	if m.health <= 0 {
		t.Fatalf("mannequin health = %v, want > 0 (seeded from MaxHealth)", m.health)
	}
}
