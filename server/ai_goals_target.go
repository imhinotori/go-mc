package server

// ai_goals_target.go — MOB-SUB-10 (Phase 35-01): the shared targetSelector goals, PORTED (the
// STANDING MANDATE, idiomatic non-1:1 Go, no GPL paste) from the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar, read via javap -c -p this session):
//
//   - net.minecraft.world.entity.ai.goal.target.NearestAttackableTargetGoal: flags {TARGET};
//     canUse rolls 1-in-randomInterval then scans for the nearest valid player target bounded by
//     FOLLOW_RANGE; start() = mob.setTarget(target). The acquired target sets mobAI.attackTargetID
//     (Mob.getTarget), which the MeleeAttackGoal (ai_goals_attack.go) canUse-gates on.
//   - net.minecraft.world.entity.ai.goal.target.HurtByTargetGoal: flags {TARGET}; canUse reads the
//     entity's lastHurtByMob/lastHurtByMobTimestamp bookkeeping (entity.go, set in combat_mob.go) and
//     retaliates against a fresh attacker; NO RNG. start() = mob.setTarget(getLastHurtByMob()).
//
// These are built ONCE here so the wolf (Phase 36) and every hostile (zombie/skeleton/spider) reuse
// them — the targetSelector is a SECOND independent goalSelector on mobAI (ai_mob.go), so a TARGET
// goal locks the TARGET flag among the targetSelector's goals, never touching the goalSelector's
// MOVE/LOOK locks.
//
// THE PIG ORACLE IS UNTOUCHED: a passive pig declares ZERO TARGET goals, so its targetSelector is
// empty and neither goal ever ticks on it — no new RNG draw reaches the pinned pig stream.
//
//   ⚠ LOCKSTEP-CRITICAL (NearestAttackableTargetGoal): the jar ctor does randomInterval =
//   reducedTickDelay(10) = ceilDiv(10,2) = 5, to compensate for vanilla evaluating TARGET goals every
//   OTHER server tick (the Mob.serverAiStep (tickCount+id)%2 decimation). The Go driver ticks
//   serverAiStep EVERY tick (tick_phases.go — no decimation), so to fire at the vanilla real-world
//   rate the Go gate MUST use the FULL interval = nextInt(10), NOT the halved nextInt(5) — exactly the
//   same identity rule adjustedTickDelay follows. DO NOT call reducedTickDelay here; use the raw 10.
//   (35-JARNOTES.md:149-164; pinned by TestNearestAttackableTargetGateUsesTen.)

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
)

// --- nearestAttackableTargetGoal -------------------------------------------------------------

// nearestTargetRandomInterval is the FAITHFUL Go RNG-gate bound for NearestAttackableTargetGoal:
// the FULL net.minecraft.world.entity.ai.goal.target.NearestAttackableTargetGoal.DEFAULT_RANDOM_INTERVAL
// (== 10), NOT the jar ctor's reducedTickDelay(10) == 5. See the LOCKSTEP-CRITICAL note in the file
// header: the jar halves it to compensate for its every-other-tick TARGET evaluation; our full-rate
// serverAiStep does not decimate, so the raw 10 is the 1:1-faithful value run every tick.
//
//	[VERIFIED javap NearestAttackableTargetGoal.<init>(...,int,...): randomInterval =
//	 reducedTickDelay(10); DEFAULT_RANDOM_INTERVAL == 10. canUse: getRandom().nextInt(randomInterval).]
const nearestTargetRandomInterval = 10

// nearestTargetClass parameterizes WHICH class of entity the goal acquires (the B1/B2 fix, Phase 36-01,
// cite NearestAttackableTargetGoal<T> — the generic target type). The Phase-35 goal was hard-coded to
// the Player branch; the wolf needs a SKELETON branch (NearestAttackableTargetGoal<AbstractSkeleton>)
// AND an anger gate on its Player branch. The zero value is targetClassPlayer, so the EXISTING
// newNearestAttackableTargetGoal() (untouched below) stays the byte-identical Phase-35 Player goal.
type nearestTargetClass int

