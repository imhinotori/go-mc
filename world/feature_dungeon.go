package world

// feature_dungeon.go ports MonsterRoomFeature — the simple cobblestone mob-spawner
// "dungeon" — as the "monster_room" featureBody (the type is in the recognized roster,
// world/levelgen/feature/parse.go, but was a no-op until now). Research v2-structures
// Tier 0 (HIGH confidence): the dungeon is a Feature, NOT a Structure — it extends
// net.minecraft.world.level.levelgen.feature.Feature and places via its placed_feature's
// count/height_range/in_square/biome modifiers in the UNDERGROUND_DECORATION step,
// exactly like an ore. Registering this body makes the EXISTING applyBiomeDecoration
// place dungeons at the jar frequency — no pipeline change.
//
// PORTED from net.minecraft.world.level.levelgen.feature.MonsterRoomFeature.place
// (javap -c against temp/cache/26.2-inner.jar). The geometry is HARDCODED in the method
// (the configured_feature config is empty {} — JAR-VERIFIED monster_room.json). The
// EXACT rng draw order — the two nextInt(2) half-extent draws, then the per-wall-cell
// nextInt(4) mossy-cobblestone speckle in the build loop, then the two chest attempts'
// (nextInt(2*xr+1), nextInt(2*zr+1)) position draws, then the spawner mob roll — IS the
// determinism contract (T-13-09). A reorder corrupts both the room layout and the
// post-place rng state.
//
// Block draws (javap -c, MonsterRoomFeature):
//   - validity scan: count open sides (j==0 wall cells with air at pos & pos.above);
//     require a solid floor (j==-1) + solid ceiling (j==4); reject if openCount<1 or >5
//     (return false, NO partial room — T-13-10). NO rng draws in the scan.
//   - build loop (i in [xrn..xrp], j FROM 3 DOWN TO -1, k in [zrn..zrp]): interior cells
//     cleared to CAVE_AIR; wall/floor/ceiling cells get cobblestone, or — on the j==-1
//     FLOOR ring only — mossy_cobblestone when nextInt(4)==0 (the per-floor-cell speckle).
//   - chests: up to 2 attempts, each up to 3 tries: a random in-room position
//     (nextInt(2*xr+1)-xr, Y, nextInt(2*zr+1)-zr); placed iff the spot is empty AND has
//     EXACTLY ONE solid horizontal neighbor (so it sits flush against a wall); on success
//     the chest is reoriented to face away from the wall (StructurePiece.reorient — pure
//     geometry, NO draw) and a SIMPLE_DUNGEON loot-table ref is recorded.
//   - spawner: placed at the room CENTER (origin), then the spawner block entity's mob id
//     is rolled (randomEntityId: SKELETON/ZOMBIE/ZOMBIE/SPIDER via Util.getRandom).
//
// LOOT + MOB DEFERRED to v3 (documented, NOT silently dropped — matches the structure
// chest deferrals in research v2-structures "Deferrals"): the chest places as the CHEST
// BLOCK (reoriented) and the spawner as the SPAWNER BLOCK; the loot-table resolution
// (SIMPLE_DUNGEON) and the spawner mob roll (randomEntityId) are the block-entity / loot
// subsystem, a separate v3 effort. The VISIBLE deliverable — a cobblestone room with a
// mossy-speckled floor, a center spawner block, and 1-2 wall chests — is fully placed.
// CRITICAL determinism note: the deferred mob roll is the LAST draw in vanilla place().
// To keep the post-place rng state jar-identical (so the global decoration sequence does
// not desync after a dungeon places), the body STILL CONSUMES that final draw via a
// faithful randomEntityId replay (one Util.getRandom -> nextInt(4)); only the resulting
// EntityType is discarded. Likewise the loot-table call (setBlockEntityLootTable) takes
// ZERO rng draws in vanilla, so its omission consumes nothing — no replay needed.
//
// All writes go ONLY through bctx.placeState (Neighborhood.SetBlock — cross-chunk +
// live-heightmap), so a dungeon near a chunk edge spills its cobble shell into the +x/+z
// neighbor (T-13-10 cross-chunk). All reads go through bctx.getState (air outside 3x3).

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

