package placement

import (
	"encoding/json"
	"fmt"

	"github.com/imhinotori/sulfur/world/levelgen"
)

// ---- VerticalAnchor (net.minecraft.world.level.levelgen.VerticalAnchor) ----
//
// JAR-CONFIRMED resolveY per subclass:
//   - Absolute(N)     -> N
//   - AboveBottom(N)  -> minGenY + N
//   - BelowTop(N)     -> (genDepth - 1) + minGenY - N
//
// (The surface package only needed absolute/above_bottom; height_range's ore data
// DOES use below_top — e.g. ore_coal max_inclusive {below_top:0} — so below_top is
// ported here in full rather than erroring, which would break every ore feature.)

type anchorKind int

const (
	anchorAbsolute anchorKind = iota
	anchorAboveBottom
	anchorBelowTop
)

// verticalAnchor ports VerticalAnchor: one of absolute / above_bottom / below_top.
type verticalAnchor struct {
	kind   anchorKind
	offset int
}

// resolveY ports VerticalAnchor.resolveY(WorldGenerationContext). minGenY is
// ctx.MinY(); genDepth is ctx.Height().
func (a verticalAnchor) resolveY(minGenY, genDepth int) int {
	switch a.kind {
	case anchorAbsolute:
		return a.offset
	case anchorAboveBottom:
		return minGenY + a.offset
	case anchorBelowTop:
		return (genDepth - 1) + minGenY - a.offset
	default:
		// Unreachable: every verticalAnchor is built via parseAnchor.
		panic(fmt.Sprintf("placement: invalid vertical anchor kind %d", a.kind))
	}
}

// jsonAnchor is a VerticalAnchor JSON object: exactly one of absolute /
// above_bottom / below_top.
type jsonAnchor struct {
	Absolute    *int `json:"absolute"`
	AboveBottom *int `json:"above_bottom"`
	BelowTop    *int `json:"below_top"`
}

// parseAnchor decodes a VerticalAnchor JSON object.
func parseAnchor(j jsonAnchor) (verticalAnchor, error) {
	switch {
	case j.Absolute != nil:
		return verticalAnchor{kind: anchorAbsolute, offset: *j.Absolute}, nil
	case j.AboveBottom != nil:
		return verticalAnchor{kind: anchorAboveBottom, offset: *j.AboveBottom}, nil
	case j.BelowTop != nil:
		return verticalAnchor{kind: anchorBelowTop, offset: *j.BelowTop}, nil
	default:
		return verticalAnchor{}, fmt.Errorf("placement: vertical anchor missing absolute/above_bottom/below_top")
	}
}

// ---- HeightProvider (net.minecraft.world.level.levelgen.heightproviders.*) ----
//
// The height_range modifier's `height` field. JAR-CONFIRMED draw bodies for the 3
// types the overworld placed_features use: uniform, trapezoid, very_biased_to_bottom.
// All resolve their two VerticalAnchors first (0 draws), then draw via
// Mth.randomBetweenInclusive / Mth.nextInt (inclusive nextInt(hi-lo+1) helpers).

type heightProviderKind int

const (
	heightUniform heightProviderKind = iota
	heightTrapezoid
	heightVeryBiasedToBottom
)

// heightProvider ports HeightProvider (uniform / trapezoid / very_biased_to_bottom).
type heightProvider struct {
	kind heightProviderKind
	min  verticalAnchor
	max  verticalAnchor
	// trapezoid plateau (TrapezoidHeight.plateau); very_biased inner band
	// (VeryBiasedToBottomHeight.inner).
	plateau int
	inner   int
}

// mthRandomBetweenInclusive ports Mth.randomBetweenInclusive(rng, lo, hi):
// lo + rng.nextInt(hi - lo + 1). Caller guarantees hi >= lo.
func mthRandomBetweenInclusive(rng levelgen.RandomSource, lo, hi int) int {
	return lo + int(rng.NextIntN(int32(hi-lo+1)))
}

// mthNextInt ports Mth.nextInt(rng, lo, hi): if lo >= hi return lo (NO draw);
// else lo + rng.nextInt(hi - lo + 1). (very_biased_to_bottom uses this variant.)
func mthNextInt(rng levelgen.RandomSource, lo, hi int) int {
	if lo >= hi {
		return lo
	}
	return lo + int(rng.NextIntN(int32(hi-lo+1)))
}

