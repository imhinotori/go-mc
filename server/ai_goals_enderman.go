package server

// ai_goals_enderman.go — MOB-HOST-08 (Task #9): the EnderMan's teleport subsystem + the daylight-flee
// tick, ported 1:1 from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR this session):
//
//   - EnderMan.teleport(): pick a random point (x ± 32, y ± 32, z ± 32-ish: nextDouble()-0.5)*64 for x/z,
//     nextInt(64)-32 for y), snap DOWN to the first block that blocksMotion, reject if not standable or in
//     water, else randomTeleport there (move + ENDERMAN_TELEPORT sound/gameEvent).
//   - EnderMan.customServerAiStep: the daylight-flee — if brightOutside && tickCount≥targetChangeTime+600
//     && magic>0.5 && canSeeSky && nextFloat()*30 < (br-0.4)*2 → setTarget(null) + teleport().
//   - EnderMan.hurtServer: a PROJECTILE hit → try teleport up to 64× (dodge); a non-living-attacker hit →
//     teleport on nextInt(10)!=0. (Melee from a player: NO teleport — the enderman stands and fights.)
//
// v1 STUBS (cited): the gaze subsystem (EndermanFreezeWhenLookedAt / EndermanLookForPlayerGoal — "look at
// the enderman to aggro it") needs a player-look-direction read that does not exist in v1 → cite-deferred
// (the enderman still hunts via the base nearest-target/hurt-by goals). Block-carry (LeaveBlock/TakeBlock)
// + Endermite target + universal-anger-reset are cite-deferred (no held-block/endermite subsystems). The
// magic-value/targetChangeTime daylight preconditions collapse to the same isDay+canSeeSky gate the
// sunBurnTick uses (no light engine); the br==1.0 roll is identical.

import (
	"math"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// endermanAiStep is the port of EnderMan.customServerAiStep's daylight-flee (the per-type hook, sibling of
// creeperAiStep/chickenAiStep). A brightly-lit, sky-exposed enderman randomly teleports away (dropping its
// target). Called from tickAI for a live enderman (typ == entity.Enderman.ID), AFTER serverAiStep.
func (t *TickLoop) endermanAiStep(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	if !t.isDay() || t.entityInWater(e) || !t.canSeeSky(e) {
		return
	}
	// br == 1.0 (day, open sky): nextFloat()*30 < (1.0-0.4)*2 == 1.2 — the SAME gate as sunBurnTick.
	const br = 1.0
	if mobRandom(e).nextFloat()*30.0 < float32((br-0.4)*2.0) {
		if e.ai != nil {
			e.ai.setTarget(0) // setTarget(null)
		}
		t.endermanTeleport(e)
	}
}

// endermanTeleport is the port of EnderMan.teleport(): pick a random 64-block-box point and try to land
// there. Returns whether the teleport succeeded. Draws 3 values (x double, y int, z double) then delegates
// to endermanTeleportTo.
func (t *TickLoop) endermanTeleport(e *Entity) bool {
	r := mobRandom(e)
	xx := e.x + (r.nextDouble()-0.5)*64.0
	yy := e.y + float64(r.nextInt(64)-32)
	zz := e.z + (r.nextDouble()-0.5)*64.0
	return t.endermanTeleportTo(e, xx, yy, zz)
}

// endermanTeleportTo is the port of EnderMan.teleport(x,y,z): snap DOWN from (x,y,z) to the first block
// that blocksMotion, reject if the landing is not standable or is water, else move the enderman there and
// broadcast the teleport. Cite EnderMan.teleport(double,double,double) + randomTeleport.
func (t *TickLoop) endermanTeleportTo(e *Entity, x, y, z float64) bool {
	if t.world() == nil {
		return false
	}
	// Snap down to the first solid (blocksMotion) block below the sampled point.
	bx := int(math.Floor(x))
	bz := int(math.Floor(z))
	by := int(math.Floor(y))
	landY := -1
	for cy := by; cy > dimMinY; cy-- {
		if t.isSolidAt(pk.Position{X: bx, Y: cy, Z: bz}) {
			landY = cy
			break
		}
	}
	if landY < 0 {
		return false // no ground found in the column
	}
	// couldStandOn == blocksMotion (true here); reject water landings (isWet). The enderman stands ON the
	// solid block → its feet are at landY+1.
	feetY := float64(landY + 1)
	if t.fluidAt(pk.Position{X: bx, Y: landY, Z: bz}).isWater {
		return false
	}
	// randomTeleport: move to the block center at feet height. Re-bucket via the store move + broadcast the
	// absolute teleport so trackers see the jump (ClientboundTeleportEntity).
	nx := float64(bx) + 0.5
	nz := float64(bz) + 0.5
	t.cur().entities.move(e, nx, feetY, nz)
	t.broadcastToTrackers(e.id, encodeTeleportEntity(e))
	return true
}

// endermanHurtTeleport is the port of EnderMan.hurtServer's teleport reaction, called AFTER the shared
// applyDamageEntity applies the hit (a per-type post-hurt hook gated on typ == entity.Enderman.ID). A
// PROJECTILE/indirect hit makes the enderman dodge (try teleport up to 64×); a non-living-attacker hit
// teleports on nextInt(10)!=0. A living melee attacker (a player) draws NO teleport — the enderman fights.
// Cite EnderMan.hurtServer.
func (t *TickLoop) endermanHurtTeleport(e *Entity, src damageSource) {
	if e.dead || e.health <= 0 {
		return // a dead enderman does not teleport
	}
	// IS_PROJECTILE (arrow/thrown potion) → dodge: up to 64 teleport attempts.
	if src.is("is_projectile") {
		for i := 0; i < 64; i++ {
			if t.endermanTeleport(e) {
				return
			}
		}
		return
	}
	// A non-living attacker (attacker id resolves to no player/mob) teleports on nextInt(10)!=0. A living
	// attacker (a player melee) does NOT teleport (the enderman stands and fights).
	_, attackerIsMob := t.cur().entities.get(src.attacker)
	attackerIsLiving := t.playerByEntityID(src.attacker) != nil || attackerIsMob
	if !attackerIsLiving && mobRandom(e).nextInt(10) != 0 {
		t.endermanTeleport(e)
	}
}
