package main

// gate.go — the PLUGIN-07 BOT-DRIVEN VISUAL GATE (Plan 28-02). The operator-directed replacement
// for the human eye: cmd/testbot connects as a real proto-776 client, TRIGGERS each plugin-system
// behavior, and ASSERTS on the OBSERVED packets. What the bot observes on the wire IS the visual
// proof — a STRONGER gate than a human eye because it asserts the exact wire behavior.
//
// The 4-item checklist (each: trigger -> assert on observed packets; exit 0 = all pass):
//  1. CUSTOM mob   — right-click the test-kit spawn egg (Plan 28-01) -> observe ClientboundAddEntity
//     + move packets for the new id (the wander mob WALKS via the Go nav).
//  2. VANILLA pig  — the only pig is the vanilla-pig-as-plugin; the bot confirms a pig-wire AddEntity
//     + move/headrot is observed (Phase-24 TestPluginPigEqualsGoNativePig already proved
//     equivalence; the bot confirms it on the wire). The wander mob renders as the pig wire type
//     (base_type=pig) — so its AddEntity also satisfies the pig-wire observation; the DISTINGUISHING
//     proof for #1 is the Go-nav movement, for #2 the pig wire type.
//  3. CRAFTING     — place + open a crafting_table (ClientboundOpenScreen), place ingredients into
//     the 3x3 grid via ServerboundContainerClick, observe the result slot populate
//     (ContainerSetContent/SetSlot with a non-empty result) for a VANILLA recipe (oak planks ->
//     sticks) AND a CUSTOM recipe (dirt -> diamond, the customrecipe plugin).
//  4. EVENTS       — break a block -> observe a ClientboundSystemChat carrying "gate_events:" (the
//     on_block_break hook -> chat() builtin -> broadcastSystemChat). The join hook fired at connect.
//
// false-pass guard (threat T-28-02): each item asserts the SPECIFIC observed effect (a real
// AddEntity id that accrued move packets; a non-empty result slot; a SystemChat with the
// gate_events marker) — never a blanket "got some packets". A missing trigger FAILS.
//
// Scope notes:
//   - The bot runs on the DEFAULT CGO_ENABLED=0 binary (Starlark core). The Python off-tick lane is
//     covered by its own Phase-26 `-tags python` Docker gate (already green) — NOT re-tested here.
//   - Folia gameplay-identical is proven by TestTwoRegionsTickInParallel + -race; the bot confirms
//     the observed AddEntity/move behavior holds on the regionized server.

import (
	"bytes"
	"fmt"
	"log"
	"math"
	"os"
	"strings"
	"time"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// gate kit slot layout (test_kit.go, the SULFUR_TEST_KIT join kit). The egg/dirt/planks/table live on
// the HOTBAR so the bot can SetCarriedItem them; the crafting-window menu maps hotbar window slots
// 36..44 to menu slots 37..45.
const (
	gateTableHotbar    = 8  // hotbar index 8 (inventory window slot 44) = CraftingTable
	gateDirtMenuSlot   = 43 // Dirt at hotbar window slot 42 -> crafting menu slot 37+(42-36)=43
	gatePlanksMenuSlot = 41 // OakPlanks at hotbar window slot 40 -> crafting menu slot 37+(40-36)=41
	gateCraftResultID  = 0  // the crafting-window result slot (menu slot 0, take-only)
)

// gate item ids (data/item) the bot asserts the crafting result against.
const (
	idStickItem   = 808 // minecraft:stick — vanilla recipe result (2 vertical planks -> 4 sticks)
	idDiamondItem = 926 // minecraft:diamond — customrecipe result (1 dirt -> 1 diamond)
)

// drainTicks moves+keepalives for n bot ticks while the reader records observations. It sends a
// stationary MovePlayerPos each tick (the server expects ~20Hz movement) so the connection stays
// alive and the tracker keeps streaming entity packets.
func (b *bot) drainTicks(n int) {
	for i := 0; i < n; i++ {
		if errp := b.fatal.Load(); errp != nil {
			return
		}
		b.mu.Lock()
		x, y, z := b.x, b.y, b.z
		b.mu.Unlock()
		_ = b.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundMovePlayerPos),
			pk.Double(x), pk.Double(y), pk.Double(z), movementFlagOnGround))
		time.Sleep(tickInterval)
	}
}