// sample ports HeightProvider.sample(rng, WorldGenerationContext) for the 3 kinds.
func (hp heightProvider) sample(rng levelgen.RandomSource, ctx PlacementContext) int {
	minY, genDepth := ctx.MinY(), ctx.Height()
	lo := hp.min.resolveY(minY, genDepth)
	hi := hp.max.resolveY(minY, genDepth)

	switch hp.kind {
	case heightUniform:
		// UniformHeight.sample: lo > hi -> lo (empty range, NO draw); else
		// Mth.randomBetweenInclusive(rng, lo, hi).
		if lo > hi {
			return lo
		}
		return mthRandomBetweenInclusive(rng, lo, hi)

	case heightTrapezoid:
		// TrapezoidHeight.sample (JAR-CONFIRMED):
		//   if lo > hi: return lo (no draw)
		//   range = hi - lo
		//   if plateau >= range: return Mth.randomBetweenInclusive(rng, lo, hi)  (1 draw)
		//   m = (range - plateau) / 2; n = range - m
		//   return lo + randomBetweenInclusive(0,n) + randomBetweenInclusive(0,m)  (2 draws)
		if lo > hi {
			return lo
		}
		rng2 := hi - lo
		if hp.plateau >= rng2 {
			return mthRandomBetweenInclusive(rng, lo, hi)
		}
		m := (rng2 - hp.plateau) / 2
		n := rng2 - m
		return lo + mthRandomBetweenInclusive(rng, 0, n) + mthRandomBetweenInclusive(rng, 0, m)

	case heightVeryBiasedToBottom:
		// VeryBiasedToBottomHeight.sample (JAR-CONFIRMED):
		//   if (hi - lo - inner + 1) <= 0: return lo (no draw)
		//   a = Mth.nextInt(rng, lo+inner, hi)         (draw 1)
		//   b = Mth.nextInt(rng, lo, a-1)              (draw 2)
		//   return Mth.nextInt(rng, lo, b-1+inner)     (draw 3)
		if hi-lo-hp.inner+1 <= 0 {
			return lo
		}
		a := mthNextInt(rng, lo+hp.inner, hi)
		b := mthNextInt(rng, lo, a-1)
		return mthNextInt(rng, lo, b-1+hp.inner)

	default:
		panic(fmt.Sprintf("placement: invalid height provider kind %d", hp.kind))
	}
}

// jsonHeightProvider is a HeightProvider JSON object (or a bare absolute int, which
// vanilla decodes as a constant ConstantHeight — modeled here as uniform[v,v]).
type jsonHeightProvider struct {
	Type         string     `json:"type"`
	Value        jsonAnchor `json:"value"`
	MinInclusive jsonAnchor `json:"min_inclusive"`
	MaxInclusive jsonAnchor `json:"max_inclusive"`
	Plateau      *int       `json:"plateau"`
	Inner        *int       `json:"inner"`
}

