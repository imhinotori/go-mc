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

// isSuffocating is the 1:1 port of BlockState.isSuffocating (the per-block StatePredicate set in
// BlockBehaviour.Properties). CITE: javap BlockBehaviour$BlockStateBase.isSuffocating — it invokes
// the block's `isSuffocating` StatePredicate. The vanilla DEFAULT predicate
// (Block.Properties.isSuffocating, used by most full-cube blocks) is
// `(state, level, pos) -> state.blocksMotion() && state.isCollisionShapeFullBlock(level, pos)` —
// i.e. a solid, full-cube block. A few blocks OVERRIDE it (glass, leaves, etc. return false even
// though they are full collision cubes), and non-full blocks (slabs, stairs, fences) return false.
//
// The per-state result is now extracted from the jar (SUB-FACESTURDY): block.IsSuffocating reads
// the baked `isSuffocating(EmptyBlockGetter, ZERO)` value — the actual predicate (default OR
// override) evaluated with no world context, which equals the live read for the dominant
// non-dynamic-shape suffocation case (a head inside dirt/stone/sand/etc.). This replaces the
// earlier non-air/non-fluid proxy, which over-approximated glass/leaves/slabs as suffocating; the
// faithful table now reports glass=false, leaves=false, bottom-slab=false, matching vanilla.
//
// REMAINING v1 GAP (cited, not yet modeled): the few blocks whose isSuffocating override is
// genuinely context-dependent (it reads the BlockGetter/pos) are baked at EmptyBlockGetter — a
// no-context approximation. None of the common suffocation blocks are in that set, so this matches
// vanilla for the IN_WALL case; a fully context-aware read would require the live VoxelShape
// engine Sulfur has not extracted.
func (t *TickLoop) isSuffocating(s block.StateID) bool {
	return block.IsSuffocating(s)
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