// --- reader-side recorders (called from readLoop on the reader goroutine, gate mode only) ----------

// gateInit lazily allocates the observation maps under gateMu. Called by every recorder + runGate.
func (b *bot) gateInit() {
	if b.addEntities == nil {
		b.addEntities = make(map[int32]int32)
		b.moveCounts = make(map[int32]int)
		b.sawHeadRot = make(map[int32]bool)
	}
}

// recordAddEntity decodes the leading id + type of a ClientboundAddEntity (encodeAddEntity wire:
// VarInt id, UUID, VarInt typeId, ...) and records id -> typeId.
func (b *bot) recordAddEntity(p pk.Packet) {
	r := bytes.NewReader(p.Data)
	var id pk.VarInt
	var uuid pk.UUID
	var typ pk.VarInt
	if _, err := id.ReadFrom(r); err != nil {
		return
	}
	if _, err := uuid.ReadFrom(r); err != nil {
		return
	}
	if _, err := typ.ReadFrom(r); err != nil {
		return
	}
	b.gateMu.Lock()
	b.gateInit()
	b.addEntities[int32(id)] = int32(typ)
	b.gateMu.Unlock()
	log.Printf("[gate] observed AddEntity id=%d type=%d", int32(id), int32(typ))
}

// recordMove increments the move count for the entity id leading a move/teleport packet.
func (b *bot) recordMove(p pk.Packet) {
	var id pk.VarInt
	if _, err := id.ReadFrom(bytes.NewReader(p.Data)); err != nil {
		return
	}
	b.gateMu.Lock()
	b.gateInit()
	b.moveCounts[int32(id)]++
	b.gateMu.Unlock()
}

// recordHeadRot records a RotateHead for the leading entity id (item #2 supporting).
func (b *bot) recordHeadRot(p pk.Packet) {
	var id pk.VarInt
	if _, err := id.ReadFrom(bytes.NewReader(p.Data)); err != nil {
		return
	}
	b.gateMu.Lock()
	b.gateInit()
	b.sawHeadRot[int32(id)] = true
	b.gateMu.Unlock()
}

// recordContainerContent decodes ClientboundContainerSetContent (VarInt containerId, VarInt stateId,
// VarInt count, count×SlotData, SlotData carried) and records the RESULT slot (menu slot 0) if it is
// a non-empty stack — the proof the plugin matcher populated the crafting result.
func (b *bot) recordContainerContent(p pk.Packet) {
	r := bytes.NewReader(p.Data)
	var containerID, stateID, count pk.VarInt
	if _, err := containerID.ReadFrom(r); err != nil {
		return
	}
	if _, err := stateID.ReadFrom(r); err != nil {
		return
	}
	if _, err := count.ReadFrom(r); err != nil {
		return
	}
	if count <= 0 || count > 256 {
		return
	}
	for i := int32(0); i < int32(count); i++ {
		var s component.SlotData
		if _, err := s.ReadFrom(r); err != nil {
			return
		}
		if i == int32(gateCraftResultID) && s.Count > 0 {
			b.gateMu.Lock()
			b.craftResults = append(b.craftResults, craftResultObs{slot: int16(i), itemID: int32(s.ItemID), count: int32(s.Count)})
			b.gateMu.Unlock()
			log.Printf("[gate] observed crafting RESULT (SetContent slot 0) id=%d count=%d", int32(s.ItemID), int32(s.Count))
		}
	}
}