func init() { registerFeatureBody("monster_room", monsterRoomBody) }

// ---- resolved block states (cached once) ----

// dungeonStates holds the StateIDs MonsterRoomFeature places, resolved once. CAVE_AIR is
// the interior fill (vanilla MonsterRoomFeature.AIR = Blocks.CAVE_AIR.defaultBlockState()).
var (
	dungeonCobble       = block.ToStateID[block.Cobblestone{}]
	dungeonMossyCobble  = block.ToStateID[block.MossyCobblestone{}]
	dungeonCaveAir      = block.ToStateID[block.CaveAir{}]
	dungeonSpawner      = block.ToStateID[block.Spawner{}]
	dungeonChestDefault = block.ToStateID[block.Chest{Facing: block.North, Type: block.ChestTypeSingle, Waterlogged: false}]
)

// featuresCannotReplace is the #minecraft:features_cannot_replace block tag
// (data/minecraft/tags/block/features_cannot_replace.json, JAR-VERIFIED 26.2): a feature
// may NOT overwrite these. safeSetBlock (Feature.safeSetBlock / isReplaceable) only writes
// when the EXISTING block is NOT in this set. Resolved constant-for-constant from the tag
// def (the ore-replaceables precedent). The body consults it before every cobble/air/chest
// /spawner write so a dungeon never clobbers a pre-existing chest/spawner/bedrock.
var featuresCannotReplace = newBlockIDSet(
	"minecraft:bedrock",
	"minecraft:spawner",
	"minecraft:chest",
	"minecraft:end_portal_frame",
	"minecraft:reinforced_deepslate",
	"minecraft:trial_spawner",
	"minecraft:vault",
)

// newBlockIDSet resolves a set of block ids to the set of all their state ids (the
// is(block)/is(tag) check is property-agnostic — every state of each member matches).
func newBlockIDSet(ids ...string) map[block.StateID]bool {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	set := map[block.StateID]bool{}
	for sid, b := range block.StateList {
		if b != nil && want[b.ID()] {
			set[block.StateID(sid)] = true
		}
	}
	return set
}

// ---- MonsterRoomFeature.place ----

