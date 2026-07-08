package server

// end_gateway_be.go -- the End Gateway block-entity (net.minecraft.world.level.block.entity.
// TheEndGatewayBlockEntity) + the post-dragon-death gateway spawn (EnderDragonFight.spawnNewGateway),
// a 1:1 port from the unobfuscated 26.2 jar (javap -c -p this task). A gateway, after a SPAWN_TIME
// warm-up and once its teleportCooldown is 0, TELEPORTS an entity standing in it to a paired exit.
// It follows the beacon BE pattern: a live gatewayBE per world position in t.gateways, ticked by
// tickGateways.
//
// VANILLA (verified javap this task):
//   EnderDragonFight.gateways = shuffledCopy(ContiguousSet[0,20), levelRng) (20 indices, shuffled).
//   spawnNewGateway() (on the dragon kill): if empty return; idx = gateways.remove(size-1);
//     x = Mth.floor(96.0 * cos(2.0 * (-PI + 0.15707963267948966 * idx)));
//     z = Mth.floor(96.0 * sin(2.0 * (-PI + 0.15707963267948966 * idx))); spawnNewGateway(BlockPos(x,75,z)).
//     (0.15707963267948966 == PI/20.)
//   spawnNewGateway(BlockPos): levelEvent(3000, pos, 0) + EndGatewayFeature: a bedrock frame around
//     the gateway block with END_GATEWAY at center.
//   TheEndGatewayBlockEntity: SPAWN_TIME 200, COOLDOWN_TIME 40; portalTick: age++; if isSpawning() OR
//     isCoolingDown() decrement; else teleport entities in the gateway block + triggerCooldown.
//
// LANDED (bytecode-exact): the 96-block ring math, the gateway block + a bedrock frame, the SPAWN_TIME
// 200 + COOLDOWN_TIME 40 gate, the teleport-in-gateway -> exit + 40-tick cooldown. Wired into the
// dragon death so a gateway appears on the ring after the dragon dies.
//
// DEFERRED (cited): the full findOrCreateValidTeleportPos outer-island generation is reduced to a
// fixed radial exit far out; the beam animation is a cite-deferred visual; the return-gateway (exact
// [100,50,0]) is available via spawnReturnGateway.

import (
	"math"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// End Gateway constants (VERIFIED javap TheEndGatewayBlockEntity + EnderDragonFight this task).
const (
	gatewaySpawnTime     = 200
	gatewayCooldownTime  = 40
	gatewayRingRadius    = 96.0
	gatewayRingAngleStep = 0.15707963267948966
	gatewayRingY         = 75
	gatewayCount         = 20
	gatewayExitRadius    = 1024.0
)

// gatewayBE is the tick-owned state of one END_GATEWAY block-entity (the beaconBE twin). Cite
// TheEndGatewayBlockEntity fields (age, teleportCooldown, exitPortal, exactTeleport).
type gatewayBE struct {
	age                 int64
	teleportCooldown    int
	exitX, exitY, exitZ float64
	exactTeleport       bool
}

func (g *gatewayBE) gatewayIsSpawning() bool { return g.age < gatewaySpawnTime }

func (g *gatewayBE) gatewayIsCoolingDown() bool { return g.teleportCooldown > 0 }

// spawnEndGateway places an END_GATEWAY block + a bedrock frame + a live gatewayBE at pos in the End
// world, exiting to (exitX,exitY,exitZ). Ports EndGatewayFeature.place + setExitPosition. Tick-owned.
func (t *TickLoop) spawnEndGateway(pos pk.Position, exitX, exitY, exitZ float64, exact bool) {
	mgr := t.endWorld
	if mgr == nil {
		return
	}
	if t.gateways == nil {
		t.gateways = make(map[pk.Position]*gatewayBE)
	}
	gateway := block.ToStateID[block.EndGateway{}]
	bedrock := block.ToStateID[block.Bedrock{}]
	for dy := -1; dy <= 1; dy++ {
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				if dx == 0 && dy == 0 && dz == 0 {
					continue
				}
				mgr.SetBlock(pk.Position{X: pos.X + dx, Y: pos.Y + dy, Z: pos.Z + dz}, bedrock, dimEndMinY)
			}
		}
	}
	mgr.SetBlock(pos, gateway, dimEndMinY)
	t.gateways[pos] = &gatewayBE{exitX: exitX, exitY: exitY, exitZ: exitZ, exactTeleport: exact}
}

// spawnNewGateway ports EnderDragonFight.spawnNewGateway(): place a gateway on the 96-block ring at
// y 75, x/z = floor(96 * cos/sin(2*(-PI + PI/20*idx))). Cite EnderDragonFight.spawnNewGateway.
func (t *TickLoop) spawnNewGateway(idx int) {
	angle := 2.0 * (-math.Pi + gatewayRingAngleStep*float64(idx))
	x := int(math.Floor(gatewayRingRadius * math.Cos(angle)))
	z := int(math.Floor(gatewayRingRadius * math.Sin(angle)))
	pos := pk.Position{X: x, Y: gatewayRingY, Z: z}
	dir := math.Atan2(float64(z), float64(x))
	exitX := gatewayExitRadius * math.Cos(dir)
	exitZ := gatewayExitRadius * math.Sin(dir)
	t.spawnEndGateway(pos, exitX+0.5, gatewayRingY, exitZ+0.5, false)
}

// spawnReturnGateway places the RETURN gateway (exact teleport to the End spawn platform [100,50,0]).
// Cite end_gateway_return.json (exact:true, exit:[100,50,0]).
func (t *TickLoop) spawnReturnGateway(pos pk.Position) {
	t.spawnEndGateway(pos, 100.5, 50.0, 0.5, true)
}

// tickGateways ticks every live END_GATEWAY block-entity once per tick (the tickBeacons twin). Cite
// TheEndGatewayBlockEntity.portalTick.
func (t *TickLoop) tickGateways() {
	if len(t.gateways) == 0 {
		return
	}
	mgr := t.endWorld
	if mgr == nil {
		return
	}
	for pos, g := range t.gateways {
		state, ok := mgr.GetBlock(pos, dimEndMinY)
		if !ok || state != block.ToStateID[block.EndGateway{}] {
			delete(t.gateways, pos)
			continue
		}
		t.gatewayServerTick(pos, g)
	}
}

// gatewayServerTick ports TheEndGatewayBlockEntity.portalTick: age++; if cooling down decrement; else
// once spawned in (age >= SPAWN_TIME) teleport any player in the gateway block + triggerCooldown (40).
// Cite portalTick + teleportEntity + triggerCooldown.
func (t *TickLoop) gatewayServerTick(pos pk.Position, g *gatewayBE) {
	g.age++
	if g.gatewayIsCoolingDown() {
		g.teleportCooldown--
		return
	}
	if g.gatewayIsSpawning() {
		return
	}
	for _, p := range t.players {
		if p == nil || p.dead || p.dimension != dimEnd {
			continue
		}
		if int(math.Floor(p.x)) != pos.X || int(math.Floor(p.z)) != pos.Z {
			continue
		}
		fy := int(math.Floor(p.y))
		if fy != pos.Y && fy != pos.Y+1 {
			continue
		}
		t.teleportPlayer(p, g.exitX, g.exitY, g.exitZ)
		g.teleportCooldown = gatewayCooldownTime
	}
}
