package block

import (
	"reflect"
	"strings"
)

// IsDoor reports whether a state id resolves to a DoorBlock. DoorBlock carries
// {FACING, HALF:DoubleBlockHalf, HINGE, OPEN, POWERED}; its ID ends in "_door". The
// DoubleBlockHalf-typed Half field disambiguates it from a trapdoor (Half:Half).
// CITE: net.minecraft.world.level.block.DoorBlock.
func IsDoor(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	b := StateList[s]
	if !strings.HasSuffix(b.ID(), "_door") {
		return false
	}
	return hasDoubleBlockHalf(b) && hasBoolField(b, "Open")
}

// IsTrapdoor reports whether a state id resolves to a TrapDoorBlock (ID ends "_trapdoor").
// CITE: net.minecraft.world.level.block.TrapDoorBlock.
func IsTrapdoor(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	b := StateList[s]
	return strings.HasSuffix(b.ID(), "_trapdoor") && hasBoolField(b, "Open")
}

// IsFenceGate reports whether a state id resolves to a FenceGateBlock (ID ends "_fence_gate").
// CITE: net.minecraft.world.level.block.FenceGateBlock.
func IsFenceGate(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	b := StateList[s]
	return strings.HasSuffix(b.ID(), "_fence_gate") && hasBoolField(b, "Open")
}

// DoorOpen reads the OPEN property of any door/trapdoor/fence-gate state
// (BlockState.getValue(OPEN)). Returns false for a block with no Open field.
func DoorOpen(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	return boolField(StateList[s], "Open")
}

// DoorWithOpen resolves the state with OPEN set to open, preserving every other property
// (state.setValue(OPEN, open)). Returns (s, false) if the block has no Open field or the
// resulting state is not a registered block state.
func DoorWithOpen(s StateID, open bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	nb, ok := withBoolField(StateList[s], "Open", open)
	if !ok {
		return s, false
	}
	sid, ok := ToStateID[nb]
	return sid, ok
}

// DoorCycleOpen resolves the state with OPEN flipped (state.cycle(OPEN)) -- the exact op
// DoorBlock/TrapDoorBlock.useWithoutItem apply. Returns (s, false) if not openable.
func DoorCycleOpen(s StateID) (StateID, bool) {
	return DoorWithOpen(s, !DoorOpen(s))
}

// DoorHalf reads the HALF (DoubleBlockHalf) property of a door state. Returns (Lower, false)
// for a non-door / a block with no DoubleBlockHalf.
func DoorHalf(s StateID) (DoubleBlockHalf, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return DoubleBlockHalfLower, false
	}
	v := reflect.ValueOf(StateList[s])
	if v.Kind() != reflect.Struct {
		return DoubleBlockHalfLower, false
	}
	f := v.FieldByName("Half")
	if !f.IsValid() || f.Type() != reflect.TypeOf(DoubleBlockHalf(0)) {
		return DoubleBlockHalfLower, false
	}
	return DoubleBlockHalf(f.Uint()), true
}

// FenceGateFacing reads the FACING (Direction) property of a fence-gate state. Returns
// (North, false) for a block with no Facing field.
func FenceGateFacing(s StateID) (Direction, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return North, false
	}
	v := reflect.ValueOf(StateList[s])
	if v.Kind() != reflect.Struct {
		return North, false
	}
	f := v.FieldByName("Facing")
	if !f.IsValid() || f.Type() != reflect.TypeOf(Direction(0)) {
		return North, false
	}
	return Direction(f.Uint()), true
}

// FenceGateWithFacing resolves the fence-gate state with FACING set to facing, preserving
// every other property (state.setValue(FACING, direction) -- the FenceGateBlock "face the
// player on open" flip). Returns (s, false) if no Facing field or the result is unregistered.
func FenceGateWithFacing(s StateID, facing Direction) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	v := reflect.ValueOf(StateList[s])
	if v.Kind() != reflect.Struct {
		return s, false
	}
	cp := reflect.New(v.Type()).Elem()
	cp.Set(v)
	f := cp.FieldByName("Facing")
	if !f.IsValid() || f.Type() != reflect.TypeOf(Direction(0)) {
		return s, false
	}
	f.SetUint(uint64(facing))
	nb, ok := cp.Interface().(Block)
	if !ok {
		return s, false
	}
	sid, ok := ToStateID[nb]
	return sid, ok
}

