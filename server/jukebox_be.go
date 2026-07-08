package server

// jukebox_be.go -- the JUKEBOX BLOCK-ENTITY (JUKEBOX-01): a 1:1 port of
// net.minecraft.world.level.block.entity.JukeboxBlockEntity (setTheItem / popOutTheItem /
// notifyItemChangedInJukebox / getComparatorOutput) + the JukeboxBlock use hooks and
// JukeboxPlayable.tryInsertIntoJukebox over the 26.2 jar (temp/cache/26.2-inner.jar, CFR/javap this
// session). A jukebox holds ONE music-disc item; inserting a disc (right-click with a JUKEBOX_PLAYABLE
// item) sets HAS_RECORD=true; a right-click on a loaded jukebox EJECTS the disc (popOutTheItem, dropping it
// + HAS_RECORD=false). The redstone comparator reads the disc song comparatorOutput (1..15 per disc).
//
// 1:1 jar (net.minecraft.world.level.block.entity.JukeboxBlockEntity), VERIFIED CFR:
//   setTheItem(stack): item = stack; boolean has = !item.isEmpty(); notifyItemChangedInJukebox(has);
//       if (has && JukeboxSong.fromStack(item).isPresent()) songPlayer.play(...) else songPlayer.stop(...).
//   notifyItemChangedInJukebox(has): setBlock(pos, state.setValue(HAS_RECORD, has), 2) + gameEvent(BLOCK_CHANGE).
//   popOutTheItem(): if item empty return; removeTheItem(); spawn ItemEntity(copy) at (0.5, 1.01, 0.5)
//       offsetRandomXZ(0.7); onSongChanged().
//   getComparatorOutput(): JukeboxSong.fromStack(item).map(Holder::value).map(comparatorOutput).orElse(0).
//
// The disc slot + HAS_RECORD state + comparator output + the disc-eject drop are server-authoritative and
// ported faithfully. The JukeboxSongPlayer.play/stop MUSIC PLAYBACK (the client sound + the 20-tick spin
// ticks + the songPlayer.tick) is cite-deferred: v1 has no jukebox-song sound-event / music packet seam.
// The music-disc comparator value is a fixed per-song datum (the jukebox_song registry comparator_output),
// materialized here as a disc-item-id -> value table (extracted from data/minecraft/jukebox_song/*.json,
// where each music_disc_<name> maps to song <name>).