// parseHeightProvider decodes a HeightProvider, erroring loudly on an unported type.
func parseHeightProvider(raw json.RawMessage) (heightProvider, error) {
	if len(raw) == 0 {
		return heightProvider{}, fmt.Errorf("placement: empty height provider")
	}
	// A bare integer is a constant height (ConstantHeight): absolute[v]..absolute[v].
	var num int
	if err := json.Unmarshal(raw, &num); err == nil {
		a := verticalAnchor{kind: anchorAbsolute, offset: num}
		return heightProvider{kind: heightUniform, min: a, max: a}, nil
	}
	var j jsonHeightProvider
	if err := json.Unmarshal(raw, &j); err != nil {
		return heightProvider{}, fmt.Errorf("placement: height provider: %w", err)
	}
	switch j.Type {
	case "minecraft:constant", "constant":
		a, err := parseAnchor(j.Value)
		if err != nil {
			return heightProvider{}, fmt.Errorf("placement: constant height: %w", err)
		}
		return heightProvider{kind: heightUniform, min: a, max: a}, nil
	case "minecraft:uniform", "uniform":
		lo, err := parseAnchor(j.MinInclusive)
		if err != nil {
			return heightProvider{}, fmt.Errorf("placement: uniform height min: %w", err)
		}
		hi, err := parseAnchor(j.MaxInclusive)
		if err != nil {
			return heightProvider{}, fmt.Errorf("placement: uniform height max: %w", err)
		}
		return heightProvider{kind: heightUniform, min: lo, max: hi}, nil
	case "minecraft:trapezoid", "trapezoid":
		lo, err := parseAnchor(j.MinInclusive)
		if err != nil {
			return heightProvider{}, fmt.Errorf("placement: trapezoid height min: %w", err)
		}
		hi, err := parseAnchor(j.MaxInclusive)
		if err != nil {
			return heightProvider{}, fmt.Errorf("placement: trapezoid height max: %w", err)
		}
		p := 0
		if j.Plateau != nil {
			p = *j.Plateau
		}
		return heightProvider{kind: heightTrapezoid, min: lo, max: hi, plateau: p}, nil
	case "minecraft:very_biased_to_bottom", "very_biased_to_bottom":
		lo, err := parseAnchor(j.MinInclusive)
		if err != nil {
			return heightProvider{}, fmt.Errorf("placement: very_biased_to_bottom min: %w", err)
		}
		hi, err := parseAnchor(j.MaxInclusive)
		if err != nil {
			return heightProvider{}, fmt.Errorf("placement: very_biased_to_bottom max: %w", err)
		}
		inner := 1
		if j.Inner != nil {
			inner = *j.Inner
		}
		return heightProvider{kind: heightVeryBiasedToBottom, min: lo, max: hi, inner: inner}, nil
	default:
		return heightProvider{}, fmt.Errorf("placement: unsupported height provider type %q (defer per research)", j.Type)
	}
}

// ---- IntProvider (net.minecraft.util.valueproviders.IntProvider) ----
//
// The count modifier's `count` field. JAR-CONFIRMED draw bodies for the types the
// overworld count modifiers use: constant (bare int, 0 draws), uniform, clamped,
// biased_to_bottom, weighted_list. (clamped_normal/very_biased only appear in the
// deferred random_offset modifier, never in count, so they are intentionally
// unported here and error loudly.)

type intProviderKind int

const (
	intConstant intProviderKind = iota
	intUniform
	intClamped
	intBiasedToBottom
	intWeightedList
)

// weightedIntEntry is one (provider, weight) pair of a WeightedListInt.
type weightedIntEntry struct {
	provider *intProvider
	weight   int
}

// intProvider ports the IntProvider subset count uses.
type intProvider struct {
	kind intProviderKind
	// constant: value; uniform/clamped/biased: minVal/maxVal.
	value          int
	minVal, maxVal int
	// clamped: the inner source whose sample is clamped to [minVal,maxVal].
	source *intProvider
	// weighted_list: the distribution + its precomputed total weight.
	entries     []weightedIntEntry
	totalWeight int
}

// Sample ports IntProvider.sample(RandomSource). The draw count per kind is
// JAR-exact (Pitfall 4): constant 0, uniform 1, clamped = source's draws,
// biased_to_bottom 2 (inner then outer), weighted_list 1 (the pick) + the chosen
// entry's draws.
func (ip intProvider) Sample(rng levelgen.RandomSource) int {
	switch ip.kind {
	case intConstant:
		// ConstantInt.sample: return value. NO draw.
		return ip.value
	case intUniform:
		// UniformInt.sample = Mth.randomBetweenInclusive(rng, min, max) =
		// min + nextInt(max-min+1). 1 draw.
		return ip.minVal + int(rng.NextIntN(int32(ip.maxVal-ip.minVal+1)))
	case intClamped:
		// ClampedInt.sample = clamp(source.sample(rng), min, max). source draws.
		v := ip.source.Sample(rng)
		if v < ip.minVal {
			return ip.minVal
		}
		if v > ip.maxVal {
			return ip.maxVal
		}
		return v
	case intBiasedToBottom:
		// BiasedToBottomInt.sample = min + rng.nextInt(rng.nextInt(max-min+1)+1).
		// The INNER nextInt draws first, then the OUTER. 2 draws.
		inner := int(rng.NextIntN(int32(ip.maxVal-ip.minVal+1))) + 1
		return ip.minVal + int(rng.NextIntN(int32(inner)))
	case intWeightedList:
		// WeightedListInt.sample = distribution.getRandomOrThrow(rng).sample(rng).
		// getRandomOrThrow draws nextInt(totalWeight) FIRST to pick the entry, then
		// the chosen IntProvider samples.
		pick := int(rng.NextIntN(int32(ip.totalWeight)))
		chosen := ip.entries[len(ip.entries)-1].provider
		acc := 0
		for _, e := range ip.entries {
			acc += e.weight
			if pick < acc {
				chosen = e.provider
				break
			}
		}
		return chosen.Sample(rng)
	default:
		panic(fmt.Sprintf("placement: invalid int provider kind %d", ip.kind))
	}
}

