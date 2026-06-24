package noisechunk

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/density"
	"github.com/imhinotori/sulfur/world/levelgen/router"
)

// OreVeinifier ports net.minecraft.world.level.levelgen.OreVeinifier (javap -c,
// 26.2-inner.jar). ore_veins_enabled is true in overworld.json, so for every SOLID
// block the doFill consults the veinifier: it samples vein_toggle (sign selects the
// VeinType — COPPER for the upper band, IRON for the deep band), vein_ridged + vein_gap
// (shape + carve the deposit), and a per-position random (the ore-vs-raw-vs-filler
// chance), placing the VeinType's ore / raw-ore-block / filler — else the position is
// not part of a vein (caller keeps the default stone/deepslate).
//
// It is a faithful algorithmic port of OreVeinifier.create's lambda (calculate),
// constant-for-constant from the bytecode, NOT a copy of Mojang source.
//
// Source (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.levelgen.OreVeinifier            (create + the calculate lambda)
//   - net.minecraft.world.level.levelgen.OreVeinifier$VeinType   (COPPER/IRON: ore/raw/filler + minY/maxY)
type OreVeinifier struct {
	veinToggle density.Function
	veinRidged density.Function
	veinGap    density.Function
	oreRandom  levelgen.PositionalRandomFactory

	copper veinType
	iron   veinType
}

// veinType ports OreVeinifier$VeinType — the ore/raw/filler block ids + the y-range a
// vein of that type occupies. The ids are resolved once (the bytecode reads
// Blocks.X.defaultBlockState()):
//
//	COPPER: ore=copper_ore         raw=raw_copper_block filler=granite minY=0   maxY=50
//	IRON:   ore=deepslate_iron_ore raw=raw_iron_block   filler=tuff    minY=-60 maxY=-8
type veinType struct {
	ore    block.StateID
	raw    block.StateID
	filler block.StateID
	minY   int
	maxY   int
}

// OreVeinifier tuning constants, read constant-for-constant from the OreVeinifier
// calculate bytecode (the `final` fields are inlined as literals at the use sites):
//
//	VEININESS_THRESHOLD          = 0.4   (|toggle|+roundoff must reach it)
//	EDGE_ROUNDOFF_BEGIN          = 20    (the y-distance over which the edge softens)
//	MAX_EDGE_ROUNDOFF            = 0.2   (clampedMap maps to [-0.2, 0])
//	VEIN_SOLIDNESS               = 0.7   (nextFloat() > 0.7 -> skip)
//	MIN_RICHNESS / MAX_RICHNESS  = 0.1 / 0.3  (richness clampedMap output band)
//	MAX_RICHNESS_THRESHOLD       = 0.6   (the upper |toggle| for the richness map)
//	CHANCE_OF_RAW_ORE_BLOCK      = 0.02  (nextFloat() < 0.02 -> raw block)
//	SKIP_ORE_IF_GAP_NOISE_BELOW  = -0.3  (vein_gap must exceed it for the raw-ore upgrade)
const (
	veininessThreshold     = 0.4
	edgeRoundoffBegin      = 20.0
	maxEdgeRoundoff        = -0.2
	veinSolidness          = 0.7
	minRichness            = 0.1
	maxRichness            = 0.3
	maxRichnessThreshold   = 0.6
	chanceOfRawOreBlock    = 0.02
	skipOreIfGapNoiseBelow = -0.3
	copperMinY, copperMaxY = 0, 50
	ironMinY, ironMaxY     = -60, -8
)