// DoorOpenableByHand ports BlockSetType.canOpenByHand(): DoorBlock/TrapDoorBlock.useWithoutItem
// return PASS when it is false. In 26.2 only the IRON BlockSetType is false (bytecode: IRON ctor
// iconst_0 for the canOpenByHand arg; COPPER + all wood are true). So iron_door/iron_trapdoor
// reject a hand click; everything else opens. A non-door state returns true.
// CITE: BlockSetType.IRON (canOpenByHand=false).
func DoorOpenableByHand(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return true
	}
	id := StateList[s].ID()
	return id != "minecraft:iron_door" && id != "minecraft:iron_trapdoor"
}

// --- SoundEvent registry ids (data/soundid/soundid.go, jar registries.json order) ------------
// Per-material door/trapdoor/fence-gate open/close SoundEvents, resolved by block ID prefix to
// match which SoundEvent each BlockSetType/WoodType was built with (static init, bytecode-verified).
const (
	sndWoodenDoorClose = 1845
	sndWoodenDoorOpen  = 1846
	sndWoodenTrapClose = 1847
	sndWoodenTrapOpen  = 1848

	sndCopperDoorClose = 411
	sndCopperDoorOpen  = 412
	sndCopperTrapClose = 442
	sndCopperTrapOpen  = 443

	sndIronDoorClose = 895
	sndIronDoorOpen  = 896
	sndIronTrapClose = 903
	sndIronTrapOpen  = 904

	sndCherryDoorClose = 337
	sndCherryDoorOpen  = 338
	sndCherryTrapClose = 339
	sndCherryTrapOpen  = 340

	sndNetherDoorClose = 1105
	sndNetherDoorOpen  = 1106
	sndNetherTrapClose = 1107
	sndNetherTrapOpen  = 1108

	sndBambooDoorClose = 130
	sndBambooDoorOpen  = 131
	sndBambooTrapClose = 132
	sndBambooTrapOpen  = 133

	sndFenceGateClose = 624
	sndFenceGateOpen  = 625

	sndCherryFenceGateClose = 345
	sndCherryFenceGateOpen  = 346

	sndNetherFenceGateClose = 1113
	sndNetherFenceGateOpen  = 1114

	sndBambooFenceGateClose = 138
	sndBambooFenceGateOpen  = 139
)

// DoorSoundID returns the SoundEvent id for a door's open/close, keyed by material.
// CITE: BlockSetType.doorOpen()/doorClose().
func DoorSoundID(s StateID, open bool) int32 {
	if int(s) < 0 || int(s) >= len(StateList) {
		return pick(open, sndWoodenDoorOpen, sndWoodenDoorClose)
	}
	id := StateList[s].ID()
	switch {
	case strings.HasPrefix(id, "minecraft:iron_"):
		return pick(open, sndIronDoorOpen, sndIronDoorClose)
	case strings.Contains(id, "copper"):
		return pick(open, sndCopperDoorOpen, sndCopperDoorClose)
	case strings.HasPrefix(id, "minecraft:cherry_"):
		return pick(open, sndCherryDoorOpen, sndCherryDoorClose)
	case strings.HasPrefix(id, "minecraft:crimson_"), strings.HasPrefix(id, "minecraft:warped_"):
		return pick(open, sndNetherDoorOpen, sndNetherDoorClose)
	case strings.HasPrefix(id, "minecraft:bamboo_"):
		return pick(open, sndBambooDoorOpen, sndBambooDoorClose)
	default:
		return pick(open, sndWoodenDoorOpen, sndWoodenDoorClose)
	}
}

