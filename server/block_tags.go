package server

// block_tags.go — the SERVER-package bridge from a world block STATE ID to a block-tag membership
// answer, the runtime BlockState.is(TagKey<Block>) the AI + interaction code calls (e.g. the cat's
// CatRelaxOnOwnerGoal / CatLieOnBedGoal asking "is the block at this position in #minecraft:beds").
//
// The mapping is two hops, both already authoritative in the codebase:
//
//	stateID  --level/block.StateList[stateID].ID()-->  block name ("minecraft:white_bed")
//	          --registrydata.BlockInTag(name, tag)-->  membership in the flattened tag JSON
//
// level/block.StateList is the state-id -> Block table (block.go init from block_states.nbt); Block.ID()
// is the resource name. registrydata.BlockInTag flattens the SAME embedded tags/block/<tag>.json the
// Update Tags wire packet uses (recursive #tag expansion), so the runtime answer is byte-consistent
// with what the client is told at registry load. No new tag data, no second source of truth.
//
//	[VERIFIED javap BlockState.is(TagKey<Block>): Holder.is(tag) over the block's bound tag set (the
//	 datapack tag JSON). BlockTags.BEDS == BlockTags.create("beds").]

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/server/registrydata"
)

// blockTagBeds is the bare name of net.minecraft.tags.BlockTags.BEDS (BlockTags.create("beds")). Pinned
// as a named constant (not a magic literal at the call sites) so the cat bed goals read the SAME tag the
// wire packet binds. CITE BlockTags.BEDS.
const blockTagBeds = "beds"

// blockInTag reports whether the block STATE `s` belongs to the block tag `tagName` (bare name, e.g.
// "beds"; the "#minecraft:" / "minecraft:" forms are also accepted — registrydata normalizes them). It
// resolves the state id to its block name via block.StateList, then delegates to registrydata.BlockInTag
// (the flattened, #tag-expanded membership test). It is the BlockState.is(TagKey<Block>) analogue.
//
// An out-of-range state id (a corrupt/unloaded read) is NOT in any tag -> false. An absent tag is
// likewise a false (empty-tag semantics, per registrydata.BlockInTag). A malformed embedded tag JSON is
// the only error path; it is swallowed to false here (the caller — an AI gate — treats "not a bed" as
// the safe default; a corrupt embed would have failed the boot-time Update Tags build first).
func blockInTag(s block.StateID, tagName string) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	name := block.StateList[s].ID()
	in, err := registrydata.BlockInTag(name, tagName)
	if err != nil {
		return false
	}
	return in
}
