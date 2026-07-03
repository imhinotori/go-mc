// enchantments.go contains helper types for the Enchantments data component.
package component

import (
	"io"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// EnchantmentEntry is one (enchantment, level) pair in the minecraft:enchantments /
// minecraft:stored_enchantments wire body. Its wire form is the ItemEnchantments.STREAM_CODEC
// element (verified 26.2 jar, ItemEnchantments static init): ByteBufCodecs.map over
// Enchantment.STREAM_CODEC (= ByteBufCodecs.holderRegistry(Registries.ENCHANTMENT), a VarInt of the
// holder's registry index) keyed levels ByteBufCodecs.VAR_INT — i.e. VarInt id, VarInt level.
//
// The ID is the numeric ENCHANTMENT registry index (the datapack registry id the server assigns by
// send order). The Level is intRange(1,255) on the disk CODEC; the STREAM_CODEC is a bare VarInt.
type EnchantmentEntry struct {
	ID    pk.VarInt
	Level pk.VarInt
}

// WriteTo encodes the pair as VarInt id then VarInt level, matching the ItemEnchantments.STREAM_CODEC
// element order. Implementing pk.FieldEncoder is REQUIRED: Enchantments/StoredEnchantments serialize
// their entry slice via pk.Array (net/packet.Ary), which type-asserts each element to FieldEncoder.
func (e EnchantmentEntry) WriteTo(w io.Writer) (int64, error) {
	n, err := e.ID.WriteTo(w)
	if err != nil {
		return n, err
	}
	n2, err := e.Level.WriteTo(w)
	return n + n2, err
}

// ReadFrom decodes VarInt id then VarInt level (the inverse of WriteTo). Implementing pk.FieldDecoder
// is REQUIRED for pk.Array (net/packet.Ary) to read the entry slice back.
func (e *EnchantmentEntry) ReadFrom(r io.Reader) (int64, error) {
	n, err := e.ID.ReadFrom(r)
	if err != nil {
		return n, err
	}
	n2, err := e.Level.ReadFrom(r)
	return n + n2, err
}