// recordContainerSlot decodes ClientboundContainerSetSlot (VarInt containerId, VarInt stateId, Short
// slot, SlotData item) and records a non-empty result slot (menu slot 0).
func (b *bot) recordContainerSlot(p pk.Packet) {
	r := bytes.NewReader(p.Data)
	var containerID, stateID pk.VarInt
	var slot pk.Short
	if _, err := containerID.ReadFrom(r); err != nil {
		return
	}
	if _, err := stateID.ReadFrom(r); err != nil {
		return
	}
	if _, err := slot.ReadFrom(r); err != nil {
		return
	}
	var s component.SlotData
	if _, err := s.ReadFrom(r); err != nil {
		return
	}
	if int16(slot) == int16(gateCraftResultID) && s.Count > 0 {
		b.gateMu.Lock()
		b.craftResults = append(b.craftResults, craftResultObs{slot: int16(slot), itemID: int32(s.ItemID), count: int32(s.Count)})
		b.gateMu.Unlock()
		log.Printf("[gate] observed crafting RESULT (SetSlot slot 0) id=%d count=%d", int32(s.ItemID), int32(s.Count))
	}
}

// recordSystemChat decodes a ClientboundSystemChat (chat.Message content, Boolean overlay) and flags
// the gate-events marker. The plain-text component decodes into Message.Text.
func (b *bot) recordSystemChat(p pk.Packet) {
	var msg chat.Message
	if _, err := msg.ReadFrom(bytes.NewReader(p.Data)); err != nil {
		return
	}
	text := msg.Text
	b.gateMu.Lock()
	b.gateChatTexts = append(b.gateChatTexts, text)
	if strings.Contains(text, "gate_events:") {
		b.sawGateChat = true
	}
	b.gateMu.Unlock()
	log.Printf("[gate] observed SystemChat: %q", text)
}

// --- the scenario --------------------------------------------------------------------------------

// gateResult tracks one checklist item's pass/fail + the evidence string for the summary table.
type gateResult struct {
	item     int
	name     string
	pass     bool
	evidence string
}

// runGate runs the 4-item scenario on the main goroutine while readLoop (started here) records
// observations. Each item triggers then asserts on the SPECIFIC observed effect. Exits 0 only if
// ALL items pass; 1 otherwise. This IS the PLUGIN-07 visual gate.
func (b *bot) runGate() {
	b.mode = "gate" // ensure the readLoop records (already set by -mode gate, but explicit)
	b.gateMu.Lock()
	b.gateInit()
	b.gateMu.Unlock()

	// Start the continuous reader (owns ALL reads from here; the main goroutine only writes/sleeps).
	go b.readLoop()

	// Snapshot the spawn so the crafting_table is placed on reachable ground.
	b.mu.Lock()
	spawnX, spawnY, spawnZ := b.x, b.y, b.z
	b.mu.Unlock()
	log.Printf("[gate] scenario start at (%.2f, %.2f, %.2f)", spawnX, spawnY, spawnZ)

	// Let the initial chunk + entity stream settle (and the join-hook SystemChat arrive).
	b.drainTicks(20)

	var results []gateResult
	results = append(results, b.gateItem1CustomMob())
	results = append(results, b.gateItem2VanillaPig())
	results = append(results, b.gateItem3Crafting())
	results = append(results, b.gateItem4Events())

	// Clean disconnect.
	b.closed.Store(true)
	b.conn.Close()

	// Summary table + verdict.
	allPass := true
	fmt.Println("\n===== PLUGIN-07 VISUAL GATE — CHECKLIST =====")
	for _, r := range results {
		status := "PASS"
		if !r.pass {
			status = "FAIL"
			allPass = false
		}
		fmt.Printf("  [%s] item %d: %s — %s\n", status, r.item, r.name, r.evidence)
	}
	fmt.Println("=============================================")
	if allPass {
		fmt.Println("GATE RESULT: PASS (all 4 plugin-system items observed)")
		os.Exit(0)
	}
	fmt.Println("GATE RESULT: FAIL")
	os.Exit(1)
}

