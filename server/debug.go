package server

import (
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

	// navObservable arms the OFF-by-default "navigate toward a fixed point" trigger (07-06
	// interactive gate, SULFUR_DEBUG_NAV=1). With random strolling alone the pig's wander
	// target is unpredictable, so an operator cannot RELIABLY place a wall across its path to
	// watch the A* route around it. When armed, tickDebug periodically OVERRIDES the pig's
	// navigation target with a deterministic fixed point a fixed distance from spawn
	// (alternating between two anchors), forcing it to repeatedly traverse a known line — so
	// the operator walls that line off and SEES the ported A* (07-02) route around the
	// obstacle. It uses the SAME setWantTarget seam the real randomStrollGoal uses, so the
	// path that runs is the production navigation, only the destination is deterministic.
	navObservable bool
	// navTick counts ticks toward the next fixed-point retarget; navToggle alternates the
	// anchor so the pig paces a known segment back and forth.
	navTick   int
	navToggle bool
}

// debugNavEvery is the tick interval between fixed-point retargets when SULFUR_DEBUG_NAV is
// armed (~6s at 20 TPS — long enough for the pig to walk a ~12-block leg and for the operator
// to place/adjust a wall, short enough that the demo loops promptly).
const debugNavEvery = 120

// debugNavReach is the distance (blocks) from the pig's spawn column to each fixed nav anchor.
// The two anchors sit on opposite sides along +Z/-Z so the pig paces a straight ~2*reach line
// through its spawn — the operator walls that line to watch A* route around.
const debugNavReach = 12.0

// SetDebug arms the OFF-by-default debug triggers (the Plan 06-07 interactive gate). main()
// calls it before Run only when SULFUR_DEBUG=1: it spawns a visible, AI-driven pig near spawn so
// the operator can SEE entity movement + the ported AI. surfaceY is the world spawn surface (the
// same value handed to the generator + SetSpawn). Set-once at setup; the debug state is
// read/written only on the tick goroutine (TICK-05). With debug off (the default) every debug
// hook is a nil-check no-op.
//
// NOTE: the periodic player DAMAGE trigger is NOT armed here — it is opt-in via SetDebugDamage
// (SULFUR_DEBUG_DAMAGE=1). Without it, the operator can move/explore/observe the mob freely
// without being killed every ~20s (which would drop the death screen and freeze movement). Arm
// the damage only when specifically testing the health-bar / death / respawn loop.
func (t *TickLoop) SetDebug(surfaceY int) {
	t.debug = &debugConfig{
		spawnSurfaceY: surfaceY,
		// damageEvery defaults to 0 (no periodic damage) — opt in via SetDebugDamage.
	}
}

// SetDebugDamage arms the OFF-by-default periodic player-damage trigger (SULFUR_DEBUG_DAMAGE=1,
// in addition to SULFUR_DEBUG=1). It bites every living player ~1 heart / 2s so the operator can
// SEE the health bar drop and the death -> respawn loop. Left OFF by default so normal interactive
// testing (movement, mob observation, building) is not interrupted by death. A no-op if debug is
// off. Tick-owned.
func (t *TickLoop) SetDebugDamage() {
	if t.debug == nil {
		return
	}
	t.debug.damageEvery = 40   // ~2s at 20 TPS between bites
	t.debug.damageAmount = 2.0 // 1 heart per bite
}

// SetDebugNavObservable arms the OFF-by-default fixed-point navigation trigger (07-06
// interactive gate). main() calls it only when SULFUR_DEBUG_NAV=1 (in addition to
// SULFUR_DEBUG=1). It makes the debug pig deterministically pace a known straight line near
// spawn via the REAL ported navigation (setWantTarget -> A*), so an operator can wall the line
// and reliably OBSERVE the A* route around the obstacle (AI-02). A no-op if debug is off.
func (t *TickLoop) SetDebugNavObservable() {
	if t.debug == nil {
		return
	}
	t.debug.navObservable = true
}

