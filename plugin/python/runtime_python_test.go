//go:build python

// runtime_python_test.go is the TAGGED-BUILD smoke test for the CPython runtime.
// It runs ONLY under `CGO_ENABLED=1 go test -tags python ./plugin/python/` on an
// image with libpython 3.14 present (python-3.14-embed + libffi) — i.e. the
// python3.14 Docker image / CI, NOT a default Windows/CGO=0 box. The default
// build instead runs runtime_stub_test.go (//go:build !python).
//
// Gate (per 26-RESEARCH.md Pitfall 6): proves the tagged side actually links
// libpython and can init the interpreter + run a .py — the real link-test the
// default build cannot perform.

package python

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPythonAvailableTrue(t *testing.T) {
	if !Available() {
		t.Fatal("Available() = false on the -tags python build; the impl must report the runtime IS built in")
	}
}

func TestPythonLoadRunsEntrypoint(t *testing.T) {
	// Write a tiny no-op .py to a temp dir and Load it. Success proves CPython
	// initialized (InitAndLock) and RunFile executed the module body.
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.py")
	if err := os.WriteFile(entry, []byte("x = 1 + 1\n"), 0o600); err != nil {
		t.Fatalf("write temp .py: %v", err)
	}
	rt, err := Load(entry)
	if err != nil {
		t.Fatalf("Load(%s) err = %v; want nil (CPython should init + run the module)", entry, err)
	}
	if rt == nil {
		t.Fatal("Load returned a nil Runtime with no error")
	}
	rt.Close()
}
