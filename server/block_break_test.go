package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// block_break_test.go covers Plan 17-21: the server-authoritative block-break dig-time port
// (ServerPlayerGameMode handleBlockBreakAction START/STOP/ABORT + tick + getDestroyProgress +
// incrementDestroyProgress + destroyBlockProgress). The tests are deterministic (no RNG): the
// dig clock is driven by setting loop.gametime directly, so the elapsed-tick math is exact.
//
// Reuses the block_interact_test.go harness (newBlockLoop / blockPlayer / playerActionPacket /
// drainPackets / countID).

// destroyStageOf decodes the first ClientboundBlockDestruction packet's stage byte.
func destroyStageOf(t *testing.T, ps []pk.Packet) (int8, bool) {
	t.Helper()
	for _, p := range ps {
		if p.ID == int32(packetid.ClientboundBlockDestruction) {
			var id pk.VarInt
			var pos pk.Position
			var stage pk.Byte
			if err := p.Scan(&id, &pos, &stage); err != nil {
				t.Fatalf("decode ClientboundBlockDestruction: %v", err)
			}
			return int8(stage), true
		}
	}
	return 0, false
}

// hasDestroyStage reports whether any ClientboundBlockDestruction in ps carries the given stage.
func hasDestroyStage(t *testing.T, ps []pk.Packet, want int8) bool {
	t.Helper()
	for _, p := range ps {
		if p.ID == int32(packetid.ClientboundBlockDestruction) {
			var id pk.VarInt
			var pos pk.Position
			var stage pk.Byte
			if err := p.Scan(&id, &pos, &stage); err != nil {
				t.Fatalf("decode ClientboundBlockDestruction: %v", err)
			}
			if int8(stage) == want {
				return true
			}
		}
	}
	return false
}

// startDig issues a START_DESTROY_BLOCK for pos at the current gametime.
func startDig(loop *TickLoop, p *tickPlayer, pos pk.Position, seq int32) {
	pa := playerActionPacket(0 /*START*/, pos, 1, seq)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: pa})
}

// stopDig issues a STOP_DESTROY_BLOCK for pos at the current gametime.
func stopDig(loop *TickLoop, p *tickPlayer, pos pk.Position, seq int32) {
	pa := playerActionPacket(2 /*STOP*/, pos, 1, seq)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: pa})
}

// completeSurvivalDig drives a full survival dig of pos to completion: START at gametime 0, then a
// STOP after a large elapsed window so accumulated progress is well past the 0.7 completion
// threshold for any normal breakable block (the slowest v1 block, stone bare-handed, completes at
// elapsed 104; 10000 is comfortably beyond). Used by drop tests that need the block to actually
// break (a survival break is a dig-timer now, not an instant STOP — Plan 17-21).
func completeSurvivalDig(loop *TickLoop, p *tickPlayer, pos pk.Position) {
	p.gameMode = gameModeSurvival
	loop.gametime = 0
	startDig(loop, p, pos, 1)
	loop.gametime = 10000
	stopDig(loop, p, pos, 2)
}

// TestDigStartStoneDoesNotInstaBreak: START on a bare-handed stone block (hardness 1.5, requires
// tool) does NOT break it (1-tick progress = 1.0/1.5/100 = 0.0067 < 1.0), begins a per-tick dig, and
// sends a ClientboundBlockDestruction stage 0 (the first crack overlay). No ack/break yet.
func TestDigStartStoneDoesNotInstaBreak(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.entityID = 1000
	p.gameMode = gameModeSurvival

	target := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(target, block.ToStateID[block.Stone{}], dimMinY)

	loop.gametime = 0
	startDig(loop, p, target, 1)

	// Block is NOT broken on START.
	if got, ok := mgr.GetBlock(target, dimMinY); !ok || block.IsAir(got) {
		t.Fatalf("survival START broke stone instantly: GetBlock = (%v, ok=%v), want still-stone", got, ok)
	}
	if !p.isDestroyingBlock {
		t.Fatalf("survival START did not begin a per-tick dig (isDestroyingBlock=false)")
	}
	if p.destroyPos != target {
		t.Fatalf("destroyPos = %v, want %v", p.destroyPos, target)
	}

	pkts := drainPackets(p.client)
	if n := countID(pkts, packetid.ClientboundBlockChangedAck); n != 0 {
		t.Fatalf("survival START acked (%d), want 0 (no break yet)", n)
	}
	stage, ok := destroyStageOf(t, pkts)
	if !ok {
		t.Fatalf("survival START sent no ClientboundBlockDestruction (the crack overlay)")
	}
	if stage != 0 {
		t.Fatalf("survival START crack stage = %d, want 0 (progress 0.0067 * 10 = 0)", stage)
	}
}

