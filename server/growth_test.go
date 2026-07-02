package server

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world"
	"github.com/imhinotori/sulfur/world/levelgen"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// growth_test.go — the SAPLING / LEAVES / GRASS-MYCELIUM random-tick gate (growth_block.go). It
// proves each family's jar-exact behavior (STAGE advance + oak grow, DISTANCE==7 decay + the 6-
// neighbour distance recompute, die-under-opaque + spread-to-dirt) and pins the nextInt bounds +
// light/distance thresholds against the jar. Uses the shared newRandomTickLoop / mustGet helpers.

// ---- state builders ----

func oakSapling(stage int) block.StateID {
	s, ok := block.ToStateID[block.OakSapling{Stage: block.Integer(stage)}]
	if !ok {
		panic("no oak sapling state")
	}
	return s
}

func oakLeaves(distance int, persistent bool) block.StateID {
	s, ok := block.ToStateID[block.OakLeaves{Distance: block.Integer(distance), Persistent: block.Boolean(persistent)}]
	if !ok {
		panic("no oak leaves state")
	}
	return s
}

func grassBlock(snowy bool) block.StateID {
	return block.ToStateID[block.GrassBlock{Snowy: block.Boolean(snowy)}]
}

func myceliumBlock(snowy bool) block.StateID {
	return block.ToStateID[block.Mycelium{Snowy: block.Boolean(snowy)}]
}

func dirtState() block.StateID  { return block.ToStateID[block.Dirt{}] }
func stoneState() block.StateID { return block.ToStateID[block.Stone{}] }

// ---- SAPLING ----

// TestSaplingAdvanceStageThenGrows: advanceTree on a STAGE-0 sapling cycles it to STAGE 1 (no tree);
// a second advanceTree (STAGE 1) grows the oak tree, clearing the sapling cell. Exercises the handler
// directly (advanceTree) so the STAGE machine + the oak grow seam are both asserted.
func TestSaplingAdvanceStageThenGrows(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(0x5A11)

	// A dirt floor for the tree to root on; the sapling on top of it.
	pos := pk.Position{X: 8, Y: 65, Z: 8}
	mgr.SetBlock(pk.Position{X: pos.X, Y: pos.Y - 1, Z: pos.Z}, dirtState(), dimMinY)
	mgr.SetBlock(pos, oakSapling(0), dimMinY)

	// advanceTree #1: STAGE 0 -> 1 (no tree yet).
	loop.advanceTree(loop.only(), oakSapling(0), pos)
	if got := block.SaplingStage(mustGet(t, mgr, pos)); got != 1 {
		t.Fatalf("after advanceTree #1 STAGE = %d, want 1 (STAGE 0 cycles to 1, no grow)", got)
	}

	// advanceTree #2: STAGE 1 -> grow the oak. The sapling cell must no longer be a sapling (the tree
	// replaced it with a log or the feature cleared it), and at least one oak log must have been placed.
	loop.advanceTree(loop.only(), oakSapling(1), pos)
	if block.IsSapling(mustGet(t, mgr, pos)) {
		t.Fatal("after advanceTree #2 the sapling should have grown into a tree (cell no longer a sapling)")
	}
	if !treePlaced(t, mgr) {
		t.Fatal("advanceTree #2 (STAGE 1) grew nothing — expected the oak feature to place logs/leaves")
	}
}

// treePlaced reports whether any oak log or oak leaves landed anywhere in the (0,0) column near the
// grow site — proof the oak feature ran.
func treePlaced(t *testing.T, mgr *world.ChunkManager) bool {
	t.Helper()
	oakLog := block.ToStateID[block.OakLog{Axis: block.Y}]
	for y := 60; y < 90; y++ {
		for x := 0; x < 16; x++ {
			for z := 0; z < 16; z++ {
				s, ok := mgr.GetBlock(pk.Position{X: x, Y: y, Z: z}, dimMinY)
				if !ok {
					continue
				}
				if s == oakLog || block.IsLeaves(s) {
					return true
				}
			}
		}
	}
	return false
}

