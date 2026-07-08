package block

// sculk.go - block-package accessors for the SCULK family (Sculk, SculkVein, SculkCatalyst,
// SculkSensor, CalibratedSculkSensor, SculkShrieker). 1:1 with the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar). The live behavior (spread/charge, the vibration listener phase
// machine, the shrieker warning-level machine) lives in server/sculk_*.go; this file is only the
// StateList type-switch read/write layer, mirroring level/block/redstone_blocks.go.
//
// PROPERTY NAMING NOTE (jar-verified): SculkCatalystBlock's blockstate property is named `bloom`
// on the wire / in the codegen struct (SculkCatalyst.Bloom), but the Java field it is assigned to
// is BlockStateProperties.BLOOM aliased as SculkCatalystBlock.PULSE. So Go `Bloom` == Java `PULSE`
// (the 8-tick bloom-animation pulse the CatalystListener sets on a nearby mob death). CITE:
// SculkCatalystBlock static{} `PULSE = BlockStateProperties.BLOOM`.

// ---- Sculk ----

func IsSculk(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(Sculk)
	return ok
}

// ---- SculkVein ----

func IsSculkVein(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(SculkVein)
	return ok
}

// ---- SculkCatalyst ----

func IsSculkCatalyst(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(SculkCatalyst)
	return ok
}

// SculkCatalystBloom reads the PULSE (codegen `bloom`) property. CITE: SculkCatalystBlock.PULSE.
func SculkCatalystBloom(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	if b, ok := StateList[s].(SculkCatalyst); ok {
		return bool(b.Bloom)
	}
	return false
}

// SculkCatalystWithBloom sets PULSE (bloom). CITE: SculkCatalystBlockEntity.CatalystListener.bloom
// (setValue(PULSE, true)) + SculkCatalystBlock.tick (setValue(PULSE, false)).
func SculkCatalystWithBloom(s StateID, bloom bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(SculkCatalyst); ok {
		b.Bloom = Boolean(bloom)
		return lookup(b)
	}
	return s, false
}

// ---- SculkSensor ----

func IsSculkSensor(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(SculkSensor)
	return ok
}

func IsCalibratedSculkSensor(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(CalibratedSculkSensor)
	return ok
}

// IsAnySculkSensor reports getBlock() instanceof SculkSensorBlock (CalibratedSculkSensorBlock
// extends SculkSensorBlock). The redstone/signal + tick paths treat both as the same block class.
func IsAnySculkSensor(s StateID) bool {
	return IsSculkSensor(s) || IsCalibratedSculkSensor(s)
}

// SculkSensorPower reads the POWER property (0..15) for either sensor variant. CITE: SculkSensorBlock
// POWER (the redstone strength out all faces while ACTIVE).
func SculkSensorPower(s StateID) int {
	if int(s) < 0 || int(s) >= len(StateList) {
		return -1
	}
	switch b := StateList[s].(type) {
	case SculkSensor:
		return int(b.Power)
	case CalibratedSculkSensor:
		return int(b.Power)
	}
	return -1
}

// SculkSensorPhaseOf reads the PHASE property for either sensor variant. CITE: SculkSensorBlock.getPhase.
func SculkSensorPhaseOf(s StateID) (SculkSensorPhase, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return 0, false
	}
	switch b := StateList[s].(type) {
	case SculkSensor:
		return b.SculkSensorPhase, true
	case CalibratedSculkSensor:
		return b.SculkSensorPhase, true
	}
	return 0, false
}

// SculkSensorWaterlogged reads WATERLOGGED for either sensor variant.
func SculkSensorWaterlogged(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch b := StateList[s].(type) {
	case SculkSensor:
		return bool(b.Waterlogged)
	case CalibratedSculkSensor:
		return bool(b.Waterlogged)
	}
	return false
}

// CalibratedSculkSensorFacing reads FACING for the calibrated sensor. CITE: CalibratedSculkSensorBlock.FACING.
func CalibratedSculkSensorFacing(s StateID) (Direction, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return 0, false
	}
	if b, ok := StateList[s].(CalibratedSculkSensor); ok {
		return b.Facing, true
	}
	return 0, false
}

// SculkSensorWithPhaseAndPower sets PHASE + POWER together for either sensor variant, preserving
// FACING/WATERLOGGED. This is the write both SculkSensorBlock.activate (ACTIVE, power) and
// .deactivate (COOLDOWN, 0) / .tick (INACTIVE) perform. CITE: SculkSensorBlock.activate/deactivate/tick.
func SculkSensorWithPhaseAndPower(s StateID, phase SculkSensorPhase, power int) (StateID, bool) {
	if power < 0 || power > 15 {
		return s, false
	}
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	switch b := StateList[s].(type) {
	case SculkSensor:
		b.SculkSensorPhase = phase
		b.Power = Integer(power)
		return lookup(b)
	case CalibratedSculkSensor:
		b.SculkSensorPhase = phase
		b.Power = Integer(power)
		return lookup(b)
	}
	return s, false
}

// ---- SculkShrieker ----

func IsSculkShrieker(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(SculkShrieker)
	return ok
}

// SculkShriekerShrieking reads SHRIEKING. CITE: SculkShriekerBlock.SHRIEKING.
func SculkShriekerShrieking(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	if b, ok := StateList[s].(SculkShrieker); ok {
		return bool(b.Shrieking)
	}
	return false
}

// SculkShriekerCanSummon reads CAN_SUMMON. CITE: SculkShriekerBlock.CAN_SUMMON.
func SculkShriekerCanSummon(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	if b, ok := StateList[s].(SculkShrieker); ok {
		return bool(b.CanSummon)
	}
	return false
}

// SculkShriekerWithShrieking sets SHRIEKING, preserving CAN_SUMMON/WATERLOGGED. CITE:
// SculkShriekerBlockEntity.shriek (setValue(SHRIEKING, true)) + SculkShriekerBlock.tick (false).
func SculkShriekerWithShrieking(s StateID, shrieking bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	if b, ok := StateList[s].(SculkShrieker); ok {
		b.Shrieking = Boolean(shrieking)
		return lookup(b)
	}
	return s, false
}
