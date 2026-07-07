// Package carver ports the legacy WorldCarver pass of Minecraft 26.2 (protocol
// 776) — the SEPARATE generation step that carves the classic winding tunnel
// caves (CaveWorldCarver) and the dramatic RAVINES (CanyonWorldCarver) ON TOP of
// the noise terrain. It is an additional pass, distinct from the noise caves (the
// negative-final_density regions the Wave-4 graph already produces): the plains
// biome references carvers:[minecraft:cave, minecraft:cave_extra_underground,
// minecraft:canyon], so vanilla runs these carvers over the filled chunk.
//
// CanyonWorldCarver = ravines; CaveWorldCarver = the extra tunnel caves. Both walk
// a randomized path (seeded from the world seed + the source-chunk coords via the
// legacy WorldgenRandom) and carve the already-placed blocks to air (or water below
// the local aquifer fluid level — the carve is aquifer-aware so a deep ravine
// floods). Only blocks in the overworld_carver_replaceables tag are carved; bedrock
// and non-replaceable blocks are left intact.
//
// Everything is PURE + deterministic over the world seed (PARITY-01 / Pitfall 7):
// no math/rand, no global mutable state. It consumes the Wave-1 configured_carver
// configs + the overworld_carver_replaceables tag as DATA, and is aquifer-aware via
// a FluidSource the Generator (Wave 8) wires to the Wave-5 Aquifer. Mineshafts-as-
// STRUCTURES are a separate (deferred) subsystem; the carver class of underground
// features (ravines + tunnels) is what this package delivers.
//
// Sources (javap -c, temp/cache/26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.carver.WorldCarver          (base carve/carveBlock/canReplaceBlock/carveEllipsoid/canReach/getRange/getCarveState)
//   - net.minecraft.world.level.levelgen.carver.CaveWorldCarver      (carve/createRoom/createTunnel/getThickness/getCaveBound)
//   - net.minecraft.world.level.levelgen.carver.CanyonWorldCarver    (carve/doCarve/initWidthFactors/updateVerticalRadius/shouldSkip)
//   - net.minecraft.world.level.levelgen.carver.CarverConfiguration  (y/yScale/lavaLevel/replaceable + probability)
//   - net.minecraft.world.level.levelgen.carver.CaveCarverConfiguration / CanyonCarverConfiguration (the shape params)
//   - net.minecraft.world.level.levelgen.carver.CanyonCarverConfiguration$CanyonShapeConfiguration  (widthFactors/thickness/radius factors)
//   - net.minecraft.world.level.chunk.NoiseBasedChunkGenerator.applyCarvers          (the cross-chunk driver + the per-source-chunk seeding)
//   - net.minecraft.world.level.levelgen.LegacyRandomSource / BitRandomSource        (the java.util.Random LCG the carver seeds from)
//   - net.minecraft.world.level.levelgen.WorldgenRandom.setLargeFeatureSeed          (the per-source-chunk carver seed mix)
//   - net.minecraft.world.level.levelgen.heightproviders.UniformHeight + util.valueproviders.{UniformFloat,TrapezoidFloat} (the providers the configs use)
//
// It is an algorithmic port, NOT a copy of Mojang source.
package carver

import (
	"encoding/json"
	"fmt"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen/data"
)

// CarverKind distinguishes the two carve shapes a CarverConfig drives.
type CarverKind int

const (
	// KindCave is the CaveWorldCarver shape (winding tunnels + rooms).
	KindCave CarverKind = iota
	// KindCanyon is the CanyonWorldCarver shape (a tall narrow ravine).
	KindCanyon
	// KindNetherCave is the NetherWorldCarver shape: the same winding-tunnel walk as KindCave with
	// getCaveBound()=10, getYScale()=5.0, a doubled getThickness(), and a lava-floor carveBlock
	// (LAVA at/below minGenY+31, else CAVE_AIR — no aquifer). CITE: NetherWorldCarver.
	KindNetherCave
)

// floatProvider ports the subset of net.minecraft.util.valueproviders.FloatProvider
// the carver configs use: a constant, a uniform [min,max) range, or a trapezoid.
// sample(rng) mirrors the corresponding *.sample(RandomSource) bytecode.
type floatProvider struct {
	kind    string  // "constant" | "uniform" | "trapezoid"
	a, b, c float64 // constant: a; uniform: min(a)/max(b); trapezoid: min(a)/max(b)/plateau(c)
}