const (
	// targetClassPlayer is the Player branch: findTarget scans t.players (the existing nearestPlayerIDAt).
	// The zero value — so every Phase-35 hostile keeps this with NO change.
	targetClassPlayer nearestTargetClass = iota
	// targetClassSkeleton is the AbstractSkeleton branch (the wolf @7
	// NearestAttackableTargetGoal<AbstractSkeleton>): findTarget scans the entity store for
	// entity.Skeleton.ID within FOLLOW_RANGE (the mob-vs-mob nearestEntityOfTypeAt).
	targetClassSkeleton
	// targetClassFoxPrey is the Fox landTargetGoal branch (Fox.registerGoals: NearestAttackableTargetGoal
	// <Animal>(this, Animal.class, 10, false, false, target -> target instanceof Chicken || target
	// instanceof Rabbit)): findTarget scans the entity store for entity.Chicken.ID / entity.Rabbit.ID
	// within FOLLOW_RANGE (the same mob-vs-mob nearestEntityOfTypeAt the skeleton branch uses). Cite
	// Fox.registerGoals landTargetGoal.
	targetClassFoxPrey
)

// nearestAttackableTargetGoal ports NearestAttackableTargetGoal<T> (flags {TARGET}). It acquires the
// nearest valid target of its targetClass (PLAYER via nearestPlayerIDAt, SKELETON via
// nearestEntityOfTypeAt) bounded by FOLLOW_RANGE — exactly findTarget's getNearestEntity branch (the
// Player branch is level.getNearestPlayer; the non-Player branch is
// getNearestEntity(getEntitiesOfClass(type, searchArea), conditions, mob, x, eyeY, z)).
type nearestAttackableTargetGoal struct {
	baseGoal
	randomInterval int

	// targetClass selects the findTarget branch (B1/B2). targetClassPlayer (zero) == the Phase-35
	// Player goal; targetClassSkeleton == the wolf skeleton goal. The bare newNearestAttackableTargetGoal
	// leaves it zero, so the hostiles are byte-identical.
	targetClass nearestTargetClass

	// angerGate is an OPTIONAL post-acquire predicate (NeutralMob.isAngryAt gating
	// NearestAttackableTargetGoal<Player>): after findTarget acquires a candidate, if angerGate != nil
	// && it returns false, the target is DROPPED (a wild un-hit wolf must NOT aggro players). nil for
	// every hostile (they always aggro) AND for the skeleton goal (no anger gate) → the Phase-35
	// behavior is UNCHANGED wherever angerGate is nil. Cite NeutralMob.isAngryAt.
	angerGate func(t *TickLoop, e *Entity, targetID int32) bool

	// target is NearestAttackableTargetGoal.target, captured in canUse(findTarget) and committed to
	// mobAI.attackTargetID in start(). It carries the acquired entity id (a player id for PLAYER, a
	// skeleton entity id for SKELETON); 0 == no target found.
	target int32

	// forceTrigger skips the RNG gate once (test seam, mirroring randomStrollGoal.forceTrigger — it
	// bypasses the ROLL, not the source, so a unit test can make findTarget run deterministically).
	forceTrigger bool
}

// newNearestAttackableTargetGoal builds the goal with the FULL randomInterval (10) and the TARGET
// flag (NearestAttackableTargetGoal ctor: setFlags(EnumSet.of(TARGET))). targetClass is the zero value
// (targetClassPlayer) and angerGate is nil — so this is the byte-identical Phase-35 hostile-vs-player
// goal, UNCHANGED by the B1/B2 parameterization (re-run the hostile tests + the pig oracle to prove it).
func newNearestAttackableTargetGoal() *nearestAttackableTargetGoal {
	return &nearestAttackableTargetGoal{
		baseGoal:       newBaseGoal(flagTarget),
		randomInterval: nearestTargetRandomInterval,
	}
}

