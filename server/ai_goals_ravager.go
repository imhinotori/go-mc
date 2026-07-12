package server

// ai_goals_ravager.go — RAIDER (Task): the Ravager.aiStep roar/stun/attack state machine + roar() AoE,
// PORTED 1:1 from the unobfuscated 26.2 jar (net.minecraft.world.entity.monster.Ravager, CFR this
// session). Ravager uses the plain MeleeAttackGoal (NO RavagerMeleeAttackGoal inner class in 26.2, NO
// charge). doHurtTarget sets attackTick=10 + event 4. aiStep: speed lerp (immobile->0 / target?0.35:0.3),
// leaf-trample (DEFERRED, no block-break), then roar/attack/stun countdowns (roar() fires at roarTick==10;
// stun end re-arms roarTick=20). roar(): LivingEntity in BB.inflate(4.0) -> non-illager hurt 6.0 (mobAttack),
// non-player strongKnockback (dd=max(xd2+zd2,0.001); push(xd/dd*4, 0.2, zd/dd*4)). NO RNG. v1 DEFERRALS
// (cited): stun TRIGGER (shield-block, no subsystem), leaf-trample block-break, stunEffect particle (client).

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// Ravager constants (VERIFIED CFR Ravager).
const (
	ravagerAttackDuration = 10   // ATTACK_DURATION
	ravagerStunDuration   = 40   // STUN_DURATION (trigger deferred)
	ravagerRoarArmTicks   = 20   // roarTick set when the stun ends
	ravagerRoarFireTick   = 10   // roarTick value at which roar() fires
	ravagerBaseSpeed      = 0.3  // BASE_MOVEMENT_SPEED (no target)
	ravagerAttackSpeed    = 0.35 // ATTACK_MOVEMENT_SPEED (has target)
	ravagerSpeedLerpT     = 0.1  // Mth.lerp(0.1, base, maxSpeed)
	ravagerRoarInflate    = 4.0  // roar() AoE box inflate on all axes
	ravagerRoarDamage     = 6.0  // roar() hurtServer(mobAttack, 6.0)
	ravagerStrongKbHoriz  = 4.0  // strongKnockback horizontal scale
	ravagerStrongKbVert   = 0.2  // strongKnockback vertical
)

// entityRavagerID is the Ravager wire type id, exposed for the shield-block stun trigger (blockUsingItem
// in shield_blocking.go resolves the attacker without importing data/entity). Cite EntityType.RAVAGER.
var entityRavagerID = entity.Ravager.ID

// Ravager entity-event bytes. handleEntityEvent(4)=attack swing; (69)=roar poof + client knockback.
const (
	entityEventRavagerAttack  byte = 4  // handleEntityEvent(4): attack swing
	entityEventRavagerStunned byte = 39 // handleEntityEvent(39): blockedByItem stun (client stun-color flash)
	entityEventRavagerRoar    byte = 69 // handleEntityEvent(69): roar poof + client knockback
)

// ravagerIsImmobile ports Ravager.isImmobile: health<=0 || attackTick>0 || stunnedTick>0 || roarTick>0.
func ravagerIsImmobile(e *Entity) bool {
	return e.health <= 0 || e.ravagerAttackTick > 0 || e.ravagerStunnedTick > 0 || e.ravagerRoarTick > 0
}

// ravagerDidHurt ports Ravager.doHurtTarget pre-super: attackTick=10; broadcastEntityEvent(4). Called from
// the melee-hit path for a ravager BEFORE the shared doHurtTarget deals damage. RAVAGER_ATTACK sound deferred.
func (t *TickLoop) ravagerDidHurt(e *Entity) {
	e.ravagerAttackTick = ravagerAttackDuration
	t.broadcastToTrackers(e.id, encodeEntityEvent(e.id, entityEventRavagerAttack))
}

