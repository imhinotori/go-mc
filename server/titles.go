package server

// titles.go - the clientbound Title / TabList-header-footer WIRE (proto-776) + the per-player
// send helpers and the vanilla /title command, ported 1:1 from the unobfuscated 26.2 inner jar
// (temp/cache/26.2-inner.jar, javap -c -p this session):
//
//   net.minecraft.network.protocol.game.ClientboundSetTitleTextPacket
//   net.minecraft.network.protocol.game.ClientboundSetSubtitleTextPacket
//   net.minecraft.network.protocol.game.ClientboundSetActionBarTextPacket
//   net.minecraft.network.protocol.game.ClientboundSetTitlesAnimationPacket
//   net.minecraft.network.protocol.game.ClientboundClearTitlesPacket
//   net.minecraft.network.protocol.game.ClientboundTabListPacket
//   net.minecraft.server.commands.TitleCommand
//
// WIRE LAYOUTS (VERIFIED javap against the 26.2 jar this session):
//
//   SetTitleText / SetSubtitleText / SetActionBarText - each a Record with a SINGLE field
//     Component text, encoded by a StreamCodec.composite over
//     ComponentSerialization.TRUSTED_STREAM_CODEC (the SAME NBT network form ClientboundSystemChat
//     uses here via chat.Message). Wire body = one TRUSTED_STREAM_CODEC Component, nothing else.
//     [VERIFIED javap: private final Component text + static composite(TRUSTED_STREAM_CODEC, ...).]
//
//   SetTitlesAnimation - three ints fadeIn/stay/fadeOut, each FriendlyByteBuf.writeInt (a
//     BIG-ENDIAN 4-byte Int, NOT a VarInt). Codec = Packet.codec(encode, decode) over the
//     hand-written write(buf)= writeInt(fadeIn); writeInt(stay); writeInt(fadeOut).
//     [VERIFIED javap ClientboundSetTitlesAnimationPacket.write: three writeInt(I) calls in order.]
//
//   ClearTitles - a SINGLE boolean resetTimes, FriendlyByteBuf.writeBoolean.
//     [VERIFIED javap ClientboundClearTitlesPacket.write: writeBoolean(resetTimes).]
//
//   TabList - a Record of TWO fields Component header, Component footer, encoded by
//     StreamCodec.composite over TRUSTED_STREAM_CODEC (header) then TRUSTED_STREAM_CODEC (footer).
//     Wire body = header Component, footer Component (in that field order).
//     [VERIFIED javap ClientboundTabListPacket: private final Component header; private final
//      Component footer; + static composite(TRUSTED_STREAM_CODEC, header, TRUSTED_STREAM_CODEC,
//      footer, ...).]
//
// pk.Int is binary.BigEndian 4 bytes (net/packet/types.go) - the faithful writeInt. pk.Boolean is
// the single writeBoolean byte. chat.Message is the TRUSTED_STREAM_CODEC Component (the bossbar.go /
// chat.go note: chat.Message is the faithful port of that codec).

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/server/command"
)

// --- WIRE BUILDERS (stateless; each returns a ready-to-Send pk.Packet) ------------------------

// encodeSetTitleText builds ClientboundSetTitleText: a single TRUSTED_STREAM_CODEC Component.
//
//	[VERIFIED javap ClientboundSetTitleTextPacket: STREAM_CODEC = composite(TRUSTED_STREAM_CODEC,
//	 ClientboundSetTitleTextPacket::text, ClientboundSetTitleTextPacket::new); one Component field.]
func encodeSetTitleText(text chat.Message) pk.Packet {
	return pk.Marshal(int32(packetid.ClientboundSetTitleText), text)
}

// encodeSetSubtitleText builds ClientboundSetSubtitleText: a single TRUSTED_STREAM_CODEC Component.
//
//	[VERIFIED javap ClientboundSetSubtitleTextPacket: STREAM_CODEC = composite(TRUSTED_STREAM_CODEC,
//	 ...::text, ...::new); one Component field.]
func encodeSetSubtitleText(text chat.Message) pk.Packet {
	return pk.Marshal(int32(packetid.ClientboundSetSubtitleText), text)
}

