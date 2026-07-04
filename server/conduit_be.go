package server

// conduit_be.go — the CONDUIT BLOCK-ENTITY (CONDUIT-01): a 1:1 port of
// net.minecraft.world.level.block.entity.ConduitBlockEntity.serverTick / updateShape / applyEffects /
// updateAndAttackTarget over the 26.2 jar (temp/cache/26.2-inner.jar, CFR/javap this session). The conduit is
// a per-tick DRIVE (the beacon twin, t.conduits keyed by world position) that (a) every 40 game-ticks
// re-scans its activation frame (the 3x3x3 water pocket + the -2..2 prismarine/sea-lantern shell), sets
// isActive when the pocket is all-water and >=16 frame blocks surround it, then (b) when active applies
// CONDUIT_POWER to every in-water/rain player in range and, when the frame is FULL (>=42 blocks), acquires a
// hostile target and deals 4 magic damage to it. The eye-opening BEAM/rotation VISUAL + the ambient/attack
// SOUNDS + the animationTick particle stream are cite-deferred (client cosmetic).
//
// 1:1 jar (net.minecraft.world.level.block.entity.ConduitBlockEntity), VERIFIED CFR static fields:
//
//	static final int BLOCK_REFRESH_RATE = 2;   // (the vanilla-doc value; the ACTUAL scan gate is %40 below)
//	static final int EFFECT_DURATION = 13;     // seconds -> 13*20 == 260 tick effect duration
//	static final float ROTATION_SPEED = -0.0375f;
//	static final int MIN_ACTIVE_SIZE = 16;     // updateShape returns size >= 16
//	static final int MIN_KILL_SIZE = 42;       // updateHunting/updateAndAttackTarget gate size >= 42
//	static final int KILL_RANGE = 8;           // selectNewTarget/updateDestroyTarget AABB inflate + closerThan
//	static final Block[] VALID_BLOCKS = { PRISMARINE, PRISMARINE_BRICKS, SEA_LANTERN, DARK_PRISMARINE };
//
//	serverTick(level, pos, state, entity):
//	   entity.tickCount++;
//	   long gt = level.getGameTime();
//	   List<BlockPos> ebs = entity.effectBlocks;
//	   if (gt % 40L == 0L) {
//	       boolean nowActive = updateShape(level, pos, ebs);
//	       // (play CONDUIT_ACTIVATE/DEACTIVATE on a change — deferred sound)
//	       entity.isActive = nowActive;
//	       updateHunting(entity, ebs);              // isHunting = ebs.size() >= 42
//	       if (nowActive) {
//	           applyEffects(level, pos, ebs);
//	           updateAndAttackTarget((ServerLevel) level, pos, state, entity, ebs.size() >= 42);
//	       }
//	   }
//	   if (entity.isActive()) { /* %80 ambient sound + short-ambient RNG scheduler — deferred */ }
//
// The %40 scan gate + the isActive/isHunting/effectBlocks state machine + the >=16/>=42 thresholds are
// preserved so the activation/attack timing matches vanilla tick-for-tick.

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// Conduit constants (ConduitBlockEntity static fields, VERIFIED CFR).
const (
	conduitScanCadenceTicks = 40  // serverTick's `gt % 40L == 0L` updateShape/applyEffects/attack gate
	conduitEffectDuration   = 260 // EFFECT_DURATION(13) * 20 — the MobEffectInstance(CONDUIT_POWER, 260, 0, true, true) duration
	conduitMinActiveSize    = 16  // MIN_ACTIVE_SIZE — updateShape returns effectBlocks.size() >= 16
	conduitMinKillSize      = 42  // MIN_KILL_SIZE — updateHunting/attack gate effectBlocks.size() >= 42
	conduitKillRange        = 8   // KILL_RANGE — the destroy-target AABB inflate + the closerThan(target, 8) leash
)

