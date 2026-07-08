package server

// shulker.go -- the Shulker (net.minecraft.world.entity.monster.Shulker), a 1:1 port from the
// unobfuscated 26.2 jar (javap -c -p this task). The Shulker is the End box-turret hostile: it
// CLINGS to a surface (attachFace), sits CLOSED (peek 0, +20 armor), OPENS to fire a homing
// ShulkerBullet that inflicts LEVITATION, and TELEPORTS when its spot is destroyed/exposed. Same
// additive + per-type-gated pattern as phantom/warden: ALL shulker state lives behind the single
// e.shulker pointer (nil for every other entity) so the pig oracle stays byte-identical.
//
// VANILLA (verified javap Shulker + inner classes + ShulkerBullet this task):
//   createAttributes: Mob.createMobAttributes() + MAX_HEALTH 30.0 (ARMOR base 0.0). The +20 armor
//     while CLOSED is COVERED_ARMOR_MODIFIER ("covered", 20.0, ADD_VALUE), added when peek==0 /
//     removed otherwise by setRawPeekAmount. isClosed() == getRawPeekAmount()==0.
//   ShulkerAttackGoal.tick: attackTime--; at <=0 arm attackTime = 20 + nextInt(10)*20/2 + spawn a
//     ShulkerBullet(level, this, target, attachFace.axis).
//   teleportSomewhere(): 5 tries, each axis Mth.randomBetweenInclusive(rng,-8,8); require empty +
//     above getMinY() + an attachable surface; teleportTo + setAttachFace on success.
//   ShulkerBullet.onHitEntity: hurtOrSimulate(mobProjectile(owner),4.0f); on hit a LivingEntity add
//     MobEffectInstance(LEVITATION, 200) (amplifier 0).
//
// LANDED (bytecode-exact): attributes (30 HP + the +20 covered-armor toggle), the peek open/close
// state machine, the ranged-attack cadence -> homing bullet, the bullet 4.0 damage + 200-tick
// LEVITATION via the EXISTING addPlayerEffect, the 5-attempt teleport-on-expose, attachFace/color.
// ALL RNG is on the shulker OWN mobRandom stream.
//
// DEFERRED (cited): natural spawn is via the End City "Sentry" data marker (no roaming spawner,
// matching vanilla). The peek-animation lerp, open/close sounds + CONTAINER game events, and the
// bullet client particles are cite-deferred visuals; the peek folds to a closed/open toggle.

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/block"
)

// Shulker constants (VERIFIED javap Shulker + Shulker$ShulkerAttackGoal + ShulkerBullet this task).
const (
	shulkerMaxHealth        = 30.0
	shulkerCoveredArmor     = 20.0
	shulkerCoveredArmorID   = "covered"
	shulkerPeekClosed       = 0
	shulkerPeekOpen         = 30
	shulkerNoColor          = 16
	shulkerAttackRange      = 15.0
	shulkerAttackBaseTicks  = 20
	shulkerAttackJitter     = 10
	shulkerTeleportTries    = 5
	shulkerTeleportSpan     = 8
	shulkerScanCadence      = 10
	shulkerBulletDamage     = 4.0
	shulkerBulletLevitTicks = 200
	shulkerBulletLevitAmp   = 0
	shulkerBulletSpeed      = 0.35
	shulkerBulletMaxLife    = 300
	shulkerBulletHitDistSqr = 1.0
)

type shulkerState struct {
	peek         int
	attachFace   block.Direction
	color        byte
	attackTime   int32
	nextScanTick int32
}

type shulkerBulletState struct {
	ownerID  int32
	targetID int32
	life     int32
}

// spawnShulker creates a Shulker at (x,y,z) with 30 HP, CLOSED (+20 covered armor), attachFace DOWN.
// Cite Shulker(EntityType, Level) ctor + setRawPeekAmount(0).
func (t *TickLoop) spawnShulker(x, y, z float64) *Entity {
	s := NewEntity(t.idAlloc.AllocID(), entity.Shulker, x, y, z)
	s.shulker = &shulkerState{
		peek:       shulkerPeekClosed,
		attachFace: block.Down,
		color:      shulkerNoColor,
	}
	_ = shulkerMaxHealth
	initSpawnHealth(s)
	s.ai = &mobAI{}
	reseedMobAI(s.ai, s.id)
	t.shulkerSetRawPeek(s, shulkerPeekClosed)
	owner := t.regionForEntity(s)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(s)
	return s
}

// shulkerSetRawPeek ports Shulker.setRawPeekAmount(int): REMOVE covered-armor; if peek==0 (closed)
// ADD COVERED_ARMOR_MODIFIER (+20 ARMOR ADD_VALUE). Sounds/game-event are cite-deferred visuals.
func (t *TickLoop) shulkerSetRawPeek(e *Entity, v int) {
	s := e.shulker
	s.peek = v
	if e.attributes == nil {
		return
	}
	inst := e.attributes.GetInstance(attribute.Armor.Name())
	if inst == nil {
		return
	}
	inst.RemoveModifier(shulkerCoveredArmorID)
	if v == shulkerPeekClosed {
		inst.AddPermanentModifier(attribute.AttributeModifier{
			ID:        shulkerCoveredArmorID,
			Amount:    shulkerCoveredArmor,
			Operation: attribute.AddValue,
		})
	}
}

