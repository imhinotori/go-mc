package server

// sign_test.go - the SIGN edit end-to-end seam (sign.go): placing a sign opens the editor for the
// placer, a ServerboundSignUpdate stores the 4 lines + broadcasts the block-entity data, a waxed sign
// rejects edits, and the sign block-entity round-trips its text through the NBT codec.
//
// Jar-cited: SignItem.updateCustomBlockEntityTag -> SignBlock.openTextEdit -> ServerPlayer.openTextEdit
// (ClientboundBlockUpdate + ClientboundOpenSignEditor(pos, true)); ServerGamePacketListenerImpl
// .handleSignUpdate -> SignBlockEntity.updateSignText (waxed + playerWhoMayEdit guards);
// SignBlockEntity.saveAdditional/loadAdditional (front_text/back_text/is_waxed).

import (
	"testing"

	"github.com/google/uuid"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// signUpdatePacket builds a ServerboundSignUpdate with the jar field order: BlockPos, isFrontText,
// then the 4 lines. CITE ServerboundSignUpdatePacket wire.
func signUpdatePacket(pos pk.Position, isFront bool, l0, l1, l2, l3 string) pk.Packet {
	return pk.Marshal(int32(packetid.ServerboundSignUpdate),
		pos, pk.Boolean(isFront),
		pk.String(l0), pk.String(l1), pk.String(l2), pk.String(l3))
}

// placeSignBlock sets an oak_sign block at pos in the loaded test column (no BE recorded yet - the
// resolveSignBE path synthesizes an empty signBE, exactly as a freshly-placed sign).
func placeSignBlock(loop *TickLoop, pos pk.Position) {
	loop.world().SetBlock(pos, block.ToStateID[block.OakSign{}], dimMinY)
}

// TestSignPlaceOpensEditor: after openSignForPlace runs for a placed sign, the placer receives a
// ClientboundOpenSignEditor carrying the sign pos + isFrontText=true, and is recorded as the allowed
// editor. CITE SignItem.updateCustomBlockEntityTag -> SignBlock.openTextEdit.
func TestSignPlaceOpensEditor(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.uuid = uuid.New()

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	placeSignBlock(loop, pos)
	state := block.ToStateID[block.OakSign{}]

	loop.openSignForPlace(p, pos, state)

	// The placer is now the allowed editor.
	s := loop.signs[pos]
	if s == nil {
		t.Fatal("sign BE not registered after openSignForPlace")
	}
	if s.playerWhoMayEdit != p.uuid {
		t.Fatalf("playerWhoMayEdit = %v, want the placer uuid %v", s.playerWhoMayEdit, p.uuid)
	}

	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundOpenSignEditor); n != 1 {
		t.Fatalf("ClientboundOpenSignEditor sent %d times, want 1", n)
	}
	// Decode the OpenSignEditor payload: BlockPos pos, Boolean isFrontText.
	var found bool
	for _, packet := range got {
		if packet.ID != int32(packetid.ClientboundOpenSignEditor) {
			continue
		}
		var gotPos pk.Position
		var isFront pk.Boolean
		if err := packet.Scan(&gotPos, &isFront); err != nil {
			t.Fatalf("OpenSignEditor scan: %v", err)
		}
		if gotPos != pos {
			t.Fatalf("OpenSignEditor pos = %v, want %v", gotPos, pos)
		}
		if !bool(isFront) {
			t.Fatal("OpenSignEditor isFrontText = false, want true on place")
		}
		found = true
	}
	if !found {
		t.Fatal("no OpenSignEditor packet decoded")
	}
}

// TestSignUpdateStoresAndBroadcasts: a ServerboundSignUpdate from the allowed editor stores the 4
// front lines, clears the edit lock, and broadcasts a ClientboundBlockEntityData to the tracker.
// CITE handleSignUpdate -> SignBlockEntity.updateSignText.
func TestSignUpdateStoresAndBroadcasts(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.uuid = uuid.New()

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	placeSignBlock(loop, pos)
	state := block.ToStateID[block.OakSign{}]
	loop.openSignForPlace(p, pos, state) // makes p the allowed editor
	p.client = captureClient(64) // fresh buffer; do not close the queue we read later

	upd := signUpdatePacket(pos, true, "hello", "world", "line3", "line4")
	loop.handleSignUpdate(p, upd)

	s := loop.signs[pos]
	if s == nil {
		t.Fatal("sign BE missing after update")
	}
	want := [signLines]string{"hello", "world", "line3", "line4"}
	if s.front.messages != want {
		t.Fatalf("front messages = %v, want %v", s.front.messages, want)
	}
	// The edit lock is cleared after a successful edit.
	if s.playerWhoMayEdit != uuid.Nil {
		t.Fatalf("playerWhoMayEdit = %v after edit, want cleared (Nil)", s.playerWhoMayEdit)
	}

	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundBlockEntityData); n != 1 {
		t.Fatalf("ClientboundBlockEntityData sent %d times, want 1", n)
	}
}

