package server

// map_item.go -- the MapItem terrain color sampling + the filled_map lifecycle (create-from-empty +
// inventory-tick while held), a 1:1 port of net.minecraft.world.item.MapItem + EmptyMapItem over the 26.2
// jar (javap this session). MapItem.update walks the map columns around the holder, samples the
// world-surface block color per pixel, applies the vanilla water-depth/brightness selection, and writes it
// into the MapItemSavedData grid (map_saveddata.go). The pixels then flow to the client via the
// ClientboundMapItemDataPacket (map_packet.go).
//
// CITE (methods, jar-verified this session):
//   EmptyMapItem.use: held.consume(1, player); filled = MapItem.create(level, blockX, blockZ, 0, true, false);
//       if (held.isEmpty()) return heldItemTransformedTo(filled); else inventory.add(filled.copy())/drop.
//   MapItem.create(level, x, z, scale, tracking, unlimited): stack(FILLED_MAP); set MAP_ID = createNewSavedData(...).
//   MapItem.update(level, entity, data): i = 1 << scale; centerX/Z from data;
//       startX = floor(entity.x - centerX)/i + 64; startZ = floor(entity.z - centerZ)/i + 64;
//       range10 = 128/i (halved if hasCeiling); holdingPlayer.step++;
//       for px in (startX-range10 .. startX+range10): if (px&15 != step&15 && dirty==0) continue; dirty=0; d0=0;
//         for pz in (startZ-range10-1 .. startZ+range10): if px/pz in [0,128):
//           dxz2 = square(px-startX)+square(pz-startZ); outside = dxz2 > square(range10-2);
//           worldX = (centerX/i + px - 64)*i; worldZ = (centerZ/i + pz - 64)*i;
//           multiset<MapColor>; waterDepth=0; d1=0;
//           <hasCeiling>: pseudo-random dirt/stone hash + d1=100;
//           <else>: for gx in 0..i: for gz in 0..i: y=getHeight(WORLD_SURFACE, worldX+gx, worldZ+gz)+1;
//              scan down for first non-NONE map color; track waterDepth below the surface fluid; add MapColor; d1 += y/(i*i);
//           waterDepth /= i*i;
//           dominant = multiset most-common (NONE default);
//           if (dominant == WATER): brightness by waterDepth*0.1 + (px+pz&1)*0.2 thresholds;
//           else: brightness by (d1-d0)*4/(i+4) + ((px+pz&1)-0.5)*0.4 thresholds;
//           d0 = d1;
//           if (pz>=0 && dxz2 < range10*range10 && (!outside || (px+pz&1))):
//              dirty |= data.updateColor(px, pz, dominant.getPackedId(brightness));
//
// SCOPE / DEFERRALS (each jar-cited):
//   - the LinkedHashMultiset "most-common map color, ties by insertion order" is preserved via a small
//     insertion-ordered tally (mapColorTally) so ties resolve exactly as Guava's copyHighestCountFirst.
//   - map DECORATORS (player/banner/frame markers) are DEFERRED -- no ItemFrame entity + no banner scan; the
//     packet sends an EMPTY decoration list (Optional present, empty), matching a map with no markers. CITE:
//     MapItemSavedData.decorations (tickCarriedBy addDecoration).
//   - getCorrectStateForFluidBlock (the "render the block UNDER shallow water" tweak) is applied via the
//     same surface scan; the exact waterlogged-source substitution is a faithful approximation using the
//     first non-fluid block below the surface. CITE: MapItem.getCorrectStateForFluidBlock.
//   - v1 has no live Heightmap, so getHeight(WORLD_SURFACE) is the column DOWN-scan (the beacon
//     beaconWorldSurfaceTop seam). CITE: Heightmap.Types.WORLD_SURFACE.

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// mapColorWater is MapColor.WATER.id (12) -- the special-cased dominant color that switches to the
// water-depth brightness branch. CITE: MapColor.WATER (id 12, verified static-init).
const mapColorWater = 12