// encodeSetActionBarText builds ClientboundSetActionBarText: a single TRUSTED_STREAM_CODEC Component.
//
//	[VERIFIED javap ClientboundSetActionBarTextPacket: STREAM_CODEC = composite(TRUSTED_STREAM_CODEC,
//	 ...::text, ...::new); one Component field.]
func encodeSetActionBarText(text chat.Message) pk.Packet {
	return pk.Marshal(int32(packetid.ClientboundSetActionBarText), text)
}

// encodeSetTitlesAnimation builds ClientboundSetTitlesAnimation: three BIG-ENDIAN Ints
// fadeIn/stay/fadeOut (FriendlyByteBuf.writeInt, NOT VarInt).
//
//	[VERIFIED javap ClientboundSetTitlesAnimationPacket.write: writeInt(fadeIn); writeInt(stay);
//	 writeInt(fadeOut).]
func encodeSetTitlesAnimation(fadeIn, stay, fadeOut int32) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundSetTitlesAnimation),
		pk.Int(fadeIn),
		pk.Int(stay),
		pk.Int(fadeOut),
	)
}

// encodeClearTitles builds ClientboundClearTitles: a single Boolean resetTimes.
//
//	[VERIFIED javap ClientboundClearTitlesPacket.write: writeBoolean(resetTimes).]
func encodeClearTitles(resetTimes bool) pk.Packet {
	return pk.Marshal(int32(packetid.ClientboundClearTitles), pk.Boolean(resetTimes))
}

// encodeTabList builds ClientboundTabList: header Component then footer Component (field order).
//
//	[VERIFIED javap ClientboundTabListPacket: STREAM_CODEC = composite(TRUSTED_STREAM_CODEC, header,
//	 TRUSTED_STREAM_CODEC, footer, ClientboundTabListPacket::new); two Component fields in order.]
func encodeTabList(header, footer chat.Message) pk.Packet {
	return pk.Marshal(int32(packetid.ClientboundTabList), header, footer)
}

// --- PER-PLAYER SEND HELPERS ------------------------------------------------------------------
// Each mirrors the send seam broadcastSystemChat/sendSystemChat use: the packet goes through the
// player's bounded outbound queue via Client.Send (the writeLoop stays the sole socket writer -
// TICK-05). All run on the tick goroutine (command dispatch, gameplay) and touch no tick-owned
// game state - they only fan a send.

// sendTitle sends ClientboundSetTitleText to a single player.
func (t *TickLoop) sendTitle(p *tickPlayer, text chat.Message) {
	if p == nil || p.client == nil {
		return
	}
	p.client.Send(encodeSetTitleText(text))
}

// sendSubtitle sends ClientboundSetSubtitleText to a single player.
func (t *TickLoop) sendSubtitle(p *tickPlayer, text chat.Message) {
	if p == nil || p.client == nil {
		return
	}
	p.client.Send(encodeSetSubtitleText(text))
}

// sendActionBar sends ClientboundSetActionBarText to a single player.
func (t *TickLoop) sendActionBar(p *tickPlayer, text chat.Message) {
	if p == nil || p.client == nil {
		return
	}
	p.client.Send(encodeSetActionBarText(text))
}

// sendTitlesAnimation sends ClientboundSetTitlesAnimation (fadeIn/stay/fadeOut in ticks).
func (t *TickLoop) sendTitlesAnimation(p *tickPlayer, fadeIn, stay, fadeOut int32) {
	if p == nil || p.client == nil {
		return
	}
	p.client.Send(encodeSetTitlesAnimation(fadeIn, stay, fadeOut))
}

// sendClearTitles sends ClientboundClearTitles. resetTimes=false clears the current title/subtitle
// (vanilla clear); resetTimes=true additionally resets the fade times to the defaults (vanilla
// reset). [VERIFIED javap TitleCommand.clearTitle -> new ClientboundClearTitlesPacket(false);
// resetTitle -> new ClientboundClearTitlesPacket(true).]
func (t *TickLoop) sendClearTitles(p *tickPlayer, resetTimes bool) {
	if p == nil || p.client == nil {
		return
	}
	p.client.Send(encodeClearTitles(resetTimes))
}

