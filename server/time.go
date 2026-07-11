package server

// time.go -- the day/night clock synchronization packet (ClientboundSetTimePacket), ported 1:1
// from the unobfuscated 26.2 jar. Read from the jar bytecode via javap -c -p this session.
//
// Without this the server never tells the client what time it is: the vanilla client free-runs
// its own day/night clock from login, so the sky desyncs immediately and /time has no visible
// effect. 26.2 REWORKED the packet + the server clock. In 26.1 and earlier it was (long gameTime,
// long dayTime) where a NEGATIVE dayTime froze the client clock (the doDaylightCycle sign
// convention). 26.2 replaced that with a per-clock ClockManager model:
//
//	ClientboundSetTimePacket:
//	    long                                         gameTime     (ByteBufCodecs.LONG, big-endian)
//	    Map<Holder<WorldClock>, ClockNetworkState>   clockUpdates (ByteBufCodecs.map)
//
//	Map wire form (ByteBufCodecs.map): VarInt size, then per entry:
//	    Holder<WorldClock>  key   -- WorldClock.STREAM_CODEC == holderRegistry(WORLD_CLOCK) ->
//	                                 idMapper -> RAW VarInt registry id, NO +1 offset (verified vs
//	                                 ByteBufCodecs idMapper encode: VarInt.write(applyAsInt(v))).
//	    ClockNetworkState   value -- ClockNetworkState.STREAM_CODEC composite:
//	                                     VarLong totalTicks   (ByteBufCodecs.VAR_LONG)
//	                                     Float   partialTick  (ByteBufCodecs.FLOAT)
//	                                     Float   rate         (ByteBufCodecs.FLOAT)
//
// The WORLD_CLOCK registry (server/registrydata/registries/world_clock) holds two unit clocks. It
// is a datapack registry sent alphabetically (registrydata.Load order), so the network id is the
// sorted position: minecraft:overworld == 0, minecraft:the_end == 1. The overworld clock is the
// day/night clock the sky reads.
//
// THREE emit sites in vanilla (all the same packet type):
//   - JOIN: PlayerList.sendLevelInfo sends ServerClockManager.createFullSyncPacket() -- the FULL
//     clock map (both clocks + states). Sulfur join seam mirrors this.
//   - EVERY 20 TICKS: MinecraftServer.tickChildren does "if (tickCount % 20 == 0)
//     forceGameTimeSynchronization()", which broadcasts SetTimePacket(overworld.getGameTime(),
//     Map.of()) -- gameTime ONLY, an EMPTY clock map. Keeps the client gameTime aligned without
//     perturbing its clock extrapolation.
//   - ON CLOCK MODIFY: ServerClockManager.modifyClock (driven by /time set|add and the sleep skip)
//     broadcasts SetTimePacket(getGameTime(), Map.of(clock, packedState)) -- the modified clock.
//
// ClockNetworkState packed by ServerClockManager.ClockInstance.packNetworkState(server):
//     advanceTime = gameRules.get(ADVANCE_TIME)   // ex-doDaylightCycle, default true
//     frozen      = this.paused || !advanceTime
//     return new ClockNetworkState(totalTicks, partialTick, frozen ? 0.0f : rate)
// The 26.2 freeze convention is therefore rate == 0.0 (NOT a negative dayTime). Default clock
// state: rate = 1.0f, partialTick = 0.0f, paused = false (ClockInstance ctor + ClockState defaults).
//
// SULFUR MODEL MAP: Sulfur has no separate ServerClockManager -- it keeps a single gametime counter
// and derives dayTime = gametime % 24000 everywhere (fire.go, spawner.go, commands_ops.go). The
// overworld clock totalTicks is therefore t.gametime (vanilla overworld clock advances in lockstep
// with getGameTime for a normal world); partialTick is 0 (Sulfur advances whole ticks); rate is 1.0
// when the advance_time gamerule is on, else 0.0 (the freeze). the_end mirrors the overworld state
// in the full-sync map. When a real per-clock manager lands, these become reads off it; the wire
// shape and emit sites do not change.

