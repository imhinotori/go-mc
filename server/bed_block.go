package server

// bed_block.go — the BedBlock helpers for the SLEEP-01 subsystem: reading a bed block state's
// FACING / PART / OCCUPIED properties and rebuilding the state with OCCUPIED toggled, plus the
// BedBlock.useWithoutItem right-click port (useBed) that starts the player sleeping.
//
// A bed is 16 colored block structs (WhiteBed..BlackBed), each carrying {Facing Direction, Occupied
// Boolean, Part BedPart} (level/block/blocks.go). The vanilla BedBlock exposes them as the shared
// HorizontalDirectionalBlock.FACING, BedBlock.OCCUPIED, BedBlock.PART properties; here a type switch
// over the 16 colors reads/writes the same fields uniformly (there is no shared Go interface for the
// per-color structs, so the switch is the explicit mapping).
//
//	[VERIFIED javap BedBlock: FACING (HorizontalDirectionalBlock), OCCUPIED (BlockStateProperties.
//	 OCCUPIED, boolean), PART (BedPart HEAD/FOOT). BedBlock.useWithoutItem chain below.]

import (
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// bedProps is the (facing, part, occupied) triple read from a bed block state, with ok=false when the
// state is not a bed. It mirrors reading BedBlock.FACING / BedBlock.PART / BedBlock.OCCUPIED.
type bedProps struct {
	facing   block.Direction
	part     block.BedPart
	occupied bool
}

// readBed extracts a bed state's {facing, part, occupied}, or ok=false when s is not one of the 16 bed
// blocks. The type switch is the per-color mapping (no shared struct interface).
func readBed(s block.StateID) (bedProps, bool) {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return bedProps{}, false
	}
	switch b := block.StateList[s].(type) {
	case block.WhiteBed:
		return bedProps{b.Facing, b.Part, bool(b.Occupied)}, true
	case block.OrangeBed:
		return bedProps{b.Facing, b.Part, bool(b.Occupied)}, true
	case block.MagentaBed:
		return bedProps{b.Facing, b.Part, bool(b.Occupied)}, true
	case block.LightBlueBed:
		return bedProps{b.Facing, b.Part, bool(b.Occupied)}, true
	case block.YellowBed:
		return bedProps{b.Facing, b.Part, bool(b.Occupied)}, true
	case block.LimeBed:
		return bedProps{b.Facing, b.Part, bool(b.Occupied)}, true
	case block.PinkBed:
		return bedProps{b.Facing, b.Part, bool(b.Occupied)}, true
	case block.GrayBed:
		return bedProps{b.Facing, b.Part, bool(b.Occupied)}, true
	case block.LightGrayBed:
		return bedProps{b.Facing, b.Part, bool(b.Occupied)}, true
	case block.CyanBed:
		return bedProps{b.Facing, b.Part, bool(b.Occupied)}, true
	case block.PurpleBed:
		return bedProps{b.Facing, b.Part, bool(b.Occupied)}, true
	case block.BlueBed:
		return bedProps{b.Facing, b.Part, bool(b.Occupied)}, true
	case block.BrownBed:
		return bedProps{b.Facing, b.Part, bool(b.Occupied)}, true
	case block.GreenBed:
		return bedProps{b.Facing, b.Part, bool(b.Occupied)}, true
	case block.RedBed:
		return bedProps{b.Facing, b.Part, bool(b.Occupied)}, true
	case block.BlackBed:
		return bedProps{b.Facing, b.Part, bool(b.Occupied)}, true
	default:
		return bedProps{}, false
	}
}

// bedStateWithOccupied returns the state id of the SAME bed (color/facing/part preserved) with its
// OCCUPIED property set to occ, or ok=false when s is not a bed (or the modified state is somehow
// unregistered). It ports state.setValue(BedBlock.OCCUPIED, occ) -> the block's other state id. The
// per-color switch rebuilds the exact struct with Occupied flipped, then looks up block.ToStateID.
//
//	[VERIFIED javap LivingEntity.startSleeping/stopSleeping: setBlock(pos, state.setValue(OCCUPIED,b),3).]
func bedStateWithOccupied(s block.StateID, occ bool) (block.StateID, bool) {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return 0, false
	}
	o := block.Boolean(occ)
	var nb block.Block
	switch b := block.StateList[s].(type) {
	case block.WhiteBed:
		b.Occupied = o
		nb = b
	case block.OrangeBed:
		b.Occupied = o
		nb = b
	case block.MagentaBed:
		b.Occupied = o
		nb = b
	case block.LightBlueBed:
		b.Occupied = o
		nb = b
	case block.YellowBed:
		b.Occupied = o
		nb = b
	case block.LimeBed:
		b.Occupied = o
		nb = b
	case block.PinkBed:
		b.Occupied = o
		nb = b
	case block.GrayBed:
		b.Occupied = o
		nb = b
	case block.LightGrayBed:
		b.Occupied = o
		nb = b
	case block.CyanBed:
		b.Occupied = o
		nb = b
	case block.PurpleBed:
		b.Occupied = o
		nb = b
	case block.BlueBed:
		b.Occupied = o
		nb = b
	case block.BrownBed:
		b.Occupied = o
		nb = b
	case block.GreenBed:
		b.Occupied = o
		nb = b
	case block.RedBed:
		b.Occupied = o
		nb = b
	case block.BlackBed:
		b.Occupied = o
		nb = b
	default:
		return 0, false
	}
	sid, ok := block.ToStateID[nb]
	return sid, ok
}

