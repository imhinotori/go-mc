package server

// sign.go - the SIGN block-entity + the sign edit protocol, ported 1:1 from the unobfuscated
// 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p this session). Placing a sign opens the client
// edit screen for the placer, the typed lines are stored on the sign block-entity (front/back
// SignText, 4 lines each, color, glowing, waxed lock), and the updated block-entity data is
// broadcast to trackers so every player sees the text.
//
// 1:1 ANCHORS (VERIFIED via javap this session):
//  SignItem.updateCustomBlockEntityTag -> SignBlock.openTextEdit(player, sign, true) on place.
//  ServerPlayer.openTextEdit(sign, isFront): send ClientboundBlockUpdate then ClientboundOpenSignEditor.
//  SignBlock.openTextEdit: sign.setAllowedPlayerEditor(player.getUUID()); player.openTextEdit(sign, isFront).
//  SignBlock.useWithoutItem: if isWaxed -> reject; else openTextEdit(player, sign, isFacingFrontText).
//  ServerGamePacketListenerImpl.handleSignUpdate -> updateSignText(player, isFront, lines).
//  SignBlockEntity.updateSignText: if isWaxed || !uuid.equals(playerWhoMayEdit) || level==null -> warn+return;
//      else setMessages; setAllowedPlayerEditor(null); level.sendBlockUpdated(pos, state, state, 3).
//  SignText.DIRECT_CODEC: { messages: Component[] (required), filtered_messages (opt), color: DyeColor
//      (opt, default BLACK), has_glowing_text: boolean (opt, default false) }. LINES = 4.
//  SignBlockEntity.saveAdditional: front_text (DIRECT_CODEC), back_text (DIRECT_CODEC), is_waxed (bool).
//  ServerboundSignUpdate wire: BlockPos pos, boolean isFrontText, 4x readUtf(384) lines.
//  ClientboundOpenSignEditor wire: BlockPos pos, boolean isFrontText.
//  ClientboundBlockEntityData wire: BlockPos pos, VarInt beType, NBT tag (saveCustomOnly).

