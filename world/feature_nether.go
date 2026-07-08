package world

// feature_nether.go ports the bounded Nether configured-feature batch:
// glowstone_blob, weeping_vines, twisting_vines, and nether_forest_vegetation.
//
// Verified against Minecraft Java 26.2 bytecode with:
//   - net.minecraft.world.level.levelgen.feature.GlowstoneFeature
//   - net.minecraft.world.level.levelgen.feature.WeepingVinesFeature
//   - net.minecraft.world.level.levelgen.feature.TwistingVinesFeature
//   - net.minecraft.world.level.levelgen.feature.NetherForestVegetationFeature
// plus the survival gates in NetherRootsBlock, NetherSproutsBlock, NetherFungusBlock,
// and VegetationBlock. RNG draw order follows the bytecode order exactly.

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

func init() {
	registerFeatureBody("glowstone_blob", glowstoneBlobBody)
	registerFeatureBody("weeping_vines", weepingVinesBody)
	registerFeatureBody("twisting_vines", twistingVinesBody)
	registerFeatureBody("nether_forest_vegetation", netherForestVegetationBody)
}

var (
	glowstoneID          = block.ToStateID[block.Glowstone{}]
	netherWartBlockID    = block.ToStateID[block.NetherWartBlock{}]
	weepingVinesPlantID  = block.ToStateID[block.WeepingVinesPlant{}]
	twistingVinesPlantID = block.ToStateID[block.TwistingVinesPlant{}]
	netherrackID         = block.ToStateID[block.Netherrack{}]
	basaltID             = block.ToStateID[block.Basalt{}]
	blackstoneID         = block.ToStateID[block.Blackstone{}]
	warpedNyliumID       = block.ToStateID[block.WarpedNylium{}]
	crimsonNyliumID      = block.ToStateID[block.CrimsonNylium{}]
	warpedWartBlockID    = block.ToStateID[block.WarpedWartBlock{}]
	soulSoilID           = block.ToStateID[block.SoulSoil{}]
)

// ---- glowstone_blob (GlowstoneFeature) ----

func glowstoneBlobBody(
	bctx *bodyContext,
	_ *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	if !block.IsAir(bctx.getState(pos)) {
		return false
	}
	if !glowstoneCeilingBlock(bctx.getState(placement.BlockPos{X: pos.X, Y: pos.Y + 1, Z: pos.Z})) {
		return false
	}

	bctx.placeState(pos, glowstoneID)
	for i := 0; i < 1500; i++ {
		p := placement.BlockPos{
			X: pos.X + int(rng.NextIntN(8)) - int(rng.NextIntN(8)),
			Y: pos.Y - int(rng.NextIntN(12)),
			Z: pos.Z + int(rng.NextIntN(8)) - int(rng.NextIntN(8)),
		}
		if !block.IsAir(bctx.getState(p)) {
			continue
		}
		neighbors := 0
		for _, d := range directionValues {
			if bctx.getState(placement.BlockPos{X: p.X + d.dx, Y: p.Y + d.dy, Z: p.Z + d.dz}) == glowstoneID {
				neighbors++
				if neighbors > 1 {
					break
				}
			}
		}
		if neighbors == 1 {
			bctx.placeState(p, glowstoneID)
		}
	}
	return true
}

func glowstoneCeilingBlock(st block.StateID) bool {
	return st == netherrackID || st == basaltID || st == blackstoneID
}

// ---- weeping_vines (WeepingVinesFeature) ----

func weepingVinesBody(
	bctx *bodyContext,
	_ *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	if !block.IsAir(bctx.getState(pos)) {
		return false
	}
	if !weepingRoofBlock(bctx.getState(placement.BlockPos{X: pos.X, Y: pos.Y + 1, Z: pos.Z})) {
		return false
	}
	placeRoofNetherWart(bctx, rng, pos)
	placeRoofWeepingVines(bctx, rng, pos)
	return true
}