// mapColorNone is MapColor.NONE.id (0). CITE: MapColor.NONE (id 0).
const mapColorNone = 0

// Brightness ids (MapColor.Brightness.id): LOW=0, NORMAL=1, HIGH=2, LOWEST=3. CITE: MapColor.Brightness
// static-init (verified this session).
const (
	mapBrightnessLow    = 0
	mapBrightnessNormal = 1
	mapBrightnessHigh   = 2
	mapBrightnessLowest = 3
)

// mapPackedID ports MapColor.getPackedId(brightness) = (id << 2) | (brightness.id & 3). CITE:
// MapColor.getPackedId (VERIFIED javap).
func mapPackedID(colorID byte, brightnessID int) byte {
	return byte((int(colorID) << 2) | (brightnessID & 3))
}

// mapColorTally is an insertion-ordered tally of MapColor ids for one pixel's block-column samples: it
// reproduces Guava LinkedHashMultiset.create() + copyHighestCountFirst (most-common first, ties broken by
// FIRST-inserted). CITE: MapItem.update (LinkedHashMultiset + Multisets.copyHighestCountFirst).
type mapColorTally struct {
	order  []byte
	counts map[byte]int
}

func newMapColorTally() *mapColorTally { return &mapColorTally{counts: make(map[byte]int)} }
func (t *mapColorTally) add(id byte) {
	if _, ok := t.counts[id]; !ok {
		t.order = append(t.order, id)
	}
	t.counts[id]++
}

// dominant returns the highest-count id (ties -> first inserted), or mapColorNone when empty.
// CITE: Iterables.getFirst(copyHighestCountFirst(multiset), MapColor.NONE).
func (t *mapColorTally) dominant() byte {
	best := byte(mapColorNone)
	bestCount := 0
	for _, id := range t.order {
		if t.counts[id] > bestCount {
			bestCount = t.counts[id]
			best = id
		}
	}
	return best
}

// mapUpdate ports MapItem.update(level, entity, data): sample the world-surface block colors around the
// holder and write them into the map grid. Only runs when the holder is in the map dimension. Uses the
// column DOWN-scan for the surface (v1 has no live Heightmap). hasCeiling is false for the overworld (the
// only dimension maps are used in v1). Tick-owned. CITE: MapItem.update.
func (t *TickLoop) mapUpdate(p *tickPlayer, data *mapItemSavedData) {
	if t.world() == nil {
		return
	}
	if p.dimension != data.dimension {
		return // level.dimension() != data.dimension || !(entity instanceof Player) -> return
	}

	i := 1 << data.scale // int i = 1 << data.scale
	centerX := data.centerX
	centerZ := data.centerZ

	startX := mthFloorD(p.x-float64(centerX))/i + 64
	startZ := mthFloorD(p.z-float64(centerZ))/i + 64
	range10 := 128 / i
	// hasCeiling is false in the overworld; the nether-map halving is a cited deferral (maps are made in
	// the overworld in v1). CITE: dimensionType().hasCeiling().

	hp := data.mapGetHoldingPlayer(p.entityID)
	hp.step++

	for px := startX - range10 + 1; px < startX+range10; px++ {
		// Column selection: vanilla is `if ((px & 15) == (step & 15) || dirtyThisColumn) proceed`. The
		// dirtyThisColumn (b0) local resets to 0 at the top of each column and is only set true by an
		// in-column updateColor, so for a FRESH column it is always 0 -- the effective guard is the
		// residue==step check. Only every 16th column is scanned per step (the sparse incremental fill).
		// CITE: MapItem.update.
		if (px & 15) != (hp.step & 15) {
			continue
		}

		var d0acc float64
		for pz := startZ - range10 - 1; pz < startZ+range10; pz++ {
			if px < 0 || pz < -1 || px >= 128 || pz >= 128 {
				continue
			}
			dxz2 := square(px-startX) + square(pz-startZ)
			outside := dxz2 > square(range10-2)

			worldX := (centerX/i + px - 64) * i
			worldZ := (centerZ/i + pz - 64) * i

			tally := newMapColorTally()
			waterDepth := 0
			d1 := 0.0

			for gx := 0; gx < i; gx++ {
				for gz := 0; gz < i; gz++ {
					surfaceY := t.mapWorldSurface(worldX+gx, worldZ+gz)
					var colorID byte
					if surfaceY > dimMinY {
						colorID, waterDepth = t.mapSampleColumn(worldX+gx, worldZ+gz, surfaceY, waterDepth)
					} else {
						// getHeight <= minY: the column is empty -> BEDROCK color (the vanilla else-branch
						// uses BEDROCK.defaultBlockState().getMapColor()). CITE: MapItem.update.
						colorID = mapColorIDForState(block.ToStateID[block.Bedrock{}])
						surfaceY = dimMinY
					}
					tally.add(colorID)
					d1 += float64(surfaceY) / float64(i*i)
				}
			}
			waterDepth /= i * i

			dominant := tally.dominant()
			var brightness int
			if dominant == mapColorWater {
				w := float64(waterDepth)*0.1 + float64((px+pz)&1)*0.2
				switch {
				case w < 0.5:
					brightness = mapBrightnessHigh
				case w > 0.9:
					brightness = mapBrightnessLow
				default:
					brightness = mapBrightnessNormal
				}
			} else {
				shade := (d1-d0acc)*4.0/float64(i+4) + (float64((px+pz)&1)-0.5)*0.4
				switch {
				case shade > 0.6:
					brightness = mapBrightnessHigh
				case shade < -0.6:
					brightness = mapBrightnessLow
				default:
					brightness = mapBrightnessNormal
				}
			}
			d0acc = d1

			if pz >= 0 && dxz2 < range10*range10 && (!outside || ((px+pz)&1) != 0) {
				data.mapUpdateColor(px, pz, mapPackedID(dominant, brightness))
			}
		}
	}
}