import (
	"math/rand/v2"

	"github.com/google/uuid"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/nbt"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// signLines is the fixed number of text lines per side (SignText.LINES == 4).
const signLines = 4

// signMaxLineLength is ServerboundSignUpdatePacket's per-line cap (readUtf(384)). A longer line is
// truncated on decode - the server-authoritative bound (vanilla throws + disconnects; v1 truncates).
const signMaxLineLength = 384

// signDefaultColor is the SignText() default color DyeColor.BLACK (id 15). CITE SignText() ctor.
const signDefaultColor = 15 // DyeColor.BLACK

// signText is the Go analog of SignText: one side's 4 text lines plus color + glowing. Lines are
// plain literal strings (Component.literal(line)); an empty line is empty. CITE SignText.
type signText struct {
	messages [signLines]string
	color    int
	glowing  bool
}

// newSignText builds the SignText() default: 4 empty messages, BLACK, not glowing.
func newSignText() signText {
	return signText{color: signDefaultColor}
}

// signBE is the Go analog of SignBlockEntity: front + back SignText, the waxed lock, and the
// playerWhoMayEdit UUID (the editor allowed to submit the next ServerboundSignUpdate).
type signBE struct {
	front            signText
	back             signText
	waxed            bool
	playerWhoMayEdit uuid.UUID
	beType           block.EntityType
}

// newSignBE builds a freshly-placed sign: front + back default SignText, not waxed, no editor.
func newSignBE(beType block.EntityType) *signBE {
	return &signBE{front: newSignText(), back: newSignText(), beType: beType}
}

// text returns the front or back SignText by the isFrontText flag (SignBlockEntity.getText(bool)).
func (s *signBE) text(isFront bool) *signText {
	if isFront {
		return &s.front
	}
	return &s.back
}

// isSignBlock reports whether a block state is any standing/wall SignBlock. Reuses the generated
// SignEntity.IsValidBlock membership. Hanging signs are a separate block-entity (not handled here).
func isSignBlock(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	return block.SignEntity{}.IsValidBlock(block.StateList[s])
}

// signStateShape is the on-disk / on-wire SignText compound: {messages, color, has_glowing_text}.
// filtered_messages is omitted (v1 has no text filtering, so filteredOrEmpty == raw). Each message is a
// literal-text Component serialized via ComponentSerialization.CODEC, which for a text-only component is a
// bare NBT TAG_String (an EMPTY line is the empty string ""). Storing []string keeps the messages list
// HOMOGENEOUS TAG_String -- a []chat.Message would emit a mixed String/Compound list (an empty line
// marshals as a {} compound, a non-empty line as a bare string) that fails to round-trip. color is omitted
// when BLACK so the optionalAlwaysPresentFieldOf default reads back BLACK. CITE SignText.DIRECT_CODEC.
type signStateShape struct {
	Messages       []string `nbt:"messages"`
	Color          string   `nbt:"color,omitempty"`
	HasGlowingText bool     `nbt:"has_glowing_text,omitempty"`
}

// signBEShape is the SignBlockEntity save compound: {front_text, back_text, is_waxed}. Marshals as
// the bare BlockEntity.Data compound. CITE SignBlockEntity.saveAdditional.
type signBEShape struct {
	FrontText signStateShape `nbt:"front_text"`
	BackText  signStateShape `nbt:"back_text"`
	IsWaxed   bool           `nbt:"is_waxed,omitempty"`
}

// toShape converts a signText into its {messages, color, has_glowing_text} compound. Each line
// becomes a literal-text component; an empty line is an empty-string component (all lines the same
// TAG_String element type so the NBT list is homogeneous). CITE SignText.DIRECT_CODEC.
func (s signText) toShape() signStateShape {
	msgs := make([]string, signLines)
	for i := 0; i < signLines; i++ {
		msgs[i] = s.messages[i]
	}
	sh := signStateShape{Messages: msgs, HasGlowingText: s.glowing}
	if s.color != signDefaultColor {
		sh.Color = dyeColorName(byte(s.color))
	}
	return sh
}

// encodeSignBE builds the bare BlockEntity.Data compound for a signBE (saveAdditional's full field
// set). Strips the 3-byte nbt.Marshal root header so the result is the bare compound payload (the
// BlockEntity.Data convention, same as encodeFurnaceBE). CITE SignBlockEntity.saveAdditional.
func encodeSignBE(s *signBE) (nbt.RawMessage, error) {
	shape := signBEShape{
		FrontText: s.front.toShape(),
		BackText:  s.back.toShape(),
		IsWaxed:   s.waxed,
	}
	doc, err := nbt.Marshal(shape)
	if err != nil {
		return nbt.RawMessage{Type: nbt.TagCompound}, err
	}
	return nbt.RawMessage{Type: nbt.TagCompound, Data: doc[3:]}, nil
}

// signTextFromShape restores a signText from a compound. Lines beyond the decoded list stay empty;
// a missing color reads back BLACK. CITE SignText.load.
func signTextFromShape(sh signStateShape) signText {
	st := newSignText()
	for i := 0; i < signLines && i < len(sh.Messages); i++ {
		st.messages[i] = sh.Messages[i]
	}
	st.glowing = sh.HasGlowingText
	st.color = dyeColorIDFromName(sh.Color)
	return st
}

// dyeColorIDFromName inverts dyeColorName: a serialized DyeColor name -> its id, defaulting to BLACK
// for an empty/unknown name. CITE DyeColor.CODEC + optionalAlwaysPresentFieldOf(..., BLACK).
func dyeColorIDFromName(name string) int {
	if name == "" {
		return signDefaultColor
	}
	for i, n := range dyeColorNames {
		if n == name {
			return i
		}
	}
	return signDefaultColor
}

// decodeSignBE inverts encodeSignBE: decode a sign BlockEntity.Data compound into a fresh signBE
// (tolerant decode, never a panic). playerWhoMayEdit is NOT persisted (a transient edit lock).
// CITE SignBlockEntity.loadAdditional.
func decodeSignBE(data nbt.RawMessage, beType block.EntityType) *signBE {
	s := newSignBE(beType)
	var shape signBEShape
	if data.Type == nbt.TagCompound && len(data.Data) > 0 {
		_ = data.Unmarshal(&shape)
	}
	s.front = signTextFromShape(shape.FrontText)
	s.back = signTextFromShape(shape.BackText)
	s.waxed = shape.IsWaxed
	return s
}

// openSignEditor builds a ClientboundOpenSignEditor: BlockPos pos THEN Boolean isFrontText.
// CITE ClientboundOpenSignEditorPacket.write.
func openSignEditor(pos pk.Position, isFrontText bool) pk.Packet {
	return pk.Marshal(int32(packetid.ClientboundOpenSignEditor), pos, pk.Boolean(isFrontText))
}

// blockEntityData builds a ClientboundBlockEntityData: BlockPos pos, VarInt beType, NBT tag. The tag
// is getUpdateTag == saveCustomOnly == the saveAdditional compound. CITE ClientboundBlockEntityDataPacket.
func blockEntityData(pos pk.Position, beType block.EntityType, data nbt.RawMessage) pk.Packet {
	return pk.Marshal(int32(packetid.ClientboundBlockEntityData), pos, pk.VarInt(beType), pk.NBT(data))
}

// resolveSignBE returns the tick-owned signBE for pos, decoding it from the chunk's BlockEntity list
// on first access and caching it in t.signs. A player-placed sign with no recorded BE synthesizes an
// empty one. Returns nil only when pos is not a sign block (or the column is unloaded). Tick-owned.
func (t *TickLoop) resolveSignBE(pos pk.Position) *signBE {
	if t.signs == nil {
		t.signs = make(map[pk.Position]*signBE)
	}
	if s, ok := t.signs[pos]; ok {
		return s
	}
	if t.world() == nil {
		return nil
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !isSignBlock(state) {
		return nil
	}
	beType := block.EntityTypes["minecraft:sign"]
	col := level.ChunkPos{int32(pos.X >> 4), int32(pos.Z >> 4)}
	if ch, ok := t.world().Get(col); ok {
		lx, lz := pos.X&15, pos.Z&15
		for i := range ch.BlockEntity {
			be := ch.BlockEntity[i]
			bx, bz := be.UnpackXZ()
			if bx == lx && bz == lz && int(be.Y) == pos.Y && be.Type == beType {
				s := decodeSignBE(be.Data, beType)
				t.signs[pos] = s
				return s
			}
		}
	}
	s := newSignBE(beType)
	t.signs[pos] = s
	return s
}

// openSignForPlace ports SignItem.updateCustomBlockEntityTag's editor-open-on-place: mark the placer
// as the allowed editor and send ClientboundBlockUpdate + ClientboundOpenSignEditor(pos, true). Called
// from handleUseItemOn AFTER the successful place. CITE SignItem.updateCustomBlockEntityTag ->
// SignBlock.openTextEdit -> ServerPlayer.openTextEdit.
func (t *TickLoop) openSignForPlace(p *tickPlayer, pos pk.Position, state block.StateID) {
	if p == nil || p.client == nil || !isSignBlock(state) {
		return
	}
	s := t.resolveSignBE(pos)
	if s == nil {
		return
	}
	s.playerWhoMayEdit = p.uuid
	t.markSignDirty(pos)
	p.client.Send(blockUpdate(pos, state))
	p.client.Send(openSignEditor(pos, true))
}

// reopenSignEdit ports SignBlock.useWithoutItem 1:1 (the useWithoutItem step that runs AFTER useItemOn
// returns TRY_WITH_EMPTY_HAND): resolve the facing side (isFacingFrontText); executeClickCommandsIfPresent
// (a no-op for literal-text signs); if the sign is WAXED, play the WAXED_SIGN_INTERACT_FAIL sound and
// consume (SUCCESS_SERVER); else, if no other player is editing and the clicker may build and the side
// has editable (all-empty, i.e. not filtered-out) text, mark the clicker the allowed editor and open the
// edit screen (SUCCESS_SERVER). Otherwise PASS (returns false) -- but for our dispatch a sign always
// consumes the interaction, so we return true in the non-open cases too (no block is placed on a sign).
// CITE SignBlock.useWithoutItem.
func (t *TickLoop) reopenSignEdit(p *tickPlayer, pos pk.Position) bool {
	if p == nil || p.client == nil {
		return true
	}
	s := t.resolveSignBE(pos)
	if s == nil {
		return true
	}
	state := t.stateAt(pos)
	isFront := isFacingFrontText(p.x, p.z, pos, state)
	// executeClickCommandsIfPresent(level, player, pos, isFront): literal-text signs carry no click
	// events, so this is always false. CITE SignBlockEntity.executeClickCommandsIfPresent.
	if s.waxed {
		// isWaxed -> playSound(getSignInteractionFailedSoundEvent() == WAXED_SIGN_INTERACT_FAIL, BLOCKS)
		// then SUCCESS_SERVER. Level.playSound(entity, pos, sound, source) seeds with a fresh server draw
		// (soundSeedGenerator.nextLong analogue) at volume/pitch 1/1. CITE SignBlock.useWithoutItem.
		t.playSound(signWaxedInteractFailID, soundSourceBlocks,
			float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, 1.0, 1.0, rand.Int64())
		return true
	}
	// !otherPlayerIsEditingSign && player.mayBuild() && hasEditableText -> openTextEdit; else PASS.
	// hasEditableText == all of the side's messages are empty (getString().isEmpty()) -- an already-typed
	// side blocks re-open. v1 mayBuild() is always true (see applySignItem). CITE SignBlock.useWithoutItem.
	if !t.otherPlayerIsEditingSign(p, s) && signSideHasEditableText(s.text(isFront)) {
		s.playerWhoMayEdit = p.uuid
		t.markSignDirty(pos)
		p.client.Send(blockUpdate(pos, state)) // openTextEdit: ClientboundBlockUpdate(level, pos).
		p.client.Send(openSignEditor(pos, isFront))
	}
	return true
}

// signSideHasEditableText ports SignBlock.hasEditableText 1:1. The jar predicate is
// Arrays.stream(getMessages(filtered)).allMatch(c -> c.equals(CommonComponents.EMPTY) ||
// c.getContents() instanceof PlainTextContents) — a line is editable when it is EMPTY *or* its
// contents are PLAIN TEXT. This port models sign lines as plain Go strings (literal text only, never
// a command/click-event component), so every line's contents IS PlainTextContents and the predicate is
// UNCONDITIONALLY TRUE — an already-typed, unwaxed sign re-opens on a bare right-click, exactly as
// vanilla. (An earlier version wrongly gated on all-lines-empty, which refused to re-edit a written
// sign — a real regression.) CITE SignBlock.hasEditableText (lambda$hasEditableText$0).
func signSideHasEditableText(side *signText) bool {
	_ = side // every line is plain text -> allMatch(empty || PlainTextContents) is always true
	return true
}

// stateAt reads the block state at pos (or 0 when unloaded) - the ClientboundBlockUpdate re-sync the
// openTextEdit path sends. CITE openTextEdit's ClientboundBlockUpdatePacket(level, pos).
func (t *TickLoop) stateAt(pos pk.Position) block.StateID {
	if t.world() == nil {
		return 0
	}
	if s, ok := t.world().GetBlock(pos, dimMinY); ok {
		return s
	}
	return 0
}

// handleSignUpdate ports handleSignUpdate -> updateSignText: decode the ServerboundSignUpdate
// (BlockPos, isFrontText, 4 lines), resolve the sign BE, and apply the edit under the vanilla guards
// (not waxed AND the sender is the allowed editor). On a valid edit the 4 lines are stored, the edit
// lock is cleared, and the updated BE data is broadcast to every tracker. A malformed packet, an
// unloaded column, a non-sign target, a waxed sign, or a wrong editor is a silent no-op (vanilla logs
// a warn + returns). v1 has no chat filter (filterTextPacket identity), so raw lines are stored.
// CITE handleSignUpdate + SignBlockEntity.updateSignText.
func (t *TickLoop) handleSignUpdate(p *tickPlayer, pkt pk.Packet) {
	var pos pk.Position
	var isFrontText pk.Boolean
	var l0, l1, l2, l3 pk.String
	if err := pkt.Scan(&pos, &isFrontText, &l0, &l1, &l2, &l3); err != nil {
		return
	}
	if t.world() == nil {
		return
	}
	col := level.ChunkPos{int32(pos.X >> 4), int32(pos.Z >> 4)}
	if _, ok := t.world().Get(col); !ok {
		return
	}
	s := t.resolveSignBE(pos)
	if s == nil {
		return
	}
	if s.waxed || s.playerWhoMayEdit == uuid.Nil || s.playerWhoMayEdit != p.uuid {
		return
	}
	side := s.text(bool(isFrontText))
	lines := [signLines]string{
		truncateSignLine(string(l0)),
		truncateSignLine(string(l1)),
		truncateSignLine(string(l2)),
		truncateSignLine(string(l3)),
	}
	side.messages = lines
	s.playerWhoMayEdit = uuid.Nil
	t.markSignDirty(pos)
	t.broadcastSignUpdate(pos, s)
}

// truncateSignLine enforces the readUtf(384) per-line byte cap. A line at or under the cap is
// unchanged; an over-long line is truncated to the last whole rune within the byte bound.
func truncateSignLine(s string) string {
	if len(s) <= signMaxLineLength {
		return s
	}
	b := s[:signMaxLineLength]
	for len(b) > 0 && b[len(b)-1]&0xC0 == 0x80 {
		b = b[:len(b)-1]
	}
	return b
}

// broadcastSignUpdate encodes the sign BE's current data and sends a ClientboundBlockEntityData to
// every player tracking the sign's column. Also rewrites the sign's BE.Data in the chunk so a later
// chunk send / persist carries the text. CITE SignBlockEntity.getUpdatePacket + level.sendBlockUpdated.
func (t *TickLoop) broadcastSignUpdate(pos pk.Position, s *signBE) {
	data, err := encodeSignBE(s)
	if err != nil {
		return
	}
	if t.world() != nil {
		t.world().SetBlockEntityAt(pos, s.beType, data, dimMinY)
	}
	packet := blockEntityData(pos, s.beType, data)
	col := chunkCenterOf(int32(pos.X), int32(pos.Z))
	for _, pl := range t.players {
		if pl.client == nil {
			continue
		}
		if pl.center == col || (pl.sentChunks != nil && pl.sentChunks[col]) {
			pl.client.Send(packet)
		}
	}
}

// markSignDirty flags the column owning a sign as dirty so its text flushes on the next save pass
// (the sign twin of markChestDirty / markFurnaceDirty). A no-op when persistence is off.
func (t *TickLoop) markSignDirty(pos pk.Position) {
	if t.world() == nil {
		return
	}
	t.world().MarkDirty(level.ChunkPos{int32(pos.X >> 4), int32(pos.Z >> 4)})
}