// conduitValidBlocks is ConduitBlockEntity.VALID_BLOCKS: the four frame blocks updateShape counts in the
// -2..2 shell. Order mirrors the jar array (PRISMARINE, PRISMARINE_BRICKS, SEA_LANTERN, DARK_PRISMARINE).
// A cell matching ANY of these adds to effectBlocks. CITE updateShape's `for (Block type : VALID_BLOCKS)`.
var conduitValidBlocks = [4]string{
	"minecraft:prismarine",
	"minecraft:prismarine_bricks",
	"minecraft:sea_lantern",
	"minecraft:dark_prismarine",
}

// conduitBE is the tick-owned state of one conduit block-entity — the Go analogue of ConduitBlockEntity
// narrowed to the fields serverTick reads/writes. The activeRotation / animation float + the beam sections
// are cite-deferred (visual). effectBlocks is recomputed each scan, so it is not held long-lived; only the
// count-derived isActive/isHunting + the destroyTarget leash persist between scans.
type conduitBE struct {
	tickCount int // ConduitBlockEntity.tickCount — incremented every serverTick (used only by the deferred animation)

	isActive  bool // ConduitBlockEntity.isActive  — committed from updateShape on each %40 scan
	isHunting bool // ConduitBlockEntity.isHunting — committed from updateHunting (size >= 42) on each %40 scan

	// destroyTarget is ConduitBlockEntity.destroyTarget: the EntityReference<LivingEntity> the conduit is
	// currently attacking, reduced to the target's entity id (0 == null, the Folia thin-id rule — never a live
	// *Entity pointer). updateDestroyTarget keeps it while the target is alive+within 8, else re-selects.
	destroyTarget int32
	// destroyTargetSet distinguishes "no target" (destroyTarget==0, EntityReference null) from a genuine
	// target whose id happens to be 0. Vanilla uses a nullable reference; this pair is the faithful analogue.
	destroyTargetSet bool
}

// conduitServerTick ports ConduitBlockEntity.serverTick(level, pos, state, entity) EXACTLY: the tickCount
// increment, the every-40-tick frame re-scan (updateShape) + isActive/isHunting commit, and — when active —
// applyEffects + updateAndAttackTarget. Runs on the tick goroutine. The ambient-sound branch (isActive %80
// + the short-ambient RNG scheduler) is cite-deferred (no BE sound-event emit seam in v1). The activate/
// deactivate sound on an isActive change is likewise deferred; the boolean transition itself is preserved.
//
// 1:1 net.minecraft.world.level.block.entity.ConduitBlockEntity.serverTick
func (t *TickLoop) conduitServerTick(pos pk.Position, c *conduitBE) {
	if t.world() == nil {
		return
	}

	// entity.tickCount++ (bytecode 0-7). Used only by the deferred animationTick; incremented for fidelity.
	c.tickCount++

	// long gt = level.getGameTime(); if (gt % 40L == 0L) { ... }
	if t.GameTime()%int64(conduitScanCadenceTicks) != 0 {
		// The %40 branch is the ONLY server-side work; the remaining serverTick body is the isActive ambient
		// sound (deferred). Nothing else runs off-boundary.
		return
	}

	// boolean nowActive = updateShape(level, pos, effectBlocks);
	effectBlocks := t.conduitUpdateShape(pos)
	nowActive := len(effectBlocks) >= conduitMinActiveSize

	// (nowActive != entity.isActive) -> play CONDUIT_ACTIVATE / CONDUIT_DEACTIVATE: cite-deferred sound.
	// entity.isActive = nowActive;
	c.isActive = nowActive

	// updateHunting(entity, effectBlocks): entity.setHunting(effectBlocks.size() >= 42).
	c.isHunting = len(effectBlocks) >= conduitMinKillSize

	// if (nowActive) { applyEffects(...); updateAndAttackTarget(serverLevel, pos, state, entity, size>=42); }
	if nowActive {
		t.conduitApplyEffects(pos, len(effectBlocks))
		t.conduitUpdateAndAttackTarget(pos, c, len(effectBlocks) >= conduitMinKillSize)
	}
	// The isActive() ambient-sound branch (bytecode 131-210) is cite-deferred (client cosmetic sound).
}

