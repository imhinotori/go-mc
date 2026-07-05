package world

// feature_disk.go ports DiskFeature.place + placeColumn — the sand/clay/gravel/grass "disk"
// feature (net.minecraft.world.level.levelgen.feature.DiskFeature, javap -c against
// temp/cache/26.2-inner.jar). DiskConfiguration carries a `state_provider` (the block to lay),
// a `target` BlockPredicate (which existing blocks the disk replaces), a `radius` IntProvider,
// and a `half_height`. The feature paints a flat disk of `state` over the target ground near
// water (the placed_feature places it at the water table).
//
// RNG DRAW ORDER (the determinism contract): DiskFeature.place makes exactly ONE draw —
// radius = radiusProvider.sample(rng) — up front. placeColumn then places via the
// state_provider's getOptionalState, which for the data's simple_state_provider and
// rule_based_state_provider takes ZERO rng draws (both are positional/predicate-driven), so
// the whole feature consumes exactly ONE IntProvider draw regardless of how many blocks land.
//
// Sources (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.feature.DiskFeature.place / placeColumn
//   - net.minecraft.world.level.levelgen.feature.configurations.DiskConfiguration
//   - net.minecraft.world.level.levelgen.feature.stateproviders.RuleBasedStateProvider.
//     getOptionalState (fallback + ordered rules; first matching rule wins, else fallback)
//   - net.minecraft.world.level.levelgen.blockpredicates.BlockPredicate (target + rule if_true)
//
// The `target` and the rule `if_true` predicates are OFFSET-AWARE and read the LIVE terrain, so
// they are evaluated through placement.ParsePredicate (the ported BlockPredicate set, which
// already handles matching_blocks/solid/matching_fluids/not/any_of + the [dx,dy,dz] offset) over
// the placement context — NOT the feature package's offset-less rule predicate (that one is for
// the tree below-trunk providers and cannot express the disk rules' offsets/solid/fluid tests).
//
// getOptionalState SEAM: the jar's RuleBasedStateProvider.getOptionalState returns null only
// when NO rule matches AND there is NO fallback; DiskFeature skips the setBlock on null. Every
// disk state_provider in the 26.2 data is either a simple_state_provider or a rule_based provider
// WITH a fallback, so getOptionalState is never null for the disk configs — the port evaluates
// the rules then the fallback (behavior-identical for the disk data). A rule_based provider with
// no fallback would need the null-skip; none exists in the disk configs, and the port documents it.

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

func init() { registerFeatureBody("disk", diskBody) }

// diskConfigCache memoizes the decoded DiskConfiguration per ConfiguredFeature (oreConfigCache
// pattern): the predicate/provider parse scans block states and must not run per placement.
var diskConfigCache sync.Map // map[*feature.ConfiguredFeature]*diskConfig

// diskStateSource yields the disk block at a candidate position, mirroring
// BlockStateProvider.getOptionalState: (state, true) to place, (0, false) to skip. For a
// simple_state_provider it always yields the state; for a rule_based provider it walks the
// rules (first matching if_true predicate wins) then the fallback.
type diskStateSource interface {
	optionalState(ctx placement.PlacementContext, rng levelgen.RandomSource, x, y, z int) (block.StateID, bool)
}

// diskConfig is the decoded DiskConfiguration.
type diskConfig struct {
	provider   diskStateSource
	target     placement.BlockPredicate
	radius     *miscIntProvider
	halfHeight int
}

// jsonDiskConfig is the on-disk DiskConfiguration shape (verified disk_sand/clay/gravel/grass).
type jsonDiskConfig struct {
	StateProvider json.RawMessage `json:"state_provider"`
	Target        json.RawMessage `json:"target"`
	Radius        json.RawMessage `json:"radius"`
	HalfHeight    int             `json:"half_height"`
}

// decodeDiskConfigCached returns the memoized decoded config for cf.
func decodeDiskConfigCached(cf *feature.ConfiguredFeature) (*diskConfig, error) {
	if v, ok := diskConfigCache.Load(cf); ok {
		return v.(*diskConfig), nil
	}
	cfg, err := decodeDiskConfig(configRaw(cf))
	if err != nil {
		return nil, err
	}
	diskConfigCache.Store(cf, cfg)
	return cfg, nil
}