// newAngryPlayerTargetGoal builds the wolf @4 NearestAttackableTargetGoal<Player>(this, Player, 10,
// true, false, this::isAngryAt) — the PLAYER class GATED on isAngryAt (B1). A wild un-hit wolf (no live
// angerEndTime / no matching angerTarget) acquires no player target; a provoked wolf (angerEndTime live
// && angerTarget == the candidate) does. Same struct/canUse/start/stop as the bare goal — only the
// angerGate differs. Cite Wolf.registerGoals targetSelector @4 + NeutralMob.isAngryAt.
func newAngryPlayerTargetGoal() *nearestAttackableTargetGoal {
	return &nearestAttackableTargetGoal{
		baseGoal:       newBaseGoal(flagTarget),
		randomInterval: nearestTargetRandomInterval,
		targetClass:    targetClassPlayer,
		angerGate:      isAngryAt,
	}
}

// newSkeletonTargetGoal builds the wolf @7 NearestAttackableTargetGoal<AbstractSkeleton>(this,
// AbstractSkeleton, false) — the SKELETON class, NO anger gate (B2). Wolves attack skeletons on sight.
// findTarget scans entity.Skeleton.ID within FOLLOW_RANGE (nearestEntityOfTypeAt). Cite
// Wolf.registerGoals targetSelector @7.
func newSkeletonTargetGoal() *nearestAttackableTargetGoal {
	return &nearestAttackableTargetGoal{
		baseGoal:       newBaseGoal(flagTarget),
		randomInterval: nearestTargetRandomInterval,
		targetClass:    targetClassSkeleton,
	}
}

// newFoxLandTargetGoal builds the Fox landTargetGoal: NearestAttackableTargetGoal<Animal>(this,
// Animal.class, 10, false, false, chicken||rabbit). It scans the entity store for the nearest Chicken or
// Rabbit within FOLLOW_RANGE (targetClassFoxPrey), NO anger gate. The randomInterval stays the shared
// nearestTargetRandomInterval (the full-rate 10 the goal uses everywhere — matching the vanilla reachRange
// arg of 10 that also seeds the DEFAULT_RANDOM_INTERVAL). Cite Fox.registerGoals landTargetGoal.
func newFoxLandTargetGoal() *nearestAttackableTargetGoal {
	return &nearestAttackableTargetGoal{
		baseGoal:       newBaseGoal(flagTarget),
		randomInterval: nearestTargetRandomInterval,
		targetClass:    targetClassFoxPrey,
	}
}

// canUse ports NearestAttackableTargetGoal.canUse (bytecode-verified this session):
//
//	if (randomInterval > 0 && mob.getRandom().nextInt(randomInterval) != 0) return false;  // RNG GATE
//	findTarget();
//	return target != null;
//
// The RNG gate draws EXACTLY ONE nextInt(randomInterval) per canUse, with randomInterval == 10 (the
// faithful full interval — see the header LOCKSTEP note; NOT reducedTickDelay's 5). When the gate
// fails (nextInt(10) != 0) it returns BEFORE the scan (no findTarget call). The scan is a pure world
// read (no RNG).
//
//	[VERIFIED javap NearestAttackableTargetGoal.canUse: getfield randomInterval; ifle; getRandom();
//	 nextInt(randomInterval); ifeq -> findTarget; else iconst_0 ireturn; findTarget; target ifnull?0:1.]
func (g *nearestAttackableTargetGoal) canUse(t *TickLoop, e *Entity) bool {
	if !g.forceTrigger {
		// DRAW 1 (the gate): getRandom().nextInt(randomInterval). randomInterval == 10 (the FULL
		// DEFAULT_RANDOM_INTERVAL, our full-rate-tick compensation for the jar's reducedTickDelay(10)=5).
		if g.randomInterval > 0 && mobRandom(e).nextInt(g.randomInterval) != 0 {
			return false
		}
	}
	g.forceTrigger = false
	g.findTarget(t, e)
	// THE ANGER GATE (B1, Phase 36-01): NeutralMob gates NearestAttackableTargetGoal<Player> on
	// isAngryAt — the wolf @4 passes `this::isAngryAt` as the targeting-conditions selector. After the
	// scan acquires a candidate, drop it unless the wolf is angry AT that candidate. nil for every
	// hostile (the bare goal) AND the skeleton goal → this branch NEVER fires for them, so their
	// Phase-35 behavior is byte-identical (proven by the unchanged hostile tests + the pig oracle). The
	// gate runs AFTER findTarget (the candidate id is what isAngryAt checks against e.angerTarget).
	//	[VERIFIED javap Wolf.registerGoals: NearestAttackableTargetGoal<Player>(..., this::isAngryAt);
	//	 NearestAttackableTargetGoal.findTarget builds targetConditions with the passed selector and
	//	 getNearestEntity filters on it — i.e. a candidate failing isAngryAt is not selected.]
	if g.angerGate != nil && g.target != 0 && !g.angerGate(t, e, g.target) {
		g.target = 0
	}
	return g.target != 0
}

