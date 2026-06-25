package server

// STUB created by 17-01 so the tick pipeline compiles. 17-02 (GAMEPLAY-05) OWNS and
// OVERWRITES this file with the real FlowingFluid port + scheduled-tick queue. The call site
// (tick_phases.go tickWorld -> t.tickFluids()) and the TickLoop.fluidSchedule field
// declaration (tick.go) are NOT touched by 17-02 — 17-02 edits ONLY this file. 17-02 lazily
// constructs the queue inside tickFluids (a nil-check), so it never edits SetWorld (which
// lives in the shared tick.go).

// fluidScheduleQueue is the GAMEPLAY-05 scheduled-fluid-tick queue. It is an empty stub here;
// 17-02 fills it with the per-gametime bucket map (or min-heap) the FlowingFluid pass drains.
// TickLoop.fluidSchedule (tick.go) holds a *fluidScheduleQueue; a nil pointer is a valid
// "no fluids scheduled" state that tickFluids treats as a no-op.
type fluidScheduleQueue struct{}

// tickFluids is the GAMEPLAY-05 fluid pass, called from tickWorld each tick. STUB: a no-op
// until 17-02 ports FlowingFluid.tick/spread/getNewLiquid and drains the scheduled queue here
// in deterministic order. A nil t.fluidSchedule drains to nothing.
func (t *TickLoop) tickFluids() {}
