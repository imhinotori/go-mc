package server

import (
	"log"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// tick_profile.go is a DIAGNOSTIC, opt-in (SULFUR_TICK_PROFILE=1) per-phase timer for
// tickOnce. It is NOT part of the vanilla tick pipeline and mutates NO game state — it
// exists to attribute a slow tick (the "feels like low TPS / movement chops" report,
// i.e. the MSPTp99 spikes) to the exact coordinator phase that ate the time. When off
// (the default) phase() returns a no-op closure and the whole thing costs one atomic load.
//
// USAGE: wrap a coordinator phase call as `defer t.prof("tickWorld")()`-style is awkward
// for statements, so the shape is:
//
//	stop := t.profStart("tickWorld"); t.tickWorld(); stop()
//
// or the sugar `t.profPhase("tickWorld", t.tickWorld)`. On a tick whose TOTAL exceeds
// profSlowThreshold the accumulated per-phase breakdown is logged, sorted worst-first.

// tickProfileOn is resolved once from the env (‑1 unknown, 0 off, 1 on).
var tickProfileOn int32 = -1

// profSlowThreshold: only ticks slower than this dump their phase breakdown, so the log
// shows the SPIKES (the p99 stalls) not every steady 25ms tick. 100ms == 2× the budget.
const profSlowThreshold = 100 * time.Millisecond

func tickProfileEnabled() bool {
	if v := atomic.LoadInt32(&tickProfileOn); v != -1 {
		return v == 1
	}
	on := int32(0)
	if os.Getenv("SULFUR_TICK_PROFILE") == "1" {
		on = 1
	}
	atomic.StoreInt32(&tickProfileOn, on)
	return on == 1
}

// profSample is one phase's accumulated cost within the current tick.
type profSample struct {
	name string
	dur  time.Duration
}

// profReset clears the current tick's per-phase accumulator. Called at the top of a
// profiled tickOnce. Owner-goroutine only (the whole profiler runs on the tick).
func (t *TickLoop) profReset() {
	if !tickProfileEnabled() {
		return
	}
	t.profSamples = t.profSamples[:0]
}

// profStart begins timing a phase; the returned closure stops it and records the sample.
// A no-op (returns a shared empty closure) when profiling is off.
func (t *TickLoop) profStart(name string) func() {
	if !tickProfileEnabled() {
		return profNoop
	}
	s := t.clock.Now()
	return func() {
		t.profSamples = append(t.profSamples, profSample{name: name, dur: t.clock.Now().Sub(s)})
	}
}

// profPhase is the sugar for timing a phase that is a plain niladic method call.
func (t *TickLoop) profPhase(name string, fn func()) {
	stop := t.profStart(name)
	fn()
	stop()
}

var profNoop = func() {}

// profDump logs the per-phase breakdown for a tick whose total exceeded profSlowThreshold,
// sorted worst-first. Called at the end of a profiled tickOnce with the tick's total cost.
func (t *TickLoop) profDump(total time.Duration) {
	if !tickProfileEnabled() || total < profSlowThreshold {
		return
	}
	samples := make([]profSample, len(t.profSamples))
	copy(samples, t.profSamples)
	sort.Slice(samples, func(i, j int) bool { return samples[i].dur > samples[j].dur })
	var b strings.Builder
	for i, s := range samples {
		if i >= 8 {
			break // top 8 offenders is enough to name the culprit
		}
		if s.dur < 500*time.Microsecond {
			break // don't spam sub-ms phases
		}
		b.WriteString(s.name)
		b.WriteString("=")
		b.WriteString(s.dur.Round(100 * time.Microsecond).String())
		b.WriteString(" ")
	}
	log.Printf("SLOW TICK gametime=%d total=%s | %s", t.gametime, total.Round(100*time.Microsecond), b.String())
}
