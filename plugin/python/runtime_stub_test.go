//go:build !python

// runtime_stub_test.go is the DEFAULT-build test for the python package: it runs
// under `CGO_ENABLED=0 go test ./plugin/python/` with NO libpython present. It
// proves the cgo-free stub refuses gracefully so a runtime="python" plugin is
// skipped (not loaded) on a default binary. The tagged-build smoke test lives in
// runtime_python_test.go (//go:build python) and runs only on the python3.14
// Docker image.

package python

import (
	"errors"
	"testing"
)

func TestStubAvailableFalse(t *testing.T) {
	if Available() {
		t.Fatal("Available() = true on the default build; the stub must report the python runtime is NOT built in")
	}
}

func TestStubLoadReturnsErrNotBuilt(t *testing.T) {
	rt, err := Load("anything.py")
	if rt != nil {
		t.Fatalf("Load() returned a non-nil Runtime on the stub: %v", rt)
	}
	if !errors.Is(err, ErrNotBuilt) {
		t.Fatalf("Load() err = %v; want ErrNotBuilt", err)
	}
}

func TestStubCallHookReturnsErrNotBuilt(t *testing.T) {
	if err := (&Runtime{}).CallHook("on_block_break", 1, 2, 3); !errors.Is(err, ErrNotBuilt) {
		t.Fatalf("CallHook() err = %v; want ErrNotBuilt", err)
	}
}

func TestStubCloseNoPanic(t *testing.T) {
	// Close must be safe to call on a zero Runtime (the host calls it on unload).
	(&Runtime{}).Close()
}
