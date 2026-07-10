package server

// warden.go -- the WARDEN (net.minecraft.world.entity.monster.warden.Warden, entity id 143), a 1:1
// port from the unobfuscated 26.2 jar. The Warden is the blind, sculk-summoned boss-tier hostile: it
// tracks by ACCUMULATED ANGER (per-suspect), EMERGES on a sculk-shrieker summon, MELEE-SLAMS for 30
// (ATTACK_DAMAGE 30 + ATTACK_KNOCKBACK 1.5), fires a ranged SONIC BOOM (10 dmg + knock-up, ignores
// armor/shields), pulses DARKNESS (now wired), and DIGS AWAY + despawns after no anger. Additive +
// per-type-gated behind e.warden (nil for every other entity; the pig oracle stays byte-identical).
//
// VANILLA (verified javap this task):
//   createAttributes: MAX_HEALTH 500, MOVEMENT_SPEED 0.30000001192092896, KNOCKBACK_RESISTANCE 1.0,
//     ATTACK_KNOCKBACK 1.5, ATTACK_DAMAGE 30, FOLLOW_RANGE 24.
//   AngerManagement: per-suspect angerBySuspect; getActiveAnger(target) = the target anger (or highest
//     if null); increaseAnger(entity,i) adds i (min-clamped MAX_ANGER 150); tick (every 20 warden ticks)
//     DECAYS every suspect anger by 1 and drops suspects at anger <= 1.
//   AngerLevel.byAnger: CALM minimumAnger 0, AGITATED 40, ANGRY 80.
//   Warden.increaseAngerAt(entity) = increaseAngerAt(entity, DEFAULT_ANGER=35, true).
//   doHurtTarget: broadcastEntityEvent(4) + WARDEN_ATTACK_IMPACT + SonicBoom.setCooldown(this, 40) then
//     Monster.doHurtTarget (ATTACK_DAMAGE 30 + ATTACK_KNOCKBACK 1.5).
//   SonicBoom: fires when closerThan(target, 15.0, 20.0); windup TICKS_BEFORE_PLAYING_SOUND=34,
//     DURATION=60; on the sound tick hurtServer(sonicBoom, 10.0) and on a landed hit push(dir.x*d9,
//     dir.y*d7, dir.z*d9) d7=0.5*(1-kbResist) d9=2.5*(1-kbResist); stop() setCooldown(this, 40).
//   customServerAiStep: applyDarknessAround(pos, this, 20) every (tickCount+getId())%120==0;
//     angerManagement.tick + syncClientAngerLevel every tickCount%20==0.
//   finalizeSpawn (TRIGGERED): DIG_COOLDOWN 1200, EMERGING + IS_EMERGING for EMERGE_DURATION (134).
//   WardenAi Dig: no anger + on ground -> dig away over DIGGING_DURATION=100 then discard.
//
// LANDED: the 6 attributes, AngerManagement (per-suspect anger, DEFAULT_ANGER 35, -1-per-20t decay,
// byAnger 0/40/80), target = highest-anger suspect, melee doHurtTarget (30 + 1.5 kb + 40-tick sonic
// lock), SonicBoom (15/20 gate, 34-tick windup, 60 duration, 10 dmg ignoring armor/shields, 0.5/2.5
// knock-up, 40 cooldown), EMERGE-on-spawn lock, DIG-away despawn. RNG on the warden OWN stream only.
//
// WIRED: DARKNESS pulse (MobEffectUtil.addEffectToPlayersAround, now that server/mob_effect.go exists) keeps
// the %120 gate + radius 20. The live-warden VibrationSystem/Sniffing anger feed is
// DEFERRED -- v1 feeds anger from the DIRECT path (a nearby player within FOLLOW_RANGE + a hit). The full
// Brain graph is reduced to the code-driven wardenAiStep (the wither/phantom bounded reduction). Emerge/dig
// ANIMATION states + WARDEN_* sounds are client-cosmetic.

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// Warden constants (VERIFIED javap this task -- every number read off the bytecode ldc/bipush/enum init).
const (
	wardenDefaultAnger        = 35  // Warden.increaseAngerAt(entity): increaseAngerAt(entity, 35, true) (bipush 35)
	wardenAgitatedAnger       = 40  // AngerLevel.AGITATED minimumAnger (enum init bipush 40)
	wardenAngryAnger          = 80  // AngerLevel.ANGRY minimumAnger (enum init bipush 80)
	wardenAngerTickDelay      = 20  // ANGERMANAGEMENT_TICK_DELAY: angerManagement.tick every tickCount%20==0 (bipush 20)
	wardenAngerDecayFloor     = 1   // AngerManagement.tick: drop a suspect at anger <= 1, else anger-1 (iconst_1)
	wardenMaxAnger            = 150 // AngerManagement.MAX_ANGER (increaseAnger min-clamp ceiling; cited)
	wardenProximityAngerBoost = 35  // v1 proximity feed: a nearby player raises anger by DEFAULT_ANGER (cited)

	wardenMeleeToSonicLock = 40  // TIME_TO_USE_MELEE_UNTIL_SONIC_BOOM: SonicBoom.setCooldown(this, 40) on a melee hit
	wardenSonicOnAcquire   = 200 // setAttackTarget -> SonicBoom.setCooldown(this, 200) on target ACQUISITION (sipush 200)
	wardenMeleeCooldown    = 18  // WardenAi.initFightActivity: MeleeAttack.create(18) -> MELEE_ATTACK_COOLDOWN 18 (bipush 18)

	wardenSonicDistanceXZ    = 15.0 // DISTANCE_XZ: closerThan(target, 15.0, 20.0) horizontal gate (ldc2_w 15.0d)
	wardenSonicDistanceY     = 20.0 // DISTANCE_Y: closerThan(target, 15.0, 20.0) vertical gate (ldc2_w 20.0d)
	wardenSonicChargeTicks   = 34   // TICKS_BEFORE_PLAYING_SOUND = Mth.ceil(34.0) (static init ldc2_w 34.0d)
	wardenSonicDuration      = 60   // DURATION = Mth.ceil(60.0f) (static init ldc_w 60.0f)
	wardenSonicReachBonus    = 7    // particle beam steps = Mth.floor(vec.length()) + 7 (bipush 7)
	wardenSonicDamage        = 10.0 // hurtServer(sonicBoom, 10.0f) (ldc_w 10.0f)
	wardenSonicKnockVertical = 0.5  // d7 = 0.5 * (1 - KNOCKBACK_RESISTANCE) (ldc2_w 0.5d)
	wardenSonicKnockHoriz    = 2.5  // d9 = 2.5 * (1 - KNOCKBACK_RESISTANCE) (ldc2_w 2.5d)
	wardenSonicCooldown      = 40   // SonicBoom.stop -> setCooldown(this, 40) (bipush 40)

	wardenDarknessInterval = 120 // applyDarknessAround when (tickCount + getId()) % 120 == 0 (bipush 120)
	wardenDarknessRadius   = 20  // applyDarknessAround(level, pos, this, 20) (bipush 20)
	// applyDarknessAround builds new MobEffectInstance(DARKNESS, 260, 0, false, false, false) and hands it to
	// MobEffectUtil.addEffectToPlayersAround(level, this, this.position(), 20, inst, 200). VERIFIED javap
	// Warden.applyDarknessAround + MobEffectUtil.addEffectToPlayersAround.
	wardenDarknessDuration    = 260 // new MobEffectInstance(DARKNESS, 260, ...) (sipush 260)
	wardenDarknessAmplifier   = 0   // amplifier 0 (iconst_0)
	wardenDarknessReapplyProb = 200 // addEffectToPlayersAround(..., 200): the endsWithin gate arg is prob-1 (sipush 200)
	wardenEmergeDuration      = 134 // WardenAi.EMERGE_DURATION = Mth.ceil(133.59999f) (static init)
	wardenDiggingDuration     = 100 // WardenAi.DIGGING_DURATION = Mth.ceil(100.0f) (static init)
	wardenRoarDuration        = 84  // WardenAi.ROAR_DURATION = Mth.ceil(84.0f) (static init ldc_w 84.0f)
	wardenRoarAngerIncrease   = 20  // Roar.ROAR_ANGER_INCREASE: Roar.start -> increaseAngerAt(roarTarget, 20, false) (bipush 20)
	wardenHurtAngerBoost      = 100 // Warden.hurtServer: increaseAngerAt(attacker, ANGRY.minimumAnger 80 + 20, false) == 100
	wardenHurtDirectRange     = 5.0 // Warden.hurtServer: (isDirect() || closerThan(attacker, 5.0)) -> setAttackTarget (ldc2_w 5.0d)

	wardenDigCooldownTicks    = 1200 // finalizeSpawn: DIG_COOLDOWN memory 1200 ticks (ldc2_w 1200l)
	wardenNoAngerDespawnTicks = 1200 // the DIG_COOLDOWN-gated idle window before the dig-away (== 60s)
)

