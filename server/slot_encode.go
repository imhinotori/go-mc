package server

import (
	"io"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// slot_encode.go holds the proto-776 inventory WIRE codecs (ENT-04): the authoritative
// clientbound ContainerSetContent / ContainerSetSlot encoders over the component-slot
// SlotData codec, and the serverbound HashedStack decoder used by ContainerClick. All
// layouts are JAR-DERIVED (see 06-CAPTURE-DIFF.md §ENT-04). The HashedStack is a CRC
// digest, NOT a full ItemStack — decoding it as a full stack mis-frames the packet, so the
// decoder consumes the bytes per the jar framing and DISCARDS the hashes (the server is
// authoritative). Byte-level seal deferred to Plan 06-07.

// containerSetContent builds ClientboundContainerSetContent. Jar-derived 4-field composite
// (ClientboundContainerSetContentPacket.STREAM_CODEC):
//
//	VarInt  containerId   (ByteBufCodecs.CONTAINER_ID, a VarInt alias)
//	VarInt  stateId       (ByteBufCodecs.VAR_INT)
//	List    items         (ItemStack.OPTIONAL_LIST_STREAM_CODEC: VarInt count + N × ItemStack)
//	ItemStack carriedItem (ItemStack.OPTIONAL_STREAM_CODEC: a single SlotData)
//
// The ItemStack is the component-slot SlotData (count <= 0 => empty). The list count prefix
// is a plain VarInt. This is the AUTHORITATIVE inventory view the server pushes to correct
// the client (it discards the client's ContainerClick hashes and re-sends this).
func containerSetContent(containerID, stateID int32, items []component.SlotData, carried component.SlotData) pk.Packet {
	fields := make([]pk.FieldEncoder, 0, len(items)+4)
	fields = append(fields,
		pk.VarInt(containerID),
		pk.VarInt(stateID),
		pk.VarInt(int32(len(items))), // list count prefix
	)
	for i := range items {
		fields = append(fields, &items[i])
	}
	fields = append(fields, &carried)
	return pk.Marshal(int32(packetid.ClientboundContainerSetContent), fields...)
}

// containerSetSlot builds ClientboundContainerSetSlot. Jar-derived manual write
// (ClientboundContainerSetSlotPacket.write):
//
//	VarInt    containerId   (writeContainerId — a VarInt)
//	VarInt    stateId       (writeVarInt)
//	Short     slot          (writeShort — a Short, NOT a VarInt)
//	ItemStack itemStack     (ItemStack.OPTIONAL_STREAM_CODEC: a single SlotData)
func containerSetSlot(containerID, stateID int32, slot int16, item component.SlotData) pk.Packet {
	return pk.Marshal(int32(packetid.ClientboundContainerSetSlot),
		pk.VarInt(containerID),
		pk.VarInt(stateID),
		pk.Short(slot),
		&item,
	)
}

// decodeHashedStack consumes one serverbound HashedStack from r WITHOUT mis-framing and
// DISCARDS the hashes (the server is authoritative — it never builds a slot from the client's
// claimed item/hashes). Jar-derived framing (HashedStack.STREAM_CODEC = optional(ActualItem)):
//
//	Boolean present
//	if present:
//	  VarInt itemId                (ActualItem.item: holderRegistry(ITEM) => VarInt)
//	  VarInt count                 (ActualItem.count: ByteBufCodecs.VAR_INT)
//	  HashedPatchMap components:
//	    VarInt addedCount,   addedCount   × ( VarInt typeId + Int32 hash )  // value is a FIXED 4-byte CRC, NOT a component
//	    VarInt removedCount, removedCount × ( VarInt typeId )
//
// THE LOAD-BEARING FACT: the added-component value is a fixed 4-byte int (ByteBufCodecs.INT) —
// the component's hash, not its body. Reading it as a component body mis-frames every
// subsequent byte. Returns the number of bytes consumed and any read error (a short/truncated
// stack returns the error so the caller no-ops).
func decodeHashedStack(r io.Reader) (n int64, err error) {
	var present pk.Boolean
	n, err = present.ReadFrom(r)
	if err != nil || !present {
		return n, err
	}
	var itemID, count pk.VarInt
	header := pk.Tuple{&itemID, &count}
	n2, err := header.ReadFrom(r)
	n += n2
	if err != nil {
		return n, err
	}
	// HashedPatchMap: added map (typeId + Int32 hash), then removed collection (typeId).
	var addedCount pk.VarInt
	n2, err = addedCount.ReadFrom(r)
	n += n2
	if err != nil {
		return n, err
	}
	for i := int32(0); i < int32(addedCount); i++ {
		var typeID pk.VarInt
		var hash pk.Int // FIXED 4-byte big-endian int (the CRC hash) — DISCARDED
		entry := pk.Tuple{&typeID, &hash}
		n2, err = entry.ReadFrom(r)
		n += n2
		if err != nil {
			return n, err
		}
	}
	var removedCount pk.VarInt
	n2, err = removedCount.ReadFrom(r)
	n += n2
	if err != nil {
		return n, err
	}
	for i := int32(0); i < int32(removedCount); i++ {
		var typeID pk.VarInt
		n2, err = typeID.ReadFrom(r)
		n += n2
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
