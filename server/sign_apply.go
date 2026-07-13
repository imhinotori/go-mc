package server

// sign_apply.go - the SignApplicator half of a sign right-click (the useItemOn step that runs BEFORE
// the useWithoutItem edit-reopen), plus the isFacingFrontText geometry both steps share. Ported 1:1
// from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p this session).
//
// Right-clicking a sign with a dye colors that side's text, with an ink_sac clears glowing, with a
// glow_ink_sac makes it glow, and with a honeycomb waxes the whole sign (locking it). Each of these is
// a SignApplicator whose tryApplyToSign mutates the side's SignText (or the waxed flag) and plays the
// item's use sound; the interaction is then consumed and the item shrinks by one in survival.
//
// 1:1 ANCHORS (VERIFIED via javap this session):
//  SignBlock.useItemOn: be = getBlockEntity; applicator = (item instanceof SignApplicator);
//      bl = applicator != null && player.mayBuild(); on !ServerLevel return SUCCESS if (bl||waxed) else CONSUME;
//      if !bl || waxed || otherPlayerIsEditingSign -> TRY_WITH_EMPTY_HAND;
//      isFront = be.isFacingFrontText(player);
//      if applicator.canApplyToSign(be.getText(isFront), stack, player)
//         && applicator.tryApplyToSign(level, be, isFront, stack, player):
//           be.executeClickCommandsIfPresent(level, player, pos, isFront);  // (result ignored)
//           player.awardStat(ITEM_USED); level.gameEvent(BLOCK_CHANGE); stack.consume(1, player);
//           return SUCCESS;
//      else return TRY_WITH_EMPTY_HAND.
//  SignApplicator.canApplyToSign (default): signText.hasMessage(player)  (any of the 4 lines non-empty).
//  HoneycombItem.canApplyToSign: return true (override).
//  DyeItem.tryApplyToSign: color = stack.get(DataComponents.DYE); if color!=null &&
//      be.updateText(t -> t.setColor(color), isFront): playSound(DYE_USE, BLOCKS,1,1); return true; else false.
//  InkSacItem.tryApplyToSign: be.updateText(t -> t.setHasGlowingText(false), isFront) -> playSound(INK_SAC_USE); ...
//  GlowInkSacItem.tryApplyToSign: be.updateText(t -> t.setHasGlowingText(true), isFront) -> playSound(GLOW_INK_SAC_USE); ...
//  HoneycombItem.tryApplyToSign: be.setWaxed(true) -> levelEvent(3003) (client wax particle); return true.
//  SignText.setColor/setHasGlowingText: return this when the value is unchanged, else a NEW SignText;
//      SignBlockEntity.setFrontText/setBackText compares by reference, so updateText returns true ONLY
//      when the value actually changed (this gates the sound + item consume).
//  SignBlockEntity.isFacingFrontText(player): only for a SignBlock; hitCtr = getSignHitboxCenterPosition
//      == (0.5,0.5,0.5); dx = player.getX() - (blockX + hitCtr.x); dz = player.getZ() - (blockZ + hitCtr.z);
//      f = getYRotationDegrees(state); f1 = (float)(Mth.atan2(dz, dx)*57.2957763671875) - 90.0;
//      return Mth.degreesDifferenceAbs(f, f1) <= 90.0.
//  StandingSignBlock.getYRotationDegrees: RotationSegment.convertToDegrees(ROTATION) = ROTATION * 22.5.
//  WallSignBlock.getYRotationDegrees:    FACING.toYRot() (SOUTH 0, WEST 90, NORTH 180, EAST 270).

import (
	"math/rand/v2"
	"reflect"

	"github.com/google/uuid"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// Sign-interaction sound registry ids (data/soundid/soundid.go), all played on SoundSource.BLOCKS.
// CITE SoundEvents.DYE_USE / INK_SAC_USE / GLOW_INK_SAC_USE / SIGN_WAXED_INTERACT_FAIL.
const (
	signDyeUseSoundID        int32 = 571  // item.dye.use
	signGlowInkSacUseSoundID int32 = 725  // item.glow_ink_sac.use
	signInkSacUseSoundID     int32 = 889  // item.ink_sac.use
	signWaxedInteractFailID  int32 = 1756 // block.sign.waxed_interact_fail
)

// mthDegToRad57 is Mth.atan2's degrees factor 180/pi as the jar's exact double literal 57.2957763671875
// (NOT math.Pi-derived) so the float cast in isFacingFrontText reproduces vanilla bit-for-bit.
// CITE SignBlockEntity.isFacingFrontText (ldc2_w 57.2957763671875d).
const mthDegToRad57 = 57.2957763671875

// signYRotationDegrees ports getYRotationDegrees for a sign StateID: a standing sign is ROTATION*22.5,
// a wall sign is FACING.toYRot(). Reads the generated block struct's Rotation/Facing field by reflection
// (the same value the property carries). Returns 0 for a non-sign / unreadable state (never reached for a
// real sign). CITE StandingSignBlock/WallSignBlock.getYRotationDegrees + RotationSegment.convertToDegrees.
func signYRotationDegrees(s block.StateID) float32 {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return 0
	}
	v := reflect.ValueOf(block.StateList[s])
	if v.Kind() != reflect.Struct {
		return 0
	}
	if f := v.FieldByName("Rotation"); f.IsValid() && f.CanInt() {
		// SegmentedAnglePrecision(16).toDegrees(rotation) = rotation * (360/16) = rotation * 22.5f.
		return float32(f.Int()) * 22.5
	}
	if f := v.FieldByName("Facing"); f.IsValid() && f.CanUint() {
		return directionToYRot(block.Direction(f.Uint()))
	}
	return 0
}

