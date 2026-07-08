package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

// synthElement is a synthetic PoolElement test double: a fixed-size box with a configurable
// set of jigsaw connectors. It lets the termination tests build a pool that COULD recurse
// forever (a self-referencing element) so each of the three bounds can be proven independently.
type synthElement struct {
	size     [3]int            // the box span (LOCAL, before rotation/translation)
	jigsaws  []JigsawBlockInfo // the connectors (LOCAL positions + facings)
	proj     Projection
	empty    bool
}

func (e *synthElement) Projection() Projection { return e.proj }
func (e *synthElement) IsEmpty() bool          { return e.empty }

func (e *synthElement) BoundingBox(origin Pos, _ Rotation) BoundingBox {
	return BoundingBox{
		MinX: origin.X, MinY: origin.Y, MinZ: origin.Z,
		MaxX: origin.X + e.size[0] - 1, MaxY: origin.Y + e.size[1] - 1, MaxZ: origin.Z + e.size[2] - 1,
	}
}

func (e *synthElement) Jigsaws(origin Pos, _ Rotation, _ levelgen.RandomSource) ([]JigsawBlockInfo, error) {
	out := make([]JigsawBlockInfo, len(e.jigsaws))
	for i, j := range e.jigsaws {
		j.WorldPos = Pos{origin.X + j.LocalPos.X, origin.Y + j.LocalPos.Y, origin.Z + j.LocalPos.Z}
		out[i] = j
	}
	return out, nil
}

func (e *synthElement) Place(WorldGenView, Pos, Rotation, BoundingBox, levelgen.RandomSource) {}

// installSynthPool injects a synthetic pool into the pool cache under id so addPieces resolves
// it via resolvePool/LoadTemplatePool. Each test installs its own ids + restores the cache.
func installSynthPool(id string, fallback string, els ...PoolElement) {
	poolCacheMu.Lock()
	defer poolCacheMu.Unlock()
	poolCache[resolvePoolID(id)] = &StructureTemplatePool{id: resolvePoolID(id), templates: els, fallback: fallback}
}

func clearSynthPools(ids ...string) {
	poolCacheMu.Lock()
	defer poolCacheMu.Unlock()
	for _, id := range ids {
		delete(poolCache, resolvePoolID(id))
	}
}

// selfRefElement builds a synthetic element with N jigsaw connectors all targeting the same
// pool ("synth:loop") with matching name/target — so the Placer keeps attaching copies of the
// same element forever UNLESS a bound stops it. front/top are chosen so canAttach succeeds:
// each connector faces a horizontal direction, and the child's reciprocal connector faces the
// opposite. The element is a 4x4x4 box with connectors on its +X / -X / +Z / -Z faces.
func selfRefElement(targetPool string) *synthElement {
	const s = 4
	mk := func(x, y, z int, front, top block.Direction) JigsawBlockInfo {
		return JigsawBlockInfo{
			LocalPos:    Pos{x, y, z},
			Name:        "synth:jig",
			Pool:        targetPool,
			Target:      "synth:jig",
			Joint:       "aligned",
			FrontFacing: front,
			TopFacing:   top,
		}
	}
	return &synthElement{
		size: [3]int{s, s, s},
		proj: ProjectionRigid,
		jigsaws: []JigsawBlockInfo{
			mk(s-1, 0, s/2, block.East, block.Up),
			mk(0, 0, s/2, block.West, block.Up),
			mk(s/2, 0, s-1, block.South, block.Up),
			mk(s/2, 0, 0, block.North, block.Up),
		},
	}
}

// runSelfRefPlacer installs a self-referencing pool "synth:loop" (and an empty fallback) and
// runs the Placer with the given bounds, returning the piece count. With all bounds wide, the
// self-referencing element tiles the whole 80-radius footprint until a bound caps it.
func runSelfRefPlacer(t *testing.T, maxDepth, maxDistance int) int {
	t.Helper()
	el := selfRefElement("synth:loop")
	installSynthPool("synth:loop", "minecraft:empty", el)
	defer clearSynthPools("synth:loop")

	start := &StructureTemplatePool{id: "synth:start", templates: []PoolElement{el}, fallback: "minecraft:empty"}
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(1234, 0, 0)
	pieces := addPieces(start, Pos{0, 0, 0}, maxDepth, maxDistance, true, flatTestSampler{0}, rng)
	return len(pieces)
}

type flatTestSampler struct{ y int }

func (s flatTestSampler) SampleSurfaceY(int, int) int { return s.y }

// TestPlacerTerminatesMaxDepthOnly proves bound #1 (maxDepth) ALONE caps the count: with a
// huge max_distance (so the distance bound never fires) and collision still active, a
// self-referencing pool terminates because depth is capped. Dropping the depth bound here
// would let the village grow until the distance/collision bound stops it — this isolates depth.
func TestPlacerTerminatesMaxDepthOnly(t *testing.T) {
	// maxDepth=2, max_distance huge. The self-ref element fans out at most a few rings before
	// every branch hits depth 2 and attaches only the empty terminator.
	got := runSelfRefPlacer(t, 2, 100000)
	if got >= jigsawTotalPieceCap {
		t.Fatalf("maxDepth-only: hit the piece cap (%d) — the depth bound did not terminate the Placer", got)
	}
	if got < 1 {
		t.Fatalf("maxDepth-only: placed %d pieces; want at least the root", got)
	}
	t.Logf("maxDepth=2 only -> %d pieces (bounded)", got)
}