// findTarget ports NearestAttackableTargetGoal.findTarget — the Player branch (the only target type
// v1 acquires): target = level.getNearestPlayer(targetConditions, mob, x, eyeY, z), where
// targetConditions.range == getFollowDistance() == getAttributeValue(FOLLOW_RANGE)
// (TargetGoal.getFollowDistance). The search is bounded by the FOLLOW_RANGE attribute, matching the
// jar's getTargetSearchArea(followDistance) = boundingBox.inflate(followDistance) — nearestPlayerAt
// applies the SAME radius as a sphere bound (the player scan the lookAtPlayerGoal/world.nearest_player
// seam already uses). NO RNG.
//
// CITE-DEFERRED sub-behavior (recorded, NEVER silently dropped):
//   - mustSee line-of-sight: TargetingConditions.forCombat() carries a LoS check; no raycast/LoS
//     subsystem exists in v1 (the passive goals have no LoS check either), so the LoS gate is a cited
//     stub equal to "visible" — every player in FOLLOW_RANGE is acquirable. Upgrade: gate on a real
//     hasLineOfSight raycast when sensing lands (35-JARNOTES OPEN: TargetingConditions).
//   - the TargetingConditions team/invisibility/selector filters: v1 has no teams/invisibility; the
//     range bound is the only active filter, the rest are cited no-ops.
//
//	[VERIFIED javap NearestAttackableTargetGoal.findTarget Player branch: getServerLevel(mob)
//	 .getNearestPlayer(getTargetConditions(), mob, getX(), getEyeY(), getZ()); TargetGoal.getFollowDistance
//	 == getAttributeValue(FOLLOW_RANGE).]
func (g *nearestAttackableTargetGoal) findTarget(t *TickLoop, e *Entity) {
	follow := e.getAttributeValue(attribute.FollowRange)
	switch g.targetClass {
	case targetClassSkeleton:
		// The non-Player branch (B2): getNearestEntity(getEntitiesOfClass(AbstractSkeleton, searchArea),
		// conditions, mob, x, eyeY, z) — the nearest entity.Skeleton.ID within FOLLOW_RANGE. The mob-class
		// analog of the Player branch, scanning the entity store instead of t.players. Cite
		// NearestAttackableTargetGoal.findTarget's non-Player getNearestEntity branch.
		if id, ok := nearestEntityOfTypeAt(t, e, entity.Skeleton.ID, follow); ok {
			g.target = id
			return
		}
	case targetClassFoxPrey:
		// The Fox landTarget branch: the nearest Chicken OR Rabbit within FOLLOW_RANGE. Scans both prey
		// types and keeps the closer (the getNearestEntity(getEntitiesOfClass(Animal, area, chicken||rabbit))
		// predicate). Cite Fox.registerGoals landTargetGoal (Chicken || Rabbit).
		bestID, bestOK := int32(0), false
		best := follow * follow
		for _, other := range t.cur().entities.near(e.x, e.z, int(math.Ceil(follow/16.0))) {
			if other == e || other.dead {
				continue
			}
			if other.typ != entity.Chicken.ID && other.typ != entity.Rabbit.ID {
				continue
			}
			d := entityDistSqr(e, other)
			if d <= best {
				best = d
				bestID, bestOK = other.id, true
			}
		}
		if bestOK {
			g.target = bestID
			return
		}
	default: // targetClassPlayer (the Phase-35 branch, UNCHANGED)
		// getNearestPlayer is anchored at (mob.getX(), mob.getEyeY(), mob.getZ()); v1 has no eye-height
		// field (refreshDimensions notes the cited eye-height gap), so the scan anchors at the mob feet y
		// — the same anchor lookAtPlayerGoal/nearestPlayerWithin use. The FOLLOW_RANGE bound is faithful.
		if id, ok := nearestPlayerIDAt(t, e.x, e.y, e.z, follow); ok {
			g.target = id
			return
		}
	}
	g.target = 0
}