// sample ports FloatProvider.sample(RandomSource).
func (fp floatProvider) sample(r *legacyRandom) float64 {
	switch fp.kind {
	case "constant":
		return fp.a
	case "uniform":
		// UniformFloat.sample = Mth.randomBetween(min,max) = min + nextFloat()*(max-min).
		return fp.a + float64(r.nextFloat())*(fp.b-fp.a)
	case "trapezoid":
		// TrapezoidFloat.sample: f2 = max-min; f3 = (f2-plateau)/2; f4 = f2-f3;
		//   min + nextFloat()*f4 + nextFloat()*f3.
		f2 := fp.b - fp.a
		f3 := (f2 - fp.c) / 2.0
		f4 := f2 - f3
		return fp.a + float64(r.nextFloat())*f4 + float64(r.nextFloat())*f3
	default:
		panic("carver: unknown float provider kind " + fp.kind)
	}
}

// verticalAnchor ports net.minecraft.world.level.levelgen.VerticalAnchor: either an
// absolute Y or an above_bottom offset. resolveY needs the world floor (minGenY).
type verticalAnchor struct {
	absolute    bool
	value       int // absolute Y, or the above_bottom offset
	aboveBottom bool
}

// resolveY ports VerticalAnchor.resolveY(WorldGenerationContext): Absolute -> y;
// AboveBottom -> minGenY + offset.
func (va verticalAnchor) resolveY(minGenY int) int {
	if va.absolute {
		return va.value
	}
	if va.aboveBottom {
		return minGenY + va.value
	}
	return va.value
}

// heightProvider ports the uniform HeightProvider the carver `y` field uses: a
// uniform-inclusive draw between two VerticalAnchors.
type heightProvider struct {
	min verticalAnchor
	max verticalAnchor
}

// sample ports UniformHeight.sample = Mth.randomBetweenInclusive(rng, lo, hi) =
// lo + rng.nextInt(hi-lo+1), with the empty-range guard returning lo.
func (hp heightProvider) sample(r *legacyRandom, minGenY int) int {
	lo := hp.min.resolveY(minGenY)
	hi := hp.max.resolveY(minGenY)
	if lo > hi {
		return lo
	}
	return lo + int(r.nextIntN(int32(hi-lo+1)))
}

// canyonShape ports CanyonShapeConfiguration: the per-step width/height shaping that
// gives the ravine its tall-narrow profile.
type canyonShape struct {
	distanceFactor              floatProvider
	thickness                   floatProvider
	widthSmoothness             int
	horizontalRadiusFactor      floatProvider
	verticalRadiusDefaultFactor float64
	verticalRadiusCenterFactor  float64
}

// CarverConfig is the parsed configured_carver config (DATA): the probability gate,
// the y-range, the lava level, and the kind-specific shape params. The replaceable
// set is parsed separately (shared across carvers) into Replaceables.
type CarverConfig struct {
	Kind        CarverKind
	Probability float64
	Y           heightProvider
	YScaleFloat floatProvider // canyon: a FloatProvider; cave: a FloatProvider
	LavaLevel   verticalAnchor

	// Cave shape params.
	HorizontalRadiusMultiplier floatProvider
	VerticalRadiusMultiplier   floatProvider
	FloorLevel                 floatProvider

	// Canyon shape params.
	VerticalRotation floatProvider
	Shape            canyonShape
}

// --- JSON shapes (mirror the configured_carver/{cave,canyon}.json structure) ---

type jsonFloatProvider struct {
	// A bare number decodes as a constant; an object carries a type.
	Type         string   `json:"type"`
	Value        *float64 `json:"value"`
	MinInclusive *float64 `json:"min_inclusive"`
	MaxExclusive *float64 `json:"max_exclusive"`
	Min          *float64 `json:"min"`
	Max          *float64 `json:"max"`
	Plateau      *float64 `json:"plateau"`
}

type jsonVerticalAnchor struct {
	Absolute    *int `json:"absolute"`
	AboveBottom *int `json:"above_bottom"`
	BelowTop    *int `json:"below_top"`
}

type jsonHeightProvider struct {
	Type         string             `json:"type"`
	MinInclusive jsonVerticalAnchor `json:"min_inclusive"`
	MaxInclusive jsonVerticalAnchor `json:"max_inclusive"`
}