// wardenState holds all Warden-specific tick state behind the single e.warden pointer. angerBySuspect is
// AngerManagement.angerBySuspect (player id -> anger). (anger ticks on the tickCount%20 phase boundary).
// emergeTicks is the IS_EMERGING lock (134 -> 0). sonicChargeTicks is the SonicBoom windup (0 idle; >0
// charging). sonicCooldown is SONIC_BOOM_COOLDOWN (40 after a boom or melee hit). noAngerTicks counts idle
// ticks toward the dig-away despawn. digTicks is the DIGGING_DURATION countdown (100 -> 0 discard).
type wardenState struct {
	angerBySuspect   map[int32]int
	emergeTicks      int
	sonicChargeTicks int
	sonicCooldown    int
	meleeCooldown    int
	noAngerTicks     int
	lastTargetID     int32
	digTicks         int
	digging          bool
	// roarTargetID is MemoryModuleType.ROAR_TARGET (0 = absent). roarTicks is the Roar behavior countdown
	// (WardenAi.ROAR_DURATION=84 -> 0; 0 = not roaring). The Warden state machine mirrors the Brain activity
	// priority EMERGE > DIG > ROAR > FIGHT > IDLE: SetRoarTarget arms ROAR_TARGET only when getEntityAngryAt()
	// (anger >= ANGRY 80) yields a suspect; Roar runs 84 ticks; Roar.stop -> setAttackTarget(roarTarget) hands
	// the FIGHT activity its ATTACK_TARGET. Cite WardenAi.updateActivity + SetRoarTarget + Roar.
	roarTargetID int32
	roarTicks    int
	// vibration is the VibrationSystem.Data (the in-flight game-event vibration); vibrationCooldown
	// is the VIBRATION_COOLDOWN memory (40t after a received vibration); recentProjectileTicks is the
	// RECENT_PROJECTILE memory (100t after a projectile vibration). Cite Warden.VibrationUser.
	vibration             *vibrationData
	vibrationCooldown     int
	recentProjectileTicks int
}

