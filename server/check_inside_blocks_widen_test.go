package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// insidePlayer attaches a survival player (full health/food, a capture client) at (x,y,z) to a physics
// loop, so applyDamage/addPlayerEffect route through the real player pipeline.
func insidePlayer(loop *TickLoop, id int32, x, y, z float64) *tickPlayer {
	p := &tickPlayer{
		client:     captureClient(64),
		entityID:   id,
		gameMode:   gameModeSurvival,
		health:     maxHealth,
		food:       maxFood,
		saturation: defaultSaturation,
		x:          x, y: y, z: z,
		prevX: x, prevY: y, prevZ: z,
	}
	loop.players = append(loop.players, p)
	return p
}

// TestCactusEntityInsidePlayerDamages: a PLAYER standing in a cactus cell takes 1.0 cactus contact damage
// per checkInsideBlocksPlayer pass, routed through the player hurtServer pipeline (NOT the mob store
// entity). This is the marquee widening bug: pre-fix a player in cactus took no damage.
func TestCactusEntityInsidePlayerDamages(t *testing.T) {
	loop, _ := insideBlocksLoop(t)
	pos := pk.Position{X: 8, Y: 64, Z: 8}
	mgrOf(loop).SetBlock(pos, block.ToStateID[block.Cactus{}], dimMinY)

	p := insidePlayer(loop, 1, 8.5, 64.0, 8.5)
	before := p.health
	loop.withRegion(loop.only(), func() { loop.checkInsideBlocksPlayer(p) })
	if p.health >= before {
		t.Fatalf("cactus (player): health did not drop (before=%v after=%v) -- player checkInsideBlocks did not fire", before, p.health)
	}

	dry := insidePlayer(loop, 2, 2.5, 64.0, 2.5)
	loop.withRegion(loop.only(), func() { loop.checkInsideBlocksPlayer(dry) })
	if dry.health != maxHealth {
		t.Fatalf("cactus (player): dry player took damage (health=%v) -- non-cactus block must be a no-op", dry.health)
	}
}

// TestWitherRoseEntityInsidePlayerEffect: a player in a wither_rose on non-PEACEFUL gets WITHER; PEACEFUL
// is a no-op.
func TestWitherRoseEntityInsidePlayerEffect(t *testing.T) {
	loop, _ := insideBlocksLoop(t)
	pos := pk.Position{X: 8, Y: 64, Z: 8}
	mgrOf(loop).SetBlock(pos, block.ToStateID[block.WitherRose{}], dimMinY)

	loop.levelDifficulty = difficultyNormal
	p := insidePlayer(loop, 1, 8.5, 64.0, 8.5)
	loop.withRegion(loop.only(), func() { loop.checkInsideBlocksPlayer(p) })
	if !playerHasEffect(p, effectWither) {
		t.Fatalf("wither_rose (player): non-PEACEFUL player did not get WITHER")
	}

	loop.levelDifficulty = difficultyPeaceful
	peaceful := insidePlayer(loop, 2, 8.5, 64.0, 8.5)
	loop.withRegion(loop.only(), func() { loop.checkInsideBlocksPlayer(peaceful) })
	if playerHasEffect(peaceful, effectWither) {
		t.Fatalf("wither_rose (player): PEACEFUL player got WITHER -- the difficulty gate must skip it")
	}
}

// TestSweetBerryBushPlayerDamageOnMove: a player in a grown berry bush takes damage only while moving
// (prevX/prevZ delta >= 0.003); a stationary player is unharmed.
func TestSweetBerryBushPlayerDamageOnMove(t *testing.T) {
	loop, _ := insideBlocksLoop(t)
	pos := pk.Position{X: 8, Y: 64, Z: 8}
	mgrOf(loop).SetBlock(pos, block.ToStateID[block.SweetBerryBush{Age: 3}], dimMinY)

	moving := insidePlayer(loop, 1, 8.5, 64.0, 8.5)
	moving.prevX = 8.5 + 0.1
	before := moving.health
	loop.withRegion(loop.only(), func() { loop.checkInsideBlocksPlayer(moving) })
	if moving.health >= before {
		t.Fatalf("berry (player): moving player took no damage (before=%v after=%v)", before, moving.health)
	}

	still := insidePlayer(loop, 2, 8.5, 64.0, 8.5)
	still.prevX, still.prevZ = 8.5, 8.5
	loop.withRegion(loop.only(), func() { loop.checkInsideBlocksPlayer(still) })
	if still.health != maxHealth {
		t.Fatalf("berry (player): stationary player took damage (health=%v) -- no movement must not hurt", still.health)
	}
}

