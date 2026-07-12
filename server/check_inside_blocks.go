package server

// check_inside_blocks.go -- the Entity.checkInsideBlocks / BlockState.entityInside dispatch, a 1:1 port
// of vanilla Java 26.2 (protocol 776), verified method-for-method against temp/cache/26.2-inner.jar via
// javap -c -p this session (no GPL paste). It closes Fable audit #6: without this dispatch the blocks an
// entity STANDS IN never fired their entityInside behavior (cactus contact damage, cobweb slow, sweet-
// berry slow+damage, wither-rose effect, tripwire trigger).
//
// VANILLA DISPATCH (net.minecraft.world.entity.Entity.checkInsideBlocks, 26.2):
//   - Entity.move calls checkInsideBlocks(List<Movement>, applier) at the end of a successful move.
//   - The stepped variant iterates the movement segments; the leaf checkInsideBlocks(from,to,...) deflates
//     makeBoundingBox(to) by 1.0E-5 and calls BlockGetter.forEachBlockIntersectedBetween(from,to,box,visit).
//   - The visit lambda (lambda$checkInsideBlocks$0), for each visited BlockPos:
//       * getBlockState(pos); if isAir -> skip (still counts as a step).
//       * shape = state.getEntityInsideCollisionShape(level,pos,entity)  [default == Shapes.block()].
//       * intersectsShape = (shape == Shapes.block()) ? true
//                           : collidedWithShapeMovingFrom(from,to, shape.move(pos).toAabbs()).
//       * if (intersectsShape || collidedWithFluid) visitedBlocks.add(pos.asLong()) (dedup within a move).
//       * if (intersectsShape) { applier.advanceStep(step); state.entityInside(level,pos,entity,applier,
//         isInside); onInsideBlock(state); }  where isInside = flag || box.intersects(pos).
//
// SULFUR MODEL (the faithful reduction for a grid-aligned, sub-block-per-tick server):
//   - A mob/player moves a small fraction of a block per tick, so the multi-segment STEP machinery and the
//     collidedWithShapeMovingFrom swept test collapse to: "for each block position the deflated final AABB
//     overlaps, if the block entity-inside-collision-shape intersects that AABB, call entityInside". Of
//     the ported blocks (cactus / cobweb / sweet_berry_bush / wither_rose / tripwire) NONE override
//     getEntityInsideCollisionShape -- all use the default Shapes.block(), so intersectsShape is true iff
//     the deflated AABB overlaps the block cell (the visit lambda shape==Shapes.block() fast path). This
//     is byte-observably identical for these blocks; the swept-shape test only matters for partial-shape
//     inside-blocks (none ported here). The 1.0E-5 deflate is applied so a mob merely STANDING ON a block
//     (its box bottom flush with the top face) is not treated as inside the block below.
//   - POWDER_SNOW is deliberately NOT dispatched here: it is the ONE ported target that overrides
//     getEntityInsideCollisionShape, and freeze.go tickEntityFreeze already applies its FREEZE effect via
//     the entityInsidePowderSnow proxy. Routing it through here too would DOUBLE-apply the frost. Cited.
//   - LAVA is likewise already handled (tickEntityLava / tickLavaPlayers, the LavaFluid.entityInside path).
//
// PIG ORACLE (CRITICAL): TestPluginPigEqualsGoNativePig drives goPig.ai.serverAiStep DIRECTLY and never
// runs the tickAI per-entity loop where checkInsideBlocks is wired, so the dispatch is NEVER INVOKED in
// that test -- structurally byte-identical. Independently, checkInsideBlocks is a pure block-id scan with
// NO RandomSource draw on any path (cactus hurt, cobweb/berry makeStuckInBlock, wither addEffect, tripwire
// checkPressed are all RNG-free), and the oracle pig stands on grass over air: every block its AABB
// overlaps is air (skipped) or a non-effect block (entityInsideBlock type switch falls through), so even
// if it DID run, zero effect fires and zero draws happen. The moveEntity stuck-speed consumption is
// e.stuck-gated: an un-armed entity (never in cobweb/berry) skips it, so serverAiStep -> moveEntity is
// byte-identical for the oracle pig.

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// checkInsideBlocksDeflate is the AABB.deflate amount Entity.checkInsideBlocks applies before scanning
// (ldc2_w 9.999999747378752E-6 == the float 1.0E-5 widened to double). Shrinking the box on all sides by
// this epsilon means a box whose face is flush with a block boundary does NOT count that neighboring cell.
//
//	[VERIFIED javap Entity.checkInsideBlocks(Vec3,Vec3,...): makeBoundingBox(to).deflate(9.999999747378752E-6).]
const checkInsideBlocksDeflate = 9.999999747378752e-6

