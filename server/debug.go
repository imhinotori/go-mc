package server

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
)

// debug.go wires the OPTIONAL, off-by-default debug triggers the Plan 06-07 interactive
// human-verify gate needs to OBSERVE the Phase-6 milestone in a real vanilla 26.2 client:
//
//   - a VISIBLE, MOVING entity spawned near each joining player (so the operator SEES the
//     entityTracker's Add / Teleport / Remove working end-to-end — ENT-01/02), and
//   - periodic DAMAGE applied to each player (so the operator SEES the health bar drop and
//     the death-screen -> respawn loop — ENT-05/06).
//
// Both are gated behind TickLoop.debug: it is nil in production and in every test (so the
// fixed tick-phase pipeline and the -race gate are unaffected), and is armed only when
// cmd/sulfur is started with SULFUR_DEBUG=1. The debug entity uses a VANILLA-RENDERABLE type
// (minecraft:pig, type id 100) — NOT the custom SulfurCube (type 130), which a vanilla client
// has no renderer for — so the operator actually sees a pig, the decisive ENT-01 visual.
//
// Everything here runs ON the tick goroutine over tick-owned state (the entityStore, the
// players slice), folded into the existing tickEntities seam — it spawns NO goroutine and
// adds NO new tick phase (TestTickPhaseOrder's fixed pipeline is preserved), so it is -race
// clean by the same single-owner discipline as the rest of the loop (TICK-05).

// debugConfig holds the tick-owned debug-trigger state. A nil *debugConfig on the loop means
// debug is OFF (production / tests). All fields are touched only on the tick goroutine.
type debugConfig struct {
	// spawnSurfaceY is the surface block world-Y the debug pig stands on (matches the world
	// generator surface so the pig rests on the floor a couple blocks from the player).
	spawnSurfaceY int

	// pigSpawned marks that the per-session debug pig has been spawned (one pig for the
	// session, near origin spawn). Its id lets the move logic find it each tick.
	pigSpawned bool
	pigID      int32

	// pigPhase advances each tick; the pig's X oscillates with it so it visibly paces back
	// and forth in front of the player (the tracker emits TeleportEntity for the move).
	pigPhase float64

	// damageEvery is the tick interval between debug damage applications; damageTick counts
	// ticks toward the next hit. A non-zero interval drives the health bar down so the
	// operator sees damage -> death -> respawn without needing a real damage source.
	damageEvery int
	damageTick  int

	// damageAmount is the HP removed per debug hit (a small bite so the health bar visibly
	// steps down over several seconds before death).
	damageAmount float32
}

// SetDebug arms the OFF-by-default debug triggers (the Plan 06-07 interactive gate). main()
// calls it before Run only when SULFUR_DEBUG=1: it spawns a visible, moving pig near spawn and
// periodically damages players so the operator can SEE entity movement, the health bar drop,
// and the death/respawn loop. surfaceY is the world spawn surface (the same value handed to
// the generator + SetSpawn). Set-once at setup; the debug state is read/written only on the
// tick goroutine (TICK-05). With debug off (the default) every debug hook is a nil-check no-op.
func (t *TickLoop) SetDebug(surfaceY int) {
	t.debug = &debugConfig{
		spawnSurfaceY: surfaceY,
		damageEvery:   40,  // ~2s at 20 TPS between bites
		damageAmount:  2.0, // 1 heart per bite: 10 bites (~20s) from full to death
	}
}

// tickDebug runs the debug spawn/move/damage triggers when debug is armed. It is called from
// tickEntities (an EXISTING pipeline phase) so it adds no new phase to the fixed tick order.
// It is a cheap nil-check no-op when debug is off (production / every test).
func (t *TickLoop) tickDebug() {
	d := t.debug
	if d == nil || t.entities == nil {
		return
	}

	// (1) Spawn ONE visible, vanilla-renderable pig near origin spawn, once per session, as
	// soon as a player is present to see it. The pig stands on the surface a few blocks north
	// of the origin column center so it is in front of a spawning player and inside trackRange.
	if !d.pigSpawned && len(t.players) > 0 {
		id := t.idAlloc.AllocID()
		px := 8.5            // origin column center X
		pz := 4.5            // a few blocks toward -Z from the player's 8.5 spawn Z
		py := float64(d.spawnSurfaceY + 1)
		pig := NewEntity(id, entity.Pig, px, py, pz)
		t.entities.add(pig)
		d.pigID = id
		d.pigSpawned = true
	}

	// (2) Move the pig every tick so the operator SEES it pace (the tracker emits an absolute
	// TeleportEntity for a moved, tracked entity). Oscillate X around the spawn column center;
	// route the position change through entities.move so the per-section bucket stays consistent
	// for the tracker's near() (the bucket-consistency contract).
	if d.pigSpawned {
		if pig, ok := t.entities.get(d.pigID); ok {
			d.pigPhase += 0.15
			// Pace ±3 blocks along X; keep Y on the surface and Z fixed.
			newX := 8.5 + 3.0*sinApprox(d.pigPhase)
			// Face the direction of travel so the head visibly turns.
			if cosApprox(d.pigPhase) >= 0 {
				pig.yaw, pig.headYaw = 90, 90
			} else {
				pig.yaw, pig.headYaw = 270, 270
			}
			t.entities.move(pig, newX, pig.y, pig.z)
		}
	}

	// (3) Periodically bite every living player so the health bar drops and, after enough
	// bites, the death screen appears (a subsequent client Respawn request runs performRespawn).
	if d.damageEvery > 0 {
		d.damageTick++
		if d.damageTick >= d.damageEvery {
			d.damageTick = 0
			for _, p := range t.players {
				if p != nil && !p.dead {
					t.applyDamage(p, d.damageAmount)
				}
			}
		}
	}
}

// sinApprox / cosApprox are tiny wire-irrelevant trig helpers for the debug pig's pacing.
// They avoid importing math into the hot path for a debug-only oscillation; a low-order
// polynomial is plenty for a visible back-and-forth. (Range-reduced to [-pi, pi].)
func sinApprox(x float64) float64 {
	const twoPi = 6.283185307179586
	const pi = 3.141592653589793
	// reduce to [-pi, pi]
	for x > pi {
		x -= twoPi
	}
	for x < -pi {
		x += twoPi
	}
	// Bhaskara I's sine approximation (good to ~0.2% over [0,pi]); odd-extend for negatives.
	neg := false
	if x < 0 {
		x = -x
		neg = true
	}
	y := 16 * x * (pi - x) / (5*pi*pi - 4*x*(pi-x))
	if neg {
		return -y
	}
	return y
}

func cosApprox(x float64) float64 {
	const halfPi = 1.5707963267948966
	return sinApprox(x + halfPi)
}

// ensure level import is used even if a future edit drops the only reference; the debug
// spawn uses level.ChunkPos indirectly via entities.add/move bucketing semantics.
var _ = level.ChunkPos{}
