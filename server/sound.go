package server

// sound.go -- the SOUND wire subsystem: the positional ClientboundSound encoder and the TickLoop
// broadcast API (playSound / playSoundEntity), a 1:1 port of ServerLevel.playSeededSound + PlayerList
// .broadcast from the unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap-verified this session).
// It is the sibling of the entity_encode.go encodeSoundEntity (the entity-ATTACHED sound the mob
// hurt/death path already rides): this file adds the POSITIONAL sound (a fixed world x,y,z) that the
// Witch.aiStep self-drink and the Raid.playSound RAID_HORN emit.
//
// JAR-DERIVED WIRE + BROADCAST (all javap-verified this session):
//   - ClientboundSoundPacket.write: Holder<SoundEvent> (registry Reference -> VarInt(id+1); 0 reserved
//     for an inline Direct SoundEvent), SoundSource enum (writeEnum -> VarInt(ordinal)), int x*8, y*8,
//     z*8 (position scaled to 1/8-block fixed point via (int)(coord*8.0), a plain 4-byte Int, NOT a
//     VarInt), float volume, float pitch, long seed.
//   - ServerLevel.playSeededSound: broadcast radius = SoundEvent.getRange(volume) = volume>1.0f ?
//     volume*16 : 16; the seed comes from Level.soundSeedGenerator (a DEDICATED RandomSource, NOT the
//     gameplay random stream) so a per-sound seed never perturbs any mob/pig gameplay RNG.
//   - PlayerList.broadcast: send to a player iff dx*dx+dy*dy+dz*dz < radius*radius (strictly less).
//
// TICK-05: playSound/playSoundEntity run on the tick goroutine over tick-owned state (t.players, each
// player x/y/z), emitting via p.client.Send (the bounded outbound queue) -- no goroutine, no new sync.

import (
	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// soundSourceHostile is SoundSource.HOSTILE.ordinal() == 5 (enum order MASTER0 MUSIC1 RECORDS2 WEATHER3
// BLOCKS4 HOSTILE5 NEUTRAL6 PLAYERS7 AMBIENT8 VOICE9 UI10). Monster.getSoundSource() returns HOSTILE, so
// a witch WITCH_DRINK self-drink sound plays on this category. soundSourceNeutral/soundSourcePlayers
// live in entity_encode.go (the pre-existing entity-attached path).
//
//	[VERIFIED javap Monster.getSoundSource: getstatic SoundSource.HOSTILE; areturn. SoundSource enum order.]
const soundSourceHostile = 5

const soundBroadcastBaseRange = 16.0

// soundRange is SoundEvent.getRange(volume) for a variable-range SoundEvent: volume>1.0f ? 16.0f*volume
// : 16.0f. Every SoundEvent played here is a createVariableRangeEvent (empty fixedRange), so this is the
// whole formula. It is the broadcast radius ServerLevel.playSeededSound passes to PlayerList.broadcast.
//
//	[VERIFIED javap SoundEvent.getRange(float): fload volume; fconst_1; fcmpl; ifle -> 16.0f; else 16.0f*volume.]
func soundRange(volume float32) float64 {
	if volume > 1.0 {
		return float64(16.0 * volume)
	}
	return float64(soundBroadcastBaseRange)
}

// encodeSound builds a positional ClientboundSound (jar: ClientboundSoundPacket.write). The position is
// quantized to 1/8-block fixed point exactly as the ctor does ((int)(coord*8.0)), written as a plain
// 4-byte Int. The Holder<SoundEvent> is a registry Reference (VarInt id+1; 0 reserved for an inline
// Direct SoundEvent, never taken for a registered sound).
//
//	[VERIFIED javap ClientboundSoundPacket write: SoundEvent.STREAM_CODEC(Holder) ; writeEnum(source) ;
//	 writeInt(x) ; writeInt(y) ; writeInt(z) ; writeFloat(volume) ; writeFloat(pitch) ; writeLong(seed).
//	 ByteBufCodecs.holder encode: Reference -> VarInt(id+1), Direct -> VarInt(0)+inline.]
func encodeSound(soundID int32, source int, x, y, z float64, volume, pitch float32, seed int64) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundSound),
		pk.VarInt(soundID+1),
		pk.VarInt(int32(source)),
		pk.Int(int32(x*8.0)),
		pk.Int(int32(y*8.0)),
		pk.Int(int32(z*8.0)),
		pk.Float(volume),
		pk.Float(pitch),
		pk.Long(seed),
	)
}

// playSound is ServerLevel.playSeededSound(null-excluded, x,y,z, sound, source, volume, pitch, seed) ->
// PlayerList.broadcast: emit a positional ClientboundSound to every player within getRange(volume) of
// (x,y,z). The seed is caller-supplied (a dedicated non-gameplay draw, the soundSeedGenerator analogue,
// so it never perturbs a mob/pig gameplay stream). No player is excluded (a mob source passes null).
// Single-overworld v1: no cross-dimension filter.
//
//	[VERIFIED javap ServerLevel.playSeededSound + PlayerList.broadcast: dx*dx+dy*dy+dz*dz < radius*radius.]
func (t *TickLoop) playSound(soundID int32, source int, x, y, z float64, volume, pitch float32, seed int64) {
	radius := soundRange(volume)
	rsq := radius * radius
	pkt := encodeSound(soundID, source, x, y, z, volume, pitch, seed)
	for _, p := range t.players {
		if p == nil || p.client == nil {
			continue
		}
		dx := x - p.x
		dy := y - p.y
		dz := z - p.z
		if dx*dx+dy*dy+dz*dz < rsq {
			p.client.Send(pkt)
		}
	}
}

// playSoundEntity is ServerLevel.playSeededSound(entity, sound, source, volume, pitch, seed) ->
// PlayerList.broadcast for an ENTITY-attached sound: same getRange(volume) radius test around the
// entity position, but the wire packet is ClientboundSoundEntity (the sound follows the entity, so only
// the entity id is sent, not a position). Seed is the caller-supplied non-gameplay draw.
//
//	[VERIFIED javap ServerLevel.playSeededSound(entity,...): getRange(volume) radius around entity.getX/Y/Z;
//	 PlayerList.broadcast(..., ClientboundSoundEntityPacket).]
func (t *TickLoop) playSoundEntity(e *Entity, soundID int32, source int, volume, pitch float32, seed int64) {
	radius := soundRange(volume)
	rsq := radius * radius
	pkt := encodeSoundEntity(soundID, source, e.id, volume, pitch, seed)
	for _, p := range t.players {
		if p == nil || p.client == nil {
			continue
		}
		dx := e.x - p.x
		dy := e.y - p.y
		dz := e.z - p.z
		if dx*dx+dy*dy+dz*dz < rsq {
			p.client.Send(pkt)
		}
	}
}
