package server

// ai_goals_powder_snow.go — the Go-native ClimbOnTopOfPowderSnowGoal, the goal that lets a small,
// powder-snow-walkable mob (rabbit/fox/silverfish/endermite) climb ON TOP of powder snow instead of
// sinking into it. Wired via the kind="climb_on_powder_snow" seam (buildNativeGoal) on the same mobs
// vanilla registers it on — Rabbit @1, Fox @0, Silverfish @1 (jar-verified this session; Creeper does
// NOT register it and is NOT in the tag, so it is intentionally absent from the creeper wiring).
//
// PORTED 1:1 from net.minecraft.world.entity.ai.goal.ClimbOnTopOfPowderSnowGoal in the unobfuscated
// 26.2 jar (javap -c -p / CFR temp/cache/26.2-inner.jar, this session):
//
//	public ClimbOnTopOfPowderSnowGoal(Mob mob, Level level) {
//	    this.mob = mob; this.level = level;
//	    this.setFlags(EnumSet.of(Flag.JUMP));                 // -> newBaseGoal(flagJump)
//	}
//	public boolean canUse() {
//	    boolean inPowderSnow = this.mob.wasInPowderSnow || this.mob.isInPowderSnow;
//	    if (!inPowderSnow || !this.mob.is(EntityTypeTags.POWDER_SNOW_WALKABLE_MOBS)) return false;
//	    BlockPos above = this.mob.blockPosition().above();
//	    BlockState aboveState = this.level.getBlockState(above);
//	    return aboveState.is(Blocks.POWDER_SNOW) || aboveState.getCollisionShape(level, above) == Shapes.empty();
//	}
//	public boolean requiresUpdateEveryTick() { return true; }
//	public void tick() { this.mob.getJumpControl().jump(); }   // -> jumpControl.doJump()
//
// RNG DISCIPLINE: this goal draws NO random numbers (canUse is pure world/state reads, tick only arms
// the jump control) — exactly like FloatGoal.canUse. Adding it to a mob perturbs no RNG stream, so a
// mob that is never in powder snow behaves byte-identically to one without the goal.
//
// DEFERRAL (cite-recorded, NEVER silently dropped): the mob's isInPowderSnow / wasInPowderSnow fields
// (net.minecraft.world.entity.Entity.isInPowderSnow, set true by PowderSnowBlock.entityInside ->
// Entity.setIsInPowderSnow during checkInsideBlocks, cleared+carried each baseTick via
// `wasInPowderSnow = isInPowderSnow; isInPowderSnow = false;`) are NOT modeled in v1 — no
// powder-snow-inside subsystem (makeStuckInBlock / freeze / slow) exists yet. mobInPowderSnow below is
// the CITED stub reading the observable proxy: whether the mob's feet block IS powder snow (the block
// whose entityInside would set isInPowderSnow). It reads the codegen'd block.PowderSnow state id
// (data present) so it becomes a real `isInPowderSnow` field read when the inside-block subsystem lands.
// The `mob.is(POWDER_SNOW_WALKABLE_MOBS)` tag gate is modeled faithfully by entity-type membership
// (the tag's exact jar contents: rabbit, endermite, silverfish, fox — powder_snow_walkable_mobs.json).

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// climbOnTopOfPowderSnowGoal ports net.minecraft.world.entity.ai.goal.ClimbOnTopOfPowderSnowGoal
// (flag {JUMP}). It holds no per-mob state beyond baseGoal — the jar goal holds only mob+level
// back-references, which the (t, e) method parameters supply here.
type climbOnTopOfPowderSnowGoal struct {
	baseGoal
}

// newClimbOnTopOfPowderSnowGoal builds the goal with the JUMP flag (ctor: setFlags(EnumSet.of(JUMP))).
//
//	[VERIFIED javap ClimbOnTopOfPowderSnowGoal.<init>: setFlags(EnumSet.of(Goal$Flag.JUMP)).]
func newClimbOnTopOfPowderSnowGoal() *climbOnTopOfPowderSnowGoal {
	return &climbOnTopOfPowderSnowGoal{baseGoal: newBaseGoal(flagJump)}
}

// canUse ports ClimbOnTopOfPowderSnowGoal.canUse EXACTLY, in bytecode branch order:
//  1. inPowderSnow = wasInPowderSnow || isInPowderSnow; if !inPowderSnow OR the mob is not in the
//     POWDER_SNOW_WALKABLE_MOBS tag -> false. (NO RNG.)
//  2. above = blockPosition().above(); aboveState = getBlockState(above).
//  3. return aboveState.is(POWDER_SNOW) || aboveState.getCollisionShape(...) == Shapes.empty().
//
//	[VERIFIED javap ClimbOnTopOfPowderSnowGoal.canUse: (wasInPowderSnow || isInPowderSnow) &&
//	 mob.is(POWDER_SNOW_WALKABLE_MOBS) gate; blockPosition().above(); getBlockState;
//	 BlockState.is(Blocks.POWDER_SNOW) || getCollisionShape == Shapes.empty().]
func (g *climbOnTopOfPowderSnowGoal) canUse(t *TickLoop, e *Entity) bool {
	if e == nil || t.world() == nil {
		return false
	}
	// (1) the powder-snow membership gate — cited stub (isInPowderSnow/wasInPowderSnow proxy) AND the
	// POWDER_SNOW_WALKABLE_MOBS tag. Both must hold before any block read; NO RNG.
	if !t.mobInPowderSnow(e) || !isPowderSnowWalkableMob(e.typ) {
		return false
	}
	// (2) the block DIRECTLY ABOVE the mob's blockPosition (feet cell). blockPosition() == floor(x,y,z).
	above := pk.Position{
		X: int(math.Floor(e.x)),
		Y: int(math.Floor(e.y)) + 1, // .above()
		Z: int(math.Floor(e.z)),
	}
	// (3) the above block is powder snow OR its collision shape is empty (nothing to climb into).
	// getCollisionShape == Shapes.empty() is exactly !blocksMotion() — blockSolidAt returns true only
	// for a colliding (non-empty-shape) block, so !blockSolidAt == empty collision shape. isPowderSnowBlock
	// reads the codegen'd block.PowderSnow state id.
	if t.isPowderSnowBlock(above) {
		return true
	}
	return !t.blockSolidAt(above.X, above.Y, above.Z)
}

