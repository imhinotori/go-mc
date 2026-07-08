package server

// scoreboard_packets.go -- the five clientbound scoreboard/teams packet WIRE encoders
// (proto-776), ported 1:1 from the unobfuscated 26.2 inner jar (temp/cache/26.2-inner.jar,
// javap -c -p this session):
//   ClientboundSetObjectivePacket 106, ClientboundSetScorePacket 110, ClientboundResetScorePacket 79,
//   ClientboundSetDisplayObjectivePacket 98, ClientboundSetPlayerTeamPacket 109.
// IDs READ from data/packetid/packetid.go + clientboundpacketid_string.go.
// Component fields are ComponentSerialization.TRUSTED_STREAM_CODEC (NBT network form) -- same codec
// ClientboundSystemChat/BossEvent use via chat.Message. writeEnum == writeVarInt(ordinal); an enum
// STREAM_CODEC / writeById writes a VarInt id; writeByte writes ONE raw byte; writeUtf is a
// VarInt-length-prefixed UTF string; ByteBufCodecs.optional / writeNullable write a Boolean flag
// then payload; writeCollection writes a VarInt count then each element.

import (
	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// numberFormatKind is the NUMBER_FORMAT_TYPE registry index (blank=0/styled=1/fixed=2).
// numberFormatNone means the Optional is absent (Boolean false, no payload). VERIFIED javap
// NumberFormatTypes bootstrap order + data/registryid/numberformattype.go.
type numberFormatKind int32

const (
	numberFormatNone   numberFormatKind = -1
	numberFormatBlank  numberFormatKind = 0
	numberFormatStyled numberFormatKind = 1
	numberFormatFixed  numberFormatKind = 2
)

// numberFormat is a scoreboard NumberFormat. kind selects the registry branch; fixedValue is the
// fixed-branch Component; styledValue is the styled-branch Style (a chat.Message with only style
// fields). Zero value {kind: numberFormatNone} == Optional.empty.
type numberFormat struct {
	kind        numberFormatKind
	fixedValue  chat.Message
	styledValue chat.Message
}

// numberFormatOptional appends the Optional<NumberFormat> wire form onto fields: a Boolean present
// flag, then (if present) the registry id VarInt + branch body.
//
//	[VERIFIED javap NumberFormatTypes.OPTIONAL_STREAM_CODEC = ByteBufCodecs.optional(dispatch);
//	 dispatch -> registry id VarInt then branch STREAM_CODEC. blank has no body; fixed=Component;
//	 styled=Style.]
func numberFormatOptional(fields []pk.FieldEncoder, nf numberFormat) []pk.FieldEncoder {
	if nf.kind == numberFormatNone {
		return append(fields, pk.Boolean(false))
	}
	fields = append(fields, pk.Boolean(true), pk.VarInt(int32(nf.kind)))
	switch nf.kind {
	case numberFormatBlank:
	case numberFormatFixed:
		fields = append(fields, nf.fixedValue)
	case numberFormatStyled:
		fields = append(fields, nf.styledValue)
	}
	return fields
}

// ClientboundSetObjectivePacket (id 106).
// WIRE (VERIFIED javap write): writeUtf(objectiveName); writeByte(method);
//
//	if method==ADD(0) || method==CHANGE(2) { TRUSTED_STREAM_CODEC.encode(displayName);
//	writeEnum(renderType); OPTIONAL_STREAM_CODEC.encode(numberFormat); }
//
// method is a single BYTE. renderType writeEnum == VarInt(ordinal): INTEGER=0/HEARTS=1.
const (
	objectiveMethodAdd    = 0 // METHOD_ADD
	objectiveMethodRemove = 1 // METHOD_REMOVE
	objectiveMethodChange = 2 // METHOD_CHANGE
)

// encodeSetObjectiveAddOrChange builds the ADD/CHANGE form (carries the display payload).
func encodeSetObjectiveAddOrChange(method int, objectiveName string, displayName chat.Message, renderType objectiveRenderType, nf numberFormat) pk.Packet {
	fields := []pk.FieldEncoder{
		pk.String(objectiveName),
		pk.Byte(int8(method)),
		displayName,
		pk.VarInt(int32(renderType)),
	}
	fields = numberFormatOptional(fields, nf)
	return pk.Marshal(int32(packetid.ClientboundSetObjective), fields...)
}

// encodeSetObjectiveRemove builds the REMOVE form (no display payload).
//
//	[VERIFIED javap write: method==REMOVE writes only objectiveName + the method byte.]
func encodeSetObjectiveRemove(objectiveName string) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundSetObjective),
		pk.String(objectiveName),
		pk.Byte(int8(objectiveMethodRemove)),
	)
}

// ClientboundSetScorePacket (id 110) -- record, StreamCodec.composite order.
// WIRE (VERIFIED javap composite): owner (STRING_UTF8); objectiveName (STRING_UTF8); score (VAR_INT);
//
//	display (TRUSTED_OPTIONAL_STREAM_CODEC -> Optional<Component>);
//	numberFormat (OPTIONAL_STREAM_CODEC -> Optional<NumberFormat>).
func encodeSetScore(owner, objectiveName string, score int32, display chat.Message, hasDisplay bool, nf numberFormat) pk.Packet {
	fields := []pk.FieldEncoder{
		pk.String(owner),
		pk.String(objectiveName),
		pk.VarInt(score),
	}
	if hasDisplay {
		fields = append(fields, pk.Boolean(true), display)
	} else {
		fields = append(fields, pk.Boolean(false))
	}
	fields = numberFormatOptional(fields, nf)
	return pk.Marshal(int32(packetid.ClientboundSetScore), fields...)
}

