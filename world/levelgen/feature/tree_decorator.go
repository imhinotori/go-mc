package feature

// tree_decorator.go ports the COMMON overworld TreeDecorator subsystem — the subset the
// embedded common-overworld `tree` configs reference (derived from the data):
//   AlterGroundDecorator (mega_pine/mega_spruce podzol disk under the trunk)
//   BeehiveDecorator     (oak/birch/cherry bee-nest, a probability draw)
//   CocoaDecorator       (jungle cocoa pods on the lower trunk)
//   LeaveVineDecorator   (jungle/swamp hanging vines on the leaves)
//   TrunkVineDecorator   (jungle vines on the trunk sides)
//
// Each TreeDecorator.place runs AFTER the trunk+foliage (PlaceTree step 7) on the SAME
// threaded rng (the determinism contract T-13-06), fed the accumulated placed-log/placed-
// leaf positions through the DecoratorContext. It is PURE: the decorators resolve their
// block states via the Phase-12 resolveBlockState path and write through the setBlock/read
// callback seam (no placement/world import). The special-biome decorators
// (attached_to_leaves/pale_moss/creaking_heart) stay loud-errors -> 13-03.
//
// Sources (javap -c, temp/cache/26.2-inner.jar, net.minecraft.world.level.levelgen.feature):
//   treedecorators.TreeDecorator(.Context) +
//   treedecorators.{AlterGroundDecorator, BeehiveDecorator, CocoaDecorator,
//                   LeaveVineDecorator, TrunkVineDecorator}.place
//   feature.TreeFeature.getLowestTrunkOrRootOfTree
//
// An algorithmic port, NOT a copy of Mojang source. No GPL paste.

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// DecoratorContext is TreeDecorator.Context: the inputs each decorator.place needs — the
// placed LOG/LEAF/ROOT positions (collected during placeTrunk/createFoliage), the threaded
// rng, and the setBlock/read seam. isAir reads the live world through Read.
type DecoratorContext struct {
	Logs   []TreePos
	Leaves []TreePos
	Roots  []TreePos
	Rng    levelgen.RandomSource
	Set    SetBlockFn
	Read   ReadFn
}

// isAir reports whether the live block at pos is air (TreeDecorator.Context.isAir).
func (c *DecoratorContext) isAir(pos TreePos) bool {
	return block.IsAir(c.Read(pos.X, pos.Y, pos.Z))
}

// setBlock writes a state at pos (TreeDecorator.Context.setBlock).
func (c *DecoratorContext) setBlock(pos TreePos, st block.StateID) {
	c.Set(pos.X, pos.Y, pos.Z, st)
}

// placeVine ports TreeDecorator.Context.placeVine: set a vine{<prop>:true} at pos.
func (c *DecoratorContext) placeVine(pos TreePos, prop string) {
	st, err := vineState(prop)
	if err != nil {
		return // an unresolvable vine state is a no-op (never panics mid-decoration)
	}
	c.setBlock(pos, st)
}

// TreeDecorator is the decorator interface a TreeConfiguration carries. place runs after
// trunk+foliage with the accumulated positions (the DecoratorContext).
type TreeDecorator interface {
	place(ctx *DecoratorContext)
}

