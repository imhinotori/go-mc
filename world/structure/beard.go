package structure

import (
	"encoding/json"
	"math"
	"sync"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/world/levelgen/data"
)

// Beardifier ports net.minecraft.world.level.levelgen.Beardifier (26.2-inner.jar,
// javap -c -p). It is the structure terrain-adaptation density contribution: an ADDITIVE
// term threaded into final_density at FILL time so terrain rises to meet a floating
// structure (BEARD_THIN/BEARD_BOX) or digs out to bury it (BURY/ENCAPSULATE).
//
// Vanilla wires it via DensityFunctions$BeardifierMarker, which the NoiseChunk constructor
// replaces per-chunk with Beardifier.forStructuresInChunk(structureManager, chunkPos). In
// Sulfur the substitution is the additive term in noisechunk fill (see world/noisegen.go +
// world/levelgen/noisechunk/fill.go) — NOT a router-graph node and NOT inside the per-marker
// interpolator (the beard is non-interpolated, A5).
//
// SCOPE: only village (terrain_adaptation=beard_thin) and stronghold (bury) declare a
// non-NONE adaptation among the structures Sulfur generates; temples/igloo/mineshaft/
// swamp-hut have no terrain_adaptation key (codec default NONE) so they are NOT gathered and
// Compute returns 0 for them (byte-identical terrain — the NONE regression guard).

// terrainAdjustment is the ported net.minecraft.world.level.levelgen.structure.TerrainAdjustment
// enum (NONE/BURY/BEARD_THIN/BEARD_BOX/ENCAPSULATE). The ordinal order is preserved exactly so
// the Compute switch matches the jar tableswitch.
type terrainAdjustment int

const (
	adjNone       terrainAdjustment = iota // 0
	adjBury                                // 1
	adjBeardThin                           // 2
	adjBeardBox                            // 3
	adjEncapsulate                         // 4
)

// terrainAdaptationFor returns the structure's terrain_adaptation, read from the embedded
// worldgen/structure JSON (the same data the rest of the structure pipeline parses). Vanilla
// Structure.StructureSettings codec is optionalFieldOf("terrain_adaptation", NONE) — so an
// absent key (every Sulfur NONE structure) defaults to NONE. Memoized per id (pure over id).
//
// Source: javap Structure (optionalFieldOf "terrain_adaptation" default NONE) +
// the 26.2 datagen worldgen/structure/*.json terrain_adaptation values.
//
// CONCURRENCY: the beardifier runs inside the chunk-generation worker pool (ants) — many worker
// goroutines call ForStructuresInChunk -> hasTerrainAdaptation -> terrainAdaptationFor CONCURRENTLY
// for different chunks. A plain map read+write here is a `fatal error: concurrent map read and map
// write` (Go kills the process, no recover) — the observed crash that took the whole server down
// (every worker died, TPS collapsed). The value is a PURE function of id (memoization only), so a
// lock-free sync.Map is the right fit: obstruction-free reads, and a duplicate compute on a cache
// miss race is harmless (same value stored twice). CITE: same jar data, only the store is made safe.
var terrainAdaptationCache sync.Map // map[string]terrainAdjustment

func terrainAdaptationFor(id string) terrainAdjustment {
	if v, ok := terrainAdaptationCache.Load(id); ok {
		return v.(terrainAdjustment)
	}
	adj := adjNone
	if raw, err := data.StructureJSON(id); err == nil {
		var sj struct {
			TerrainAdaptation string `json:"terrain_adaptation"`
		}
		if json.Unmarshal(raw, &sj) == nil {
			switch sj.TerrainAdaptation {
			case "bury":
				adj = adjBury
			case "beard_thin":
				adj = adjBeardThin
			case "beard_box":
				adj = adjBeardBox
			case "encapsulate":
				adj = adjEncapsulate
			default: // "" (absent) or "none"
				adj = adjNone
			}
		}
	}
	terrainAdaptationCache.Store(id, adj)
	return adj
}