// canContinueToUse ports the TargetGoal.canContinueToUse base: a running target goal keeps running
// while the acquired target is still a valid, in-range, alive player. The jar walks
// canContinueToUse -> the target is alive, within getFollowDistance(), and passes targetConditions;
// v1 checks the player still exists (resolvable) + within FOLLOW_RANGE (the live distance bound).
// NO RNG.
//
//	[VERIFIED javap TargetGoal.canContinueToUse: target = mob.getTarget(); if null return false; if
//	 !target.isAlive() return false; distSqr > followDistance² -> false; else canAttack.]
func (g *nearestAttackableTargetGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	if e.ai == nil {
		return false
	}
	id := e.ai.getTarget()
	if id == 0 {
		return false
	}
	follow := e.getAttributeValue(attribute.FollowRange)
	if g.targetClass == targetClassSkeleton || g.targetClass == targetClassFoxPrey {
		// SKELETON class: resolve the target through the OWNING-region entity store (a skeleton is an
		// *Entity, not a player) + the live FOLLOW_RANGE distance bound. t.cur() is the region whose
		// fan-out is running this goal — the SAME store nearestEntityOfTypeAt scanned (the v5 same-region
		// cut, as the breed/follow scans use). Keeps the "target alive + within followDistance²"
		// TargetGoal.canContinueToUse shape.
		other, ok := t.cur().entities.get(id)
		if !ok || other.dead {
			return false // the skeleton died / was removed (TargetGoal: target not alive/absent)
		}
		return entityDistSqr(e, other) <= follow*follow
	}
	// PLAYER class (the Phase-35 branch, byte-identical): resolve via playerByEntityID + the same
	// FOLLOW_RANGE distance bound.
	p := t.playerByEntityID(id)
	if p == nil {
		return false // the target left / disconnected (TargetGoal: target not alive/absent)
	}
	dx, dy, dz := p.x-e.x, p.y-e.y, p.z-e.z
	return dx*dx+dy*dy+dz*dz <= follow*follow
}

// start ports NearestAttackableTargetGoal.start: mob.setTarget(this.target); super.start(). It
// commits the acquired target id to mobAI.attackTargetID (the MeleeAttackGoal canUse-gate reads it).
// NO RNG beyond the canUse gate.
//
//	[VERIFIED javap NearestAttackableTargetGoal.start: mob.setTarget(target); super.start().]
func (g *nearestAttackableTargetGoal) start(_ *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.setTarget(g.target)
	}
}

// stop ports TargetGoal.stop: clear the target (mob.setTarget(null)). The jar's TargetGoal.stop sets
// mob.setTarget(null) and clears targetMob. v1 clears attackTargetID so a dropped target stops the
// downstream MeleeAttackGoal.
//
//	[VERIFIED javap TargetGoal.stop: mob.setTarget((LivingEntity) null); targetMob = null.]
func (g *nearestAttackableTargetGoal) stop(_ *TickLoop, e *Entity) {
	g.target = 0
	if e.ai != nil {
		e.ai.setTarget(0)
	}
}

// --- hurtByTargetGoal ------------------------------------------------------------------------