// spawnWarden creates a Warden at (x,y,z) (NewEntity seeds the 500-HP wardenSupplier by the type name
// "warden"; initSpawnHealth reads MAX_HEALTH 500). The emerging flag mirrors finalizeSpawn on a TRIGGERED
// summon (EMERGING lock for EMERGE_DURATION=134, no anger). e.ai enters the serverAiStep snapshot loop.
// persistenceRequired is NOT set (a warden despawns via the dig-away idle). Cite Warden ctor + createAttributes
// + finalizeSpawn.
func (t *TickLoop) spawnWarden(x, y, z float64, emerging bool) *Entity {
	w := NewEntity(t.idAlloc.AllocID(), entity.Warden, x, y, z)
	ws := &wardenState{angerBySuspect: make(map[int32]int)}
	if emerging {
		ws.emergeTicks = wardenEmergeDuration // finalizeSpawn TRIGGERED: IS_EMERGING for EMERGE_DURATION
	}
	w.warden = ws
	initSpawnHealth(w) // LivingEntity ctor setHealth(getMaxHealth()) -> 500.0
	w.ai = &mobAI{}
	reseedMobAI(w.ai, w.id)
	owner := t.regionForEntity(w)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(w)
	// Register the Warden VibrationSystem.Listener on the game-event bus so nearby STEP/BLOCK_*/
	// PROJECTILE_LAND events reach its VibrationUser (radius 16) and drive anger. Cite
	// Warden.VibrationUser (getPositionSource) + GameEventListenerRegistry.register.
	t.registerVibrationListener(w)
	return w
}

// wardenActiveAnger ports Warden.getActiveAnger() -> angerManagement.getActiveAnger(getTarget()): the
// anger of the CURRENT target suspect, or (target null) the highest anger of any suspect. Cite
// Warden.getActiveAnger + AngerManagement.getActiveAnger.
func (t *TickLoop) wardenActiveAnger(e *Entity) int {
	ws := e.warden
	if ws == nil {
		return 0
	}
	if e.ai != nil && e.ai.attackTargetID != 0 {
		return ws.angerBySuspect[e.ai.attackTargetID] // angerBySuspect.getInt(target)
	}
	best := 0
	for _, a := range ws.angerBySuspect {
		if a > best {
			best = a
		}
	}
	return best
}

// wardenAngerLevel ports AngerLevel.byAnger(anger): the highest AngerLevel whose minimumAnger <= anger
// (SORTED_LEVELS is [ANGRY 80, AGITATED 40, CALM 0]). Returns 0=CALM, 1=AGITATED, 2=ANGRY. Cite AngerLevel.byAnger.
func wardenAngerLevel(anger int) int {
	if anger >= wardenAngryAnger {
		return 2 // ANGRY
	}
	if anger >= wardenAgitatedAnger {
		return 1 // AGITATED
	}
	return 0 // CALM
}

// wardenIncreaseAngerAt ports Warden.increaseAngerAt(entity) = increaseAngerAt(entity, 35, true): add 35
// anger to the suspect (AngerManagement.increaseAnger, min-clamped to MAX_ANGER=150). The ANGRY-reselect
// (eraseMemory ATTACK_TARGET) is implicit here (the target is re-derived each tick from the top suspect).
// Cite Warden.increaseAngerAt + AngerManagement.increaseAnger.
func (t *TickLoop) wardenIncreaseAngerAt(e *Entity, suspectID int32, amount int) {
	ws := e.warden
	if ws == nil || suspectID == 0 {
		return
	}
	if ws.angerBySuspect == nil {
		ws.angerBySuspect = make(map[int32]int)
	}
	na := ws.angerBySuspect[suspectID] + amount // computeInt: min(MAX_ANGER, v + i)
	if na > wardenMaxAnger {
		na = wardenMaxAnger
	}
	ws.angerBySuspect[suspectID] = na
}

// wardenCanTargetEntity ports Warden.canTargetEntity(entity) for a PLAYER attacker: a LivingEntity in the
// same level, NO_CREATIVE_OR_SPECTATOR, not allied, not an armor stand / warden, not invulnerable, not
// dead-or-dying, within the world border. For a v1 player suspect the live discriminators are: alive AND not
// creative/spectator (a player is never allied to a warden, is not an armor stand/warden, and the world
// border / invulnerable gates are not modeled). Cite Warden.canTargetEntity (EntitySelector
// .NO_CREATIVE_OR_SPECTATOR + isDeadOrDying + isAlliedTo guards).
func (t *TickLoop) wardenCanTargetPlayer(p *tickPlayer) bool {
	if p == nil || p.dead {
		return false // isDeadOrDying() -> false
	}
	// EntitySelector.NO_CREATIVE_OR_SPECTATOR: a creative or spectator player is not targetable.
	if p.gameMode == gameModeCreative || p.gameMode == gameModeSpectator {
		return false
	}
	return true
}

