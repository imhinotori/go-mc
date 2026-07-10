package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// piston_entity_test.go — REDSTONE TIER-3 (PISTON) entity-shove validation gates. Each asserts the
// ported PistonMovingBlockEntity.moveCollidedEntities / moveStuckEntities / moveEntityByPiston behaviour
// against the unobfuscated 26.2 jar:
//   - a moving_piston extending into a MOB translates the mob one block along the push axis over the
//     2-tick animation (PistonMovingBlockEntity.tick -> moveCollidedEntities -> moveEntityByPiston);
//   - the same shove applies to a PLAYER (players live in t.players, not the entity store) and to an
//     ITEM entity;
//   - a moving_piston with a CLEAR path (no entity in the swept box) translates nothing — the shove is
//     strictly additive, so a piston nowhere near an entity behaves byte-identically to before.
//
// The per-tick push is min(getMovement, d0) + 0.01 along movementDirection (jar-verified: d0 == 0.5, so
// each of the 2 animation ticks pushes up to ~0.51, ~1.02 total ≈ one block). CITE:
// PistonMovingBlockEntity.moveCollidedEntities (d1 = min(d1, d0) + 0.01d).

// spawnExtendingStonePiston places a moving_piston carrying stone (extending east, non-source) at dst,
// registers its BE at progress 0, and returns dst. This is the DESTINATION cell of an east-facing
// piston's push (PistonBaseBlock.moveBlocks toPush loop): its moveByPositionAndProgress box starts one
// cell WEST of dst and animates east into dst, sweeping the cell in front of dst.
func spawnExtendingStonePiston(loop *TickLoop, mgr *world.ChunkManager, dst pk.Position) {
	movingState, _ := block.MovingPistonState(block.East, block.PistonTypeNormal)
	mgr.SetBlock(dst, movingState, dimMinY)
	loop.newMovingBlockEntity(dst, stoneState(), block.East, true /*extending*/, false /*not source*/)
}

// TestPistonShovesMob locks moveCollidedEntities for a MOB: a stone-carrying moving_piston extending east
// into a mob standing in front of it translates the mob east by ~1 block over the 2-tick animation, and
// never west. CITE: PistonMovingBlockEntity.moveCollidedEntities / moveEntityByPiston.
func TestPistonShovesMob(t *testing.T) {
	loop, mgr := newPistonLoop()

	dst := pk.Position{X: 6, Y: 64, Z: 4} // the moving_piston (stone) destination cell
	spawnExtendingStonePiston(loop, mgr, dst)

	// A pig standing in the cell the stone sweeps into (centered at X=6.5, feet at Y=64): its AABB
	// (0.9 wide) spans X ∈ [6.05, 6.95], overlapping the swept push box.
	pig := testEntity(1, entity.Pig, 6.5, 64.0, 4.5)
	loop.only().entities.add(pig)
	startX := pig.x

	loop.completeMovingPistons()

	moved := pig.x - startX
	if moved < 0.9 || moved > 1.2 {
		t.Fatalf("piston should shove the mob ~1 block east, moved %.4f (want ~1.0)", moved)
	}
	if pig.z != 4.5 || pig.y != 64.0 {
		t.Fatalf("piston must shove ONLY along the push axis (east); off-axis drift z=%.4f y=%.4f", pig.z, pig.y)
	}
}

// TestPistonShovesPlayer locks moveCollidedEntities for a PLAYER (t.players, not the entity store): the
// same east extend translates a player standing in front of it east by ~1 block. CITE:
// PistonMovingBlockEntity.moveCollidedEntities (Level.getEntities includes players).
func TestPistonShovesPlayer(t *testing.T) {
	loop, mgr := newPistonLoop()

	dst := pk.Position{X: 6, Y: 64, Z: 4}
	spawnExtendingStonePiston(loop, mgr, dst)

	// A player (0.6 wide) centered at X=6.0 (against the pushing face), feet at Y=64 — box X ∈ [5.7, 6.3]
	// fully inside the swept push box, so it takes the full per-tick shove (min(getMovement,0.5)+0.01).
	p := &tickPlayer{x: 6.0, y: 64.0, z: 4.5, entityID: 9001}
	loop.players = append(loop.players, p)
	startX := p.x

	loop.completeMovingPistons()

	moved := p.x - startX
	if moved < 0.9 || moved > 1.2 {
		t.Fatalf("piston should shove the player ~1 block east, moved %.4f (want ~1.0)", moved)
	}
}

// TestPistonShovesItem locks moveCollidedEntities for an ITEM entity: a dropped item in the swept box is
// pushed east like any other entity. CITE: PistonMovingBlockEntity.moveCollidedEntities (per-Entity).
func TestPistonShovesItem(t *testing.T) {
	loop, mgr := newPistonLoop()

	dst := pk.Position{X: 6, Y: 64, Z: 4}
	spawnExtendingStonePiston(loop, mgr, dst)

	// An item entity centered at X=6.4, feet at Y=64. entity.Item is 0.25 wide; box X ∈ [6.275, 6.525].
	item := testEntity(2, entity.Item, 6.4, 64.0, 4.5)
	loop.only().entities.add(item)
	startX := item.x

	loop.completeMovingPistons()

	moved := item.x - startX
	if moved <= 0.0 {
		t.Fatalf("piston should shove the item east, moved %.4f (want > 0)", moved)
	}
}

// TestPistonClearPathMovesNothing is the STRICTLY-ADDITIVE gate: a moving_piston whose swept box holds NO
// entity translates nothing — the entity far away is byte-identical to no-shove. A pig nowhere near the
// piston (the pig-oracle invariant) never enters the shove path. CITE: moveCollidedEntities
// (getEntities empty -> return before any translate).
func TestPistonClearPathMovesNothing(t *testing.T) {
	loop, mgr := newPistonLoop()

	dst := pk.Position{X: 6, Y: 64, Z: 4}
	spawnExtendingStonePiston(loop, mgr, dst)

	// A pig 40 blocks away — well outside the swept box (and outside the queried bucket radius).
	far := testEntity(3, entity.Pig, 46.5, 64.0, 44.5)
	loop.only().entities.add(far)
	sx, sy, sz := far.x, far.y, far.z

	loop.completeMovingPistons()

	if far.x != sx || far.y != sy || far.z != sz {
		t.Fatalf("a piston with a clear path must move nothing; far pig drifted to (%.4f,%.4f,%.4f) from (%.4f,%.4f,%.4f)",
			far.x, far.y, far.z, sx, sy, sz)
	}
}
