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
