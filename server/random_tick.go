package server

import (
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// random_tick.go — the RANDOM-TICK DRIVER: the 1:1 port of the vanilla per-section block-sampling
// pass that dispatches BlockState.randomTick. This is the UNSCHEDULED random ticker — a wholly
// separate subsystem from the SCHEDULED block-tick LevelTicks in block_ticks.go (which fires
// (block,pos) ticks at a target game-time). The random ticker instead SAMPLES random positions in
// every loaded, randomly-ticking section every tick and dispatches the block's randomTick handler.
//
// VANILLA CALL CHAIN (all verified against temp/cache/26.2-inner.jar this session via CFR):
//
//	net.minecraft.server.level.ServerChunkCache.tickChunks():
//	    int tickSpeed = level.getGameRules().get(GameRules.RANDOM_TICK_SPEED);   // default 3
//	    chunkMap.forEachBlockTickingChunk(chunk -> level.tickChunk(chunk, tickSpeed));
//
//	net.minecraft.server.level.ServerLevel.tickChunk(LevelChunk chunk, int tickSpeed):
//	    int minX = chunkPos.getMinBlockX();   // chunkX << 4
//	    int minZ = chunkPos.getMinBlockZ();   // chunkZ << 4
//	    // (ice-and-snow / precipitation loop — DEFERRED, see below)
//	    if (tickSpeed > 0) {
//	        for each LevelChunkSection section (by index):
//	            if (!section.isRandomlyTicking()) continue;
//	            int minYInSection = SectionPos.sectionToBlockCoord(chunk.getSectionYFromSectionIndex(idx));
//	            for (int i = 0; i < tickSpeed; ++i) {
//	                BlockPos pos = getBlockRandomPos(minX, minYInSection, minZ, 15);
//	                BlockState s = section.getBlockState(pos.x-minX, pos.y-minYInSection, pos.z-minZ);
//	                if (s.isRandomlyTicking()) s.randomTick(this, pos, this.random);
//	                FluidState f = s.getFluidState();
//	                if (f.isRandomlyTicking()) f.randomTick(this, pos, this.random);   // DEFERRED (fluids)
//	            }
//	    }
//
//	net.minecraft.world.level.Level.getBlockRandomPos(int xo, int yo, int zo, int yMask):
//	    this.randValue = this.randValue * 3 + 1013904223;   // int LCG on the SEPARATE randValue field
//	    int val = this.randValue >> 2;                       // arithmetic shift
//	    return new BlockPos(xo + (val & 0xF), yo + (val >> 16 & yMask), zo + (val >> 8 & 0xF));
//
// PIG-ORACLE SAFETY (mandate — PITFALLS Pitfall 5): getBlockRandomPos advances the region's OWN
// randValue int LCG (region.randValue), NEVER levelRandom, so the POSITION sampling adds ZERO draws
// to the levelRandom stream the pig oracle pins. Some wired handlers DO draw levelRandom (`this.random`)
// — CropBlock/BeetrootBlock.randomTick roll random.nextInt for growth; sugar cane and farmland draw
// none. The pig oracle (TestPluginPigEqualsGoNativePig) drives serverAiStep directly and NEVER runs
// this driver, so the levelRandom stream it pins is untouched regardless of which handlers draw here;
// the byte-identical gate stays green.
//
// PLACEMENT: this is a WORLD-GLOBAL phase (it iterates the SHARED ChunkManager's loaded columns and
// mutates the shared world via SetBlock, exactly like the scheduled-block drain), so it runs on the
// COORDINATOR inside tickWorld — NOT in the per-region entity fan-out (which touches only per-region
// entity stores). It is an ADDITIVE call inside the existing tickWorld phase, so no new tick phase is
// added and the fixed tick order is unchanged (TestTickPhaseOrder stays green).

// randomTickSpeed is the GameRules.RANDOM_TICK_SPEED gamerule value. v1 has no gamerule engine yet;
// it is the vanilla default (3), structured to become a real ServerLevel.getGameRules().get(
// GameRules.RANDOM_TICK_SPEED) read later — the same cited-default discipline as mobGriefing in
// explosion_blocks.go (never bake it away).
//
//	[VERIFIED CFR net.minecraft.world.level.gamerules.GameRules: RANDOM_TICK_SPEED =
//	 registerInteger("random_tick_speed", GameRuleCategory.UPDATES, 3, 0) — default 3.]
const randomTickSpeed = 3

// blockRandomPosMul / blockRandomPosAdd are the getBlockRandomPos int-LCG constants
// (randValue = randValue*3 + 1013904223). Named for the faithful port; the multiply/add wrap in
// int32 exactly as Java's `int` arithmetic overflows. CITE: Level.getBlockRandomPos.
const (
	blockRandomPosMul = 3
	blockRandomPosAdd = 1013904223
)

// nextBlockRandomPos is net.minecraft.world.level.Level.getBlockRandomPos(xo, yo, zo, yMask): advance
// the region's randValue int LCG, then bit-extract the three offsets. yMask is always 15 at the call
// site (the vanilla ServerLevel.tickChunk passes 15), so y spans a full 16-block section. It draws
// from region.randValue — its OWN stream, so it NEVER perturbs levelRandom (the pig-oracle stream).
// int32 wrapping reproduces Java's `int` overflow; `>>` on int32 is Go's arithmetic (sign-extending)
// shift, matching Java's `>>`. CITE: Level.getBlockRandomPos.
func (r *region) nextBlockRandomPos(xo, yo, zo, yMask int) pk.Position {
	r.randValue = r.randValue*blockRandomPosMul + blockRandomPosAdd
	val := r.randValue >> 2
	return pk.Position{
		X: xo + int(val&0xF),
		Y: yo + int((val>>16)&int32(yMask)),
		Z: zo + int((val>>8)&0xF),
	}
}

// tickRandomBlocks is the driver entry — the ServerChunkCache.tickChunks + ServerLevel.tickChunk
// port. It reads the RANDOM_TICK_SPEED gamerule (constant 3 in v1), and for every loaded (Ready)
// column runs tickChunk. A nil world (Phase-3-style tests / pre-SetWorld) or tickSpeed<=0 is a cheap
// no-op. Tick-owned; called from tickWorld on the coordinator. CITE: ServerChunkCache.tickChunks.
func (t *TickLoop) tickRandomBlocks() {
	if t.world() == nil {
		return // no world wired: cheap no-op (pre-SetWorld / bare test loops)
	}
	tickSpeed := randomTickSpeed
	if tickSpeed <= 0 {
		return // GameRules.RANDOM_TICK_SPEED == 0 disables random ticking (tickChunk `if tickSpeed > 0`)
	}
	// forEachBlockTickingChunk -> tickChunk per loaded column. ForEachReady ranges the tick-owned
	// column map; tickChunk only reads sections + writes block STATE (SetBlock, which mutates a
	// section in place, never the column map), so ranging is safe.
	t.world().ForEachReady(func(pos level.ChunkPos, ch *level.Chunk) {
		t.tickChunk(pos, ch, tickSpeed)
	})
}

// tickChunk is net.minecraft.server.level.ServerLevel.tickChunk(chunk, tickSpeed) — the block
// random-tick body (the ice-and-snow precipitation loop is DEFERRED; see the deferral note). For
// every section that isRandomlyTicking, it draws tickSpeed random positions, reads the state, and
// dispatches randomTick when the state isRandomlyTicking. minX/minZ are the chunk's min block coords
// (chunkX<<4 / chunkZ<<4 == ChunkPos.getMinBlockX/getMinBlockZ). minYInSection is the section's min
// block Y (SectionPos.sectionToBlockCoord(minSectionY + sectionIndex)) — for this dimension
// minSectionY == dimMinY>>4, so minYInSection == dimMinY + sectionIndex*16. CITE:
// ServerLevel.tickChunk; ChunkPos.getMinBlockX/Z; SectionPos.sectionToBlockCoord.
func (t *TickLoop) tickChunk(pos level.ChunkPos, ch *level.Chunk, tickSpeed int) {
	minX := int(pos[0]) << 4
	minZ := int(pos[1]) << 4
	// The random ticker is a WORLD-GLOBAL pass over the SHARED ChunkManager on the coordinator (the
	// world is not yet per-region-sharded — a locked deferral), so it uses only() (the tolerant
	// globalRegion resolver) rather than cur() (which would panic under strictRegion off the fan-out).
	// only()'s randValue drives getBlockRandomPos (its OWN int LCG — never the pig-oracle levelRandom);
	// its levelRandom is `this.random` for the randomTick handler. When the world is sharded per region
	// (the follow-up), this becomes the owning region's randValue/levelRandom.
	r := t.only()

	// tickChunk: `if (tickSpeed > 0)` — guaranteed by the caller, but mirror the guard for fidelity.
	if tickSpeed <= 0 {
		return
	}
	for sectionIndex := range ch.Sections {
		section := &ch.Sections[sectionIndex]
		// `if (!section.isRandomlyTicking()) continue;` — a section with no randomly-ticking block is
		// skipped entirely (no RNG drawn for it), exactly as vanilla. HasRandomlyTicking approximates
		// tickingBlockCount>0 by a palette scan (per-section counter is a perf follow-up).
		if !section.HasRandomlyTicking() {
			continue
		}
		// minYInSection = SectionPos.sectionToBlockCoord(getSectionYFromSectionIndex(sectionIndex)).
		// getSectionYFromSectionIndex(idx) == minSectionY + idx; minSectionY == dimMinY >> 4. So
		// minYInSection == (minSectionY + idx) << 4 == dimMinY + idx*16.
		minYInSection := dimMinY + sectionIndex*16
		for i := 0; i < tickSpeed; i++ {
			// getBlockRandomPos(minX, minYInSection, minZ, 15): draws region.randValue (NOT levelRandom).
			bpos := r.nextBlockRandomPos(minX, minYInSection, minZ, 15)
			// section.getBlockState(x-minX, y-minYInSection, z-minZ): resolve the state at the sampled
			// absolute pos. GetBlock over the world uses the SAME (y&15)<<8|(z&15)<<4|(x&15) local index
			// as vanilla's PalettedContainer.Strategy.getIndex(x,y,z), and bpos is always inside THIS
			// column+section (offsets 0..15 off min), so the world read is equivalent to the vanilla
			// section-local read — done via GetBlock so the palette lookup is the shared, tested path.
			state, ok := t.world().GetBlock(bpos, dimMinY)
			if !ok {
				continue // out-of-range (should not happen: bpos is inside a Ready column+section)
			}
			// `if (blockState.isRandomlyTicking()) blockState.randomTick(this, pos, this.random);`
			// r is the owning region (the tickChunk-resolved only()): its levelRandom is `this.random`
			// passed to the handler, and its randValue drove getBlockRandomPos above.
			if block.IsRandomlyTicking(state) {
				t.dispatchRandomTick(r, state, bpos)
			}
			// FLUID random tick (`if (fluidState.isRandomlyTicking()) fluidState.randomTick(...)`) is
			// DEFERRED — see the deferral note at dispatchRandomTick.
		}
	}
}

// dispatchRandomTick is the BlockState.randomTick(level, pos, random) dispatch — the per-block-family
// switch that routes a sampled randomly-ticking state to its ported randomTick handler. It is the
// growth seam: each randomly-ticking family (crops, saplings, leaves, grass spread, farmland moisture,
// …) plugs in here BY BLOCK IDENTITY as it is ported, alongside its IsRandomlyTicking flag in
// level/block. r is the OWNING region (tickChunk's only()); its levelRandom is `this.random` handed
// to a handler that draws RNG (crops/beetroot), and its randValue is the position-sampling stream.
// CITE: BlockBehaviour$BlockStateBase.randomTick -> the block's randomTick(state, level, pos, random).
func (t *TickLoop) dispatchRandomTick(r *region, state block.StateID, pos pk.Position) {
	switch {
	case block.IsSugarCane(state):
		// SugarCaneBlock.randomTick(state, level, pos, random): deterministic growth (no RNG draw), so
		// this touches levelRandom ZERO times — the pig-oracle stream is unperturbed.
		t.sugarCaneRandomTick(state, pos)
	case block.IsCrop(state):
		// CropBlock.randomTick / BeetrootBlock.randomTick: light-gated growth. DRAWS levelRandom
		// (random.nextInt) — see crop_block.go. The pig oracle never runs the random-tick driver, so
		// the levelRandom stream it pins is untouched by this handler.
		t.cropRandomTick(r, state, pos)
	case block.IsFarmland(state):
		// FarmlandBlock.randomTick: moisture drop / hydrate / turnToDirt. NO RNG draw (deterministic
		// off the near-water + rain + moisture reads), so it leaves levelRandom untouched.
		t.farmlandRandomTick(r, state, pos)
	case block.IsSapling(state):
		// SaplingBlock.randomTick: light-gated growth. DRAWS levelRandom (random.nextInt(7); the STAGE
		// advance draws none, the oak grow step draws the tree-feature sequence). See growth_block.go.
		t.saplingRandomTick(r, state, pos)
	case block.IsLeaves(state):
		// LeavesBlock.randomTick: DISTANCE==7 && !PERSISTENT -> decay to air. NO RNG draw (deterministic
		// off DISTANCE/PERSISTENT), so it leaves levelRandom untouched. See growth_block.go.
		t.leavesRandomTick(r, state, pos)
	case block.IsSpreadingSnowy(state):
		// SpreadingSnowyBlock.randomTick (grass/mycelium): die-to-dirt (no draw) OR spread (draws 12
		// levelRandom ints -- 3 per the 4 spread attempts). See growth_block.go.
		t.grassRandomTick(r, state, pos)
	case block.IsCactus(state):
		// CactusBlock.randomTick: grow/flower. DRAWS levelRandom (one nextDouble, only in the age==8
		// canSurvive branch). See growth_extra.go. The driver never runs on the pig-oracle path.
		t.cactusRandomTick(r, state, pos)
	case block.IsBambooSapling(state):
		// BambooSaplingBlock.randomTick: grow to a stalk. DRAWS levelRandom (one nextInt(3) always).
		// See growth_extra.go.
		t.bambooSaplingRandomTick(r, state, pos)
	case block.IsBamboo(state):
		// BambooStalkBlock.randomTick: grow the stalk. DRAWS levelRandom (one nextInt(3) always; one
		// nextFloat iff the height >= 11). IsRandomlyTicking gates on STAGE==0. See growth_extra.go.
		t.bambooStalkRandomTick(r, state, pos)
	case block.IsIce(state):
		// IceBlock.randomTick: melt to water when block-light > 11 - lightDampening. NO RNG draw. See
		// growth_extra.go.
		t.iceRandomTick(state, pos)
	case block.IsSnowLayer(state):
		// SnowLayerBlock.randomTick: melt to air when block-light > 11. NO RNG draw. See growth_extra.go.
		t.snowLayerRandomTick(state, pos)
	case block.IsCocoa(state):
		// CocoaBlock.randomTick: 1-in-5 AGE advance (no light gate). DRAWS levelRandom (one
		// unconditional nextInt(5)). IsRandomlyTicking gates on AGE < MAX_AGE. See growth_extra.go.
		t.cocoaRandomTick(r, state, pos)
	case block.IsSweetBerryBush(state):
		// SweetBerryBushBlock.randomTick: 1-in-5 AGE advance gated on light>=9. DRAWS levelRandom (one
		// nextInt(5), only for age<3). IsRandomlyTicking gates on AGE < 3. See growth_extra.go.
		t.sweetBerryRandomTick(r, state, pos)
	case block.IsStem(state):
		// StemBlock.randomTick (pumpkin + melon): light-gated growth roll (one nextInt((int)(25/speed)+1));
		// at AGE 7 a SECOND draw (getRandomDirection == nextInt(4)) picks the fruit-spawn direction. DRAWS
		// levelRandom. IsRandomlyTicking is true for every AGE. See stem.go.
		t.stemRandomTick(r, state, pos)
	case block.IsKelp(state):
		// KelpBlock / GrowingPlantHeadBlock.randomTick: 0.14 grow-up chance while the cell above is water.
		// DRAWS levelRandom (one nextDouble, only for AGE<25). IsRandomlyTicking gates on AGE < 25. See
		// kelp.go.
		t.kelpRandomTick(r, state, pos)
	default:
		// A state whose IsRandomlyTicking is true but whose randomTick handler is not yet ported: no-op
		// (the family's IsRandomlyTicking should not be true until its handler is wired — kept as a
		// defensive fall-through so adding the flag before the handler never mis-dispatches).
	}
}