// conduitUpdateShape ports ConduitBlockEntity.updateShape(level, pos, effectBlocks): first the inner 3x3x3
// water pocket (-1..1 on each axis) must be ENTIRELY water (a non-water cell fails the scan immediately,
// returning an empty list). Then the -2..2 shell is walked; a cell is a FRAME position iff it is NOT inside
// the inner 3x3 cube AND it lies on the correct edge/face per the jar's offset predicate; each frame cell
// that is a VALID_BLOCKS block is added. The returned slice's length is the activatingBlocks count (the
// caller applies the >=16 active gate exactly as `return effectBlocks.size() >= 16`).
//
// 1:1 net.minecraft.world.level.block.entity.ConduitBlockEntity.updateShape
func (t *TickLoop) conduitUpdateShape(pos pk.Position) []pk.Position {
	// effectBlocks.clear();
	var effectBlocks []pk.Position

	// The 3x3x3 water pocket: for (ox=-1..1) for (oy=-1..1) for (oz=-1..1) if (!isWaterAt(pos+offset)) return
	// (empty). Every one of the 27 surrounding cells (including the conduit's own cell) must be water.
	for ox := -1; ox <= 1; ox++ {
		for oy := -1; oy <= 1; oy++ {
			for oz := -1; oz <= 1; oz++ {
				if !t.conduitIsWaterAt(pos.X+ox, pos.Y+oy, pos.Z+oz) {
					return nil // level.isWaterAt(testPos) == false -> the pocket is broken; no active shape.
				}
			}
		}
	}

	// The -2..2 frame shell: for (ox=-2..2) for (oy=-2..2) for (oz=-2..2) { skip the inner cube + the non-
	// frame positions per the predicate; add any VALID_BLOCKS cell }.
	for ox := -2; ox <= 2; ox++ {
		for oy := -2; oy <= 2; oy++ {
			for oz := -2; oz <= 2; oz++ {
				ax := absInt(ox)
				ay := absInt(oy)
				az := absInt(oz)
				// `if (ax<=1 && ay<=1 && az<=1 || (ox!=0 || ay!=2 && az!=2) && (oy!=0 || ax!=2 && az!=2) &&
				//     (oz!=0 || ax!=2 && ay!=2)) continue;`
				// The FIRST disjunct skips the inner 3x3x3 cube (already water-checked). The remaining
				// conjunction is TRUE for every cell that is NOT one of the 12 edge-midpoint frame positions
				// (the ring of prismarine that forms the conduit frame); such cells are skipped too. Only the
				// 12 frame edge-cells (ox==0 with ay==2&&az==2, etc.) survive `continue`.
				if (ax <= 1 && ay <= 1 && az <= 1) ||
					((ox != 0 || (ay != 2 && az != 2)) &&
						(oy != 0 || (ax != 2 && az != 2)) &&
						(oz != 0 || (ax != 2 && ay != 2))) {
					continue
				}
				testPos := pk.Position{X: pos.X + ox, Y: pos.Y + oy, Z: pos.Z + oz}
				testBlock, ok := t.world().GetBlock(testPos, dimMinY)
				if !ok {
					continue
				}
				// for (Block type : VALID_BLOCKS) if (testBlock.is(type)) effectBlocks.add(testPos);
				// The jar loop has NO break: a state can only match one block, so at most one add per cell —
				// but the loop structure is preserved (a match adds and keeps checking the rest, all misses).
				name := block.StateList[testBlock].ID()
				for _, valid := range conduitValidBlocks {
					if name == valid { // BlockState.is(Block) == StateList[state].ID() == block.ID()
						effectBlocks = append(effectBlocks, testPos)
					}
				}
			}
		}
	}
	// return effectBlocks.size() >= 16 — the caller applies the >=16 gate (nowActive); this returns the raw
	// list so applyEffects/updateHunting can read the exact count.
	return effectBlocks
}

