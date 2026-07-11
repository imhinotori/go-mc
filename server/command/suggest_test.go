package command

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

// suggest_test.go -- Graph.Suggest walk: literal-frontier and argument-frontier completions.

func noopHandler(context.Context, []ParsedData) error { return nil }

// buildSuggestGraph: /gamemode <mode:gamemode+players-suggest> ; /time (set|add|query) ...
func buildSuggestGraph() *Graph {
	g := NewGraph()
	mode := g.Argument("mode", GameModeParser()).Suggests(SuggestPlayers, "").HandleFunc(noopHandler)
	g.AppendLiteral(g.Literal("gamemode").AppendArgument(mode).Unhandle())
	g.AppendLiteral(g.Literal("time").
		AppendLiteral(g.Literal("set").AppendArgument(g.Argument("t", TimeParser{}).HandleFunc(noopHandler)).Unhandle()).
		AppendLiteral(g.Literal("add").AppendArgument(g.Argument("t", TimeParser{}).HandleFunc(noopHandler)).Unhandle()).
		AppendLiteral(g.Literal("query").HandleFunc(noopHandler)).
		Unhandle())
	return g
}

func sorted(s []string) []string { sort.Strings(s); return s }

func TestSuggestRootLiterals(t *testing.T) {
	g := buildSuggestGraph()
	start, cands := g.Suggest("ga", nil)
	if start != 0 {
		t.Fatalf("start = %d, want 0", start)
	}
	if !reflect.DeepEqual(cands, []string{"gamemode"}) {
		t.Fatalf("root 'ga' cands = %v, want [gamemode]", cands)
	}
}

func TestSuggestSubLiterals(t *testing.T) {
	g := buildSuggestGraph()
	start, cands := g.Suggest("time ", nil)
	if start != 5 {
		t.Fatalf("start = %d, want 5", start)
	}
	if !reflect.DeepEqual(sorted(cands), []string{"add", "query", "set"}) {
		t.Fatalf("'time ' cands = %v, want [add query set]", sorted(cands))
	}
	// prefix filter
	_, cands = g.Suggest("time s", nil)
	if !reflect.DeepEqual(cands, []string{"set"}) {
		t.Fatalf("'time s' cands = %v, want [set]", cands)
	}
}

func TestSuggestArgumentResolver(t *testing.T) {
	g := buildSuggestGraph()
	resolve := func(kind SuggestKind, registry string) []string {
		if kind == SuggestPlayers {
			return []string{"Steve", "Alex", "Notch"}
		}
		return nil
	}
	start, cands := g.Suggest("gamemode ", resolve)
	if start != 9 {
		t.Fatalf("start = %d, want 9", start)
	}
	if !reflect.DeepEqual(sorted(cands), []string{"Alex", "Notch", "Steve"}) {
		t.Fatalf("'gamemode ' player cands = %v", sorted(cands))
	}
	// prefix filter on the argument frontier
	_, cands = g.Suggest("gamemode S", resolve)
	if !reflect.DeepEqual(cands, []string{"Steve"}) {
		t.Fatalf("'gamemode S' cands = %v, want [Steve]", cands)
	}
}

func TestSuggestNoResolverArgumentEmpty(t *testing.T) {
	g := buildSuggestGraph()
	// A nil resolver yields no argument-frontier candidates (only literal children would show).
	_, cands := g.Suggest("gamemode ", nil)
	if len(cands) != 0 {
		t.Fatalf("nil resolver on arg frontier = %v, want empty", cands)
	}
}
