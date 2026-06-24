package server

import (
	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/component"
)

// debugHotbarSlot0 is the player-inventory WINDOW slot for hotbar position 0: the 46-slot
// window lays out 1 craft-result + 4 craft-grid (slots 0-4) + 4 armor (5-8) + 1 offhand?...
// the standard mapping places the 9 hotbar slots at window indices 36-44, so hotbar 0 == 36.
const debugHotbarSlot0 = 36

// debugStoneItemID is minecraft:stone's protocol item id (data/item.Stone.ID == 1). Used to
// hand the operator a placeable block for the interactive place check.
const debugStoneItemID = 1

// debugStoneStack builds a component-free ItemStack of 64 stone (count + id, no added/removed
// components) — the minimal SlotData form the wire encoder already round-trips.
func debugStoneStack() component.SlotData {
	return component.SlotData{Count: 64, ItemID: debugStoneItemID}
}

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
	// session, near origin spawn). Its id is recorded for reference; the pig's MOTION is now
	// driven by the real ported AI (tickAI -> serverAiStep), not by any per-tick logic here
	// (the sinusoidal mover was retired in 07-03 / AI-03).
	pigSpawned bool
	pigID      int32

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
	//
	// AI-03 (the STANDING MANDATE): the debug pig's MOTION now comes from the REAL ported AI —
	// it is given a newPigAI() (07-01 GoalSelector + 07-02 A* navigation), so tickAI's
	// serverAiStep drives it to WANDER and NAVIGATE exactly like a naturally-spawned mob. The
	// throwaway sinusoidal pacing (sinApprox/cosApprox) that used to move it is RETIRED (see
	// note below). The SULFUR_DEBUG spawn trigger remains only to GUARANTEE a visible mob near
	// spawn for the 07-06 interactive check — the behavior it shows is vanilla AI, not a sine.
	if !d.pigSpawned && len(t.players) > 0 {
		id := t.idAlloc.AllocID()
		px := 8.5            // origin column center X
		pz := 4.5            // a few blocks toward -Z from the player's 8.5 spawn Z
		py := float64(d.spawnSurfaceY + 1)
		pig := NewEntity(id, entity.Pig, px, py, pz)
		pig.ai = newPigAI() // REAL ported AI: tickAI's serverAiStep wanders + navigates it
		t.entities.add(pig)
		d.pigID = id
		d.pigSpawned = true
	}

	// (1b) Give every player a stack of stone in the first hotbar slot once, so the operator
	// has a placeable block in hand (the v1 inventory is otherwise empty, and a survival client
	// will not emit ServerboundUseItemOn — the place packet — without a placeable item held).
	// Hotbar slot 0 maps to inventory window slot 36 (4 craft + 4 armor + 27 main precede it).
	// The set + authoritative ContainerSetContent run on the tick goroutine over tick-owned
	// inventory state (TICK-05); marking it per-player keeps it idempotent across reconnects.
	for _, p := range t.players {
		if p == nil || p.debugGaveItems {
			continue
		}
		inv := ensureInventory(p)
		inv.set(debugHotbarSlot0, debugStoneStack())
		t.sendContent(p)
		p.debugGaveItems = true
	}

	// (2) The pig's MOTION is RETIRED here and delegated to the REAL AI: tickAI's serverAiStep
	// (07-01 goals + 07-02 A* navigation) drives the debug pig's wander/navigation each tick,
	// the same as any naturally-spawned mob (AI-03 / the STANDING MANDATE). The old throwaway
	// sinusoidal pacing (d.pigPhase + sinApprox/cosApprox) is GONE — the debug pig now shows
	// VANILLA behavior (amble + obstacle navigation), not a cosmetic sine oscillation, so the
	// 07-06 interactive check observes the real ported AI. No motion code lives here anymore;
	// the move + the tracker broadcast happen inside tickAI/moveEntity on the tick goroutine.

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

// NOTE: the sinApprox/cosApprox sinusoidal-mover helpers that used to pace the debug pig were
// REMOVED in 07-03 (AI-03): the debug pig is now driven by the real ported GoalSelector + A*
// navigation (tickAI -> serverAiStep), so there is no cosmetic oscillation to compute. If a
// future debug trigger needs trig it should use math.Sin/Cos directly rather than reviving a
// throwaway approximation.

// ensure level import is used even if a future edit drops the only reference; the debug
// spawn uses level.ChunkPos indirectly via entities.add/move bucketing semantics.
var _ = level.ChunkPos{}