import (
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// jukeboxBE is the tick-owned state of one jukebox block-entity -- the Go analogue of JukeboxBlockEntity
// narrowed to the single held disc item. item is the loaded disc (empty Count 0 == ItemStack.EMPTY).
// Registered on placement / on disc insert; read by the comparator analog-output seam. The JukeboxSongPlayer
// (music playback + spin ticks) is DEFERRED cited, so there is no per-tick drive in v1.
type jukeboxBE struct {
	item component.SlotData
}

// jukeboxSongComparator maps a music-disc song NAME to its jukebox_song comparator_output (1..15). Extracted
// VERBATIM from data/minecraft/jukebox_song/*.json in the 26.2 jar (the JukeboxSong.comparatorOutput field).
// A disc item music_disc_<name> resolves its song as <name> (the vanilla disc->song convention). CITE
// JukeboxBlockEntity.getComparatorOutput -> JukeboxSong.comparatorOutput.
var jukeboxSongComparator = map[string]int{
	"13":                1,
	"cat":               2,
	"blocks":            3,
	"chirp":             4,
	"far":               5,
	"mall":              6,
	"mellohi":           7,
	"stal":              8,
	"strad":             9,
	"ward":              10,
	"tears":             10,
	"11":                11,
	"creator_music_box": 11,
	"wait":              12,
	"creator":           12,
	"pigstep":           13,
	"precipice":         13,
	"otherside":         14,
	"relic":             14,
	"5":                 15,
	"lava_chicken":      9,
	"bounce":            8,
}

// jukeboxComparatorForItem ports JukeboxBlockEntity.getComparatorOutput for a held disc item: resolve the
// disc song comparator_output, or 0 when the item is not a music disc (JukeboxSong.fromStack -> empty).
// The disc item name music_disc_<name> maps to song <name>. CITE JukeboxBlockEntity.getComparatorOutput +
// JukeboxSong.fromStack.
func jukeboxComparatorForItem(item component.SlotData) int {
	if stackEmpty(item) {
		return 0
	}
	name := itemName(int32(item.ItemID))
	name = trimNamespace(name)
	const prefix = "music_disc_"
	if len(name) <= len(prefix) || name[:len(prefix)] != prefix {
		return 0 // not a music disc -> JukeboxSong.fromStack empty -> orElse(0)
	}
	song := name[len(prefix):]
	if v, ok := jukeboxSongComparator[song]; ok {
		return v
	}
	return 0
}

// jukeboxSetTheItem ports JukeboxBlockEntity.setTheItem(stack): store the disc, flip HAS_RECORD to
// (!stack.isEmpty()) via notifyItemChangedInJukebox, and start/stop the song player. The play/stop MUSIC is
// DEFERRED (cited -- no jukebox-song sound seam); the item slot + HAS_RECORD state + comparator are the
// server-authoritative half. CITE JukeboxBlockEntity.setTheItem.
func (t *TickLoop) jukeboxSetTheItem(pos pk.Position, j *jukeboxBE, stack component.SlotData) {
	j.item = stack
	has := !stackEmpty(stack)
	// notifyItemChangedInJukebox(has): setBlock HAS_RECORD=has, flag 2 + gameEvent(BLOCK_CHANGE).
	t.jukeboxNotifyItemChanged(pos, has)
	// if (has && JukeboxSong.fromStack(item).isPresent()) songPlayer.play else songPlayer.stop: MUSIC
	// DEFERRED (cited).
}

// jukeboxNotifyItemChanged ports JukeboxBlockEntity.notifyItemChangedInJukebox(hasRecord): write the
// jukebox state with HAS_RECORD=hasRecord (setBlock flag 2 -> broadcast, no neighbor update), then
// gameEvent(BLOCK_CHANGE) (deferred). The HAS_RECORD flip re-drives the comparator below via the redstone
// edit hook (a jukebox is a signal source / analog-output block). CITE
// JukeboxBlockEntity.notifyItemChangedInJukebox.
func (t *TickLoop) jukeboxNotifyItemChanged(pos pk.Position, hasRecord bool) {
	if t.world() == nil {
		return
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsJukebox(state) {
		return
	}
	newState, ok := block.JukeboxWithRecord(state, hasRecord)
	if !ok || !t.world().SetBlock(pos, newState, dimMinY) {
		return
	}
	t.broadcastBlockUpdate(pos, newState)
	// gameEvent(BLOCK_CHANGE) deferred. Re-notify the comparator (jukebox analog output changed).
	t.onRedstoneEdit(pos)
}

// jukeboxPopOutTheItem ports JukeboxBlockEntity.popOutTheItem(): if a disc is loaded, remove it, drop it as
// an Item entity above the jukebox, and clear HAS_RECORD (onSongChanged -> the item is gone). Returns true
// when a disc was ejected. CITE JukeboxBlockEntity.popOutTheItem.
func (t *TickLoop) jukeboxPopOutTheItem(pos pk.Position, j *jukeboxBE) bool {
	if stackEmpty(j.item) {
		return false // item.isEmpty() -> return
	}
	disc := j.item
	// removeTheItem(): item = EMPTY; then setTheItem(EMPTY) equivalent (HAS_RECORD=false + stop).
	j.item = component.SlotData{Count: 0}
	// spawn ItemEntity(copy) at Vec3(0.5, 1.01, 0.5) offsetRandomXZ(0.7): drop the disc above the jukebox.
	if t.cur() != nil {
		x := float64(pos.X) + 0.5
		y := float64(pos.Y) + 1.01
		z := float64(pos.Z) + 0.5
		ie := NewItemEntity(t.idAlloc.AllocID(), x, y, z, disc)
		t.cur().entities.add(ie)
	}
	// onSongChanged(): updateNeighborsAt + setChanged -> HAS_RECORD=false + comparator re-read.
	t.jukeboxNotifyItemChanged(pos, false)
	return true
}

// useJukebox ports JukeboxBlock.useItemOn (insert a disc via JukeboxPlayable.tryInsertIntoJukebox) AND
// useWithoutItem (eject the disc via popOutTheItem): a right-click on a LOADED jukebox ejects the disc; a
// right-click on an EMPTY jukebox holding a JUKEBOX_PLAYABLE (music-disc) item inserts it. Either way the
// interaction is consumed so no block is placed. A right-click on an empty jukebox with a non-disc hand
// falls through (returns false -> placement continues). CITE JukeboxBlock.useItemOn / useWithoutItem +
// JukeboxPlayable.tryInsertIntoJukebox.
//
// 1:1 net.minecraft.world.level.block.JukeboxBlock.useItemOn / useWithoutItem
func (t *TickLoop) useJukebox(p *tickPlayer, pos pk.Position, state block.StateID) bool {
	if t.world() == nil {
		return false
	}
	// useWithoutItem: if HAS_RECORD, popOutTheItem (eject) and consume (SUCCESS).
	if block.JukeboxHasRecord(state) {
		j := t.resolveJukebox(pos)
		if j == nil {
			return false
		}
		t.jukeboxPopOutTheItem(pos, j)
		return true // ejected -- no block-place fall-through.
	}
	// useItemOn (empty jukebox): tryInsertIntoJukebox -- the held item must be a JUKEBOX_PLAYABLE (music
	// disc). A non-disc hand returns TRY_WITH_EMPTY_HAND -> placement continues (return false).
	inv := ensureInventory(p)
	held := inv.get(heldWindowSlot(inv.heldSlot))
	if stackEmpty(held) || !isJukeboxPlayable(held) {
		return false
	}
	j := t.resolveJukebox(pos)
	if j == nil {
		return false
	}
	// tryInsertIntoJukebox: item = stack.consumeAndReturn(1, player); setTheItem(item).
	one := held
	one.Count = 1
	t.jukeboxSetTheItem(pos, j, one)
	if p.gameMode != gameModeCreative {
		// stack.consumeAndReturn(1, player): shrink the held stack by 1 + sync the slot.
		t.shrinkHeldItem(p, inv)
	}
	return true // inserted -- no block-place fall-through.
}

// isJukeboxPlayable ports the DataComponents.JUKEBOX_PLAYABLE presence check (a music disc carries the
// jukebox_playable component pointing to its song). v1 gates on the item being a music_disc_* item (the set
// that carries JUKEBOX_PLAYABLE) -- the same set JukeboxPlayable.tryInsertIntoJukebox accepts. CITE
// JukeboxPlayable.tryInsertIntoJukebox (stack.get(JUKEBOX_PLAYABLE) != null gate).
func isJukeboxPlayable(item component.SlotData) bool {
	if stackEmpty(item) {
		return false
	}
	name := trimNamespace(itemName(int32(item.ItemID)))
	const prefix = "music_disc_"
	return len(name) > len(prefix) && name[:len(prefix)] == prefix
}

// jukeboxAnalogOutputSignal ports JukeboxBlock.getAnalogOutputSignal (hasAnalogOutputSignal == true):
// JukeboxBlockEntity.getComparatorOutput() -- the loaded disc song comparator_output (0 when empty).
// Returns (signal, true) for a jukebox, (0, false) otherwise (so the comparator falls to its container/
// super path). CITE JukeboxBlock.getAnalogOutputSignal.
func (t *TickLoop) jukeboxAnalogOutputSignal(pos pk.Position) (int, bool) {
	if t.world() == nil {
		return 0, false
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsJukebox(state) {
		return 0, false
	}
	j := t.resolveJukebox(pos)
	if j == nil {
		return 0, true
	}
	return jukeboxComparatorForItem(j.item), true
}

// trimNamespace strips a leading "minecraft:" (or any "ns:") namespace from an item registry name,
// yielding the bare path (itemName returns the namespaced form). Used to match the music_disc_ prefix.
func trimNamespace(name string) string {
	for i := 0; i < len(name); i++ {
		if name[i] == ':' {
			return name[i+1:]
		}
	}
	return name
}

// resolveJukebox returns the tick-owned jukeboxBE for pos, creating an EMPTY one (no disc) on first access
// -- the analogue of a freshly-placed jukebox default JukeboxBlockEntity. Returns nil when pos is not a
// jukebox block (or the world is unloaded). Tick-owned (t.jukeboxes, the t.lecterns twin).
func (t *TickLoop) resolveJukebox(pos pk.Position) *jukeboxBE {
	if t.jukeboxes == nil {
		t.jukeboxes = make(map[pk.Position]*jukeboxBE)
	}
	if j, ok := t.jukeboxes[pos]; ok {
		return j
	}
	if t.world() == nil {
		return nil
	}
	state, ok := t.world().GetBlock(pos, dimMinY)
	if !ok || !block.IsJukebox(state) {
		return nil
	}
	j := &jukeboxBE{}
	t.jukeboxes[pos] = j
	return j
}