// hurtByTargetGoal ports HurtByTargetGoal (flags {TARGET}), NO RNG — it retaliates against whoever
// last hit the mob, read from the entity's lastHurtByMob/lastHurtByMobTimestamp bookkeeping
// (entity.go, set at the combat_mob.go flag2 store-point).
type hurtByTargetGoal struct {
	baseGoal

	// timestamp is HurtByTargetGoal.timestamp — the lastHurtByMobTimestamp value captured on the LAST
	// start(), so canUse fires only ONCE per fresh hit (timestamp == this.timestamp -> already
	// retaliated -> false).
	timestamp int32
}

// newHurtByTargetGoal builds the goal with the TARGET flag (HurtByTargetGoal ctor:
// setFlags(EnumSet.of(TARGET))).
func newHurtByTargetGoal() *hurtByTargetGoal {
	return &hurtByTargetGoal{baseGoal: newBaseGoal(flagTarget)}
}

// canUse ports HurtByTargetGoal.canUse (bytecode-verified this session), NO RNG:
//
//	int timestamp = mob.getLastHurtByMobTimestamp();
//	LivingEntity last = mob.getLastHurtByMob();
//	if (timestamp == this.timestamp || last == null) return false;
//	if (last.is(PLAYER) && UNIVERSAL_ANGER gamerule) return false;
//	for (Class c : toIgnoreDamage) if (c.isAssignableFrom(last.getClass())) return false;
//	return canAttack(last, HURT_BY_TARGETING);
//
// CITE-DEFERRED sub-behavior (recorded, NEVER silently dropped):
//   - UNIVERSAL_ANGER gamerule: no gamerule subsystem in v1 — the gamerule defaults FALSE in vanilla,
//     so this guard is a cited no-op (never skips retaliation). Upgrade: read the gamerule when one
//     lands (35-JARNOTES.md:89).
//   - toIgnoreDamage class filter: the base HurtByTargetGoal has an EMPTY toIgnoreDamage array (only
//     subclasses set one, e.g. ZombifiedPiglin); the shared goal carries no ignore set, so this loop
//     is a cited empty no-op for the v1 hostiles.
//
//	[VERIFIED javap HurtByTargetGoal.canUse: getLastHurtByMobTimestamp; getLastHurtByMob; (ts ==
//	 this.timestamp || last == null) -> false; UNIVERSAL_ANGER guard; toIgnoreDamage loop; canAttack.]
func (g *hurtByTargetGoal) canUse(t *TickLoop, e *Entity) bool {
	timestamp := e.lastHurtByMobTimestamp
	last := e.lastHurtByMob
	if timestamp == g.timestamp || last == 0 {
		return false
	}
	// (UNIVERSAL_ANGER gamerule + toIgnoreDamage filter — cited no-ops above.)
	// canAttack(last, HURT_BY_TARGETING): the attacker must be a valid combat target. v1's attacker is a
	// player id; validity == the player still exists (resolvable on the loop). The full TargetingConditions
	// (range/LoS) of HURT_BY_TARGETING is a cited no-op in v1 (no LoS subsystem) — the existence check is
	// the active validity gate, mirroring the passive goals' player-exists discipline.
	return g.canAttack(t, last)
}

// canAttack ports the canAttack(last, HURT_BY_TARGETING) validity check for v1: the attacker (a
// player id) must still be present on the loop (the player has not disconnected). HURT_BY_TARGETING ==
// TargetingConditions.forCombat().ignoreLineOfSight().ignoreInvisibilityTesting() — so it carries NO
// LoS/invisibility filter (cited), leaving "the target exists and is a live combat candidate" as the
// active gate. NO RNG.
func (g *hurtByTargetGoal) canAttack(t *TickLoop, id int32) bool {
	return t.playerByEntityID(id) != nil
}