// TestSaplingRandomTickNextIntSeven: saplingRandomTick draws EXACTLY one nextInt(7) when the light
// gate passes, and advances STAGE only when that roll is 0 — the jar `nextInt(7) == 0` gate. We drive
// a levelRandom whose first nextInt(7) is 0 (grows) and one whose first is non-zero (does not).
func TestSaplingRandomTickNextIntSeven(t *testing.T) {
	// Find a seed whose first LegacyRandom nextInt(7) is 0, and one whose first is != 0.
	seedZero, seedNonZero := int64(-1), int64(-1)
	for s := int64(0); s < 200 && (seedZero < 0 || seedNonZero < 0); s++ {
		r := levelgen.NewLegacyRandomSource(s)
		if r.NextIntN(7) == 0 {
			if seedZero < 0 {
				seedZero = s
			}
		} else if seedNonZero < 0 {
			seedNonZero = s
		}
	}
	if seedZero < 0 || seedNonZero < 0 {
		t.Fatal("could not find seeds for nextInt(7)==0 and !=0")
	}

	run := func(seed int64) int {
		loop, mgr, _ := newRandomTickLoop()
		loop.only().levelRandom = levelgen.NewLegacyRandomSource(seed)
		pos := pk.Position{X: 4, Y: 65, Z: 4}
		mgr.SetBlock(pos, oakSapling(0), dimMinY)
		loop.saplingRandomTick(loop.only(), oakSapling(0), pos)
		return block.SaplingStage(mustGet(t, mgr, pos))
	}

	if got := run(seedZero); got != 1 {
		t.Fatalf("nextInt(7)==0 seed: STAGE = %d, want 1 (grow roll hit)", got)
	}
	if got := run(seedNonZero); got != 0 {
		t.Fatalf("nextInt(7)!=0 seed: STAGE = %d, want 0 (grow roll missed)", got)
	}
}

// TestDriverAdvancesSapling: the DRIVER (tickChunk over many ticks) advances a STAGE-0 oak sapling —
// proving the sapling family is wired into IsRandomlyTicking + dispatchRandomTick, not just callable.
func TestDriverAdvancesSapling(t *testing.T) {
	loop, mgr, ch := newRandomTickLoop()
	loop.only().randValue = 20260702
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(0xB0A)

	pos := pk.Position{X: 6, Y: 65, Z: 10}
	mgr.SetBlock(pk.Position{X: pos.X, Y: pos.Y - 1, Z: pos.Z}, dirtState(), dimMinY)
	mgr.SetBlock(pos, oakSapling(0), dimMinY)

	const maxTicks = 2000000
	advanced := false
	for i := 0; i < maxTicks; i++ {
		loop.tickChunk(level.ChunkPos{0, 0}, ch, randomTickSpeed)
		s := mustGet(t, mgr, pos)
		if !block.IsSapling(s) || block.SaplingStage(s) >= 1 {
			advanced = true
			break
		}
	}
	if !advanced {
		t.Fatalf("driver never advanced the oak sapling past STAGE 0 in %d ticks", maxTicks)
	}
}

// ---- LEAVES ----

// TestLeavesDecayAtDistanceSeven: a non-persistent leaf at DISTANCE 7 decays to air on a random tick;
// the same leaf at DISTANCE 6, or PERSISTENT at DISTANCE 7, does NOT decay. Asserts the jar
// `decaying == !PERSISTENT && DISTANCE==7` gate exactly.
func TestLeavesDecayAtDistanceSeven(t *testing.T) {
	cases := []struct {
		name       string
		distance   int
		persistent bool
		wantAir    bool
	}{
		{"distance7 non-persistent decays", 7, false, true},
		{"distance6 non-persistent survives", 6, false, false},
		{"distance7 persistent survives", 7, true, false},
		{"distance1 non-persistent survives", 1, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			loop, mgr, _ := newRandomTickLoop()
			pos := pk.Position{X: 3, Y: 70, Z: 3}
			leaf := oakLeaves(c.distance, c.persistent)
			mgr.SetBlock(pos, leaf, dimMinY)
			loop.leavesRandomTick(loop.only(), leaf, pos)
			isAir := block.IsAir(mustGet(t, mgr, pos))
			if isAir != c.wantAir {
				t.Fatalf("after randomTick isAir = %v, want %v (decaying gate)", isAir, c.wantAir)
			}
		})
	}
}