// ClientboundResetScorePacket (id 79).
// WIRE (VERIFIED javap write): writeUtf(owner); writeNullable(objectiveName) -> Boolean present flag
// then String if present. An absent objective resets the owner across ALL objectives.
func encodeResetScore(owner, objectiveName string, hasObjective bool) pk.Packet {
	fields := []pk.FieldEncoder{pk.String(owner)}
	if hasObjective {
		fields = append(fields, pk.Boolean(true), pk.String(objectiveName))
	} else {
		fields = append(fields, pk.Boolean(false))
	}
	return pk.Marshal(int32(packetid.ClientboundResetScore), fields...)
}

// ClientboundSetDisplayObjectivePacket (id 98).
// WIRE (VERIFIED javap write): writeById(DisplaySlot::id, slot) -> VarInt slot id; writeUtf(objectiveName).
// A cleared slot sends objectiveName == "" (getObjectiveName maps "" back to null on the client).
func encodeSetDisplayObjective(slot displaySlot, objectiveName string) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundSetDisplayObjective),
		pk.VarInt(int32(slot)),
		pk.String(objectiveName),
	)
}

// ClientboundSetPlayerTeamPacket (id 109).
// WIRE (VERIFIED javap write): writeUtf(name); writeByte(method);
//
//	if shouldHaveParameters(method) Parameters.STREAM_CODEC.encode(parameters);
//	if shouldHavePlayerList(method) writeCollection(players) -> VarInt count + each UTF String.
//
// method (BYTE): ADD=0, REMOVE=1, CHANGE=2, JOIN=3, LEAVE=4.
// shouldHaveParameters == (method==0 || method==2); shouldHavePlayerList == (0 || 3 || 4).
// Parameters record (VERIFIED javap Function7 order): displayName (Component); playerPrefix
// (Component); playerSuffix (Component); nameTagVisibility (Team$Visibility -> VarInt id);
// collisionRule (Team$CollisionRule -> VarInt id); color (optional(TeamColor) -> Boolean flag +
// VarInt id); options (BYTE: 0x1 friendly-fire | 0x2 see-friendly-invisibles). options is LAST.
const (
	teamMethodAdd    = 0 // METHOD_ADD
	teamMethodRemove = 1 // METHOD_REMOVE
	teamMethodChange = 2 // METHOD_CHANGE
	teamMethodJoin   = 3 // METHOD_JOIN
	teamMethodLeave  = 4 // METHOD_LEAVE
)

// teamParametersFields appends the Parameters record body (VERIFIED Function7 order). color<0 means
// TeamColor absent (Optional.empty); options is the packed flags byte (packOptions).
func teamParametersFields(fields []pk.FieldEncoder, t *scoreboardTeam) []pk.FieldEncoder {
	fields = append(fields,
		t.displayName,
		t.playerPrefix,
		t.playerSuffix,
		pk.VarInt(int32(t.nameTagVisibility)),
		pk.VarInt(int32(t.collisionRule)),
	)
	if t.color >= 0 {
		fields = append(fields, pk.Boolean(true), pk.VarInt(int32(t.color)))
	} else {
		fields = append(fields, pk.Boolean(false))
	}
	fields = append(fields, pk.Byte(t.packOptions()))
	return fields
}

// encodeSetPlayerTeamAddOrChange builds the ADD/CHANGE form (method 0/2): name, method, Parameters,
// and (only for ADD) the player list. CHANGE carries Parameters but NOT a player list.
func encodeSetPlayerTeamAddOrChange(method int, t *scoreboardTeam) pk.Packet {
	fields := []pk.FieldEncoder{
		pk.String(t.name),
		pk.Byte(int8(method)),
	}
	fields = teamParametersFields(fields, t)
	if method == teamMethodAdd {
		fields = append(fields, teamMemberList(t.orderedMembers()))
	}
	return pk.Marshal(int32(packetid.ClientboundSetPlayerTeam), fields...)
}

// encodeSetPlayerTeamRemove builds the REMOVE form (method 1): name + method only.
func encodeSetPlayerTeamRemove(name string) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundSetPlayerTeam),
		pk.String(name),
		pk.Byte(int8(teamMethodRemove)),
	)
}

// encodeSetPlayerTeamMembers builds the JOIN/LEAVE form (method 3/4): name, method, then the player
// list (no Parameters). JOIN adds the members, LEAVE removes them.
func encodeSetPlayerTeamMembers(method int, name string, members []string) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundSetPlayerTeam),
		pk.String(name),
		pk.Byte(int8(method)),
		teamMemberList(members),
	)
}

// teamMemberList wraps a []string as the VarInt-count-prefixed collection of UTF strings the packet
// writeCollection emits. pk.Ary[VarInt] over pk.String elements is that exact wire form.
func teamMemberList(members []string) pk.FieldEncoder {
	strs := make([]pk.String, len(members))
	for i, m := range members {
		strs[i] = pk.String(m)
	}
	return pk.Ary[pk.VarInt]{Ary: strs}
}