// conduitIsWaterAt ports Level.isWaterAt(pos) == getFluidState(pos).is(FluidTags.WATER) for the conduit's
// activation-frame pocket scan: a cell is water iff it is a water block OR a WATERLOGGED block
// (SimpleWaterloggedBlock.getFluidState returns Fluids.WATER.getSource). The waterlogged branch is
// load-bearing here because the conduit's OWN cell (offset 0,0,0) is a waterlogged conduit block, and every
// surrounding cell may hold a waterlogged block (stairs/slabs) that vanilla counts as water. The shared
// t.isWaterAt (stroll) reads only true water blocks (getFluidState of a plain block), so the conduit uses
// this fuller port. CITE Level.isWaterAt + SimpleWaterloggedBlock.getFluidState.
func (t *TickLoop) conduitIsWaterAt(bx, by, bz int) bool {
	if t.world() == nil {
		return false
	}
	pos := pk.Position{X: bx, Y: by, Z: bz}
	s, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return false
	}
	if decodeFluid(s).isWater {
		return true // a water block: getFluidState is WATER.
	}
	return block.IsWaterloggedState(s) // a waterlogged block: getFluidState is WATER too.
}

// conduitApplyEffects ports ConduitBlockEntity.applyEffects(level, pos, effectBlocks): compute the effect
// range from the frame size, gather every Player in the range AABB, and grant CONDUIT_POWER (duration 260,
// amplifier 0) to each one that is within `effectRange` blocks (integer closerThan) AND isInWaterOrRain.
//
//	int activeSize = effectBlocks.size();
//	int effectRange = activeSize / 7 * 16;
//	AABB bb = new AABB(x,y,z, x+1,y+1,z+1).inflate(effectRange).expandTowards(0, level.getHeight(), 0);
//	List<Player> players = level.getEntitiesOfClass(Player.class, bb);
//	if (players.isEmpty()) return;
//	for (Player p : players)
//	    if (worldPosition.closerThan(p.blockPosition(), effectRange) && p.isInWaterOrRain())
//	        p.addEffect(new MobEffectInstance(CONDUIT_POWER, 260, 0, true, true));
//
// 1:1 net.minecraft.world.level.block.entity.ConduitBlockEntity.applyEffects
func (t *TickLoop) conduitApplyEffects(pos pk.Position, activeSize int) {
	// int effectRange = activeSize / 7 * 16; — INTEGER division then multiply (16..42 -> 16, 42 -> 96... but
	// activeSize maxes at 48 frame cells; e.g. 16/7*16 == 2*16 == 32; 42/7*16 == 6*16 == 96).
	effectRange := activeSize / 7 * 16

	// AABB(x,y,z, x+1,y+1,z+1).inflate(effectRange).expandTowards(0, getHeight(), 0): the 1-block conduit cube
	// inflated by effectRange on all faces, then extended UP by the world height. getHeight() == the build
	// height span (dimMinY..dimMinY+384); expandTowards(0,+h,0) raises maxY by h. The beacon uses the same
	// broad-phase; reuse its player-AABB gather with the conduit's bounds.
	rng := float64(effectRange)
	minX := float64(pos.X) - rng
	minY := float64(pos.Y) - rng
	minZ := float64(pos.Z) - rng
	maxX := float64(pos.X) + 1 + rng
	maxY := float64(pos.Y) + 1 + rng + beaconWorldHeight // expandTowards(0, getHeight(), 0)
	maxZ := float64(pos.Z) + 1 + rng

	players := t.beaconPlayersInAABB(minX, minY, minZ, maxX, maxY, maxZ)
	// if (players.isEmpty()) return; — a no-op fast path (the loop below is already empty, but preserve it).
	if len(players) == 0 {
		return
	}
	rangeSq := float64(effectRange) * float64(effectRange) // Mth.square(effectRange) for closerThan
	for _, p := range players {
		// worldPosition.closerThan(p.blockPosition(), effectRange): distSqr(conduitPos, floor(playerPos)) <
		// effectRange². blockPosition() floors the player's feet position (BlockPos.containing).
		pbx := floorInt(p.x)
		pby := floorInt(p.y)
		pbz := floorInt(p.z)
		dx := float64(pos.X - pbx)
		dy := float64(pos.Y - pby)
		dz := float64(pos.Z - pbz)
		if dx*dx+dy*dy+dz*dz >= rangeSq {
			continue // !closerThan -> skip
		}
		if !t.playerIsInWaterOrRain(p) {
			continue // !isInWaterOrRain -> skip
		}
		// p.addEffect(new MobEffectInstance(CONDUIT_POWER, 260, 0, ambient=true, showParticles=true)).
		t.addPlayerEffect(p, 0, effectConduitPower, conduitEffectDuration, 0, 1.0)
	}
}