func placeRoofNetherWart(bctx *bodyContext, rng levelgen.RandomSource, pos placement.BlockPos) {
	bctx.placeState(pos, netherWartBlockID)
	for i := 0; i < 200; i++ {
		p := placement.BlockPos{
			X: pos.X + int(rng.NextIntN(6)) - int(rng.NextIntN(6)),
			Y: pos.Y + int(rng.NextIntN(2)) - int(rng.NextIntN(5)),
			Z: pos.Z + int(rng.NextIntN(6)) - int(rng.NextIntN(6)),
		}
		if !block.IsAir(bctx.getState(p)) {
			continue
		}
		neighbors := 0
		for _, d := range directionValues {
			if weepingRoofBlock(bctx.getState(placement.BlockPos{X: p.X + d.dx, Y: p.Y + d.dy, Z: p.Z + d.dz})) {
				neighbors++
				if neighbors > 1 {
					break
				}
			}
		}
		if neighbors == 1 {
			bctx.placeState(p, netherWartBlockID)
		}
	}
}

func placeRoofWeepingVines(bctx *bodyContext, rng levelgen.RandomSource, pos placement.BlockPos) {
	for i := 0; i < 100; i++ {
		p := placement.BlockPos{
			X: pos.X + int(rng.NextIntN(8)) - int(rng.NextIntN(8)),
			Y: pos.Y + int(rng.NextIntN(2)) - int(rng.NextIntN(7)),
			Z: pos.Z + int(rng.NextIntN(8)) - int(rng.NextIntN(8)),
		}
		if !block.IsAir(bctx.getState(p)) {
			continue
		}
		if !weepingRoofBlock(bctx.getState(placement.BlockPos{X: p.X, Y: p.Y + 1, Z: p.Z})) {
			continue
		}
		height := nextIntInclusive(rng, 1, 8)
		if rng.NextIntN(6) == 0 {
			height *= 2
		}
		if rng.NextIntN(5) == 0 {
			height = 1
		}
		placeWeepingVinesColumn(bctx, rng, p, height, 17, 25)
	}
}

func placeWeepingVinesColumn(bctx *bodyContext, rng levelgen.RandomSource, pos placement.BlockPos, height, minAge, maxAge int) {
	cur := pos
	for i := 0; i <= height; i++ {
		if block.IsAir(bctx.getState(cur)) {
			below := placement.BlockPos{X: cur.X, Y: cur.Y - 1, Z: cur.Z}
			if i == height || !block.IsAir(bctx.getState(below)) {
				bctx.placeState(cur, weepingVinesState(nextIntInclusive(rng, minAge, maxAge)))
				return
			}
			bctx.placeState(cur, weepingVinesPlantID)
		}
		cur.Y--
	}
}

func weepingRoofBlock(st block.StateID) bool {
	return st == netherrackID || st == netherWartBlockID
}

func weepingVinesState(age int) block.StateID {
	if st, ok := block.ToStateID[block.WeepingVines{Age: block.Integer(age)}]; ok {
		return st
	}
	return block.ToStateID[block.WeepingVines{}]
}

// ---- twisting_vines (TwistingVinesFeature) ----

type twistingVinesConfig struct {
	MaxHeight    int `json:"max_height"`
	SpreadHeight int `json:"spread_height"`
	SpreadWidth  int `json:"spread_width"`
}

var twistingVinesCache sync.Map // map[*feature.ConfiguredFeature]*twistingVinesConfig

func decodeTwistingVines(cf *feature.ConfiguredFeature) *twistingVinesConfig {
	if v, ok := twistingVinesCache.Load(cf); ok {
		return v.(*twistingVinesConfig)
	}
	cfg := &twistingVinesConfig{}
	if raw := configRaw(cf); len(raw) > 0 {
		if err := json.Unmarshal(raw, cfg); err != nil {
			panic(fmt.Errorf("world: twisting_vines config %q: %w", cf.ID, err))
		}
	}
	twistingVinesCache.Store(cf, cfg)
	return cfg
}

func twistingVinesBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	if twistingInvalidPlacement(bctx, pos) {
		return false
	}
	cfg := decodeTwistingVines(cf)
	for i := 0; i < cfg.SpreadWidth*cfg.SpreadWidth; i++ {
		p := placement.BlockPos{
			X: pos.X + nextIntInclusive(rng, -cfg.SpreadWidth, cfg.SpreadWidth),
			Y: pos.Y + nextIntInclusive(rng, -cfg.SpreadHeight, cfg.SpreadHeight),
			Z: pos.Z + nextIntInclusive(rng, -cfg.SpreadWidth, cfg.SpreadWidth),
		}
		if !findFirstAirBlockAboveGround(bctx, ctx, &p) {
			continue
		}
		if twistingInvalidPlacement(bctx, p) {
			continue
		}
		height := nextIntInclusive(rng, 1, cfg.MaxHeight)
		if rng.NextIntN(6) == 0 {
			height *= 2
		}
		if rng.NextIntN(5) == 0 {
			height = 1
		}
		placeTwistingVinesColumn(bctx, rng, p, height, 17, 25)
	}
	return true
}

func findFirstAirBlockAboveGround(bctx *bodyContext, ctx placement.PlacementContext, p *placement.BlockPos) bool {
	p.Y--
	for {
		if outsideBuildHeight(bctx, ctx, p.Y) {
			return false
		}
		if !block.IsAir(bctx.getState(*p)) {
			break
		}
		p.Y--
	}
	p.Y++
	return true
}

func placeTwistingVinesColumn(bctx *bodyContext, rng levelgen.RandomSource, pos placement.BlockPos, height, minAge, maxAge int) {
	cur := pos
	for i := 1; i <= height; i++ {
		if block.IsAir(bctx.getState(cur)) {
			above := placement.BlockPos{X: cur.X, Y: cur.Y + 1, Z: cur.Z}
			if i == height || !block.IsAir(bctx.getState(above)) {
				bctx.placeState(cur, twistingVinesState(nextIntInclusive(rng, minAge, maxAge)))
				return
			}
			bctx.placeState(cur, twistingVinesPlantID)
		}
		cur.Y++
	}
}