// ravagerAiStep ports Ravager.aiStep server branch (per-type hook, sibling of creeperAiStep), AFTER
// serverAiStep. Speed lerp, (leaf-trample DEFERRED), then roar/attack/stun countdowns.
func (t *TickLoop) ravagerAiStep(e *Entity) {
	if !e.isAlive() || e.dead {
		return
	}
	if e.attributes != nil {
		if inst := e.attributes.GetInstance(attribute.MovementSpeed.Name()); inst != nil {
			if ravagerIsImmobile(e) {
				inst.SetBaseValue(0.0)
			} else {
				maxSpeed := ravagerBaseSpeed
				if mobTarget(e) != 0 {
					maxSpeed = ravagerAttackSpeed
				}
				base := inst.BaseValue()
				inst.SetBaseValue(base + (maxSpeed-base)*ravagerSpeedLerpT) // Mth.lerp(0.1, base, maxSpeed)
			}
		}
	}
	// leaf-trample (Ravager.aiStep server branch, offsets 96-271): only when horizontalCollision is set
	// AND mobGriefing is on, destroy every LeavesBlock cell in getBoundingBox().inflate(0.2). `flag` tracks
	// whether ANY block was destroyed (destroyBlock returned false but the cell WAS a leaf -> still counts,
	// via `!destroyBlock(pos, true, this) ? flag : true`), and if NOTHING was destroyed while onGround the
	// ravager jumps. NO RNG (no ravager stream draws here). CITE: Ravager.aiStep (LeavesBlock destroyBlock).
	t.ravagerLeafTrample(e)
	if e.ravagerRoarTick > 0 {
		e.ravagerRoarTick--
		if e.ravagerRoarTick == ravagerRoarFireTick {
			t.ravagerRoar(e)
		}
	}
	if e.ravagerAttackTick > 0 {
		e.ravagerAttackTick--
	}
	if e.ravagerStunnedTick > 0 {
		e.ravagerStunnedTick--
		t.ravagerStunEffect(e) // stunEffect(): random.nextInt(6) [+ 2x nextDouble on ==0] every stunned tick.
		if e.ravagerStunnedTick == 0 {
			// playSound(RAVAGER_ROAR): cite-deferred client sound.
			e.ravagerRoarTick = ravagerRoarArmTicks
		}
	}
}

// ravagerRoar ports Ravager.roar(): getEntitiesOfClass(LivingEntity, BB.inflate(4.0), predicate) ->
// non-illager hurt 6.0 (mobAttack), non-player strongKnockback. NO RNG. Players take 6.0 but are NOT
// knocked back server-side (client event 69). The predicate is mobGriefing-branched: ROAR_TARGET_WITH_
// GRIEFING == `!(e instanceof Ravager) && e.isAlive()`; ROAR_TARGET_WITHOUT_GRIEFING adds `&& !e.is(
// ARMOR_STAND)`. Only alive() + is-alive() checked, the type filters below. CITE: Ravager.roar +
// ROAR_TARGET_WITH_GRIEFING / ROAR_TARGET_WITHOUT_GRIEFING (lambda$static$0 / lambda$static$1).
func (t *TickLoop) ravagerRoar(e *Entity) {
	// roar() is guarded by isAlive() && level instanceof ServerLevel (offsets 0-16); a dead ravager does
	// nothing. CITE: Ravager.roar (isAlive early-out).
	if !e.isAlive() || e.dead {
		return
	}
	box := e.AABB()
	lx, ly, lz := box.Lower[0]-ravagerRoarInflate, box.Lower[1]-ravagerRoarInflate, box.Lower[2]-ravagerRoarInflate
	ux, uy, uz := box.Upper[0]+ravagerRoarInflate, box.Upper[1]+ravagerRoarInflate, box.Upper[2]+ravagerRoarInflate
	src := damageSourceMobAttack(e.id)
	// mobGriefing selects the predicate: with griefing keeps armor stands as roar targets; without griefing
	// excludes them (ROAR_TARGET_WITHOUT_GRIEFING). CITE: Ravager.roar (GameRules.MOB_GRIEFING branch).
	griefing := t.gameRule(ruleMobGriefing)
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		if !pointInBox(p.x, p.y, p.z, lx, ly, lz, ux, uy, uz) {
			continue
		}
		t.applyDamage(p, src, float32(ravagerRoarDamage))
	}
	for _, other := range t.cur().entities.all() {
		if other == nil || other.id == e.id || other.dead || !other.isAlive() {
			continue
		}
		if other.typ == e.typ { // !(e instanceof Ravager) -> exclude other ravagers
			continue
		}
		if !griefing && other.typ == entity.ArmorStand.ID { // ROAR_TARGET_WITHOUT_GRIEFING excludes armor stands
			continue
		}
		if !pointInBox(other.x, other.y, other.z, lx, ly, lz, ux, uy, uz) {
			continue
		}
		if !isAbstractIllager(other.typ) {
			t.applyDamageEntity(other, src, float32(ravagerRoarDamage))
		}
		ravagerStrongKnockback(e, other)
	}
	t.broadcastToTrackers(e.id, encodeEntityEvent(e.id, entityEventRavagerRoar))
}

