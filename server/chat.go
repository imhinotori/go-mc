package server

import (
	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// chat.go implements CMD-02: receive a player's chat message and broadcast it to every
// player. v1 broadcasts via ClientboundSystemChat (resolved Open Question 2): a server-
// attributed "<name> message" that SKIPS the signed-message chain entirely. The signed
// ClientboundPlayerChat path is documented below but deferred to v2/ONLINE.
//
// Everything here runs SYNCHRONOUSLY on the tick goroutine (TICK-05): handleChat is called
// inline from dispatch, and the fan-out iterates the tick-owned t.players slice and enqueues
// each send via Client.Send (the bounded outbound queue; the writeLoop stays the sole socket
// writer). No new goroutine, no off-tick game-state access, ZERO new dependencies
// (chat.Message is the fork's NBT Component encoder, already a pk.FieldEncoder).
//
// --- ServerboundChat wire (JAR-VERIFIED: javap -p -c net.minecraft.network.protocol.game.
// ServerboundChatPacket against temp/cache/26.2-inner.jar this session) ---
// The read constructor decodes, in order:
//
//	readUtf(256)                       -> message  (String, capped at 256 chars)
//	readInstant()                      -> timeStamp (Long ms on the wire)
//	readLong()                         -> salt     (Long)
//	readNullable(MessageSignature)     -> signature (Boolean-present prefix + fixed sig bytes)
//	new LastSeenMessages$Update(buf)   -> lastSeenMessages (VarInt + bitset)
//
// v1 keeps ONLY the message string and IGNORES the signature/lastSeen: the server is
// AUTHORITATIVE in offline mode, attributing the message to the connection's login-profile
// name (tickPlayer.name), never trusting a client-supplied signature (T-7-07, accepted).
// The decode is DEFENSIVE: we decode only the leading message String and tolerate the rest,
// so a Scan error is a no-op, never a panic (T-3-02). The readUtf(256) cap bounds the
// message length (T-7-06 / ASVS V5) — even a hostile client cannot inflate it past 256.
//
// --- ClientboundSystemChat wire (JAR-VERIFIED: javap ClientboundSystemChatPacket) ---
// A record of exactly TWO fields:
//
//	Component content   (chat.Message NBT Component — the message body)
//	boolean overlay     (false => the chat box, not the action-bar overlay)
//
// broadcastSystemChat marshals pk.Marshal(ClientboundSystemChat, chat.Message{Text: text},
// pk.Boolean(false)) — content + overlay=false — and fans it to every player.
//
// --- ClientboundPlayerChat globalIndex layout (JAR-VERIFIED: javap -p -c
// ClientboundPlayerChatPacket) — DOCUMENTED to satisfy CMD-02's literal "PlayerChat
// globalIndex prefix handled" ask, while v1 ships SystemChat instead ---
// The read/write order (record field order, confirmed by the RegistryFriendlyByteBuf
// constructor bytecode) is, with globalIndex as FIELD 1 (the leading VarInt prefix):
//
//	 1. globalIndex      VarInt                    <-- the globalIndex prefix (FIELD 1)
//	 2. sender           UUID
//	 3. index            VarInt
//	 4. signature        nullable MessageSignature
//	 5. body             SignedMessageBody$Packed  (content / timeStamp / salt / lastSeen)
//	 6. unsignedContent  nullable Component        (TRUSTED_STREAM_CODEC)
//	 7. filterMask       FilterMask
//	 8. chatType         ChatType$Bound            (Holder<ChatType> + name + Optional<target>)
//
// We do NOT ship this path: it drags MessageSignature + SignedMessageBody$Packed +
// FilterMask + ChatType$Bound + a chat session the offline client never establishes, and a
// single wrong field disconnects the client (Pitfall 2). SystemChat renders identically as a
// server message without any of that. The globalIndex requirement is satisfied by
// UNDERSTANDING + DOCUMENTING the layout (it is the leading VarInt); the signed path is
// deferred to v2/ONLINE.

// maxChatLen bounds the inbound chat message length on the server side, mirroring the wire's
// readUtf(256) cap (the jar-verified ServerboundChat decode caps the message at 256 chars).
// The wire decode already bounds it, but we assert the bound explicitly so an over-long
// message is truncated before it reaches the broadcast (T-7-06 / ASVS V5). A real vanilla
// client never sends more than 256 chars of chat.
const maxChatLen = 256

// handleChat decodes a ServerboundChat packet DEFENSIVELY and broadcasts the message to every
// player as a server-attributed ClientboundSystemChat. It runs INLINE on the tick goroutine
// (called from dispatch) — chat is not a timestamped movement input, so it is NOT buffered
// through the subtick path; it mutates no per-player game state, only fans a send to the
// bounded outbound queues (TICK-05).
//
// Only the leading message String is decoded; the timeStamp/salt/signature/lastSeen are
// IGNORED (offline mode, server-authoritative). A Scan error is a silent no-op, never a panic
// (T-3-02). The message is length-bounded (maxChatLen) before the broadcast.
func (t *TickLoop) handleChat(p *tickPlayer, raw pk.Packet) {
	if p == nil {
		return
	}
	// DEFENSIVE decode: read ONLY the leading message String. The trailing
	// timeStamp/salt/signature/lastSeen fields are intentionally not scanned — v1 ignores
	// them, and decoding only the prefix tolerates any trailing-layout drift (a Scan error
	// on the leading String alone is a no-op, never a panic — T-3-02).
	var msg pk.String
	if err := raw.Scan(&msg); err != nil {
		return // malformed payload: silent no-op (T-3-02)
	}

	// Bound the message length (the wire caps at readUtf(256); assert it explicitly so the
	// broadcast does no unbounded work — T-7-06 / ASVS V5).
	text := string(msg)
	if len(text) > maxChatLen {
		text = text[:maxChatLen]
	}

	// Server-AUTHORITATIVE attribution: the "<name>" comes from the connection's login
	// profile (tickPlayer.name), never from any client-supplied field (T-7-07).
	t.broadcastSystemChat("<" + p.name + "> " + text)
}

// broadcastSystemChat fans a ClientboundSystemChat carrying text (as the NBT Component
// content, overlay=false) to EVERY player's bounded outbound queue. It runs on the tick
// goroutine over the tick-owned t.players slice; each send goes through Client.Send (the
// writeLoop stays the sole socket writer — TICK-05 / T-7-08). The packet is marshalled ONCE
// and the same immutable pk.Packet is enqueued per player (a pk.Packet is read-only after
// Marshal, so sharing it across queues is safe).
func (t *TickLoop) broadcastSystemChat(text string) {
	out := pk.Marshal(int32(packetid.ClientboundSystemChat), chat.Message{Text: text}, pk.Boolean(false))
	for _, pl := range t.players {
		if pl != nil && pl.client != nil {
			pl.client.Send(out)
		}
	}
}

// sendSystemChat delivers a ClientboundSystemChat (text as the NBT Component content,
// overlay=false) to a SINGLE player — the single-target variant of broadcastSystemChat. It is
// the command reply path: runCommand (CMD-01, Plan 07-04) uses it to tell the player a command
// failed/succeeded, consolidating the reply 07-04 left as a documented stub. Runs on the tick
// goroutine; the send goes through the player's bounded outbound queue (TICK-05).
func (t *TickLoop) sendSystemChat(p *tickPlayer, text string) {
	if p == nil || p.client == nil {
		return
	}
	out := pk.Marshal(int32(packetid.ClientboundSystemChat), chat.Message{Text: text}, pk.Boolean(false))
	p.client.Send(out)
}