type jsonCanyonShape struct {
	DistanceFactor              json.RawMessage `json:"distance_factor"`
	Thickness                   json.RawMessage `json:"thickness"`
	WidthSmoothness             int             `json:"width_smoothness"`
	HorizontalRadiusFactor      json.RawMessage `json:"horizontal_radius_factor"`
	VerticalRadiusDefaultFactor float64         `json:"vertical_radius_default_factor"`
	VerticalRadiusCenterFactor  float64         `json:"vertical_radius_center_factor"`
}

type jsonCarverConfig struct {
	Type   string `json:"type"`
	Config struct {
		Probability                float64            `json:"probability"`
		Y                          jsonHeightProvider `json:"y"`
		YScale                     json.RawMessage    `json:"yScale"`
		LavaLevel                  jsonVerticalAnchor `json:"lava_level"`
		HorizontalRadiusMultiplier json.RawMessage    `json:"horizontal_radius_multiplier"`
		VerticalRadiusMultiplier   json.RawMessage    `json:"vertical_radius_multiplier"`
		FloorLevel                 json.RawMessage    `json:"floor_level"`
		VerticalRotation           json.RawMessage    `json:"vertical_rotation"`
		Shape                      *jsonCanyonShape   `json:"shape"`
	} `json:"config"`
}

// parseFloatProvider decodes a FloatProvider from raw JSON: a bare number is a
// constant; an object carries a "type" (minecraft:uniform / minecraft:trapezoid /
// minecraft:constant).
func parseFloatProvider(raw json.RawMessage) (floatProvider, error) {
	if len(raw) == 0 {
		return floatProvider{}, fmt.Errorf("carver: empty float provider")
	}
	// Try a bare number first (a constant).
	var num float64
	if err := json.Unmarshal(raw, &num); err == nil {
		return floatProvider{kind: "constant", a: num}, nil
	}
	var jp jsonFloatProvider
	if err := json.Unmarshal(raw, &jp); err != nil {
		return floatProvider{}, fmt.Errorf("carver: float provider: %w", err)
	}
	switch jp.Type {
	case "minecraft:constant", "constant":
		if jp.Value == nil {
			return floatProvider{}, fmt.Errorf("carver: constant float provider missing value")
		}
		return floatProvider{kind: "constant", a: *jp.Value}, nil
	case "minecraft:uniform", "uniform":
		if jp.MinInclusive == nil || jp.MaxExclusive == nil {
			return floatProvider{}, fmt.Errorf("carver: uniform float provider missing bounds")
		}
		return floatProvider{kind: "uniform", a: *jp.MinInclusive, b: *jp.MaxExclusive}, nil
	case "minecraft:trapezoid", "trapezoid":
		if jp.Min == nil || jp.Max == nil || jp.Plateau == nil {
			return floatProvider{}, fmt.Errorf("carver: trapezoid float provider missing bounds")
		}
		return floatProvider{kind: "trapezoid", a: *jp.Min, b: *jp.Max, c: *jp.Plateau}, nil
	default:
		return floatProvider{}, fmt.Errorf("carver: unsupported float provider type %q", jp.Type)
	}
}

// parseVerticalAnchor decodes a VerticalAnchor (absolute / above_bottom / below_top).
func parseVerticalAnchor(jv jsonVerticalAnchor) (verticalAnchor, error) {
	switch {
	case jv.Absolute != nil:
		return verticalAnchor{absolute: true, value: *jv.Absolute}, nil
	case jv.AboveBottom != nil:
		return verticalAnchor{aboveBottom: true, value: *jv.AboveBottom}, nil
	case jv.BelowTop != nil:
		// below_top is unused by the overworld cave/canyon configs; supported for completeness.
		return verticalAnchor{aboveBottom: false, value: *jv.BelowTop}, nil
	default:
		return verticalAnchor{}, fmt.Errorf("carver: vertical anchor missing absolute/above_bottom/below_top")
	}
}

// parseHeightProvider decodes the uniform HeightProvider the carver `y` uses.
func parseHeightProvider(jh jsonHeightProvider) (heightProvider, error) {
	lo, err := parseVerticalAnchor(jh.MinInclusive)
	if err != nil {
		return heightProvider{}, fmt.Errorf("carver: height min: %w", err)
	}
	hi, err := parseVerticalAnchor(jh.MaxInclusive)
	if err != nil {
		return heightProvider{}, fmt.Errorf("carver: height max: %w", err)
	}
	return heightProvider{min: lo, max: hi}, nil
}

// ParseCarverConfig parses a configured_carver registry id ("minecraft:cave" /
// "minecraft:canyon" / "minecraft:cave_extra_underground") into a CarverConfig.
func ParseCarverConfig(id string) (*CarverConfig, error) {
	raw, err := data.ConfiguredCarver(id)
	if err != nil {
		return nil, err
	}
	return parseCarverConfigBytes(raw)
}

