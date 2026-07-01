package block

// sand.go — the #minecraft:sand block-tag membership predicate, ported 1:1 from the unobfuscated 26.2
// datagen tag closure. It backs TurtleEggBlock.isSand (BlockState.is(BlockTags.SAND)) — the turtle's
// egg-lay sand check (server/ai_goals_turtle.go). The tag closure is a literal set of the three sand
// blocks, mirroring block/sugarcane.go's IsSupportsSugarCane #sand branch.
//
// CITE: BlockTags.SAND (26.2 datagen tags/block/sand.json): { sand, red_sand, suspicious_sand }.

// IsSand reports whether a state id is a member of the #minecraft:sand block tag (sand / red_sand /
// suspicious_sand). It is TurtleEggBlock.isSand's BlockState.is(BlockTags.SAND) — the exact tag closure.
//
//	[VERIFIED 26.2 datagen tags/block/sand.json: values = [minecraft:sand, minecraft:red_sand,
//	 minecraft:suspicious_sand]. CITE TurtleEggBlock.isSand: getBlockState(pos).is(BlockTags.SAND).]
func IsSand(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	case Sand, RedSand, SuspiciousSand:
		return true
	default:
		return false
	}
}
