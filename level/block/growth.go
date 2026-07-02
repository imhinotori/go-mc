package block

// growth.go — state predicates + property accessors for the three RANDOM-TICK GROWTH families
// ported 1:1 from the unobfuscated 26.2 jar:
//
//   - SAPLING (SaplingBlock: STAGE 0/1, treeGrower.growTree)
//   - LEAVES  (LeavesBlock/DecayingLeaves: DISTANCE 1..7, PERSISTENT, the DISTANCE==7 && !persistent
//     decay gate + the 6-neighbour min+1 distance recompute)
//   - GRASS / MYCELIUM spread (SpreadingSnowyBlock: die-to-dirt / spread-to-adjacent-dirt)
//
// They back the sapling/leaf/grass random-tick handlers in the server package (growth_block.go),
// mirroring the shape of crop.go (which backs crop_block.go).

// ---- SAPLING (net.minecraft.world.level.block.SaplingBlock) ----
//
// SaplingBlock carries an IntegerProperty STAGE (BlockStateProperties.STAGE == 0..1). randomTick
// advances STAGE 0 -> 1 the first time, then treeGrower.growTree the second. CITE: SaplingBlock
// (STAGE field, advanceTree).

// IsSapling reports whether a state id is a base SaplingBlock (the eight overworld saplings;
// MangrovePropagule is a MangrovePropaguleBlock override and is NOT a base sapling — its randomTick
// differs, so it is deliberately excluded here, matching IsVegetation's SaplingBlock membership).
// CITE: SaplingBlock subclasses (oak/spruce/birch/jungle/acacia/cherry/dark_oak/pale_oak).
func IsSapling(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	case OakSapling, SpruceSapling, BirchSapling, JungleSapling,
		AcaciaSapling, CherrySapling, DarkOakSapling, PaleOakSapling:
		return true
	default:
		return false
	}
}

// IsOakSapling reports whether a state id is specifically an oak sapling — the ONE grower wired to a
// real tree feature in v1 (OakTreeGrower -> the "oak" configured_feature). The other saplings are a
// cited grower-registry follow-up (their STAGE advance is still ported; only the grow step is
// deferred). CITE: SaplingBlock treeGrower (Blocks.OAK_SAPLING -> TreeGrower.OAK).
func IsOakSapling(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(OakSapling)
	return ok
}

// SaplingStage returns the STAGE property (0 or 1) of a sapling state, or -1 when not a base
// sapling. CITE: SaplingBlock.advanceTree (state.getValue(STAGE)).
func SaplingStage(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	switch b := StateList[s].(type) {
	case OakSapling:
		return int(b.Stage)
	case SpruceSapling:
		return int(b.Stage)
	case BirchSapling:
		return int(b.Stage)
	case JungleSapling:
		return int(b.Stage)
	case AcaciaSapling:
		return int(b.Stage)
	case CherrySapling:
		return int(b.Stage)
	case DarkOakSapling:
		return int(b.Stage)
	case PaleOakSapling:
		return int(b.Stage)
	default:
		return -1
	}
}

// SaplingCycleStage returns the sapling state id with STAGE cycled (advanceTree's
// state.cycle(STAGE)). STAGE is a 0..1 IntegerProperty, so cycle is 0 -> 1 -> 0 (wraps). advanceTree
// only ever calls this when STAGE==0, so the observed transition is 0 -> 1. ok=false if not a sapling.
// CITE: SaplingBlock.advanceTree (state.cycle(STAGE)); BlockState.cycle (IntegerProperty wrap).
func SaplingCycleStage(s StateID) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return 0, false
	}
	// STAGE has exactly two values {0,1}; cycle(0)=1, cycle(1)=0 (the IntegerProperty wrap-around).
	switch b := StateList[s].(type) {
	case OakSapling:
		sid, ok := ToStateID[OakSapling{Stage: cycleStage(b.Stage)}]
		return sid, ok
	case SpruceSapling:
		sid, ok := ToStateID[SpruceSapling{Stage: cycleStage(b.Stage)}]
		return sid, ok
	case BirchSapling:
		sid, ok := ToStateID[BirchSapling{Stage: cycleStage(b.Stage)}]
		return sid, ok
	case JungleSapling:
		sid, ok := ToStateID[JungleSapling{Stage: cycleStage(b.Stage)}]
		return sid, ok
	case AcaciaSapling:
		sid, ok := ToStateID[AcaciaSapling{Stage: cycleStage(b.Stage)}]
		return sid, ok
	case CherrySapling:
		sid, ok := ToStateID[CherrySapling{Stage: cycleStage(b.Stage)}]
		return sid, ok
	case DarkOakSapling:
		sid, ok := ToStateID[DarkOakSapling{Stage: cycleStage(b.Stage)}]
		return sid, ok
	case PaleOakSapling:
		sid, ok := ToStateID[PaleOakSapling{Stage: cycleStage(b.Stage)}]
		return sid, ok
	default:
		return 0, false
	}
}

