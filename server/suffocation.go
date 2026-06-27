package server

import (
	"math"

	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/level/block"
)

// suffocation.go is the 1:1 port of the IN_WALL (suffocation) branch of
// net.minecraft.world.entity.LivingEntity.baseTick:
//
//	if (this.isInWall()) {
//	    this.hurtServer(serverLevel, this.damageSources().inWall(), 1.0F);
//	}
//
// CITE: javap net.minecraft.world.entity.LivingEntity.baseTick — at the ServerLevel branch the
// bytecode is `invokevirtual isInWall:()Z ; ifeq ... ; damageSources().inWall() ; fconst_1 ;
// hurtServer(...)`. The damage is fconst_1 = 1.0F. Run in tickEntities alongside tickFallDamage /
// tickBreath / tickFood (the same fixed environmental-damage phase, no reorder).
const suffocationDamage = 1.0

// isInWall is the port of net.minecraft.world.entity.Entity.isInWall:
//
//	if (this.noPhysics) return false;
//	float f = this.dimensions.width() * 0.8F;
//	AABB box = AABB.ofSize(this.getEyePosition(), (double)f, 1.0E-6, (double)f);
//	return BlockPos.betweenClosedStream(box).anyMatch(pos -> {
//	    BlockState s = level.getBlockState(pos);
//	    return !s.isAir()
//	        && s.isSuffocating(level, pos)
//	        && Shapes.joinIsNotEmpty(s.getCollisionShape(level,pos).move(pos), Shapes.create(box), AND);
//	});
//
// CITE: javap Entity.isInWall + lambda$isInWall$0. The eye-box is sub-block-sized (width*0.8 wide,
// 1e-6 tall, centered on the eye), so betweenClosedStream yields the single block CONTAINING the
// eye in the dominant case (a player standing with its head inside a full block). v1 collapses the
// stream to that one block at floor(eyePos): the faithful behavior for the common suffocation case
// (head in dirt/stone/sand). The width*0.8 horizontal spread (which can touch a neighbour block at
// a block boundary) and the exact collision-shape intersection (joinIsNotEmpty) are NOT yet modeled
// — they require the per-block VoxelShape engine Sulfur has not extracted; documented as a v1 gap.
func (t *TickLoop) isInWall(p *tickPlayer) bool {
	if t.world == nil {
		return false
	}
	// getEyePosition() = (x, y + eyeHeight, z); the eye-box is centered here. The dominant case
	// is the block containing the eye: floor(eyePos). (noPhysics is always false for a player.)
	bx := int(math.Floor(p.x))
	by := int(math.Floor(p.y + playerStandingEyeHeight))
	bz := int(math.Floor(p.z))
	s, ok := t.world.GetBlock(pk.Position{X: bx, Y: by, Z: bz}, dimMinY)
	if !ok {
		return false
	}
	if block.IsAir(s) {
		return false // !s.isAir() guard
	}
	return t.isSuffocating(s)
}

// isSuffocating is the port of BlockState.isSuffocating (the per-block StatePredicate set in
// BlockBehaviour.Properties). CITE: javap BlockBehaviour$BlockStateBase.isSuffocating — it invokes
// the block's `isSuffocating` StatePredicate. The vanilla DEFAULT predicate
// (Block.Properties.isSuffocating, used by most full-cube blocks) is
// `(state, level, pos) -> state.blocksMotion() && state.isCollisionShapeFullBlock(level, pos)` —
// i.e. a solid, full-cube block. A few blocks override it to false (e.g. those with a special
// occlusion), and non-full blocks (slabs, stairs, fences, glass, leaves) return false.
//
// v1 STUB (clearly cited, structured to become the real read later): the per-block isSuffocating
// flag + isCollisionShapeFullBlock are NOT yet extracted from the jar. As the faithful v1 proxy we
// use blockSolidAt's solidity test (non-air, non-fluid) — which equals the vanilla predicate for
// the dominant full-cube blocks (dirt/stone/sand/ores/wood/etc, the blocks a player actually
// suffocates in) and over-approximates only for the minority of solid non-full-cube blocks
// (slabs/stairs/glass), where vanilla returns false. When the block-property table is extracted
// (the same data subsystem block-survival needs), replace this body with the real
// blocksMotion() && isCollisionShapeFullBlock() && the per-block override — the call site does not
// change. This MUST stay a function (not a baked constant) so that replacement is a one-spot edit.
func (t *TickLoop) isSuffocating(s block.StateID) bool {
	if block.IsAir(s) {
		return false
	}
	// FLUID is never suffocating (it is not a full collision cube): vanilla LiquidBlock is not a
	// full block and its default isSuffocating is false. Exclude it explicitly (mirrors
	// blockSolidAt's fluid exclusion).
	if _, isWater := waterLevelOf(s); isWater {
		return false
	}
	return true // v1 proxy: any non-air, non-fluid block is treated as a full suffocating cube.
}

// tickSuffocation runs the IN_WALL branch of LivingEntity.baseTick for every live player:
// `if isInWall() hurtServer(inWall(), 1.0F)`. Routes through applyDamage (Sulfur's hurtServer
// port) so the IN_WALL hit obeys i-frames exactly as vanilla — one 1.0 hit per i-frame window
// while the head stays in a block. Run in the same fixed environmental-damage phase as
// tickFallDamage / tickBreath (no reorder; faithful position in tickEntities).
func (t *TickLoop) tickSuffocation() {
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		if t.isInWall(p) {
			// hurtServer(damageSources().inWall(), 1.0F) — the 1.0 IN_WALL hit.
			t.applyDamage(p, suffocationDamage)
		}
	}
}