// TestDigStopStoneCompletesAfterEnoughTicks: after digging stone bare-handed for enough ticks, a STOP
// whose accumulated progress >= 0.7 breaks the block, clears the overlay, and acks the sequence.
// Stone per-tick progress = 1/1.5/100 = 0.0066667; 0.7 threshold needs (elapsed+1) >= 105, i.e.
// elapsed >= 104. Drive gametime to start+104 so progress = 0.0066667*105 = 0.7 exactly.
func TestDigStopStoneCompletesAfterEnoughTicks(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.entityID = 1000
	p.gameMode = gameModeSurvival

	target := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(target, block.ToStateID[block.Stone{}], dimMinY)

	loop.gametime = 0
	startDig(loop, p, target, 1) // emits a stage-0 crack overlay; do NOT drain (drain closes the queue)

	// Advance the dig clock so elapsed = 104 (progress = 0.0066667 * 105 = 0.7 == threshold).
	loop.gametime = 104
	const seq = 77
	stopDig(loop, p, target, seq)

	if got, ok := mgr.GetBlock(target, dimMinY); !ok || !block.IsAir(got) {
		t.Fatalf("STOP at progress>=0.7 did not break stone: GetBlock = (%v, ok=%v), want air", got, ok)
	}
	if p.isDestroyingBlock {
		t.Fatalf("after completing STOP, isDestroyingBlock=true, want false")
	}
	if p.hasDelayedDestroy {
		t.Fatalf("after completing STOP, hasDelayedDestroy=true, want false (it completed, not delayed)")
	}

	// Drain once at the end (drainPackets closes the queue, so it must be the last read).
	pkts := drainPackets(p.client)
	if n := countID(pkts, packetid.ClientboundBlockChangedAck); n != 1 {
		t.Fatalf("completing STOP acked %d times, want 1", n)
	}
	if seqGot := findAckSequence(t, pkts); seqGot != seq {
		t.Fatalf("completing STOP ack sequence = %d, want %d", seqGot, seq)
	}
	// A stage-0 START overlay, then the stop's overlay-clear (stage -1) are both present; the LAST
	// ClientboundBlockDestruction is the -1 clear. Assert a -1 clear was emitted.
	if !hasDestroyStage(t, pkts, -1) {
		t.Fatalf("completing STOP did not emit an overlay-clear (stage -1) ClientboundBlockDestruction")
	}
}