// cycleStage cycles the two-valued STAGE property (0 -> 1 -> 0), the IntegerProperty.cycle behaviour
// SaplingBlock.advanceTree relies on. CITE: BlockState.cycle over BlockStateProperties.STAGE (0..1).
func cycleStage(v Integer) Integer {
	if v == 0 {
		return 1
	}
	return 0
}

// ---- LEAVES (net.minecraft.world.level.block.LeavesBlock / DecayingLeaves) ----
//
// LeavesBlock carries DISTANCE (1..7), PERSISTENT, WATERLOGGED. isRandomlyTicking == DISTANCE==7 &&
// !PERSISTENT. randomTick decays (drop + remove) when decaying(state) == !PERSISTENT && DISTANCE==7.
// The scheduled tick recomputes DISTANCE = min over the 6 neighbours of getDistanceAt(neighbour)+1
// (starting at 7). getDistanceAt: a log (#PREVENTS_NEARBY_LEAF_DECAY == #logs) -> 0; a leaf -> its
// DISTANCE; anything else -> 7. CITE: LeavesBlock.{isRandomlyTicking,randomTick,decaying,
// updateDistance,getDistanceAt,getOptionalDistanceAt}.

// IsLeaves reports whether a state id is any DecayingLeaves LeavesBlock (the eight tree leaves +
// azalea/flowering azalea). Mangrove leaves are also a LeavesBlock but its DISTANCE is anchored by a
// different mechanic; all ten leaves carry DISTANCE/PERSISTENT so the base decay logic applies to
// each — they are enumerated explicitly (matching the generated struct set). CITE: LeavesBlock.
func IsLeaves(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	case OakLeaves, SpruceLeaves, BirchLeaves, JungleLeaves, AcaciaLeaves,
		CherryLeaves, DarkOakLeaves, PaleOakLeaves, MangroveLeaves,
		AzaleaLeaves, FloweringAzaleaLeaves:
		return true
	default:
		return false
	}
}

// LeavesDistance returns the DISTANCE property (1..7) of a leaves state, or -1 when not leaves.
// CITE: LeavesBlock.DISTANCE (BlockStateProperties.DISTANCE, IntegerProperty 1..7).
func LeavesDistance(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	switch b := StateList[s].(type) {
	case OakLeaves:
		return int(b.Distance)
	case SpruceLeaves:
		return int(b.Distance)
	case BirchLeaves:
		return int(b.Distance)
	case JungleLeaves:
		return int(b.Distance)
	case AcaciaLeaves:
		return int(b.Distance)
	case CherryLeaves:
		return int(b.Distance)
	case DarkOakLeaves:
		return int(b.Distance)
	case PaleOakLeaves:
		return int(b.Distance)
	case MangroveLeaves:
		return int(b.Distance)
	case AzaleaLeaves:
		return int(b.Distance)
	case FloweringAzaleaLeaves:
		return int(b.Distance)
	default:
		return -1
	}
}

