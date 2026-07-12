package registrydata

// gameeventtags.go -- the RUNTIME GameEventTags query (the Holder<GameEvent>.is(TagKey<GameEvent>)
// analogue), the sibling of blocktags.go for the game_event registry.
//
// VibrationSystem.User.isValidVibration calls event.is(getListenableEvents()) where
// getListenableEvents() is a TagKey<GameEvent> (GameEventTags.VIBRATIONS for a sculk sensor,
// GameEventTags.SHRIEKER_CAN_LISTEN for a shrieker, GameEventTags.WARDEN_CAN_LISTEN for a warden).
// The membership set is BOUND at datapack load from tags/game_event/<name>.json -- the SAME embedded
// tree tags.go flattens for the ClientboundUpdateTags wire packet. GameEventInTag reproduces
// Holder.is(TagKey) by flattening that JSON (recursive #tag expansion, de-duplicated) and testing the
// event id.
//
//	[VERIFIED javap GameEventTags: VIBRATIONS = create("vibrations"), SHRIEKER_CAN_LISTEN =
//	 create("shrieker_can_listen"), WARDEN_CAN_LISTEN = create("warden_can_listen"),
//	 IGNORE_VIBRATIONS_SNEAKING = create("ignore_vibrations_sneaking"). Holder.is(TagKey) over the
//	 datapack-bound tag set == membership in the flattened tags/game_event/<name>.json.]

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"sync"
)

// gameEventTagCache holds each game_event tag's flattened member set (event name -> present), lazily
// built on first query and cached forever (the embedded tag tree is immutable). Keyed by the bare tag
// name (no "minecraft:" prefix, no "#"), e.g. "vibrations". A cached nil set marks an absent tag.
var (
	gameEventTagCacheMu sync.RWMutex
	gameEventTagCache   = map[string]map[string]struct{}{}
)

// GameEventInTag reports whether the game-event resource-location eventName (e.g. "minecraft:step" or
// the bare "step") is a member of the game_event tag tagName (bare name "vibrations", or the
// "#minecraft:vibrations" / "minecraft:vibrations" forms -- all normalized). It is the
// Holder<GameEvent>.is(TagKey<GameEvent>) analogue used by VibrationSystem.User.isValidVibration.
//
// A bare eventName is qualified to "minecraft:<name>" (tag JSON lists fully-qualified ids). An absent
// tag yields false with a nil error (empty-tag semantics, never a panic).
func GameEventInTag(eventName, tagName string) (bool, error) {
	tag := normalizeTagName(tagName)
	name := normalizeGameEventName(eventName)

	members, err := gameEventTagMembers(tag)
	if err != nil {
		return false, err
	}
	if members == nil {
		return false, nil // absent tag: no members
	}
	_, ok := members[name]
	return ok, nil
}

// normalizeGameEventName qualifies a bare event name to "minecraft:<name>" so it matches the fully-
// qualified ids the tag JSON lists. An already-namespaced name (contains ':') is returned unchanged.
func normalizeGameEventName(eventName string) string {
	if strings.Contains(eventName, ":") {
		return eventName
	}
	return "minecraft:" + eventName
}

// gameEventTagMembers returns the cached flattened member set for game_event tag `tag`, building it on
// first request by parsing tags/game_event/<tag>.json and recursively expanding #tag references.
// Concurrency-safe (RLock reads, double-checked write build); a nil map (no error) marks an absent tag.
func gameEventTagMembers(tag string) (map[string]struct{}, error) {
	gameEventTagCacheMu.RLock()
	if set, ok := gameEventTagCache[tag]; ok {
		gameEventTagCacheMu.RUnlock()
		return set, nil
	}
	gameEventTagCacheMu.RUnlock()

	gameEventTagCacheMu.Lock()
	defer gameEventTagCacheMu.Unlock()
	if set, ok := gameEventTagCache[tag]; ok {
		return set, nil
	}

	set := map[string]struct{}{}
	if err := expandGameEventTag(tag, set, map[string]bool{}); err != nil {
		return nil, err
	}
	if len(set) == 0 {
		gameEventTagCache[tag] = nil
		return nil, nil
	}
	gameEventTagCache[tag] = set
	return set, nil
}

// expandGameEventTag adds every leaf event name of game_event tag `tag` to `set`, recursively expanding
// any "#minecraft:other_tag" member (de-duplicated via `set`, cycle-guarded via `seen`). An absent tag
// JSON contributes nothing (empty-tag semantics, not an error); malformed JSON IS an error.
func expandGameEventTag(tag string, set map[string]struct{}, seen map[string]bool) error {
	if seen[tag] {
		return nil
	}
	seen[tag] = true

	file := path.Join("tags", "game_event", tag+".json")
	data, err := tagFS.ReadFile(file)
	if err != nil {
		return nil // absent tag file: no members
	}
	var tf tagFile
	if err := json.Unmarshal(data, &tf); err != nil {
		return fmt.Errorf("registrydata: parse game_event tag %s: %w", tag, err)
	}
	for _, v := range tf.Values {
		if strings.HasPrefix(v, "#") {
			ref := normalizeTagName(v)
			if err := expandGameEventTag(ref, set, seen); err != nil {
				return err
			}
			continue
		}
		set[normalizeGameEventName(v)] = struct{}{}
	}
	return nil
}