// ParseTreeDecorator dispatches a decorator envelope by "type". The COMMON overworld set is
// ported; the special-biome decorators error LOUDLY -> 13-03.
func ParseTreeDecorator(raw json.RawMessage) (TreeDecorator, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("feature: empty tree decorator")
	}
	var j struct {
		Type        string          `json:"type"`
		Probability *float64        `json:"probability"`
		Provider    json.RawMessage `json:"provider"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("feature: tree decorator: %w", err)
	}
	switch stripNS(j.Type) {
	case "alter_ground":
		prov, err := ParseProvider(j.Provider)
		if err != nil {
			return nil, fmt.Errorf("feature: alter_ground provider: %w", err)
		}
		return AlterGroundDecorator{provider: prov}, nil
	case "beehive":
		if j.Probability == nil {
			return nil, fmt.Errorf("feature: beehive missing probability")
		}
		return BeehiveDecorator{probability: float32(*j.Probability)}, nil
	case "cocoa":
		if j.Probability == nil {
			return nil, fmt.Errorf("feature: cocoa missing probability")
		}
		return CocoaDecorator{probability: float32(*j.Probability)}, nil
	case "leave_vine":
		if j.Probability == nil {
			return nil, fmt.Errorf("feature: leave_vine missing probability")
		}
		return LeaveVineDecorator{probability: float32(*j.Probability)}, nil
	case "trunk_vine":
		return TrunkVineDecorator{}, nil
	case "attached_to_leaves":
		return parseAttachedToLeavesDecorator(raw)
	case "pale_moss":
		return parsePaleMossDecorator(raw)
	case "creaking_heart":
		return parseCreakingHeartDecorator(raw)
	default:
		return nil, fmt.Errorf("feature: unknown tree decorator type %q", j.Type)
	}
}

// ============================================================================
// AlterGroundDecorator (mega_pine/mega_spruce) — replace the dirt under the trunk with
// podzol/coarse_dirt over a radius. The provider draw is per-position.
// ============================================================================

type AlterGroundDecorator struct {
	provider BlockStateProvider
}

// place ports AlterGroundDecorator.place: find the lowest trunk/root blocks (here only the
// trunk, since the common trees have no root_placer), filter the logs at the min Y, and
// place a 5x5 podzol circle (corners trimmed) under each. The rule_based provider is bound
// to the live read so its `beneath_tree_podzol_replaceable` rule evaluates the ground.
func (d AlterGroundDecorator) place(ctx *DecoratorContext) {
	prov := d.provider
	if rb, ok := prov.(RuleBasedStateProvider); ok {
		prov = rb.withExisting(func(x, y, z int) block.StateID { return ctx.Read(x, y, z) })
	}
	lowest := lowestTrunkOrRoot(ctx)
	if len(lowest) == 0 {
		return
	}
	minY := lowest[0].Y
	for _, p := range lowest {
		if p.Y == minY {
			d.placeCircle(ctx, prov, p)
		}
	}
}

// placeCircle ports AlterGroundDecorator.placeCircle: a 5x5 (-2..2) disc with the 4 corners
// (|dx|==2 && |dz|==2) trimmed; placeBlockAt at the center.below-ground stack.
func (d AlterGroundDecorator) placeCircle(ctx *DecoratorContext, prov BlockStateProvider, center TreePos) {
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			if abs(dx) == 2 && abs(dz) == 2 {
				continue
			}
			d.placeBlockAt(ctx, prov, center.offset(dx, 0, dz))
		}
	}
}

// placeBlockAt ports AlterGroundDecorator.placeBlockAt: walk from above(2) down to above(-3)
// (i.e. y+2 .. y-3); at each, getOptionalState — if the rule matches (podzol-replaceable)
// place the state and stop; otherwise stop once a non-air block below ground is hit. The
// provider's optional-state gate IS the determinism contract.
func (d AlterGroundDecorator) placeBlockAt(ctx *DecoratorContext, prov BlockStateProvider, base TreePos) {
	for i := 2; i >= -3; i-- {
		pos := base.above(i)
		st, ok := OptionalState(prov, ctx.Rng, pos.X, pos.Y, pos.Z)
		if ok {
			ctx.setBlock(pos, st)
			return
		}
		if !ctx.isAir(pos) && i < 0 {
			return
		}
	}
}

// lowestTrunkOrRoot ports TreeFeature.getLowestTrunkOrRootOfTree: when roots are present use
// them (+ the logs if the lowest log shares the lowest root's Y); the common trees have no
// roots, so this is just the logs. Returned in their collected (insertion) order — the first
// log is the trunk base.
func lowestTrunkOrRoot(ctx *DecoratorContext) []TreePos {
	if len(ctx.Roots) == 0 {
		return ctx.Logs
	}
	// (roots present: the mangrove path — 13-03 supplies roots; keep the faithful merge.)
	out := append([]TreePos{}, ctx.Roots...)
	if len(ctx.Logs) > 0 && ctx.Logs[0].Y == ctx.Roots[0].Y {
		out = append(ctx.Logs, ctx.Roots...)
	}
	return out
}

// ============================================================================
// BeehiveDecorator (oak_bees/birch_bees/...) — attach a bee_nest to a trunk-side block at
// the canopy base with a probability draw.
// ============================================================================

type BeehiveDecorator struct {
	probability float32
}

// place ports BeehiveDecorator.place. Draw order (javap -c):
//   if logs empty -> return
//   if nextFloat() >= probability -> return            [nextFloat]
//   targetY = leaves empty ? logs[0].Y+1 + nextInt(3)  [nextInt(3) only when leaves empty]
//                          : max(leaves[0].Y-1, logs[0].Y+1)   (clamped to <= logs.last.Y)
//   candidates = logs at Y==targetY where the block is air AND the SOUTH neighbor is air
//   shuffle(candidates, rng)                            [shuffle draws]
//   pick the first passing the (isAir && south isAir) test; place bee_nest{facing:south}
func (d BeehiveDecorator) place(ctx *DecoratorContext) {
	logs := ctx.Logs
	leaves := ctx.Leaves
	if len(logs) == 0 {
		return
	}
	if ctx.Rng.NextFloat() >= d.probability {
		return
	}
	var targetY int
	if len(leaves) != 0 {
		targetY = maxInt(leaves[0].Y-1, logs[0].Y+1)
		if last := logs[len(logs)-1].Y; targetY > last {
			targetY = last
		}
	} else {
		targetY = logs[0].Y + 1 + int(ctx.Rng.NextIntN(3))
		if last := logs[len(logs)-1].Y; targetY > last {
			targetY = last
		}
	}
	// candidate trunk-side positions: a log at targetY whose own +SOUTH face cell is air and
	// the cell at (log.south) air (the face the nest attaches to faces SOUTH). The jar
	// flatMaps each log at targetY to its SPAWN_DIRECTIONS (HORIZONTAL minus SOUTH) offsets.
	var candidates []TreePos
	for _, lg := range logs {
		if lg.Y != targetY {
			continue
		}
		for _, dir := range spawnDirections() {
			candidates = append(candidates, lg.offset(dir.sx, 0, dir.sz))
		}
	}
	if len(candidates) == 0 {
		return
	}
	shuffleTreePos(candidates, ctx.Rng)
	for _, cand := range candidates {
		if !ctx.isAir(cand) {
			continue
		}
		if !ctx.isAir(cand.offset(0, 0, 1)) { // SOUTH neighbor air (WORLDGEN_FACING = SOUTH)
			continue
		}
		st, err := beeNestState()
		if err != nil {
			return
		}
		ctx.setBlock(cand, st)
		// The bee occupants (nextInt(3) bees, each nextInt(599)) populate the BlockEntity,
		// which worldgen placement here does not model — the block + facing land
		// deterministically; the occupant draws are skipped (no BlockEntity to store into).
		return
	}
}

// spawnDirections is BeehiveDecorator.SPAWN_DIRECTIONS: HORIZONTAL minus the WORLDGEN_FACING
// (SOUTH). The bee-nest attaches to a face that is NOT south (so its south face is open).
func spawnDirections() []hdir {
	return []hdir{{0, -1}, {1, 0}, {-1, 0}} // NORTH, EAST, WEST
}

// ============================================================================
// CocoaDecorator (jungle_tree) — cocoa pods on the lower trunk faces.
// ============================================================================

type CocoaDecorator struct {
	probability float32
}

// place ports CocoaDecorator.place. Draw order (javap -c):
//   if nextFloat() >= probability -> return            [nextFloat]
//   if logs empty -> return
//   minY = logs[0].Y
//   for each log with (log.Y - minY) <= 2 (the lower 3 trunk blocks):
//     for each horizontal direction:
//       if nextFloat() < 0.25:                         [nextFloat per direction]
//         attach cocoa{age:nextInt(3),facing:dir} at log + opposite(dir) if that cell air
func (d CocoaDecorator) place(ctx *DecoratorContext) {
	if ctx.Rng.NextFloat() >= d.probability {
		return
	}
	if len(ctx.Logs) == 0 {
		return
	}
	minY := ctx.Logs[0].Y
	for _, lg := range ctx.Logs {
		if lg.Y-minY > 2 {
			continue
		}
		for _, dir := range horizontalDirections {
			if ctx.Rng.NextFloat() >= 0.25 {
				continue
			}
			opp := dir.opposite()
			at := lg.offset(opp.sx, 0, opp.sz)
			age := int(ctx.Rng.NextIntN(3))
			if !ctx.isAir(at) {
				continue
			}
			st, err := cocoaState(age, dir)
			if err != nil {
				continue
			}
			ctx.setBlock(at, st)
		}
	}
}

// ============================================================================
// LeaveVineDecorator (jungle/swamp) — hanging vines off the leaves.
// ============================================================================

type LeaveVineDecorator struct {
	probability float32
}

// place ports LeaveVineDecorator.place: for each leaf position, in W/E/N/S order, draw
// nextFloat() < probability and (if the side cell is air) start a hanging vine column.
func (d LeaveVineDecorator) place(ctx *DecoratorContext) {
	for _, leaf := range ctx.Leaves {
		d.tryFaces(ctx, leaf)
	}
}

// tryFaces ports LeaveVineDecorator.lambda$place$0: the 4 face draws (W,E,N,S) with the
// vine attached on the OPPOSITE wall property (a west cell gets a vine with EAST=true).
func (d LeaveVineDecorator) tryFaces(ctx *DecoratorContext, leaf TreePos) {
	// west -> vine{EAST}
	if ctx.Rng.NextFloat() < d.probability {
		w := leaf.offset(-1, 0, 0)
		if ctx.isAir(w) {
			addHangingVine(ctx, w, vineEast)
		}
	}
	// east -> vine{WEST}
	if ctx.Rng.NextFloat() < d.probability {
		e := leaf.offset(1, 0, 0)
		if ctx.isAir(e) {
			addHangingVine(ctx, e, vineWest)
		}
	}
	// north -> vine{SOUTH}
	if ctx.Rng.NextFloat() < d.probability {
		n := leaf.offset(0, 0, -1)
		if ctx.isAir(n) {
			addHangingVine(ctx, n, vineSouth)
		}
	}
	// south -> vine{NORTH}
	if ctx.Rng.NextFloat() < d.probability {
		s := leaf.offset(0, 0, 1)
		if ctx.isAir(s) {
			addHangingVine(ctx, s, vineNorth)
		}
	}
}

// addHangingVine ports LeaveVineDecorator.addHangingVine: place the vine, then drape up to 4
// more vines down while the cell below is air.
func addHangingVine(ctx *DecoratorContext, pos TreePos, prop string) {
	ctx.placeVine(pos, prop)
	n := 4
	cur := pos.below(1)
	for n > 0 && ctx.isAir(cur) {
		ctx.placeVine(cur, prop)
		cur = cur.below(1)
		n--
	}
}

// ============================================================================
// TrunkVineDecorator (jungle) — vines on the trunk sides.
// ============================================================================

type TrunkVineDecorator struct{}

// place ports TrunkVineDecorator.place: for each log, in W/E/N/S order, nextInt(3) > 0 and
// (if the side cell is air) attach a single vine on the opposite wall property.
func (d TrunkVineDecorator) place(ctx *DecoratorContext) {
	for _, lg := range ctx.Logs {
		// west -> vine{EAST}
		if ctx.Rng.NextIntN(3) > 0 {
			w := lg.offset(-1, 0, 0)
			if ctx.isAir(w) {
				ctx.placeVine(w, vineEast)
			}
		}
		// east -> vine{WEST}
		if ctx.Rng.NextIntN(3) > 0 {
			e := lg.offset(1, 0, 0)
			if ctx.isAir(e) {
				ctx.placeVine(e, vineWest)
			}
		}
		// north -> vine{SOUTH}
		if ctx.Rng.NextIntN(3) > 0 {
			n := lg.offset(0, 0, -1)
			if ctx.isAir(n) {
				ctx.placeVine(n, vineSouth)
			}
		}
		// south -> vine{NORTH}
		if ctx.Rng.NextIntN(3) > 0 {
			s := lg.offset(0, 0, 1)
			if ctx.isAir(s) {
				ctx.placeVine(s, vineNorth)
			}
		}
	}
}

// ============================================================================
// block-state helpers (resolve via the Phase-12 resolveBlockState path)
// ============================================================================

const (
	vineEast  = "east"
	vineWest  = "west"
	vineNorth = "north"
	vineSouth = "south"
)

// beeNestState resolves bee_nest{facing:south, honey_level:0} (BeehiveBlock default + the
// WORLDGEN_FACING south).
func beeNestState() (block.StateID, error) {
	return resolveBlockState(blockStateJSON{
		Name:       "minecraft:bee_nest",
		Properties: map[string]string{"facing": "south", "honey_level": "0"},
	})
}

// cocoaState resolves cocoa{age:N, facing:<dir>}.
func cocoaState(age int, dir hdir) (block.StateID, error) {
	return resolveBlockState(blockStateJSON{
		Name:       "minecraft:cocoa",
		Properties: map[string]string{"age": itoa(age), "facing": dirName(dir)},
	})
}

// vineState resolves vine{<prop>:true} (a single attached face).
func vineState(prop string) (block.StateID, error) {
	return resolveBlockState(blockStateJSON{
		Name:       "minecraft:vine",
		Properties: map[string]string{prop: "true"},
	})
}

// dirName maps a horizontal step to the facing property string (cocoa's FACING).
func dirName(d hdir) string {
	switch {
	case d.sz < 0:
		return "north"
	case d.sz > 0:
		return "south"
	case d.sx < 0:
		return "west"
	default:
		return "east"
	}
}

// itoa is a tiny non-negative int -> string (avoids strconv import churn; cocoa age 0..2).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// shuffleTreePos ports Util.shuffle(list, rng): Fisher-Yates from the END using nextInt(i+1).
// (Util.shuffle: for i = size-1 down to 1: swap(i, nextInt(i+1)).)
func shuffleTreePos(s []TreePos, rng levelgen.RandomSource) {
	for i := len(s) - 1; i > 0; i-- {
		j := int(rng.NextIntN(int32(i + 1)))
		s[i], s[j] = s[j], s[i]
	}
}

// ensure deterministic candidate order before the shuffle (the collected logs are already
// insertion-ordered; the per-log SPAWN_DIRECTIONS expansion is fixed) — sort is a guard for
// any map-derived ordering drift. No-op when already ordered.
func sortTreePos(s []TreePos) {
	sort.Slice(s, func(a, b int) bool {
		if s[a].Y != s[b].Y {
			return s[a].Y < s[b].Y
		}
		if s[a].X != s[b].X {
			return s[a].X < s[b].X
		}
		return s[a].Z < s[b].Z
	})
}

// compile-time assertions: the decorators satisfy the interface.
var (
	_ TreeDecorator = AlterGroundDecorator{}
	_ TreeDecorator = BeehiveDecorator{}
	_ TreeDecorator = CocoaDecorator{}
	_ TreeDecorator = LeaveVineDecorator{}
	_ TreeDecorator = TrunkVineDecorator{}
)
