// This file ports Tier-D of the Minecraft 26.2 (protocol 776) world generator from
// the unobfuscated server jar (temp/cache/26.2-inner.jar, javap -c):
//
//   - NoiseGeneratorSettings — the parsed overworld.json settings struct (noise dims,
//     default block/fluid, sea level, aquifers/ore-veins enabled, legacy_random_source).
//   - NoiseRouter            — the 15 named density functions the runtime evaluates
//     (final_density + the Aquifer inputs barrier/fluid_level_*/lava + the OreVeinifier
//     inputs vein_* + the climate continents/depth/erosion/ridges/temperature/vegetation
//   - preliminary_surface_level for the Wave-7 surface rules).
//   - RandomState            — the seeding BRIDGE: it holds the per-world positional
//     random factory (Xoroshiro(seed).forkPositional()) and seeds every router noise
//     node's NormalNoise via factory.FromHashOf(noise registry id) (Pitfall 1, the
//     determinism hinge). It implements density.NoiseBinder so the parser binds noise
//     nodes to their seeded primitives as it builds the graph.
//
// NewRouter(seed) is PURE: same seed -> identical bound graph -> identical evaluation
// (Pitfall 7). The whole graph (DATA from Wave 1) is parsed once and bound to the
// Tier-A/B primitives (Wave 2); caves are negative-density regions of final_density.
//
// Sources (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.NoiseGeneratorSettings
//   - net.minecraft.world.level.levelgen.NoiseRouter
//   - net.minecraft.world.level.levelgen.RandomState (create, getOrCreateNoise, the
//     NoiseWiringHelper visitor that seeds each noise via random.fromHashOf(id))
//   - net.minecraft.world.level.levelgen.Noises.instantiate (NormalNoise.create(
//     factory.fromHashOf(key.identifier()), params))
//
// It is a faithful algorithmic port, not a copy of Mojang source.
//
// PACKAGE LOCATION NOTE: this is package `router` (under world/levelgen/router/), not
// package `levelgen`. The Tier-A primitives (RandomSource/PositionalRandomFactory/
// Xoroshiro) live in package levelgen, which synth and density transitively import; a
// composition root that imports density therefore CANNOT sit in package levelgen
// without an import cycle (levelgen -> density -> synth -> levelgen). Router is that
// composition root, so it lives one level up as its own leaf-above package.
package router

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/data"
	"github.com/imhinotori/sulfur/world/levelgen/density"
	"github.com/imhinotori/sulfur/world/levelgen/synth"
)

// NoiseSettings is the cell/height geometry (NoiseSettings: height/min_y/
// size_horizontal/size_vertical).
type NoiseSettings struct {
	Height         int `json:"height"`
	MinY           int `json:"min_y"`
	SizeHorizontal int `json:"size_horizontal"`
	SizeVertical   int `json:"size_vertical"`
}

// blockState is the {Name, Properties} block reference used by default_block/fluid.
type blockState struct {
	Name       string            `json:"Name"`
	Properties map[string]string `json:"Properties,omitempty"`
}

// noiseGeneratorSettingsJSON is the raw overworld.json envelope. The noise_router and
// surface_rule are kept as RawMessage (the router is parsed by RandomState; the surface
// rule is consumed by Wave 7).
type noiseGeneratorSettingsJSON struct {
	Noise              NoiseSettings              `json:"noise"`
	DefaultBlock       blockState                 `json:"default_block"`
	DefaultFluid       blockState                 `json:"default_fluid"`
	SeaLevel           int                        `json:"sea_level"`
	LegacyRandomSource bool                       `json:"legacy_random_source"`
	AquifersEnabled    bool                       `json:"aquifers_enabled"`
	OreVeinsEnabled    bool                       `json:"ore_veins_enabled"`
	DisableMobGen      bool                       `json:"disable_mob_generation"`
	NoiseRouter        map[string]json.RawMessage `json:"noise_router"`
	SurfaceRule        json.RawMessage            `json:"surface_rule"`
}

// NoiseGeneratorSettings is the parsed overworld.json (DATA): the geometry, the default
// block/fluid, sea level, the feature toggles, and the raw surface_rule (Wave 7).
type NoiseGeneratorSettings struct {
	Noise              NoiseSettings
	DefaultBlock       string
	DefaultFluid       string
	SeaLevel           int
	LegacyRandomSource bool
	AquifersEnabled    bool
	OreVeinsEnabled    bool
	SurfaceRule        json.RawMessage
}

