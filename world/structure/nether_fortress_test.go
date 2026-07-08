package structure

import (
	"testing"

	"github.com/imhinotori/sulfur/level"
	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
)

type netherFortressSurfaceSampler struct{}

func (netherFortressSurfaceSampler) SampleSurfaceY(int, int) int { return 64 }

func netherBiome(t *testing.T, id string) BiomeAt {
	t.Helper()
	var bt levelbiome.Type
	if err := bt.UnmarshalText([]byte(id)); err != nil {
		t.Fatalf("bad biome id %q: %v", id, err)
	}
	return func(int, int, int) levelbiome.Type { return bt }
}

func findFortressChunk(t *testing.T, seed int64, g *netherFortressStartGen) (passCX, passCZ, failCX, failCZ int) {
	t.Helper()
	pass, fail := false, false
	for cx := 0; cx < 300 && !(pass && fail); cx++ {
		for cz := 0; cz < 300 && !(pass && fail); cz++ {
			place := g.placement.IsStructureChunk(seed, cx, cz)
			if place && !pass {
				pick := levelgen.NewWorldgenRandom(0)
				pick.SetLargeFeatureSeed(seed, cx, cz)
				if int(pick.NextIntN(5)) < 2 {
					passCX, passCZ, pass = cx, cz, true
				}
			} else if !place && !fail {
				failCX, failCZ, fail = cx, cz, true
			}
		}
	}
	if !pass {
		t.Fatalf("no fortress-placing chunk found in 300x300 at seed %d", seed)
	}
	if !fail {
		t.Fatalf("no placement-failing chunk found")
	}
	return
}

func TestNetherFortressPlacement(t *testing.T) {
	const seed = int64(0x50FA)
	g, err := NewNetherFortressStartGen()
	if err != nil {
		t.Fatalf("NewNetherFortressStartGen: %v", err)
	}
	fg := g.(*netherFortressStartGen)

	if fg.placement.Salt != 30084232 || fg.placement.Spacing != 27 || fg.placement.Separation != 4 {
		t.Fatalf("placement mismatch: salt=%d spacing=%d separation=%d", fg.placement.Salt, fg.placement.Spacing, fg.placement.Separation)
	}
	for _, want := range []string{"minecraft:nether_wastes", "minecraft:crimson_forest", "minecraft:basalt_deltas"} {
		if !fg.biomeAllow[want] {
			t.Fatalf("biome allow-set missing %q", want)
		}
	}

	passCX, passCZ, failCX, failCZ := findFortressChunk(t, seed, fg)
	sampler := netherFortressSurfaceSampler{}
	biome := netherBiome(t, "minecraft:nether_wastes")

	starts := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, sampler, biome)
	if len(starts) != 1 {
		t.Fatalf("expected 1 fortress start at (%d,%d), got %d", passCX, passCZ, len(starts))
	}
	ss := starts[0]
	if ss.Structure != "minecraft:fortress" {
		t.Fatalf("start structure = %q", ss.Structure)
	}
	if len(ss.Pieces) == 0 {
		t.Fatalf("fortress start has no pieces")
	}
	if ss.BBox.MinY < netherFortressMagicStartY-8 || ss.BBox.MinY > netherFortressMagicStartY+8 {
		t.Fatalf("fortress bbox minY=%d not near MAGIC_START_Y=%d", ss.BBox.MinY, netherFortressMagicStartY)
	}

	none := g.GenerateStarts(seed, level.ChunkPos{int32(failCX), int32(failCZ)}, sampler, biome)
	if len(none) != 0 {
		t.Fatalf("placement-failing chunk produced %d starts", len(none))
	}

	again := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, sampler, biome)
	if len(again) != 1 || len(again[0].Pieces) != len(ss.Pieces) || again[0].BBox != ss.BBox {
		t.Fatalf("non-deterministic fortress start")
	}
}

func TestNetherFortressBiomeGate(t *testing.T) {
	const seed = int64(0x50FA)
	g, err := NewNetherFortressStartGen()
	if err != nil {
		t.Fatalf("NewNetherFortressStartGen: %v", err)
	}
	fg := g.(*netherFortressStartGen)
	passCX, passCZ, _, _ := findFortressChunk(t, seed, fg)
	starts := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, netherFortressSurfaceSampler{}, netherBiome(t, "minecraft:plains"))
	if len(starts) != 0 {
		t.Fatalf("non-nether biome produced %d fortress starts", len(starts))
	}
}

type fortressCaptureView struct {
	blocks   map[[3]int]block.StateID
	spawners map[[3]int]string
}

func newFortressCaptureView() *fortressCaptureView {
	return &fortressCaptureView{blocks: map[[3]int]block.StateID{}, spawners: map[[3]int]string{}}
}

func (v *fortressCaptureView) SetBlock(x, y, z int, st block.StateID) { v.blocks[[3]int{x, y, z}] = st }
func (v *fortressCaptureView) GetBlock(x, y, z int) block.StateID {
	if st, ok := v.blocks[[3]int{x, y, z}]; ok {
		return st
	}
	return stateAir
}
func (v *fortressCaptureView) SetBlockEntity(int, int, int, block.EntityType, string, int64) {}
func (v *fortressCaptureView) SetSpawner(x, y, z int, id string) {
	v.spawners[[3]int{x, y, z}] = id
}
func (v *fortressCaptureView) RecordSpawn(SpawnRequest) {}