func parseCarverConfigBytes(raw []byte) (*CarverConfig, error) {
	var jc jsonCarverConfig
	if err := json.Unmarshal(raw, &jc); err != nil {
		return nil, fmt.Errorf("carver: config: %w", err)
	}

	cfg := &CarverConfig{Probability: jc.Config.Probability}

	y, err := parseHeightProvider(jc.Config.Y)
	if err != nil {
		return nil, err
	}
	cfg.Y = y

	lava, err := parseVerticalAnchor(jc.Config.LavaLevel)
	if err != nil {
		return nil, fmt.Errorf("carver: lava_level: %w", err)
	}
	cfg.LavaLevel = lava

	if len(jc.Config.YScale) > 0 {
		ys, err := parseFloatProvider(jc.Config.YScale)
		if err != nil {
			return nil, fmt.Errorf("carver: yScale: %w", err)
		}
		cfg.YScaleFloat = ys
	}

	switch jc.Type {
	case "minecraft:cave", "cave", "minecraft:nether_cave", "nether_cave":
		// nether_cave is CaveCarverConfiguration too — same fields as cave; only the carver's
		// bound/yscale/thickness/carveBlock differ (KindNetherCave selects that impl).
		if jc.Type == "minecraft:nether_cave" || jc.Type == "nether_cave" {
			cfg.Kind = KindNetherCave
		} else {
			cfg.Kind = KindCave
		}
		if cfg.HorizontalRadiusMultiplier, err = parseFloatProvider(jc.Config.HorizontalRadiusMultiplier); err != nil {
			return nil, fmt.Errorf("carver: horizontal_radius_multiplier: %w", err)
		}
		if cfg.VerticalRadiusMultiplier, err = parseFloatProvider(jc.Config.VerticalRadiusMultiplier); err != nil {
			return nil, fmt.Errorf("carver: vertical_radius_multiplier: %w", err)
		}
		if cfg.FloorLevel, err = parseFloatProvider(jc.Config.FloorLevel); err != nil {
			return nil, fmt.Errorf("carver: floor_level: %w", err)
		}
	case "minecraft:canyon", "canyon":
		cfg.Kind = KindCanyon
		if cfg.VerticalRotation, err = parseFloatProvider(jc.Config.VerticalRotation); err != nil {
			return nil, fmt.Errorf("carver: vertical_rotation: %w", err)
		}
		if jc.Config.Shape == nil {
			return nil, fmt.Errorf("carver: canyon config missing shape")
		}
		sh := jc.Config.Shape
		shape := canyonShape{
			widthSmoothness:             sh.WidthSmoothness,
			verticalRadiusDefaultFactor: sh.VerticalRadiusDefaultFactor,
			verticalRadiusCenterFactor:  sh.VerticalRadiusCenterFactor,
		}
		if shape.distanceFactor, err = parseFloatProvider(sh.DistanceFactor); err != nil {
			return nil, fmt.Errorf("carver: shape.distance_factor: %w", err)
		}
		if shape.thickness, err = parseFloatProvider(sh.Thickness); err != nil {
			return nil, fmt.Errorf("carver: shape.thickness: %w", err)
		}
		if shape.horizontalRadiusFactor, err = parseFloatProvider(sh.HorizontalRadiusFactor); err != nil {
			return nil, fmt.Errorf("carver: shape.horizontal_radius_factor: %w", err)
		}
		cfg.Shape = shape
	default:
		return nil, fmt.Errorf("carver: unsupported carver type %q", jc.Type)
	}

	return cfg, nil
}

// Replaceables is the set of block StateIDs the carver may replace (the
// overworld_carver_replaceables tag, resolved to concrete states). canReplaceBlock
// gates every carve on membership: only stone/dirt/sand/etc. are carved, never
// bedrock or non-replaceable blocks.
type Replaceables struct {
	set map[block.StateID]bool
}

// Has reports whether a state id is carve-able.
func (rp *Replaceables) Has(s block.StateID) bool { return rp.set[s] }

// Size returns the number of carve-able state ids (for tests).
func (rp *Replaceables) Size() int { return len(rp.set) }

// jsonTag is the {"values":[...]} block-tag shape.
type jsonTag struct {
	Values []json.RawMessage `json:"values"`
}

