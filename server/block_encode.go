package server

import (
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// block_encode.go holds the JAR-DERIVED proto-776 clientbound BLOCK encoders for the
// ENT-03 reconciliation handshake. Both layouts are decompiled from the unobfuscated 26.2
// inner jar (temp/cache/26.2-inner.jar) this session:
//
//   ClientboundBlockChangedAckPacket.STREAM_CODEC = VarInt(sequence)
//     (javap: read() = FriendlyByteBuf.readVarInt; the packet is a single int record.)
//
//   ClientboundBlockUpdatePacket.STREAM_CODEC =
//     composite(BlockPos.STREAM_CODEC, idMapper(Block.BLOCK_STATE_REGISTRY))
//     => packed BlockPos Long (pk.Position) THEN VarInt(blockStateId).
//     (javap: BlockPos.STREAM_CODEC writes the packed long; idMapper writes the registry
//     id as a VarInt — so the wire is the position long followed by the state-id varint.)
//
// The fork's pk.* codecs are symmetric, so these match a client's decoder. Like the entity
// encoders, byte-identity with vanilla is sealed by the 06-07 capture-diff; these are the
// producer side of the contract.

// blockChangedAck builds a ClientboundBlockChangedAck for the given action sequence. It
// reconciles the client's predictive block edits up to and including this sequence — the
// load-bearing anti-ghost-block ack (06-RESEARCH Pitfall 5): without it the client's
// predicted place/break stays uncommitted and renders as a ghost.
func blockChangedAck(seq int32) pk.Packet {
	return pk.Marshal(int32(packetid.ClientboundBlockChangedAck), pk.VarInt(seq))
}

// blockUpdate builds a ClientboundBlockUpdate announcing that the block at pos is now
// state. Sent to every player tracking the edited column (editor included, so a
// rejected/adjusted client prediction snaps back to the authoritative state).
func blockUpdate(pos pk.Position, state block.StateID) pk.Packet {
	return pk.Marshal(int32(packetid.ClientboundBlockUpdate), pos, pk.VarInt(state))
}
