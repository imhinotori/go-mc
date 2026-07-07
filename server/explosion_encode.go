package server

// explosion_encode.go — the ClientboundExplode wire encoder (A1 of the Fable explosion audit).
// ServerLevel.explode's tail loops the level players and, for each within distanceToSqr(center) <
// 4096.0 (64 blocks), sends `new ClientboundExplodePacket(center, radius, blockCount,
// Optional<Vec3> playerKnockback = hitPlayers.get(player), explosionParticle, explosionSound,
// blockParticles)`. Without it the client shows NO blast particle/sound AND never receives the
// per-player knockback vector the server applied — so the whole explosion is invisible client-side.
//
// JAR-DERIVED WIRE LAYOUT (javap net.minecraft.network.protocol.game.ClientboundExplodePacket
// STREAM_CODEC, temp/cache/26.2-inner.jar this session — the 26.x slimmed layout, NOT the pre-1.21
// per-block/per-player-velocity form):
//
//	1. center            : Vec3.STREAM_CODEC        -> 3× Double (x, y, z)
//	2. radius            : ByteBufCodecs.FLOAT       -> Float
//	3. blockCount        : ByteBufCodecs.INT         -> Int (the ServerExplosion.explode() return —
//	                                                   the destroyed-block count, for the client debug/scale)
//	4. playerKnockback   : Vec3.STREAM_CODEC.apply(optional) -> Boolean present [+ 3× Double if present]
//	5. explosionParticle : ParticleTypes.STREAM_CODEC -> VarInt(particleTypeId) [+ per-type options]
//	6. explosionSound    : SoundEvent.STREAM_CODEC (Holder) -> VarInt(soundId+1) [0 == inline Direct]
//	7. blockParticles    : WeightedList.streamCodec(ExplosionParticleInfo.STREAM_CODEC)
//	                       -> VarInt(len) then per entry: VarInt(particleTypeId), Float(scaling),
//	                          Float(speed), VarInt(weight)
//
//	[VERIFIED javap ClientboundExplodePacket.STREAM_CODEC composite(Vec3.STREAM_CODEC center,
//	 ByteBufCodecs.FLOAT radius, ByteBufCodecs.INT blockCount, Vec3.STREAM_CODEC.apply(optional)
//	 playerKnockback, ParticleTypes.STREAM_CODEC explosionParticle, SoundEvent.STREAM_CODEC
//	 explosionSound, WeightedList.streamCodec(ExplosionParticleInfo.STREAM_CODEC) blockParticles).
//	 ServerLevel.explode tail: for each player, distanceToSqr(center) < 4096.0 -> connection.send(
//	 new ClientboundExplodePacket(center, radius, blockCount, Optional.ofNullable(hitPlayers.get(p)),
//	 particle, sound, blockParticles)).]

