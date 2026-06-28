# Phase 26 — the opt-in Python runtime CI matrix (NOTES)

This is a NOTES doc, not a runnable workflow. It documents the THREE build/test
gates the opt-in Python runtime needs (per `26-RESEARCH.md` Pitfall 6), so when
real CI lands these become the job matrix. The first two gates run anywhere
(including the executor's Windows box); the third needs libpython 3.14 and runs
ONLY on a python3.14 image — exactly like the existing `-race` Docker split.

## The non-negotiable invariant

`gopython.xyz/py/v14` (gopy = CPython via cgo) is reachable ONLY from
`plugin/python/runtime_python.go` behind `//go:build python`. The instant it
reaches an untagged file, `CGO_ENABLED=0 go build ./...` breaks and the
static-binary value prop is gone. Gate (a) below proves it stays out of the
default graph on every change.

## Gate (a) — DEFAULT static build + the no-gopy grep (LOCAL, always)

Runs on any box, no Python needed. This is THE #1 isolation gate.

```bash
CGO_ENABLED=0 go build ./...                                  # must exit 0 (pure-Go static)
test -z "$(go list -deps ./... | grep -i gopython)" && echo OK  # must print OK (zero gopy in graph)
CGO_ENABLED=0 go test ./plugin/python/ -run TestStub          # the cgo-free stub refuses gracefully
CGO_ENABLED=0 go test ./plugin/host/  -run TestRuntimeRouting  # python manifest skipped/routed, no gopy
```

## Gate (b) — DEFAULT race (CGO=1, NO -tags python) — the existing Phase-21/22 gate

```bash
CGO_ENABLED=1 go test -race ./...        # covers the host + the stub; no libpython needed
```

## Gate (c) — the -tags python build + race (python3.14 IMAGE / CI only)

Needs libpython 3.14 with the embed pkg-config + libffi. NOT runnable on the
default Windows box (no libpython3.14) — the executor writes the tagged code +
tests and this gate runs in the python3.14 Docker image, exactly like the
existing `-race` split.

```bash
# image prereq (debian/ubuntu): apt-get install python3.14-dev libffi-dev
#   (or build CPython --enable-shared)
pkg-config --exists python-3.14-embed                 # precondition; must exit 0
CGO_ENABLED=1 go build -tags python ./...             # links python-3.14-embed + libffi
CGO_ENABLED=1 go test  -race -tags python ./plugin/python/   # the off-tick lane + the rejoin
```

## The pinned dependency

`gopython.xyz/py/v14` is pinned to the **python3.14 BRANCH commit**
`b0bdc04a384b443df2279101ac335b5808727793` (pseudo-version
`v14.0.0-alpha.0.0.20260510154237-b0bdc04a384b`), NOT the moving
`v14.0.0-alpha.0` tag. Because it is behind `//go:build python`, only operators
who opt into Python are exposed to the alpha — the default binary never links it.
The pin + its hashes are recorded in `go.mod` + `go.sum`.