// start ports HurtByTargetGoal.start (bytecode-verified):
//
//	mob.setTarget(mob.getLastHurtByMob());
//	this.targetMob = mob.getTarget();
//	this.timestamp = mob.getLastHurtByMobTimestamp();
//	this.unseenMemoryTicks = 300;
//	if (alertSameType) alertOthers();
//	super.start();
//
// It commits the attacker as the target and stamps its OWN timestamp so canUse won't re-fire on the
// same hit. unseenMemoryTicks=300 + alertOthers (setAlertOthers, a Zombie-only alert burst) are
// CITE-DEFERRED: the v1 shared goal carries no alertSameType, and the unseenMemoryTicks
// lose-sight-grace is a cited no-op (no LoS subsystem). NO RNG.
//
//	[VERIFIED javap HurtByTargetGoal.start: setTarget(getLastHurtByMob()); targetMob = getTarget();
//	 timestamp = getLastHurtByMobTimestamp(); unseenMemoryTicks = 300; alertSameType? alertOthers();
//	 super.start().]
func (g *hurtByTargetGoal) start(_ *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.setTarget(e.lastHurtByMob)
	}
	g.timestamp = e.lastHurtByMobTimestamp
	// (unseenMemoryTicks = 300 + alertOthers — cited deferrals above.)
}

// canContinueToUse ports the TargetGoal.canContinueToUse base (shared with the nearest-target goal):
// keep retaliating while the target is a present, in-range player. v1 reuses the same existence +
// FOLLOW_RANGE distance gate. NO RNG.
//
//	[VERIFIED javap TargetGoal.canContinueToUse: target alive + within followDistance² + canAttack.]
func (g *hurtByTargetGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	if e.ai == nil {
		return false
	}
	id := e.ai.getTarget()
	if id == 0 {
		return false
	}
	return t.playerByEntityID(id) != nil
}

// stop ports TargetGoal.stop: mob.setTarget(null). Clears the attack target so the downstream attack
// goal stops. NO RNG.
//
//	[VERIFIED javap TargetGoal.stop: mob.setTarget((LivingEntity) null).]
func (g *hurtByTargetGoal) stop(_ *TickLoop, e *Entity) {
	if e.ai != nil {
		e.ai.setTarget(0)
	}
}

// nearestPlayerIDAt is the entity-id-returning sibling of nearestPlayerAt (ai_goals_passive.go): the
// id of the nearest player within maxDist of (cx,cy,cz), or ok=false if none. NearestAttackableTargetGoal
// needs the player's ENTITY id (to set as the attack target), where lookAtPlayerGoal only needs its
// POSITION — so this returns p.entityID instead of the position. It scans the SAME tick-owned t.players
// set (the player seam; players are not entityStore entries) with the SAME distance discipline.
// Tick-owned read (TICK-05). NO RNG.
func nearestPlayerIDAt(t *TickLoop, cx, cy, cz, maxDist float64) (id int32, ok bool) {
	best := maxDist * maxDist
	for _, p := range t.players {
		if p == nil {
			continue
		}
		dx, dy, dz := p.x-cx, p.y-cy, p.z-cz
		d2 := dx*dx + dy*dy + dz*dz
		if d2 <= best {
			best = d2
			id, ok = p.entityID, true
		}
	}
	return id, ok
}