// hasTerrainAdaptation ports Beardifier.lambda$forStructuresInChunk$0:
// structure.terrainAdaptation() != NONE. Only such structures are gathered.
func hasTerrainAdaptation(id string) bool {
	return terrainAdaptationFor(id) != adjNone
}

// rigid ports Beardifier$Rigid(box, terrainAdjustment, groundLevelDelta): one structure
// piece's contribution geometry.
type rigid struct {
	box              BoundingBox
	adj              terrainAdjustment
	groundLevelDelta int
}

// Beardifier ports net.minecraft.world.level.levelgen.Beardifier: the per-chunk gathered
// rigid pieces + the inflated affected box. junctions (jigsaw JigsawJunction) are NOT
// modeled — Sulfur's village pieces do not yet carry junction lists, and a piece is gathered
// as a single rigid over its whole bbox (the same rigid path vanilla takes for a non-pool
// piece and for the RIGID projection). An empty Beardifier (affectedBox.IsEmpty) returns 0.
type Beardifier struct {
	pieces      []rigid
	affectedBox BoundingBox
	empty       bool
}

// EmptyBeardifier is the EMPTY sentinel (Beardifier.EMPTY): Compute always returns 0.
func EmptyBeardifier() *Beardifier { return &Beardifier{empty: true} }

// ForStructuresInChunk ports Beardifier.forStructuresInChunk(structureManager, chunkPos):
// gather every adapting (terrain_adaptation != NONE) start's pieces that are close to the
// chunk (within 12 blocks), record each as a Rigid, and inflate the union by 24 to form the
// affectedBox. Returns the EMPTY Beardifier when nothing adapts (Compute -> 0).
//
// Source: javap Beardifier.forStructuresInChunk. Junctions are skipped (Sulfur has no
// JigsawJunction list); each gathered piece becomes a Rigid over its bbox with
// groundLevelDelta from GroundLevelDelta() (0 for non-jigsaw pieces, matching the jar's
// non-pool path which records groundLevelDelta 0).
func ForStructuresInChunk(starts []*StructureStart, pos level.ChunkPos) *Beardifier {
	var pieces []rigid
	var hasUnion bool
	var union BoundingBox

	for _, st := range starts {
		if st == nil || !st.IsValid() {
			continue
		}
		if !hasTerrainAdaptation(st.Structure) {
			continue
		}
		adj := terrainAdaptationFor(st.Structure)
		for _, pc := range st.Pieces {
			bb := pc.BoundingBox()
			if !isCloseToChunk(bb, pos, 12) {
				continue
			}
			pieces = append(pieces, rigid{
				box:              bb,
				adj:              adj,
				groundLevelDelta: groundLevelDeltaOf(pc),
			})
			if !hasUnion {
				union = bb
				hasUnion = true
			} else {
				union = union.Encapsulate(bb)
			}
		}
	}

	if !hasUnion {
		return EmptyBeardifier()
	}
	return &Beardifier{
		pieces:      pieces,
		affectedBox: inflatedBy(union, 24),
	}
}

// groundLevelDeltaOf returns a piece's ground-level delta. Vanilla reads
// PoolElementStructurePiece.getGroundLevelDelta() for RIGID pool pieces (jigsaw/village) and
// records 0 for every other piece (the non-pool branch in forStructuresInChunk). Sulfur's
// pieces do not yet carry a ground-level delta (the jigsaw placer projects every village
// piece to the surface, so the effective delta is 0); a piece may opt in via the optional
// GroundLevelDelta() method. Cite: Beardifier.forStructuresInChunk Rigid(..., 0) /
// PoolElementStructurePiece.getGroundLevelDelta. Stronghold pieces (the BURY path) are
// non-pool -> 0, matching the jar exactly.
func groundLevelDeltaOf(p Piece) int {
	if g, ok := p.(interface{ GroundLevelDelta() int }); ok {
		return g.GroundLevelDelta()
	}
	return 0
}

