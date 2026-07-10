package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestRavagerStunOnBlockedAttack: the STUN TRIGGER end-to-end. A player SHIELD-BLOCKS a ravager's front
// melee hit; the block path (applyItemBlocking -> blockUsingItem -> Ravager.blockedByItem) fires. On the
// nextDouble()<0.5 branch the ravager stuns (stunnedTick=40) + the player is pushed; on the other branch
// the player is strongKnockback'd. The branch is predicted deterministically from a twin-seeded rng, so
// the assertion is exact. Cite Ravager.blockedByItem (stunnedTick=40; nextDouble 0.5 gate).
func TestRavagerStunOnBlockedAttack(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	p.x, p.z = 0, 0
	p.headYaw = 0 // looking +Z
	equipShieldBlocking(p, 20)

	// A ravager attacker in front (+Z) so the front-cone block resolves (angle 0 < 90deg -> full block).
	const seed uint64 = 777 // -> nextDouble 0.054 < 0.5 -> the STUN branch
	rav := &Entity{id: 2, typ: entity.Ravager.ID, x: 0, y: 64, z: 5}
	rav.health = 100
	rav.ai = &mobAI{}
	rav.ai.rng = newEntityRandom(seed)
	loop.only().entities.add(rav)

	// Predict the blockedByItem nextDouble() branch with a twin-seeded rng (same seed -> same first draw).
	predictStun := newEntityRandom(seed).nextDouble() < 0.5

	src := damageSourceMobAttack(rav.id)
	loop.applyDamage(p, src, 6)

	if predictStun {
		if rav.ravagerStunnedTick != ravagerStunDuration {
			t.Fatalf("blocked hit (stun branch): stunnedTick = %d, want %d", rav.ravagerStunnedTick, ravagerStunDuration)
		}
	} else {
		if rav.ravagerStunnedTick != 0 {
			t.Fatalf("blocked hit (knockback branch): stunnedTick = %d, want 0", rav.ravagerStunnedTick)
		}
		// strongKnockback pushes the player AWAY (-Z, since the ravager is at +Z of the player).
		if p.playerEntity != nil && p.playerEntity.vz >= 0 {
			t.Fatalf("blocked hit (knockback branch): player vz = %v, want < 0 (pushed away)", p.playerEntity.vz)
		}
	}
	// The block negated the damage regardless of the branch (front full block).
	if p.health != maxHealth {
		t.Fatalf("blocked hit: player health = %v, want %v (full front block)", p.health, maxHealth)
	}
}

// TestRavagerBlockedByItemRoaringNoDraw: when the ravager is already roaring (roarTick != 0), a blocked
// hit does NOTHING and draws NO RNG (Ravager.blockedByItem early-out). Verified by comparing the ravager's
// rng stream position before/after: a fresh draw must equal the twin's FIRST draw (i.e. the trigger did
// not consume). Cite Ravager.blockedByItem (if roarTick != 0 return).
func TestRavagerBlockedByItemRoaringNoDraw(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := combatPlayer(loop, 1)
	const seed uint64 = 777
	rav := &Entity{id: 2, typ: entity.Ravager.ID, x: 0, y: 64, z: 0}
	rav.health = 100
	rav.ravagerRoarTick = 15 // already roaring
	rav.ai = &mobAI{}
	rav.ai.rng = newEntityRandom(seed)

	loop.ravagerBlockedByItem(rav, p)

	if rav.ravagerStunnedTick != 0 {
		t.Fatalf("roaring ravager stunned on block: stunnedTick = %d, want 0", rav.ravagerStunnedTick)
	}
	// No RNG consumed: the ravager's next draw equals a twin-seeded fresh rng's first draw.
	got := mobRandom(rav).nextDouble()
	want := newEntityRandom(seed).nextDouble()
	if got != want {
		t.Fatalf("roaring blockedByItem consumed RNG: next draw %v != fresh %v", got, want)
	}
}