func TestNetherFortressPieceAssembly(t *testing.T) {
	// seed 0 / chunk (15,2) is a placing chunk whose RNG walk assembles a MonsterThrone (the
	// blaze-spawner piece) -- derived by scanning the placement+walk, so this test proves the
	// blaze spawner arises through the ACTUAL weighted piece walk, not just direct construction.
	const seed = int64(0)
	const passCX, passCZ = 15, 2
	g, err := NewNetherFortressStartGen()
	if err != nil {
		t.Fatalf("NewNetherFortressStartGen: %v", err)
	}
	biome := netherBiome(t, "minecraft:nether_wastes")

	starts := g.GenerateStarts(seed, level.ChunkPos{int32(passCX), int32(passCZ)}, netherFortressSurfaceSampler{}, biome)
	if len(starts) != 1 {
		t.Fatalf("expected 1 start, got %d", len(starts))
	}
	ss := starts[0]

	root, ok := ss.Pieces[0].(*netherPiece)
	if !ok || (root.kind != netherKindStart && root.kind != netherKindBridgeCrossing) {
		t.Fatalf("root piece is not a BridgeCrossing/StartPiece: %T", ss.Pieces[0])
	}

	wide := BoundingBox{MinX: -1 << 20, MinY: -1 << 20, MinZ: -1 << 20, MaxX: 1 << 20, MaxY: 1 << 20, MaxZ: 1 << 20}
	view := newFortressCaptureView()
	rng := levelgen.NewWorldgenRandom(0)
	rng.SetLargeFeatureSeed(seed, passCX, passCZ)
	throneFound, crossingFound := false, false
	for _, pc := range ss.Pieces {
		np := pc.(*netherPiece)
		if np.kind == netherKindMonsterThrone {
			throneFound = true
		}
		if np.kind == netherKindStart || np.kind == netherKindBridgeCrossing {
			crossingFound = true
		}
		np.PostProcess(view, wide, level.ChunkPos{int32(passCX), int32(passCZ)}, rng)
	}

	if !crossingFound {
		t.Fatalf("no BridgeCrossing in the assembled fortress")
	}

	nbCount := 0
	for _, st := range view.blocks {
		if st == netherBrickState {
			nbCount++
		}
	}
	if nbCount == 0 {
		t.Fatalf("fortress placed no nether_bricks")
	}

	if !throneFound {
		t.Fatalf("expected a MonsterThrone (blaze spawner) in the seed-0/(15,2) assembly")
	}
	blaze := 0
	for _, id := range view.spawners {
		if id == "minecraft:blaze" {
			blaze++
		}
	}
	if blaze == 0 {
		t.Fatalf("MonsterThrone present but no blaze spawner recorded")
	}
	t.Logf("fortress: %d pieces, nether_bricks=%d, throne=%v, blaze_spawners=%d", len(ss.Pieces), nbCount, throneFound, blaze)
}

func TestNetherFortressBlazeSpawnerOffset(t *testing.T) {
	b := &netherFortressBuilder{}
	throne := createMonsterThrone(b, 100, 64, 200, 3, block.South)
	if throne == nil {
		t.Fatal("createMonsterThrone returned nil (isOkBox/collision)")
	}
	wide := BoundingBox{MinX: -1 << 20, MinY: -1 << 20, MinZ: -1 << 20, MaxX: 1 << 20, MaxY: 1 << 20, MaxZ: 1 << 20}
	view := newFortressCaptureView()
	throne.PostProcess(view, wide, level.ChunkPos{6, 12}, levelgen.NewWorldgenRandom(0))

	if len(view.spawners) != 1 {
		t.Fatalf("expected exactly 1 spawner, got %d", len(view.spawners))
	}
	wx := throne.worldX(3, 5)
	wy := throne.worldY(5)
	wz := throne.worldZ(3, 5)
	id, ok := view.spawners[[3]int{wx, wy, wz}]
	if !ok {
		t.Fatalf("no spawner at expected world pos (%d,%d,%d)", wx, wy, wz)
	}
	if id != "minecraft:blaze" {
		t.Fatalf("spawner entity = %q, want minecraft:blaze", id)
	}
	if view.blocks[[3]int{wx, wy, wz}] != spawnerStateID {
		t.Fatalf("no spawner block at (%d,%d,%d)", wx, wy, wz)
	}
	nb := 0
	for _, st := range view.blocks {
		if st == netherBrickState {
			nb++
		}
	}
	if nb == 0 {
		t.Fatal("MonsterThrone placed no nether_bricks")
	}
}

func TestNetherFortressBridgeStraightRail(t *testing.T) {
	b := &netherFortressBuilder{}
	br := createBridgeStraight(b, 0, 64, 0, block.North, 1)
	if br == nil {
		t.Fatal("createBridgeStraight returned nil")
	}
	wide := BoundingBox{MinX: -1 << 20, MinY: -1 << 20, MinZ: -1 << 20, MaxX: 1 << 20, MaxY: 1 << 20, MaxZ: 1 << 20}
	view := newFortressCaptureView()
	br.PostProcess(view, wide, level.ChunkPos{0, 0}, levelgen.NewWorldgenRandom(0))

	nb, fence := 0, 0
	for _, st := range view.blocks {
		switch {
		case st == netherBrickState:
			nb++
		case st == netherBrickFenceNS || st == netherBrickFenceEW:
			fence++
		}
	}
	if nb == 0 {
		t.Fatal("BridgeStraight placed no nether_bricks")
	}
	if fence == 0 {
		t.Fatal("BridgeStraight placed no nether_brick_fence rail")
	}
}