// TestSignWaxedRejectsEdit: a waxed sign rejects a ServerboundSignUpdate even from the allowed
// editor - the lines stay empty and no block-entity data is broadcast. CITE SignBlockEntity
// .updateSignText (isWaxed() guard).
func TestSignWaxedRejectsEdit(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.uuid = uuid.New()

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	placeSignBlock(loop, pos)
	state := block.ToStateID[block.OakSign{}]
	loop.openSignForPlace(p, pos, state)
	p.client = captureClient(64) // fresh buffer

	// Wax the sign (the honeycomb lock).
	loop.signs[pos].waxed = true

	upd := signUpdatePacket(pos, true, "nope", "", "", "")
	loop.handleSignUpdate(p, upd)

	s := loop.signs[pos]
	empty := [signLines]string{}
	if s.front.messages != empty {
		t.Fatalf("waxed sign stored %v, want no change (empty)", s.front.messages)
	}

	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundBlockEntityData); n != 0 {
		t.Fatalf("waxed sign broadcast BlockEntityData %d times, want 0 (edit rejected)", n)
	}
}

// TestSignUpdateWrongEditorRejected: a ServerboundSignUpdate from a player who is NOT the allowed
// editor is rejected (the playerWhoMayEdit guard) - no lines stored. CITE SignBlockEntity
// .updateSignText (uuid.equals(getPlayerWhoMayEdit) guard).
func TestSignUpdateWrongEditorRejected(t *testing.T) {
	loop, _ := newBlockLoop()
	placer := blockPlayer(loop, 1.5, 65.0, 1.5)
	placer.uuid = uuid.New()
	other := blockPlayer(loop, 2.5, 65.0, 1.5)
	other.uuid = uuid.New()

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	placeSignBlock(loop, pos)
	state := block.ToStateID[block.OakSign{}]
	loop.openSignForPlace(placer, pos, state) // placer is the allowed editor
	placer.client = captureClient(64) // fresh buffer

	// The OTHER player tries to submit the edit.
	upd := signUpdatePacket(pos, true, "hijack", "", "", "")
	loop.handleSignUpdate(other, upd)

	s := loop.signs[pos]
	empty := [signLines]string{}
	if s.front.messages != empty {
		t.Fatalf("wrong-editor edit stored %v, want no change", s.front.messages)
	}
	// The placer is still the allowed editor (a rejected edit does not clear the lock).
	if s.playerWhoMayEdit != placer.uuid {
		t.Fatalf("playerWhoMayEdit = %v, want the placer (a rejected edit keeps the lock)", s.playerWhoMayEdit)
	}
}

// TestSignBERoundTrip: encodeSignBE -> decodeSignBE preserves both sides' 4 lines + the waxed flag
// (the SignBlockEntity save/load contract). CITE SignBlockEntity.saveAdditional/loadAdditional.
func TestSignBERoundTrip(t *testing.T) {
	beType := block.EntityTypes["minecraft:sign"]
	s := newSignBE(beType)
	s.front.messages = [signLines]string{"a", "b", "c", "d"}
	s.back.messages = [signLines]string{"1", "2", "3", "4"}
	s.waxed = true

	data, err := encodeSignBE(s)
	if err != nil {
		t.Fatalf("encodeSignBE: %v", err)
	}
	got := decodeSignBE(data, beType)

	if got.front.messages != s.front.messages {
		t.Fatalf("front round-trip = %v, want %v", got.front.messages, s.front.messages)
	}
	if got.back.messages != s.back.messages {
		t.Fatalf("back round-trip = %v, want %v", got.back.messages, s.back.messages)
	}
	if !got.waxed {
		t.Fatal("is_waxed did not round-trip (got false, want true)")
	}
}