// tickDebug runs the debug spawn/move/damage triggers when debug is armed. It is called from
// tickEntities (an EXISTING pipeline phase) so it adds no new phase to the fixed tick order.
// It is a cheap nil-check no-op when debug is off (production / every test).
func (t *TickLoop) tickDebug() {
	d := t.debug
	if d == nil || t.cur().entities == nil {
		return
	}

	// (1) Spawn ONE visible, vanilla-renderable pig near origin spawn, once per session, as
	// soon as a player is present to see it. The pig stands on the surface a few blocks north
	// of the origin column center so it is in front of a spawning player and inside trackRange.
	//
	// AI-03 (the STANDING MANDATE) + PLUGIN-04 (the SWAP): the debug pig's MOTION comes from the
	// REAL ported AI — now the PLUGIN-DRIVEN vanilla pig (spawnVanillaPig: the 3 passive goals
	// re-expressed 1:1 in Starlark, behavior-identical to the old Go builder), so tickAI's
	// serverAiStep drives it to WANDER and NAVIGATE exactly like a naturally-spawned mob. The
	// throwaway sinusoidal pacing (sinApprox/cosApprox) that used to move it is RETIRED (see
	// note below). The SULFUR_DEBUG spawn trigger remains only to GUARANTEE a visible mob near
	// spawn for the 07-06 interactive check — the behavior it shows is vanilla AI, not a sine.
	// Spawn ONLY once the pig's column (0,0) is actually LOADED — the chunk is generated
	// off-tick (async worker), so spawning the instant a player joins can place the pig before
	// its floor exists: the navigation snapshot would read all-air (no path → the pig stands
	// still) and gravity would drop it through the un-generated floor. Gating on the loaded
	// column guarantees solid ground under the pig and a non-degenerate A* snapshot.
	if !d.pigSpawned && len(t.players) > 0 && t.world() != nil {
		if _, loaded := t.world().Get(level.ChunkPos{0, 0}); loaded {
			px := 8.5 // origin column center X
			pz := 4.5 // a few blocks toward -Z from the player's 8.5 spawn Z
			py := float64(d.spawnSurfaceY + 1)
			// SWAP (PLUGIN-04 / Plan 24-02): the debug pig is the PLUGIN-DRIVEN vanilla_pig (the 3
			// passive goals re-expressed 1:1), built from the boot-loaded declaration via
			// spawnVanillaPig — NOT the Go newPigAI. It renders as entity.Pig.ID and tickAI's
			// serverAiStep wanders + navigates it exactly like a naturally-spawned plugin pig.
			pig := t.spawnVanillaPig(px, py, pz)
			d.pigID = pig.id
			d.pigSpawned = true
		}
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

	// (2b) OBSERVABLE NAVIGATION (07-06 AI-02, SULFUR_DEBUG_NAV=1): deterministically retarget
	// the debug pig to a fixed point a known distance from its spawn column, alternating between
	// two anchors so it paces a straight ~2*reach line. This drives the SAME ported navigation
	// the randomStrollGoal uses (setWantTarget -> serverAiStep's A* requestPath), only with a
	// DETERMINISTIC destination — so the operator walls the known line and reliably watches the
	// A* route around it (random strolling alone rarely crosses a placed wall predictably). The
	// retarget only sets the want-target; the path computation + the move + the tracker
	// broadcast all run in tickAI/moveEntity on the tick goroutine (TICK-05), exactly as for a
	// natural mob.
	if d.navObservable && d.pigSpawned && t.cur().entities != nil {
		d.navTick++
		if d.navTick >= debugNavEvery {
			d.navTick = 0
			if pig, ok := t.cur().entities.get(d.pigID); ok && pig != nil && pig.ai != nil {
				// The two anchors sit along ±Z from the pig's spawn Z (4.5), at the same X/Y, so
				// the pig walks a known straight segment the operator can wall off.
				const spawnX, spawnZ = 8.5, 4.5
				tz := spawnZ + debugNavReach
				if d.navToggle {
					tz = spawnZ - debugNavReach
				}
				d.navToggle = !d.navToggle
				pig.ai.setWantTarget(spawnX, float64(d.spawnSurfaceY+1), tz)
			}
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

// NOTE: the sinApprox/cosApprox sinusoidal-mover helpers that used to pace the debug pig were
// REMOVED in 07-03 (AI-03): the debug pig is now driven by the real ported GoalSelector + A*
// navigation (tickAI -> serverAiStep), so there is no cosmetic oscillation to compute. If a
// future debug trigger needs trig it should use math.Sin/Cos directly rather than reviving a
// throwaway approximation.

// ensure level import is used even if a future edit drops the only reference; the debug
// spawn uses level.ChunkPos indirectly via entities.add/move bucketing semantics.
var _ = level.ChunkPos{}