// nestedReplaceableTags maps the #minecraft:* nested tag references in
// overworld_carver_replaceables to their member block ids. The base block tags
// (base_stone_overworld, substrate_overworld, sand, ...) are NOT embedded as DATA
// (only the top-level carver_replaceables tag is), so their membership is resolved
// here constant-for-constant from the vanilla 26.2 block-tag definitions (the
// data/minecraft/tags/block/*.json the jar ships). Cited inline.
var nestedReplaceableTags = map[string][]string{
	// #minecraft:base_stone_overworld
	"minecraft:base_stone_overworld": {
		"minecraft:stone", "minecraft:granite", "minecraft:diorite",
		"minecraft:andesite", "minecraft:tuff", "minecraft:deepslate",
	},
	// #minecraft:substrate_overworld (dirt-like substrate the carver may replace).
	"minecraft:substrate_overworld": {
		"minecraft:dirt", "minecraft:coarse_dirt", "minecraft:podzol",
		"minecraft:mycelium", "minecraft:grass_block", "minecraft:rooted_dirt",
		"minecraft:mud", "minecraft:clay",
	},
	// #minecraft:sand
	"minecraft:sand": {"minecraft:sand", "minecraft:red_sand"},
	// #minecraft:terracotta (the plain + the 16 colored terracottas; the plain is
	// what the overworld carve actually meets — the colored variants are listed for
	// completeness so a badlands carve is also faithful).
	"minecraft:terracotta": {
		"minecraft:terracotta",
		"minecraft:white_terracotta", "minecraft:orange_terracotta",
		"minecraft:magenta_terracotta", "minecraft:light_blue_terracotta",
		"minecraft:yellow_terracotta", "minecraft:lime_terracotta",
		"minecraft:pink_terracotta", "minecraft:gray_terracotta",
		"minecraft:light_gray_terracotta", "minecraft:cyan_terracotta",
		"minecraft:purple_terracotta", "minecraft:blue_terracotta",
		"minecraft:brown_terracotta", "minecraft:green_terracotta",
		"minecraft:red_terracotta", "minecraft:black_terracotta",
	},
	// #minecraft:iron_ores
	"minecraft:iron_ores": {"minecraft:iron_ore", "minecraft:deepslate_iron_ore"},
	// #minecraft:copper_ores
	"minecraft:copper_ores": {"minecraft:copper_ore", "minecraft:deepslate_copper_ore"},
	// #minecraft:snow
	"minecraft:snow": {"minecraft:snow", "minecraft:snow_block", "minecraft:powder_snow"},
}

// ParseReplaceables parses the overworld_carver_replaceables tag (DATA) into a
// StateID set. Nested #minecraft:* tag references are resolved via
// nestedReplaceableTags; direct block ids resolve against the block registry. Blocks
// with state properties (e.g. snow Layers, deepslate Axis) are expanded over ALL of
// their states so any property variant the fill placed is carve-able.
func ParseReplaceables() (*Replaceables, error) {
	raw, err := data.CarverReplaceables()
	if err != nil {
		return nil, err
	}
	return parseReplaceablesBytes(raw)
}

func parseReplaceablesBytes(raw []byte) (*Replaceables, error) {
	var tag jsonTag
	if err := json.Unmarshal(raw, &tag); err != nil {
		return nil, fmt.Errorf("carver: replaceables tag: %w", err)
	}

	// Collect the target block ids (resolving nested tags).
	ids := map[string]bool{}
	for _, v := range tag.Values {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			// Object form ({"id":...,"required":...}); pull the id.
			var obj struct {
				ID string `json:"id"`
			}
			if err2 := json.Unmarshal(v, &obj); err2 != nil || obj.ID == "" {
				return nil, fmt.Errorf("carver: replaceables entry: %w", err)
			}
			s = obj.ID
		}
		if len(s) > 0 && s[0] == '#' {
			members, ok := nestedReplaceableTags[s[1:]]
			if !ok {
				return nil, fmt.Errorf("carver: unknown nested replaceable tag %q", s)
			}
			for _, m := range members {
				ids[m] = true
			}
			continue
		}
		ids[s] = true
	}

	// Resolve each block id to ALL of its state ids (any property variant is carve-able).
	set := make(map[block.StateID]bool, len(ids)*2)
	for sid, b := range block.StateList {
		if ids[b.ID()] {
			set[block.StateID(sid)] = true
		}
	}
	if len(set) == 0 {
		return nil, fmt.Errorf("carver: replaceables resolved to an empty set")
	}
	return &Replaceables{set: set}, nil
}