// wardenHurtServer ports Warden.hurtServer's post-super tail: AFTER the shared hurt pipeline (Monster
// .hurtServer) lands, if !isNoAi() && !isDiggingOrEmerging(), the warden increaseAngerAt(getEntity(),
// ANGRY.minimumAnger 80 + 20 = 100, false) at its attacker, then -- if it has NO ATTACK_TARGET yet AND the
// attacker is a LivingEntity AND (source.isDirect() || closerThan(attacker, 5.0)) -- setAttackTarget(attacker)
// IMMEDIATELY (bypassing the roar). This is the get-hurt aggro boost: hitting a warden raises its anger at you
// by 100 (straight to ANGRY) and, for a direct/close hit with no current target, makes you its attack target.
// Additive + warden-gated (nil for every other entity). Cite Warden.hurtServer (bytecode 9-101) +
// increaseAngerAt(Entity,int,boolean) + setAttackTarget.
func (t *TickLoop) wardenHurtServer(e *Entity, src damageSource) {
	ws := e.warden
	if ws == nil {
		return
	}
	// isNoAi() (v1 mobs are never noAi) || isDiggingOrEmerging(): a digging/emerging warden ignores the hit.
	if ws.emergeTicks > 0 || ws.digging {
		return
	}
	// getEntity(): the CAUSING entity (src.attacker). An environmental hit (attacker 0) angers no one.
	attackerID := src.attacker
	if attackerID == 0 {
		return
	}
	attacker := t.playerByEntityID(attackerID)
	// increaseAngerAt(getEntity(), 100, false): the (Entity,int,boolean) form guards on canTargetEntity(entity)
	// -- a non-targetable attacker (creative/spectator/dead) neither angers nor becomes a target. The
	// playListeningSound arg is false (no sound). Cite Warden.increaseAngerAt(Entity,int,boolean) (canTargetEntity gate).
	if !t.wardenCanTargetPlayer(attacker) {
		return // canTargetEntity(entity) == false -> increaseAngerAt returns before AngerManagement.increaseAnger
	}
	t.wardenIncreaseAngerAt(e, attackerID, wardenHurtAngerBoost) // AngerManagement.increaseAnger(entity, 100)
	// `if (getBrain().getMemory(ATTACK_TARGET).isEmpty() && getEntity() instanceof LivingEntity)`: only when
	// the warden has NO current attack target. The attacker is a Player (a LivingEntity), so the instanceof
	// holds. `if (source.isDirect() || closerThan(attacker, 5.0)) setAttackTarget(attacker);` -- a direct
	// (melee) hit OR a close (<5) indirect hit acquires the attacker immediately, skipping the roar. Cite
	// Warden.hurtServer (bytecode 45-98).
	if e.ai != nil && e.ai.attackTargetID == 0 {
		// closerThan(attacker, 5.0) is the single-arg Entity.closerThan -> Vec3.closerThan(pos, 5.0): a 3D
		// squared-distance gate distSq < 5.0*5.0 (strict <). Cite Entity.closerThan(Entity,double) + Vec3.closerThan.
		if src.isDirect() || distanceToSqrPlayer(attacker, e) < wardenHurtDirectRange*wardenHurtDirectRange {
			t.wardenSetAttackTarget(e, attackerID) // setAttackTarget(attacker) -> ATTACK_TARGET + 200 sonic lock
		}
	}
}

// wardenTickAnger ports AngerManagement.tick (every ANGERMANAGEMENT_TICK_DELAY=20 warden ticks): DECAY
// every suspect anger by 1, DROP a suspect at anger <= 1. Cite AngerManagement.tick.
func (t *TickLoop) wardenTickAnger(e *Entity) {
	ws := e.warden
	if ws == nil {
		return
	}
	for id, a := range ws.angerBySuspect {
		if a <= wardenAngerDecayFloor {
			delete(ws.angerBySuspect, id) // remove: anger <= 1
			continue
		}
		ws.angerBySuspect[id] = a - 1 // setValue(anger - 1)
	}
}

// wardenGetEntityAngryAt ports Warden.getEntityAngryAt(): iff getAngerLevel().isAngry() (anger >= ANGRY 80)
// return the AngerManagement active entity (the highest-anger LIVE, canTargetEntity suspect); otherwise
// Optional.empty(). This is the ROAR GATE -- the warden acquires NO target until it reaches ANGRY. The warden
// is BLIND so there is no line-of-sight test; a dead/gone suspect is dropped. Cite Warden.getEntityAngryAt +
// AngerLevel.isAngry + AngerManagement.getActiveEntity.
func (t *TickLoop) wardenGetEntityAngryAt(e *Entity) *tickPlayer {
	ws := e.warden
	if ws == nil {
		return nil
	}
	var best *tickPlayer
	bestAnger := 0
	for id, a := range ws.angerBySuspect {
		p := t.playerByEntityID(id)
		if p == nil || p.dead {
			delete(ws.angerBySuspect, id) // getRemovalReason() != null -> dropped from AngerManagement
			continue
		}
		if a > bestAnger {
			bestAnger = a
			best = p
		}
	}
	// getEntityAngryAt(): `if (!getAngerLevel().isAngry()) return Optional.empty();` -- the active anger must be
	// >= ANGRY (80). getActiveAnger uses the CURRENT top suspect's anger (there is no ATTACK_TARGET during the
	// IDLE selection). Cite Warden.getEntityAngryAt (AngerLevel.byAnger(getActiveAnger).isAngry()).
	if best == nil || wardenAngerLevel(bestAnger) < 2 {
		return nil
	}
	return best
}

// wardenSetAttackTarget ports Warden.setAttackTarget(target): erase ROAR_TARGET, set ATTACK_TARGET, erase
// CANT_REACH_WALK_TARGET_SINCE, then SonicBoom.setCooldown(this, 200). This is the SINGLE point that arms the
// 200-tick sonic lock (TIME_TO_USE_MELEE_UNTIL_SONIC_BOOM) on target acquisition -- called from Roar.stop and
// from Warden.hurtServer's close-hit path. Cite Warden.setAttackTarget (bytecode 0-38, sipush 200).
func (t *TickLoop) wardenSetAttackTarget(e *Entity, targetID int32) {
	ws := e.warden
	if ws == nil || e.ai == nil {
		return
	}
	ws.roarTargetID = 0            // eraseMemory(ROAR_TARGET)
	e.ai.attackTargetID = targetID // setMemory(ATTACK_TARGET, target)
	ws.lastTargetID = targetID
	ws.sonicCooldown = wardenSonicOnAcquire // SonicBoom.setCooldown(this, 200)
}