// requiresUpdateEveryTick ports ClimbOnTopOfPowderSnowGoal.requiresUpdateEveryTick == true: tick()
// arms the jump control every tick while the mob is climbing (so it keeps bobbing to the surface).
//
//	[VERIFIED javap ClimbOnTopOfPowderSnowGoal.requiresUpdateEveryTick: iconst_1; ireturn.]
func (g *climbOnTopOfPowderSnowGoal) requiresUpdateEveryTick() bool { return true }

// canContinueToUse ports the INHERITED Goal.canContinueToUse (the goal declares no override, so it gets
// Goal's default `canContinueToUse(){ return canUse(); }`). Without this the goal would inherit
// baseGoal.canContinueToUse -> true and keep holding JUMP + arming the jump after the mob left the snow.
//
//	[VERIFIED javap Goal.canContinueToUse: aload_0; invokevirtual canUse; ireturn (default = canUse()).]
func (g *climbOnTopOfPowderSnowGoal) canContinueToUse(t *TickLoop, e *Entity) bool {
	return g.canUse(t, e)
}

// tick ports ClimbOnTopOfPowderSnowGoal.tick: getJumpControl().jump() -> jumpControl.doJump() (arm the
// JUMP flag this tick). The serverAiStep JUMP slot's jumpControl.tick + entityJumpStep turn the armed
// flag into the upward impulse (setJumping(true) -> the jump velocity) — the same seam FloatGoal uses.
// NO RNG.
//
//	[VERIFIED javap ClimbOnTopOfPowderSnowGoal.tick: getJumpControl(); JumpControl.jump(); return.]
func (g *climbOnTopOfPowderSnowGoal) tick(_ *TickLoop, e *Entity) {
	if e != nil && e.ai != nil {
		e.ai.jumpControl.doJump()
	}
}

// isPowderSnowBlock reports whether the block at pos is minecraft:powder_snow, reading the codegen'd
// block.PowderSnow state id (data/block present). The BlockState.is(Blocks.POWDER_SNOW) analogue.
func (t *TickLoop) isPowderSnowBlock(pos pk.Position) bool {
	if t.world() == nil {
		return false
	}
	s, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return false // unloaded/out-of-range column reads as air (== not powder snow), the faithful default
	}
	return s == block.ToStateID[block.PowderSnow{}]
}

// mobInPowderSnow is the CITED stub for `mob.wasInPowderSnow || mob.isInPowderSnow` (DEFERRAL above):
// no powder-snow-inside subsystem models the Entity.isInPowderSnow field yet, so this reads the
// observable proxy — whether the mob's feet block (blockPosition() == floor(x,y,z)) IS powder snow, the
// block whose PowderSnowBlock.entityInside would call Entity.setIsInPowderSnow(true). It becomes a real
// `e.isInPowderSnow || e.wasInPowderSnow` field read when checkInsideBlocks / the inside-block effects
// land. Cite net.minecraft.world.entity.Entity.isInPowderSnow / PowderSnowBlock.entityInside.
func (t *TickLoop) mobInPowderSnow(e *Entity) bool {
	if e == nil {
		return false
	}
	feet := pk.Position{
		X: int(math.Floor(e.x)),
		Y: int(math.Floor(e.y)),
		Z: int(math.Floor(e.z)),
	}
	return t.isPowderSnowBlock(feet)
}

// isPowderSnowWalkableMob models EntityTypeTags.POWDER_SNOW_WALKABLE_MOBS — the exact jar tag contents
// (data/minecraft/tags/entity_type/powder_snow_walkable_mobs.json, this session): rabbit, endermite,
// silverfish, fox. The ClimbOnTopOfPowderSnowGoal.canUse `mob.is(POWDER_SNOW_WALKABLE_MOBS)` gate (and
// PowderSnowBlock.canEntityWalkOnPowderSnow's first branch) test this. Kept as an explicit type set (no
// tag subsystem yet) — a faithful, cite-anchored membership that becomes a real tag lookup when tags land.
func isPowderSnowWalkableMob(typ entity.ID) bool {
	switch typ {
	case entity.Rabbit.ID, entity.Endermite.ID, entity.Silverfish.ID, entity.Fox.ID:
		return true
	default:
		return false
	}
}