// ravagerStrongKnockback ports Ravager.strongKnockback: dd=max(xd2+zd2,0.001); push(xd/dd*4, 0.2, zd/dd*4).
func ravagerStrongKnockback(e, victim *Entity) {
	xd := victim.x - e.x
	zd := victim.z - e.z
	dd := math.Max(xd*xd+zd*zd, 0.001)
	victim.vx += xd / dd * ravagerStrongKbHoriz
	victim.vy += ravagerStrongKbVert
	victim.vz += zd / dd * ravagerStrongKbHoriz
}

// pointInBox reports whether the feet point is inside the [l,u] box (the reduced getEntitiesOfClass; a
// full AABB-intersect is the exact-parity refinement once the entity broad-phase lands).
func pointInBox(px, py, pz, lx, ly, lz, ux, uy, uz float64) bool {
	return px >= lx && px <= ux && py >= ly && py <= uy && pz >= lz && pz <= uz
}

// isAbstractIllager reports whether a wire type is an AbstractIllager subclass (Pillager/Vindicator/Evoker).
// Ravager itself is NOT one (extends Raider directly). Cite AbstractIllager.
func isAbstractIllager(typ entity.ID) bool {
	switch typ {
	case entity.Pillager.ID, entity.Vindicator.ID, entity.Evoker.ID:
		return true
	default:
		return false
	}
}

// ravagerLeafTrample ports the Ravager.aiStep leaf-trample loop (offsets 96-271): only when the ravager
// is on a ServerLevel, horizontalCollision is set, and mobGriefing is on, iterate every BlockPos in
// getBoundingBox().inflate(0.2) (Mth.floor of each face), and for each LeavesBlock cell call
// destroyBlock(pos, drops=true, this). flag = whether any cell was destroyed (flag = destroyBlock ||
// flag, mirroring the !destroyBlock ? flag : true bytecode). After the sweep, if NOTHING was destroyed
// AND the ravager is onGround, jumpFromGround(). NO ravager-stream RNG. The dropResources loot roll +
// levelEvent(2001) break particle/sound are a cited client/loot deferral (block -> air is load-bearing).
// CITE: Ravager.aiStep (LeavesBlock instanceof -> ServerLevel.destroyBlock; onGround -> jumpFromGround).
func (t *TickLoop) ravagerLeafTrample(e *Entity) {
	if t.world() == nil {
		return // level not a ServerLevel proxy in this test/region -> no-op (matches non-ServerLevel branch)
	}
	if !e.horizontalCollision {
		return // offset 96: if (this.horizontalCollision) gate
	}
	if !t.gameRule(ruleMobGriefing) {
		return // offset 103-119: GameRules.MOB_GRIEFING gate
	}
	// getBoundingBox().inflate(0.2): expand all six faces by 0.2, then Mth.floor each face (offsets 124-177).
	box := e.AABB()
	const inflate = 0.2
	minX := mthFloor(box.Lower[0] - inflate)
	minY := mthFloor(box.Lower[1] - inflate)
	minZ := mthFloor(box.Lower[2] - inflate)
	maxX := mthFloor(box.Upper[0] + inflate)
	maxY := mthFloor(box.Upper[1] + inflate)
	maxZ := mthFloor(box.Upper[2] + inflate)
	flag := false // offset 122: boolean flag = false;
	// BlockPos.betweenClosed(minX,minY,minZ, maxX,maxY,maxZ): the inclusive x->y->z walk (offsets 177-253).
	for x := minX; x <= maxX; x++ {
		for y := minY; y <= maxY; y++ {
			for z := minZ; z <= maxZ; z++ {
				pos := pk.Position{X: x, Y: y, Z: z}
				state := t.blockStateAt(x, y, z)
				if !block.IsLeaves(state) { // if (block instanceof LeavesBlock)
					continue
				}
				// destroyBlock(pos, drops=true, this): remove the leaf (block -> air). flag |= destroyed.
				if t.ravagerDestroyLeaf(pos, state) {
					flag = true
				}
			}
		}
	}
	// offset 256-268: if (!flag && this.onGround()) this.jumpFromGround();.
	if !flag && e.onGround {
		jumpFromGround(e)
	}
}