// The 15 router function names (NoiseRouter fields), in a fixed order so iteration is
// deterministic (Pitfall 7 — no map-iteration-order in the build path).
var routerFunctionNames = []string{
	"barrier",
	"fluid_level_floodedness",
	"fluid_level_spread",
	"lava",
	"temperature",
	"vegetation",
	"continents",
	"erosion",
	"depth",
	"ridges",
	"preliminary_surface_level",
	"final_density",
	"vein_toggle",
	"vein_ridged",
	"vein_gap",
}

// NoiseRouter holds the bound (parsed + seeded) router functions. Every field is an
// evaluable density.Function; the Aquifer (Wave 5) reads Barrier/FluidLevel*/Lava, the
// OreVeinifier (Wave 5) reads Vein*, Wave 4 samples FinalDensity, and Wave 7's surface
// rules read PreliminarySurfaceLevel.
type NoiseRouter struct {
	Barrier                 density.Function
	FluidLevelFloodedness   density.Function
	FluidLevelSpread        density.Function
	Lava                    density.Function
	Temperature             density.Function
	Vegetation              density.Function
	Continents              density.Function
	Erosion                 density.Function
	Depth                   density.Function
	Ridges                  density.Function
	PreliminarySurfaceLevel density.Function
	FinalDensity            density.Function
	VeinToggle              density.Function
	VeinRidged              density.Function
	VeinGap                 density.Function

	// byName indexes the bound functions for generic access (tests, Wave 5/7 lookups).
	byName map[string]density.Function
}

// Function returns a bound router function by name (the 15 routerFunctionNames).
func (r *NoiseRouter) Function(name string) (density.Function, bool) {
	fn, ok := r.byName[name]
	return fn, ok
}

// RandomState is the per-world seeding bridge. It owns the positional random factory
// (Xoroshiro(seed).forkPositional()) and a cache of seeded NormalNoise per registry id.
// It implements density.NoiseBinder so the parser binds noise nodes as it walks the
// graph. RandomState.create mirrors the bytecode: the base factory is forked once from
// the world seed and every noise is seeded via factory.FromHashOf(id).
type RandomState struct {
	factory levelgen.PositionalRandomFactory

	mu     sync.Mutex
	noises map[string]*synth.NormalNoise
}

// NewRandomState builds the per-world RandomState. The base positional random factory
// every noise/aquifer/ore/surface random forks from is
// settings.getRandomSource().newInstance(seed).forkPositional() (RandomState <init>,
// offsets 4-18). getRandomSource() (NoiseGeneratorSettings.getRandomSource) branches on
// the parsed useLegacyRandomSource flag: true -> WorldgenRandom.Algorithm.LEGACY
// (seed -> new LegacyRandomSource(seed)), false -> XOROSHIRO
// (seed -> new XoroshiroRandomSource(seed)). Newer overworld settings carry
// legacy_random_source:false (Xoroshiro); the nether, the end, and the pre-1.18-style
// settings carry legacy_random_source:true and MUST seed from the LCG-backed
// LegacyRandomSource, else the whole density graph draws off the wrong stream and the
// terrain diverges from vanilla.
//
// Both algorithms are already ported bit-for-bit in package levelgen
// (levelgen.NewXoroshiro / levelgen.NewLegacyRandomSource, each with a matching
// forkPositional -> PositionalRandomFactory whose At/FromHashOf mirror the vanilla
// LegacyPositionalRandomFactory / XoroshiroPositionalRandomFactory), so the router
// only has to consult the flag and pick.
//
// Sources (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.RandomState.<init> (getRandomSource().newInstance(seed).forkPositional())
//   - net.minecraft.world.level.levelgen.NoiseGeneratorSettings.getRandomSource (useLegacyRandomSource ? LEGACY : XOROSHIRO)
//   - net.minecraft.world.level.levelgen.WorldgenRandom$Algorithm (LEGACY / XOROSHIRO newInstance lambdas)
func NewRandomState(seed int64, useLegacyRandomSource bool) *RandomState {
	var base levelgen.RandomSource
	if useLegacyRandomSource {
		base = levelgen.NewLegacyRandomSource(seed)
	} else {
		base = levelgen.NewXoroshiro(seed)
	}
	return &RandomState{
		factory: base.ForkPositional(),
		noises:  make(map[string]*synth.NormalNoise),
	}
}