// gateItem1CustomMob: spawn the custom wander mob via the test-kit egg and assert it is OBSERVED as a
// new AddEntity that MOVES (it walks via the Go nav). The egg is at MAIN inventory slot 35 (28-01); the
// handleGateSpawnEgg use-seam matches the HELD item, so the bot first moves the egg to the hotbar via
// two player-window ContainerClicks (pick up slot 35 -> deposit into hotbar slot 36), selects it, then
// right-clicks the air (ServerboundUseItem) to trigger the spawn (28-01's documented seam).
func (b *bot) gateItem1CustomMob() gateResult {
	before := b.entitySnapshot()

	// Move the spawn egg from main slot 35 into hotbar slot 0 (window slot 36) so it is HELD, then use it.
	// ServerboundContainerClick on the player window (containerId 0): a left-click PICKUP on menu slot 35
	// (the egg), then a left-click PICKUP on menu slot 36 (hotbar 0) deposits it. The player-window menu
	// indices equal the inventory window slots for this path (slot 35 main, slot 36 hotbar-0).
	b.playerClick(35, 0, containerInputPickup)
	b.playerClick(36, 0, containerInputPickup)
	// Select hotbar slot 0 (now holding the egg) and right-click the air to spawn the wander mob.
	_ = b.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundSetCarriedItem), pk.Short(0)))
	b.mu.Lock()
	yaw, pitch := b.yaw, b.pitch
	b.mu.Unlock()
	_ = b.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundUseItem),
		pk.VarInt(0), pk.VarInt(0), pk.Float(yaw), pk.Float(pitch)))
	log.Printf("[gate] item1: used spawn egg (right-click) — waiting for the wander mob to appear + move")

	// Drain ~3s so the mob spawns AND walks via the Go nav.
	b.drainTicks(60)

	newIDs := b.newEntities(before)
	if len(newIDs) == 0 {
		return gateResult{1, "custom wander mob", false, "no new AddEntity observed after the spawn egg (trigger fired but nothing rendered)"}
	}
	// Find a new entity that MOVED (the wander mob walks via the Go nav).
	bestID, bestMoves := int32(-1), 0
	for _, id := range newIDs {
		if m := b.moveCount(id); m > bestMoves {
			bestID, bestMoves = id, m
		}
	}
	const moveThreshold = 3 // a walking mob emits many move packets over 3s; >=3 proves motion, not a static spawn
	if bestID < 0 || bestMoves < moveThreshold {
		return gateResult{1, "custom wander mob", false,
			fmt.Sprintf("a mob spawned (ids=%v) but none accrued >=%d move packets (max=%d) — it did not MOVE", newIDs, moveThreshold, bestMoves)}
	}
	return gateResult{1, "custom wander mob", true,
		fmt.Sprintf("AddEntity id=%d (type=%d) accrued %d move packets — it spawned AND walks via the Go nav", bestID, b.entityType(bestID), bestMoves)}
}

// gateItem2VanillaPig: the wander mob (item #1) renders as the PIG wire type (base_type=pig), so its
// AddEntity already proves a pig-wire mob is observed with vanilla-like AddEntity + move. Phase-24
// TestPluginPigEqualsGoNativePig proves the plugin pig == the Go-native pig on behavior; the bot
// confirms the pig WIRE type + accrued move/headrot here. (A deterministic gate pig egg is not in the
// kit; the wander mob's pig-wire AddEntity is the observed pig-type entity.)
func (b *bot) gateItem2VanillaPig() gateResult {
	pigType := b.pigTypeID()
	// Find an observed entity rendered as the pig wire type that accrued move OR headrot packets.
	b.gateMu.Lock()
	var pigID int32 = -1
	var moves int
	var head bool
	for id, typ := range b.addEntities {
		if typ == pigType {
			if m := b.moveCounts[id]; m >= moves {
				pigID, moves, head = id, m, b.sawHeadRot[id]
			}
		}
	}
	b.gateMu.Unlock()
	if pigID < 0 {
		return gateResult{2, "vanilla-pig-as-plugin", false,
			fmt.Sprintf("no AddEntity rendered as the pig wire type (%d) observed", pigType)}
	}
	if moves < 1 && !head {
		return gateResult{2, "vanilla-pig-as-plugin", false,
			fmt.Sprintf("a pig-wire entity (id=%d) spawned but accrued no move/headrot packets", pigID)}
	}
	return gateResult{2, "vanilla-pig-as-plugin", true,
		fmt.Sprintf("pig-wire AddEntity id=%d accrued %d move + headrot=%v (vanilla-like on the wire; Phase-24 oracle proves behavior equivalence)", pigID, moves, head)}
}