// ravagerDestroyLeaf ports ServerLevel.destroyBlock(pos, true, ravager) for the leaf-trample cell: set the
// leaf to air and broadcast the change (+ relight). Returns true when the block was actually removed
// (destroyBlock returns false for an already-air/failed cell). The drops=true loot roll (dropResources)
// and the levelEvent(2001) break particle/sound are a cited client/loot deferral. CITE: ServerLevel.
// destroyBlock (removeBlock + spawnAfterBreak/dropResources + levelEvent 2001).
func (t *TickLoop) ravagerDestroyLeaf(pos pk.Position, oldState block.StateID) bool {
	air := t.airState()
	if !t.world().SetBlock(pos, air, dimMinY) {
		return false // destroyBlock returns false when the state did not change (e.g. already air)
	}
	t.broadcastBlockUpdate(pos, air)
	// Light re-propagation fires CENTRALLY from ChunkManager.SetBlock (SetBlockChangeHook) -- the
	// SetBlock above already relit + broadcast. No per-site relight call.
	return true
}

// ravagerStunEffect ports Ravager.stunEffect(): draw random.nextInt(6); on == 0, draw two more
// random.nextDouble() (the x and z particle offsets) and (in vanilla) spawn one ENTITY_EFFECT particle at
// the computed head position. The particle spawn is a CITED client-visual deferral (no per-tick particle
// wire), but the RNG draws MUST land in this exact order/count every stunned tick so the ravager's
// per-entity random stream stays byte-identical to the jar. Uses the mob's own RandomSource (this.random ==
// getRandom() == mobRandom(e)). CITE: Ravager.stunEffect (nextInt(6); [nextDouble; nextDouble]).
func (t *TickLoop) ravagerStunEffect(e *Entity) {
	r := mobRandom(e)
	if r.nextInt(6) != 0 { // offset 4-11: if (random.nextInt(6) == 0)
		return
	}
	// offset 14-140: the ENTITY_EFFECT particle at (x - bbWidth*sin(yBodyRot*PI/180) + nextDouble()*0.6-0.3,
	// y + bbHeight - 0.3, z + bbWidth*cos(...) + nextDouble()*0.6-0.3). The two nextDouble draws MUST fire in
	// x-then-z order; the particle emit itself is deferred (no client particle broadcast seam here).
	_ = r.nextDouble() // particle x offset draw (Ravager.stunEffect first nextDouble)
	_ = r.nextDouble() // particle z offset draw (Ravager.stunEffect second nextDouble)
	// Level.addParticle(ColorParticleOption(ENTITY_EFFECT, 0.498,0.514,0.573), ...): cited client deferral.
}