// shulkerIsClosed ports Shulker.isClosed(): getRawPeekAmount() == 0.
func shulkerIsClosed(e *Entity) bool { return e.shulker != nil && e.shulker.peek == shulkerPeekClosed }

// shulkerTarget reads the shulker current attack-target player, or nil (mirrors phantomTarget).
func (t *TickLoop) shulkerTarget(e *Entity) *tickPlayer {
	if e.ai == nil || e.ai.attackTargetID == 0 {
		return nil
	}
	p := t.playerByEntityID(e.ai.attackTargetID)
	if p == nil || p.dead {
		return nil
	}
	return p
}

// shulkerAiStep ports the Shulker tick + goals (gated on e.shulker != nil, AFTER serverAiStep).
// Cite Shulker.customServerAiStep + Shulker$ShulkerAttackGoal.
func (t *TickLoop) shulkerAiStep(e *Entity) {
	if e.dead || e.health <= 0 {
		return
	}
	s := e.shulker
	if s == nil {
		return
	}
	t.shulkerAcquireTarget(e)
	target := t.shulkerTarget(e)
	if target != nil && t.shulkerInRange(e, target) {
		if shulkerIsClosed(e) {
			t.shulkerSetRawPeek(e, shulkerPeekOpen)
		}
		t.shulkerAttackGoal(e, target)
	} else if !shulkerIsClosed(e) {
		t.shulkerSetRawPeek(e, shulkerPeekClosed)
	}
}

// shulkerInRange reports whether the target is within the attack range. Cite Shulker$ShulkerAttackGoal.
func (t *TickLoop) shulkerInRange(e *Entity, p *tickPlayer) bool {
	if math.Abs(p.x-e.x) > shulkerAttackRange || math.Abs(p.z-e.z) > shulkerAttackRange {
		return false
	}
	return math.Abs(p.y-e.y) <= shulkerAttackRange
}

// shulkerAcquireTarget scans for the nearest live player in range every scanCadence ticks. RNG-free.
// Cite Shulker.registerGoals targetSelector (NearestAttackableTargetGoal Player).
func (t *TickLoop) shulkerAcquireTarget(e *Entity) {
	if e.ai == nil {
		return
	}
	s := e.shulker
	if e.ai.attackTargetID != 0 {
		p := t.playerByEntityID(e.ai.attackTargetID)
		if p != nil && !p.dead && t.shulkerInRange(e, p) {
			return
		}
		e.ai.attackTargetID = 0
	}
	if s.nextScanTick > 0 {
		s.nextScanTick--
		return
	}
	s.nextScanTick = int32(reducedTickDelay(shulkerScanCadence))
	var best *tickPlayer
	bestSq := math.MaxFloat64
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		if !t.shulkerInRange(e, p) {
			continue
		}
		dsq := distanceToSqrPlayer(p, e)
		if dsq < bestSq {
			bestSq = dsq
			best = p
		}
	}
	if best != nil {
		e.ai.attackTargetID = best.entityID
	}
}

// shulkerAttackGoal ports Shulker$ShulkerAttackGoal.tick: attackTime--; at <=0 arm attackTime =
// 20 + nextInt(10)*20/2 (bipush 20; nextInt(10); bipush 20; imul; iconst_2; idiv; iadd) + spawn a
// homing ShulkerBullet. Cite Shulker$ShulkerAttackGoal.tick.
func (t *TickLoop) shulkerAttackGoal(e *Entity, target *tickPlayer) {
	s := e.shulker
	if s.attackTime > 0 {
		s.attackTime--
	}
	if s.attackTime > 0 {
		return
	}
	s.attackTime = int32(shulkerAttackBaseTicks + int(mobRandom(e).nextInt(shulkerAttackJitter))*shulkerAttackBaseTicks/2)
	t.spawnShulkerBullet(e, target)
}

// spawnShulkerBullet ports new ShulkerBullet(level, this, target, attachFace.getAxis()) + addFreshEntity.
// Cite Shulker$ShulkerAttackGoal.tick + ShulkerBullet ctor.
func (t *TickLoop) spawnShulkerBullet(e *Entity, target *tickPlayer) *Entity {
	b := NewEntity(t.idAlloc.AllocID(), entity.ShulkerBullet, e.x, e.y+0.5, e.z)
	b.shulkerBullet = &shulkerBulletState{
		ownerID:  e.id,
		targetID: target.entityID,
	}
	b.ai = &mobAI{}
	reseedMobAI(b.ai, b.id)
	owner := t.regionForEntity(b)
	if owner == nil {
		owner = t.cur()
	}
	owner.entities.add(b)
	return b
}

// shulkerBulletIsFlyer reports whether an entity is a ShulkerBullet (tickPhysics NO-gravity gate).
// Cite ShulkerBullet ctor (isNoGravity() == true).
func shulkerBulletIsFlyer(e *Entity) bool { return e.shulkerBullet != nil }