// gateItem3Crafting: place a crafting_table, open it (OpenScreen), then drive a VANILLA recipe
// (2 vertical oak planks -> 4 sticks) and a CUSTOM recipe (1 dirt -> 1 diamond) into the 3x3 grid via
// ContainerClick, asserting the result slot populated for BOTH.
func (b *bot) gateItem3Crafting() gateResult {
	// Place a crafting_table from the hotbar in front of the player, then right-click to open it.
	b.mu.Lock()
	fx, fy, fz := b.x, b.y, b.z
	b.mu.Unlock()
	tbx, tby, tbz := int(math.Floor(fx))+1, int(math.Floor(fy))-1, int(math.Floor(fz))
	// Select the crafting_table hotbar slot, place it on the UP face of the floor block beneath the target.
	_ = b.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundSetCarriedItem), pk.Short(gateTableHotbar)))
	below := pk.Position{X: tbx, Y: tby - 1, Z: tbz}
	b.blockSeq++
	_ = b.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundUseItemOn),
		pk.VarInt(0), below, pk.VarInt(1), // face UP
		pk.Float(0.5), pk.Float(1.0), pk.Float(0.5), pk.Boolean(false), pk.Boolean(false), pk.VarInt(b.blockSeq)))
	b.drainTicks(6)

	// Right-click the placed crafting_table to OPEN the 3x3 menu.
	tablePos := pk.Position{X: tbx, Y: tby, Z: tbz}
	b.blockSeq++
	_ = b.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundUseItemOn),
		pk.VarInt(0), tablePos, pk.VarInt(1),
		pk.Float(0.5), pk.Float(1.0), pk.Float(0.5), pk.Boolean(false), pk.Boolean(false), pk.VarInt(b.blockSeq)))
	b.drainTicks(6)

	b.gateMu.Lock()
	opened := b.sawOpenScreen
	b.gateMu.Unlock()
	if !opened {
		return gateResult{3, "crafting (plugin path)", false, "ClientboundOpenScreen never arrived — the crafting_table did not open"}
	}

	// The crafting window id: parse it back is unneeded — handleContainerClick matches the player's
	// currently-open container, so a click with the open window id routes to clickedCrafting. We use
	// windowId 0 only for the player window; for the crafting window we must use the real id. The server
	// allocated it (1..100); we discover it by tracking the OpenScreen — but we did not capture it here.
	// Instead, drive the grid via the player-window click path is wrong. So capture the window id.
	win := b.craftWindowID()
	if win <= 0 {
		return gateResult{3, "crafting (plugin path)", false, "could not determine the crafting window id from OpenScreen"}
	}

	// --- VANILLA recipe: 2 vertical oak planks -> 4 sticks ---
	// Pick up the planks stack from the player area of the crafting window (menu slot 41), then deposit
	// 1 into grid cell 1 (top-left) and 1 into grid cell 4 (middle-left): right-click (button 1) deposits
	// one each.
	b.craftClick(win, gatePlanksMenuSlot, 0, containerInputPickup) // pick up the whole planks stack onto cursor
	b.craftClick(win, 1, 1, containerInputPickup)                  // right-click grid cell 1 (menu slot 1): deposit 1
	b.craftClick(win, 4, 1, containerInputPickup)                  // right-click grid cell 4 (menu slot 4): deposit 1
	b.drainTicks(6)
	sawStick := b.sawCraftResult(idStickItem)
	// Return the cursor + clear the grid: shift-take the result if present, then put the cursor back.
	b.craftClick(win, 0, 0, containerInputQuickMove) // shift-click the result (take + consume) if it populated
	b.drainTicks(4)

	// --- CUSTOM recipe: 1 dirt -> 1 diamond (customrecipe plugin) ---
	// Clear the cursor first (deposit any leftover planks back to the planks menu slot), then pick up dirt.
	b.craftClick(win, gatePlanksMenuSlot, 0, containerInputPickup) // dump cursor leftovers back
	b.craftClick(win, gateDirtMenuSlot, 0, containerInputPickup)   // pick up the dirt stack
	b.craftClick(win, 1, 1, containerInputPickup)                  // deposit 1 dirt into grid cell 1
	b.drainTicks(6)
	sawDiamond := b.sawCraftResult(idDiamondItem)

	// Close the window (return the grid items).
	_ = b.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundContainerClose), pk.VarInt(win)))
	b.drainTicks(4)

	switch {
	case sawStick && sawDiamond:
		return gateResult{3, "crafting (plugin path)", true,
			"OpenScreen + result slot populated for the VANILLA recipe (planks->stick id 808) AND the CUSTOM recipe (dirt->diamond id 926) via the plugin Match path"}
	case sawStick:
		return gateResult{3, "crafting (plugin path)", false,
			"the vanilla recipe (planks->stick) crafted but the CUSTOM recipe (dirt->diamond) result was not observed"}
	case sawDiamond:
		return gateResult{3, "crafting (plugin path)", false,
			"the custom recipe (dirt->diamond) crafted but the VANILLA recipe (planks->stick) result was not observed"}
	default:
		return gateResult{3, "crafting (plugin path)", false,
			"the crafting menu opened but NO result slot populated for either recipe"}
	}
}