// monsterRoomBody ports MonsterRoomFeature.place (javap -c, 26.2-inner.jar). The config
// is empty (NoneFeatureConfiguration) — the room geometry is hardcoded here. Returns
// false (no blocks placed) when the candidate cavern is rejected, true once the room is
// built (matching the bytecode: every reject path returns 0/false BEFORE any write, the
// success path returns 1/true after placing).
func monsterRoomBody(
	bctx *bodyContext,
	_ *feature.ConfiguredFeature,
	_ placement.PlacementContext,
	rng levelgen.RandomSource,
	origin placement.BlockPos,
) bool {
	// xr = nextInt(2) + 2; the room half-extent in X (so the inner room is 2*xr+1 wide,
	// xr in {2,3}). xrn/xrp are the shell bounds (one past the inner room each side).
	xr := int(rng.NextIntN(2)) + 2
	xrn := -xr - 1
	xrp := xr + 1
	// yrn/yrp are FIXED: floor at j=-1, ceiling at j=4 (the room is 4 tall + floor).
	const yrn = -1
	const yrp = 4
	// zr = nextInt(2) + 2; the room half-extent in Z. (The Z draw happens AFTER the X
	// draw — the determinism contract for the extents.)
	zr := int(rng.NextIntN(2)) + 2
	zrn := -zr - 1
	zrp := zr + 1

	// ---- validity scan (no rng draws) ----
	// Loop i in [xrn..xrp], j in [-1..4], k in [zrn..zrp]; count "open" sides + require a
	// solid floor and ceiling. A wall (i/k on the boundary) at j==0 with air at the cell
	// AND the cell above counts as an open side (the room must connect to a cavern).
	openCount := 0
	for i := xrn; i <= xrp; i++ {
		for j := yrn; j <= yrp; j++ {
			for k := zrn; k <= zrp; k++ {
				p := placement.BlockPos{X: origin.X + i, Y: origin.Y + j, Z: origin.Z + k}
				solid := dungeonIsSolid(bctx.getState(p))
				// Floor (j==-1) must be solid; else reject (no floor to build on).
				if j == yrn && !solid {
					return false
				}
				// Ceiling (j==4) must be solid; else reject (open to the sky/void above).
				if j == yrp && !solid {
					return false
				}
				// An open side: a wall cell (on the X or Z boundary) at j==0 whose cell AND
				// the cell above are both empty (air) — i.e. the room can see into a cavern.
				if (i == xrn || i == xrp || k == zrn || k == zrp) && j == 0 &&
					block.IsAir(bctx.getState(p)) &&
					block.IsAir(bctx.getState(placement.BlockPos{X: p.X, Y: p.Y + 1, Z: p.Z})) {
					openCount++
				}
			}
		}
	}
	// Require between 1 and 5 open sides (a cavern adjacency that is neither fully sealed
	// nor fully open). Reject otherwise — return false, NO partial room (T-13-10).
	if openCount < 1 || openCount > 5 {
		return false
	}

	// ---- build loop: carve interior + lay the cobblestone/mossy shell ----
	// j iterates FROM 3 DOWN TO -1 (the jar's `for (j=3; j>=-1; j--)`), so the interior is
	// cleared top-down before the floor is laid. i/k iterate ascending. (Mirrors the
	// bytecode loops at i: [xrn..xrp], j: 3..-1, k: [zrn..zrp].)
	for i := xrn; i <= xrp; i++ {
		for j := yrp - 1; j >= yrn; j-- { // 3 down to -1
			for k := zrn; k <= zrp; k++ {
				p := placement.BlockPos{X: origin.X + i, Y: origin.Y + j, Z: origin.Z + k}
				cur := bctx.getState(p)
				// The jar's INTERIOR test (bytecode 318-355): a cell is INTERIOR iff
				// i!=xrn && j!=-1 && k!=zrn && i!=xrp && j!=4 && k!=zrp. j==4 (ceiling) is
				// never reached here (the loop tops out at j==3), so the test reduces to:
				// interior = i not on the X boundary, k not on the Z boundary, j != -1.
				interior := i != xrn && i != xrp && k != zrn && k != zrp && j != yrn
				if interior {
					// INTERIOR clear (bytecode 480-511): clear to CAVE_AIR UNLESS the cell is
					// already a chest or spawner (a previously-placed dungeon block is kept).
					// NOTE the jar uses safeSetBlock here too — chest/spawner are also in
					// #features_cannot_replace, so safeSet already guards them; the explicit
					// is(CHEST)/is(SPAWNER) checks are redundant with safeSet but transcribed
					// for fidelity.
					if !dungeonIsChest(cur) && !dungeonIsSpawner(cur) {
						bctx.safeSet(p, dungeonCaveAir)
					}
					continue
				}

				// SHELL cell (wall / floor / ceiling-row). Bytecode 358-477:
				//   if pos.Y >= minY && !below.isSolid(): RAW setBlock(AIR) — open the void
				//     below an exposed shell cell (NOT safeSetBlock; a direct write). NO
				//     cobble draw on this branch.
				//   else if cur.isSolid() && !cur.is(CHEST):
				//     on the FLOOR ring (j==-1): draw nextInt(4); ==0 -> COBBLESTONE, else
				//       (3/4) -> MOSSY_COBBLESTONE (the floor speckle).
				//     otherwise (walls/ceiling-row): COBBLESTONE.
				below := bctx.getState(placement.BlockPos{X: p.X, Y: p.Y - 1, Z: p.Z})
				if p.Y >= bctx.minY() && !dungeonIsSolid(below) {
					// RAW setBlock(AIR) — vanilla writes AIR directly (no replaceable gate)
					// to open the void beneath an exposed shell cell. No rng draw.
					bctx.placeState(p, dungeonCaveAir)
					continue
				}
				if dungeonIsSolid(cur) && !dungeonIsChest(cur) {
					if j == yrn {
						// Floor speckle: nextInt(4)==0 -> cobblestone; else mossy_cobblestone.
						if rng.NextIntN(4) == 0 {
							bctx.safeSet(p, dungeonCobble)
						} else {
							bctx.safeSet(p, dungeonMossyCobble)
						}
					} else {
						bctx.safeSet(p, dungeonCobble)
					}
				}
			}
		}
	}

	// ---- chests: up to 2, each up to 3 tries (the chest draw block) ----
	for chest := 0; chest < 2; chest++ {
		for try := 0; try < 3; try++ {
			// A random position inside the room: x = origin.X + nextInt(2*xr+1) - xr;
			// z = origin.Z + nextInt(2*zr+1) - zr; y = origin.Y (the floor level).
			cx := origin.X + int(rng.NextIntN(int32(2*xr+1))) - xr
			cy := origin.Y
			cz := origin.Z + int(rng.NextIntN(int32(2*zr+1))) - zr
			p := placement.BlockPos{X: cx, Y: cy, Z: cz}
			if !block.IsAir(bctx.getState(p)) {
				continue
			}
			// Count solid horizontal neighbors; a chest is placed only when EXACTLY ONE
			// (so it sits flush against a single wall, opening into the room).
			solidNeighbors := 0
			for _, d := range horizontalDirections {
				np := placement.BlockPos{X: p.X + d.dx, Y: p.Y, Z: p.Z + d.dz}
				if dungeonIsSolid(bctx.getState(np)) {
					solidNeighbors++
				}
			}
			if solidNeighbors != 1 {
				continue
			}
			// Reorient the chest to face away from the wall (pure geometry, NO rng draw)
			// then place it; record the SIMPLE_DUNGEON loot ref (loot DEFERRED v3 — the
			// setBlockEntityLootTable call takes zero rng draws, so omitting it consumes
			// nothing). Break to the next chest once placed.
			st := bctx.dungeonReorientChest(p)
			bctx.safeSet(p, st)
			// LOOT DEFERRED (v3): vanilla calls RandomizableContainer.setBlockEntityLootTable(
			//   level, rng, p, BuiltInLootTables.SIMPLE_DUNGEON) here. That call consumes NO
			//   rng draws (it only stamps the block-entity's loot-table id), so the deferral
			//   does not perturb the determinism contract. The chest is placed as a block.
			break
		}
	}

	// ---- spawner at the room center ----
	bctx.safeSet(origin, dungeonSpawner)
	// MOB ROLL DEFERRED (v3): vanilla fetches the SpawnerBlockEntity and calls
	//   setEntityId(randomEntityId(rng), rng). randomEntityId = Util.getRandom(MOBS, rng)
	//   where MOBS = [SKELETON, ZOMBIE, ZOMBIE, SPIDER]. Util.getRandom draws ONE nextInt(4).
	//   We have no block-entity subsystem yet, so the spawner is placed as an inert block —
	//   BUT this final draw is the LAST rng consumption in vanilla place(); to keep the
	//   post-place rng state jar-identical (so the global decoration sequence does not
	//   desync after a dungeon), we STILL CONSUME it and discard the result.
	_ = dungeonRandomMobRoll(rng)

	return true
}

