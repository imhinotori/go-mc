package server

// pillager_test.go — RAIDER (Task): the per-mob behavior test for the Pillager, the 1:1 vanilla Pillager
// re-expressed as the vanilla_pillager Starlark plugin (float@0 + Go-native pillager_crossbow_attack@3 +
// hurt_by@1 + nearest@2 + patrol@4). The CORE assertion is the phase goal "the pillager HUNTS the player
// and FIRES its crossbow": a spawned pillager + a player drives ticks until the pillager ACQUIRES the player
// AND a crossbow ARROW is spawned into the store (the RangedCrossbowAttackGoal charged + released). Cite
// Pillager.registerGoals @3 RangedCrossbowAttackGoal + Pillager.performRangedAttack.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
)

// TestPillagerFiresCrossbow: a spawned pillager acquires a player in crossbow range and FIRES an arrow.
func TestPillagerFiresCrossbow(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaRaiderRegistry(t, "vanilla_pillager"))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	if got := categoryOf(entity.Pillager.ID); got != categoryMonster {
		t.Fatalf("categoryOf(Pillager) = %v, want categoryMonster", got)
	}

	decl := loop.mobRegistry.byName["vanilla_pillager"]
	pl := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	pl.onGround = true
	if pl.typ != entity.Pillager.ID {
		t.Fatalf("pillager typ = %d, want %d", pl.typ, entity.Pillager.ID)
	}

	// Player ~5 blocks away (inside crossbow attackRadius 8 -> radiusSqr 64) so the goal stops moving + fires.
	p := combatTestPlayer(loop, 13.5, float64(floorY+1), 8.5, 7403)

	acquired := false
	firedArrow := false
	for i := 0; i < 400 && !firedArrow; i++ {
		clock.add(tickStep)
		loop.advance(clock.Now())
		if pl.ai.getTarget() == p.entityID {
			acquired = true
		}
		for _, e := range loop.only().entities.byID {
			if e.isArrow && e.arrowShooterID == pl.id {
				firedArrow = true
				break
			}
		}
	}
	if !acquired {
		t.Fatal("the pillager never ACQUIRED the player")
	}
	if !firedArrow {
		t.Fatal("the pillager never FIRED a crossbow arrow (RangedCrossbowAttackGoal did not release)")
	}
}

// TestCrossbowLobWidenedLiteral proves the mob crossbow lob uses dist*0.20000000298023224 (the float 0.2f
// widened to double, CrossbowItem.shootProjectile) rather than a plain 0.2 double -- a trajectory-shifting
// 1:1 literal. It reseeds the pillager RNG so the 3 triangle-spread draws are reproducible, fires the
// crossbow, then recomputes the expected arrow velocity with the widened lob (must match to the bit) and
// with 0.2 (must differ). Cite CrossbowItem.shootProjectile + Pillager.performRangedAttack.
func TestCrossbowLobWidenedLiteral(t *testing.T) {
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(loadVanillaRaiderRegistry(t, "vanilla_pillager"))
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())

	decl := loop.mobRegistry.byName["vanilla_pillager"]
	pl := loop.spawnDeclaredMob(decl, 8.5, float64(floorY+1), 8.5)
	pl.onGround = true
	// Target offset so the lob (dist*const) is a meaningful fraction of yd -> the widened vs plain constant
	// resolves to a DIFFERENT normalized vector.
	p := combatTestPlayer(loop, 8.5+6.0, float64(floorY+1)+0.0, 8.5+3.0, 7411)

	const seed uint64 = 0xABCDEF0123456789

	// Fire through the real code path with a fixed seed.
	pl.ai.rng.reseed(seed)
	before := 0
	for _, e := range loop.only().entities.byID {
		if e.isArrow {
			before++
		}
	}
	loop.performCrossbowAttack(pl, p)
	var arrow *Entity
	for _, e := range loop.only().entities.byID {
		if e.isArrow {
			arrow = e
		}
	}
	if arrow == nil {
		t.Fatal("pillager crossbow fired no arrow")
	}

	// Recompute the expected velocity with the SAME seed + the WIDENED lob constant.
	recompute := func(lob float64) (float64, float64, float64) {
		r := newEntityRandom(seed)
		launchY := pl.y + pl.height*0.85 // eyeHeightForArrow() == e.height*0.85 (float64), the crossbow launch Y
		xd := p.x - pl.x
		yd := (p.y + float64(playerHeight)*0.3333333333333333) - launchY
		zd := p.z - pl.z
		dist := math.Sqrt(xd*xd + zd*zd)
		ydLob := yd + dist*lob
		diff := float64(serverDifficulty)
		inaccuracy := 14.0 - diff*4.0
		vx, vy, vz := normalizeVec3(xd, ydLob, zd)
		spread := 0.0172275 * inaccuracy
		vx += arrowTriangle(r, 0, spread)
		vy += arrowTriangle(r, 0, spread)
		vz += arrowTriangle(r, 0, spread)
		vx *= crossbowMobArrowPower
		vy *= crossbowMobArrowPower
		vz *= crossbowMobArrowPower
		return vx, vy, vz
	}
	wx, wy, wz := recompute(0.20000000298023224)
	if arrow.vx != wx || arrow.vy != wy || arrow.vz != wz {
		t.Fatalf("arrow velocity (%v,%v,%v) != widened-lob recompute (%v,%v,%v)", arrow.vx, arrow.vy, arrow.vz, wx, wy, wz)
	}
	px, py, pz := recompute(0.2)
	if wx == px && wy == py && wz == pz {
		t.Fatal("widened 0.20000000298023224 and plain 0.2 produced identical velocity -- pick a target where the lob matters")
	}
	// And the real arrow must NOT match the plain-0.2 computation.
	if arrow.vx == px && arrow.vy == py && arrow.vz == pz {
		t.Fatal("arrow matches the PLAIN 0.2 lob -- the widened literal fix is not in effect")
	}
}