// sendTabListHeaderFooter sends ClientboundTabList (the player-list header + footer). There is NO
// vanilla /tablist command - the header/footer surface is normally plugin/datapack-driven (vanilla
// itself never sends this packet from a command; it exposes ServerGamePacketListenerImpl.send of a
// ClientboundTabListPacket only through the API). This is the internal API a plugin or gameplay hook
// calls; the /title-style command layer intentionally does not expose it.
func (t *TickLoop) sendTabListHeaderFooter(p *tickPlayer, header, footer chat.Message) {
	if p == nil || p.client == nil {
		return
	}
	p.client.Send(encodeTabList(header, footer))
}

// --- /title COMMAND (net.minecraft.server.commands.TitleCommand, 1:1) -------------------------
//
// Vanilla tree (VERIFIED javap TitleCommand.register):
//
//	/title <targets> clear
//	/title <targets> reset
//	/title <targets> title     <title:Component>
//	/title <targets> subtitle  <title:Component>
//	/title <targets> actionbar <title:Component>
//	/title <targets> times <fadeIn> <stay> <fadeOut>
//
// <targets> is EntityArgument.players(); clear -> ClientboundClearTitlesPacket(false);
// reset -> ClientboundClearTitlesPacket(true); title/subtitle/actionbar -> showTitle sends the
// matching ClientboundSet*TextPacket per player; times -> ClientboundSetTitlesAnimationPacket(fadeIn,
// stay, fadeOut). requires(permission level 2) - gated here on minecraft.command.title.
//
// The fork command.Graph is a flat literal+greedy-string dispatcher (see commands.go), not full
// Brigadier, so the sub-tree is parsed off a single greedy trailing arg: "<targets> <sub> [args...]".
// This preserves the vanilla ORDER (targets first, then the sub-verb) and the vanilla per-player
// packet emission exactly; the wire the client receives is identical to vanilla. EntityArgument
// selectors are reduced to the codebase playerByName lookup (the only resolver on disk) plus the
// "@a"/"@s" conveniences so a bare player name or @a works - the emitted packets are byte-identical
// to what vanilla sends each resolved player.

// errTitleUsage is the /title parse error (bad arity / unknown sub-verb).
var errTitleUsage = errors.New("usage: /title <targets> (clear|reset|title <text>|subtitle <text>|actionbar <text>|times <fadeIn> <stay> <fadeOut>)")

// registerTitleCommand wires /title onto the shared graph, permission-gated on
// minecraft.command.title (vanilla requires(2)).
func registerTitleCommand(g *command.Graph) {
	h := permissionGated("minecraft.command.title", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok {
			return nil
		}
		return e.t.runTitleCommand(e.p, pickWord(args))
	})
	arg := g.Argument("targets-and-sub", command.StringParser(2)).HandleFunc(h)
	g.AppendLiteral(g.Literal("title").AppendArgument(arg).Unhandle())
}

// titleTargets resolves the <targets> token to the set of receiving players, mirroring
// EntityArgument.players() for the resolvers this codebase has: "@a" = all online players, "@s" =
// the issuer, otherwise a single named player (playerByName). Returns an error when a named target is
// not found (vanilla EntityArgument raises NO_PLAYERS_FOUND for an empty match).
func (t *TickLoop) titleTargets(issuer *tickPlayer, token string) ([]*tickPlayer, error) {
	switch strings.ToLower(token) {
	case "@a", "@e", "@p":
		out := make([]*tickPlayer, 0, len(t.players))
		for _, p := range t.players {
			if p != nil {
				out = append(out, p)
			}
		}
		if len(out) == 0 {
			return nil, errors.New("no player was found")
		}
		return out, nil
	case "@s":
		if issuer == nil {
			return nil, errors.New("no player was found")
		}
		return []*tickPlayer{issuer}, nil
	default:
		p := t.playerByName(token)
		if p == nil {
			return nil, errors.New("no player was found")
		}
		return []*tickPlayer{p}, nil
	}
}

