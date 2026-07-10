package server

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// growth_randomtick.go -- the NETHER-WART / CHORUS-FLOWER / TURTLE-EGG / BUDDING-AMETHYST /
// CAVE-VINES random-tick handlers, ported 1:1 from the unobfuscated 26.2 jar. Each is wired into
// the random-tick driver's dispatchRandomTick (random_tick.go) by block identity. Growth is driven
// by the world's random-tick pass -- these handlers are CALLED, never called back.
//
// PIG-ORACLE SAFETY: the random-tick driver never runs on the pig-oracle path
// (TestPluginPigEqualsGoNativePig drives serverAiStep directly, never tickRandomBlocks), so these
// handlers do NOT pin the levelRandom stream the pig oracle uses. The draws below are deterministic
// for any seeded levelRandom so the seeded growth tests pin byte-identical outcomes. CITE:
// ServerChunkCache.tickChunks + ServerLevel.tickChunk (no pig-oracle caller).

// ---- NETHER WART (NetherWartBlock.randomTick) ----

// netherWartRandomTick is NetherWartBlock.randomTick: a wart below MAX_AGE (3) advances AGE with a
// 1-in-10 chance. r is the owning region; r.levelRandom is `this.random`.
//
// RNG DRAW ORDER (must match the jar exactly): the age < 3 gate draws NOTHING (IsRandomlyTicking
// already excludes a max wart, but the guard is mirrored). Then ONE nextInt(10); only on a 0 roll
// is AGE advanced. There is NO light/moisture/neighbour gate. CITE: NetherWartBlock.randomTick.
//
//	int age = state.getValue(AGE);
//	if (age < 3 && random.nextInt(10) == 0) level.setBlock(pos, state.setValue(AGE, age+1), 2);
//
// setBlock flag 2 == UPDATE_CLIENTS (no neighbour notify) -- mirrored as SetBlock + broadcast.
func (t *TickLoop) netherWartRandomTick(r *region, state block.StateID, pos pk.Position) {
	if t.world() == nil || r == nil || r.levelRandom == nil {
		return
	}
	age := block.NetherWartAge(state)
	if age < 0 {
		return // not a nether wart (defensive)
	}
	// `age < 3` gate FIRST (no draw). IsRandomlyTicking already excludes a max wart, but mirror it.
	if age >= block.NetherWartMaxAge {
		return
	}
	// `random.nextInt(10) == 0` -- the ONLY RNG draw. CITE: NetherWartBlock.randomTick.
	if r.levelRandom.NextIntN(10) != 0 {
		return
	}
	if grown, ok := block.NetherWartWithAge(state, age+1); ok {
		if t.world().SetBlock(pos, grown, dimMinY) {
			t.broadcastBlockUpdate(pos, grown)
		}
	}
}