// wardenTickRoar ports the Roar behavior: it runs for ROAR_DURATION=84 ticks (ATTACK_TARGET stays absent, so
// the FIGHT activity cannot run -- ROAR outranks FIGHT). On Roar.start (roarTicks armed to 84) the warden calls
// increaseAngerAt(roarTarget, ROAR_ANGER_INCREASE=20, false). On the final tick (Roar.stop) it calls
// setAttackTarget(roarTarget) -> ATTACK_TARGET set, ROAR_TARGET erased. Returns true while roaring (the caller
// must NOT fight). Cite Roar.start + Roar (duration) + Roar.stop.
func (t *TickLoop) wardenTickRoar(e *Entity) bool {
	ws := e.warden
	if ws == nil || ws.roarTicks <= 0 {
		return false
	}
	ws.roarTicks--
	if ws.roarTicks <= 0 {
		// Roar.stop: getMemory(ROAR_TARGET).ifPresent(warden::setAttackTarget); eraseMemory(ROAR_TARGET). A
		// roar target that logged off / died in the 84 ticks yields no attack target (Optional empty). The
		// suspect is re-derived from the live-suspect map; if gone, ROAR_TARGET is simply cleared.
		rt := ws.roarTargetID
		ws.roarTargetID = 0
		if rt != 0 {
			if p := t.playerByEntityID(rt); p != nil && !p.dead {
				t.wardenSetAttackTarget(e, rt) // setAttackTarget(roarTarget)
			}
		}
	}
	return true // still roaring this tick (or just finished) -- no FIGHT this tick
}

// wardenSenseNearbyPlayers is the v1 anger FEED standing in for the deferred VibrationSystem.Ticker +
// Sniffing: a player within FOLLOW_RANGE (24) raises the warden anger at it by DEFAULT_ANGER 35 (the same
// increment a shriek/hit uses). Runs on the every-20 cadence so a nearby player anger stays above the
// decay (net +34 per 20t while adjacent). Cite Warden VibrationSystem.User + WardenAi (the anger feeds).
func (t *TickLoop) wardenSenseNearbyPlayers(e *Entity) {
	ws := e.warden
	if ws == nil {
		return
	}
	followRange := e.getAttributeValue(attribute.FollowRange) // 24.0
	rangeSq := followRange * followRange
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		if distanceToSqrPlayer(p, e) <= rangeSq {
			t.wardenIncreaseAngerAt(e, p.entityID, wardenProximityAngerBoost) // increaseAngerAt(player) == +35
		}
	}
}

// wardenAiStep ports Warden.customServerAiStep + the reduced Brain activity graph, driven per-type from
// tickAI (gated on e.warden != nil, AFTER serverAiStep, like witherAiStep). Order: (1) EMERGE lock; (2)
// the every-20 anger feed + decay + the %120 darkness gate; (3) target = highest-anger suspect; (4) with
// a target, SONIC BOOM if in 15/20 range + off cooldown else MELEE when adjacent; (5) no anger -> count
// toward + run the DIG-away despawn. tickCount is the t.gametime proxy. Cite Warden.customServerAiStep.
func (t *TickLoop) wardenAiStep(e *Entity) {
	ws := e.warden
	if ws == nil || e.dead || e.health <= 0 {
		return
	}
	// (1) EMERGE lock: isDiggingOrEmerging() -> the warden does not target/act.
	if ws.emergeTicks > 0 {
		ws.emergeTicks--
		return
	}
	// A dig in progress runs to completion then discards the warden.
	if ws.digging {
		t.wardenTickDig(e)
		return
	}
	// (2) cadence: applyDarknessAround every (tickCount+getId())%120==0; anger tick every tickCount%20==0.
	if (t.gametime+int64(e.id))%wardenDarknessInterval == 0 {
		t.wardenApplyDarknessAround(e) // DEFERRED darkness pulse (cited no-op)
	}
	// WardEN-08 FIX: the anger tick gates on tickCount%20==0 directly (like the darkness %120 gate on the
	// line above), NOT a private accumulator -- angerManagement.tick runs on the every-20 phase boundary.
	// tickCount is the t.gametime proxy (this warden lives while the fight is active). Cite Warden
	// .customServerAiStep (ANGERMANAGEMENT_TICK_DELAY=20 gate) + AngerManagement.tick.
	// REAL vibration feed: VibrationSystem.Ticker.tick runs EVERY tick (candidate select, travel-
	// time decrement, delivery -> onReceiveVibration anger). This REPLACES the synthetic proximity
	// feed (wardenSenseNearbyPlayers). Cite VibrationSystem.Ticker.tick.
	t.tickWardenVibration(e)
	// AngerManagement.tick runs on the every-20 phase boundary (decay + drop).
	if t.gametime%wardenAngerTickDelay == 0 {
		t.wardenTickAnger(e) // AngerManagement.tick: decay + drop
	}
	// SonicBoom cooldown countdown (SONIC_BOOM_COOLDOWN memory expiry).
	if ws.sonicCooldown > 0 {
		ws.sonicCooldown--
	}
	// MeleeAttack cooldown countdown (ATTACK_COOLING_DOWN memory expiry; MeleeAttack.create(18)).
	if ws.meleeCooldown > 0 {
		ws.meleeCooldown--
	}
	// (3) ROAR (activity priority ROAR > FIGHT): while roaring the warden CANNOT fight (ATTACK_TARGET is
	// absent for the 84-tick Roar). wardenTickRoar counts the roar down and, on the final tick (Roar.stop),
	// calls setAttackTarget(roarTarget) -- handing the FIGHT activity its target on the tick AFTER the roar.
	if t.wardenTickRoar(e) {
		// Still roaring (or just handed off the attack target). A charging boom cannot start during a roar
		// (ATTACK_COOLING_DOWN); a boom already in flight is finished below on a later tick. No fight this tick.
		if ws.sonicChargeTicks > 0 {
			var chargeTarget *tickPlayer
			if e.ai.attackTargetID != 0 {
				chargeTarget = t.playerByEntityID(e.ai.attackTargetID)
			}
			t.wardenTickSonicCharge(e, chargeTarget)
		}
		ws.noAngerTicks = 0 // an active roar means the warden is angry -> no dig-away
		return
	}
	// (4) FIGHT: the warden has an ATTACK_TARGET (set by Roar.stop or Warden.hurtServer). Validate it is a
	// live suspect (StopAttackingIfTargetInvalid); a gone target clears ATTACK_TARGET and falls through to the
	// idle roar-gate. Cite WardenAi.initFightActivity (StopAttackingIfTargetInvalid) + isTarget.
	var target *tickPlayer
	if e.ai.attackTargetID != 0 {
		target = t.playerByEntityID(e.ai.attackTargetID)
		if target == nil || target.dead {
			e.ai.attackTargetID = 0 // onTargetInvalid -> ATTACK_TARGET erased
			ws.lastTargetID = 0
			target = nil
		}
	}
	// A charging boom finishes regardless of reselection (DURATION-locked); tick it first.
	if ws.sonicChargeTicks > 0 {
		t.wardenTickSonicCharge(e, target)
		return // ATTACK_COOLING_DOWN: no melee/re-charge while a boom is in flight
	}
	if target != nil {
		ws.noAngerTicks = 0
		// (4a) SONIC BOOM vs MELEE. checkExtraStartConditions: closerThan(15,20) AND sonicCooldown==0 (the
		// melee-hit lock is sonicCooldown, so a just-melee'd warden cannot immediately boom -- the
		// TIME_TO_USE_MELEE_UNTIL_SONIC_BOOM intent).
		if ws.sonicCooldown == 0 && wardenCloserThan(e, target, wardenSonicDistanceXZ, wardenSonicDistanceY) {
			t.wardenStartSonicBoom(e)
			return
		}
		// WardenAi.initFightActivity: MeleeAttack.create(18) -- the melee behavior sets ATTACK_COOLING_DOWN
		// with an 18-tick expiry after a swing and will not re-run while it is active. meleeCooldown models
		// that memory: melee only when it is 0, then re-arm to 18. Cite WardenAi (MeleeAttack.create(18),
		// MELEE_ATTACK_COOLDOWN=18) + MeleeAttack (ATTACK_COOLING_DOWN gate).
		if ws.meleeCooldown == 0 && isWithinMeleeAttackRange(e, target) {
			t.wardenDoHurtTarget(e, target) // 30 dmg + 1.5 kb + set the 40-tick sonic lock
			ws.meleeCooldown = wardenMeleeCooldown
		}
		return
	}
	// (5) IDLE: no ATTACK_TARGET. SetRoarTarget (IDLE/INVESTIGATE/SNIFF) arms ROAR_TARGET ONLY when
	// getEntityAngryAt() (anger >= ANGRY 80) yields a suspect -- this is the roar gate: an AGITATED (40-79) or
	// CALM warden acquires nothing and will not attack. On arming ROAR_TARGET the Roar behavior starts next.
	// Cite SetRoarTarget (getEntityAngryAt) + WardenAi.getActivities (IDLE.SetRoarTarget).
	if angry := t.wardenGetEntityAngryAt(e); angry != nil {
		ws.noAngerTicks = 0
		t.wardenStartRoar(e, angry.entityID) // SetRoarTarget -> ROAR_TARGET; Roar.start
		return
	}
	// (6) NO active anger (below ANGRY): count toward the dig-away despawn; reset when anger returns to ANGRY.
	// getActiveAnger()==0 is the vanilla no-anger signal; below-ANGRY (but >0) the warden is idle but not
	// despawning yet -- vanilla's dig-away is gated on the DIG_COOLDOWN + no anger, so mirror the anger==0 gate.
	if t.wardenActiveAnger(e) == 0 {
		ws.noAngerTicks++
		if ws.noAngerTicks >= wardenNoAngerDespawnTicks {
			t.wardenStartDig(e)
		}
	} else {
		ws.noAngerTicks = 0
	}
}

