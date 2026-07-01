package registrydata

// blocktags.go — the RUNTIME BlockTags query (the BlockState.is(TagKey<Block>) analogue).
//
// The Update Tags machinery in tags.go resolves every registry's tags into WIRE form (block-name ->
// numeric network index) for the ClientboundUpdateTags packet the client validates registry loading
// against. This file adds the SERVER-SIDE query the AI/interaction code needs: "is block <name> a
// member of #minecraft:<tag>?" — the vanilla Holder-tag membership test.
//
// VANILLA MODEL (javap-verified this session, temp/cache/26.2-inner.jar):
//
//	net.minecraft.tags.BlockTags.BEDS = BlockTags.create("beds") == TagKey.create(Registries.BLOCK,
//	    ResourceLocation("minecraft","beds")).
//	net.minecraft.world.level.block.state.BlockState.is(TagKey<Block> tag): delegates to the block's
//	    Holder.is(tag) — true iff the block's Holder$Reference carries that TagKey in its bound tag set.
//	    The tag set is BOUND at datapack load from the SAME tag JSON tags.go embeds (data/minecraft/
//	    tags/block/<name>.json), so a runtime membership test resolves to "is <block name> listed in
//	    (the flattened, #tag-expanded) beds.json".
//
// So BlockInTag(blockName, "beds") reproduces BlockState.is(BlockTags.BEDS) by asking whether blockName
// is in the flattened member set of tags/block/beds.json — the EXACT same embedded JSON, expanded the
// EXACT same way (recursive #tag flattening) tags.go uses for the wire. There is no second source of
// truth: both the wire packet and this query read the one embedded tag tree (tagFS).

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"sync"
)

// blockTagCache holds each block tag's flattened member set (block name -> present), lazily built on
// first query and cached forever (the embedded tag tree is immutable at runtime). Keyed by the bare
// tag name (no "minecraft:" prefix, no "#"), e.g. "beds". A cached nil set marks a tag that does not
// exist in the block registry's tag tree (a miss is a stable, cheap answer).
var (
	blockTagCacheMu sync.RWMutex
	blockTagCache   = map[string]map[string]struct{}{}
)

// BlockInTag reports whether the block resource-location blockName (e.g. "minecraft:white_bed") is a
// member of the block tag tagName (the bare name, e.g. "beds", OR the "#minecraft:beds" / "minecraft:
// beds" forms — all normalized). It is the runtime BlockState.is(TagKey<Block>) analogue: it flattens
// the embedded tags/block/<tagName>.json (recursively expanding any "#minecraft:other_tag" reference,
// de-duplicated — the SAME expansion tags.go's expandTag applies for the wire) and tests membership.
//
// A blockName WITHOUT a namespace is treated as "minecraft:<blockName>" (the tag JSON always writes the
// fully-qualified id). A tagName that does not exist in the embedded block tag tree returns false with
// a nil error (an absent tag has no members — the vanilla empty-tag semantics, never a panic).
//
//	[VERIFIED javap BlockState.is(TagKey): Holder.is(tag) over the bound tag set; the tag set is the
//	 datapack tag JSON — the same tags/block/<name>.json this reads. BlockTags.BEDS == create("beds").]
func BlockInTag(blockName, tagName string) (bool, error) {
	tag := normalizeTagName(tagName)
	name := normalizeBlockName(blockName)

	members, err := blockTagMembers(tag)
	if err != nil {
		return false, err
	}
	if members == nil {
		return false, nil // absent tag: no members (empty-tag semantics)
	}
	_, ok := members[name]
	return ok, nil
}

// normalizeTagName strips a leading "#" and a "minecraft:" prefix so "beds", "#minecraft:beds", and
// "minecraft:beds" all resolve to the bare "beds" used to locate tags/block/beds.json.
func normalizeTagName(tagName string) string {
	s := strings.TrimPrefix(tagName, "#")
	s = strings.TrimPrefix(s, "minecraft:")
	return s
}

// normalizeBlockName qualifies a bare block name to "minecraft:<name>" so it matches the fully-
// qualified ids the tag JSON lists. An already-namespaced name (contains ':') is returned unchanged.
func normalizeBlockName(blockName string) string {
	if strings.Contains(blockName, ":") {
		return blockName
	}
	return "minecraft:" + blockName
}

// blockTagMembers returns the cached flattened member set for block tag `tag` (bare name), building it
// on first request by parsing tags/block/<tag>.json and recursively expanding #tag references. It
// returns a nil map (no error) for a tag whose JSON is absent — the empty-tag case. Concurrency-safe:
// reads take the RLock, the one-time build takes the write lock (double-checked).
func blockTagMembers(tag string) (map[string]struct{}, error) {
	blockTagCacheMu.RLock()
	if set, ok := blockTagCache[tag]; ok {
		blockTagCacheMu.RUnlock()
		return set, nil
	}
	blockTagCacheMu.RUnlock()

	blockTagCacheMu.Lock()
	defer blockTagCacheMu.Unlock()
	if set, ok := blockTagCache[tag]; ok {
		return set, nil // built by another goroutine between the RUnlock and the Lock
	}

	set := map[string]struct{}{}
	if err := expandBlockTag(tag, set, map[string]bool{}); err != nil {
		return nil, err
	}
	if len(set) == 0 {
		// Distinguish "absent tag" (no file) from "present but empty": expandBlockTag returns a nil
		// error and leaves the set empty for BOTH; the observable answer (no members) is identical, so a
		// nil-map cache entry is correct for either. Cache it so repeat misses stay O(1).
		blockTagCache[tag] = nil
		return nil, nil
	}
	blockTagCache[tag] = set
	return set, nil
}

// expandBlockTag adds every leaf block name of block tag `tag` to `set`, recursively expanding any
// "#minecraft:other_tag" member (de-duplicated via `set`, cycle-guarded via `seen`) — the SAME flatten
// tags.go's expandTag performs for the wire, but keyed by NAME (the runtime query) instead of index.
// A tag whose JSON is absent contributes nothing (the empty-tag case): it is NOT an error, so a query
// for a never-defined tag simply yields no members. A malformed JSON IS an error (a corrupt embed).
func expandBlockTag(tag string, set map[string]struct{}, seen map[string]bool) error {
	if seen[tag] {
		return nil // already on this expansion path — break cycles defensively
	}
	seen[tag] = true

	file := path.Join("tags", "block", tag+".json")
	data, err := tagFS.ReadFile(file)
	if err != nil {
		return nil // absent block tag file: no members (empty-tag semantics, not an error)
	}
	var tf tagFile
	if err := json.Unmarshal(data, &tf); err != nil {
		return fmt.Errorf("registrydata: parse block tag %s: %w", tag, err)
	}
	for _, v := range tf.Values {
		if strings.HasPrefix(v, "#") {
			ref := normalizeTagName(v)
			if err := expandBlockTag(ref, set, seen); err != nil {
				return err
			}
			continue
		}
		set[normalizeBlockName(v)] = struct{}{}
	}
	return nil
}