// LeavesPersistent returns the PERSISTENT property of a leaves state (false when not leaves).
// CITE: LeavesBlock.PERSISTENT (BlockStateProperties.PERSISTENT).
func LeavesPersistent(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch b := StateList[s].(type) {
	case OakLeaves:
		return bool(b.Persistent)
	case SpruceLeaves:
		return bool(b.Persistent)
	case BirchLeaves:
		return bool(b.Persistent)
	case JungleLeaves:
		return bool(b.Persistent)
	case AcaciaLeaves:
		return bool(b.Persistent)
	case CherryLeaves:
		return bool(b.Persistent)
	case DarkOakLeaves:
		return bool(b.Persistent)
	case PaleOakLeaves:
		return bool(b.Persistent)
	case MangroveLeaves:
		return bool(b.Persistent)
	case AzaleaLeaves:
		return bool(b.Persistent)
	case FloweringAzaleaLeaves:
		return bool(b.Persistent)
	default:
		return false
	}
}

// LeavesDecaying is LeavesBlock.decaying(state) == !PERSISTENT && DISTANCE==7 — the randomTick decay
// gate. CITE: LeavesBlock.decaying.
func LeavesDecaying(s StateID) bool {
	if !IsLeaves(s) {
		return false
	}
	return !LeavesPersistent(s) && LeavesDistance(s) == 7
}

// LeavesWithDistance returns the leaves state id with DISTANCE set to d (1..7), PERSISTENT and
// WATERLOGGED preserved — the port of LeavesBlock.updateDistance's state.setValue(DISTANCE, d).
// ok=false if not leaves or d out of range. CITE: LeavesBlock.updateDistance (setValue(DISTANCE,d)).
func LeavesWithDistance(s StateID, d int) (StateID, bool) {
	if d < 1 || d > 7 || int(s) < 0 || int(s) >= len(StateList) {
		return 0, false
	}
	switch b := StateList[s].(type) {
	case OakLeaves:
		b.Distance = Integer(d)
		sid, ok := ToStateID[b]
		return sid, ok
	case SpruceLeaves:
		b.Distance = Integer(d)
		sid, ok := ToStateID[b]
		return sid, ok
	case BirchLeaves:
		b.Distance = Integer(d)
		sid, ok := ToStateID[b]
		return sid, ok
	case JungleLeaves:
		b.Distance = Integer(d)
		sid, ok := ToStateID[b]
		return sid, ok
	case AcaciaLeaves:
		b.Distance = Integer(d)
		sid, ok := ToStateID[b]
		return sid, ok
	case CherryLeaves:
		b.Distance = Integer(d)
		sid, ok := ToStateID[b]
		return sid, ok
	case DarkOakLeaves:
		b.Distance = Integer(d)
		sid, ok := ToStateID[b]
		return sid, ok
	case PaleOakLeaves:
		b.Distance = Integer(d)
		sid, ok := ToStateID[b]
		return sid, ok
	case MangroveLeaves:
		b.Distance = Integer(d)
		sid, ok := ToStateID[b]
		return sid, ok
	case AzaleaLeaves:
		b.Distance = Integer(d)
		sid, ok := ToStateID[b]
		return sid, ok
	case FloweringAzaleaLeaves:
		b.Distance = Integer(d)
		sid, ok := ToStateID[b]
		return sid, ok
	default:
		return 0, false
	}
}

// LeafDistanceAt is LeavesBlock.getDistanceAt(state) == getOptionalDistanceAt(state).orElse(7):
//   - a block in #minecraft:prevents_nearby_leaf_decay (== #minecraft:logs) -> 0
//   - a leaves state (has DISTANCE) -> its DISTANCE
//   - anything else -> 7 (Optional.empty -> orElse(7))
//
// This is the per-neighbour contribution the 6-neighbour min+1 distance recompute reads. CITE:
// LeavesBlock.getDistanceAt / getOptionalDistanceAt; #prevents_nearby_leaf_decay == #logs.
func LeafDistanceAt(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return 7
	}
	if leafLogBlockIDs[StateList[s].ID()] {
		return 0 // #prevents_nearby_leaf_decay (== #logs) -> OptionalInt.of(0)
	}
	if d := LeavesDistance(s); d >= 0 {
		return d // has DISTANCE property -> OptionalInt.of(distance)
	}
	return 7 // OptionalInt.empty -> orElse(7)
}