// checkInsideBlocks ports Entity.checkInsideBlocks for a store Entity: iterate the block positions the
// entity deflated bounding box overlaps and, for each, dispatch BlockState.entityInside. isAffectedByBlocks
// gates the whole thing (Entity.isAffectedByBlocks: !isRemoved() && !noPhysics); a dead/removed entity is a
// no-op. Air cells are skipped. Runs in the tickAI per-entity phase, AFTER serverAiStep (so it observes this
// tick post-move position -- vanilla runs it inside Entity.move at the position the move landed).
//
//	[VERIFIED javap Entity.checkInsideBlocks: isAffectedByBlocks() ifeq return; then the per-block visit.]
func (t *TickLoop) checkInsideBlocks(e *Entity) {
	if e == nil || e.dead || t.world() == nil {
		return // isAffectedByBlocks: !isRemoved() (a dead/removed entity is not affected by blocks).
	}
	// The deflated AABB (Entity.checkInsideBlocks: makeBoundingBox(to).deflate(1e-5)).
	hw := e.width / 2
	minX := e.x - hw + checkInsideBlocksDeflate
	minY := e.y + checkInsideBlocksDeflate
	minZ := e.z - hw + checkInsideBlocksDeflate
	maxX := e.x + hw - checkInsideBlocksDeflate
	maxY := e.y + e.height - checkInsideBlocksDeflate
	maxZ := e.z + hw - checkInsideBlocksDeflate

	loX, hiX := int(math.Floor(minX)), int(math.Floor(maxX))
	loY, hiY := int(math.Floor(minY)), int(math.Floor(maxY))
	loZ, hiZ := int(math.Floor(minZ)), int(math.Floor(maxZ))

	for bx := loX; bx <= hiX; bx++ {
		for by := loY; by <= hiY; by++ {
			for bz := loZ; bz <= hiZ; bz++ {
				pos := pk.Position{X: bx, Y: by, Z: bz}
				state, ok := t.world().GetBlock(pos, dimMinY)
				if !ok {
					continue // unloaded == treated as air (no false effects)
				}
				id := block.StateList[state].ID()
				if id == "minecraft:air" || id == "minecraft:cave_air" || id == "minecraft:void_air" {
					continue // isAir() -> skip (the visit lambda air branch)
				}
				// intersectsShape: for every ported target block getEntityInsideCollisionShape is the
				// default Shapes.block(), so the visit lambda shape == Shapes.block() fast path yields
				// true whenever the deflated AABB overlaps the cell -- which it does by construction here
				// (we only enumerate overlapped cells). Blocks with a partial inside-shape (none ported)
				// would need the swept collidedWithShapeMovingFrom test; cited-deferred.
				t.entityInsideBlock(state, pos, e)
			}
		}
	}
}

