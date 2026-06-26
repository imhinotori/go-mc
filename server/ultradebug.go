package server

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/imhinotori/sulfur/data/packetid"
)

// ultradebug.go is the SULFUR_ULTRA_DEBUG firehose: an opt-in, category-tagged trace of
// essentially everything the server does on the tick goroutine — inbound packets (decoded),
// per-tick player state (position, velocity, in-water/eye-in-water, air, food, health, dig/use
// state), fluid detection, block edits, combat, eating, and the collision clamp. It exists so a
// developer (or this agent) can read the COMPLETE runtime context of a gate session from the log
// instead of guessing — every gameplay decision the server makes is greppable by category.
//
// It is GATED by the SULFUR_ULTRA_DEBUG env var (read ONCE at process start, cached) so the
// default path pays nothing: udebugEnabled is a single bool test, and when off every udebug* call
// is an immediate return before any formatting. All output goes to the same stderr logger as the
// rest of the server (log.Default), prefixed `ULTRA[<category>]` so a session can be filtered with
// `grep 'ULTRA\[water\]'` etc. Tick-owned: every call site is on the tick goroutine over tick-owned
// state (TICK-05), so the firehose introduces no new synchronization.
//
// Categories (the `cat` argument — keep them stable so greps stay valid):
//
//	packet   — every inbound Serverbound packet, decoded to its gameplay-relevant fields
//	move     — accepted player position/rotation + the per-tick delta and onGround flag
//	water    — fluid detection: playerInWater / eyeInWater / the fluid cell under feet+eyes
//	tick     — the once-per-N-ticks player state snapshot (pos/vel/air/food/health/dig/use)
//	fluid    — fluid sim: schedule + spread + the neighbor-reschedule on a block edit
//	edit     — block break/place: pos, old→new state, sequence
//	combat   — attack dispatch + damage applied + i-frames
//	eat      — item-use / eating: start, per-tick remaining, completion, food restored
//	collide  — the anti-clip collision clamp: claimed vs accepted position

// udebugEnabled is read once at init from SULFUR_ULTRA_DEBUG (== "1" enables). A package-level
// bool so the hot path is a single load+branch; flipping it needs a restart (matching the other
// SULFUR_* env toggles like SULFUR_DEBUG / SULFUR_TEST_KIT).
var udebugEnabled = os.Getenv("SULFUR_ULTRA_DEBUG") == "1"

// udebug emits one category-tagged firehose line when SULFUR_ULTRA_DEBUG=1, else returns
// immediately (the off-path cost is one bool test, no formatting). The line is
// `ULTRA[<cat>] <message>` on the shared stderr logger.
func udebug(cat, format string, args ...any) {
	if !udebugEnabled {
		return
	}
	log.Printf("ULTRA[%s] %s", cat, fmt.Sprintf(format, args...))
}

// udebugPlayer prefixes a firehose line with the player's identity (name + entity id) so a
// multi-player session stays attributable. Same gating/cost contract as udebug.
func udebugPlayer(p *tickPlayer, cat, format string, args ...any) {
	if !udebugEnabled {
		return
	}
	name := "?"
	if p != nil {
		name = p.name
	}
	id := int32(-1)
	if p != nil {
		id = p.entityID
	}
	log.Printf("ULTRA[%s] [%s#%d] %s", cat, name, id, fmt.Sprintf(format, args...))
}