// wardenStartRoar ports SetRoarTarget (arm ROAR_TARGET) + Roar.start: set ROAR_TARGET, arm the ROAR_DURATION=84
// countdown, and increaseAngerAt(roarTarget, ROAR_ANGER_INCREASE=20, false). The warden looks at the target +
// enters the ROARING pose (client-cosmetic, deferred). ATTACK_TARGET stays absent until Roar.stop. Cite
// SetRoarTarget.create + Roar.start.
func (t *TickLoop) wardenStartRoar(e *Entity, targetID int32) {
	ws := e.warden
	if ws == nil || targetID == 0 {
		return
	}
	ws.roarTargetID = targetID        // setMemory(ROAR_TARGET, target)
	ws.roarTicks = wardenRoarDuration // Roar duration 84
	// Roar.start: increaseAngerAt(roarTarget, ROAR_ANGER_INCREASE=20, false).
	t.wardenIncreaseAngerAt(e, targetID, wardenRoarAngerIncrease)
}

// wardenCloserThan ports Entity.closerThan(entity, dx, dz): horizontal lengthSquared(dX,dZ) < dx*dx AND
// vertical dY*dY < dz*dz (VERIFIED javap Entity.closerThan(Entity,double,double)). Cite Entity.closerThan.
func wardenCloserThan(e *Entity, p *tickPlayer, dx, dz float64) bool {
	ddx := p.x - e.x
	ddy := p.y - e.y
	ddz := p.z - e.z
	if ddx*ddx+ddz*ddz >= dx*dx {
		return false
	}
	return ddy*ddy < dz*dz
}

// wardenDoHurtTarget ports Warden.doHurtTarget: broadcastEntityEvent(4) [deferred] + WARDEN_ATTACK_IMPACT
// [deferred] + SonicBoom.setCooldown(this, 40) [the melee->sonic lock] + Monster.doHurtTarget (ATTACK_DAMAGE
// 30 via the player hurt path; the default-knockback pushes the target away, its ATTACK_KNOCKBACK 1.5
// feeding the mob-attack knockback). Cite Warden.doHurtTarget + Monster.doHurtTarget.
func (t *TickLoop) wardenDoHurtTarget(e *Entity, target *tickPlayer) {
	if target == nil || target.dead {
		return
	}
	if ws := e.warden; ws != nil {
		ws.sonicCooldown = wardenMeleeToSonicLock // SonicBoom.setCooldown(this, 40)
	}
	t.broadcastMobSwing(e)                                      // attack animation (broadcastEntityEvent(4) analogue)
	dmg := float32(e.getAttributeValue(attribute.AttackDamage)) // == 30.0
	src := damageSourceMobAttack(e.id)
	t.applyDamage(target, src, dmg)
}

