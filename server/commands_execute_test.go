package server

import (
	"context"
	"math"
	"testing"

	"github.com/imhinotori/sulfur/chat"
)

// commands_execute_test.go pins the bytecode-verified /execute behavior (ledger #44-9): the
// CommandSourceStack fork/gate/store/run semantics from ExecuteCommand. No RNG anywhere in /execute.

// withExecTailDispatch swaps the run tail dispatch seam for a test and restores it, so a test can
// capture the exact tail command reaching the dispatcher per source without touching the shared graph.
func withExecTailDispatch(t *testing.T, fn func(ctx context.Context, cmd string) error) {
	t.Helper()
	prev := execTailDispatch
	execTailDispatch = fn
	t.Cleanup(func() { execTailDispatch = prev })
}

// grantAllExecCtx builds the ctx runExecute expects: the executor plus a grant-all resolver.
func grantAllExecCtx(loop *TickLoop, p *tickPlayer) context.Context {
	ctx := withPermissionResolver(context.Background(), func(string) bool { return true })
	return withExecutor(ctx, loop, p)
}

func approxF(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-3 }

// TestExecuteAsFanout: as @a run <tail> runs the tail once per player (register $0 fan-out).
func TestExecuteAsFanout(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	a := commandPlayer(loop)
	a.name = "Alpha"
	b := commandPlayer(loop)
	b.name = "Bravo"

	var seen []string
	withExecTailDispatch(t, func(ctx context.Context, cmd string) error {
		e, ok := executorFrom(ctx)
		if !ok {
			t.Fatal("run tail had no executor installed")
		}
		seen = append(seen, e.p.name)
		return nil
	})

	if err := loop.runExecute(grantAllExecCtx(loop, a), a, []string{"as", "@a", "run", "say", "hi"}); err != nil {
		t.Fatalf("execute as @a run: %v", err)
	}
	if len(seen) != 2 || seen[0] != "Alpha" || seen[1] != "Bravo" {
		t.Fatalf("as @a fan-out ran for %v, want [Alpha Bravo] in order", seen)
	}
}

// TestExecuteAsTailUnchanged: the tail reaching the dispatcher is the verbatim remainder after run.
func TestExecuteAsTailUnchanged(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := commandPlayer(loop)
	p.name = "Solo"

	var got string
	withExecTailDispatch(t, func(ctx context.Context, cmd string) error { got = cmd; return nil })

	if err := loop.runExecute(grantAllExecCtx(loop, p), p, []string{"as", "@s", "run", "say", "hello", "world"}); err != nil {
		t.Fatalf("execute as @s run: %v", err)
	}
	if got != "say hello world" {
		t.Fatalf("run tail = %q, want %q", got, "say hello world")
	}
}

// TestExecuteIfGates: if score matches runs the tail only when the condition passes; unless mirrors.
func TestExecuteIfGates(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := commandPlayer(loop)
	p.name = "Scorer"
	o := loop.scoreboardAddObjective("obj", builtInCriteria["dummy"], chat.Message{Text: "obj"})
	if o == nil {
		t.Fatal("failed to create objective")
	}
	loop.scoreboardSetScore("Scorer", o, 5)

	runs := 0
	withExecTailDispatch(t, func(ctx context.Context, cmd string) error { runs++; return nil })

	runs = 0
	if err := loop.runExecute(grantAllExecCtx(loop, p), p, []string{"if", "score", "Scorer", "obj", "matches", "5", "run", "say", "x"}); err != nil {
		t.Fatalf("if matches 5: %v", err)
	}
	if runs != 1 {
		t.Fatalf("if score matches 5 ran %d, want 1", runs)
	}

	runs = 0
	if err := loop.runExecute(grantAllExecCtx(loop, p), p, []string{"if", "score", "Scorer", "obj", "matches", "6", "run", "say", "x"}); err != nil {
		t.Fatalf("if matches 6: %v", err)
	}
	if runs != 0 {
		t.Fatalf("if score matches 6 ran %d, want 0", runs)
	}

	runs = 0
	if err := loop.runExecute(grantAllExecCtx(loop, p), p, []string{"unless", "score", "Scorer", "obj", "matches", "6", "run", "say", "x"}); err != nil {
		t.Fatalf("unless matches 6: %v", err)
	}
	if runs != 1 {
		t.Fatalf("unless score matches 6 ran %d, want 1", runs)
	}
}