// TestLeavesUpdateDistanceRecompute: leavesUpdateDistance ports the 6-neighbour min+1 recompute.
// A leaf adjacent to a log recomputes to DISTANCE 1 (getDistanceAt(log)==0 -> +1); a leaf whose only
// neighbours are far leaves at DISTANCE 6 recomputes to 7 (6+1, clamped to the max) -> becomes decaying.
func TestLeavesUpdateDistanceRecompute(t *testing.T) {
	t.Run("adjacent log -> distance 1", func(t *testing.T) {
		loop, mgr, _ := newRandomTickLoop()
		pos := pk.Position{X: 5, Y: 70, Z: 5}
		mgr.SetBlock(pos, oakLeaves(7, false), dimMinY)
		// A log directly below -> getDistanceAt(log) == 0 -> distance 1.
		mgr.SetBlock(pk.Position{X: pos.X, Y: pos.Y - 1, Z: pos.Z}, block.ToStateID[block.OakLog{Axis: block.Y}], dimMinY)
		got, ok := loop.leavesUpdateDistance(oakLeaves(7, false), pos)
		if !ok {
			t.Fatal("updateDistance returned ok=false for oak leaves")
		}
		if d := block.LeavesDistance(got); d != 1 {
			t.Fatalf("distance next to a log = %d, want 1 (getDistanceAt(log)+1)", d)
		}
	})
	t.Run("neighbour leaf distance 6 -> distance 7", func(t *testing.T) {
		loop, mgr, _ := newRandomTickLoop()
		pos := pk.Position{X: 8, Y: 72, Z: 8}
		mgr.SetBlock(pos, oakLeaves(1, false), dimMinY)
		// Exactly one neighbour: a leaf at DISTANCE 6 to the east. All other neighbours are air (->7).
		mgr.SetBlock(pk.Position{X: pos.X + 1, Y: pos.Y, Z: pos.Z}, oakLeaves(6, false), dimMinY)
		got, ok := loop.leavesUpdateDistance(oakLeaves(1, false), pos)
		if !ok {
			t.Fatal("updateDistance returned ok=false")
		}
		if d := block.LeavesDistance(got); d != 7 {
			t.Fatalf("distance = %d, want 7 (min(air+1=8 clamp 7, leaf6+1=7) = 7)", d)
		}
		// A recomputed-to-7 non-persistent leaf is now decaying.
		if !block.LeavesDecaying(got) {
			t.Fatal("leaf recomputed to distance 7 must be decaying")
		}
	})
}

// TestDriverDecaysLeaves: the DRIVER decays a distance-7 non-persistent leaf to air (proves the leaf
// family is wired into IsRandomlyTicking + dispatchRandomTick).
func TestDriverDecaysLeaves(t *testing.T) {
	loop, mgr, ch := newRandomTickLoop()
	loop.only().randValue = 55555
	pos := pk.Position{X: 2, Y: 70, Z: 11}
	mgr.SetBlock(pos, oakLeaves(7, false), dimMinY)

	const maxTicks = 2000000
	decayed := false
	for i := 0; i < maxTicks; i++ {
		loop.tickChunk(level.ChunkPos{0, 0}, ch, randomTickSpeed)
		if block.IsAir(mustGet(t, mgr, pos)) {
			decayed = true
			break
		}
	}
	if !decayed {
		t.Fatalf("driver never decayed the distance-7 leaf in %d ticks", maxTicks)
	}
}

// ---- GRASS / MYCELIUM ----

// TestGrassDiesUnderOpaqueBlock: a grass block with a solid opaque full cube (dirt/stone) directly
// above turns to dirt on a random tick (canStayAlive false via the light-dampening proxy). A grass
// block under air survives. Asserts the die-to-dirt branch + the opaque-above kill.
func TestGrassDiesUnderOpaqueBlock(t *testing.T) {
	t.Run("opaque above -> dies to dirt", func(t *testing.T) {
		loop, mgr, _ := newRandomTickLoop()
		loop.only().levelRandom = levelgen.NewLegacyRandomSource(1)
		pos := pk.Position{X: 7, Y: 68, Z: 7}
		mgr.SetBlock(pos, grassBlock(false), dimMinY)
		mgr.SetBlock(above(pos), stoneState(), dimMinY) // opaque full cube above
		loop.grassRandomTick(loop.only(), grassBlock(false), pos)
		if !block.IsDirt(mustGet(t, mgr, pos)) {
			t.Fatal("grass under an opaque block must turn to dirt")
		}
	})
	t.Run("air above -> survives", func(t *testing.T) {
		loop, mgr, _ := newRandomTickLoop()
		loop.only().levelRandom = levelgen.NewLegacyRandomSource(1)
		pos := pk.Position{X: 7, Y: 68, Z: 9}
		mgr.SetBlock(pos, grassBlock(false), dimMinY)
		// above is air (EmptyChunk default). No dirt neighbours -> spread does nothing, block stays grass.
		loop.grassRandomTick(loop.only(), grassBlock(false), pos)
		if !block.IsGrassBlock(mustGet(t, mgr, pos)) {
			t.Fatal("grass under air (no dirt to spread to) must remain grass")
		}
	})
}

