package server

// ai_goals_creeper.go — MOB-HOST-06 (Task #9): the Creeper's SwellGoal + the fuse tick, PORTED 1:1 from
// the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, CFR/javap this session):
//
//   - net.minecraft.world.entity.ai.goal.SwellGoal: flags {MOVE}, requiresUpdateEveryTick. It reads the
//     creeper's target + distance and DRIVES setSwellDir(±1) — it does NOT itself explode. canUse arms
//     when already swelling OR a live target is within 3 blocks (dist²<9); tick disarms (-1) with no
//     target / target dead / dist²>49 (7 blocks) / no line-of-sight, else arms (+1).
//   - Creeper.tick advances swell by swellDir each tick and, at swell>=maxSwell (30), explodeCreeper()
//     (creeperAiStep below — the per-type hook, the sibling of chickenAiStep).
//
// v1 STUBS (cited): line-of-sight (Sensing.hasLineOfSight) is the always-true stub (no sensing subsystem),
// so tick's !hasLineOfSight disarm never fires; the primed-fuse SOUND + the swell client METADATA
// (DATA_SWELL_DIR/DATA_IS_POWERED/DATA_IS_IGNITED) are cite-deferred client visuals (the creeper still
// fuses + explodes with REAL damage/blocks — the gameplay — exactly as the zombie's raise-arm bit was
// deferred). The explosion math lives in explosion.go (level.explode port).

// SwellGoal / Creeper.tick constants (verified CFR).
const (
	creeperSwellArmDistSqr    = 9.0  // SwellGoal.canUse: distanceToSqr(target) < 9.0 (arm within 3 blocks)
	creeperSwellDisarmDistSqr = 49.0 // SwellGoal.tick: distanceToSqr(target) > 49.0 (disarm past 7 blocks)
	creeperMaxSwellDefault    = 30   // Creeper.maxSwell default (the 30-tick fuse)
	creeperExplosionRadius    = 3    // Creeper.explosionRadius default
)

// swellGoal is the ported SwellGoal (net.minecraft.world.entity.ai.goal.SwellGoal). It holds the target
// snapshot the tick disarm checks (SwellGoal.target).
type swellGoal struct {
	baseGoal
	targetID int32 // SwellGoal.target (the LivingEntity captured at start)
}

func newSwellGoal() *swellGoal {
	return &swellGoal{baseGoal: newBaseGoal(flagMove)}
}

func (g *swellGoal) requiresUpdateEveryTick() bool { return true }

// canUse: creeper.getSwellDir() > 0 || (target != null && !target.isDeadOrDying() && dist²<9.0).
func (g *swellGoal) canUse(t *TickLoop, e *Entity) bool {
	if e.swellDir > 0 {
		return true
	}
	id := mobTarget(e)
	if id == 0 {
		return false
	}
	target := t.playerByEntityID(id)
	if target == nil || target.dead {
		return false
	}
	return distanceToSqrPlayer(target, e) < creeperSwellArmDistSqr
}

// start: navigation.stop(); target = getTarget(). Park the creeper (it swells in place).
func (g *swellGoal) start(t *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.clearWantTarget()
	}
	g.targetID = mobTarget(e)
}

// stop: target = null.
func (g *swellGoal) stop(t *TickLoop, e *Entity) {
	g.targetID = 0
}

// tick: disarm (setSwellDir(-1)) with no/dead target, dist²>49, or no LoS; else arm (setSwellDir(1)).
func (g *swellGoal) tick(t *TickLoop, e *Entity) {
	target := t.playerByEntityID(g.targetID)
	if target == nil || target.dead {
		e.swellDir = -1
		return
	}
	if distanceToSqrPlayer(target, e) > creeperSwellDisarmDistSqr {
		e.swellDir = -1
		return
	}
	// !getSensing().hasLineOfSight(target): v1 always-true stub — the disarm never fires here.
	e.swellDir = 1
}

// creeperAiStep is the port of Creeper.tick's fuse advance (the per-type hook, the sibling of
// chickenAiStep). It advances swell by swellDir each tick and, at swell>=maxSwell, explodes. Called from
// tickAI for a live creeper (typ == entity.Creeper.ID), AFTER serverAiStep so the SwellGoal has set
// swellDir this tick.
//
//	[VERIFIED CFR Creeper.tick: oldSwell=swell; if(isIgnited()) setSwellDir(1); swellDir=getSwellDir();
//	 if(swellDir>0 && swell==0){ playSound(CREEPER_PRIMED); } swell += swellDir; if(swell<0) swell=0;
//	 if(swell>=maxSwell){ swell=maxSwell; explodeCreeper(); }.]
func (t *TickLoop) creeperAiStep(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	if e.maxSwell == 0 {
		e.maxSwell = creeperMaxSwellDefault // lazy default (spawn does not set it)
	}
	e.oldSwell = e.swell
	if e.ignited {
		e.swellDir = 1
	}
	swellDir := e.swellDir
	// (swellDir>0 && swell==0): the primed-fuse sound/gameEvent — a cite-deferred client cue.
	e.swell += swellDir
	if e.swell < 0 {
		e.swell = 0
	}
	if e.swell >= e.maxSwell {
		e.swell = e.maxSwell
		t.explodeCreeper(e)
	}
}

// explodeCreeper is the port of Creeper.explodeCreeper: mark dead, run the explosion at the creeper's
// position with radius explosionRadius × (powered ? 2 : 1), then discard the creeper.
//
//	[VERIFIED CFR Creeper.explodeCreeper: multiplier = isPowered()?2:1; dead=true;
//	 level.explode(this, x, y, z, explosionRadius*multiplier, ExplosionInteraction.MOB); discard().]
func (t *TickLoop) explodeCreeper(e *Entity) {
	multiplier := 1.0
	if e.powered {
		multiplier = 2.0
	}
	e.dead = true
	t.explode(e.id, e.x, e.y, e.z, float64(creeperExplosionRadius)*multiplier)
	t.cur().entities.remove(e.id)
}
