package server

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// end_dimension.go -- the observable core of arriving in the_end: the obsidian spawn platform
// (EndPlatformFeature.createEndPlatform) laid under ServerLevel.END_SPAWN_POINT, plus the
// coordinate constants EndPortalBlock.getPortalDestination places the player at. Dragon fight
// is OUT of scope (deferred): showEndCredits + the exit-portal generation are not ported; a
// player in the End returns to the overworld via the shared changeDimension path.
//
// CITED JAR (javap -c, 26.2-inner.jar):
//   - net.minecraft.server.level.ServerLevel.END_SPAWN_POINT == new BlockPos(100, 50, 0)
//     (static initializer: bipush 100, bipush 50, iconst_0).
//   - net.minecraft.world.level.levelgen.feature.EndPlatformFeature.createEndPlatform(
//     ServerLevelAccessor, BlockPos, boolean): for x2 in [-2,2], z2 in [-2,2], y in [-1,3):
//     the block at move(z2, y, x2) is OBSIDIAN when y == -1 else AIR (a 5x5 obsidian floor
//     with a 5x5x3 air box above).
//   - net.minecraft.world.level.block.EndPortalBlock.getPortalDestination: overworld->End
//     destination = atBottomCenterOf(END_SPAWN_POINT) (== 100.5, 50, 0.5); createEndPlatform(
//     containing(that).below() == (100,49,0), true); for a ServerPlayer the spawn is that Vec3
//     .subtract(0,1,0) == (100.5, 49, 0.5), yaw = Direction.WEST.toYRot() == -90/270 == 90 wrap.

// End spawn-platform coordinates. END_SPAWN_POINT is (100, 50, 0). The player is placed at the
// bottom-center of that block, shifted down one (ServerPlayer branch): (100.5, 49, 0.5). The
// obsidian floor sits at y 48 (createEndPlatform's base = containing(100.5,50,0.5).below() ==
// (100,49,0); the obsidian row is base + move(_, -1, _) == y 48; the 3-high air box is y 49..51).
const (
	endSpawnX = 100.5 // atBottomCenterOf(END_SPAWN_POINT).x == 100 + 0.5
	endSpawnY = 49.0  // (100.5, 50, 0.5).subtract(0,1,0).y == 49 (ServerPlayer branch)
	endSpawnZ = 0.5   // atBottomCenterOf(END_SPAWN_POINT).z == 0 + 0.5
)

// endPlatformBase is BlockPos.containing(atBottomCenterOf(END_SPAWN_POINT)).below() == (100,49,0):
// the BlockPos createEndPlatform receives. The obsidian floor is one below (y 48).
var endPlatformBase = pk.Position{X: 100, Y: 49, Z: 0}

// createEndPlatform ports EndPlatformFeature.createEndPlatform(level, base, dropBlocks=true) 1:1:
// a 5x5 obsidian floor (y == base.y-1) with a 5x5x3 air box above it (y in base.y .. base.y+2).
// It writes through the End ChunkManager at the End's minY. Vanilla iterates x2 in [-2,2], z2 in
// [-2,2], y in [-1,3), and at move(z2, y, x2) places OBSIDIAN when y == -1 else AIR; the footprint
// is symmetric in x2/z2 so the (z2,x2) swap is immaterial. dropBlocks (true) is a destroyBlock of
// any non-matching block first -- with a freshly generated End chunk the target cells are already
// air/end_stone, so the setBlock alone is byte-equivalent here.
func (t *TickLoop) createEndPlatform(base pk.Position) {
	mgr := t.endWorld
	if mgr == nil {
		return
	}
	minY := dimEndMinY
	obsidian := block.ToStateID[block.Obsidian{}]
	air := block.ToStateID[block.Air{}]
	for x2 := -2; x2 <= 2; x2++ {
		for z2 := -2; z2 <= 2; z2++ {
			for y := -1; y < 3; y++ {
				state := air
				if y == -1 {
					state = obsidian
				}
				pos := pk.Position{X: base.X + x2, Y: base.Y + y, Z: base.Z + z2}
				mgr.SetBlock(pos, state, minY)
			}
		}
	}
}

// ensureEndPlatform lays the obsidian spawn platform under the arrival point. If the platform
// chunk is not yet present in the End world (the async worker has not produced it), it force-
// generates it synchronously via the End generator and inserts it -- the standing-in for vanilla's
// PLACE_PORTAL_TICKET, which force-loads the destination chunk during the transition so the
// platform can be built. Then it writes the platform blocks. Tick-owned (called from changeDimension).
func (t *TickLoop) ensureEndPlatform() {
	mgr := t.endWorld
	if mgr == nil {
		return
	}
	// The 5x5 platform (base +/-2 in X/Z) can straddle up to a 2x2 block of chunks. Force-
	// generate EACH touched chunk that is not yet loaded (synchronous, pure over (seed,pos)) so
	// every platform cell resolves through SetBlock -- the standing-in for vanilla's
	// PLACE_PORTAL_TICKET force-load of the destination chunk during the transition.
	seen := make(map[level.ChunkPos]bool, 4)
	for _, dx := range []int{-2, 2} {
		for _, dz := range []int{-2, 2} {
			probe := pk.Position{X: endPlatformBase.X + dx, Y: endPlatformBase.Y, Z: endPlatformBase.Z + dz}
			col := level.ChunkPos{int32(probe.X >> 4), int32(probe.Z >> 4)}
			if seen[col] {
				continue
			}
			seen[col] = true
			if _, ok := mgr.GetBlock(probe, dimEndMinY); ok {
				continue
			}
			if t.endGen != nil {
				ch := t.endGen.Generate(col)
				ch.Status = level.StatusFull
				mgr.Insert(col, ch)
			}
		}
	}
	t.createEndPlatform(endPlatformBase)
}
