package block

// lilypad.go — the minecraft:lily_pad state predicate. It backs FishingHook.getOpenWaterTypeForBlock's
// `state.is(Blocks.LILY_PAD)` branch (a lily pad counts as ABOVE_WATER for the open-water treasure test).
//
// CITE: FishingHook.getOpenWaterTypeForBlock (state.isAir() || state.is(Blocks.LILY_PAD) -> ABOVE_WATER).

// IsLilyPad reports whether a state id is the minecraft:lily_pad block. Mirrors the IsSand tag-closure
// pattern (a single block type here, not a tag).
func IsLilyPad(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	case LilyPad:
		return true
	default:
		return false
	}
}