// ---- GRASS / MYCELIUM (net.minecraft.world.level.block.SpreadingSnowyBlock) ----
//
// GrassBlock + MyceliumBlock extend SpreadingSnowyBlock (base block = dirt). randomTick:
//   - if !canStayAlive -> set the base block (dirt) default.
//   - else if getMaxLocalRawBrightness(above) >= 9: 4 iterations, pick
//     blockpos1 = pos.offset(nextInt(3)-1, nextInt(5)-3, nextInt(3)-1); if that cell is DIRT and
//     canPropagate(grassDefault, level, blockpos1) -> set grass/mycelium there (SNOWY = isSnowySetting
//     of the cell above blockpos1).
// CITE: SpreadingSnowyBlock.{randomTick,canStayAlive,canPropagate}.

// IsGrassBlock reports whether a state id is a grass_block. CITE: GrassBlock.
func IsGrassBlock(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(GrassBlock)
	return ok
}

// IsMycelium reports whether a state id is mycelium. CITE: MyceliumBlock.
func IsMycelium(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(Mycelium)
	return ok
}

// IsSpreadingSnowy reports whether a state id is a SpreadingSnowyBlock (grass or mycelium) — the
// family whose randomTick runs the die/spread logic. CITE: SpreadingSnowyBlock subclasses.
func IsSpreadingSnowy(s StateID) bool { return IsGrassBlock(s) || IsMycelium(s) }

// SpreadingBaseBlock is the SpreadingSnowyBlock.baseBlock the die-to-dirt branch reverts to — dirt
// for BOTH grass and mycelium (both are constructed with Blocks.DIRT as the base block). CITE:
// GrassBlock / MyceliumBlock ctor (super(..., Blocks.DIRT)).
func SpreadingBaseBlock(s StateID) (StateID, bool) {
	if !IsSpreadingSnowy(s) {
		return 0, false
	}
	sid, ok := ToStateID[Dirt{}]
	return sid, ok
}

// SpreadingWithSnowy returns the same SpreadingSnowyBlock state with SNOWY set — the port of the
// spread branch's grassDefault.setValue(SNOWY, isSnowySetting(above)). It keys off the INPUT block
// family (grass -> grass, mycelium -> mycelium) so the spreading block propagates its own kind.
// ok=false if not grass/mycelium. CITE: SpreadingSnowyBlock.randomTick (setValue(SNOWY,...)).
func SpreadingWithSnowy(s StateID, snowy bool) (StateID, bool) {
	switch StateList[s].(type) {
	case GrassBlock:
		sid, ok := ToStateID[GrassBlock{Snowy: Boolean(snowy)}]
		return sid, ok
	case Mycelium:
		sid, ok := ToStateID[Mycelium{Snowy: Boolean(snowy)}]
		return sid, ok
	default:
		return 0, false
	}
}

// SpreadingDefault returns the family's defaultBlockState (SNOWY=false) — the port of
// this.defaultBlockState() used as the `grassDefault` propagate template (its canPropagate arg) and
// the base of the spread setValue. ok=false if not grass/mycelium. CITE: SpreadingSnowyBlock.
// randomTick (BlockState blockstate = this.defaultBlockState()).
func SpreadingDefault(s StateID) (StateID, bool) {
	switch StateList[s].(type) {
	case GrassBlock:
		sid, ok := ToStateID[GrassBlock{}]
		return sid, ok
	case Mycelium:
		sid, ok := ToStateID[Mycelium{}]
		return sid, ok
	default:
		return 0, false
	}
}

// IsDirt reports whether a state id is plain dirt — the block the grass/mycelium spread targets
// (`getBlockState(blockpos1).is(baseBlock)` where baseBlock == Blocks.DIRT). CITE:
// SpreadingSnowyBlock.randomTick (blockstate1.is(optional.get()) with optional == the dirt base).
func IsDirt(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(Dirt)
	return ok
}

// IsSnowySetting is SnowyBlock.isSnowySetting(state) == state.is(BlockTags.SNOW). #minecraft:snow ==
// { snow, snow_block, powder_snow } (verified snow.json). CITE: SnowyBlock.isSnowySetting.
func IsSnowySetting(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	case Snow, SnowBlock, PowderSnow:
		return true
	default:
		return false
	}
}