// TestCactusDestroysItem: a dropped item resting on cactus loses 1 health/tick and is discarded after 5
// ticks (ItemEntity.hurtServer health path; <init> health=5). Pre-fix an item on cactus was never destroyed.
func TestCactusDestroysItem(t *testing.T) {
	loop, _ := insideBlocksLoop(t)
	pos := pk.Position{X: 8, Y: 64, Z: 8}
	mgrOf(loop).SetBlock(pos, block.ToStateID[block.Cactus{}], dimMinY)

	ie := NewEntity(1, entity.Item, 8.5, 64.0, 8.5)
	ie.isItem = true
	loop.only().entities.add(ie)

	loop.withRegion(loop.only(), func() {
		for i := 0; i < 4; i++ {
			loop.checkInsideBlocks(ie)
		}
	})
	if _, ok := loop.only().entities.get(ie.id); !ok {
		t.Fatalf("cactus (item): item destroyed too early (after 4 ticks, health should be 1)")
	}
	if ie.itemDamageTaken != 4 {
		t.Fatalf("cactus (item): itemDamageTaken = %d after 4 ticks, want 4", ie.itemDamageTaken)
	}
	loop.withRegion(loop.only(), func() { loop.checkInsideBlocks(ie) })
	if _, ok := loop.only().entities.get(ie.id); ok {
		t.Fatalf("cactus (item): item NOT destroyed after 5 ticks -- ItemEntity.hurtServer discard did not fire")
	}
}

// TestMobIceFrictionSlides: a mob standing on ICE reads the 0.98 block friction (not the flat 0.6); a mob
// on stone reads 0.6 (byte-identical to the old hardcoded default -- the pig-oracle invariant).
func TestMobIceFrictionSlides(t *testing.T) {
	loop, _ := insideBlocksLoop(t)

	onStone := NewEntity(1, entity.Pig, 8.5, 64.0, 8.5)
	var fStone float32
	loop.withRegion(loop.only(), func() { fStone = loop.entityBlockFrictionBelow(onStone) })
	if fStone != 0.6 {
		t.Fatalf("friction: mob on stone got %v, want 0.6 (default, pig-oracle invariant)", fStone)
	}

	mgrOf(loop).SetBlock(pk.Position{X: 8, Y: 63, Z: 8}, block.ToStateID[block.Ice{}], dimMinY)
	onIce := NewEntity(2, entity.Pig, 8.5, 64.0, 8.5)
	var fIce float32
	loop.withRegion(loop.only(), func() { fIce = loop.entityBlockFrictionBelow(onIce) })
	if fIce != 0.98 {
		t.Fatalf("friction: mob on ice got %v, want 0.98 (ice slide)", fIce)
	}
}

// TestBlockSpeedFactor: soul_sand / honey_block below the feet yield 0.4; a normal floor (stone) yields 1.0
// (the byte-identical no-op the pig oracle relies on); soul_soil is NOT slowed (1.0).
func TestBlockSpeedFactor(t *testing.T) {
	loop, _ := insideBlocksLoop(t)

	onStone := NewEntity(1, entity.Pig, 8.5, 64.0, 8.5)
	var sfStone float32
	loop.withRegion(loop.only(), func() { sfStone = loop.entityBlockSpeedFactor(onStone) })
	if sfStone != 1.0 {
		t.Fatalf("speedFactor: mob on stone got %v, want 1.0 (no slowdown, pig-oracle invariant)", sfStone)
	}

	mgrOf(loop).SetBlock(pk.Position{X: 8, Y: 63, Z: 8}, block.ToStateID[block.SoulSand{}], dimMinY)
	onSoul := NewEntity(2, entity.Pig, 8.5, 64.0, 8.5)
	var sfSoul float32
	loop.withRegion(loop.only(), func() { sfSoul = loop.entityBlockSpeedFactor(onSoul) })
	if sfSoul != 0.4 {
		t.Fatalf("speedFactor: mob on soul_sand got %v, want 0.4", sfSoul)
	}

	mgrOf(loop).SetBlock(pk.Position{X: 8, Y: 63, Z: 8}, block.ToStateID[block.SoulSoil{}], dimMinY)
	onSoil := NewEntity(3, entity.Pig, 8.5, 64.0, 8.5)
	var sfSoil float32
	loop.withRegion(loop.only(), func() { sfSoil = loop.entityBlockSpeedFactor(onSoil) })
	if sfSoil != 1.0 {
		t.Fatalf("speedFactor: mob on soul_soil got %v, want 1.0 (soul_soil is NOT slowed)", sfSoil)
	}
}