// Compute ports Beardifier.compute(FunctionContext): the additive density at (wx,wy,wz).
// 0 outside affectedBox (the no-double-apply gate). Otherwise sum each rigid piece's
// contribution per its TerrainAdjustment.
//
// Source: javap Beardifier.compute. The junctions loop is omitted (no junctions modeled).
func (b *Beardifier) Compute(wx, wy, wz int) float64 {
	if b == nil || b.empty || b.affectedBox.IsEmpty() {
		return 0
	}
	if !b.affectedBox.IsInside(wx, wy, wz) {
		return 0
	}

	d := 0.0
	for _, pc := range b.pieces {
		box := pc.box
		groundLevelDelta := pc.groundLevelDelta

		// dx/dz: distance OUTSIDE the box along X/Z (0 when inside), via
		// max(0, max(minX-wx, wx-maxX)) — javap Beardifier.compute. (var 11, 12)
		dx := max(0, max(box.MinX-wx, wx-box.MaxX))
		dz := max(0, max(box.MinZ-wz, wz-box.MaxZ))

		// i = box.minY() + groundLevelDelta (var 13); n = wy - i (var 14).
		i := box.MinY + groundLevelDelta
		n := wy - i

		// dy (var 15) per the TerrainAdjustment (javap tableswitch 1..5):
		//   BURY        -> 0
		//   BEARD_THIN  -> n
		//   BEARD_BOX   -> max(0, max(i-wy, wy-box.maxY))
		//   ENCAPSULATE -> max(0, max(box.minY-wy, wy-box.maxY))
		var dy int
		switch pc.adj {
		case adjBury:
			dy = 0
		case adjBeardThin:
			dy = n
		case adjBeardBox:
			dy = max(0, max(i-wy, wy-box.MaxY))
		case adjEncapsulate:
			dy = max(0, max(box.MinY-wy, wy-box.MaxY))
		}

		// The magnitude switch (javap tableswitch 1..5):
		//   BURY        -> getBuryContribution(dx, dy/2, dz)
		//   BEARD_THIN/BEARD_BOX -> getBeardContribution(dx, dy, dz, n) * 0.8
		//   ENCAPSULATE -> getBuryContribution(dx/2, dy/2, dz/2) * 0.8
		switch pc.adj {
		case adjBury:
			d += getBuryContribution(float64(dx), float64(dy)/2.0, float64(dz))
		case adjBeardThin, adjBeardBox:
			d += getBeardContribution(dx, dy, dz, n) * 0.8
		case adjEncapsulate:
			d += getBuryContribution(float64(dx)/2.0, float64(dy)/2.0, float64(dz)/2.0) * 0.8
		}
	}
	return d
}

// getBuryContribution ports Beardifier.getBuryContribution(double, double, double):
// Mth.clampedMap(Mth.length(dx,dy,dz), 0, 6, 1, 0). 1 at the center, falling to 0 at
// distance 6 (clamped). Cite: javap Beardifier.getBuryContribution + Mth.clampedMap/length.
func getBuryContribution(dx, dy, dz float64) float64 {
	return mthClampedMap(mthLength3(dx, dy, dz), 0, 6, 1, 0)
}

// getBeardContribution ports Beardifier.getBeardContribution(int, int, int, int): the kernel
// lookup. Adds 12 to each of dx/dy/dz (the kernel is centered at 12 in a 24-wide span); if
// any falls outside [0,24) returns 0; else magnitude * BEARD_KERNEL[ dz' *24*24 + dx' *24 + dy' ].
//
// magnitude = -(n+0.5) / sqrt(lengthSquared(dx, n+0.5, dz) / 2) / 2, using Mth.fastInvSqrt.
// Cite: javap Beardifier.getBeardContribution + Mth.fastInvSqrt/lengthSquared.
func getBeardContribution(dx, dy, dz, n int) float64 {
	kx := dx + 12 // var 4
	ky := dy + 12 // var 5
	kz := dz + 12 // var 6
	if !isInKernelRange(kx) || !isInKernelRange(ky) || !isInKernelRange(kz) {
		return 0
	}
	d := float64(n) + 0.5 // var 7
	e := mthLengthSquared3(float64(dx), d, float64(dz))
	// magnitude = -d * fastInvSqrt(e/2.0) / 2.0
	magnitude := -d * mthFastInvSqrt(e/2.0) / 2.0
	// BEARD_KERNEL index: kz*24*24 + kx*24 + ky (javap: var6*24*24 + var4*24 + var5).
	return magnitude * float64(beardKernel[kz*24*24+kx*24+ky])
}