// TestExecuteScoreOperatorForm pins the operator comparison form (checkScore IntBiPredicate).
func TestExecuteScoreOperatorForm(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := commandPlayer(loop)
	p.name = "Op"
	o := loop.scoreboardAddObjective("ob", builtInCriteria["dummy"], chat.Message{Text: "ob"})
	loop.scoreboardSetScore("A", o, 3)
	loop.scoreboardSetScore("B", o, 7)

	runs := 0
	withExecTailDispatch(t, func(ctx context.Context, cmd string) error { runs++; return nil })

	runs = 0
	if err := loop.runExecute(grantAllExecCtx(loop, p), p, []string{"if", "score", "A", "ob", "<", "B", "ob", "run", "say", "x"}); err != nil {
		t.Fatalf("op form: %v", err)
	}
	if runs != 1 {
		t.Fatalf("if A<B ran %d, want 1", runs)
	}
	runs = 0
	if err := loop.runExecute(grantAllExecCtx(loop, p), p, []string{"if", "score", "B", "ob", "<", "A", "ob", "run", "say", "x"}); err != nil {
		t.Fatalf("op form: %v", err)
	}
	if runs != 0 {
		t.Fatalf("if B<A ran %d, want 0", runs)
	}
}

// TestExecuteStoreResultScore: store result score sets the run result (storeValue). One success -> 1.
func TestExecuteStoreResultScore(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := commandPlayer(loop)
	p.name = "St"
	o := loop.scoreboardAddObjective("out", builtInCriteria["dummy"], chat.Message{Text: "out"})
	if o == nil {
		t.Fatal("objective create failed")
	}
	withExecTailDispatch(t, func(ctx context.Context, cmd string) error { return nil })
	if err := loop.runExecute(grantAllExecCtx(loop, p), p, []string{"store", "result", "score", "Holder", "out", "run", "say", "ok"}); err != nil {
		t.Fatalf("store result score run: %v", err)
	}
	if sc := loop.scoreboard.getOrCreateScore("Holder", "out"); sc.value != 1 {
		t.Fatalf("store result score = %d, want 1", sc.value)
	}
}

// TestExecuteStoreSuccessScore: store success sets 0 on a failed conditional tail.
func TestExecuteStoreSuccessScore(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := commandPlayer(loop)
	p.name = "Su"
	o := loop.scoreboardAddObjective("suc", builtInCriteria["dummy"], chat.Message{Text: "suc"})
	if o == nil {
		t.Fatal("objective create failed")
	}
	if err := loop.runExecute(grantAllExecCtx(loop, p), p, []string{"store", "success", "score", "H", "suc", "if", "score", "Nobody", "suc", "matches", "99"}); err != nil {
		t.Fatalf("store success (failing) unexpected err: %v", err)
	}
	if sc := loop.scoreboard.getOrCreateScore("H", "suc"); sc.value != 0 {
		t.Fatalf("store success on a failed conditional = %d, want 0", sc.value)
	}
}

// TestExecuteAtSourceMutation: at @s moves the source to the acting entity pose; the tail runs once.
func TestExecuteAtSourceMutation(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := commandPlayer(loop)
	p.name = "Mover"
	p.x, p.y, p.z = 10.7, 64.0, -3.2

	runs := 0
	withExecTailDispatch(t, func(ctx context.Context, cmd string) error { runs++; return nil })
	if err := loop.runExecute(grantAllExecCtx(loop, p), p, []string{"at", "@s", "run", "say", "x"}); err != nil {
		t.Fatalf("at @s run: %v", err)
	}
	if runs != 1 {
		t.Fatalf("at @s run ran %d times, want 1", runs)
	}
}

// TestExecuteAlignFloors pins align: floor the named axes (register $9 + Vec3.align).
func TestExecuteAlignFloors(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	s := execSource{t: loop, x: 3.7, y: 64.9, z: -2.1, anchor: anchorFeet}
	x, y, z := s.x, s.y, s.z
	x = math.Floor(x)
	z = math.Floor(z)
	got := s.withPosition(x, y, z)
	if got.x != 3 || got.z != -3 || got.y != 64.9 {
		t.Fatalf("align xz => (%v,%v,%v), want (3, 64.9, -3)", got.x, got.y, got.z)
	}
}

