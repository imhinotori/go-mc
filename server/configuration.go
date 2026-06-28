package server

import (
	"github.com/imhinotori/sulfur/bot"
	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/net"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/server/registrydata"
)

// ConfigHandler drives the Configuration protocol state for a freshly logged-in
// connection. AcceptConfig must complete the full ordered 26.2 Configuration
// sequence and only return nil once the client has acknowledged Finish
// Configuration — at which point server.AcceptConn transitions the connection to
// Play. Any failure returns a ConfigFailErr so the caller can emit a readable
// ClientboundConfigDisconnect (NET-07) instead of a silent kick.
type ConfigHandler interface {
	// AcceptConfig drives the Configuration sequence and returns the client's reported displayed
	// skin parts (modelCustomisation, captured from a CONFIG-state Client Information packet; 0 if
	// none) so the joining player spawns with its overlay layers already set (BUG-4).
	AcceptConfig(conn *net.Conn) (uint8, error)
}

// Configurations is the default ConfigHandler. The 26.2 registry payload is no
// longer carried on this struct: it is sourced from the embedded real 26.2 NBT in
// server/registrydata (Plan 02-03) at send time, not from the stale
// registry.Registries flat-schema struct (which the 26.2 client silently rejects).
// The type is intentionally fieldless so the assembly in cmd/ender stays trivial
// while the send path owns the authoritative payload.
type Configurations struct{}

// AcceptConfig implements the full ordered vanilla 26.2 Configuration sequence
// (NET-04). The order is fixed and confirmed against bot/configuration.go (the
// authoritative client wire reference) and a vanilla 26.2 capture-diff:
//
//	S→C Select Known Packs [{minecraft,core,<ProtocolName>}]   (MUST precede Registry Data)
//	C→S Select Known Packs echo                                 (the drain-loop exit key)
//	C→S Client Information                                      (OPTIONAL — consumed/no-op)
//	S→C Update Enabled Features [minecraft:vanilla]
//	S→C Registry Data × N                                       (registrydata.WriteRegistryData)
//	S→C Update Tags                                             (registrydata.WriteTags)
//	S→C Finish Configuration
//	C→S Acknowledge Finish Configuration                        (READ before Play)
//
// The serverbound drain loop dispatches by packet ID and EXITS ONLY on the Known
// Packs echo (ServerboundConfigSelectKnownPacks). Client Information is OPTIONAL
// and never a blocking precondition — the fork's bot/configuration.go test client
// does not send it, so requiring it would deadlock TestConfigSequence. KeepAlive
// and Pong are answered inline; unknown/unhandled IDs (including the new 26.2
// CodeOfConduct accept) are no-ops, so a hostile or chatty client cannot crash the
// loop (T-2-10). Every failure path returns a ConfigFailErr with readable text.
func (c *Configurations) AcceptConfig(conn *net.Conn) (uint8, error) {
	// skinParts captures the modelCustomisation byte from a CONFIG-state Client Information
	// packet (BUG-4): a 26.2 client reports its displayed skin layers (hat/jacket/sleeves) in
	// the CONFIGURATION state, before PLAY. Capturing it here lets the joining player's entity
	// spawn WITH its overlay layers already set, instead of waiting for the (later, sometimes
	// absent) PLAY-state Client Information to flip them on. 0 = none reported (Steve default).
	var skinParts uint8

	// 1. Select Known Packs FIRST — it governs registry NBT omission and MUST be
	//    sent before Registry Data (Pitfall 2). v1 sends full NBT regardless.
	corePack := []bot.DataPack{{Namespace: "minecraft", ID: "core", Version: ProtocolName}}
	if err := conn.WritePacket(pk.Marshal(
		packetid.ClientboundConfigSelectKnownPacks,
		pk.Array(corePack),
	)); err != nil {
		return 0, ConfigFailErr{reason: chat.Text("failed to send known packs")}
	}

	// 2. Drain serverbound config replies until the Known Packs echo arrives.
	//    Answer KeepAlive/Pong inline; capture Client Information's skin parts; consume
	//    unknown IDs as no-ops. The echo (ServerboundConfigSelectKnownPacks) is the SOLE exit key.
	if err := c.drainKnownPacksEcho(conn, &skinParts); err != nil {
		return 0, err
	}

	// 3. Update Enabled Features [minecraft:vanilla] (Feature Flags).
	if err := conn.WritePacket(pk.Marshal(
		packetid.ClientboundConfigUpdateEnabledFeatures,
		pk.Array([]pk.Identifier{"minecraft:vanilla"}),
	)); err != nil {
		return 0, ConfigFailErr{reason: chat.Text("failed to send enabled features")}
	}

	// 4. Registry Data + Update Tags from the embedded real 26.2 NBT (Plan 02-03).
	if err := registrydata.WriteRegistryData(conn); err != nil {
		return 0, ConfigFailErr{reason: chat.Text("registry data send failed")}
	}
	if err := registrydata.WriteTags(conn); err != nil {
		return 0, ConfigFailErr{reason: chat.Text("update tags send failed")}
	}

	// 5. Finish Configuration, then BLOCK reading the Acknowledge (Pitfall 3 — the
	//    stub returned right after Finish, desyncing the client into a silent hang).
	if err := conn.WritePacket(pk.Marshal(
		packetid.ClientboundConfigFinishConfiguration,
	)); err != nil {
		return 0, ConfigFailErr{reason: chat.Text("finish config send failed")}
	}

	var ack pk.Packet
	if err := conn.ReadPacket(&ack); err != nil {
		return 0, ConfigFailErr{reason: chat.Text("failed reading finish-config acknowledge")}
	}
	if packetid.ServerboundPacketID(ack.ID) != packetid.ServerboundConfigFinishConfiguration {
		return 0, ConfigFailErr{reason: chat.Text("expected finish-config acknowledge")}
	}

	return skinParts, nil
}

