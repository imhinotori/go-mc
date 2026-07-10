package block

// rail.go — the BaseRailBlock state helpers the minecart physics (server/minecart.go, the 1:1 port
// of net.minecraft.world.entity.vehicle.minecart.OldMinecartBehavior) reads to follow a track: the
// "is this a rail" gate (BaseRailBlock.isRail), the RailShape read (BaseRailBlock.getShapeProperty),
// the POWERED read (PoweredRailBlock.POWERED, shared by powered/detector/activator rails), and the
// POWERED write (re-encode a rail state with a flipped POWERED bit — the detector-rail on/off and the
// powered-rail boost toggle). Decompiled from temp/cache/26.2-inner.jar this session.
//
// The four BaseRailBlock subclasses in 26.2:
//   - Rail           (minecraft:rail)          — a plain rail, 10 RailShape values (incl. the curves).
//   - PoweredRail    (minecraft:powered_rail)  — POWERED boost/brake, straight+ascending shapes only.
//   - DetectorRail   (minecraft:detector_rail) — POWERED while a cart is on it (a redstone source).
//   - ActivatorRail  (minecraft:activator_rail)— POWERED activates the cart (TNT prime / eject).
// isRail == state.is(BlockTags.RAILS) && block instanceof BaseRailBlock — the four types above.
//	[VERIFIED CFR BaseRailBlock.isRail: state.is(BlockTags.RAILS) && getBlock() instanceof BaseRailBlock;
//	 RailBlock.getShapeProperty()==SHAPE (10 shapes), PoweredRailBlock.getShapeProperty()==SHAPE (6 shapes,
//	 straight+ascending only); PoweredRailBlock.POWERED shared by powered/detector/activator rails.]

// IsRail ports BaseRailBlock.isRail(BlockState): the state is one of the four BaseRailBlock types
// (Rail / PoweredRail / DetectorRail / ActivatorRail). This is the minecart onRails gate.
func IsRail(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	case Rail, PoweredRail, DetectorRail, ActivatorRail:
		return true
	}
	return false
}

// RailShapeOf returns the RailShape of a rail state (BaseRailBlock.getShapeProperty read), and true if
// the state is a rail. A non-rail state returns (RailShapeNorthSouth, false).
//	[VERIFIED CFR: RailBlock.SHAPE / PoweredRailBlock.SHAPE both back the RailShape state value.]
func RailShapeOf(s StateID) (RailShape, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return RailShapeNorthSouth, false
	}
	switch b := StateList[s].(type) {
	case Rail:
		return b.Shape, true
	case PoweredRail:
		return b.Shape, true
	case DetectorRail:
		return b.Shape, true
	case ActivatorRail:
		return b.Shape, true
	}
	return RailShapeNorthSouth, false
}

// IsPoweredRailBlock reports whether the state is a PoweredRail (minecraft:powered_rail) — the boost/
// brake rail OldMinecartBehavior.moveAlongTrack treats specially (`state.is(Blocks.POWERED_RAIL)`).
func IsPoweredRailBlock(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(PoweredRail)
	return ok
}

// IsPoweredRailFamily reports whether the state is one of the two PoweredRailBlock instances
// (powered_rail or activator_rail) — the rails whose POWERED bit PoweredRailBlock.updateState drives
// and whose runs findPoweredRailSignal walks. DetectorRail carries POWERED too but is a distinct block
// (DetectorRailBlock, not a PoweredRailBlock), so it is NOT part of a powered-rail run. This is the
// `state.is(this)`-family gate: both powered_rail and activator_rail ARE PoweredRailBlock in vanilla.
// CITE: ActivatorRailBlock == new PoweredRailBlock(...); PoweredRailBlock.isSameRailWithPower.
func IsPoweredRailFamily(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	switch StateList[s].(type) {
	case PoweredRail, ActivatorRail:
		return true
	}
	return false
}

// IsDetectorRailBlock reports whether the state is a DetectorRail (minecraft:detector_rail).
func IsDetectorRailBlock(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(DetectorRail)
	return ok
}

// IsActivatorRailBlock reports whether the state is an ActivatorRail (minecraft:activator_rail) — the
// rail OldMinecartBehavior.tick activates the cart on (`state.is(Blocks.ACTIVATOR_RAIL)` +
// activateMinecart(..., POWERED)).
func IsActivatorRailBlock(s StateID) bool {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false
	}
	_, ok := StateList[s].(ActivatorRail)
	return ok
}

// RailPowered returns the POWERED value of a powered/detector/activator rail (PoweredRailBlock.POWERED),
// and true if the state carries the POWERED property (a plain Rail has none → false, false). This is the
// `state.getValue(PoweredRailBlock.POWERED)` read the powered-rail boost + the detector-rail source use.
func RailPowered(s StateID) (bool, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return false, false
	}
	switch b := StateList[s].(type) {
	case PoweredRail:
		return bool(b.Powered), true
	case DetectorRail:
		return bool(b.Powered), true
	case ActivatorRail:
		return bool(b.Powered), true
	}
	return false, false
}

// SetRailPowered re-encodes a powered/detector/activator rail state with POWERED set to the given value,
// preserving its Shape + Waterlogged, and returns the new StateID (and true). A plain Rail (no POWERED)
// or a non-rail returns (s, false). This is the state write behind the detector-rail on/off toggle
// (DetectorRailBlock.updateState) and the powered-rail boost toggle (PoweredRailBlock.updatePowerState).
func SetRailPowered(s StateID, powered bool) (StateID, bool) {
	if int(s) < 0 || int(s) >= len(StateList) {
		return s, false
	}
	switch b := StateList[s].(type) {
	case PoweredRail:
		b.Powered = Boolean(powered)
		if sid, ok := ToStateID[b]; ok {
			return sid, true
		}
	case DetectorRail:
		b.Powered = Boolean(powered)
		if sid, ok := ToStateID[b]; ok {
			return sid, true
		}
	case ActivatorRail:
		b.Powered = Boolean(powered)
		if sid, ok := ToStateID[b]; ok {
			return sid, true
		}
	}
	return s, false
}