// ravagerBlockedByItem ports Ravager.blockedByItem(LivingEntity, DamageSource, float): the STUN TRIGGER.
// Invoked (via LivingEntity.blockUsingItem -> attacker.blockedByItem) when a victim SHIELD-BLOCKS the
// ravager's melee hit. If roarTick == 0, draw random.nextDouble(): < 0.5 -> stun (stunnedTick = 40, play
// RAVAGER_STUNNED [deferred], broadcastEntityEvent(this, 39), victim.push(this)); else strongKnockback the
// victim. Then set victim.hurtMarked = true. When roarTick != 0 the ravager is already roaring and does
// nothing (no RNG draw). CITE: Ravager.blockedByItem (offsets 0-66; nextDouble 0.5 gate; stunnedTick 40).
//
//	[VERIFIED CFR Ravager.blockedByItem: if (roarTick != 0) return; if (random.nextDouble() < 0.5) {
//	 stunnedTick = 40; playSound(RAVAGER_STUNNED,1,1); level().broadcastEntityEvent(this, (byte)39);
//	 attacker.push(this); } else { strongKnockback(attacker); } attacker.hurtMarked = true;.]
func (t *TickLoop) ravagerBlockedByItem(rav *Entity, victim *tickPlayer) {
	if rav.ravagerRoarTick != 0 {
		return // offset 0-4: if (roarTick != 0) return; -> NO RNG draw while roaring
	}
	if mobRandom(rav).nextDouble() < 0.5 { // offset 7-20: if (random.nextDouble() < 0.5)
		rav.ravagerStunnedTick = ravagerStunDuration // offset 23-26: stunnedTick = 40
		// playSound(RAVAGER_STUNNED, 1, 1): cited client-sound deferral.
		t.broadcastToTrackers(rav.id, encodeEntityEvent(rav.id, entityEventRavagerStunned)) // event 39
		// attacker.push(this): the victim (shield holder) is pushed away from the ravager (Entity.push).
		ravagerPushPlayer(rav, victim)
	} else {
		ravagerStrongKnockbackPlayer(rav, victim) // offset 56-58: strongKnockback(attacker)
	}
	// offset 61-63: attacker.hurtMarked = true -> force a velocity re-send to the victim's client (the
	// SetEntityMotion push, mirroring hoglinThrowTarget's hurtMarked flush).
	if victim.playerEntity != nil && victim.client != nil {
		victim.client.Send(encodeSetEntityMotion(victim.playerEntity))
	}
}

// ravagerPushPlayer ports Entity.push(Entity) for attacker.push(this): the pushed body is the VICTIM,
// moving away from the ravager. Entity.push (this==victim, other==rav): xd = victim.x-rav.x; zd =
// victim.z-rav.z; d = Mth.absMax(xd, zd); if (d >= 0.01) { d = sqrt(d); xd/=d; zd/=d; e = 1/d; if (e>1)
// e=1; xd*=e; zd*=e; xd*=0.05; zd*=0.05; victim.push(-xd, 0, -zd) }. push adds to deltaMovement. The
// pushable/level.isClientSide guards are true server-side. CITE: Entity.push(Entity).
func ravagerPushPlayer(rav *Entity, victim *tickPlayer) {
	if victim.playerEntity == nil {
		return
	}
	xd := victim.x - rav.x
	zd := victim.z - rav.z
	d := mthAbsMax(xd, zd)
	if d < 0.01 {
		return
	}
	d = math.Sqrt(d)
	xd /= d
	zd /= d
	e := 1.0 / d
	if e > 1.0 {
		e = 1.0
	}
	xd *= e
	zd *= e
	xd *= 0.05
	zd *= 0.05
	// this(victim).push(-xd, 0, -zd): the victim is shoved directly away from the ravager (addDeltaMovement).
	victim.playerEntity.vx -= xd
	victim.playerEntity.vz -= zd
}

// ravagerStrongKnockbackPlayer ports Ravager.strongKnockback for a player victim: dd = max(xd^2+zd^2,
// 0.001); push(xd/dd*4, 0.2, zd/dd*4). Identical numerics to ravagerStrongKnockback but for a tickPlayer
// (the impulse ADDS to the player's velocity, LivingEntity.push == addDeltaMovement). CITE: Ravager.
// strongKnockback.
func ravagerStrongKnockbackPlayer(rav *Entity, victim *tickPlayer) {
	if victim.playerEntity == nil {
		return
	}
	xd := victim.x - rav.x
	zd := victim.z - rav.z
	dd := math.Max(xd*xd+zd*zd, 0.001)
	victim.playerEntity.vx += xd / dd * ravagerStrongKbHoriz
	victim.playerEntity.vy += ravagerStrongKbVert
	victim.playerEntity.vz += zd / dd * ravagerStrongKbHoriz
}

// mthAbsMax ports net.minecraft.util.Mth.absMax(double, double): the larger of the two magnitudes.
//
//	[VERIFIED CFR Mth.absMax: if (a < 0) a = -a; if (b < 0) b = -b; return a > b ? a : b;.]
func mthAbsMax(a, b float64) float64 {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	if a > b {
		return a
	}
	return b
}