// dungeonRandomMobRoll replays MonsterRoomFeature.randomEntityId's single rng draw
// (Util.getRandom(MOBS, rng) -> MOBS[nextInt(4)]) so the post-place rng state matches
// vanilla even though the chosen mob is discarded (v3 block-entity deferral). It returns
// the index for documentation/testing; the caller discards it.
func dungeonRandomMobRoll(rng levelgen.RandomSource) int {
	// Util.getRandom(array, rng) = array[rng.nextInt(array.length)]; MOBS.length == 4.
	return int(rng.NextIntN(4))
}

// dungeonHorizontalFacing maps a horizontalDirections index (the vanilla
// [NORTH,EAST,SOUTH,WEST] order) to its block.Direction, so the chest reorient can set the
// chest's FACING property from the index of the (single) adjacent wall.
var dungeonHorizontalFacing = [4]block.Direction{
	block.North, // index 0
	block.East,  // index 1
	block.South, // index 2
	block.West,  // index 3
}

// dungeonOppositeFacingIdx returns the horizontalDirections index opposite to idx
// (NORTH<->SOUTH at 0/2, EAST<->WEST at 1/3): (idx+2)%4.
func dungeonOppositeFacingIdx(idx int) int { return (idx + 2) % 4 }

// dungeonReorientChest ports StructurePiece.reorient for a chest at p: scan the 4
// horizontal neighbors; if EXACTLY ONE is solid-render (and none is a chest) the chest
// faces the OPPOSITE of that wall (opening into the room); otherwise the default facing
// is kept. It takes NO rng draws (pure geometry over the live view).
func (b *bodyContext) dungeonReorientChest(p placement.BlockPos) block.StateID {
	wallIdx := -1
	for i := range horizontalDirections {
		d := horizontalDirections[i]
		np := placement.BlockPos{X: p.X + d.dx, Y: p.Y, Z: p.Z + d.dz}
		st := b.getState(np)
		if dungeonIsChest(st) {
			// A neighboring chest aborts reorientation (keep the default) — jar: returns the
			// passed state unchanged.
			return dungeonChestDefault
		}
		if dungeonIsSolidRender(st) {
			if wallIdx == -1 {
				wallIdx = i
			} else {
				// More than one solid-render wall — ambiguous, keep default (jar nulls the
				// candidate on the second solid neighbor and breaks).
				wallIdx = -1
				break
			}
		}
	}
	if wallIdx == -1 {
		return dungeonChestDefault
	}
	// Face the OPPOSITE of the (single) wall so the chest opens into the room.
	return dungeonChestFacing(dungeonHorizontalFacing[dungeonOppositeFacingIdx(wallIdx)])
}