// entityInsideBlock is the per-block BlockState.entityInside dispatch: a type switch over the block whose
// cell the entity overlaps, routing to the 1:1-ported override. A block with no entityInside override (the
// vast majority, incl. grass/dirt/stone the oracle pig stands on) falls through -- a no-op, zero RNG. This
// is the Go analogue of the virtual BlockState.entityInside call (Block subclasses override it; Block base
// is empty).
//
//	[VERIFIED javap Block.entityInside: empty body (return). Only the subclasses below override it.]
func (t *TickLoop) entityInsideBlock(state block.StateID, pos pk.Position, e *Entity) {
	switch b := block.StateList[state].(type) {
	case block.Cactus:
		t.cactusEntityInside(e)
	case block.Cobweb:
		t.cobwebEntityInside(state, e)
	case block.SweetBerryBush:
		t.sweetBerryBushEntityInside(state, b, e)
	case block.WitherRose:
		t.witherRoseEntityInside(e)
	case block.Tripwire:
		t.tripwireEntityInside(state, pos)
	}
}

// cactusEntityInside ports CactusBlock.entityInside 1:1: entity.hurt(damageSources().cactus(), 1.0F).
// UNCONDITIONAL -- no living gate, no difficulty gate, no server gate (hurt is the client-authoritative
// wrapper that on the server routes to hurtServer). 1.0F contact damage per tick the entity overlaps a
// cactus cell. RNG-free.
//
//	[VERIFIED javap CactusBlock.entityInside: aload entity; damageSources().cactus(); fconst_1;
//	 invokevirtual Entity.hurt(DamageSource, F).]
func (t *TickLoop) cactusEntityInside(e *Entity) {
	// entity.hurt(cactus, 1.0F) dispatches to the entity's own hurtServer override. A dropped Item routes
	// through ItemEntity.hurtServer (decrement the item's health, discard at <=0) -- NOT the LivingEntity
	// damage pipeline -- so cactus DESTROYS an item over 5 ticks rather than "damaging" a nonexistent
	// living health. Every living mob takes the shared LivingEntity.hurtServer path (applyDamageEntity).
	if e.isItem {
		t.hurtItem(e, 1.0)
		return
	}
	t.applyDamageEntity(e, damageSourceOf(damageTypeCactus), 1.0)
}

// hurtItem ports net.minecraft.world.entity.item.ItemEntity.hurtServer's health path 1:1: subtract the
// damage from the item's health (v1: ADD to itemDamageTaken, since we count up from 0 == full 5) and, once
// health <= 0 (itemDamageTaken >= itemStartHealth), discard the item. The isInvulnerableToBase, MOB_GRIEFING
// (attacker-is-Mob) and ItemStack.canBeHurtBy gates are v1 cited constants: no source here is a mob attacker
// (cactus is an environment source) and canBeHurtBy is constant-true until DAMAGE_RESISTANT components are
// wired -- so a normal stack takes the health hit exactly as vanilla. RNG-free.
//
//	[VERIFIED javap ItemEntity.hurtServer: health = (int)((float)health - amount); if (health <= 0) {
//	 getItem().onDestroyed(this); discard(); }. ItemEntity.<init> health = 5.]
func (t *TickLoop) hurtItem(e *Entity, amount float32) {
	if e == nil || !e.isItem {
		return
	}
	// health = (int)((float)health - amount): decrement the remaining health; with integer amounts (cactus
	// 1.0F) this is a plain -1 per hit. Encoded as itemDamageTaken += amount (count up from 0).
	e.itemDamageTaken += int(amount)
	if e.itemStartHealth()-e.itemDamageTaken <= 0 {
		// getItem().onDestroyed(this) (particle/sound side-effect, no drop) then discard(): remove the item
		// from the store so the tracker emits RemoveEntities next tick.
		t.cur().entities.remove(e.id)
	}
}

