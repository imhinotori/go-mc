// Package python is the OPT-IN CPython runtime lane, gated behind the `python`
// build tag. It is the SECOND plugin runtime (alongside Phase-21 Starlark):
// idiomatic CPython embedded via qur/gopy (cgo + libpython 3.14), dispatched
// OFF-TICK only and rejoining the tick via the existing async seam.
//
// THE non-negotiable isolation constraint (PLUGIN-06, CLAUDE.md): all gopy code
// — which transitively pulls `import "C"` + `#cgo pkg-config: python-3.14-embed
// libffi` — lives ONLY in files tagged `//go:build python`. The DEFAULT build
// (no `-tags python`) compiles ONLY the cgo-free stub, so `CGO_ENABLED=0 go
// build ./...` stays pure-Go static and the default import graph carries ZERO
// gopython / `import "C"`. The exported surface (Available, Runtime, Load,
// Runtime.Close, Runtime.CallHook) is BYTE-IDENTICAL across the two files so the
// package compiles in both builds (the stub returns ErrNotBuilt; the impl runs
// real CPython).
//
// Build matrix:
//   - go build ./...                (DEFAULT, CGO_ENABLED=0) → compiles runtime_stub.go
//   - go build -tags python ./...   (OPT-IN,  CGO_ENABLED=1) → compiles runtime_python.go
//     (needs libpython 3.14)
package python