// conduitUpdateAndAttackTarget ports ConduitBlockEntity.updateAndAttackTarget(serverLevel, pos, state,
// entity, isActive): resolve/keep the destroy target (updateDestroyTarget), and if one is live deal 4.0
// magic damage to it. The `isActive` argument is `effectBlocks.size() >= 42` (a full frame is required to
// hunt). The CONDUIT_ATTACK_TARGET sound + the sendBlockUpdated on a target change are cite-deferred (sound
// / block-update packet are cosmetic; the target leash + the 4-damage hit are the observable gameplay).
//
// 1:1 net.minecraft.world.level.block.entity.ConduitBlockEntity.updateAndAttackTarget
func (t *TickLoop) conduitUpdateAndAttackTarget(pos pk.Position, c *conduitBE, huntActive bool) {
	// EntityReference<LivingEntity> newTarget = updateDestroyTarget(entity.destroyTarget, level, pos, isActive);
	newTargetID, newTargetSet := t.conduitUpdateDestroyTarget(pos, c, huntActive)

	// LivingEntity targetEntity = EntityReference.getLivingEntity(newTarget, level);
	var target *Entity
	if newTargetSet {
		target = t.conduitLivingByID(newTargetID)
	}
	if target != nil {
		// level.playSound(null, tx, ty, tz, CONDUIT_ATTACK_TARGET, BLOCKS, 1, 1): cite-deferred sound.
		// targetEntity.hurtServer(level, level.damageSources().magic(), 4.0f).
		t.applyDamageEntity(target, damageSourceMagic(), 4.0)
	}

	// if (!Objects.equals(newTarget, entity.destroyTarget)) { entity.destroyTarget = newTarget;
	//     level.sendBlockUpdated(pos, state, state, 2); }
	if newTargetSet != c.destroyTargetSet || newTargetID != c.destroyTarget {
		c.destroyTarget = newTargetID
		c.destroyTargetSet = newTargetSet
		// sendBlockUpdated(pos, state, state, 2): cite-deferred (the destroyTarget is a render sync only).
	}
}

// conduitUpdateDestroyTarget ports ConduitBlockEntity.updateDestroyTarget(target, level, pos, isActive):
//
//	if (!isActive)         return null;                       // a non-full frame never hunts
//	if (target == null)    return selectNewTarget(level, pos);
//	LivingEntity te = EntityReference.getLivingEntity(target, level);
//	if (te == null || !te.isAlive() || !pos.closerThan(te.blockPosition(), 8.0)) return null; // target lost
//	return target;                                           // keep the current target
//
// Returns (id, set) where set==false is the null EntityReference.
//
// 1:1 net.minecraft.world.level.block.entity.ConduitBlockEntity.updateDestroyTarget
func (t *TickLoop) conduitUpdateDestroyTarget(pos pk.Position, c *conduitBE, huntActive bool) (int32, bool) {
	if !huntActive {
		return 0, false // !isActive -> null
	}
	if !c.destroyTargetSet {
		return t.conduitSelectNewTarget(pos) // target == null -> selectNewTarget
	}
	te := t.conduitLivingByID(c.destroyTarget)
	// te == null || !te.isAlive() || !pos.closerThan(te.blockPosition(), 8.0) -> the leash broke; null.
	if te == nil || te.dead || te.health <= 0 {
		return 0, false
	}
	tbx := floorInt(te.x)
	tby := floorInt(te.y)
	tbz := floorInt(te.z)
	dx := float64(pos.X - tbx)
	dy := float64(pos.Y - tby)
	dz := float64(pos.Z - tbz)
	if dx*dx+dy*dy+dz*dz >= float64(conduitKillRange)*float64(conduitKillRange) {
		return 0, false // !closerThan(8) -> the target left range; drop it (re-select next tick).
	}
	return c.destroyTarget, true // keep the current target
}