// TestRavagerStunEffectRNGDraw: stunEffect() draws exactly random.nextInt(6), and on the ==0 result two
// more random.nextDouble() (the particle offsets). The draw COUNT/ORDER is what keeps the ravager stream
// byte-exact while stunned (the particle emit is a cited client deferral). Verified by advancing a twin
// rng through the identical draws and asserting the streams stay in lockstep across several stunEffect
// calls. Cite Ravager.stunEffect (nextInt(6); [nextDouble; nextDouble]).
func TestRavagerStunEffectRNGDraw(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	const seed uint64 = 424242
	rav := &Entity{id: 3, typ: entity.Ravager.ID}
	rav.ai = &mobAI{}
	rav.ai.rng = newEntityRandom(seed)

	twin := newEntityRandom(seed)

	// Replay stunEffect's exact draw sequence on the twin for several calls, then assert the ravager's
	// stream is still in lockstep (a following nextDouble matches). If stunEffect drew a wrong count, the
	// streams would diverge here.
	for i := 0; i < 8; i++ {
		loop.ravagerStunEffect(rav)
		if twin.nextInt(6) == 0 { // stunEffect first draw
			_ = twin.nextDouble() // x offset
			_ = twin.nextDouble() // z offset
		}
	}
	if got, want := mobRandom(rav).nextDouble(), twin.nextDouble(); got != want {
		t.Fatalf("stunEffect RNG draw-count diverged: ravager next %v != twin %v", got, want)
	}
}

// TestRavagerLeafTrample: a MOVING ravager (horizontalCollision set) with mobGriefing on destroys the
// LeavesBlock cells inside its inflated bounding box (leaf -> air). A non-leaf cell is untouched. Cite
// Ravager.aiStep (LeavesBlock -> ServerLevel.destroyBlock).
func TestRavagerLeafTrample(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaRaiderRegistry(t, "vanilla_ravager"))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	decl := loop.mobRegistry.byName["vanilla_ravager"]
	rv := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	rv.onGround = true
	rv.horizontalCollision = true // the leaf-trample gate: only when the ravager is colliding horizontally

	// Place oak_leaves inside the ravager's AABB.inflate(0.2) footprint (a cell at the ravager's feet).
	leaf := oakLeaves(7, false)
	leafPos := pk.Position{X: 8, Y: floorY + 1, Z: 8}
	if !loop.world().SetBlock(leafPos, leaf, dimMinY) {
		t.Fatalf("failed to place leaf for trample setup")
	}
	if !block.IsLeaves(loop.blockStateAt(leafPos.X, leafPos.Y, leafPos.Z)) {
		t.Fatalf("test setup: placed block is not a leaf (id %d)", loop.blockStateAt(leafPos.X, leafPos.Y, leafPos.Z))
	}

	loop.withRegion(loop.only(), func() {
		loop.ravagerLeafTrample(rv)
	})

	if block.IsLeaves(loop.blockStateAt(leafPos.X, leafPos.Y, leafPos.Z)) {
		t.Fatalf("leaf-trample did NOT destroy the leaf in the ravager AABB (still %d)", loop.blockStateAt(leafPos.X, leafPos.Y, leafPos.Z))
	}
}

// TestRavagerLeafTrampleGatedOffWhenNotMoving: with horizontalCollision unset, the ravager does NOT break
// leaves (the offset-96 gate). Cite Ravager.aiStep (if (this.horizontalCollision)).
func TestRavagerLeafTrampleGatedOffWhenNotMoving(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaRaiderRegistry(t, "vanilla_ravager"))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	decl := loop.mobRegistry.byName["vanilla_ravager"]
	rv := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	rv.onGround = true
	rv.horizontalCollision = false // NOT colliding -> the leaf-trample loop never runs

	leaf := oakLeaves(7, false)
	leafPos := pk.Position{X: 8, Y: floorY + 1, Z: 8}
	loop.world().SetBlock(leafPos, leaf, dimMinY)

	loop.withRegion(loop.only(), func() {
		loop.ravagerLeafTrample(rv)
	})

	if !block.IsLeaves(loop.blockStateAt(leafPos.X, leafPos.Y, leafPos.Z)) {
		t.Fatalf("leaf-trample ran while not colliding (leaf destroyed) — the horizontalCollision gate failed")
	}
}
