package server

// note_block_interact.go — the PLAYER-INTERACTION half of NoteBlock (the tune / attack hooks), ported
// 1:1 from net.minecraft.world.level.block.NoteBlock in the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar, javap -c -p this session). The redstone neighborChanged + the shared
// playNote core live in redstone_blocks.go; this file wires the right-click (useItemOn/useWithoutItem
// TUNE) and the left-click (attack PLAY) interactions + the audible ClientboundBlockEvent forward.
//
// 1:1 jar (net.minecraft.world.level.block.NoteBlock):
//
//	useItemOn(stack, state, level, pos, player, hand, hit):
//	    if (stack.is(ItemTags.NOTE_BLOCK_TOP_INSTRUMENTS) && hit.getDirection() == Direction.UP)
//	        return InteractionResult.PASS;                    // let the head place ON TOP (no tune)
//	    return super.useItemOn(...);                          // -> useWithoutItem (tune)
//	useWithoutItem(state, level, pos, player, hit):
//	    if (!level.isClientSide) {
//	        state = state.cycle(NOTE);
//	        level.setBlock(pos, state, 3);
//	        playNote(player, state, level, pos);
//	        player.awardStat(Stats.TUNE_NOTEBLOCK);
//	    }
//	    return InteractionResult.SUCCESS;
//	attack(state, level, pos, player):
//	    if (level.isClientSide) return;
//	    playNote(player, state, level, pos);
//	    player.awardStat(Stats.PLAY_NOTEBLOCK);
//
// The audible note (playNote -> level.blockEvent(pos, this, 0, 0)) is forwarded as the real
// ClientboundBlockEvent from the shared noteBlockPlayNoteBy (redstone_blocks.go), so the player hears the
// tuned pitch (the client's NoteBlock.triggerEvent reads pos's INSTRUMENT+NOTE state). awardStat is a
// cited no-op (Sulfur has no per-player TUNE/PLAY note-block stat increment site yet — the note-block
// tune/play has no gameplay effect beyond the sound, so the stat is deferred).
//
// setInstrument (getStateForPlacement / updateShape instrument-by-neighbor) is DEFERRED: it needs
// BlockState.instrument() for ARBITRARY neighbor blocks (derived from each block's SoundType, a not-yet-
// built subsystem). A placed note block therefore keeps its default INSTRUMENT=HARP (the vanilla default),
// which is correct for a note block over most blocks; the mob-head-on-top / material-below variation is
// the cited follow-up. CITE NoteBlock.setInstrument.

import (
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// noteBlockRegistryID caches the minecraft:note_block index in the block registry (the id the
// ClientboundBlockEvent packet carries). Resolved once; -1 if absent (then the block-event send no-ops).
var noteBlockRegistryID = indexOf(registryid.Block, "minecraft:note_block")

// noteBlockTopInstrumentItems is the item set of ItemTags.NOTE_BLOCK_TOP_INSTRUMENTS — the mob-head items
// that, when used on the TOP face of a note block, place ON TOP instead of tuning (so the head becomes the
// note block's instrument). VERBATIM from data/minecraft/tags/item/noteblock_top_instruments.json in the
// 26.2 jar. CITE ItemTags.NOTE_BLOCK_TOP_INSTRUMENTS.
var noteBlockTopInstrumentItems = map[string]bool{
	"minecraft:zombie_head":           true,
	"minecraft:skeleton_skull":        true,
	"minecraft:creeper_head":          true,
	"minecraft:dragon_head":           true,
	"minecraft:wither_skeleton_skull": true,
	"minecraft:piglin_head":           true,
	"minecraft:player_head":           true,
}

// blockEventPacket builds a ClientboundBlockEvent(pos, b0, b1, block). Wire layout (jar-verified,
// ClientboundBlockEventPacket.write): writeBlockPos(pos) [packed long] + writeByte(b0) + writeByte(b1) +
// VarInt(block registry id). CITE ClientboundBlockEventPacket.STREAM_CODEC.
func blockEventPacket(pos pk.Position, b0, b1 int, blockRegistryID int32) pk.Packet {
	return pk.Marshal(int32(packetid.ClientboundBlockEvent),
		pos, pk.Byte(b0), pk.Byte(b1), pk.VarInt(blockRegistryID))
}

// broadcastNoteBlockEvent sends ClientboundBlockEvent(pos, note_block, 0, 0) to every player tracking the
// note block's column (the audible note cue — the client's NoteBlock.triggerEvent reads pos's INSTRUMENT+
// NOTE state to synthesize the pitch). A no-op when the block registry id is unresolved. Mirrors the
// broadcastBlockUpdate viewer set. CITE NoteBlock.playNote (level.blockEvent(pos, this, 0, 0)).
func (t *TickLoop) broadcastNoteBlockEvent(pos pk.Position) {
	if noteBlockRegistryID < 0 {
		return
	}
	col := chunkCenterOf(int32(pos.X), int32(pos.Z))
	packet := blockEventPacket(pos, levelEventNotePlay, levelEventNotePlay, noteBlockRegistryID)
	for _, pl := range t.players {
		if pl.client == nil {
			continue
		}
		if pl.center == col || (pl.sentChunks != nil && pl.sentChunks[col]) {
			pl.client.Send(packet)
		}
	}
}

// noteBlockUse ports NoteBlock.useItemOn + useWithoutItem for the right-click path. Returns true (the
// interaction CONSUMED the action -> no block is placed) for a tune; false (PASS) only when the held item
// is a NOTE_BLOCK_TOP_INSTRUMENTS head clicked on the TOP face (so the head places on top). CITE
// NoteBlock.useItemOn / useWithoutItem.
func (t *TickLoop) noteBlockUse(p *tickPlayer, pos pk.Position, state block.StateID, direction int) bool {
	if t.world() == nil {
		return false
	}
	// useItemOn: a NOTE_BLOCK_TOP_INSTRUMENTS head used on the UP face PASSes so the head places on top.
	// direction==1 is Direction.UP (from3DDataValue).
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if !slotIsEmpty(held) && direction == 1 && noteBlockTopInstrumentItems[itemName(int32(held.ItemID))] {
		return false // PASS -> placement runs (head placed on top)
	}
	// useWithoutItem: state = state.cycle(NOTE); setBlock flag 3; playNote(player, ...); awardStat(TUNE).
	newState, ok := block.NoteBlockCycleNote(state)
	if !ok || !t.world().SetBlock(pos, newState, dimMinY) {
		return true // vanilla still returns SUCCESS; consume without placing on a failed setBlock.
	}
	t.broadcastBlockUpdate(pos, newState)
	t.noteBlockPlayNoteBy(pos, newState, p.entityID)
	// player.awardStat(Stats.TUNE_NOTEBLOCK): cite-deferred (no note-block stat increment site).
	return true
}

// noteBlockAttack ports NoteBlock.attack (left-click / punch): playNote(player, state, level, pos) +
// awardStat(PLAY_NOTEBLOCK). No NOTE cycle (only useWithoutItem tunes). Called from the START dig arm
// (block_break.go) — the BlockState.attack hook that fires before getDestroyProgress. CITE NoteBlock.attack.
func (t *TickLoop) noteBlockAttack(p *tickPlayer, pos pk.Position, state block.StateID) {
	if !block.IsNoteBlock(state) {
		return
	}
	t.noteBlockPlayNoteBy(pos, state, p.entityID)
	// player.awardStat(Stats.PLAY_NOTEBLOCK): cite-deferred (no note-block stat increment site).
}
