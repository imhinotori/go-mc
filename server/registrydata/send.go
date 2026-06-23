package registrydata

import (
	"fmt"
	"io"

	"github.com/imhinotori/go-mc/data/packetid"
	pk "github.com/imhinotori/go-mc/net/packet"
)

// PacketWriter is the minimal subset of *net.Conn the send helpers need.
// *github.com/imhinotori/go-mc/net.Conn satisfies it, and tests can supply an
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

// WriteTags sends a present (empty-but-valid for v1) ClientboundConfigUpdateTags.
// Tags are never pack-sourced; the client only requires that a tags packet is
// PRESENT so it does not kick on a tag reference. The minimal valid body is a
// VarInt count of 0 — present but with no per-registry tag sets. When the 02-04
// capture-diff reveals the minimal real set vanilla sends in config, this is
// where that set gets mirrored.
func WriteTags(conn PacketWriter) error {
	p := pk.Marshal(
		packetid.ClientboundConfigUpdateTags,
		pk.VarInt(0), // present body, zero per-registry tag sets
	)
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