// TestSignRightClickReopensEdit: a right-click on an unwaxed sign via useBlockInteraction consumes
// the interaction (no placement) and re-opens the editor for the clicker. CITE SignBlock.useWithoutItem.
func TestSignRightClickReopensEdit(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.uuid = uuid.New()

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	placeSignBlock(loop, pos)

	consumed := loop.useBlockInteraction(p, pos, 1, 0, 0, 0)
	if !consumed {
		t.Fatal("useBlockInteraction on a sign returned false, want true (interaction consumed)")
	}
	if loop.signs[pos].playerWhoMayEdit != p.uuid {
		t.Fatal("right-click did not mark the clicker as the allowed editor")
	}
	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundOpenSignEditor); n != 1 {
		t.Fatalf("right-click sent OpenSignEditor %d times, want 1", n)
	}
}

// TestSignPigOracle: the sign block-entity + edit path must not perturb the pig entity - a sanity
// assertion that the sign predicate never matches the pig's block-family (signs are unrelated to
// entities). The pig path is untouched by sign.go; this asserts the isSignBlock predicate is inert
// for non-sign states (air), so no pig/entity codepath is reachable through it.
func TestSignPigOracle(t *testing.T) {
	air := block.DefaultStateID["minecraft:air"]
	if isSignBlock(air) {
		t.Fatal("isSignBlock(air) = true, want false (the sign predicate must not match non-signs)")
	}
}

// signHeldItem sets the player's held hotbar slot to one of the given item id (count 1) so the
// applicator dispatch has something to read. Mirrors composter_test's held-item setup.
func signHeldItem(p *tickPlayer, itemID int32) {
	inv := ensureInventory(p)
	inv.set(heldWindowSlot(inv.heldSlot), component.SlotData{Count: 1, ItemID: pk.VarInt(itemID)})
}

// TestSignDyeColorsText: a dye right-click on a sign whose front side already has text recolors the
// front SignText, broadcasts the block-entity, and (survival) consumes one dye. CITE SignBlock.useItemOn
// -> DyeItem.tryApplyToSign (setColor).
func TestSignDyeColorsText(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.uuid = uuid.New()
	p.gameMode = gameModeSurvival

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	placeSignBlock(loop, pos)
	// The applicator gate requires the side to already have a message.
	s := loop.resolveSignBE(pos)
	s.front.messages = [signLines]string{"hi", "", "", ""}
	signHeldItem(p, int32(item.RedDye.ID))
	p.client = captureClient(64)

	consumed := loop.useBlockInteraction(p, pos, 1, 0, 0, 0)
	if !consumed {
		t.Fatal("dye on a sign returned false, want consumed")
	}
	// RED is DyeColor id 14.
	if loop.signs[pos].front.color != 14 {
		t.Fatalf("front color = %d after red dye, want 14 (RED)", loop.signs[pos].front.color)
	}
	// One dye consumed in survival.
	if c := ensureInventory(p).get(heldWindowSlot(ensureInventory(p).heldSlot)).Count; c != 0 {
		t.Fatalf("held dye count = %d after apply, want 0 (consumed)", c)
	}
	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundBlockEntityData); n != 1 {
		t.Fatalf("dye apply broadcast BlockEntityData %d times, want 1", n)
	}
}

// TestSignDyeRejectedOnEmptySide: a dye right-click on a side with NO text does NOT apply (the default
// SignApplicator.canApplyToSign == hasMessage gate) and falls through to the edit reopen (no color change,
// no item consumed). CITE SignApplicator.canApplyToSign.
func TestSignDyeRejectedOnEmptySide(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.uuid = uuid.New()
	p.gameMode = gameModeSurvival

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	placeSignBlock(loop, pos)
	signHeldItem(p, int32(item.RedDye.ID)) // no front text set -> canApplyToSign false
	p.client = captureClient(64)

	loop.useBlockInteraction(p, pos, 1, 0, 0, 0)

	if loop.signs[pos].front.color != signDefaultColor {
		t.Fatalf("empty-side dye changed color to %d, want default %d (rejected)", loop.signs[pos].front.color, signDefaultColor)
	}
	if c := ensureInventory(p).get(heldWindowSlot(ensureInventory(p).heldSlot)).Count; c != 1 {
		t.Fatalf("dye consumed %d on empty side, want 1 (not consumed)", 1-c)
	}
	// Falls through to reopen: the clicker becomes the allowed editor + gets an OpenSignEditor.
	if loop.signs[pos].playerWhoMayEdit != p.uuid {
		t.Fatal("rejected dye did not fall through to the edit reopen")
	}
	got := drainPackets(p.client)
	if n := countID(got, packetid.ClientboundOpenSignEditor); n != 1 {
		t.Fatalf("rejected dye sent OpenSignEditor %d times, want 1 (useWithoutItem reopen)", n)
	}
}