// cobwebEntityInside ports WebBlock.entityInside 1:1: makeStuckInBlock(state, Vec3(0.25, 0.05, 0.25)).
// The WEAVING-effect branch (LivingEntity with MobEffects.WEAVING -> Vec3(0.5,0.25,0.5)) is honored via
// the mobEffects read (v1 mobs almost never carry WEAVING, but the branch is faithful). makeStuckInBlock:
// resetFallDistance(); stuckSpeedMultiplier = v (consumed at the START of the entity NEXT move). RNG-free.
//
//	[VERIFIED javap WebBlock.entityInside: Vec3 v = new Vec3(0.25, 0.05, 0.25); if (entity instanceof
//	 LivingEntity le && le.hasEffect(MobEffects.WEAVING)) v = new Vec3(0.5, 0.25, 0.5);
//	 entity.makeStuckInBlock(state, v).]
func (t *TickLoop) cobwebEntityInside(_ block.StateID, e *Entity) {
	vx, vy, vz := 0.25, 0.05000000074505806, 0.25
	// MobEffects.WEAVING == minecraft:weaving (no effectWeaving const yet; the literal id matches
	// potion_brewing.go's "weaving" reference). A LivingEntity carrying WEAVING gets the wider slow.
	if isLivingMob(e) && entityHasEffect(e, "minecraft:weaving") {
		vx, vy, vz = 0.5, 0.25, 0.5
	}
	makeStuckInBlock(e, vx, vy, vz)
}

// sweetBerryBushEntityInside ports SweetBerryBushBlock.entityInside 1:1:
//
//	if (!(entity instanceof LivingEntity) || entity.is(FOX) || entity.is(BEE)) return;   // exempt
//	entity.makeStuckInBlock(state, new Vec3(0.8, 0.75, 0.8));                            // slow (always)
//	if (level instanceof ServerLevel && state.getValue(AGE) != 0) {                       // grown bush
//	    Vec3 mv = isClientAuthoritative() ? getKnownMovement() : oldPosition().subtract(position());
//	    if (mv.horizontalDistanceSqr() > 0.0 && (abs(mv.x) >= 0.003 || abs(mv.z) >= 0.003))
//	        hurtServer(sweetBerryBush(), 1.0F);                                            // moved -> hurt
//	}
//
// The movement vector for a server-side mob is oldPosition - position (the per-tick delta). Sulfur has no
// generic per-entity previous-position field, so we read the mob velocity (e.vx/e.vz) as the movement
// delta: for a walking mob e.vx/e.vz ARE this tick committed horizontal delta (moveEntity advanced the
// position by them, then zeroed a clamped axis), so |delta| >= 0.003 is the same "the mob is actively
// moving through the bush" observable vanilla oldPos-pos check makes. Cited: the movement proxy stands in
// for oldPosition() until a generic xo/zo bookkeeping field lands (the same seam boat/minecart
// minecartXo uses). RNG-free.
//
//	[VERIFIED javap SweetBerryBushBlock.entityInside: LivingEntity && !is(FOX) && !is(BEE) gate;
//	 makeStuckInBlock(0.8,0.75,0.8); ServerLevel && AGE!=0; horizontalDistanceSqr>0 && (|x|>=0.003 ||
//	 |z|>=0.003) -> hurtServer(sweetBerryBush(), 1.0F).]
func (t *TickLoop) sweetBerryBushEntityInside(_ block.StateID, b block.SweetBerryBush, e *Entity) {
	if !isLivingMob(e) || e.typ == entity.Fox.ID || e.typ == entity.Bee.ID {
		return
	}
	makeStuckInBlock(e, 0.800000011920929, 0.75, 0.800000011920929)
	// ServerLevel && AGE != 0 (a grown/berry-bearing bush hurts; a freshly-planted AGE==0 does not).
	if int(b.Age) == 0 {
		return
	}
	// mv = oldPosition().subtract(position()) modeled as the per-tick velocity delta (see doc above).
	mvX, mvZ := e.vx, e.vz
	if mvX*mvX+mvZ*mvZ <= 0.0 { // horizontalDistanceSqr() > 0.0
		return
	}
	if math.Abs(mvX) >= 0.003000000026077032 || math.Abs(mvZ) >= 0.003000000026077032 {
		t.applyDamageEntity(e, damageSourceOf(damageTypeSweetBerryBush), 1.0)
	}
}

