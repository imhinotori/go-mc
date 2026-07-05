package world

import (
	"encoding/json"
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/data"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// cfFromEmbedded loads a real configured_feature JSON from the embed and builds a
// ConfiguredFeature whose Config.Raw is the inner `config` object (the shape the bodies parse).
func cfFromEmbedded(t *testing.T, id string) *feature.ConfiguredFeature {
	t.Helper()
	raw, err := data.ConfiguredFeatureJSON(id)
	if err != nil {
		t.Fatalf("load configured_feature %q: %v", id, err)
	}
	var env struct {
		Type   string          `json:"type"`
		Config json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode configured_feature %q: %v", id, err)
	}
	ftype := env.Type
	for i := 0; i < len(ftype); i++ {
		if ftype[i] == ':' {
			ftype = ftype[i+1:]
			break
		}
	}
	return &feature.ConfiguredFeature{Type: ftype, Config: &feature.ParsedConfig{Raw: env.Config}}
}

// fillSolid sets a solid `st` floor across the whole 3x3 at y=floorY.
func fillSolid(view *Neighborhood, center [2]int, floorY int, st block.StateID) {
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			baseX := (center[0] + dx) * 16
			baseZ := (center[1] + dz) * 16
			for lx := 0; lx < 16; lx++ {
				for lz := 0; lz < 16; lz++ {
					view.SetBlock(baseX+lx, floorY, baseZ+lz, st)
				}
			}
		}
	}
}

// ---- spring_feature ----

// TestSpringPlacesWhenGeometryMatches: with a stone shell around the origin producing exactly
// rockCount=4 rock neighbors and holeCount=1 air hole, the spring places its fluid (0 draws).
func TestSpringPlacesWhenGeometryMatches(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	view := build3x3(center, minY, height)
	stone := block.ToStateID[block.Stone{}]

	// Anchor at (8,40,8). SpringFeature reads above (must be validBlock=stone),
	// requiresBlockBelow default true so below must be validBlock; the origin itself must be
	// air-or-validBlock; then of {W,E,N,S,below} exactly 4 must be stone and exactly 1 air.
	x, y, z := 8, 40, 8
	set := func(dx, dy, dz int, st block.StateID) { view.SetBlock(x+dx, y+dy, z+dz, st) }
	set(0, 1, 0, stone)  // above = stone (validBlock)
	set(0, -1, 0, stone) // below = stone (validBlock + a rock neighbor)
	// origin stays air (air-or-validBlock ok).
	// {W,E,N,S,below}: make W,E,N stone (rock), below already stone (rock) => 4 rock; S = air (1 hole).
	set(-1, 0, 0, stone) // west
	set(1, 0, 0, stone)  // east
	set(0, 0, -1, stone) // north
	// south stays air -> the single hole
	// below already stone (rock #4)

	cf := cfFromEmbedded(t, "spring_water")
	bctx := &bodyContext{view: view}
	ctx := newPlacementContext(view, minY, height, nil)
	pos := placement.BlockPos{X: x, Y: y, Z: z}

	if !springBody(bctx, cf, ctx, levelgen.NewLegacyRandomSource(1), pos) {
		t.Fatalf("springBody returned false; want a placement (rock=4, hole=1)")
	}
	water := block.ToStateID[block.Water{Level: 0}]
	if got := view.GetBlock(x, y, z); got != water {
		t.Fatalf("spring placed %d at origin, want water source %d", got, water)
	}
}

// TestSpringNoPlaceWrongGeometry: with the above block NOT a valid block, the spring bails
// immediately (0 draws, 0 placement).
func TestSpringNoPlaceWrongGeometry(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	view := build3x3(center, minY, height) // all air -> above is air, not a validBlock
	cf := cfFromEmbedded(t, "spring_water")
	bctx := &bodyContext{view: view}
	ctx := newPlacementContext(view, minY, height, nil)
	if springBody(bctx, cf, ctx, levelgen.NewLegacyRandomSource(1), placement.BlockPos{X: 8, Y: 40, Z: 8}) {
		t.Fatalf("springBody placed over empty air; want no placement")
	}
}

// TestSpringDeterministic: two runs with the same view + rng produce the identical placement.
func TestSpringDeterministic(t *testing.T) {
	const minY, height = -64, 384
	stone := block.ToStateID[block.Stone{}]
	build := func() (*Neighborhood, placement.BlockPos) {
		center := [2]int{0, 0}
		view := build3x3(center, minY, height)
		x, y, z := 8, 40, 8
		view.SetBlock(x, y+1, z, stone)
		view.SetBlock(x, y-1, z, stone)
		view.SetBlock(x-1, y, z, stone)
		view.SetBlock(x+1, y, z, stone)
		view.SetBlock(x, y, z-1, stone)
		return view, placement.BlockPos{X: x, Y: y, Z: z}
	}
	cf := cfFromEmbedded(t, "spring_water")
	ctx0, _ := build()
	_ = ctx0
	v1, pos := build()
	v2, _ := build()
	springBody(&bodyContext{view: v1}, cf, newPlacementContext(v1, minY, height, nil), levelgen.NewLegacyRandomSource(7), pos)
	springBody(&bodyContext{view: v2}, cf, newPlacementContext(v2, minY, height, nil), levelgen.NewLegacyRandomSource(7), pos)
	if v1.GetBlock(pos.X, pos.Y, pos.Z) != v2.GetBlock(pos.X, pos.Y, pos.Z) {
		t.Fatalf("spring placement not deterministic")
	}
}

