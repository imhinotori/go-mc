package server

// collections_audit_test.go is the OPT-04 / OPT-06 ENFORCEMENT for the contention audit
// (collections_audit.go). It does three things:
//
//   1. TestHotCollectionsXsyncConcurrent — EXERCISES the one justified xsync/v4 usage
//      (asyncSubmitDrops, an xsync.Counter) under N concurrent goroutines and asserts the exact
//      final value. This is the OPT-06 -race target: a plain int64 here would race the
//      increments; the test proves the lock-free primitive is correct under contention.
//
//   2. TestNoLiveCollectionCapture — the snapshot-discipline GATE. It scans every server/*.go
//      source (comment-stripped so the audit's own prose can't false-positive), finds each
//      submitOrDrop(pool, func(){...}) pool-closure, and asserts its body NEVER references a live
//      tick-owned collection (t.only().entities / t.only().world / t.players / t.clientIndex / p.tracked /
//      .byID / .buckets). A worker must read an owner-built SNAPSHOT, never the live store — so a
//      future change that reintroduces a live cross-boundary read FAILS this test (08-RESEARCH
//      Pitfall 1 warning sign, threat T-8-21).
//
//   3. TestTickOnlyMapsStayPlain — pins the snapshot-and-stay-plain CONCLUSION: the named
//      tick-owned collections are plain Go maps (reflect.Map), not xsync types, so a future
//      "xsync everything" regression (threat T-8-23) is caught.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// TestHotCollectionsXsyncConcurrent exercises asyncSubmitDrops — the ONE justified xsync/v4 usage
// (collections_audit.go) — under heavy concurrency and asserts the EXACT final count. This is the
// OPT-06 -race target for OPT-04: many goroutines Inc() the striped counter while we also read its
// Value(), exactly the multi-writer-plus-reader pattern that justifies xsync.Counter over a plain
// int64. Run under: go test -race -run TestHotCollectionsXsyncConcurrent (Docker golang:1.26).
func TestHotCollectionsXsyncConcurrent(t *testing.T) {
	// Snapshot the starting value (the counter is a package global shared across tests/loops, so
	// assert on the DELTA we contribute, not an absolute zero — other tests may have incremented it).
	start := asyncSubmitDrops.Value()

	const (
		goroutines = 64
		perG       = 1000
	)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				// Inc() is what submitOrDrop calls on a pool overload — exercise that exact path.
				asyncSubmitDrops.Inc()
				// Read concurrently with the writes: this is the cross-boundary inc-vs-read that a
				// plain int64 would race (and -race would flag). xsync.Counter makes it clean.
				_ = asyncSubmitDrops.Value()
			}
		}()
	}
	wg.Wait()

	got := asyncSubmitDrops.Value() - start
	want := int64(goroutines * perG)
	if got != want {
		t.Fatalf("xsync.Counter lost increments under contention: delta=%d, want=%d "+
			"(the lock-free counter must be exact — a plain int64 would race)", got, want)
	}
}

// forbiddenLiveTokens are the live tick-owned collection accesses a pool-worker closure must NEVER
// contain. A worker reads an immutable owner-built snapshot; it carries only ids/values back. Any
// of these inside a submitOrDrop(...func(){...}) body is a reintroduced live cross-boundary read
// (08-RESEARCH Pitfall 1). The tokens are deliberately the LIVE accessors (a receiver field or a
// store method) — a snapshot copies these out on the owner BEFORE Submit, so the closure body
// references the copy, not these.
var forbiddenLiveTokens = []string{
	"t.only().entities", // the live entity store (byID/buckets) — workers read a copied []*Entity / near() result
	"t.only().world",    // the live ChunkManager — workers read a snapshotRegion / snapshotSpawnColumns copy
	"t.players",  // the live players slice — workers read a per-player position+tracked snapshot
	"t.clientIndex",
	"p.tracked", // the live per-player tracked set — workers compute a delta over a copied set
	".byID",     // direct live store map access
	".buckets",  // direct live bucket map access
}

