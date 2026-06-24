package server

// ai_mob_test.go — AI-01 Task 2 tests: the ported passive Pig goals (RandomStroll /
// LookAtPlayer / RandomLookAround) + the per-mob serverAiStep-order driver. These assert the
// jar-confirmed behavior read this session from javap (RandomStrollGoal / LookAtPlayerGoal /
// RandomLookAroundGoal / animal.pig.Pig.registerGoals / Mob.serverAiStep).

import (
	"math"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

// TestRandomStrollSetsTarget: when the stroll goal can use, it sets a wantTarget (x,y,z)
// within its radius on the mob AI (it does NOT move the mob) and holds the MOVE flag. The
// goal's start() is the wantTarget write (vanilla start() calls navigation.moveTo(wanted…)).
func TestRandomStrollSetsTarget(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := NewEntity(1, entity.Pig, 100, 64, 200)
	m := newPigAI()
	e.ai = m

	// Force the stroll goal to fire deterministically (vanilla rolls a 1-in-interval chance).
	stroll := findStroll(t, m)
	stroll.forceTrigger = true

	startX, startY, startZ := e.x, e.y, e.z
	m.serverAiStep(loop, e)

	if !m.hasTarget {
		t.Fatal("stroll should have set a wantTarget")
	}
	// The mob itself must NOT have moved (a goal sets a target; motion is 07-02).
	if e.x != startX || e.y != startY || e.z != startZ {
		t.Fatalf("a goal must not move the mob: pos changed to (%v,%v,%v)", e.x, e.y, e.z)
	}
	// The target is within the stroll radius (10 horizontal, 7 vertical from getPosition).
	if math.Abs(m.wantX-startX) > 10 || math.Abs(m.wantZ-startZ) > 10 || math.Abs(m.wantY-startY) > 7 {
		t.Fatalf("wantTarget (%v,%v,%v) outside stroll radius of start (%v,%v,%v)",
			m.wantX, m.wantY, m.wantZ, startX, startY, startZ)
	}
	if stroll.flags()&flagMove == 0 {
		t.Fatal("stroll goal must claim the MOVE flag")
	}
}

// TestLookAtPlayerFacesNearest: with a player nearby, the lookAtPlayer goal faces the mob
// toward that player (sets headYaw/yaw); with no player in range canUse() is false. Holds LOOK.
func TestLookAtPlayerFacesNearest(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := NewEntity(1, entity.Pig, 0, 64, 0)
	m := newPigAI()
	e.ai = m

	look := findLookAtPlayer(t, m)

	// No player in range -> canUse is false.
	if look.canUse(loop, e) {
		t.Fatal("lookAtPlayer.canUse must be false with no player in range")
	}

	// Add a player due east (+X) within the 6.0 look distance.
	loop.players = append(loop.players, &tickPlayer{x: 4, y: 64, z: 0})

	// Force the probability roll to always pass so canUse is deterministic.
	look.alwaysLook = true
	if !look.canUse(loop, e) {
		t.Fatal("lookAtPlayer.canUse must be true with a player in range")
	}
	look.start(loop, e)
	look.tick(loop, e)

	// In MC yaw convention 0=+Z(south), 90=-X(west), 270/-90=+X(east). A player at +X should
	// make the pig face roughly east (yaw ~ -90 / 270). We assert the head turned toward +X:
	// the yaw points the body's +X-facing direction. Use the same yawTowardDeg the goal uses.
	want := yawTowardDeg(4-0, 0-0)
	if math.Abs(float64(e.headYaw-want)) > 0.001 {
		t.Fatalf("headYaw should face the player: got %v want %v", e.headYaw, want)
	}
	if look.flags()&flagLook == 0 {
		t.Fatal("lookAtPlayer goal must claim the LOOK flag")
	}
}

// TestServerAiStepOrder: the per-mob driver calls goalSelector.tick THEN tickRunningGoals
// (the jar-confirmed Mob.serverAiStep order; targetSelector is skipped for the passive v1
// mob). A probe goal records the order of its lifecycle calls.
func TestServerAiStepOrder(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	e := NewEntity(1, entity.Pig, 0, 64, 0)

	var order []string
	probe := &orderProbe{rec: &order}

	m := &mobAI{}
	m.goals.addGoal(0, probe)
	e.ai = m

	m.serverAiStep(loop, e)

	// start (from goalSelector.tick pass 2) must precede the tick calls. The driver runs
	// goalSelector.tick (which internally ticks once) then an explicit tickRunningGoals.
	if len(order) < 2 || order[0] != "start" {
		t.Fatalf("expected start first, got %v", order)
	}
	// Every entry after start must be a tick (the goal only starts once, then ticks).
	for _, o := range order[1:] {
		if o != "tick" {
			t.Fatalf("after start only tick calls expected, got %v", order)
		}
	}
}

// TestPigGoalSetRegistered: newPigAI registers the v1 passive goal set at the priorities read
// from javap animal.pig.Pig.registerGoals — WaterAvoidingRandomStroll@6 [MOVE],
// LookAtPlayer@7 [LOOK], RandomLookAround@8 [MOVE|LOOK].
func TestPigGoalSetRegistered(t *testing.T) {
	m := newPigAI()
	if got := len(m.goals.goals); got != 3 {
		t.Fatalf("pig v1 goal set should have 3 goals, got %d", got)
	}
	// Assert the recorded priorities + that the three goal types are present.
	byPriority := map[int]Goal{}
	for _, wg := range m.goals.goals {
		byPriority[wg.priority] = wg.g
	}
	if _, ok := byPriority[6].(*randomStrollGoal); !ok {
		t.Fatalf("expected randomStrollGoal at priority 6, got %T", byPriority[6])
	}
	if _, ok := byPriority[7].(*lookAtPlayerGoal); !ok {
		t.Fatalf("expected lookAtPlayerGoal at priority 7, got %T", byPriority[7])
	}
	if _, ok := byPriority[8].(*randomLookAroundGoal); !ok {
		t.Fatalf("expected randomLookAroundGoal at priority 8, got %T", byPriority[8])
	}
}

// --- test helpers ---------------------------------------------------------------------

// orderProbe is a goal that records its lifecycle call order for the serverAiStep test.
type orderProbe struct {
	baseGoal
	rec *[]string
}

func (p *orderProbe) canUse(*TickLoop, *Entity) bool           { return true }
func (p *orderProbe) canContinueToUse(*TickLoop, *Entity) bool { return true }
func (p *orderProbe) start(*TickLoop, *Entity)                 { *p.rec = append(*p.rec, "start") }
func (p *orderProbe) tick(*TickLoop, *Entity)                  { *p.rec = append(*p.rec, "tick") }
func (p *orderProbe) requiresUpdateEveryTick() bool            { return true }
func (p *orderProbe) flags() goalFlag                          { return flagMove }

func findStroll(t *testing.T, m *mobAI) *randomStrollGoal {
	t.Helper()
	for _, wg := range m.goals.goals {
		if g, ok := wg.g.(*randomStrollGoal); ok {
			return g
		}
	}
	t.Fatal("no randomStrollGoal registered")
	return nil
}

func findLookAtPlayer(t *testing.T, m *mobAI) *lookAtPlayerGoal {
	t.Helper()
	for _, wg := range m.goals.goals {
		if g, ok := wg.g.(*lookAtPlayerGoal); ok {
			return g
		}
	}
	t.Fatal("no lookAtPlayerGoal registered")
	return nil
}