// witherRoseEntityInside ports WitherRoseBlock.entityInside 1:1:
//
//	if (level instanceof ServerLevel && level.getDifficulty() != PEACEFUL && entity instanceof LivingEntity
//	    && !le.isInvulnerableTo(serverLevel, damageSources().wither()))
//	    le.addEffect(getBeeInteractionEffect());   // new MobEffectInstance(WITHER, 40)
//
// getBeeInteractionEffect() == new MobEffectInstance(MobEffects.WITHER, 40) (the 2-arg ctor: amplifier 0).
// The isInvulnerableTo(wither()) gate is a cited constant-false in v1 (no mob invulnerability flag; the
// same const isInvulnerableTo=false combat_mob.go uses) -- it becomes a real read when invulnerability
// lands; a wither-immune mob is not modeled yet, so the effect applies to every living mob on non-PEACEFUL.
// RNG-free (addEntityEffect draws nothing for a non-instant effect).
//
//	[VERIFIED javap WitherRoseBlock.entityInside: ServerLevel && getDifficulty()!=PEACEFUL && LivingEntity
//	 && !isInvulnerableTo(wither()) -> addEffect(new MobEffectInstance(WITHER, 40)).
//	 WitherRoseBlock.getBeeInteractionEffect: new MobEffectInstance(MobEffects.WITHER, bipush 40).]
func (t *TickLoop) witherRoseEntityInside(e *Entity) {
	if t.levelDifficulty == difficultyPeaceful {
		return
	}
	if !isLivingMob(e) {
		return
	}
	const isInvulnerableToWither = false // v1: no mob invulnerability flag (cited, combat_mob.go pattern).
	if isInvulnerableToWither {
		return
	}
	t.addEntityEffect(e, effectWither, 40, 0) // new MobEffectInstance(WITHER, 40) -> amplifier 0.
}

// tripwireEntityInside ports TripWireBlock.entityInside 1:1: on the server (isClientSide false), if the
// wire is NOT already POWERED and has no scheduled tick, run checkPressed with THIS entity present. The
// existing tripwireCheckPressed(pos, entitiesPresent) is the checkPressed(List.of(entity)) port; an entity
// standing on the wire makes entitiesPresent==true (the entity is not ignoring block triggers -- v1 has no
// isIgnoringBlockTriggers entity except a marker armor stand, so any overlapping entity presses).
// RNG-free. The scheduled-recheck path uses redstone_blocks.go tripwireEntitiesPresent (the getEntities
// scan) for the release; this per-entity push is the entityInside(List.of(entity)) entry point.
//
//	[VERIFIED javap TripWireBlock.entityInside: isClientSide -> return; if (!POWERED && !hasScheduledTick)
//	 checkPressed(level, pos, List.of(entity)). checkPressed: powered = any entity !isIgnoringBlockTriggers.]
func (t *TickLoop) tripwireEntityInside(state block.StateID, pos pk.Position) {
	if block.TripwirePowered(state) {
		return // already POWERED: the entityInside gate returns (the scheduled tick handles the release).
	}
	if t.hasScheduledBlockTick(pos, tripwireTickType) {
		return // a scheduled recheck is pending -- entityInside defers to it (vanilla hasScheduledTick gate).
	}
	t.tripwireCheckPressed(pos, true) // an overlapping entity is present and not ignoring block triggers.
}

// makeStuckInBlock ports Entity.makeStuckInBlock(BlockState, Vec3): resetFallDistance() then
// stuckSpeedMultiplier = v. We arm e.stuck and store the per-axis multiplier; moveEntity consumes it at the
// start of the entity NEXT move (Entity.move stuck-speed branch). resetFallDistance() zeroes the
// accumulated fall distance (a cobweb/berry breaks a fall). RNG-free.
//
//	[VERIFIED javap Entity.makeStuckInBlock: resetFallDistance(); this.stuckSpeedMultiplier = v.]
func makeStuckInBlock(e *Entity, vx, vy, vz float64) {
	if e == nil {
		return
	}
	e.fallDistance = 0 // resetFallDistance()
	e.stuckSpeedMultiplierX = vx
	e.stuckSpeedMultiplierY = vy
	e.stuckSpeedMultiplierZ = vz
	e.stuck = true
}