// conduitSelectNewTarget ports ConduitBlockEntity.selectNewTarget(level, pos):
//
//	List<LivingEntity> candidates = level.getEntitiesOfClass(LivingEntity.class,
//	    getDestroyRangeAABB(pos)  // AABB(pos).inflate(8.0)
//	    , input -> input instanceof Enemy && input.isInWaterOrRain());
//	if (candidates.isEmpty()) return null;
//	return EntityReference.of(Util.getRandom(candidates, level.getRandom()));
//
// The Enemy filter is the MobCategory.MONSTER proxy (categoryOf == categoryMonster) already used by the
// IronGolem hostile-mob target goal (ai_goals_target.go). Util.getRandom(list, random) ==
// list.get(random.nextInt(list.size())) — the ONE conduit RNG draw, taken on the shared level/world RNG
// (cur().levelRandom), NOT any mob's per-entity RNG (so the pig oracle stream is unperturbed).
//
// 1:1 net.minecraft.world.level.block.entity.ConduitBlockEntity.selectNewTarget
func (t *TickLoop) conduitSelectNewTarget(pos pk.Position) (int32, bool) {
	// getDestroyRangeAABB(pos) == new AABB(pos).inflate(8.0): the 1-block conduit cube inflated by 8 on all
	// faces -> [x-8, x+9] × [y-8, y+9] × [z-8, z+9].
	minX := float64(pos.X) - float64(conduitKillRange)
	minY := float64(pos.Y) - float64(conduitKillRange)
	minZ := float64(pos.Z) - float64(conduitKillRange)
	maxX := float64(pos.X) + 1 + float64(conduitKillRange)
	maxY := float64(pos.Y) + 1 + float64(conduitKillRange)
	maxZ := float64(pos.Z) + 1 + float64(conduitKillRange)

	var candidates []*Entity
	if t.cur() != nil {
		// getEntitiesOfClass(LivingEntity, aabb, predicate): scan the entity store's chunk buckets around the
		// conduit. The 17-block-wide AABB spans at most 2 chunks each way; near() with a 1-chunk radius plus
		// the exact AABB intersect below covers it. The predicate is `Enemy && isInWaterOrRain`.
		for _, e := range t.cur().entities.near(float64(pos.X), float64(pos.Z), 2) {
			if e == nil || e.dead || e.health <= 0 {
				continue
			}
			if categoryOf(e.typ) != categoryMonster { // input instanceof Enemy (Monster-category proxy)
				continue
			}
			// AABB.intersects over the entity's hitbox (feet at e.y, width/height from its type).
			hw := e.width / 2
			eMinX, eMaxX := e.x-hw, e.x+hw
			eMinY, eMaxY := e.y, e.y+e.height
			eMinZ, eMaxZ := e.z-hw, e.z+hw
			if !(minX < eMaxX && maxX > eMinX && minY < eMaxY && maxY > eMinY && minZ < eMaxZ && maxZ > eMinZ) {
				continue
			}
			if !t.entityIsInWaterOrRain(e) { // input.isInWaterOrRain()
				continue
			}
			candidates = append(candidates, e)
		}
	}
	if len(candidates) == 0 {
		return 0, false // candidates.isEmpty() -> null
	}
	// Util.getRandom(candidates, level.getRandom()) == candidates.get(random.nextInt(size)).
	idx := 0
	if len(candidates) > 1 && t.cur() != nil && t.cur().levelRandom != nil {
		idx = int(t.cur().levelRandom.NextIntN(int32(len(candidates))))
	}
	return candidates[idx].id, true
}