// wardenStartSonicBoom ports SonicBoom.start: arm the sound delay TICKS_BEFORE_PLAYING_SOUND=34 (the charge
// windup; the DURATION=60 ATTACK_COOLING_DOWN lock is implicit). broadcastEntityEvent(62) + WARDEN_SONIC_CHARGE
// deferred. Cite SonicBoom.start.
func (t *TickLoop) wardenStartSonicBoom(e *Entity) {
	if ws := e.warden; ws != nil {
		ws.sonicChargeTicks = wardenSonicChargeTicks // windup 34
	}
}

// wardenTickSonicCharge ports SonicBoom.tick: count the windup; on reaching 0 (the sound tick), if the
// target is still in range, FIRE the boom then arm SONIC_BOOM_COOLDOWN=40 (SonicBoom.stop). Cite SonicBoom.tick
// + SonicBoom.stop.
func (t *TickLoop) wardenTickSonicCharge(e *Entity, target *tickPlayer) {
	ws := e.warden
	if ws == nil {
		return
	}
	ws.sonicChargeTicks--
	if ws.sonicChargeTicks > 0 {
		return // still charging
	}
	if target != nil && !target.dead && wardenCloserThan(e, target, wardenSonicDistanceXZ, wardenSonicDistanceY) {
		t.wardenFireSonicBoom(e, target) // lambda tick1: closerThan(15,20) re-check
	}
	ws.sonicCooldown = wardenSonicCooldown // SonicBoom.stop -> setCooldown(this, 40)
}

// wardenFireSonicBoom ports SonicBoom.lambda tick2 (the boom hit): origin = warden position + WARDEN_CHEST
// attachment; dir = normalize(target.getEyePosition() - origin); hurtServer(sonicBoom, 10.0) [ignores armor
// + shields]; on a landed hit push(dir.x*d9, dir.y*d7, dir.z*d9) with d7=0.5*(1-kbResist),
// d9=2.5*(1-kbResist). The SONIC_BOOM particle beam (floor(len)+7 steps) is client-cosmetic (deferred).
// Cite SonicBoom.lambda tick2.
func (t *TickLoop) wardenFireSonicBoom(e *Entity, target *tickPlayer) {
	// origin = position + WARDEN_CHEST attachment (chest height 1.6, cited; x/z 0). Cite EntityAttachments.WARDEN_CHEST.
	const wardenChestAttachmentY = 1.6
	ox := e.x
	oy := e.y + wardenChestAttachmentY
	oz := e.z
	tx := target.x
	ty := target.y + playerStandingEyeHeight // getEyePosition() == getY() + eyeHeight
	tz := target.z
	dvx := tx - ox
	dvy := ty - oy
	dvz := tz - oz
	length := math.Sqrt(dvx*dvx + dvy*dvy + dvz*dvz)
	nx, ny, nz := 0.0, 0.0, 0.0
	if length >= 1.0e-4 { // Vec3.normalize: d < 1.0E-4 ? ZERO : scale(1/d)
		nx = dvx / length
		ny = dvy / length
		nz = dvz / length
	}
	_ = int(math.Floor(length)) + wardenSonicReachBonus // particle beam step count (deferred)

	// hurtServer(sonicBoom, 10.0f). The sonic_boom source bypasses armor + is not shield-blockable, so the
	// full 10.0 lands. Snapshot the landed flag (like the wither-skeleton WITHER add) so the push gates on a
	// landed hit. Cite DamageSources.sonicBoom + LivingEntity.push.
	src := damageSourceByTypeName("minecraft:sonic_boom", e.id)
	hurt := !target.dead
	if hurt && float32(target.invulnerableTime) > hurtCooldownConst {
		if wardenSonicDamage <= float64(target.lastHurt) {
			hurt = false
		}
	}
	t.applyDamage(target, src, float32(wardenSonicDamage))
	if !hurt {
		return // hurtServer returned false -> no knock-up
	}
	kbResist := target.getAttributeValue(attrKnockbackResistance)
	d7 := wardenSonicKnockVertical * (1.0 - kbResist)
	d9 := wardenSonicKnockHoriz * (1.0 - kbResist)
	t.wardenPushPlayer(e, target, nx*d9, ny*d7, nz*d9)
}

// wardenPushPlayer ports LivingEntity.push(dx,dy,dz) on a player victim: setDeltaMovement(add(dx,dy,dz)) +
// the client motion sync THIS tick (the hurtMarked/needsSync flush). It ADDS (does not halve/replace like
// the melee knockback). Cite Entity.push.
func (t *TickLoop) wardenPushPlayer(e *Entity, p *tickPlayer, dx, dy, dz float64) {
	if p.playerEntity == nil {
		return
	}
	p.playerEntity.vx += dx
	p.playerEntity.vy += dy
	p.playerEntity.vz += dz
	if p.client != nil {
		p.client.Send(encodeSetEntityMotion(p.playerEntity)) // needsSync flush
	}
}

// wardenStartDig ports WardenAi Dig.start: begin the dig-away (DIGGING for DIGGING_DURATION=100); on finish
// the warden is discarded. Clears the target. Cite WardenAi Dig.start + Digging.
func (t *TickLoop) wardenStartDig(e *Entity) {
	ws := e.warden
	if ws == nil {
		return
	}
	ws.digging = true
	ws.digTicks = wardenDiggingDuration // DIGGING_DURATION 100
	if e.ai != nil {
		e.ai.attackTargetID = 0
	}
}