// IsSnowLayerOne is the SpreadingSnowyBlock.canStayAlive first clause: the block above is Blocks.SNOW
// (a snow LAYER, not snow_block) with LAYERS==1 -> the block stays alive regardless of light. CITE:
// SpreadingSnowyBlock.canStayAlive (above.is(Blocks.SNOW) && above.getValue(SnowLayerBlock.LAYERS)==1).
func IsSnowLayerOne(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	if sn, ok := StateList[s].(Snow); ok {
		return int(sn.Layers) == 1
	}
	return false
}

// FluidIsFull is FluidState.isFull() (getAmount()==8) for a block state's fluid — true for a full
// water/lava SOURCE (legacy level 0) or a falling-full fluid (level 8), and for a WATERLOGGED block
// (whose fluid is a full water source). The SpreadingSnowyBlock.canStayAlive middle clause reads
// getFluidState(above).isFull() -> a full fluid over the cell kills the grass. CITE:
// FluidState.isFull (getAmount()==8); FlowingFluid.getLegacyLevel (source -> level 0, falling-full
// -> level 8).
func FluidIsFull(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch b := StateList[s].(type) {
	case Water:
		return b.Level == 0 || b.Level == 8 // source (amount 8) or falling-full (amount 8)
	case Lava:
		return b.Level == 0 || b.Level == 8
	default:
		return isWaterlogged(b) // waterlogged -> full water source
	}
}

// leafLogBlockIDs is the flat #minecraft:logs block-id closure (== #prevents_nearby_leaf_decay),
// resolved from the 26.2 datagen tags/block/logs.json. The block package cannot import the
// world/levelgen/data tag resolver (that package imports block — an import cycle), so the closure is
// captured here as a stable id map and consulted BY ID (StateList[s].ID()) — which also sidesteps the
// package-var init ordering (StateList is populated in init(), so a package-var state-id set built
// eagerly would see an empty StateList). If Mojang adds a log family this list re-derives from the
// datagen closure (logs.json -> logs_that_burn + crimson_stems + warped_stems -> the per-wood
// *_logs tags -> log + wood + stripped variants). CITE: BlockTags.LOGS.
var leafLogBlockIDs = map[string]bool{
	"minecraft:acacia_log": true, "minecraft:acacia_wood": true,
	"minecraft:birch_log": true, "minecraft:birch_wood": true,
	"minecraft:cherry_log": true, "minecraft:cherry_wood": true,
	"minecraft:crimson_hyphae": true, "minecraft:crimson_stem": true,
	"minecraft:dark_oak_log": true, "minecraft:dark_oak_wood": true,
	"minecraft:jungle_log": true, "minecraft:jungle_wood": true,
	"minecraft:mangrove_log": true, "minecraft:mangrove_wood": true,
	"minecraft:oak_log": true, "minecraft:oak_wood": true,
	"minecraft:pale_oak_log": true, "minecraft:pale_oak_wood": true,
	"minecraft:spruce_log": true, "minecraft:spruce_wood": true,
	"minecraft:stripped_acacia_log": true, "minecraft:stripped_acacia_wood": true,
	"minecraft:stripped_birch_log": true, "minecraft:stripped_birch_wood": true,
	"minecraft:stripped_cherry_log": true, "minecraft:stripped_cherry_wood": true,
	"minecraft:stripped_crimson_hyphae": true, "minecraft:stripped_crimson_stem": true,
	"minecraft:stripped_dark_oak_log": true, "minecraft:stripped_dark_oak_wood": true,
	"minecraft:stripped_jungle_log": true, "minecraft:stripped_jungle_wood": true,
	"minecraft:stripped_mangrove_log": true, "minecraft:stripped_mangrove_wood": true,
	"minecraft:stripped_oak_log": true, "minecraft:stripped_oak_wood": true,
	"minecraft:stripped_pale_oak_log": true, "minecraft:stripped_pale_oak_wood": true,
	"minecraft:stripped_spruce_log": true, "minecraft:stripped_spruce_wood": true,
	"minecraft:stripped_warped_hyphae": true, "minecraft:stripped_warped_stem": true,
	"minecraft:warped_hyphae": true, "minecraft:warped_stem": true,
}