func decodeDiskConfig(raw json.RawMessage) (*diskConfig, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("world: disk config is empty")
	}
	var j jsonDiskConfig
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("world: disk config: %w", err)
	}
	target, err := placement.ParsePredicate(j.Target)
	if err != nil {
		return nil, fmt.Errorf("world: disk target: %w", err)
	}
	radius, err := parseMiscIntProvider(j.Radius)
	if err != nil {
		return nil, fmt.Errorf("world: disk radius: %w", err)
	}
	prov, err := parseDiskStateProvider(j.StateProvider)
	if err != nil {
		return nil, fmt.Errorf("world: disk state_provider: %w", err)
	}
	return &diskConfig{provider: prov, target: target, radius: radius, halfHeight: j.HalfHeight}, nil
}

// ---- disk state providers ----

// diskSimpleSource is a simple_state_provider: a fixed state, always placed (0 draws).
type diskSimpleSource struct{ state block.StateID }

func (s diskSimpleSource) optionalState(_ placement.PlacementContext, _ levelgen.RandomSource, _, _, _ int) (block.StateID, bool) {
	return s.state, true
}

// diskRule is one RuleBasedStateProvider.Rule: an offset-aware if_true predicate + the provider
// to use when it holds.
type diskRule struct {
	ifTrue placement.BlockPredicate
	then   diskStateSource
}

// diskRuleBasedSource ports RuleBasedStateProvider.getOptionalState: the FIRST rule whose
// if_true predicate holds at the candidate wins (its provider's state); else the fallback's
// state (or skip when there is no fallback — not exercised by the disk data). 0 rng draws for
// the disk data (simple leaves).
type diskRuleBasedSource struct {
	fallback diskStateSource // nil when the config omits `fallback` (skip on no match)
	rules    []diskRule
}

func (s diskRuleBasedSource) optionalState(ctx placement.PlacementContext, rng levelgen.RandomSource, x, y, z int) (block.StateID, bool) {
	for _, r := range s.rules {
		if r.ifTrue.Test(ctx, x, y, z) {
			return r.then.optionalState(ctx, rng, x, y, z)
		}
	}
	if s.fallback == nil {
		return 0, false // no rule matched and no fallback -> getOptionalState null (skip)
	}
	return s.fallback.optionalState(ctx, rng, x, y, z)
}

// jsonDiskProvider is the union of the two disk provider envelopes.
type jsonDiskProvider struct {
	Type     string          `json:"type"`
	State    json.RawMessage `json:"state"`    // simple_state_provider
	Fallback json.RawMessage `json:"fallback"` // rule_based_state_provider (optional)
	Rules    []struct {
		IfTrue json.RawMessage `json:"if_true"`
		Then   json.RawMessage `json:"then"`
	} `json:"rules"`
}

// parseDiskStateProvider parses a disk state_provider (simple_state_provider or
// rule_based_state_provider). The rule if_true predicates + the nested `then`/`fallback`
// providers are parsed recursively; the disk data nests only simple providers under a
// rule_based, but the recursion handles arbitrary nesting jar-faithfully.
func parseDiskStateProvider(raw json.RawMessage) (diskStateSource, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("missing state_provider")
	}
	var j jsonDiskProvider
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("decoding state_provider: %w", err)
	}
	switch stripNSMisc(j.Type) {
	case "simple_state_provider":
		sid, err := feature.ResolveBlockStateJSON(j.State)
		if err != nil {
			return nil, fmt.Errorf("simple_state_provider state: %w", err)
		}
		return diskSimpleSource{state: sid}, nil
	case "rule_based_state_provider":
		var s diskRuleBasedSource
		if len(j.Fallback) != 0 {
			fb, err := parseDiskStateProvider(j.Fallback)
			if err != nil {
				return nil, fmt.Errorf("rule_based fallback: %w", err)
			}
			s.fallback = fb
		}
		for i, r := range j.Rules {
			pred, err := placement.ParsePredicate(r.IfTrue)
			if err != nil {
				return nil, fmt.Errorf("rule_based rule %d if_true: %w", i, err)
			}
			then, err := parseDiskStateProvider(r.Then)
			if err != nil {
				return nil, fmt.Errorf("rule_based rule %d then: %w", i, err)
			}
			s.rules = append(s.rules, diskRule{ifTrue: pred, then: then})
		}
		return s, nil
	default:
		return nil, fmt.Errorf("world: unported disk state_provider type %q "+
			"(the disk data uses simple_state_provider / rule_based_state_provider)", j.Type)
	}
}