// directionToYRot ports Direction.toYRot(): SOUTH 0, WEST 90, NORTH 180, EAST 270 (a horizontal facing;
// a non-horizontal facing yields -1 in vanilla, unreachable for a wall sign). CITE Direction.toYRot.
func directionToYRot(d block.Direction) float32 {
	switch d {
	case block.South:
		return 0
	case block.West:
		return 90
	case block.North:
		return 180
	case block.East:
		return 270
	}
	return -1
}

// isFacingFrontText ports SignBlockEntity.isFacingFrontText(player) 1:1: with the sign-hitbox center at
// (0.5,0.5,0.5), take the player-to-center delta in X/Z, convert atan2(dz,dx) to degrees (minus 90), and
// return whether that bearing is within 90 degrees of the sign's own facing. A player in front of the
// sign edits the FRONT text; behind, the BACK. CITE SignBlockEntity.isFacingFrontText.
func isFacingFrontText(px, pz float64, pos pk.Position, state block.StateID) bool {
	d0 := px - (float64(pos.X) + 0.5)
	d1 := pz - (float64(pos.Z) + 0.5)
	f := signYRotationDegrees(state)
	f1 := float32(mthAtan2(d1, d0)*mthDegToRad57) - 90.0
	return mthDegreesDifferenceAbs(f, f1) <= 90.0
}

// signApplicatorKind classifies a held item as a SignApplicator (dye / ink_sac / glow_ink_sac / honeycomb).
// CITE the item instanceof SignApplicator test in SignBlock.useItemOn.
type signApplicatorKind int

const (
	signApplNone signApplicatorKind = iota
	signApplDye
	signApplInkSac
	signApplGlowInkSac
	signApplHoneycomb
)

// classifySignApplicator resolves a held item to its SignApplicator kind and (for a dye) its DyeColor id.
// Returns (signApplNone, 0) for any non-applicator item. CITE SignBlock.useItemOn (instanceof SignApplicator).
func classifySignApplicator(held component.SlotData) (signApplicatorKind, int) {
	if slotIsEmpty(held) {
		return signApplNone, 0
	}
	id := int32(held.ItemID)
	if col, isDye := dyeColorIDOf(id); isDye {
		return signApplDye, col
	}
	switch id {
	case int32(item.InkSac.ID):
		return signApplInkSac, 0
	case int32(item.GlowInkSac.ID):
		return signApplGlowInkSac, 0
	case int32(item.Honeycomb.ID):
		return signApplHoneycomb, 0
	}
	return signApplNone, 0
}

// canApplyToSign ports the SignApplicator gate for the resolved kind. Honeycomb overrides to always-true
// (HoneycombItem.canApplyToSign); the rest use the default SignApplicator.canApplyToSign ==
// signText.hasMessage(player) (at least one of the side's 4 lines is non-empty). v1 has no text filtering,
// so isTextFilteringEnabled() is false and getMessages(false) is the raw messages. CITE SignApplicator.
func canApplyToSign(kind signApplicatorKind, side *signText) bool {
	if kind == signApplHoneycomb {
		return true
	}
	return signTextHasMessage(side)
}

// signTextHasMessage ports SignText.hasMessage(player): any of the 4 messages has a non-empty string.
// CITE SignText.hasMessage (anyMatch !getString().isEmpty()).
func signTextHasMessage(side *signText) bool {
	for i := 0; i < signLines; i++ {
		if side.messages[i] != "" {
			return true
		}
	}
	return false
}