// TestDigStopStoneTooEarlySchedulesDelayed: a STOP whose accumulated progress is < 0.7 does NOT break
// the block immediately; it schedules a delayed-destroy that tickBlockBreak finishes once progress
// reaches 1.0. With elapsed=0 (STOP on the same tick as START), stone progress = 0.0067 < 0.7.
func TestDigStopStoneTooEarlySchedulesDelayed(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.entityID = 1000
	p.gameMode = gameModeSurvival

	target := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(target, block.ToStateID[block.Stone{}], dimMinY)

	loop.gametime = 0
	startDig(loop, p, target, 1)
	stopDig(loop, p, target, 2) // STOP immediately, progress << 0.7

	// Not broken yet.
	if got, ok := mgr.GetBlock(target, dimMinY); !ok || block.IsAir(got) {
		t.Fatalf("early STOP broke stone immediately: GetBlock = (%v, ok=%v), want still-stone", got, ok)
	}
	if !p.hasDelayedDestroy {
		t.Fatalf("early STOP did not schedule a delayed-destroy (hasDelayedDestroy=false)")
	}
	if p.delayedDestroyPos != target {
		t.Fatalf("delayedDestroyPos = %v, want %v", p.delayedDestroyPos, target)
	}
	if p.isDestroyingBlock {
		t.Fatalf("early STOP left isDestroyingBlock=true, want false (handed to delayed-destroy)")
	}
	drainPackets(p.client)

	// tickBlockBreak before progress reaches 1.0: still not broken. delayedTickStart=0; progress at
	// gametime=104 is 0.0066667*105 = 0.7 < 1.0 -> not yet.
	loop.gametime = 104
	loop.tickBlockBreak()
	if got, ok := mgr.GetBlock(target, dimMinY); !ok || block.IsAir(got) {
		t.Fatalf("delayed-destroy broke stone before progress>=1.0: GetBlock = (%v, ok=%v)", got, ok)
	}
	if !p.hasDelayedDestroy {
		t.Fatalf("delayed-destroy cleared before progress>=1.0")
	}

	// Drive past progress 1.0: need (elapsed+1) >= 1/0.0066667 = 150, elapsed >= 149.
	loop.gametime = 149
	loop.tickBlockBreak()
	if got, ok := mgr.GetBlock(target, dimMinY); !ok || !block.IsAir(got) {
		t.Fatalf("delayed-destroy did not break stone at progress>=1.0: GetBlock = (%v, ok=%v), want air", got, ok)
	}
	if p.hasDelayedDestroy {
		t.Fatalf("after delayed-destroy completion, hasDelayedDestroy=true, want false")
	}
}

// TestDigCreativeStartBreaksInstantly: a CREATIVE START breaks any block immediately (instabuild ->
// destroyAndAck on START), with no per-tick dig and no progress overlay.
func TestDigCreativeStartBreaksInstantly(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.entityID = 1000
	p.gameMode = gameModeCreative

	target := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(target, block.ToStateID[block.Obsidian{}], dimMinY) // even obsidian breaks instantly in creative

	loop.gametime = 0
	startDig(loop, p, target, 5)

	if got, ok := mgr.GetBlock(target, dimMinY); !ok || !block.IsAir(got) {
		t.Fatalf("creative START did not break obsidian: GetBlock = (%v, ok=%v), want air", got, ok)
	}
	if p.isDestroyingBlock {
		t.Fatalf("creative START began a per-tick dig (isDestroyingBlock=true), want false (instant)")
	}
	if n := countID(drainPackets(p.client), packetid.ClientboundBlockChangedAck); n != 1 {
		t.Fatalf("creative START acked %d times, want 1", n)
	}
}

// TestDigInstaMineZeroHardness: a START on a zero-hardness block (short_grass, destroySpeed 0.0) has
// 1-tick progress 1.0/0.0/30 = +Inf >= 1.0, so it instant-mines on START (no per-tick dig).
func TestDigInstaMineZeroHardness(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.entityID = 1000
	p.gameMode = gameModeSurvival

	target := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(target, block.ToStateID[block.ShortGrass{}], dimMinY)

	loop.gametime = 0
	startDig(loop, p, target, 9)

	if got, ok := mgr.GetBlock(target, dimMinY); !ok || !block.IsAir(got) {
		t.Fatalf("START did not instant-mine zero-hardness short_grass: GetBlock = (%v, ok=%v), want air", got, ok)
	}
	if p.isDestroyingBlock {
		t.Fatalf("instant-mine began a per-tick dig (isDestroyingBlock=true), want false")
	}
	if n := countID(drainPackets(p.client), packetid.ClientboundBlockChangedAck); n != 1 {
		t.Fatalf("instant-mine acked %d times, want 1", n)
	}
}