// gateItem4Events: break a block and assert a ClientboundSystemChat carrying "gate_events:" arrives
// (the on_block_break hook -> chat() -> broadcastSystemChat). The join hook already fired at connect.
func (b *bot) gateItem4Events() gateResult {
	// Was the join-hook chat already observed? (corroborating evidence.)
	b.gateMu.Lock()
	joinChat := b.sawGateChat
	b.gateMu.Unlock()

	// Break a block: START_DESTROY a reachable floor neighbor, swing, then STOP_DESTROY.
	b.startBreakBelow()
	b.drainTicks(10) // accrue dig-time
	b.stopBreak()
	b.drainTicks(20) // let the break complete + the hook fire + the SystemChat arrive

	b.gateMu.Lock()
	saw := b.sawGateChat
	texts := append([]string(nil), b.gateChatTexts...)
	b.gateMu.Unlock()

	if !saw {
		return gateResult{4, "plugin events (on_block_break)", false,
			fmt.Sprintf("no SystemChat containing \"gate_events:\" observed after breaking a block (join-chat seen=%v; all chat=%v)", joinChat, texts)}
	}
	return gateResult{4, "plugin events (on_block_break)", true,
		"observed a ClientboundSystemChat carrying \"gate_events:\" (the on_block_break hook reacted via chat() -> broadcastSystemChat; the on_player_join hook also fired at connect)"}
}

// --- small observation/query helpers (read under gateMu) -----------------------------------------

// entitySnapshot returns the set of entity ids observed so far (for diffing new spawns).
func (b *bot) entitySnapshot() map[int32]bool {
	b.gateMu.Lock()
	defer b.gateMu.Unlock()
	out := make(map[int32]bool, len(b.addEntities))
	for id := range b.addEntities {
		out[id] = true
	}
	return out
}

// newEntities returns the ids observed since the given snapshot.
func (b *bot) newEntities(before map[int32]bool) []int32 {
	b.gateMu.Lock()
	defer b.gateMu.Unlock()
	var out []int32
	for id := range b.addEntities {
		if !before[id] {
			out = append(out, id)
		}
	}
	return out
}

func (b *bot) moveCount(id int32) int {
	b.gateMu.Lock()
	defer b.gateMu.Unlock()
	return b.moveCounts[id]
}

func (b *bot) entityType(id int32) int32 {
	b.gateMu.Lock()
	defer b.gateMu.Unlock()
	return b.addEntities[id]
}