// nearestEntityOfTypeAt is the mob-class sibling of nearestPlayerIDAt (B2, Phase 36-01): the entity id
// of the nearest *Entity of type `typ` within maxDist of mob `e`, or ok=false if none. It is the Go
// analog of NearestAttackableTargetGoal.findTarget's non-Player branch
// getNearestEntity(getEntitiesOfClass(type, getTargetSearchArea(followDistance)), conditions, mob, x,
// eyeY, z): build the search box (FOLLOW_RANGE-inflated AABB → the chunk-column broad-phase), collect
// the entities of the wanted type, and keep the nearest by squared distance within FOLLOW_RANGE². It
// reuses the SAME t.cur().entities.near broad-phase + entityDistSqr nearest-wins loop the breed scan
// (findFreePartner) uses, converting the block range to a chunk-column range via ceil(maxDist/16) (near
// takes a CHUNK range) and re-checking the precise maxDist² inside the loop. Tick-owned read (TICK-05).
// NO RNG.
//
//	[VERIFIED javap NearestAttackableTargetGoal.findTarget (non-Player): target = getNearestEntity(
//	 level.getEntitiesOfClass(targetType, getTargetSearchArea(getFollowDistance())), getTargetConditions(),
//	 mob, getX(), getEyeY(), getZ()); getTargetSearchArea(d) = getBoundingBox().inflate(d, 4.0, d).]
func nearestEntityOfTypeAt(t *TickLoop, e *Entity, typ entity.ID, maxDist float64) (id int32, ok bool) {
	// near takes a CHUNK-column range; convert the block FOLLOW_RANGE (the inflate(d) horizontal radius)
	// to columns via ceil(maxDist/16), then re-check the precise block distance inside the loop.
	rangeChunks := int(math.Ceil(maxDist / 16.0))
	best := maxDist * maxDist // start at FOLLOW_RANGE²; only a closer same-type candidate wins
	for _, other := range t.cur().entities.near(e.x, e.z, rangeChunks) {
		if other == e || other.typ != typ || other.dead {
			continue // not a live candidate of the wanted type (getEntitiesOfClass(type) + isAlive)
		}
		d := entityDistSqr(e, other)
		if d <= best {
			best = d
			id, ok = other.id, true
		}
	}
	return id, ok
}

// isAngryAt ports net.minecraft.world.entity.NeutralMob.isAngryAt(LivingEntity, ServerLevel) for the
// wolf's @4 NearestAttackableTargetGoal<Player> gate (B1, Phase 36-01). The wolf passes `this::isAngryAt`
// as the goal's targeting selector, so a candidate is acquirable ONLY when the wolf is angry at it:
//
//	if (!canAttack(target)) return false;
//	if (isValidPlayerTarget(target) && isAngryAtAllPlayers(level)) return true;   // the gamerule path
//	EntityReference angerTarget = getPersistentAngerTarget();
//	return angerTarget != null && angerTarget.matches(target);
//
// v1 reduction (each a CITED no-op, not a silent drop):
//   - canAttack(target): the candidate must be a present, valid combat target. v1's candidate is a
//     player id; validity == the player still resolves on the loop (the same existence gate the
//     hurtByTargetGoal/nearestAttackableTargetGoal player branch uses).
//   - isAngryAtAllPlayers(level): reads the UNIVERSAL_ANGER gamerule, which DEFAULTS FALSE in vanilla
//     (javap NeutralMob.isAngryAtAllPlayers: GameRules.UNIVERSAL_ANGER) — no gamerule subsystem in v1,
//     so this all-players path is a cited constant-false (never fires). The live path is the
//     persistentAngerTarget match below.
//   - the persistentAngerTarget match: the wolf is angry at `target` iff its anger is LIVE (the
//     gametime-endpoint isAngry: angerEndTime > 0 && (angerEndTime - gameTime) > 0) AND its angerTarget
//     == the candidate id. Both are set at the combat store-point on a player hit (combat_mob.go).
//
//	[VERIFIED javap NeutralMob.isAngryAt: canAttack(target) ifeq -> false; isValidPlayerTarget &&
//	 isAngryAtAllPlayers -> true; getPersistentAngerTarget(); != null && matches(target). isAngry():
//	 endTime = getPersistentAngerEndTime(); endTime > 0 && (endTime - level.getGameTime()) > 0.]
func isAngryAt(t *TickLoop, e *Entity, targetID int32) bool {
	// canAttack(target): the candidate (a player id) must still be present on the loop.
	if t.playerByEntityID(targetID) == nil {
		return false
	}
	// (isAngryAtAllPlayers: the UNIVERSAL_ANGER gamerule defaults FALSE — a cited constant-false no-op.)
	// The persistentAngerTarget match: anger LIVE (gametime-endpoint isAngry) AND angerTarget == candidate.
	if e.angerEndTime <= 0 || (e.angerEndTime-t.gametime) <= 0 {
		return false // isAngry() == false: the anger expired (gameTime passed angerEndTime)
	}
	return e.angerTarget == targetID // getPersistentAngerTarget().matches(target)
}