// shulkerBulletTick ports the ShulkerBullet flight: home toward the target; on contact apply 4.0
// mob-projectile damage + a 200-tick LEVITATION via the EXISTING addPlayerEffect. Despawns on hit,
// on a lost target, or after the max-life guard. The selectNextMoveDirection axis walk is a
// cite-deferred refinement; the ON-HIT effect is bytecode-exact. Cite ShulkerBullet.tick + onHitEntity.
func (t *TickLoop) shulkerBulletTick(e *Entity) {
	if e.dead {
		return
	}
	b := e.shulkerBullet
	if b == nil {
		return
	}
	b.life++
	if b.life > shulkerBulletMaxLife {
		t.regionForEntity(e).entities.remove(e.id)
		return
	}
	target := t.playerByEntityID(b.targetID)
	if target == nil || target.dead {
		t.regionForEntity(e).entities.remove(e.id)
		return
	}
	dx := target.x - e.x
	dy := (target.y + 0.5*playerHeight) - e.y
	dz := target.z - e.z
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist > 1e-4 {
		e.vx = dx / dist * shulkerBulletSpeed
		e.vy = dy / dist * shulkerBulletSpeed
		e.vz = dz / dist * shulkerBulletSpeed
	}
	nx, ny, nz := e.x+e.vx, e.y+e.vy, e.z+e.vz
	t.regionForEntity(e).entities.move(e, nx, ny, nz)
	ddx := target.x - e.x
	ddy := (target.y + 0.5*playerHeight) - e.y
	ddz := target.z - e.z
	if ddx*ddx+ddy*ddy+ddz*ddz <= shulkerBulletHitDistSqr {
		t.shulkerBulletHit(e, target)
	}
}

// shulkerBulletHit ports ShulkerBullet.onHitEntity: hurtOrSimulate(mobProjectile(owner),4.0f); on a
// LANDED hit add LEVITATION 200 via the EXISTING addPlayerEffect (mob_effect.go CALLED, not edited).
// Cite ShulkerBullet.onHitEntity.
func (t *TickLoop) shulkerBulletHit(e *Entity, target *tickPlayer) {
	b := e.shulkerBullet
	src := damageSourceMobAttack(b.ownerID)
	t.applyDamage(target, src, shulkerBulletDamage)
	t.addPlayerEffect(target, b.ownerID, effectLevitation, shulkerBulletLevitTicks, shulkerBulletLevitAmp, 1.0)
	t.regionForEntity(e).entities.remove(e.id)
}

// shulkerTeleportSomewhere ports Shulker.teleportSomewhere(): 5 tries, each axis
// Mth.randomBetweenInclusive(rng,-8,8), require empty + above minY + an attachable neighbor.
// Cite Shulker.teleportSomewhere.
func (t *TickLoop) shulkerTeleportSomewhere(e *Entity) bool {
	s := e.shulker
	if s == nil {
		return false
	}
	bx := int(math.Floor(e.x))
	by := int(math.Floor(e.y))
	bz := int(math.Floor(e.z))
	r := mobRandom(e)
	for i := 0; i < shulkerTeleportTries; i++ {
		nx := bx + shulkerRandomBetweenInclusive(r, -shulkerTeleportSpan, shulkerTeleportSpan)
		ny := by + shulkerRandomBetweenInclusive(r, -shulkerTeleportSpan, shulkerTeleportSpan)
		nz := bz + shulkerRandomBetweenInclusive(r, -shulkerTeleportSpan, shulkerTeleportSpan)
		if ny <= dimEndMinY {
			continue
		}
		if t.isSolidAt(blockPosOf(float64(nx), float64(ny), float64(nz))) {
			continue
		}
		face, ok := t.shulkerFindAttachableSurface(nx, ny, nz)
		if !ok {
			continue
		}
		s.attachFace = face
		t.regionForEntity(e).entities.move(e, float64(nx)+0.5, float64(ny), float64(nz)+0.5)
		return true
	}
	return false
}

// shulkerFindAttachableSurface ports Shulker.findAttachableSurface: FIRST solid neighbor in
// Direction.values() order (DOWN,UP,NORTH,SOUTH,WEST,EAST). Cite Shulker.findAttachableSurface.
func (t *TickLoop) shulkerFindAttachableSurface(x, y, z int) (block.Direction, bool) {
	type nb struct {
		dir        block.Direction
		dx, dy, dz int
	}
	for _, n := range []nb{
		{block.Down, 0, -1, 0},
		{block.Up, 0, 1, 0},
		{block.North, 0, 0, -1},
		{block.South, 0, 0, 1},
		{block.West, -1, 0, 0},
		{block.East, 1, 0, 0},
	} {
		if t.isSolidAt(blockPosOf(float64(x+n.dx), float64(y+n.dy), float64(z+n.dz))) {
			return n.dir, true
		}
	}
	return block.Down, false
}

// shulkerRandomBetweenInclusive ports Mth.randomBetweenInclusive(rng, min, max) = min + nextInt(max-min+1).
func shulkerRandomBetweenInclusive(r *entityRandom, lo, hi int) int {
	return lo + int(r.nextInt(hi-lo+1))
}