// itemStartHealth is net.minecraft.world.entity.item.ItemEntity's initial health (the <init> `health = 5`).
// An item survives 5 points of contact damage before it is discarded (cactus 1.0F/tick -> destroyed on the
// 5th overlapping tick).
//
//	[VERIFIED javap ItemEntity.<init>: iconst_5 putfield health.]
func (e *Entity) itemStartHealth() int { return 5 }

// checkInsideBlocksPlayer is the PLAYER-facing port of Entity.checkInsideBlocks. A ServerPlayer runs
// Entity.baseTick -> ... -> Entity.move -> applyEffectsFromBlocks -> checkInsideBlocks EVERY tick, exactly
// like a mob (baseTick is Entity's, not Mob's), so a player standing in cactus/sweet-berry/wither-rose/
// tripwire must fire the SAME entityInside behaviors. But a player's health/effects/fall-distance live on
// the tickPlayer (client-authoritative movement), NOT on p.playerEntity (whose health field is 0), so the
// mob checkInsideBlocks(*Entity) path would misroute every effect into a dead store entity. This routes
// each entityInside through the PLAYER pipeline instead:
//   - CactusBlock.entityInside         -> applyDamage(p, cactus, 1.0)   [player hurtServer -> health + hurt packet]
//   - WebBlock.entityInside            -> resetFallDistance() only      [the stuck-speed slow is applied CLIENT-side
//                                                                       for client-authoritative movement; the server
//                                                                       owns only the fall-distance reset. Cited.]
//   - SweetBerryBushBlock.entityInside -> resetFallDistance(); on a grown bush (AGE!=0) while MOVING,
//                                         applyDamage(p, sweet_berry_bush, 1.0)
//   - WitherRoseBlock.entityInside     -> addPlayerEffect(WITHER, 40) on non-PEACEFUL
//   - TripWireBlock.entityInside       -> tripwireEntityInside (position-based; a player presses the wire)
//
// isAffectedByBlocks (!isRemoved && !noPhysics): a live player is always affected. The scan geometry is the
// player's deflated 0.6x1.8 box, identical to the mob scan. Air cells skip. RNG-free on every path.
//
//	[VERIFIED javap Entity.baseTick/move -> applyEffectsFromBlocks -> checkInsideBlocks; the per-block
//	 entityInside overrides cited on each mob helper; the player wrappers only re-route the sink.]
func (t *TickLoop) checkInsideBlocksPlayer(p *tickPlayer) {
	if p == nil || p.dead || t.world() == nil {
		return
	}
	hw := playerWidth / 2
	minX := p.x - hw + checkInsideBlocksDeflate
	minY := p.y + checkInsideBlocksDeflate
	minZ := p.z - hw + checkInsideBlocksDeflate
	maxX := p.x + hw - checkInsideBlocksDeflate
	maxY := p.y + playerHeight - checkInsideBlocksDeflate
	maxZ := p.z + hw - checkInsideBlocksDeflate

	loX, hiX := int(math.Floor(minX)), int(math.Floor(maxX))
	loY, hiY := int(math.Floor(minY)), int(math.Floor(maxY))
	loZ, hiZ := int(math.Floor(minZ)), int(math.Floor(maxZ))

	for bx := loX; bx <= hiX; bx++ {
		for by := loY; by <= hiY; by++ {
			for bz := loZ; bz <= hiZ; bz++ {
				pos := pk.Position{X: bx, Y: by, Z: bz}
				state, ok := t.world().GetBlock(pos, dimMinY)
				if !ok {
					continue
				}
				id := block.StateList[state].ID()
				if id == "minecraft:air" || id == "minecraft:cave_air" || id == "minecraft:void_air" {
					continue
				}
				t.playerInsideBlock(state, pos, p)
			}
		}
	}
}