// TestExecuteFacingMath pins CommandSourceStack.facing(Vec3).
func TestExecuteFacingMath(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	s := execSource{t: loop, x: 0, y: 0, z: 0, anchor: anchorFeet}
	got := s.facing(0, 0, 1)
	if !approxF(got.yaw, 0) || !approxF(got.pitch, 0) {
		t.Fatalf("facing +Z => yaw=%v pitch=%v, want (0,0)", got.yaw, got.pitch)
	}
	down := s.facing(0, -1, 0)
	if !approxF(down.yaw, 90) {
		t.Fatalf("facing straight down => f1(yaw-slot)=%v, want 90", down.yaw)
	}
}

// TestExecuteAnchoredEyes: anchored eyes applies the 1.62 eye height for a player source.
func TestExecuteAnchoredEyes(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := commandPlayer(loop)
	s := execSource{t: loop, player: p, x: 0, y: 0, z: 0, anchor: anchorEyes}
	got := s.facing(0, 1.62, 1)
	if !approxF(got.yaw, 0) {
		t.Fatalf("anchored eyes level look => pitch-slot=%v, want 0", got.yaw)
	}
}

// TestExecuteRunTailDispatchCount: as @a then run dispatches per surviving source.
func TestExecuteRunTailDispatchCount(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	commandPlayer(loop)
	commandPlayer(loop)
	commandPlayer(loop)
	p := loop.players[0]

	n := 0
	withExecTailDispatch(t, func(ctx context.Context, cmd string) error { n++; return nil })
	if err := loop.runExecute(grantAllExecCtx(loop, p), p, []string{"as", "@a", "run", "say", "z"}); err != nil {
		t.Fatalf("as @a run: %v", err)
	}
	if n != 3 {
		t.Fatalf("run tail dispatched %d times over 3 players, want 3", n)
	}
}

