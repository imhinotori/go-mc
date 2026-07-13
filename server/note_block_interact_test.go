package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// note_block_interact_test.go — validation gates for the NoteBlock player-interaction port
// (note_block_interact.go): the right-click TUNE (cycle NOTE + play), the covered-silent gate on tune,
// the mob-head-on-top PASS, and the left-click ATTACK (play without tuning). Each drives the real
// UseItemOn / PlayerAction input path and asserts the ported observable behavior against the 26.2 jar.

// TestNoteBlockTuneCyclesNoteAndPlays: a right-click (empty hand) on a note block with clear air above
// cycles its NOTE 0->1, broadcasts the new state, plays the note (records + emits ClientboundBlockEvent),
// and does NOT place a block. CITE NoteBlock.useWithoutItem (state.cycle(NOTE) + playNote).
func TestNoteBlockTuneCyclesNoteAndPlays(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival

	notePos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(notePos, block.ToStateID[block.NoteBlock{}], dimMinY) // NOTE=0, HARP, air above

	const seq = 51
	ui := useItemOnPacket(0 /*main hand*/, notePos, 1 /*UP*/, 0.5, 1.0, 0.5, false, false, seq)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	got := mustGet(t, mgr, notePos)
	if !block.IsNoteBlock(got) {
		t.Fatalf("tuned block is no longer a note block: state=%d", got)
	}
	if n := block.NoteBlockNoteOf(got); n != 1 {
		t.Fatalf("NOTE after one tune = %d, want 1 (cycle 0->1)", n)
	}
	if plays := loop.only().notesPlayed; len(plays) != 1 || plays[0] != notePos {
		t.Fatalf("note should play exactly once at %v on tune, got %v", notePos, plays)
	}
	pkts := drainPackets(p.client)
	if n := countID(pkts, packetid.ClientboundBlockEvent); n != 1 {
		t.Fatalf("tune must send exactly 1 ClientboundBlockEvent (audible note), got %d", n)
	}
	// A tune is NOT a placement: the note-block cell is unchanged in kind (still a note block, not the
	// held item, which was empty anyway).
	if n := countID(pkts, packetid.ClientboundBlockUpdate); n != 1 {
		t.Fatalf("tune should broadcast exactly 1 BlockUpdate (the cycled NOTE state), got %d", n)
	}
}

// TestNoteBlockTuneCoveredStaysSilent: a right-click on a HARP (BASE_BLOCK) note block with a SOLID block
// above still cycles NOTE (the tune happens) but plays NO note — playNote's worksAbove/air-above gate.
// CITE NoteBlock.useWithoutItem + NoteBlock.playNote gate.
func TestNoteBlockTuneCoveredStaysSilent(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival

	notePos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(notePos, block.ToStateID[block.NoteBlock{}], dimMinY)
	mgr.SetBlock(above(notePos), block.ToStateID[block.Stone{}], dimMinY) // cover the note block

	const seq = 52
	ui := useItemOnPacket(0, notePos, 1, 0.5, 1.0, 0.5, false, false, seq)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	got := mustGet(t, mgr, notePos)
	if n := block.NoteBlockNoteOf(got); n != 1 {
		t.Fatalf("covered note block should still tune to NOTE 1, got %d", n)
	}
	if plays := loop.only().notesPlayed; len(plays) != 0 {
		t.Fatalf("covered HARP note block must stay silent on tune, plays=%d", len(plays))
	}
	if n := countID(drainPackets(p.client), packetid.ClientboundBlockEvent); n != 0 {
		t.Fatalf("covered note block must send NO ClientboundBlockEvent, got %d", n)
	}
}

// TestNoteBlockTopInstrumentHeadOnTopPasses: a right-click with a NOTE_BLOCK_TOP_INSTRUMENTS head
// (zombie_head) on the UP face PASSes (no tune) so the head places on top. NOTE stays 0. CITE
// NoteBlock.useItemOn (stack.is(NOTE_BLOCK_TOP_INSTRUMENTS) && direction==UP -> PASS).
func TestNoteBlockTopInstrumentHeadOnTopPasses(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.ZombieHead.ID, 1)

	notePos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(notePos, block.ToStateID[block.NoteBlock{}], dimMinY)

	const seq = 53
	ui := useItemOnPacket(0, notePos, 1 /*UP*/, 0.5, 1.0, 0.5, false, false, seq)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	got := mustGet(t, mgr, notePos)
	if n := block.NoteBlockNoteOf(got); n != 0 {
		t.Fatalf("a head on the TOP face must PASS (no tune); NOTE = %d, want 0", n)
	}
	if plays := loop.only().notesPlayed; len(plays) != 0 {
		t.Fatalf("a head-on-top PASS must not play a note, plays=%d", len(plays))
	}
}

// TestNoteBlockTopInstrumentHeadOnSideTunes: the same head clicked on a SIDE face (not UP) does NOT PASS
// — it falls through to useWithoutItem and TUNES the note block. CITE NoteBlock.useItemOn (the UP-face
// guard is the ONLY PASS condition).
func TestNoteBlockTopInstrumentHeadOnSideTunes(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival
	setHeldItem(p, item.ZombieHead.ID, 1)

	notePos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(notePos, block.ToStateID[block.NoteBlock{}], dimMinY)

	const seq = 54
	ui := useItemOnPacket(0, notePos, 2 /*NORTH side face*/, 0.5, 0.5, 0.0, false, false, seq)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: ui})

	got := mustGet(t, mgr, notePos)
	if n := block.NoteBlockNoteOf(got); n != 1 {
		t.Fatalf("a head on a SIDE face should tune (NOTE 0->1), got %d", n)
	}
}

// TestNoteBlockPunchPlaysWithoutTuning: a SURVIVAL START_DESTROY_BLOCK (left-click / punch) on a note
// block PLAYS the note (NoteBlock.attack -> playNote) but does NOT cycle NOTE (attack never tunes) and
// does NOT break the block on START (survival begins a dig timer, no instant break for a note block).
// CITE NoteBlock.attack + ServerPlayerGameMode.handleBlockBreakAction (blockState.attack on START).
func TestNoteBlockPunchPlaysWithoutTuning(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival // creative would break on START before attack

	notePos := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(notePos, block.ToStateID[block.NoteBlock{}], dimMinY)

	const seq = 55
	pa := playerActionPacket(0 /*START_DESTROY_BLOCK*/, notePos, 1, seq)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: pa})

	got := mustGet(t, mgr, notePos)
	if !block.IsNoteBlock(got) {
		t.Fatalf("survival punch must NOT break the note block on START; state=%d", got)
	}
	if n := block.NoteBlockNoteOf(got); n != 0 {
		t.Fatalf("attack (punch) must NOT tune; NOTE = %d, want 0", n)
	}
	if plays := loop.only().notesPlayed; len(plays) != 1 || plays[0] != notePos {
		t.Fatalf("punch should play the note exactly once at %v, got %v", notePos, plays)
	}
}