// conduitLivingByID resolves a target entity id to its live *Entity (the EntityReference.getLivingEntity
// analogue), or nil if it is gone. Tick-owned (reads the region entity store).
func (t *TickLoop) conduitLivingByID(id int32) *Entity {
	if id == 0 || t.cur() == nil {
		return nil
	}
	return t.cur().entities.byID[id]
}

// playerIsInWaterOrRain ports Entity.isInWaterOrRain() for a player: isInWater() || isInRain(). isInWater is
// playerInWater (the hitbox water scan); isInRain is level.isRainingAt(blockPos) || isRainingAt(blockPos at
// bbMaxY) — the open-sky precipitation gate at the player's feet and at the top of its bounding box. CITE
// Entity.isInWaterOrRain / isInRain.
func (t *TickLoop) playerIsInWaterOrRain(p *tickPlayer) bool {
	if t.playerInWater(p) {
		return true
	}
	// isInRain: BlockPos pos = blockPosition(); level.isRainingAt(pos) || level.isRainingAt(x, bbMaxY, z).
	feet := pk.Position{X: floorInt(p.x), Y: floorInt(p.y), Z: floorInt(p.z)}
	if t.isRainingAt(feet) {
		return true
	}
	top := pk.Position{X: floorInt(p.x), Y: floorInt(p.y + playerHeight), Z: floorInt(p.z)}
	return t.isRainingAt(top)
}

// entityIsInWaterOrRain ports Entity.isInWaterOrRain() for a mob: isInWater() || isInRain(). isInWater is
// entityInWater (the feet-cell water read); isInRain is the same open-sky precipitation gate at the mob's
// feet and at its bounding-box top. CITE Entity.isInWaterOrRain / isInRain.
func (t *TickLoop) entityIsInWaterOrRain(e *Entity) bool {
	if t.entityInWater(e) {
		return true
	}
	feet := pk.Position{X: floorInt(e.x), Y: floorInt(e.y), Z: floorInt(e.z)}
	if t.isRainingAt(feet) {
		return true
	}
	top := pk.Position{X: floorInt(e.x), Y: floorInt(e.y + e.height), Z: floorInt(e.z)}
	return t.isRainingAt(top)
}

// tickConduits ticks every live conduit block-entity once per tick (the ServerLevel-side blockEntityTicker
// fan-out for ConduitBlockEntity.serverTick). Called from tickWorld (the tickBeacons twin). Each conduit
// reads the block at its position; if it is no longer a conduit (broken/replaced/unloaded), the block-entity
// is dropped from the store. A nil world leaves conduits un-ticked (tests may drive conduitServerTick
// directly). Tick-owned (CONDUIT-01).
func (t *TickLoop) tickConduits() {
	if len(t.conduits) == 0 {
		return
	}
	w := t.world()
	if w == nil {
		return
	}
	for pos, c := range t.conduits {
		state, ok := w.GetBlock(pos, dimMinY)
		if !ok || !isConduitBlock(state) {
			delete(t.conduits, pos)
			continue
		}
		t.conduitServerTick(pos, c)
	}
}

// resolveConduit returns the tick-owned conduitBE for pos, creating an EMPTY one on first access — the
// analogue of a freshly-placed conduit's default ConduitBlockEntity (inactive, no target). Returns nil only
// when pos is not a conduit block (or the world is unloaded). Tick-owned (t.conduits, the t.beacons twin).
func (t *TickLoop) resolveConduit(pos pk.Position) *conduitBE {
	if t.conduits == nil {
		t.conduits = make(map[pk.Position]*conduitBE)
	}
	if c, ok := t.conduits[pos]; ok {
		return c
	}
	if t.world() == nil {
		return nil
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !isConduitBlock(state) {
		return nil
	}
	c := &conduitBE{}
	t.conduits[pos] = c
	return c
}