// applySignItem ports SignBlock.useItemOn's applicator branch: for a valid applicator held on a sign the
// player may build, resolve the facing side, run canApplyToSign + tryApplyToSign, and on success play the
// item sound, execute click-commands (a no-op for literal-text signs), broadcast the updated block-entity,
// and consume one item (survival). Returns true when the interaction was CONSUMED (SUCCESS -> no reopen,
// no placement); false when it should fall through to the useWithoutItem edit-reopen (TRY_WITH_EMPTY_HAND).
// CITE SignBlock.useItemOn.
func (t *TickLoop) applySignItem(p *tickPlayer, pos pk.Position, state block.StateID, s *signBE, kind signApplicatorKind, dyeColor int) bool {
	// bl = applicator != null && player.mayBuild(); the applicator is non-nil here (kind != none). v1
	// mayBuild() is the creative/survival build permission, always true for a normal player (adventure
	// build restrictions are enforced upstream at the interaction gate). Structured so a real mayBuild
	// read gates this later.
	// if (!bl || be.isWaxed() || otherPlayerIsEditingSign(player, be)) return TRY_WITH_EMPTY_HAND.
	if s.waxed || t.otherPlayerIsEditingSign(p, s) {
		return false
	}
	isFront := isFacingFrontText(p.x, p.z, pos, state)
	side := s.text(isFront)
	if !canApplyToSign(kind, side) {
		return false // TRY_WITH_EMPTY_HAND
	}
	if !t.tryApplyToSign(kind, dyeColor, pos, s, side) {
		return false // TRY_WITH_EMPTY_HAND
	}
	// executeClickCommandsIfPresent: literal-text signs carry no click-events, so this is always false
	// and its result is discarded by vanilla anyway. CITE SignBlockEntity.executeClickCommandsIfPresent.
	// player.awardStat(ITEM_USED): cited no-op (stats emit seam, mirrors composter).
	// level.gameEvent(BLOCK_CHANGE): cited no-op (no BE gameEvent seam in v1, mirrors bell_be.go).
	t.markSignDirty(pos)
	t.broadcastSignUpdate(pos, s)
	if p.gameMode != gameModeCreative {
		t.shrinkHeldItem(p, ensureInventory(p)) // stack.consume(1, player): survival shrinks; creative keeps.
	}
	return true
}

// tryApplyToSign dispatches to the resolved applicator's tryApplyToSign, mutating the side's SignText (or
// the waxed flag) and playing the item sound on a real change. Returns whether the sign changed (the
// SUCCESS gate). CITE DyeItem/InkSacItem/GlowInkSacItem/HoneycombItem.tryApplyToSign.
func (t *TickLoop) tryApplyToSign(kind signApplicatorKind, dyeColor int, pos pk.Position, s *signBE, side *signText) bool {
	cx := float64(pos.X) + 0.5
	cy := float64(pos.Y) + 0.5
	cz := float64(pos.Z) + 0.5
	switch kind {
	case signApplDye:
		// updateText(t -> t.setColor(color)): changed only when the color differs.
		if side.color == dyeColor {
			return false // setColor returned this -> setFrontText false -> no change.
		}
		side.color = dyeColor
		t.playSound(signDyeUseSoundID, soundSourceBlocks, cx, cy, cz, 1.0, 1.0, rand.Int64())
		return true
	case signApplInkSac:
		// updateText(t -> t.setHasGlowingText(false)): changed only when currently glowing.
		if !side.glowing {
			return false
		}
		side.glowing = false
		t.playSound(signInkSacUseSoundID, soundSourceBlocks, cx, cy, cz, 1.0, 1.0, rand.Int64())
		return true
	case signApplGlowInkSac:
		// updateText(t -> t.setHasGlowingText(true)): changed only when not currently glowing.
		if side.glowing {
			return false
		}
		side.glowing = true
		t.playSound(signGlowInkSacUseSoundID, soundSourceBlocks, cx, cy, cz, 1.0, 1.0, rand.Int64())
		return true
	case signApplHoneycomb:
		// setWaxed(true): changed only when not already waxed. levelEvent(3003) is the client wax
		// particle (item.honeycomb.wax_on) -- a cited no-op (mirrors the composter fill levelEvent).
		if s.waxed {
			return false
		}
		s.waxed = true
		return true
	}
	return false
}

// otherPlayerIsEditingSign ports SignBlock.otherPlayerIsEditingSign(player, be): the sign has a
// playerWhoMayEdit set that is a DIFFERENT player from the interactor. A sign whose edit lock is unset,
// or held by this same player, is not "someone else editing". CITE SignBlock.otherPlayerIsEditingSign.
func (t *TickLoop) otherPlayerIsEditingSign(p *tickPlayer, s *signBE) bool {
	editor := s.playerWhoMayEdit
	return editor != uuid.Nil && editor != p.uuid
}