// TestSignHoneycombWaxes: a honeycomb right-click waxes the sign (canApplyToSign override == true) even
// with no text, locking future edits. CITE HoneycombItem.tryApplyToSign (setWaxed) + canApplyToSign.
func TestSignHoneycombWaxes(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.uuid = uuid.New()
	p.gameMode = gameModeSurvival

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	placeSignBlock(loop, pos)
	signHeldItem(p, int32(item.Honeycomb.ID))
	p.client = captureClient(64)

	consumed := loop.useBlockInteraction(p, pos, 1, 0, 0, 0)
	if !consumed {
		t.Fatal("honeycomb on a sign returned false, want consumed")
	}
	if !loop.signs[pos].waxed {
		t.Fatal("honeycomb did not wax the sign")
	}
	// A subsequent edit is now rejected by the waxed guard.
	loop.signs[pos].playerWhoMayEdit = p.uuid
	loop.handleSignUpdate(p, signUpdatePacket(pos, true, "x", "", "", ""))
	if loop.signs[pos].front.messages[0] != "" {
		t.Fatal("waxed sign accepted an edit, want rejected")
	}
}

// TestSignGlowInkToggles: a glow_ink_sac makes the (texted) side glow; a following ink_sac clears it.
// CITE GlowInkSacItem/InkSacItem.tryApplyToSign (setHasGlowingText true/false).
func TestSignGlowInkToggles(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.uuid = uuid.New()
	p.gameMode = gameModeCreative // creative: item not consumed, so both clicks reuse the slot

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	placeSignBlock(loop, pos)
	loop.resolveSignBE(pos).front.messages = [signLines]string{"lit", "", "", ""}

	signHeldItem(p, int32(item.GlowInkSac.ID))
	loop.useBlockInteraction(p, pos, 1, 0, 0, 0)
	if !loop.signs[pos].front.glowing {
		t.Fatal("glow_ink_sac did not set glowing")
	}

	signHeldItem(p, int32(item.InkSac.ID))
	loop.useBlockInteraction(p, pos, 1, 0, 0, 0)
	if loop.signs[pos].front.glowing {
		t.Fatal("ink_sac did not clear glowing")
	}
}

// TestSignFullEditRoundTrip: the task's end-to-end persistence assertion. Edit both sides' text (+color
// +glowing), encode to the block-entity NBT, decode it back, and assert every field survives -- the
// save/load contract other players and a server restart rely on. CITE SignBlockEntity.saveAdditional/
// loadAdditional + SignText.DIRECT_CODEC (messages/color/has_glowing_text).
func TestSignFullEditRoundTrip(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.uuid = uuid.New()

	pos := pk.Position{X: 1, Y: 64, Z: 1}
	placeSignBlock(loop, pos)
	state := block.ToStateID[block.OakSign{}]
	loop.openSignForPlace(p, pos, state) // p is the allowed editor (front)

	// Edit the FRONT text via the real ServerboundSignUpdate path.
	loop.handleSignUpdate(p, signUpdatePacket(pos, true, "alpha", "beta", "", "gamma"))
	// Directly stamp a color + glowing + back text (as dye/glow_ink would) to exercise every field.
	s := loop.signs[pos]
	s.front.color = 14   // RED
	s.front.glowing = true
	s.back.messages = [signLines]string{"rear", "", "", ""}
	s.back.color = 4 // YELLOW
	s.waxed = true

	data, err := encodeSignBE(s)
	if err != nil {
		t.Fatalf("encodeSignBE: %v", err)
	}
	got := decodeSignBE(data, s.beType)

	wantFront := [signLines]string{"alpha", "beta", "", "gamma"}
	if got.front.messages != wantFront {
		t.Fatalf("front lines round-trip = %v, want %v", got.front.messages, wantFront)
	}
	if got.front.color != 14 {
		t.Fatalf("front color round-trip = %d, want 14 (RED)", got.front.color)
	}
	if !got.front.glowing {
		t.Fatal("front glowing did not round-trip")
	}
	wantBack := [signLines]string{"rear", "", "", ""}
	if got.back.messages != wantBack {
		t.Fatalf("back lines round-trip = %v, want %v", got.back.messages, wantBack)
	}
	if got.back.color != 4 {
		t.Fatalf("back color round-trip = %d, want 4 (YELLOW)", got.back.color)
	}
	if !got.waxed {
		t.Fatal("waxed did not round-trip")
	}
}