// ---- disk ----

// TestDiskPlacesOverTarget: a clay disk over a dirt floor replaces the dirt within the radius
// with clay. Uses the real disk_clay config (simple_state_provider, target=dirt/clay).
func TestDiskPlacesOverTarget(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	view := build3x3(center, minY, height)
	dirt := block.ToStateID[block.Dirt{}]
	// The disk paints a flat disk at half_height around origin.Y; the target reads the block
	// AT each column cell. Fill a dirt slab spanning the disk's Y range [y-halfHeight-? .. y+hh].
	x, y, z := 8, 40, 8
	for dy := -3; dy <= 3; dy++ {
		fillSolid(view, center, y+dy, dirt)
	}
	cf := cfFromEmbedded(t, "disk_clay")
	bctx := &bodyContext{view: view}
	ctx := newPlacementContext(view, minY, height, nil)
	pos := placement.BlockPos{X: x, Y: y, Z: z}

	if !diskBody(bctx, cf, ctx, levelgen.NewLegacyRandomSource(0xD15C), pos) {
		t.Fatalf("diskBody returned false; want clay placed over dirt")
	}
	clay := block.ToStateID[block.Clay{}]
	// The origin column (dx=dz=0) is always within radius; at least the origin Y should be clay.
	if got := view.GetBlock(x, y, z); got != clay {
		t.Fatalf("disk origin cell is %d, want clay %d", got, clay)
	}
}

// TestDiskDeterministic: same seed/rng + same view -> identical placement footprint (the single
// radius IntProvider draw is the only randomness).
func TestDiskDeterministic(t *testing.T) {
	const minY, height = -64, 384
	dirt := block.ToStateID[block.Dirt{}]
	clay := block.ToStateID[block.Clay{}]
	run := func(seed int64) int {
		center := [2]int{0, 0}
		view := build3x3(center, minY, height)
		for dy := -3; dy <= 3; dy++ {
			fillSolid(view, center, 40+dy, dirt)
		}
		cf := cfFromEmbedded(t, "disk_clay")
		diskBody(&bodyContext{view: view}, cf, newPlacementContext(view, minY, height, nil),
			levelgen.NewLegacyRandomSource(seed), placement.BlockPos{X: 8, Y: 40, Z: 8})
		n := 0
		for dx := -8; dx <= 8; dx++ {
			for dz := -8; dz <= 8; dz++ {
				if view.GetBlock(8+dx, 40, 8+dz) == clay {
					n++
				}
			}
		}
		return n
	}
	if a, b := run(0x5EED), run(0x5EED); a != b {
		t.Fatalf("disk not deterministic: %d vs %d clay cells", a, b)
	}
	if run(0x5EED) == 0 {
		t.Fatalf("disk placed no clay; expected a positive footprint")
	}
}

// TestDiskRuleBasedProvider: disk_sand uses a rule_based provider (fallback=sand, rule: below
// is air -> sandstone). Over a dirt/grass floor with air below the disk layer, the rule fires
// where the cell below is air; parsing + evaluation must not error and must place sand-family.
func TestDiskRuleBasedProvider(t *testing.T) {
	const minY, height = -64, 384
	center := [2]int{0, 0}
	view := build3x3(center, minY, height)
	grass := block.ToStateID[block.GrassBlock{Snowy: false}]
	// Only a single grass layer at y=40 (target = dirt/grass); below (y=39) is air, so the
	// rule (matching_blocks air offset [0,-1,0]) fires -> sandstone at that cell.
	fillSolid(view, center, 40, grass)
	cf := cfFromEmbedded(t, "disk_sand")
	bctx := &bodyContext{view: view}
	ctx := newPlacementContext(view, minY, height, nil)
	if !diskBody(bctx, cf, ctx, levelgen.NewLegacyRandomSource(0x5A4D), placement.BlockPos{X: 8, Y: 40, Z: 8}) {
		t.Fatalf("disk_sand returned false; want a placement over grass")
	}
	sand := block.ToStateID[block.Sand{}]
	sandstone := block.ToStateID[block.Sandstone{}]
	got := view.GetBlock(8, 40, 8)
	if got != sand && got != sandstone {
		t.Fatalf("disk_sand origin is %d, want sand %d or sandstone %d", got, sand, sandstone)
	}
	// half_height=2 so the disk spans y in [38..42]; the y=40 grass cell had air below -> the
	// rule chose sandstone.
	if got != sandstone {
		t.Fatalf("disk_sand origin (air below) is %d, want sandstone %d (rule_based rule)", got, sandstone)
	}
}