import (
	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/data/registryid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// explosionSendRadiusSqr is ServerLevel.explode's per-player send gate: distanceToSqr(center) <
// 4096.0 (== 64 blocks). Cite ServerLevel.explode (`ldc2_w 4096.0d; dcmpg; ifge`).
const explosionSendRadiusSqr = 4096.0

// genericExplodeSoundName is SoundEvents.GENERIC_EXPLODE (minecraft:entity.generic.explode) — the
// sound the default Level.explode overload passes for a creeper/tnt blast. Its registry id + 1 is the
// Holder<SoundEvent> reference on the wire. Cite Level.explode default overload getstatic GENERIC_EXPLODE.
const genericExplodeSoundName = "minecraft:entity.generic.explode"

// explosionEmitterParticleName is ParticleTypes.EXPLOSION_EMITTER — the explosion particle the default
// Level.explode overload passes (a SimpleParticleType, so it writes ONLY its VarInt type id, no options).
// The client picks the small/large render itself; the packet just carries this emitter type.
//
//	[VERIFIED javap Level.explode default overload: getstatic ParticleTypes.EXPLOSION_EMITTER.]
const explosionEmitterParticleName = "minecraft:explosion_emitter"

// explosionBlockParticle is one Weighted<ExplosionParticleInfo> of the DEFAULT_EXPLOSION_BLOCK_PARTICLES
// WeightedList: the particle registry key, its scaling + speed floats, and the entry weight. The default
// list is exactly two entries (POOF 0.5f/1.0f and SMOKE 1.0f/1.0f), each added with the default weight 1.
//
//	[VERIFIED javap Level static{}: WeightedList.builder().add(new ExplosionParticleInfo(POOF,0.5f,1.0f))
//	 .add(new ExplosionParticleInfo(SMOKE,1.0f,1.0f)).build(); Builder.add(E) -> add(E,1).]
type explosionBlockParticle struct {
	particleName   string
	scaling, speed float32
	weight         int32
}

// defaultExplosionBlockParticles is DEFAULT_EXPLOSION_BLOCK_PARTICLES (Level static init), the
// blockParticles field the default explode overload passes for every creeper/tnt blast.
var defaultExplosionBlockParticles = []explosionBlockParticle{
	{particleName: "minecraft:poof", scaling: 0.5, speed: 1.0, weight: 1},
	{particleName: "minecraft:smoke", scaling: 1.0, speed: 1.0, weight: 1},
}

// encodeExplode builds ClientboundExplode. center/radius/blockCount are the blast's; kb is the
// per-player knockback vector (the SAME Vec3 the server pushed the player by) — kbPresent==false emits
// the empty Optional (a player not in the hurt set). The particle + sound + blockParticles are the
// default Level.explode overload's constants (EXPLOSION_EMITTER, GENERIC_EXPLODE, the POOF/SMOKE list).
// An unknown particle/sound key resolves to a defensive fallback of 0 (never taken on a jar-derived key).
func encodeExplode(cx, cy, cz float64, radius float32, blockCount int32, kbx, kby, kbz float64, kbPresent bool) pk.Packet {
	fields := []pk.FieldEncoder{
		pk.Double(cx), pk.Double(cy), pk.Double(cz), // 1: center Vec3
		pk.Float(radius),      // 2: radius
		pk.Int(blockCount),    // 3: blockCount
		pk.Boolean(kbPresent), // 4: Optional<Vec3> playerKnockback — present flag
	}
	if kbPresent {
		fields = append(fields, pk.Double(kbx), pk.Double(kby), pk.Double(kbz))
	}
	// 5: explosionParticle — ParticleTypes.STREAM_CODEC == VarInt(typeId) [+ options; EXPLOSION_EMITTER
	// is a SimpleParticleType with StreamCodec.unit options == no per-particle bytes].
	partID := menuTypeID(registryid.ParticleType, explosionEmitterParticleName)
	if partID < 0 {
		partID = 0
	}
	fields = append(fields, pk.VarInt(partID))
	// 6: explosionSound — Holder<SoundEvent> reference == VarInt(id+1); 0 reserved for an inline Direct.
	soundID := menuTypeID(registryid.SoundEvent, genericExplodeSoundName)
	if soundID < 0 {
		soundID = -1 // -> VarInt(0) below is the inline-Direct sentinel; never taken for a jar sound
	}
	fields = append(fields, pk.VarInt(soundID+1))
	// 7: blockParticles — WeightedList.streamCodec == VarInt(len) then per entry
	// VarInt(particleTypeId), Float(scaling), Float(speed), VarInt(weight).
	fields = append(fields, pk.VarInt(int32(len(defaultExplosionBlockParticles))))
	for _, bp := range defaultExplosionBlockParticles {
		bpID := menuTypeID(registryid.ParticleType, bp.particleName)
		if bpID < 0 {
			bpID = 0
		}
		fields = append(fields,
			pk.VarInt(bpID),
			pk.Float(bp.scaling),
			pk.Float(bp.speed),
			pk.VarInt(bp.weight),
		)
	}
	return pk.Marshal(int32(packetid.ClientboundExplode), fields...)
}