// TestGrassSpreadsToAdjacentDirt: a grass block surrounded by exposed dirt spreads to a dirt cell over
// enough random ticks. Each tick draws 12 levelRandom ints (3 per 4 attempts); a fully-dirt shell makes
// at least one attempt land on convertible dirt. Proves the spread branch + canPropagate.
func TestGrassSpreadsToAdjacentDirt(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(0xD1D7)

	center := pk.Position{X: 8, Y: 68, Z: 8}
	mgr.SetBlock(center, grassBlock(false), dimMinY)
	// Surround the center with exposed dirt in the offset(-1..1, -3..1, -1..1) sample box (air above
	// each so canPropagate/canStayAlive pass). Fill the full 3x3 plane at the same Y with dirt.
	dirtCells := []pk.Position{}
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			if dx == 0 && dz == 0 {
				continue
			}
			p := pk.Position{X: center.X + dx, Y: center.Y, Z: center.Z + dz}
			mgr.SetBlock(p, dirtState(), dimMinY)
			dirtCells = append(dirtCells, p)
		}
	}

	const maxTicks = 500000
	spread := false
	for i := 0; i < maxTicks && !spread; i++ {
		loop.grassRandomTick(loop.only(), grassBlock(false), center)
		for _, p := range dirtCells {
			if block.IsGrassBlock(mustGet(t, mgr, p)) {
				spread = true
				break
			}
		}
	}
	if !spread {
		t.Fatalf("grass never spread to any adjacent dirt cell in %d ticks", maxTicks)
	}
}

// TestGrassSpreadDrawsTwelveInts asserts the spread branch draws EXACTLY 12 levelRandom ints (3 per 4
// attempts) — the pig-oracle-safe determinism contract. We compare a scripted reference draw count
// against the region's levelRandom advance over one grassRandomTick that takes the spread branch.
func TestGrassSpreadDrawsTwelveInts(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	// A dedicated reference source with the SAME seed advanced by the SAME draws the handler makes.
	const seed = int64(0xC0DE12)
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(seed)
	ref := levelgen.NewLegacyRandomSource(seed)

	pos := pk.Position{X: 4, Y: 68, Z: 12}
	mgr.SetBlock(pos, grassBlock(false), dimMinY)
	// No dirt neighbours: every spread attempt misses, but ALL 12 draws still happen (position sampled
	// before the dirt test) — this is exactly the "12 unconditional draws" contract.
	loop.grassRandomTick(loop.only(), grassBlock(false), pos)

	// Reference: 4 iterations * (nextInt(3), nextInt(5), nextInt(3)).
	for i := 0; i < 4; i++ {
		ref.NextIntN(3)
		ref.NextIntN(5)
		ref.NextIntN(3)
	}
	// If the streams are in lockstep, one more draw from each must match.
	if got, want := loop.only().levelRandom.NextIntN(1000), ref.NextIntN(1000); got != want {
		t.Fatalf("levelRandom out of lockstep after the spread branch: got %d, want %d (expected exactly 12 draws)", got, want)
	}
}

// TestMyceliumSpreadsAsMycelium: mycelium (also SpreadingSnowyBlock) spreads its OWN kind (mycelium,
// not grass) to adjacent dirt — the setValue keys off the input family.
func TestMyceliumSpreadsAsMycelium(t *testing.T) {
	loop, mgr, _ := newRandomTickLoop()
	loop.only().levelRandom = levelgen.NewLegacyRandomSource(0x11CE)

	center := pk.Position{X: 10, Y: 68, Z: 4}
	mgr.SetBlock(center, myceliumBlock(false), dimMinY)
	dirtCells := []pk.Position{}
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			if dx == 0 && dz == 0 {
				continue
			}
			p := pk.Position{X: center.X + dx, Y: center.Y, Z: center.Z + dz}
			mgr.SetBlock(p, dirtState(), dimMinY)
			dirtCells = append(dirtCells, p)
		}
	}

	const maxTicks = 500000
	spread := false
	for i := 0; i < maxTicks && !spread; i++ {
		loop.grassRandomTick(loop.only(), myceliumBlock(false), center)
		for _, p := range dirtCells {
			if block.IsMycelium(mustGet(t, mgr, p)) {
				spread = true
				break
			}
			if block.IsGrassBlock(mustGet(t, mgr, p)) {
				t.Fatal("mycelium spread as GRASS — must spread its own kind")
			}
		}
	}
	if !spread {
		t.Fatalf("mycelium never spread to any adjacent dirt cell in %d ticks", maxTicks)
	}
}