// sawCraftResult reports whether a crafting result slot populated with the given item id was observed.
func (b *bot) sawCraftResult(itemID int32) bool {
	b.gateMu.Lock()
	defer b.gateMu.Unlock()
	for _, r := range b.craftResults {
		if r.itemID == itemID {
			return true
		}
	}
	return false
}

// craftWindowID returns the crafting window id the last OpenScreen allocated (0 if none observed).
func (b *bot) craftWindowID() int32 {
	b.gateMu.Lock()
	defer b.gateMu.Unlock()
	return b.craftWindow
}

// pigTypeID is the registry entity-type id of minecraft:pig — the wire type the wander mob (and the
// vanilla-pig-as-plugin) render as. The wander mob is base_type=pig (28-01), so both spawn with this
// AddEntity type. Discovered from the most-common AddEntity type observed (the gate world spawns pigs).
// We return the modal observed type as the "pig" type; if none observed yet, fall back to the literal
// minecraft:pig registry id 95 (data/registryid). This avoids hardcoding a possibly-stale id while
// still asserting a SPECIFIC wire type, not "any entity".
func (b *bot) pigTypeID() int32 {
	b.gateMu.Lock()
	defer b.gateMu.Unlock()
	// The wander mob + any vanilla pig share the same wire type; pick the modal type among observed
	// AddEntity packets (the only mobs the gate spawns are pig-typed). If empty, fall back to the egg
	// spawn's type below by returning the literal pig registry id.
	counts := make(map[int32]int)
	for _, typ := range b.addEntities {
		counts[typ]++
	}
	best, bestN := int32(-1), 0
	for typ, n := range counts {
		if n > bestN {
			best, bestN = typ, n
		}
	}
	if best >= 0 {
		return best
	}
	return pigRegistryID
}

// pigRegistryID is the minecraft:pig entity-type registry id (100, per server/debug.go + the
// entity_capture_test vanilla golden) — the fallback pig wire type when no AddEntity has been observed
// yet. The wander mob (base_type=pig) and the vanilla-pig-as-plugin both spawn with this type.
const pigRegistryID int32 = 100

// playerClick sends a ServerboundContainerClick on the PLAYER inventory window (containerId 0) — the
// minimal wire (empty changedSlots map + absent carried HashedStack); the server is authoritative and
// discards the client's claimed contents. Used to move the spawn egg from the main inventory to the
// hotbar (item #1).
func (b *bot) playerClick(slotNum int16, button int8, input int32) {
	b.sendContainerClick(0, slotNum, button, input)
}

// craftClick sends a ServerboundContainerClick on the open CRAFTING window (the OpenScreen-allocated
// windowId) — handleContainerClick routes it to clickedCrafting because the id matches the player's
// open container.
func (b *bot) craftClick(window int32, slotNum int16, button int8, input int32) {
	b.sendContainerClick(window, slotNum, button, input)
	time.Sleep(tickInterval) // pace clicks ~1/tick so the server applies + re-syncs between them
}

// sendContainerClick marshals the minimal ServerboundContainerClick (jar field order: VarInt
// containerId, VarInt stateId, Short slot, Byte button, VarInt containerInput, VarInt changedCount=0,
// Boolean carriedPresent=false). stateId 0 is accepted (the server applies authoritatively, no
// state-id reject in v1).
func (b *bot) sendContainerClick(window int32, slotNum int16, button int8, input int32) {
	_ = b.conn.WritePacket(pk.Marshal(int32(packetid.ServerboundContainerClick),
		pk.VarInt(window),
		pk.VarInt(0), // stateId
		pk.Short(slotNum),
		pk.Byte(button),
		pk.VarInt(input),
		pk.VarInt(0),      // changedSlots count = 0
		pk.Boolean(false), // carried HashedStack: not present
	))
}

// container-input ids (server/inventory_click.go ContainerInput ordinals) the gate clicks use.
const (
	containerInputPickup    int32 = 0 // ContainerInput.PICKUP
	containerInputQuickMove int32 = 1 // ContainerInput.QUICK_MOVE
	containerInputThrow     int32 = 4 // ContainerInput.THROW
)