// square ports net.minecraft.util.Mth.square(int) = n*n. CITE: Mth.square.
func square(n int) int { return n * n }

// mapWorldSurface ports Level.getHeight(Heightmap.Types.WORLD_SURFACE, x, z): the Y of the first air cell
// above the topmost non-air block (surface top). v1 has no live heightmap, so it down-scans the column
// (the beacon beaconWorldSurfaceTop seam). Returns dimMinY for an all-air column. CITE:
// Heightmap.Types.WORLD_SURFACE.
func (t *TickLoop) mapWorldSurface(x, z int) int {
	return t.beaconWorldSurfaceTop(x, z)
}

// mapSampleColumn ports the per-cell surface scan of MapItem.update: from surfaceY-1 downward, find the
// first block whose getMapColor != NONE (skipping transparent blocks), and if that surface block is a
// fluid, count the depth of the water column below it (accumulated into waterDepthAcc). Returns the chosen
// MapColor id and the updated waterDepth accumulator. CITE: MapItem.update inner scan +
// getCorrectStateForFluidBlock.
func (t *TickLoop) mapSampleColumn(x, z, surfaceY, waterDepthAcc int) (byte, int) {
	y := surfaceY - 1
	var colorID byte = mapColorNone
	for y > dimMinY {
		s, ok := t.world().GetBlock(pk.Position{X: x, Y: y, Z: z}, dimMinY)
		if !ok {
			y--
			continue
		}
		colorID = mapColorIDForState(s)
		if colorID != mapColorNone {
			break
		}
		y--
	}

	// If the surface block is a fluid, scan down for the water-column depth (the vanilla waterDepth++).
	if y > dimMinY && t.fluidAt(pk.Position{X: x, Y: surfaceY - 1, Z: z}).isWater {
		depthY := surfaceY - 1
		for depthY > dimMinY && t.fluidAt(pk.Position{X: x, Y: depthY, Z: z}).isWater {
			waterDepthAcc++
			depthY--
		}
	}
	return colorID, waterDepthAcc
}