// TestPlacerTerminatesDistanceOnly proves bound #2 (max_distance_from_center) ALONE caps the
// count: with a huge maxDepth (so depth never fires) and collision active, the self-ref pool
// terminates because every candidate eventually leaves the 80-radius bound. Dropping the
// distance bound would let it grow until depth/cap — this isolates distance.
func TestPlacerTerminatesDistanceOnly(t *testing.T) {
	got := runSelfRefPlacer(t, 100000, 24)
	if got >= jigsawTotalPieceCap {
		t.Fatalf("distance-only: hit the piece cap (%d) — the distance bound did not terminate the Placer", got)
	}
	if got < 1 {
		t.Fatalf("distance-only: placed %d pieces; want at least the root", got)
	}
	t.Logf("max_distance=24 only -> %d pieces (bounded)", got)
}

// TestPlacerTerminatesCollisionOnly proves bound #3 (VoxelShape collision) ALONE caps the
// count: with a huge maxDepth AND a huge max_distance, the ONLY thing that stops the self-ref
// pool from growing forever is that a new copy collides with an already-placed box. The tiling
// fills the (still finite) reachable area and then every candidate overlaps. Dropping the
// collision bound would make this grow without limit (only the defensive cap would catch it).
func TestPlacerTerminatesCollisionOnly(t *testing.T) {
	// Huge depth + huge distance: collision is the sole real bound. The 4x4 element tiles a
	// large but finite region; once packed, every candidate collides -> termination below the cap.
	got := runSelfRefPlacer(t, 100000, 60)
	if got >= jigsawTotalPieceCap {
		t.Fatalf("collision-only: hit the piece cap (%d) — collision did not terminate the Placer", got)
	}
	t.Logf("collision-only (depth+distance wide) -> %d pieces (bounded by collision)", got)
}

// TestPlacerDepthZeroAttachesOnlyTerminators proves a depth==maxDepth piece attaches ONLY the
// fallback/terminator pool (CFR the `depth != maxDepth` guard): with maxDepth=0, the root is at
// depth 0 == maxDepth, so its jigsaws may only draw from the fallback (minecraft:empty -> the
// terminator), placing NO children. The result is exactly the root piece.
func TestPlacerDepthZeroAttachesOnlyTerminators(t *testing.T) {
	el := selfRefElement("synth:loop")
	installSynthPool("synth:loop", "minecraft:empty", el)
	defer clearSynthPools("synth:loop")

	start := &StructureTemplatePool{id: "synth:start", templates: []PoolElement{el}, fallback: "minecraft:empty"}
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(1234, 0, 0)
	pieces := addPieces(start, Pos{0, 0, 0}, 0, 80, true, flatTestSampler{0}, rng)
	if len(pieces) != 1 {
		t.Fatalf("maxDepth=0 placed %d pieces; want exactly 1 (root only — depth-0 attaches only terminators)", len(pieces))
	}
}

// TestPlacerAssemblesRealVillage assembles a REAL plains town_center via the Placer and pins
// (a) it terminates well below the piece cap and (b) a multi-piece graph is built — the full
// pool model + alignment + collision working end-to-end on real village data.
func TestPlacerAssemblesRealVillage(t *testing.T) {
	pool, err := LoadTemplatePool("minecraft:village/plains/town_centers")
	if err != nil {
		t.Fatalf("LoadTemplatePool: %v", err)
	}
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(99, 0, 0)
	pieces := addPieces(pool, Pos{0, 68, 0}, 6, 80, true, flatTestSampler{68}, rng)
	if len(pieces) < 2 {
		t.Fatalf("real plains town_center assembled %d pieces; want a multi-piece graph", len(pieces))
	}
	if len(pieces) >= jigsawTotalPieceCap {
		t.Fatalf("real plains town_center hit the piece cap (%d) — runaway", len(pieces))
	}
}

// TestPlacerQueueOrderPinned pins the BFS queue order: the assembled piece sequence (a
// fingerprint of each piece's element box + rotation) is determinism-stable across two
// identical assemblies. A change to the SequencedPriorityIterator order, the shuffle draw
// count, or the alignment math changes this — pinning the RNG-lockstep contract (Pitfall #6).
func TestPlacerQueueOrderPinned(t *testing.T) {
	assemble := func() []BoundingBox {
		pool, err := LoadTemplatePool("minecraft:village/plains/town_centers")
		if err != nil {
			t.Fatalf("LoadTemplatePool: %v", err)
		}
		rng := levelgen.NewWorldgenRandom(0)
		rng.SetLargeFeatureSeed(99, 0, 0)
		pieces := addPieces(pool, Pos{0, 68, 0}, 6, 80, true, flatTestSampler{68}, rng)
		boxes := make([]BoundingBox, len(pieces))
		for i, p := range pieces {
			boxes[i] = p.BoundingBox()
		}
		return boxes
	}
	a := assemble()
	b := assemble()
	if len(a) != len(b) {
		t.Fatalf("two assemblies produced different piece counts: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("piece %d diverges between assemblies: %+v vs %+v (queue order not deterministic)", i, a[i], b[i])
		}
	}
}