// dungeonChestFacing resolves a chest with the given horizontal facing direction.
func dungeonChestFacing(d block.Direction) block.StateID {
	return block.ToStateID[block.Chest{Facing: d, Type: block.ChestTypeSingle, Waterlogged: false}]
}

// safeSet ports Feature.safeSetBlock: write `st` at p ONLY when the EXISTING block is
// replaceable (NOT in #features_cannot_replace) — so a dungeon never clobbers a
// pre-existing chest/spawner/bedrock. The write goes through bctx.placeState
// (Neighborhood.SetBlock — cross-chunk + heightmap-live).
func (b *bodyContext) safeSet(p placement.BlockPos, st block.StateID) {
	if featuresCannotReplace[b.getState(p)] {
		return
	}
	b.placeState(p, st)
}

// ---- conservative solidity reads (mid-worldgen) ----

// dungeonIsSolid is the conservative BlockState.isSolid() read the misc/patch bodies use
// (faceSturdyUp precedent): a block is "solid" if it is neither air (incl. cave_air) nor
// water. The full BlockBehaviour collision shape is unavailable mid-worldgen, so this
// never reports a solid over air/water (a dungeon never floats or roofs over a void). It
// is the same conservative gate the validity scan + carve loop + chest-wall test consult.
func dungeonIsSolid(st block.StateID) bool {
	if block.IsAir(st) {
		return false
	}
	if st == dungeonWaterState() {
		return false
	}
	return true
}

// dungeonIsSolidRender mirrors isSolidRender for the chest reorient: same conservative
// gate as dungeonIsSolid (a full opaque cube). The reorient only needs "is there a wall
// here" — air/water are not walls.
func dungeonIsSolidRender(st block.StateID) bool { return dungeonIsSolid(st) }

// dungeonIsChest reports whether st is any chest state.
func dungeonIsChest(st block.StateID) bool {
	b := block.StateList[st]
	return b != nil && b.ID() == "minecraft:chest"
}

// dungeonIsSpawner reports whether st is any spawner state.
func dungeonIsSpawner(st block.StateID) bool {
	b := block.StateList[st]
	return b != nil && b.ID() == "minecraft:spawner"
}

// dungeonWaterStateID caches the default water state id for the conservative solidity read.
var dungeonWaterStateID = block.ToStateID[block.Water{Level: 0}]

func dungeonWaterState() block.StateID { return dungeonWaterStateID }