// NewOreVeinifier builds the veinifier from the bound vein_* router functions and the
// per-world ore positional random (RandomState forks "minecraft:ore" — OreRandom()).
func NewOreVeinifier(veinToggle, veinRidged, veinGap density.Function, rs *router.RandomState) *OreVeinifier {
	return &OreVeinifier{
		veinToggle: veinToggle,
		veinRidged: veinRidged,
		veinGap:    veinGap,
		oreRandom:  rs.OreRandom(),
		copper: veinType{
			ore:    block.ToStateID[block.CopperOre{}],
			raw:    block.ToStateID[block.RawCopperBlock{}],
			filler: block.ToStateID[block.Granite{}],
			minY:   copperMinY,
			maxY:   copperMaxY,
		},
		iron: veinType{
			ore:    block.ToStateID[block.DeepslateIronOre{}],
			raw:    block.ToStateID[block.RawIronBlock{}],
			filler: block.ToStateID[block.Tuff{}],
			minY:   ironMinY,
			maxY:   ironMaxY,
		},
	}
}

// vein returns the ore/raw/filler block for a SOLID position if it falls inside an ore
// vein, else (0, false). It ports OreVeinifier.calculate (lambda$create$0):
//
//  1. d = vein_toggle(ctx); vt = d>0 ? COPPER : IRON
//  2. absD = |d|; gate on vt.minY/maxY (else default)
//  3. roundoff = clampedMap(min(yDistToMax, yDistToMin), 0, 20, -0.2, 0)
//  4. if absD + roundoff < 0.4 -> default
//  5. r = oreRandom.at(x,y,z); if r.nextFloat() > 0.7 -> default
//  6. if vein_ridged(ctx) >= 0 -> default
//  7. richness = clampedMap(absD, 0.4, 0.6, 0.1, 0.3)
//  8. if r.nextFloat() < richness:
//     if vein_gap(ctx) > -0.3 && r.nextFloat() < 0.02 -> raw; else -> ore
//     else -> filler
//
// The default (DEBUG_ORE_VEINS off) is "not a vein" — represented here as (0, false).
func (ov *OreVeinifier) vein(x, y, z int) (block.StateID, bool) {
	ctx := density.Context{X: x, Y: y, Z: z}
	d := ov.veinToggle.Compute(ctx)

	vt := ov.iron
	if d > 0 {
		vt = ov.copper
	}

	absD := d
	if absD < 0 {
		absD = -absD
	}

	yDistToMax := vt.maxY - y
	yDistToMin := y - vt.minY
	if yDistToMin < 0 || yDistToMax < 0 {
		return 0, false
	}

	edge := minInt(yDistToMax, yDistToMin)
	roundoff := clampedMap(float64(edge), 0.0, edgeRoundoffBegin, maxEdgeRoundoff, 0.0)
	if absD+roundoff < veininessThreshold {
		return 0, false
	}

	r := ov.oreRandom.At(x, y, z)
	if r.NextFloat() > veinSolidness {
		return 0, false
	}
	if ov.veinRidged.Compute(ctx) >= 0.0 {
		return 0, false
	}

	richness := clampedMap(absD, veininessThreshold, maxRichnessThreshold, minRichness, maxRichness)
	if float64(r.NextFloat()) < richness {
		if ov.veinGap.Compute(ctx) > skipOreIfGapNoiseBelow && r.NextFloat() < chanceOfRawOreBlock {
			return vt.raw, true
		}
		return vt.ore, true
	}
	return vt.filler, true
}

// minInt is Math.min(int,int) — Go has no int min before generics in the hot path here
// and we keep a tiny local helper to mirror the bytecode literally.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// clampedMap ports net.minecraft.util.Mth.clampedMap(v, in0, in1, out0, out1) =
// clampedLerp(out0, out1, inverseLerp(v, in0, in1)).
func clampedMap(v, in0, in1, out0, out1 float64) float64 {
	return clampedLerpD(out0, out1, inverseLerpD(v, in0, in1))
}

// inverseLerpD ports Mth.inverseLerp(v, a, b) = (v-a)/(b-a).
func inverseLerpD(v, a, b float64) float64 { return (v - a) / (b - a) }

// clampedLerpD ports Mth.clampedLerp(a, b, t): clamp t to [0,1] then lerp.
func clampedLerpD(a, b, t float64) float64 {
	if t < 0 {
		return a
	}
	if t > 1 {
		return b
	}
	return a + t*(b-a)
}
