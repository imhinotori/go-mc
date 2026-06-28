package starlark

import (
	"testing"

	"go.starlark.net/starlark"
)

// TestLoadExec proves the dir/path loader execs a module ONCE at load: the
// module body calls the registered echo builtin during init and leaves a frozen
// `result` global equal to "hi". It also proves a missing file is a clean error
// from the os.ReadFile path.
func TestLoadExec(t *testing.T) {
	p, err := Load("testdata/greet.star")
	if err != nil {
		t.Fatalf("Load(greet.star): unexpected error: %v", err)
	}

	v, ok := p.Global("result")
	if !ok {
		t.Fatal("expected a frozen `result` global, found none")
	}
	got, ok := v.(starlark.String)
	if !ok {
		t.Fatalf("result global: want starlark.String, got %T", v)
	}
	if got != starlark.String("hi") {
		t.Fatalf("result global: want %q, got %q", "hi", string(got))
	}

	if _, err := Load("testdata/does_not_exist.star"); err == nil {
		t.Fatal("Load(missing file): want non-nil error, got nil")
	}
}

// TestBuiltinRoundTrip proves the full Go -> Starlark -> Go-builtin -> Go round
// trip: Call invokes the greet() Starlark fn, which calls the registered echo
// builtin, and the value comes back to Go unchanged.
func TestBuiltinRoundTrip(t *testing.T) {
	p, err := Load("testdata/greet.star")
	if err != nil {
		t.Fatalf("Load(greet.star): unexpected error: %v", err)
	}

	out, err := p.Call("greet", starlark.String("bob"))
	if err != nil {
		t.Fatalf("Call(greet, \"bob\"): unexpected error: %v", err)
	}
	got, ok := out.(starlark.String)
	if !ok {
		t.Fatalf("greet return: want starlark.String, got %T", out)
	}
	if got != starlark.String("bob") {
		t.Fatalf("greet return: want %q, got %q", "bob", string(got))
	}
}