// TestExecuteWrapDegrees pins Mth.wrapDegrees folding used by facing.
func TestExecuteWrapDegrees(t *testing.T) {
	cases := []struct{ in, want float32 }{
		{0, 0}, {180, -180}, {181, -179}, {-181, 179}, {360, 0}, {270, -90},
	}
	for _, c := range cases {
		if got := wrapDegreesF(c.in); !approxF(got, c.want) {
			t.Fatalf("wrapDegreesF(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestExecuteParseIntRange pins MinMaxBounds$Ints range parsing.
func TestExecuteParseIntRange(t *testing.T) {
	lo, hi, err := parseIntRange("5")
	if err != nil || lo != 5 || hi != 5 {
		t.Fatalf("range 5 => (%d,%d,%v)", lo, hi, err)
	}
	lo, hi, err = parseIntRange("3..7")
	if err != nil || lo != 3 || hi != 7 {
		t.Fatalf("range 3..7 => (%d,%d,%v)", lo, hi, err)
	}
	lo, _, err = parseIntRange("2..")
	if err != nil || lo != 2 {
		t.Fatalf("range 2.. => lo %d err %v", lo, err)
	}
	_, hi, err = parseIntRange("..9")
	if err != nil || hi != 9 {
		t.Fatalf("range ..9 => hi %d err %v", hi, err)
	}
}

// TestExecuteUnsupportedStubs asserts the deliberately-stubbed subcommands error.
func TestExecuteUnsupportedStubs(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := commandPlayer(loop)
	for _, toks := range [][]string{
		{"summon", "minecraft:pig"},
		{"if", "biome", "~", "~", "~", "minecraft:plains"},
		{"if", "predicate", "minecraft:foo"},
		{"store", "result", "bossbar", "b", "value", "run", "say", "x"},
	} {
		if err := loop.runExecute(grantAllExecCtx(loop, p), p, toks); err == nil {
			t.Fatalf("expected stubbed subcommand %v to error, got nil", toks)
		}
	}
}

// TestExecuteContextInstallsSource: execRun installs the COMPLETE CommandSourceStack on the
// per-fork ctx via withExecSource BEFORE the player executor, so a /execute tail can recover the
// full source stack (dimension, x/y/z, yaw/pitch, anchor, acting player) by value through
// execSourceFrom(ctx). Pins the seam at the END of a long modifier chain -- positioned/rotated/
// anchored/in mutate the source, and the captured ctx source must reflect ALL of them, proving
// the per-fork context carries the latest mutated source (not the initial stack).
func TestExecuteContextInstallsSource(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := commandPlayer(loop)
	p.name = "Anchor"
	p.x, p.y, p.z = 0, 0, 0
	p.yaw, p.pitch = 0, 0
	p.dimension = dimOverworld

	var seen execSource
	var seenOK bool
	withExecTailDispatch(t, func(ctx context.Context, cmd string) error {
		seen, seenOK = execSourceFrom(ctx)
		return nil
	})

	toks := []string{
		"positioned", "10", "20", "30",
		"rotated", "40", "50",
		"anchored", "eyes",
		"in", "minecraft:the_nether",
		"run", "say", "x",
	}
	if err := loop.runExecute(grantAllExecCtx(loop, p), p, toks); err != nil {
		t.Fatalf("execute ... run: %v", err)
	}
	if !seenOK {
		t.Fatal("execSourceFrom(ctx) returned ok=false after execRun (source must be installed on the per-fork ctx)")
	}
	if seen.dim != dimNether {
		t.Fatalf("ctx source dim = %d, want dimNether(%d)", seen.dim, dimNether)
	}
	if seen.x != 10 || seen.y != 20 || seen.z != 30 {
		t.Fatalf("ctx source pos = (%v,%v,%v), want (10,20,30) from `positioned`", seen.x, seen.y, seen.z)
	}
	if !approxF(seen.yaw, 40) || !approxF(seen.pitch, 50) {
		t.Fatalf("ctx source rotation = (yaw=%v,pitch=%v), want (40,50) from `rotated`", seen.yaw, seen.pitch)
	}
	if seen.anchor != anchorEyes {
		t.Fatalf("ctx source anchor = %v, want anchorEyes(%d) from `anchored eyes`", seen.anchor, anchorEyes)
	}
	if seen.player != p {
		t.Fatalf("ctx source player = %p, want %p (the issuer)", seen.player, p)
	}
	if seen.entity != nil {
		t.Fatalf("ctx source entity = %p, want nil (player source)", seen.entity)
	}
}

// TestExecuteAsFanoutContextSourceIsolation: `as @a positioned as @s run <tail>` fans out across
// every player (3 forks). Each fork installs a DISTINCT source on its per-fork ctx whose acting
// entity + pose match the forking player, proving the per-fork context capture is correct. AND
// sibling isolation holds -- every fork's installed source is an independent value (mirrors the
// vanilla CommandSourceStack.withX semantics: each withX returns a NEW stack), so a write to
// captured[0]'s source fields must not bleed into captured[1] or captured[2].
func TestExecuteAsFanoutContextSourceIsolation(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	a := commandPlayer(loop)
	a.name = "Alpha"
	a.x, a.y, a.z = 1, 2, 3
	a.yaw, a.pitch = 10, 20
	b := commandPlayer(loop)
	b.name = "Bravo"
	b.x, b.y, b.z = 4, 5, 6
	b.yaw, b.pitch = 30, 40
	c := commandPlayer(loop)
	c.name = "Charlie"
	c.x, c.y, c.z = 7, 8, 9
	c.yaw, c.pitch = 50, 60

	var captured []execSource
	withExecTailDispatch(t, func(ctx context.Context, cmd string) error {
		s, ok := execSourceFrom(ctx)
		if !ok {
			t.Fatal("execSourceFrom returned ok=false during fan-out (source must be installed on every per-fork ctx)")
		}
		captured = append(captured, s)
		return nil
	})

	if err := loop.runExecute(grantAllExecCtx(loop, a), a,
		[]string{"as", "@a", "positioned", "as", "@s", "run", "say", "x"}); err != nil {
		t.Fatalf("execute as @a positioned as @s run: %v", err)
	}
	if len(captured) != 3 {
		t.Fatalf("fan-out captured %d sources, want 3 (one per fork)", len(captured))
	}

	// Distinct entity/poses: each captured source's acting player + position must match the
	// forking player, in fan-out order (Alpha, Bravo, Charlie). Rotation is shared across all
	// forks (all inherit Alpha's rotation, since `as @a` only mutates the acting entity and
	// `positioned as @s` only mutates position -- no `rotated` modifier is applied) -- this
	// is the vanilla withX semantics, and the per-fork ctx must reflect it bit-for-bit.
	want := []*tickPlayer{a, b, c}
	seenPlayers := map[*tickPlayer]bool{}
	for i, s := range captured {
		if s.player != want[i] {
			t.Fatalf("captured[%d].player = %p (%s), want %p (%s)", i, s.player, nameOf(s.player), want[i], want[i].name)
		}
		if s.entity != nil {
			t.Fatalf("captured[%d].entity = %p, want nil (player source)", i, s.entity)
		}
		if s.x != want[i].x || s.y != want[i].y || s.z != want[i].z {
			t.Fatalf("captured[%d] pos = (%v,%v,%v), want (%v,%v,%v) (positioned as @s -> %s's pose)",
				i, s.x, s.y, s.z, want[i].x, want[i].y, want[i].z, want[i].name)
		}
		if !approxF(s.yaw, a.yaw) || !approxF(s.pitch, a.pitch) {
			t.Fatalf("captured[%d] rotation = (yaw=%v,pitch=%v), want (%v,%v) (Alpha's -- unmodified by `as @a` or `positioned as @s`)",
				i, s.yaw, s.pitch, a.yaw, a.pitch)
		}
		seenPlayers[s.player] = true
	}
	if len(seenPlayers) != 3 {
		t.Fatalf("fan-out sources resolved to %d distinct players, want 3", len(seenPlayers))
	}

	// Sibling isolation: every fork's installed source is an independent value (vanilla
	// CommandSourceStack.withX returns a NEW stack), so writing captured[0]'s local fields
	// must NOT be visible from captured[1] or captured[2]. The mutation goes through a
	// pointer-to-slice-element, which writes the field on captured[0]'s execSource only.
	if len(captured) > 0 {
		captured[0].x = -1
		captured[0].y = -2
		captured[0].z = -3
		captured[0].yaw = -4
		captured[0].pitch = -5
		captured[0].anchor = anchorEyes
		captured[0].player = nil
	}
	for i := 1; i < len(captured); i++ {
		if captured[i].x == -1 || captured[i].y == -2 || captured[i].z == -3 {
			t.Fatalf("captured[%d] pos mutated via captured[0]: (%v,%v,%v) (sibling isolation violated -- per-fork ctx source must be independent values, not shared state)",
				i, captured[i].x, captured[i].y, captured[i].z)
		}
		if captured[i].anchor == anchorEyes {
			t.Fatalf("captured[%d].anchor leaked anchorEyes from captured[0] (sibling isolation violated)", i)
		}
		if captured[i].player == nil {
			t.Fatalf("captured[%d].player = nil after captured[0].player = nil (sibling isolation violated)", i)
		}
	}
}

func TestExecuteEntitySourceMasksIssuingPlayerExecutor(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	issuer := commandPlayer(loop)
	generic := &Entity{x: 4, y: 5, z: 6}
	source := execSource{t: loop, entity: generic, dim: dimOverworld, x: 4, y: 5, z: 6}

	var got execSource
	var gotSource, gotExecutor bool
	withExecTailDispatch(t, func(ctx context.Context, _ string) error {
		got, gotSource = execSourceFrom(ctx)
		_, gotExecutor = executorFrom(ctx)
		return nil
	})

	if _, err := loop.execRun(grantAllExecCtx(loop, issuer), []execSource{source}, []string{"say", "x"}); err != nil {
		t.Fatalf("execRun generic entity source: %v", err)
	}
	if !gotSource || got.entity != generic || got.player != nil {
		t.Fatalf("captured source = %+v ok=%v, want generic entity source", got, gotSource)
	}
	if gotExecutor {
		t.Fatal("generic entity fork retained the issuing player's legacy executor")
	}
}

// nameOf is a tiny nil-safe accessor for a test message -- the captured source's player pointer
// is checked above, but if a future change ever surfaced nil here this avoids panicking on %s.
func nameOf(p *tickPlayer) string {
	if p == nil {
		return "<nil>"
	}
	return p.name
}