// jsonIntProvider is an IntProvider JSON object (or a bare int = constant).
type jsonIntProvider struct {
	Type         string          `json:"type"`
	Value        *int            `json:"value"`
	MinInclusive *int            `json:"min_inclusive"`
	MaxInclusive *int            `json:"max_inclusive"`
	Source       json.RawMessage `json:"source"`
	Distribution []struct {
		Data   json.RawMessage `json:"data"`
		Weight int             `json:"weight"`
	} `json:"distribution"`
}

// parseIntProvider decodes an IntProvider, erroring loudly on an unported type.
func parseIntProvider(raw json.RawMessage) (*intProvider, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("placement: empty int provider")
	}
	// A bare integer is a constant.
	var num int
	if err := json.Unmarshal(raw, &num); err == nil {
		return &intProvider{kind: intConstant, value: num}, nil
	}
	var j jsonIntProvider
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("placement: int provider: %w", err)
	}
	switch j.Type {
	case "minecraft:constant", "constant":
		if j.Value == nil {
			return nil, fmt.Errorf("placement: constant int provider missing value")
		}
		return &intProvider{kind: intConstant, value: *j.Value}, nil
	case "minecraft:uniform", "uniform":
		if j.MinInclusive == nil || j.MaxInclusive == nil {
			return nil, fmt.Errorf("placement: uniform int provider missing bounds")
		}
		return &intProvider{kind: intUniform, minVal: *j.MinInclusive, maxVal: *j.MaxInclusive}, nil
	case "minecraft:clamped", "clamped":
		if j.MinInclusive == nil || j.MaxInclusive == nil {
			return nil, fmt.Errorf("placement: clamped int provider missing bounds")
		}
		src, err := parseIntProvider(j.Source)
		if err != nil {
			return nil, fmt.Errorf("placement: clamped int provider source: %w", err)
		}
		return &intProvider{kind: intClamped, minVal: *j.MinInclusive, maxVal: *j.MaxInclusive, source: src}, nil
	case "minecraft:biased_to_bottom", "biased_to_bottom":
		if j.MinInclusive == nil || j.MaxInclusive == nil {
			return nil, fmt.Errorf("placement: biased_to_bottom int provider missing bounds")
		}
		return &intProvider{kind: intBiasedToBottom, minVal: *j.MinInclusive, maxVal: *j.MaxInclusive}, nil
	case "minecraft:weighted_list", "weighted_list":
		if len(j.Distribution) == 0 {
			return nil, fmt.Errorf("placement: weighted_list int provider has empty distribution")
		}
		ip := &intProvider{kind: intWeightedList}
		for _, d := range j.Distribution {
			sub, err := parseIntProvider(d.Data)
			if err != nil {
				return nil, fmt.Errorf("placement: weighted_list entry: %w", err)
			}
			ip.entries = append(ip.entries, weightedIntEntry{provider: sub, weight: d.Weight})
			ip.totalWeight += d.Weight
		}
		if ip.totalWeight <= 0 {
			return nil, fmt.Errorf("placement: weighted_list int provider has non-positive total weight")
		}
		return ip, nil
	default:
		return nil, fmt.Errorf("placement: unsupported int provider type %q (clamped_normal/very_biased only appear in deferred random_offset)", j.Type)
	}
}