// ---- DiskFeature.place -> placeColumn ----

// diskBody ports DiskFeature.place (javap -c):
//
//	y  = origin.Y
//	y1 = y + halfHeight            // the top of the placed column (inclusive)
//	y2 = y - halfHeight - 1        // one BELOW the bottom (placeColumn loops y > y2)
//	radius = radiusProvider.sample(rng)                   // the ONE rng draw
//	for cur in betweenClosed(origin+(-radius,0,-radius), origin+(radius,0,radius)):   // flat XZ
//	    dx = cur.X - origin.X;  dz = cur.Z - origin.Z
//	    if dx*dx + dz*dz > radius*radius:  continue        // outside the disk
//	    placed |= placeColumn(cfg, level, rng, y1, y2, cur)
//	return placed
//
// placeColumn(y1, y2, colXZ): iterate y from y1 DOWN while y > y2:
//
//	if target.test(pos(colX,y,colZ)):
//	    state,ok := stateProvider.getOptionalState(...); if ok: setBlock(pos, state); placed=true; run=true
//	else: run=false   // a break in the contiguous target run
//	returns whether any block was placed in the column.
func diskBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	cfg, err := decodeDiskConfigCached(cf)
	if err != nil {
		panic(err) // build-data error (T-12-07)
	}

	y1 := pos.Y + cfg.halfHeight
	y2 := pos.Y - cfg.halfHeight - 1
	radius := cfg.radius.sample(rng) // the single rng draw
	r2 := radius * radius

	placed := false
	// betweenClosed over the flat XZ box [origin-(r,0,r) .. origin+(r,0,r)] (Y span 0). The
	// cursor order does not affect the draw stream (placeColumn takes 0 draws), so the placed
	// set is order-independent; iterate x-outer/z-inner for clarity.
	for cx := pos.X - radius; cx <= pos.X+radius; cx++ {
		for cz := pos.Z - radius; cz <= pos.Z+radius; cz++ {
			dx := cx - pos.X
			dz := cz - pos.Z
			if dx*dx+dz*dz > r2 {
				continue
			}
			if diskPlaceColumn(bctx, ctx, rng, cfg, y1, y2, cx, cz) {
				placed = true
			}
		}
	}
	return placed
}

// diskPlaceColumn ports DiskFeature.placeColumn: walk y from y1 down while y > y2; where the
// target predicate holds, place the provider's optional state. Returns whether anything landed.
//
// The jar's `bl2` (in-run) flag ONLY gates markAboveForPostProcessing (a lighting/post-process
// hint at the TOP of each contiguous placed run) — it never affects WHICH blocks are placed.
// The worldgen Neighborhood has no post-process queue (that seam is not modeled anywhere in the
// ported bodies), so markAboveForPostProcessing is omitted; the placed BLOCK set is identical.
func diskPlaceColumn(
	bctx *bodyContext,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	cfg *diskConfig,
	y1, y2, cx, cz int,
) bool {
	placed := false
	for y := y1; y > y2; y-- {
		p := placement.BlockPos{X: cx, Y: y, Z: cz}
		if !cfg.target.Test(ctx, p.X, p.Y, p.Z) {
			continue
		}
		st, ok := cfg.provider.optionalState(ctx, rng, p.X, p.Y, p.Z)
		if !ok {
			continue
		}
		bctx.placeState(p, st)
		placed = true
	}
	return placed
}