// wardenTickDig ports the Digging countdown: tick DIGGING_DURATION down; at 0 discard the warden
// (Digging.stop -> discard()). Cite Digging.
func (t *TickLoop) wardenTickDig(e *Entity) {
	ws := e.warden
	if ws == nil {
		return
	}
	ws.digTicks--
	if ws.digTicks <= 0 {
		e.dead = true                              // discard()
		t.unregisterVibrationListener(e)           // drop its VibrationSystem.Listener
		t.regionForEntity(e).entities.remove(e.id) // remove from its owner region
	}
}

// effectDarkness is MobEffects.DARKNESS -- the registry id the Warden's pulse carries. Declared here (not in
// mob_effect.go) so the effect subsystem's logic is untouched; addPlayerEffect stores it as a bare duration
// effect (DARKNESS has no attribute modifier -- its screen-darken is a client render the v1 effect subsystem
// does not yet broadcast, so the ambient/visible/showIcon flags below are stored non-observably but faithfully).
const effectDarkness = "minecraft:darkness"

// wardenApplyDarknessAround ports Warden.applyDarknessAround(level, this.position(), this, DARKNESS_RADIUS=20)
// -> MobEffectUtil.addEffectToPlayersAround(level, this, pos, 20, new MobEffectInstance(DARKNESS, 260, 0, false,
// false, false), 200). For every SURVIVAL player NOT allied to the warden (a player is never allied to a
// warden, so that guard is always true) within 20 blocks (3D closerThan) of the warden's feet, add DARKNESS
// 260/0 -- UNLESS the player already carries DARKNESS at >= this amplifier AND that instance does not end
// within reapplyProbability-1 (=199) ticks (the re-application gate that avoids resetting a still-long pulse).
// addPlayerEffect is the faithful LivingEntity.addEffect; the DARKNESS instance's ambient/visible/showIcon are
// all false, so the stored effect's flags are corrected after the add (client-render only, non-observable in
// v1 which does not yet broadcast UpdateMobEffect). Cite Warden.applyDarknessAround + MobEffectUtil
// .addEffectToPlayersAround + its lambda$0 (isSurvival && !isAllied && closerThan && reapply-gate).
func (t *TickLoop) wardenApplyDarknessAround(e *Entity) {
	radiusSqr := float64(wardenDarknessRadius) * float64(wardenDarknessRadius) // closerThan(pos, 20) == distSqr < 20*20 (strict)
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		// gameMode.isSurvival(): the pulse only lands on survival players (creative/spectator/adventure skip).
		if p.gameMode != gameModeSurvival {
			continue
		}
		// (source == null || !source.isAlliedTo(player)): the warden (source) is never allied to a player, so
		// this clause is always true here -- a warden pulses DARKNESS onto every nearby survival player.
		// player.position().closerThan(pos, 20): 3D squared-distance gate from the warden's feet.
		// Vec3.closerThan(pos, 20) is strict distSq < 20*20 (VERIFIED javap Vec3.closerThan: dcmpg; ifge),
		// so the pulse SKIPS (continue) exactly when NOT closer, i.e. distSq >= radiusSqr. A player at
		// EXACTLY the radius is NOT closerThan -> skipped. Cite Vec3.closerThan (strict <).
		dx, dy, dz := p.x-e.x, p.y-e.y, p.z-e.z
		if dx*dx+dy*dy+dz*dz >= radiusSqr {
			continue
		}
		// The reapply gate (lambda$0 tail): if the player already has DARKNESS whose amplifier >= the new
		// amplifier AND that instance does NOT end within reapplyProbability-1 ticks, skip -- do not reset a
		// still-lengthy pulse. hasEffect(false) or a shorter/weaker existing pulse falls through and re-applies.
		if amp, ok := playerEffectAmplifier(p, effectDarkness); ok && amp >= wardenDarknessAmplifier {
			if cur := p.activeEffects[effectDarkness]; cur != nil && !effectEndsWithin(cur, wardenDarknessReapplyProb-1) {
				continue
			}
		}
		// ServerPlayer.addEffect(new MobEffectInstance(DARKNESS, 260, 0), warden): the faithful add. The warden
		// is the effect source (ownerID = e.id).
		t.addPlayerEffect(p, e.id, effectDarkness, wardenDarknessDuration, wardenDarknessAmplifier, 1.0)
		// MobEffectInstance(DARKNESS, 260, 0, ambient=false, visible=false, showIcon=false): correct the render
		// flags on the stored instance to match the vanilla ctor (addPlayerEffect defaults visible/showIcon=true).
		if cur := p.activeEffects[effectDarkness]; cur != nil {
			cur.ambient = false
			cur.visible = false
			cur.showIcon = false
		}
	}
}

// effectEndsWithin ports MobEffectInstance.endsWithin(n): !isInfiniteDuration() && duration <= n. An infinite
// (-1) effect never ends within any finite window. Cite MobEffectInstance.endsWithin.
func effectEndsWithin(e *activeEffect, n int) bool {
	if e == nil || e.duration == -1 {
		return false
	}
	return e.duration <= n
}

// wardenSummonPos resolves a valid nearby spawn position for a warden summoned by a shrieker at pos,
// mirroring SculkShriekerBlockEntity.trySummonWarden / Warden.trySpawn box search: v1 uses the block above
// the shrieker (a deterministic stand-in). Returns the world-space feet position. Cite
// SculkShriekerBlockEntity.trySummonWarden.
func wardenSummonPos(pos pk.Position) (float64, float64, float64) {
	return float64(pos.X) + 0.5, float64(pos.Y) + 1.0, float64(pos.Z) + 0.5
}