import (
	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// WORLD_CLOCK registry network ids (datapack registry, sorted send order). idMapper writes the
// RAW id (no +1).
const (
	worldClockOverworldID = 0 // minecraft:overworld -- the day/night clock the sky reads
	worldClockTheEndID    = 1 // minecraft:the_end
)

// clockUpdate is one (clock network id, ClockNetworkState) map entry for the SetTime packet.
type clockUpdate struct {
	clockID     int32   // WORLD_CLOCK registry network id (raw VarInt, no +1)
	totalTicks  int64   // ClockNetworkState.totalTicks (VarLong) -- the day-time counter
	partialTick float32 // ClockNetworkState.partialTick (Float)
	rate        float32 // ClockNetworkState.rate (Float) -- 0.0 == frozen (advance_time off)
}

// clockRate returns the clock rate the packet carries: the default 1.0 unless the advance_time
// gamerule (ex-doDaylightCycle, ADVANCE_TIME) is off, in which case it is 0.0 (frozen). Mirrors
// ClockInstance.packNetworkState frozen?0.0f:rate where frozen = paused || !advanceTime and Sulfur
// has no paused clock (paused == false in v1).
//
//	[VERIFIED javap ServerClockManager.ClockInstance.packNetworkState: advanceTime =
//	 GameRules.ADVANCE_TIME; frozen = paused || !advanceTime; rate = frozen ? 0.0f : this.rate (1.0f).]
func (t *TickLoop) clockRate() float32 {
	if t.gameRule(ruleAdvanceTime) {
		return 1.0 // ClockInstance default rate
	}
	return 0.0 // frozen: advance_time off (the doDaylightCycle freeze)
}

// writeSetTimePacket builds ClientboundSetTimePacket(gameTime, clockUpdates). Wire order is
// jar-derived from the STREAM_CODEC: LONG gameTime, then the ByteBufCodecs.map body (VarInt size,
// then per entry the holder id VarInt + the ClockNetworkState composite VarLong/Float/Float).
//
//	[VERIFIED javap ClientboundSetTimePacket static-init (STREAM_CODEC composite over LONG +
//	 ByteBufCodecs.map(WorldClock.STREAM_CODEC, ClockNetworkState.STREAM_CODEC)); ClockNetworkState
//	 static-init composite (VAR_LONG, FLOAT, FLOAT); WorldClock.STREAM_CODEC == holderRegistry.]
func writeSetTimePacket(gameTime int64, clocks []clockUpdate) pk.Packet {
	fields := make([]pk.FieldEncoder, 0, 2+len(clocks)*4)
	fields = append(fields, pk.Long(gameTime))      // gameTime (big-endian long)
	fields = append(fields, pk.VarInt(len(clocks))) // map size
	for _, c := range clocks {
		fields = append(fields,
			pk.VarInt(c.clockID),     // Holder<WorldClock> -- raw registry id VarInt
			pk.VarLong(c.totalTicks), // ClockNetworkState.totalTicks
			pk.Float(c.partialTick),  // ClockNetworkState.partialTick
			pk.Float(c.rate),         // ClockNetworkState.rate (0.0 == frozen)
		)
	}
	return pk.Marshal(int32(packetid.ClientboundSetTime), fields...)
}

// fullClockSync returns the clock-update slice for the JOIN full-sync packet: both WORLD_CLOCK
// entries (overworld + the_end) with the current day-time state. Mirrors
// ServerClockManager.createFullSyncPacket which packs EVERY clock. Sulfur two clocks advance in
// lockstep with gametime in a normal world, so both carry totalTicks = gametime, partialTick = 0,
// rate = clockRate().
//
//	[VERIFIED javap ServerClockManager.createFullSyncPacket: new ClientboundSetTimePacket(
//	 getGameTime(), Util.mapValues(this.clocks, ClockInstance::packNetworkState)).]
func (t *TickLoop) fullClockSync() []clockUpdate {
	rate := t.clockRate()
	return []clockUpdate{
		{clockID: worldClockOverworldID, totalTicks: t.gametime, partialTick: 0, rate: rate},
		{clockID: worldClockTheEndID, totalTicks: t.gametime, partialTick: 0, rate: rate},
	}
}

// sendFullTimeSync sends the JOIN full-sync SetTime packet to a single joining player -- the
// PlayerList.sendLevelInfo createFullSyncPacket() call. Owner-goroutine only (called from the join
// seam alongside the weather/scoreboard sync). Nil-guarded like the other join sends.
func (t *TickLoop) sendFullTimeSync(p *tickPlayer) {
	if p == nil || p.client == nil {
		return
	}
	p.client.Send(writeSetTimePacket(t.gametime, t.fullClockSync()))
}

// broadcastTimeSync broadcasts the EVERY-20-TICKS gameTime-only SetTime packet to every connected
// player -- the MinecraftServer.forceGameTimeSynchronization() call fired from tickChildren
// tickCount % 20 == 0 gate. The clock map is EMPTY (Map.of()): this keeps the client gameTime
// aligned without perturbing its day-time clock extrapolation. Owner-goroutine only (called from
// tickOnce on the coordinator, like tickWeather).
//
//	[VERIFIED javap MinecraftServer.forceGameTimeSynchronization: playerList.broadcastAll(new
//	 ClientboundSetTimePacket(overworld().getGameTime(), Map.of())); tickChildren gate is
//	 if (this.tickCount % 20 == 0) forceGameTimeSynchronization().]
func (t *TickLoop) broadcastTimeSync() {
	packet := writeSetTimePacket(t.gametime, nil)
	for _, p := range t.players {
		if p.client == nil {
			continue
		}
		p.client.Send(packet)
	}
}

// broadcastClockModify broadcasts the ON-MODIFY SetTime packet after a /time set|add (or a sleep
// skip) changes the day-time clock -- the ServerClockManager.modifyClock broadcast. The map carries
// the single modified overworld clock new state. Owner-goroutine only (called from the command
// handler on the coordinator).
//
//	[VERIFIED javap ServerClockManager.modifyClock: playerList.broadcastAll(new
//	 ClientboundSetTimePacket(getGameTime(), Map.of(clock, instance.packNetworkState(server)))).]
func (t *TickLoop) broadcastClockModify() {
	clocks := []clockUpdate{
		{clockID: worldClockOverworldID, totalTicks: t.gametime, partialTick: 0, rate: t.clockRate()},
	}
	packet := writeSetTimePacket(t.gametime, clocks)
	for _, p := range t.players {
		if p.client == nil {
			continue
		}
		p.client.Send(packet)
	}
}