// TrapdoorSoundID returns the SoundEvent id for a trapdoor's open/close, keyed by material.
// CITE: BlockSetType.trapdoorOpen()/trapdoorClose().
func TrapdoorSoundID(s StateID, open bool) int32 {
	if int(s) < 0 || int(s) >= len(StateList) {
		return pick(open, sndWoodenTrapOpen, sndWoodenTrapClose)
	}
	id := StateList[s].ID()
	switch {
	case strings.HasPrefix(id, "minecraft:iron_"):
		return pick(open, sndIronTrapOpen, sndIronTrapClose)
	case strings.Contains(id, "copper"):
		return pick(open, sndCopperTrapOpen, sndCopperTrapClose)
	case strings.HasPrefix(id, "minecraft:cherry_"):
		return pick(open, sndCherryTrapOpen, sndCherryTrapClose)
	case strings.HasPrefix(id, "minecraft:crimson_"), strings.HasPrefix(id, "minecraft:warped_"):
		return pick(open, sndNetherTrapOpen, sndNetherTrapClose)
	case strings.HasPrefix(id, "minecraft:bamboo_"):
		return pick(open, sndBambooTrapOpen, sndBambooTrapClose)
	default:
		return pick(open, sndWoodenTrapOpen, sndWoodenTrapClose)
	}
}

// FenceGateSoundID returns the SoundEvent id for a fence-gate's open/close, keyed by WoodType.
// CITE: WoodType.fenceGateOpen()/fenceGateClose().
func FenceGateSoundID(s StateID, open bool) int32 {
	if int(s) < 0 || int(s) >= len(StateList) {
		return pick(open, sndFenceGateOpen, sndFenceGateClose)
	}
	id := StateList[s].ID()
	switch {
	case strings.HasPrefix(id, "minecraft:cherry_"):
		return pick(open, sndCherryFenceGateOpen, sndCherryFenceGateClose)
	case strings.HasPrefix(id, "minecraft:crimson_"), strings.HasPrefix(id, "minecraft:warped_"):
		return pick(open, sndNetherFenceGateOpen, sndNetherFenceGateClose)
	case strings.HasPrefix(id, "minecraft:bamboo_"):
		return pick(open, sndBambooFenceGateOpen, sndBambooFenceGateClose)
	default:
		return pick(open, sndFenceGateOpen, sndFenceGateClose)
	}
}

func pick(open bool, o, c int32) int32 {
	if open {
		return o
	}
	return c
}

// --- reflective field helpers (mirroring isWaterlogged in utilfuncs.go) ----------------------

func hasBoolField(b Block, name string) bool {
	v := reflect.ValueOf(b)
	if v.Kind() != reflect.Struct {
		return false
	}
	f := v.FieldByName(name)
	return f.IsValid() && f.Kind() == reflect.Bool
}

func boolField(b Block, name string) bool {
	v := reflect.ValueOf(b)
	if v.Kind() != reflect.Struct {
		return false
	}
	f := v.FieldByName(name)
	if !f.IsValid() || f.Kind() != reflect.Bool {
		return false
	}
	return f.Bool()
}

// withBoolField returns a copy of b with the named Bool field set, or (b, false) if absent.
func withBoolField(b Block, name string, val bool) (Block, bool) {
	v := reflect.ValueOf(b)
	if v.Kind() != reflect.Struct {
		return b, false
	}
	cp := reflect.New(v.Type()).Elem()
	cp.Set(v)
	f := cp.FieldByName(name)
	if !f.IsValid() || f.Kind() != reflect.Bool {
		return b, false
	}
	f.SetBool(val)
	nb, ok := cp.Interface().(Block)
	return nb, ok
}

func hasDoubleBlockHalf(b Block) bool {
	v := reflect.ValueOf(b)
	if v.Kind() != reflect.Struct {
		return false
	}
	f := v.FieldByName("Half")
	return f.IsValid() && f.Type() == reflect.TypeOf(DoubleBlockHalf(0))
}
