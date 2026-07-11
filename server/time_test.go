package server

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
)

// TestSetTimeEmptyMapWire locks the every-20-ticks gameTime-only packet wire shape: LONG gameTime
// (big-endian) then VarInt(0) for the empty clock map. CITE MinecraftServer
// .forceGameTimeSynchronization (Map.of()).
func TestSetTimeEmptyMapWire(t *testing.T) {
	p := writeSetTimePacket(0x0102030405060708, nil)
	if p.ID != int32(packetid.ClientboundSetTime) {
		t.Fatalf("packet id = %d, want %d", p.ID, packetid.ClientboundSetTime)
	}
	want := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x00} // BE long + VarInt(0)
	if !bytes.Equal(p.Data, want) {
		t.Fatalf("empty-map wire = % x, want % x", p.Data, want)
	}
}

// TestSetTimeFullSyncWire locks the join full-sync wire shape: LONG gameTime, VarInt(2) map size,
// then two entries each = VarInt(clockID) + VarLong(totalTicks) + Float(partialTick) + Float(rate).
// gameTime=1, both clocks totalTicks=1, partialTick=0, rate=1.0. CITE
// ServerClockManager.createFullSyncPacket + ClockNetworkState.STREAM_CODEC.
func TestSetTimeFullSyncWire(t *testing.T) {
	clocks := []clockUpdate{
		{clockID: worldClockOverworldID, totalTicks: 1, partialTick: 0, rate: 1.0},
		{clockID: worldClockTheEndID, totalTicks: 1, partialTick: 0, rate: 1.0},
	}
	p := writeSetTimePacket(1, clocks)
	// float32(1.0) big-endian = 0x3f800000; float32(0) = 0x00000000.
	want := []byte{
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, // gameTime = 1 (BE long)
		0x02,                   // map size = 2
		0x00,                   // clockID overworld = 0 (VarInt)
		0x01,                   // totalTicks = 1 (VarLong)
		0x00, 0x00, 0x00, 0x00, // partialTick = 0.0
		0x3f, 0x80, 0x00, 0x00, // rate = 1.0
		0x01,                   // clockID the_end = 1 (VarInt)
		0x01,                   // totalTicks = 1
		0x00, 0x00, 0x00, 0x00, // partialTick = 0.0
		0x3f, 0x80, 0x00, 0x00, // rate = 1.0
	}
	if !bytes.Equal(p.Data, want) {
		t.Fatalf("full-sync wire = % x, want % x", p.Data, want)
	}
}

// TestClockRateFreeze verifies rate is 0.0 when advance_time is off (the doDaylightCycle freeze) and
// 1.0 when on. CITE ClockInstance.packNetworkState frozen ? 0.0f : rate.
func TestClockRateFreeze(t *testing.T) {
	tl := &TickLoop{}
	if got := tl.clockRate(); got != 1.0 {
		t.Fatalf("default clockRate = %v, want 1.0 (advance_time defaults true)", got)
	}
	tl.gamerules = newGameRules()
	tl.gamerules.setBool(ruleAdvanceTime, false)
	if got := tl.clockRate(); got != 0.0 {
		t.Fatalf("frozen clockRate = %v, want 0.0 (advance_time off)", got)
	}
}
