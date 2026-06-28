package starlark

import (
	"errors"
	"os"
	"strings"
	"testing"

	"go.starlark.net/starlark"
)

// TestStepBudgetHalts proves the DoS mitigation (T-21-04): a runaway loop INSIDE
// a def hits the per-Thread step budget and returns a *starlark.EvalError
// ("too many steps") at the exact cap — it does NOT hang and is NOT a parse
// reject. The test builds its OWN Thread with a SMALL cap (50_000) via the
// package's safe options so the budget fires fast and the exact step count can
// be asserted, rather than burning the 10M production budget.
func TestStepBudgetHalts(t *testing.T) {
	const testCap uint64 = 50_000

	src, err := os.ReadFile("testdata/infinite_loop.star")
	if err != nil {
		t.Fatalf("read infinite_loop.star: %v", err)
	}

	th := &starlark.Thread{Name: "budget-test"}
	th.SetMaxExecutionSteps(testCap)

	_, err = starlark.ExecFileOptions(defaultFileOptions(), th, "infinite_loop.star", src, safeGlobals())
	if err == nil {
		t.Fatal("expected a budget error, got nil (loop may not be inside a def — P1)")
	}

	// GUARD against P1: the error must be a BUDGET hit, not a top-level-loop
	// PARSE reject (which would never exercise the step counter, steps=0).
	if strings.Contains(err.Error(), "not within a function") {
		t.Fatalf("P1: loop was top-level (parse reject), budget never ran: %v", err)
	}

	var evalErr *starlark.EvalError
	if !errors.As(err, &evalErr) {
		t.Fatalf("expected *starlark.EvalError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "too many steps") {
		t.Fatalf("expected 'too many steps', got: %v", err)
	}
	if th.ExecutionSteps() != testCap {
		t.Fatalf("expected steps==%d (budget fired), got %d — P1 hollow-test risk", testCap, th.ExecutionSteps())
	}
}

// TestRecursionRejected proves the stack-exhaustion mitigation (T-21-05): a
// self-recursive .star function called at module load is a dynamic error
// ("function f called recursively") because the recursion guard is ON
// (FileOptions.Recursion is the zero-value false). Recursion is NOT enabled.
func TestRecursionRejected(t *testing.T) {
	_, err := Load("testdata/recursive.star")
	if err == nil {
		t.Fatal("expected a recursion error, got nil (recursion may be enabled — guard OFF)")
	}
	if !strings.Contains(err.Error(), "function f called recursively") {
		t.Fatalf("expected 'function f called recursively', got: %v", err)
	}
}

// TestNoIOBuiltins proves the info-disclosure/tampering mitigation (T-21-06): a
// .star attempting I/O (open(...)) at module load fails "undefined: open" —
// there is no fs/network builtin in safeGlobals or the Starlark universe.
func TestNoIOBuiltins(t *testing.T) {
	_, err := Load("testdata/tries_io.star")
	if err == nil {
		t.Fatal("expected an undefined-builtin error, got nil (an I/O builtin may be exposed)")
	}
	if !strings.Contains(err.Error(), "undefined: open") {
		t.Fatalf("expected 'undefined: open', got: %v", err)
	}
}