// TestDigAbortClearsOverlay: an ABORT_DESTROY_BLOCK cancels the dig and sends a ClientboundBlockDestruction
// with stage -1 (clear the crack overlay). The block is untouched.
func TestDigAbortClearsOverlay(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.entityID = 1000
	p.gameMode = gameModeSurvival

	target := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(target, block.ToStateID[block.Stone{}], dimMinY)

	loop.gametime = 0
	startDig(loop, p, target, 1) // emits a stage-0 crack overlay; do NOT drain (drain closes the queue)

	// ABORT (action 1).
	pa := playerActionPacket(1 /*ABORT*/, target, 1, 2)
	loop.applyInput(p, SubtickInput{At: loop.clock.Now(), Packet: pa})

	if p.isDestroyingBlock {
		t.Fatalf("ABORT left isDestroyingBlock=true, want false")
	}
	if got, ok := mgr.GetBlock(target, dimMinY); !ok || block.IsAir(got) {
		t.Fatalf("ABORT broke the block: GetBlock = (%v, ok=%v), want still-stone", got, ok)
	}
	// Drain once at the end. The stream is the START stage-0 overlay then the ABORT stage-(-1) clear.
	if !hasDestroyStage(t, drainPackets(p.client), -1) {
		t.Fatalf("ABORT did not emit an overlay-clear (stage -1) ClientboundBlockDestruction")
	}
}

// TestDigUnbreakableNeverBreaks: bedrock (destroySpeed -1.0, unbreakable) has getDestroyProgress 0.0
// forever, so a START begins a dig but NO amount of elapsed ticks (STOP or delayed-destroy) ever
// breaks it.
func TestDigUnbreakableNeverBreaks(t *testing.T) {
	loop, mgr := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.entityID = 1000
	p.gameMode = gameModeSurvival

	target := pk.Position{X: 1, Y: 64, Z: 1}
	mgr.SetBlock(target, block.ToStateID[block.Bedrock{}], dimMinY)

	loop.gametime = 0
	startDig(loop, p, target, 1)
	// Bedrock is not air, progress is 0.0 < 1.0, so START begins a per-tick dig (does not insta-mine).
	if !p.isDestroyingBlock {
		t.Fatalf("START on bedrock did not begin a per-tick dig")
	}

	// STOP after a huge elapsed: progress = 0.0 * (elapsed+1) = 0.0 < 0.7, so it never completes and
	// (since progress can never reach 1.0) the scheduled delayed-destroy also never finishes.
	loop.gametime = 100000
	stopDig(loop, p, target, 2)
	if got, ok := mgr.GetBlock(target, dimMinY); !ok || block.IsAir(got) {
		t.Fatalf("STOP broke unbreakable bedrock: GetBlock = (%v, ok=%v), want still-bedrock", got, ok)
	}

	// Run the delayed-destroy tick many times: bedrock never breaks.
	for i := 0; i < 5; i++ {
		loop.gametime += 100000
		loop.tickBlockBreak()
	}
	if got, ok := mgr.GetBlock(target, dimMinY); !ok || block.IsAir(got) {
		t.Fatalf("delayed-destroy broke unbreakable bedrock: GetBlock = (%v, ok=%v), want still-bedrock", got, ok)
	}
}

// TestDigProgressGetter verifies getDestroyProgress's exact 1:1 values: unbreakable -> 0, a
// tool-requiring block uses the 100 divisor (bare hand), a non-tool block uses the 30 divisor.
func TestDigProgressGetter(t *testing.T) {
	loop, _ := newBlockLoop()
	p := blockPlayer(loop, 1.5, 65.0, 1.5)
	p.gameMode = gameModeSurvival

	// Stone: hardness 1.5, requiresTool true -> divisor 100. 1.0/1.5/100 = 0.0066666667.
	if got := loop.getDestroyProgress(p, block.ToStateID[block.Stone{}]); !approxEq(got, 1.0/1.5/100.0, 1e-7) {
		t.Fatalf("getDestroyProgress(stone) = %v, want %v", got, float32(1.0/1.5/100.0))
	}
	// Dirt: hardness 0.5, requiresTool false -> divisor 30. 1.0/0.5/30 = 0.06666667.
	if got := loop.getDestroyProgress(p, block.ToStateID[block.Dirt{}]); !approxEq(got, 1.0/0.5/30.0, 1e-7) {
		t.Fatalf("getDestroyProgress(dirt) = %v, want %v", got, float32(1.0/0.5/30.0))
	}
	// Bedrock: unbreakable -> exactly 0.
	if got := loop.getDestroyProgress(p, block.ToStateID[block.Bedrock{}]); got != 0.0 {
		t.Fatalf("getDestroyProgress(bedrock) = %v, want 0", got)
	}
}
