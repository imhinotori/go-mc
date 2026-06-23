package registrydata

import (
	"fmt"
	"io"

	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// PacketWriter is the minimal subset of *net.Conn the send helpers need.
// *github.com/imhinotori/sulfur/net.Conn satisfies it, and tests can supply an
// in-memory recorder. Keeping the dependency at this interface lets the
// registrydata package stay free of a hard *net.Conn import and be round-tripped
// through the fork's bot/ client decoder in isolation.
type PacketWriter interface {
	WritePacket(p pk.Packet) error
}

// WriteRegistryData sends one ClientboundConfigRegistryData packet per embedded
// registry, in the send order defined by Load (registryDirs). Each packet body is:
//
//	Identifier registryId
//	VarInt     entryCount
//	per entry: Identifier key, Boolean hasData(=true), network-NBT payload
//
// The NBT payload is the REAL 26.2 jar-derived content (type-faithful dynbt
// tree from embed.go), not the stale registry/codec.go struct — this is the
// NET-04 payload that lets the 26.2 client accept the registry at Finish
// Configuration instead of silently rejecting it.
func WriteRegistryData(conn PacketWriter) error {
	regs, err := Load()
	if err != nil {
		return fmt.Errorf("registrydata: load: %w", err)
	}
	for _, reg := range regs {
		p := pk.Marshal(
			packetid.ClientboundConfigRegistryData,
			pk.Identifier(reg.ID),
			entriesEncoder(reg.Entries),
		)
		if err := conn.WritePacket(p); err != nil {
			return fmt.Errorf("registrydata: write %s: %w", reg.ID, err)
		}
	}
	return nil
}

// WriteTags sends the real vanilla 26.2 ClientboundConfigUpdateTags: the 15
// registries' tag sets (block, item, worldgen/biome, entity_type, damage_type,
// enchantment, banner_pattern, fluid, game_event, timeline, instrument,
// point_of_interest_type, dialog, painting_variant, potion) with the exact
// vanilla tag counts and per-tag entry indices.
//
// This is NOT optional: an empty Update Tags leaves the tags referenced by the
// RegistryData entries (enchantment exclusive_set/*, dimension_type/timeline
// in_*, dialog quick_actions, sulfur_cube_archetype item tags) UNBOUND, and a
// real 26.2 client aborts Registry Loading with "Unbound tags in registry …".
// The tag content and index resolution live in tags.go (the wire format and the
// built-in-vs-datapack index sourcing are documented there).
func WriteTags(conn PacketWriter) error {
	p, _, err := buildUpdateTagsPacket()
	if err != nil {
		return fmt.Errorf("registrydata: build update tags: %w", err)
	}
	if err := conn.WritePacket(p); err != nil {
		return fmt.Errorf("registrydata: write update tags: %w", err)
	}
	return nil
}

// entriesEncoder writes the RegistryData entry list portion of the packet body:
// VarInt(count) followed by, for each entry, Identifier(key) + Boolean(true) +
// network-NBT. It mirrors the exact frame shape of registry.Registry.WriteTo,
// sourcing the NBT from the embedded 26.2 content instead of registry.Registries.
type entriesEncoder []Entry

func (e entriesEncoder) WriteTo(w io.Writer) (int64, error) {
	count := pk.VarInt(len(e))
	n, err := count.WriteTo(w)
	if err != nil {
		return n, err
	}
	for i := range e {
		key := pk.Identifier(e[i].Key)
		n1, err := key.WriteTo(w)
		n += n1
		if err != nil {
			return n, err
		}

		hasData := pk.Boolean(true)
		n2, err := hasData.WriteTo(w)
		n += n2
		if err != nil {
			return n, err
		}

		n3, err := pk.NBTField{V: e[i].NBT}.WriteTo(w)
		n += n3
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