// isInKernelRange ports Beardifier.isInKernelRange(int): 0 <= v < 24.
func isInKernelRange(v int) bool { return v >= 0 && v < 24 }

// inflatedBy ports BoundingBox.inflatedBy(int): grow every face by n.
// Cite: javap BoundingBox.inflatedBy.
func inflatedBy(b BoundingBox, n int) BoundingBox {
	return BoundingBox{
		MinX: b.MinX - n, MinY: b.MinY - n, MinZ: b.MinZ - n,
		MaxX: b.MaxX + n, MaxY: b.MaxY + n, MaxZ: b.MaxZ + n,
	}
}

// isCloseToChunk ports StructurePiece.isCloseToChunk(chunkPos, distance): the piece bbox is
// within `distance` blocks of the chunk's 16x16 column (an inflated-column intersect).
// Cite: javap StructurePiece.isCloseToChunk -> BoundingBox.intersects of the inflated bbox
// with the chunk column. Equivalent to bbox.IntersectsXZ(minX-d, minZ-d, maxX+d, maxZ+d).
func isCloseToChunk(bb BoundingBox, pos level.ChunkPos, distance int) bool {
	minBlockX := int(pos[0]) * 16
	minBlockZ := int(pos[1]) * 16
	maxBlockX := minBlockX + 15
	maxBlockZ := minBlockZ + 15
	return bb.MaxX >= minBlockX-distance && bb.MinX <= maxBlockX+distance &&
		bb.MaxZ >= minBlockZ-distance && bb.MinZ <= maxBlockZ+distance
}

// --- Mth ports (the exact float ops the beard math draws) ---

// mthLength3 ports Mth.length(double,double,double) = sqrt(lengthSquared3).
func mthLength3(x, y, z float64) float64 { return math.Sqrt(mthLengthSquared3(x, y, z)) }

// mthLengthSquared3 ports Mth.lengthSquared(double,double,double) = x*x + y*y + z*z.
func mthLengthSquared3(x, y, z float64) float64 { return x*x + y*y + z*z }

// mthClampedMap ports Mth.clampedMap(d,d2,d3,d4,d5) = clampedLerp(inverseLerp(d,d2,d3), d4, d5).
func mthClampedMap(d, d2, d3, d4, d5 float64) float64 {
	return mthClampedLerp(mthInverseLerp(d, d2, d3), d4, d5)
}

// mthInverseLerp ports Mth.inverseLerp(d,d2,d3) = (d - d2) / (d3 - d2).
func mthInverseLerp(d, d2, d3 float64) float64 { return (d - d2) / (d3 - d2) }

// mthClampedLerp ports Mth.clampedLerp(d,d2,d3): <0 -> d2, >1 -> d3, else lerp(d,d2,d3).
func mthClampedLerp(d, d2, d3 float64) float64 {
	if d < 0 {
		return d2
	}
	if d > 1 {
		return d3
	}
	return mthLerp(d, d2, d3)
}

// mthLerp ports Mth.lerp(d,d2,d3) = d2 + d*(d3 - d2).
func mthLerp(d, d2, d3 float64) float64 { return d2 + d*(d3-d2) }

// mthFastInvSqrt ports Mth.fastInvSqrt(double): the Quake fast inverse-sqrt with one Newton
// step. Bit-exact to the jar (doubleToRawLongBits / 0x5fe6ec85e7de30da magic / one refine).
// Cite: javap Mth.fastInvSqrt (long 6910469410427058090 = 0x5FE6EC85E7DE30DA).
func mthFastInvSqrt(d float64) float64 {
	e := 0.5 * d
	bits := math.Float64bits(d)
	bits = 0x5FE6EC85E7DE30DA - (bits >> 1)
	d = math.Float64frombits(bits)
	d = d * (1.5 - e*d*d)
	return d
}