// packetName maps an inbound Serverbound packet id to a short stable label for the `packet`
// category, so the firehose reads `MovePlayerPos` instead of a bare numeric id. An unknown id
// falls back to its decimal value. Only the play-state packets applyInput dispatches are named
// (the firehose is a gameplay trace, not a full protocol enumeration). The switch is a tiny,
// allocation-free lookup on the hot dispatch path keyed on the generated packetid constants.
func packetName(id int32) string {
	switch packetid.ServerboundPacketID(id) {
	case packetid.ServerboundMovePlayerPos:
		return "MovePlayerPos"
	case packetid.ServerboundMovePlayerPosRot:
		return "MovePlayerPosRot"
	case packetid.ServerboundMovePlayerRot:
		return "MovePlayerRot"
	case packetid.ServerboundMovePlayerStatusOnly:
		return "MovePlayerStatusOnly"
	case packetid.ServerboundPlayerAction:
		return "PlayerAction"
	case packetid.ServerboundUseItemOn:
		return "UseItemOn"
	case packetid.ServerboundUseItem:
		return "UseItem"
	case packetid.ServerboundContainerClick:
		return "ContainerClick"
	case packetid.ServerboundSetCreativeModeSlot:
		return "SetCreativeModeSlot"
	case packetid.ServerboundSetCarriedItem:
		return "SetCarriedItem"
	case packetid.ServerboundContainerClose:
		return "ContainerClose"
	case packetid.ServerboundAttack:
		return "Attack"
	case packetid.ServerboundInteract:
		return "Interact"
	default:
		return fmt.Sprintf("id=%d", id)
	}
}

// udebugWaterColumn formats the fluid cells around a player (feet + eye) for the `water` category —
// the single most useful line for the in-water physics gate. It samples the block at the feet
// (floor x,y,z), the eye (y+1.62), and the cell below the feet, reporting which are water. This is
// exactly the state the client-authoritative water physics depends on, so a mismatch between what
// the server sees here and what the client does is immediately visible in the log.
func (t *TickLoop) udebugWaterColumn(p *tickPlayer) {
	if !udebugEnabled || t == nil || p == nil {
		return
	}
	inWater := t.playerInWater(p)
	eye := t.eyeInWater(p)
	udebugPlayer(p, "water", "pos=(%.2f,%.2f,%.2f) onGround=%v playerInWater=%v eyeInWater=%v air=%d",
		p.x, p.y, p.z, p.onGround, inWater, eye, p.airSupply)
}

// udebugInboundPacket logs one decoded inbound packet for the `packet` category. The raw byte
// length is included so a malformed/short payload (which the handlers silently drop) is still
// visible in the trace. Called at the top of applyInput, before the teleport gate, so even
// pre-confirm packets are recorded.
func (t *TickLoop) udebugInboundPacket(p *tickPlayer, in SubtickInput) {
	if !udebugEnabled {
		return
	}
	udebugPlayer(p, "packet", "%s bytes=%d confirmed=%v", packetName(in.Packet.ID), len(in.Packet.Data), p.confirmedTeleport)
}

// udebugTickSnapshot logs the once-per-interval full player state snapshot for the `tick` category.
// Run from the per-player tick phase, throttled by udebugTickEvery so the firehose stays readable
// (a 20-line/s/player snapshot, not a 20× burst). Carries everything needed to reconstruct the
// player's gameplay state at that instant.
func (t *TickLoop) udebugTickSnapshot(p *tickPlayer) {
	if !udebugEnabled || p == nil {
		return
	}
	dx, dy, dz := p.x-p.prevX, p.y-p.prevY, p.z-p.prevZ
	flags := []string{}
	if p.onGround {
		flags = append(flags, "ground")
	}
	if t.playerInWater(p) {
		flags = append(flags, "water")
	}
	if t.eyeInWater(p) {
		flags = append(flags, "eyeWater")
	}
	if p.isDestroyingBlock {
		flags = append(flags, "digging")
	}
	if p.useItemRemaining > 0 {
		flags = append(flags, fmt.Sprintf("using(%d)", p.useItemRemaining))
	}
	if p.dead {
		flags = append(flags, "dead")
	}
	udebugPlayer(p, "tick", "pos=(%.3f,%.3f,%.3f) d=(%.3f,%.3f,%.3f) yaw=%.1f pitch=%.1f hp=%.1f food=%d sat=%.1f air=%d exh=%.2f [%s]",
		p.x, p.y, p.z, dx, dy, dz, p.yaw, p.pitch, p.health, p.food, p.saturation, p.airSupply, p.exhaustion, strings.Join(flags, ","))
}

// udebugTickEvery is the snapshot throttle: emit the `tick` snapshot every N ticks per player so a
// long session log stays readable. 10 == twice a second at 20 TPS — dense enough to watch a
// water-entry transition, sparse enough not to drown the other categories.
const udebugTickEvery = 10
