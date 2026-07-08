package server

// end_gateway_test.go -- deterministic pins for the End Gateway BE + the post-dragon-death ring
// spawn (1:1 javap this task). Verifies the 96-block ring math, the gateway block + bedrock frame,
// the SPAWN_TIME/COOLDOWN gate, and the dragon-death spawnNewGateway hook.

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world"
)

// endGatewayLoop builds a loop with an End world ready for gateway placement.
func endGatewayLoop() *TickLoop {
	loop := NewTickLoop(newFakeClock())
	for _, r := range loop.regions {
		r.world = world.NewChunkManager()
	}
	loop.endWorld = world.NewChunkManager()
	return loop
}

// putEndChunk inserts an empty (full-status) End chunk at col so SetBlock resolves there.
func putEndChunk(loop *TickLoop, col level.ChunkPos) {
	ch := level.EmptyChunk(dimEndSecs)
	ch.Status = level.StatusFull
	loop.endWorld.Insert(col, ch)
}

// seedEndColumns inserts empty End chunks spanning the ring (|x|,|z| <= 128) so gateway placement lands.
func seedEndColumns(loop *TickLoop) {
	for cx := int32(-9); cx <= 9; cx++ {
		for cz := int32(-9); cz <= 9; cz++ {
			putEndChunk(loop, level.ChunkPos{cx, cz})
		}
	}
}

// TestGatewayRingMath: spawnNewGateway(idx) places a gateway at x/z = floor(96 * cos/sin(2*(-PI +
// PI/20*idx))) and y 75, with the gateway block at center + a bedrock frame around it. Cite
// EnderDragonFight.spawnNewGateway.
func TestGatewayRingMath(t *testing.T) {
	loop := endGatewayLoop()
	seedEndColumns(loop)
	idx := 0
	loop.spawnNewGateway(idx)
	angle := 2.0 * (-math.Pi + gatewayRingAngleStep*float64(idx))
	wantX := int(math.Floor(gatewayRingRadius * math.Cos(angle)))
	wantZ := int(math.Floor(gatewayRingRadius * math.Sin(angle)))
	pos := pk.Position{X: wantX, Y: gatewayRingY, Z: wantZ}
	st, ok := loop.endWorld.GetBlock(pos, dimEndMinY)
	if !ok || st != block.ToStateID[block.EndGateway{}] {
		t.Fatalf("no END_GATEWAY block at ring pos %v (idx 0)", pos)
	}
	if _, live := loop.gateways[pos]; !live {
		t.Fatalf("no live gatewayBE registered at %v", pos)
	}
	// a bedrock frame neighbor (below the gateway) was placed.
	below := pk.Position{X: wantX, Y: gatewayRingY - 1, Z: wantZ}
	if st, ok := loop.endWorld.GetBlock(below, dimEndMinY); !ok || st != block.ToStateID[block.Bedrock{}] {
		t.Fatalf("no bedrock frame below the gateway at %v", below)
	}
}

// TestGatewaySpawnAndCooldownGate: a freshly spawned gateway is SPAWNING (age < 200) so it does not
// teleport; after age >= SPAWN_TIME + off cooldown it becomes eligible. Cite portalTick.
func TestGatewaySpawnAndCooldownGate(t *testing.T) {
	g := &gatewayBE{}
	if !g.gatewayIsSpawning() {
		t.Fatal("fresh gateway not spawning (age 0 < 200)")
	}
	g.age = gatewaySpawnTime
	if g.gatewayIsSpawning() {
		t.Fatal("gateway still spawning at age == SPAWN_TIME")
	}
	g.teleportCooldown = gatewayCooldownTime
	if !g.gatewayIsCoolingDown() {
		t.Fatal("gateway not cooling down with teleportCooldown == 40")
	}
}

// TestDragonDeathSpawnsGateway: the dragon death sequence (dragonSpawnExitPortalAndEgg) spawns a
// gateway on the ring + increments endGatewaysSpawned. Cite EnderDragonFight.setDragonKilled ->
// spawnNewGateway.
func TestDragonDeathSpawnsGateway(t *testing.T) {
	loop := endGatewayLoop()
	seedEndColumns(loop)
	d := loop.spawnEnderDragon(0, 128, 0)
	before := loop.endGatewaysSpawned
	loop.dragonSpawnExitPortalAndEgg(d)
	if loop.endGatewaysSpawned != before+1 {
		t.Fatalf("endGatewaysSpawned = %d, want %d after one dragon death", loop.endGatewaysSpawned, before+1)
	}
	if len(loop.gateways) == 0 {
		t.Fatal("dragon death placed no gateway")
	}
}
