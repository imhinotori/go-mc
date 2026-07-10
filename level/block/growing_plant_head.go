package block

// growing_plant_head.go -- GrowingPlantHeadBlock age helpers for the shears shear-to-max-age
// interaction (net.minecraft.world.level.block.GrowingPlantHeadBlock), ported 1:1 from the 26.2 jar.
//
// The GrowingPlantHeadBlock subclasses are the vine HEADS that grow downward/upward: cave_vines
// (age 0..25 + berries), weeping_vines, twisting_vines. Shearing a non-max-age head snaps it to
// MAX_AGE so it stops growing. The *_plant bodies are NOT heads (no AGE) and are not shearable this way.
//
// CITE (temp/cache/26.2-inner.jar, javap -c -p this session):
//   GrowingPlantHeadBlock.MAX_AGE == 25 (bipush 25).
//   GrowingPlantHeadBlock.isMaxAge(state): state.getValue(AGE) == 25.
//   GrowingPlantHeadBlock.getMaxAgeState(state): state.setValue(AGE, 25).

// growingPlantHeadMaxAge is GrowingPlantHeadBlock.MAX_AGE. CITE: GrowingPlantHeadBlock.MAX_AGE == 25.
const growingPlantHeadMaxAge = 25

// GrowingPlantHeadAge returns the AGE (0..25) of a growing-plant HEAD block (cave_vines/weeping_vines/
// twisting_vines), or -1 if the state is not a growing-plant head. CITE: GrowingPlantHeadBlock.AGE.
func GrowingPlantHeadAge(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	switch b := StateList[s].(type) {
	case CaveVines:
		return int(b.Age)
	case WeepingVines:
		return int(b.Age)
	case TwistingVines:
		return int(b.Age)
	}
	return -1
}

// GrowingPlantHeadMaxAgeState is GrowingPlantHeadBlock.getMaxAgeState(state): the same head block with
// AGE=25 (preserving other properties, e.g. cave_vines' berries). ok=false for a non-head state. CITE:
// GrowingPlantHeadBlock.getMaxAgeState.
func GrowingPlantHeadMaxAgeState(s StateID) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	switch b := StateList[s].(type) {
	case CaveVines:
		b.Age = growingPlantHeadMaxAge
		id, ok := ToStateID[b]
		return id, ok
	case WeepingVines:
		b.Age = growingPlantHeadMaxAge
		id, ok := ToStateID[b]
		return id, ok
	case TwistingVines:
		b.Age = growingPlantHeadMaxAge
		id, ok := ToStateID[b]
		return id, ok
	}
	return s, false
}