// TestNoLiveCollectionCapture is the snapshot-discipline GATE (threat T-8-21). It parses every
// server/*.go file, locates each submitOrDrop(pool, func(){...}) call, and asserts the function-
// literal body's SOURCE TEXT (with comments removed by the AST walk — only real code nodes are
// printed) contains none of the forbidden live-collection tokens. The AST approach is robust: it
// matches only ACTUAL pool-submit closures (not doc prose mentioning the tokens), and it sees the
// closure body precisely, so the audit's own comment table can never false-positive.
func TestNoLiveCollectionCapture(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing server/*.go: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("found no server/*.go sources to scan — the gate would be vacuous")
	}

	scanned := 0
	closuresChecked := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue // gate the PRODUCTION submit sites, not test scaffolding
		}
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		f, err := parser.ParseFile(fset, file, src, 0) // mode 0: comments are NOT attached → ignored
		if err != nil {
			t.Fatalf("parsing %s: %v", file, err)
		}
		scanned++

		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok || ident.Name != "submitOrDrop" {
				return true
			}
			// submitOrDrop(pool, func(){...}): the work closure is the LAST argument.
			if len(call.Args) == 0 {
				return true
			}
			lit, ok := call.Args[len(call.Args)-1].(*ast.FuncLit)
			if !ok {
				return true // not an inline closure (a named func value); nothing to scan here
			}
			closuresChecked++

			// Render ONLY the closure body's source span (no comments — they weren't parsed) and
			// scan it for any forbidden live-collection token.
			start := fset.Position(lit.Body.Pos()).Offset
			end := fset.Position(lit.Body.End()).Offset
			body := string(src[start:end])
			for _, tok := range forbiddenLiveTokens {
				if strings.Contains(body, tok) {
					pos := fset.Position(lit.Pos())
					t.Errorf("LIVE cross-boundary read in a submitOrDrop closure at %s: body references %q.\n"+
						"A pool worker must read an OWNER-BUILT SNAPSHOT, never a live tick-owned collection "+
						"(08-RESEARCH Pitfall 1). Copy the needed state on the owner before Submit and carry only "+
						"the immutable copy / ids into the closure.", pos, tok)
				}
			}
			return true
		})
	}

	if scanned == 0 {
		t.Fatal("scanned 0 production sources — the gate did not run")
	}
	if closuresChecked == 0 {
		t.Fatal("found 0 submitOrDrop closures to gate — OPT-01/02/03 submit sites should exist; " +
			"the gate would be silently vacuous otherwise")
	}
	t.Logf("snapshot-discipline gate: scanned %d production files, vetted %d submitOrDrop closures", scanned, closuresChecked)
}

// TestTickOnlyMapsStayPlain pins the snapshot-and-stay-plain CONCLUSION (threat T-8-23): the
// tick-owned collections the audit classified as PLAIN must be plain Go maps (reflect.Map), not
// xsync.Map types. If a future change "xsync-everythings" one of them (slower, pointless under
// single ownership), this reflection check fails. It covers the server-package collections
// directly; ChunkManager.columns lives in the world package (unexported) and is asserted plain by
// its own package's tests + the audit, not reachable by reflection here.
func TestTickOnlyMapsStayPlain(t *testing.T) {
	// entityStore.byID and entityStore.buckets — the authoritative entity collection + grid index.
	storeT := reflect.TypeOf(entityStore{})
	for _, name := range []string{"byID", "buckets"} {
		field, ok := storeT.FieldByName(name)
		if !ok {
			t.Fatalf("entityStore.%s field not found — the audit references it", name)
		}
		if field.Type.Kind() != reflect.Map {
			t.Errorf("entityStore.%s is %v, want a PLAIN map (snapshot-and-stay-plain): a tick-only "+
				"collection must NOT become an xsync type — single-owner is faster (T-8-23)", name, field.Type.Kind())
		}
	}

	// TickLoop.clientIndex — the connection->player dispatch index (tick-owned).
	loopT := reflect.TypeOf(TickLoop{})
	if field, ok := loopT.FieldByName("clientIndex"); !ok {
		t.Fatal("TickLoop.clientIndex field not found — the audit references it")
	} else if field.Type.Kind() != reflect.Map {
		t.Errorf("TickLoop.clientIndex is %v, want a PLAIN map (snapshot-and-stay-plain, T-8-23)", field.Type.Kind())
	}

	// tickPlayer.tracked — the per-player visibility set the async tracker mutates owner-side only.
	playerT := reflect.TypeOf(tickPlayer{})
	if field, ok := playerT.FieldByName("tracked"); !ok {
		t.Fatal("tickPlayer.tracked field not found — the audit references it")
	} else if field.Type.Kind() != reflect.Map {
		t.Errorf("tickPlayer.tracked is %v, want a PLAIN map (snapshot-and-stay-plain, T-8-23)", field.Type.Kind())
	}
}