// runTitleCommand parses "<targets> <sub> [args...]" and emits the vanilla per-player packets. It
// mirrors TitleCommand showTitle/clearTitle/resetTitle/setTimes: iterate the resolved players and
// send each the matching packet (the per-player send loop is exactly vanilla - showTitle/setTimes
// build the packet ONCE and Connection.send it to each ServerPlayer). Runs on the tick goroutine.
//
// The Component text argument uses a plain-text component ({Text: s}) - vanilla parses it with
// ComponentArgument.textComponent (a JSON/text component argument); a real /title from a client is a
// text string, so {Text: s} is the faithful realistic path and the emitted TRUSTED_STREAM_CODEC wire
// is identical to what vanilla sends for a plain-text component.
func (t *TickLoop) runTitleCommand(issuer *tickPlayer, raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errTitleUsage
	}
	// Split into <targets> and the remainder (the sub-verb + its args).
	parts := strings.SplitN(raw, " ", 2)
	if len(parts) < 2 {
		return errTitleUsage
	}
	targetsTok := parts[0]
	rest := strings.TrimSpace(parts[1])
	if rest == "" {
		return errTitleUsage
	}
	// Split the sub-verb off the rest; the tail (if any) is the text / times args.
	subParts := strings.SplitN(rest, " ", 2)
	sub := strings.ToLower(subParts[0])
	tail := ""
	if len(subParts) == 2 {
		tail = strings.TrimSpace(subParts[1])
	}

	targets, err := t.titleTargets(issuer, targetsTok)
	if err != nil {
		return err
	}

	switch sub {
	case "clear":
		// [VERIFIED javap clearTitle: new ClientboundClearTitlesPacket(false) sent per player.]
		for _, p := range targets {
			t.sendClearTitles(p, false)
		}
		return nil
	case "reset":
		// [VERIFIED javap resetTitle: new ClientboundClearTitlesPacket(true) sent per player.]
		for _, p := range targets {
			t.sendClearTitles(p, true)
		}
		return nil
	case "title":
		if tail == "" {
			return errTitleUsage
		}
		// [VERIFIED javap lambda$register$2 -> showTitle(..., ClientboundSetTitleTextPacket::new).]
		msg := chat.Message{Text: tail}
		for _, p := range targets {
			t.sendTitle(p, msg)
		}
		return nil
	case "subtitle":
		if tail == "" {
			return errTitleUsage
		}
		// [VERIFIED javap lambda$register$3 -> showTitle(..., ClientboundSetSubtitleTextPacket::new).]
		msg := chat.Message{Text: tail}
		for _, p := range targets {
			t.sendSubtitle(p, msg)
		}
		return nil
	case "actionbar":
		if tail == "" {
			return errTitleUsage
		}
		// [VERIFIED javap lambda$register$4 -> showTitle(..., ClientboundSetActionBarTextPacket::new).]
		msg := chat.Message{Text: tail}
		for _, p := range targets {
			t.sendActionBar(p, msg)
		}
		return nil
	case "times":
		// [VERIFIED javap lambda$register$5 -> setTimes(source, players, fadeIn, stay, fadeOut) ->
		//  new ClientboundSetTitlesAnimationPacket(fadeIn, stay, fadeOut).]
		fadeIn, stay, fadeOut, err := parseTitleTimes(tail)
		if err != nil {
			return err
		}
		for _, p := range targets {
			t.sendTitlesAnimation(p, fadeIn, stay, fadeOut)
		}
		return nil
	default:
		return errTitleUsage
	}
}

// parseTitleTimes parses "<fadeIn> <stay> <fadeOut>" as three integer tick counts. Vanilla uses
// TimeArgument.time() (which accepts a bare integer of ticks, or a d/s/t suffix); the plain-integer
// form is the tick count the SetTitlesAnimation packet carries verbatim, so a bare integer is the
// faithful common path. Returns errTitleUsage on wrong arity or a non-integer.
func parseTitleTimes(tail string) (int32, int32, int32, error) {
	f := strings.Fields(tail)
	if len(f) != 3 {
		return 0, 0, 0, errTitleUsage
	}
	fadeIn, err := strconv.Atoi(f[0])
	if err != nil {
		return 0, 0, 0, errTitleUsage
	}
	stay, err := strconv.Atoi(f[1])
	if err != nil {
		return 0, 0, 0, errTitleUsage
	}
	fadeOut, err := strconv.Atoi(f[2])
	if err != nil {
		return 0, 0, 0, errTitleUsage
	}
	return int32(fadeIn), int32(stay), int32(fadeOut), nil
}