// AquiferRandom returns the Aquifer's per-position random factory. Vanilla
// RandomState.<init> forks it as base.fromHashOf("minecraft:aquifer").forkPositional()
// (the base = Xoroshiro(seed).forkPositional()); the Wave-5 Aquifer seeds its per-cell
// fluid-pick random from it via At(x,y,z). Ported constant-for-constant so the aquifer
// draws match vanilla bit-for-bit (Pitfall 7: all randomness flows from the world seed).
func (s *RandomState) AquiferRandom() levelgen.PositionalRandomFactory {
	return s.factory.FromHashOf("minecraft:aquifer").ForkPositional()
}

// OreRandom returns the OreVeinifier's per-position random factory. Vanilla forks it as
// base.fromHashOf("minecraft:ore").forkPositional(); the Wave-5 OreVeinifier seeds its
// per-block ore-vs-raw-vs-filler random from it via At(x,y,z). Ported constant-for-
// constant (Pitfall 7).
func (s *RandomState) OreRandom() levelgen.PositionalRandomFactory {
	return s.factory.FromHashOf("minecraft:ore").ForkPositional()
}

// BaseFactory returns the per-world base positional random factory
// (Xoroshiro(seed).forkPositional()). Vanilla RandomState passes THIS factory
// (its `random` field) straight to `new SurfaceSystem(..., random)` as the
// surfaceSystem's `noiseRandom` — the factory the Wave-7 SurfaceSystem.getSurfaceDepth
// jitter (random.at(x,0,z).nextDouble()) and the vertical_gradient surface rule
// (randomName.at(x,y,z).nextFloat()) draw from. Exposed so the surface package can
// mirror that wiring constant-for-constant (Pitfall 7: all surface randomness flows
// from the world seed).
func (s *RandomState) BaseFactory() levelgen.PositionalRandomFactory {
	return s.factory
}