// isBedBlock reports whether a state is any of the 16 bed blocks (BedBlock instanceof / the runtime
// #minecraft:beds membership). It delegates to blockInTag(s, "beds") so the bed set is the tag data
// (block_tags.go), the single source of truth — NOT a hard-coded 16-color list.
//
//	[VERIFIED javap BedBlock.useWithoutItem gate; BlockTags.BEDS == create("beds").]
func isBedBlock(s block.StateID) bool {
	return blockInTag(s, blockTagBeds)
}

// bedFacingDelta maps a bed FACING to the (dx,dz) step from FOOT to HEAD (BedBlock.useWithoutItem's
// `pos.relative(FACING)` when the clicked part is the FOOT). BedBlock places the HEAD one block in the
// FACING direction from the FOOT. Only the 4 horizontal directions occur on a bed.
//
//	[VERIFIED javap BedBlock.useWithoutItem: if PART != HEAD -> pos = pos.relative(state.getValue(FACING)).]
func bedFacingDelta(d block.Direction) (dx, dz int, ok bool) {
	switch d {
	case block.North:
		return 0, -1, true
	case block.South:
		return 0, 1, true
	case block.West:
		return -1, 0, true
	case block.East:
		return 1, 0, true
	default:
		return 0, 0, false
	}
}

// useBed ports the observable core of net.minecraft.world.level.block.BedBlock.useWithoutItem
// (temp/cache/26.2-inner.jar, CFR this session), the right-click that puts the player to sleep:
//
//	if (level.isClientSide) return SUCCESS_SERVER;                                  // server runs it
//	if (PART != HEAD && !(state = getBlockState(pos = pos.relative(FACING))).is(this)) return CONSUME;
//	BedRule rule = ...BED_RULE; if (rule.explodes()) { ...explode...; return SUCCESS_SERVER; }
//	if (OCCUPIED) { kickVillagerOutOfBed || "bed.occupied" overlay; return SUCCESS_SERVER; }
//	player.startSleepInBed(pos).ifLeft(problem -> overlay(problem.message()));      // the sleep
//	return SUCCESS_SERVER;
//
// Sulfur subset (all cited): the FOOT->HEAD hop resolves the bed pos exactly (pos.relative(FACING) when
// the clicked part is the FOOT); the explode branch (bed_explode.go) detonates a radius-5.0 fire BLOCK
// explosion in the nether/end (BedRule.EXPLODES) and returns before sleeping; the OCCUPIED branch
// consumes without sleeping (the villager-kick
// + overlay message need subsystems v1 lacks — a cited no-op-with-consume, matching vanilla returning
// SUCCESS_SERVER on an occupied bed); the BedSleepingProblem overlays are dropped (no overlay subsystem).
// The ServerPlayer.startSleepInBed pre-checks reduce to the canSleep (night) gate — bedInRange is
// approximated by useBlockInteraction's withinReach; the monster NOT_SAFE scan + bedBlocked +
// respawn-point set are cited follow-ups needing the respawn/obstruction subsystems. Returns true
// (the action is CONSUMED -> placement is skipped) whenever the clicked block is a bed.
//
//	[VERIFIED CFR BedBlock.useWithoutItem (above) + ServerPlayer.startSleepInBed gates.]
func (t *TickLoop) useBed(p *tickPlayer, pos pk.Position) bool {
	if t.world() == nil {
		return false
	}
	s, ok := t.world().GetBlock(pos, dimMinY)
	if !ok {
		return false
	}
	bp, ok := readBed(s)
	if !ok {
		return false // not a bed (defensive; the caller already gated on isBedBlock)
	}

	// PART != HEAD -> hop to the HEAD (pos.relative(FACING)); if the neighbor is not a bed, CONSUME.
	bedPos := pos
	if bp.part != block.BedPartHead {
		dx, dz, okDir := bedFacingDelta(bp.facing)
		if !okDir {
			return true // malformed facing: consume (no place), matching the CONSUME branch
		}
		bedPos = pk.Position{X: pos.X + dx, Y: pos.Y, Z: pos.Z + dz}
		hs, ok := t.world().GetBlock(bedPos, dimMinY)
		if !ok || !isBedBlock(hs) {
			return true // neighbor is not this bed -> CONSUME (no place)
		}
		var okHead bool
		bp, okHead = readBed(hs)
		if !okHead {
			return true
		}
	}

	// BedRule rule = environmentAttributes().getValue(BED_RULE, pos); if (rule.explodes()) { ... }.
	// The BED_RULE is dimension-driven (bed_explode.go bedRuleFor): overworld does NOT explode, the
	// nether + the end DO (EXPLODES). When it explodes, remove the bed + detonate a radius-5.0 fire
	// BLOCK explosion and return SUCCESS_SERVER -- the player never sleeps. bp.facing is the resolved
	// HEAD facing. CITE BedBlock.useWithoutItem explode branch.
	if bedRuleFor(p.dimension).explodes {
		t.bedExplode(bedPos, bp.facing)
		return true // SUCCESS_SERVER (action consumed; no sleep, no placement)
	}

	// OCCUPIED: consume without sleeping (the kick-villager + "bed.occupied" overlay are cited no-ops).
	if bp.occupied {
		return true
	}

	// player.startSleepInBed(pos).ifLeft(problem -> overlay(problem.message())): the full ServerPlayer
	// gate chain now lives in TickLoop.startSleepInBed (sleep.go) -- the BedSleepingProblem checks
	// (OTHER_PROBLEM, BedRule night gate, TOO_FAR_AWAY, OBSTRUCTED, the canSetSpawn respawn set,
	// NOT_SAFE monster scan) + super.startSleepInBed. The returned problem drives an overlay message in
	// vanilla; v1 has no overlay subsystem, so the result is dropped (a cited no-op) -- either way the
	// bed click is CONSUMED (return true: placement skipped). bp.facing is the resolved HEAD facing.
	_ = t.startSleepInBed(p, bedPos, bp.facing)
	return true
}