func twistingInvalidPlacement(bctx *bodyContext, pos placement.BlockPos) bool {
	if !block.IsAir(bctx.getState(pos)) {
		return true
	}
	below := bctx.getState(placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
	return below != netherrackID && below != warpedNyliumID && below != warpedWartBlockID
}

func twistingVinesState(age int) block.StateID {
	if st, ok := block.ToStateID[block.TwistingVines{Age: block.Integer(age)}]; ok {
		return st
	}
	return block.ToStateID[block.TwistingVines{}]
}

// ---- nether_forest_vegetation (NetherForestVegetationFeature) ----

type netherForestVegetationConfig struct {
	SpreadHeight  int             `json:"spread_height"`
	SpreadWidth   int             `json:"spread_width"`
	StateProvider json.RawMessage `json:"state_provider"`
}

type netherForestVegetationDecoded struct {
	cfg      netherForestVegetationConfig
	provider feature.BlockStateProvider
	err      error
}

var netherForestVegetationCache sync.Map // map[*feature.ConfiguredFeature]*netherForestVegetationDecoded

func decodeNetherForestVegetation(cf *feature.ConfiguredFeature) *netherForestVegetationDecoded {
	if v, ok := netherForestVegetationCache.Load(cf); ok {
		return v.(*netherForestVegetationDecoded)
	}
	d := &netherForestVegetationDecoded{}
	if raw := configRaw(cf); len(raw) > 0 {
		if err := json.Unmarshal(raw, &d.cfg); err != nil {
			d.err = fmt.Errorf("world: nether_forest_vegetation config %q: %w", cf.ID, err)
			netherForestVegetationCache.Store(cf, d)
			return d
		}
	}
	provider, err := feature.ParseProvider(d.cfg.StateProvider)
	if err != nil {
		d.err = fmt.Errorf("world: nether_forest_vegetation state_provider %q: %w", cf.ID, err)
		netherForestVegetationCache.Store(cf, d)
		return d
	}
	d.provider = provider
	netherForestVegetationCache.Store(cf, d)
	return d
}

func netherForestVegetationBody(
	bctx *bodyContext,
	cf *feature.ConfiguredFeature,
	ctx placement.PlacementContext,
	rng levelgen.RandomSource,
	pos placement.BlockPos,
) bool {
	d := decodeNetherForestVegetation(cf)
	if d.err != nil {
		panic(d.err)
	}
	below := bctx.getState(placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
	if !isNylium(below) {
		return false
	}
	if pos.Y < minBuildY(bctx, ctx)+1 || pos.Y+1 > maxBuildY(bctx, ctx) {
		return false
	}

	placed := 0
	cfg := d.cfg
	for i := 0; i < cfg.SpreadWidth*cfg.SpreadWidth; i++ {
		p := placement.BlockPos{
			X: pos.X + int(rng.NextIntN(int32(cfg.SpreadWidth))) - int(rng.NextIntN(int32(cfg.SpreadWidth))),
			Y: pos.Y + int(rng.NextIntN(int32(cfg.SpreadHeight))) - int(rng.NextIntN(int32(cfg.SpreadHeight))),
			Z: pos.Z + int(rng.NextIntN(int32(cfg.SpreadWidth))) - int(rng.NextIntN(int32(cfg.SpreadWidth))),
		}
		st := d.provider.GetState(rng, p.X, p.Y, p.Z)
		if !block.IsAir(bctx.getState(p)) || p.Y <= minBuildY(bctx, ctx) || !netherVegetationCanSurvive(bctx, st, p) {
			continue
		}
		bctx.placeState(p, st)
		placed++
	}
	return placed > 0
}

func netherVegetationCanSurvive(bctx *bodyContext, st block.StateID, pos placement.BlockPos) bool {
	below := bctx.getState(placement.BlockPos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	switch block.StateList[st].(type) {
	case block.CrimsonRoots, block.WarpedRoots, block.NetherSprouts:
		return supportsNetherRoots(below)
	case block.CrimsonFungus, block.WarpedFungus:
		return supportsNetherFungus(below)
	default:
		return supportsVegetation(below)
	}
}

func supportsNetherRoots(st block.StateID) bool {
	return supportsVegetation(st) || isNylium(st) || st == soulSoilID
}

func supportsNetherFungus(st block.StateID) bool {
	return supportsNetherRoots(st) || isBlockID(st, "minecraft:mycelium")
}

func isNylium(st block.StateID) bool {
	return st == crimsonNyliumID || st == warpedNyliumID
}

func isBlockID(st block.StateID, id string) bool {
	if int(st) < 0 || int(st) >= len(block.StateList) {
		return false
	}
	return block.StateList[st].ID() == id
}

// ---- small shared helpers ----

func nextIntInclusive(rng levelgen.RandomSource, minVal, maxVal int) int {
	return minVal + int(rng.NextIntN(int32(maxVal-minVal+1)))
}

func minBuildY(bctx *bodyContext, ctx placement.PlacementContext) int {
	if ctx != nil {
		return ctx.MinY()
	}
	return bctx.minY()
}

func maxBuildY(bctx *bodyContext, ctx placement.PlacementContext) int {
	if ctx != nil {
		return ctx.MinY() + ctx.Height() - 1
	}
	if bctx != nil && bctx.view != nil {
		return bctx.view.minY + bctx.view.height - 1
	}
	return 319
}

func outsideBuildHeight(bctx *bodyContext, ctx placement.PlacementContext, y int) bool {
	return y < minBuildY(bctx, ctx) || y > maxBuildY(bctx, ctx)
}