// NormalNoise returns the seeded NormalNoise for a noise registry id, caching it
// (RandomState.getOrCreateNoise -> Noises.instantiate -> NormalNoise.create(
// factory.fromHashOf(id.identifier()), params)). The params come from the Wave-1
// embedded noise/*.json (Pitfall 2 — DATA, not hardcoded).
func (s *RandomState) NormalNoise(id string) (*synth.NormalNoise, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, ok := s.noises[id]; ok {
		return n, nil
	}
	raw, err := data.Noise(id)
	if err != nil {
		return nil, fmt.Errorf("random state: noise params %q: %w", id, err)
	}
	var p struct {
		FirstOctave int       `json:"firstOctave"`
		Amplitudes  []float64 `json:"amplitudes"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("random state: noise params %q: %w", id, err)
	}
	rs := s.factory.FromHashOf(id)
	n := synth.NewNormalNoise(rs, p.FirstOctave, p.Amplitudes)
	s.noises[id] = n
	return n, nil
}

// BlendedNoise seeds the legacy old_blended_noise. RandomState seeds the BlendedNoise
// from the base factory's "terrain" hash (the createLegacyForBlendedNoise path keys off
// the same positional factory); the scales come from the node's base_3d_noise fields.
func (s *RandomState) BlendedNoise(xzScale, yScale, xzFactor, yFactor, smearScaleMultiplier float64) (*synth.BlendedNoise, error) {
	rs := s.factory.FromHashOf("minecraft:terrain")
	return synth.NewBlendedNoise(rs, xzScale, yScale, xzFactor, yFactor, smearScaleMultiplier), nil
}

var _ density.NoiseBinder = (*RandomState)(nil)

// Router is the assembled, evaluable world generator graph: the parsed settings (DATA),
// the bound NoiseRouter (the 15 functions), and the RandomState that seeded them.
type Router struct {
	Settings    NoiseGeneratorSettings
	NoiseRouter *NoiseRouter
	Random      *RandomState
	Seed        int64
}

// NewRouter parses the wired overworld graph (DATA — Wave 1) and binds it to the
// seeded Tier-A/B primitives (Wave 2). It is PURE: same seed -> identical graph ->
// identical evaluation. It parses ALL 15 named router functions (not just
// final_density) so a missing/unported node type surfaces at construction, not at
// some later runtime sample (T-9-07).
func NewRouter(seed int64) (*Router, error) {
	return NewRouterFor(seed, "minecraft:overworld")
}

// NewRouterFor parses the wired noise graph for a SPECIFIC noise_settings registry id
// (e.g. "minecraft:overworld" or "minecraft:the_nether") and binds it to the seeded
// primitives. It is the dimension-parameterized form of NewRouter: the overworld path is
// NewRouter's alias; the nether generator passes "minecraft:the_nether" so the router,
// geometry (min_y/height/sea_level), default block/fluid, and surface_rule all come from
// nether.json. PURE over (seed, settingsID). CITE: NoiseBasedChunkGenerator binds the
// per-dimension NoiseGeneratorSettings the LevelStem references.
func NewRouterFor(seed int64, settingsID string) (*Router, error) {
	raw, err := data.NoiseSettings(settingsID)
	if err != nil {
		return nil, fmt.Errorf("router: load noise settings %q: %w", settingsID, err)
	}
	var js noiseGeneratorSettingsJSON
	if err := json.Unmarshal(raw, &js); err != nil {
		return nil, fmt.Errorf("router: parse noise settings %q: %w", settingsID, err)
	}
	settings := NoiseGeneratorSettings{
		Noise:              js.Noise,
		DefaultBlock:       js.DefaultBlock.Name,
		DefaultFluid:       js.DefaultFluid.Name,
		SeaLevel:           js.SeaLevel,
		LegacyRandomSource: js.LegacyRandomSource,
		AquifersEnabled:    js.AquifersEnabled,
		OreVeinsEnabled:    js.OreVeinsEnabled,
		SurfaceRule:        js.SurfaceRule,
	}

	// getRandomSource() reads useLegacyRandomSource (js.LegacyRandomSource) to pick the
	// LEGACY vs XOROSHIRO algorithm — RandomState.<init> seeds every random from it.
	rstate := NewRandomState(seed, js.LegacyRandomSource)
	// One registry over the whole graph so shared sub-graphs (the spline-heavy
	// offset/factor/jaggedness, the cache wrappers) are parsed + bound ONCE (dedup).
	reg := density.NewRegistry(density.DataSourceFunc(data.DensityFunction), rstate)

	nr := &NoiseRouter{byName: make(map[string]density.Function, len(routerFunctionNames))}
	for _, name := range routerFunctionNames {
		rawFn, ok := js.NoiseRouter[name]
		if !ok {
			return nil, fmt.Errorf("router: noise_router missing %q", name)
		}
		fn, err := reg.Parse(rawFn)
		if err != nil {
			return nil, fmt.Errorf("router: binding router function %q: %w", name, err)
		}
		nr.byName[name] = fn
	}
	bindRouterFields(nr)

	return &Router{
		Settings:    settings,
		NoiseRouter: nr,
		Random:      rstate,
		Seed:        seed,
	}, nil
}

// bindRouterFields copies the by-name bound functions onto the typed NoiseRouter fields.
func bindRouterFields(nr *NoiseRouter) {
	nr.Barrier = nr.byName["barrier"]
	nr.FluidLevelFloodedness = nr.byName["fluid_level_floodedness"]
	nr.FluidLevelSpread = nr.byName["fluid_level_spread"]
	nr.Lava = nr.byName["lava"]
	nr.Temperature = nr.byName["temperature"]
	nr.Vegetation = nr.byName["vegetation"]
	nr.Continents = nr.byName["continents"]
	nr.Erosion = nr.byName["erosion"]
	nr.Depth = nr.byName["depth"]
	nr.Ridges = nr.byName["ridges"]
	nr.PreliminarySurfaceLevel = nr.byName["preliminary_surface_level"]
	nr.FinalDensity = nr.byName["final_density"]
	nr.VeinToggle = nr.byName["vein_toggle"]
	nr.VeinRidged = nr.byName["vein_ridged"]
	nr.VeinGap = nr.byName["vein_gap"]
}