// drainKnownPacksEcho reads serverbound config packets in a loop, dispatching by
// ID, until it observes the Known Packs echo (ServerboundConfigSelectKnownPacks)
// — the SOLE loop-exit key. KeepAlive and Pong are answered inline. Client
// Information and any unknown/unhandled ID (including ServerboundConfigAccept-
// CodeOfConduct) are consumed as no-ops, never a blocking precondition and never a
// crash (T-2-10). A read error returns a ConfigFailErr.
func (c *Configurations) drainKnownPacksEcho(conn *net.Conn, skinParts *uint8) error {
	for {
		var p pk.Packet
		if err := conn.ReadPacket(&p); err != nil {
			return ConfigFailErr{reason: chat.Text("failed reading config reply")}
		}

		switch packetid.ServerboundPacketID(p.ID) {
		case packetid.ServerboundConfigSelectKnownPacks:
			// The drain-loop exit key. The body (the client's known-pack list) is
			// not consulted: v1 sends full registry NBT regardless of omission.
			return nil

		case packetid.ServerboundConfigKeepAlive:
			var id pk.Long
			if err := p.Scan(&id); err != nil {
				return ConfigFailErr{reason: chat.Text("malformed config keep-alive")}
			}
			if err := conn.WritePacket(pk.Marshal(
				packetid.ClientboundConfigKeepAlive, id,
			)); err != nil {
				return ConfigFailErr{reason: chat.Text("failed answering config keep-alive")}
			}

		case packetid.ServerboundConfigPong:
			// Pong needs no response. Consume and continue.

		case packetid.ServerboundConfigClientInformation:
			// OPTIONAL — never a precondition for proceeding (the bot/test client does not
			// send it; blocking here deadlocks TestConfigSequence). We DO capture the
			// modelCustomisation byte (5th field) so the player spawns with its skin overlay
			// layers (BUG-4). Wire order (ServerboundClientInformation): readUtf(16) language,
			// readByte viewDistance, readEnum(VarInt) chatVisibility, readBoolean chatColors,
			// readUnsignedByte modelCustomisation. A malformed/short packet is tolerated as a
			// no-op (no skin update), never a kick — it stays optional.
			var (
				language       pk.String
				viewDistance   pk.Byte
				chatVisibility pk.VarInt
				chatColors     pk.Boolean
				modelCustom    pk.UnsignedByte
			)
			if err := p.Scan(&language, &viewDistance, &chatVisibility, &chatColors, &modelCustom); err == nil && skinParts != nil {
				*skinParts = uint8(modelCustom)
			}

		default:
			// Any unknown/unhandled ID (including ServerboundConfigAcceptCodeOf
			// Conduct, new in 26.2) is a no-op: log nothing, never panic/return,
			// keep draining until the Known Packs echo (T-2-10).
		}
	}
}

// ConfigFailErr wraps a readable disconnect reason. server.AcceptConn maps it to a
// ClientboundConfigDisconnect carrying reason, so a config failure is always a
// readable kick, never a silent close (NET-07 / T-2-05).
type ConfigFailErr struct {
	reason chat.Message
}

func (c ConfigFailErr) Error() string {
	return "config error: " + c.reason.ClearString()
}
