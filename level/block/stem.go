package block

// stem.go -- block-family predicates and state accessors for the STEM (pumpkin + melon) random-tick
// handler (server/stem.go), ported 1:1 from the unobfuscated 26.2 jar
// (net.minecraft.world.level.block.StemBlock). A small new file (rather than editing the big
// generated blocks.go) keeps the generated data untouched. CITE per function.

// StemMaxAge is StemBlock.MAX_AGE (7). CITE: StemBlock.MAX_AGE.
const StemMaxAge = 7

// stemKind classifies a StemBlock state (pumpkin vs melon) so the shared handler can resolve the
// correct fruit + attached-stem block. CITE: Blocks.PUMPKIN_STEM / Blocks.MELON_STEM registration
// (each StemBlock carries its own fruit / attachedStem / fruitSupportBlocks).
type stemKind int

const (
	stemNone stemKind = iota
	stemPumpkin
	stemMelon
)

// IsStem reports Blocks.PUMPKIN_STEM or Blocks.MELON_STEM (a growing StemBlock, any AGE). The
// ATTACHED_*_STEM blocks are AttachedStemBlock and do NOT random-tick, so they are excluded. CITE:
// StemBlock (Blocks.PUMPKIN_STEM / Blocks.MELON_STEM).
func IsStem(s StateID) bool { return stemKindOf(s) != stemNone }

// stemKindOf classifies a StateID into its stem family (none for a non-stem). CITE: StemBlock.
func stemKindOf(s StateID) stemKind {
	if int(s) < 0 || int(s) >= len(StateList) {
		return stemNone
	}
	switch StateList[s].(type) {
	case PumpkinStem:
		return stemPumpkin
	case MelonStem:
		return stemMelon
	default:
		return stemNone
	}
}

// StemAge is state.getValue(StemBlock.AGE) (0..7) for a stem state, or -1 for a non-stem. CITE:
// StemBlock.AGE (IntegerProperty 0..7).
func StemAge(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	switch b := StateList[s].(type) {
	case PumpkinStem:
		return int(b.Age)
	case MelonStem:
		return int(b.Age)
	default:
		return -1
	}
}

// StemWithAge returns the same stem block with AGE set to age (0..7). ok=false for a non-stem s or
// an out-of-range age. CITE: StemBlock.randomTick (state.setValue(AGE, age+1)).
func StemWithAge(s StateID, age int) (StateID, bool) {
	if age < 0 || age > StemMaxAge {
		return s, false
	}
	switch stemKindOf(s) {
	case stemPumpkin:
		id, ok := ToStateID[PumpkinStem{Age: Integer(age)}]
		return id, ok
	case stemMelon:
		id, ok := ToStateID[MelonStem{Age: Integer(age)}]
		return id, ok
	default:
		return s, false
	}
}

// StemFruitState returns the fruit block's defaultBlockState for the given stem: Blocks.PUMPKIN for
// a pumpkin stem, Blocks.MELON for a melon stem. ok=false for a non-stem s. CITE: Blocks.PUMPKIN_STEM
// fruit == BlockItemIds.PUMPKIN (Blocks.PUMPKIN); Blocks.MELON_STEM fruit == BlockItemIds.MELON
// (Blocks.MELON).
func StemFruitState(s StateID) (StateID, bool) {
	switch stemKindOf(s) {
	case stemPumpkin:
		id, ok := ToStateID[Pumpkin{}]
		return id, ok
	case stemMelon:
		id, ok := ToStateID[Melon{}]
		return id, ok
	default:
		return s, false
	}
}

// StemAttachedState returns the attached-stem block's defaultBlockState with FACING set to facing
// (the horizontal Direction the fruit spawned in): Blocks.ATTACHED_PUMPKIN_STEM /
// Blocks.ATTACHED_MELON_STEM. ok=false for a non-stem s. CITE: StemBlock.randomTick
// (attachedStem.defaultBlockState().setValue(HorizontalDirectionalBlock.FACING, direction)).
func StemAttachedState(s StateID, facing Direction) (StateID, bool) {
	switch stemKindOf(s) {
	case stemPumpkin:
		id, ok := ToStateID[AttachedPumpkinStem{Facing: facing}]
		return id, ok
	case stemMelon:
		id, ok := ToStateID[AttachedMelonStem{Facing: facing}]
		return id, ok
	default:
		return s, false
	}
}

// StemFruitSupport reports BlockState.is(fruitSupportBlocks) for a stem's fruit-support check.
// Both stems use #minecraft:supports_*_stem_fruit, each of which resolves (via
// #minecraft:supports_stem_fruit) to #minecraft:supports_vegetation -- the exact closure
// IsVegetationGround implements (substrate_overworld dirt/grass/moss/mud + farmland). So the fruit
// may spawn on the same ground vegetation grows on. CITE: BlockTags.SUPPORTS_MELON_STEM_FRUIT /
// SUPPORTS_PUMPKIN_STEM_FRUIT -> #supports_stem_fruit -> #supports_vegetation
// (tags/block/supports_*_stem_fruit.json).
func StemFruitSupport(s StateID) bool { return IsVegetationGround(s) }