// playerInsideBlock is the player-facing BlockState.entityInside dispatch (the sink-rerouted twin of
// entityInsideBlock). Blocks with no entityInside override fall through -- a no-op.
func (t *TickLoop) playerInsideBlock(state block.StateID, pos pk.Position, p *tickPlayer) {
	switch b := block.StateList[state].(type) {
	case block.Cactus:
		// CactusBlock.entityInside: hurt(cactus, 1.0F) -> the player hurtServer pipeline.
		t.applyDamage(p, damageSourceOf(damageTypeCactus), 1.0)
	case block.Cobweb:
		// WebBlock.entityInside -> makeStuckInBlock: the stuck-speed slow is applied CLIENT-side for a
		// client-authoritative player; the server owns only resetFallDistance() (a cobweb breaks a fall).
		p.resetFallDistance()
	case block.SweetBerryBush:
		t.sweetBerryBushEntityInsidePlayer(b, p)
	case block.WitherRose:
		// WitherRoseBlock.entityInside: ServerLevel && difficulty!=PEACEFUL && LivingEntity &&
		// !isInvulnerableTo(wither) -> addEffect(WITHER, 40). A player is a LivingEntity; the
		// isInvulnerableTo gate is the v1 constant-false (matches witherRoseEntityInside).
		if t.levelDifficulty != difficultyPeaceful {
			t.addPlayerEffect(p, 0, effectWither, 40, 0, 1.0)
		}
	case block.Tripwire:
		// TripWireBlock.entityInside: an overlapping non-ignoring entity presses the wire (position-based,
		// identical to the mob path).
		t.tripwireEntityInside(state, pos)
	}
}

// sweetBerryBushEntityInsidePlayer is the player-facing SweetBerryBushBlock.entityInside: makeStuckInBlock
// (resetFallDistance server-side; the slow is client-side) then, on a grown bush (AGE!=0) while the player
// is MOVING (oldPosition - position: |dx|>=0.003 || |dz|>=0.003, horizontalDistanceSqr>0), hurtServer(
// sweetBerryBush, 1.0F) via applyDamage. A player is always a LivingEntity (the FOX/BEE exemptions never
// apply). The movement delta uses prevX/prevZ (the xo/zo stand-in) == oldPosition - position, matching the
// mob helper's velocity-proxy rationale but with the player's real per-tick position delta.
//
//	[VERIFIED javap SweetBerryBushBlock.entityInside: LivingEntity && !FOX && !BEE; makeStuckInBlock(
//	 0.8,0.75,0.8); ServerLevel && AGE!=0; mv=oldPosition-position; horizontalDistanceSqr>0 && (|x|>=0.003
//	 || |z|>=0.003) -> hurtServer(sweetBerryBush, 1.0F).]
func (t *TickLoop) sweetBerryBushEntityInsidePlayer(b block.SweetBerryBush, p *tickPlayer) {
	p.resetFallDistance() // makeStuckInBlock(0.8,0.75,0.8): server owns the fall reset; the slow is client-side.
	if int(b.Age) == 0 {
		return // ServerLevel && AGE != 0: a freshly-planted bush does not hurt.
	}
	// mv = oldPosition().subtract(position()) == (prevX - x, prevZ - z), the player's per-tick delta.
	mvX := p.prevX - p.x
	mvZ := p.prevZ - p.z
	if mvX*mvX+mvZ*mvZ <= 0.0 { // horizontalDistanceSqr() > 0.0
		return
	}
	if math.Abs(mvX) >= 0.003000000026077032 || math.Abs(mvZ) >= 0.003000000026077032 {
		t.applyDamage(p, damageSourceOf(damageTypeSweetBerryBush), 1.0)
	}
}
