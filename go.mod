module github.com/imhinotori/sulfur

go 1.25.0

require (
	github.com/google/uuid v1.3.0
	github.com/petermattis/goid v0.0.0-20260625140558-4207c655779d
	github.com/sourcegraph/conc v0.3.0
	golang.org/x/exp v0.0.0-20231006140011-7918f672742d
)

require (
	charm.land/bubbles/v2 v2.1.0
	charm.land/bubbletea/v2 v2.0.7
	charm.land/lipgloss/v2 v2.0.4
	github.com/BurntSushi/toml v1.6.0
	github.com/fsnotify/fsnotify v1.10.1
	github.com/klauspost/compress v1.18.6
	github.com/panjf2000/ants/v2 v2.12.1
	github.com/puzpuzpuz/xsync/v4 v4.5.0
	go.starlark.net v0.0.0-20260613233743-8ba36ccb83fb
	golang.org/x/sync v0.21.0
	golang.org/x/term v0.44.0
	// gopy (CPython 3.14 embed) — pinned to the python3.14 BRANCH commit
	// b0bdc04a384b, NOT the moving v14.0.0-alpha.0 tag. Reachable ONLY from
	// plugin/python/runtime_python.go behind //go:build python, so it stays OUT
	// of the default CGO=0 import graph (PLUGIN-06). Verified via `go build
	// -tags python` on the python3.14 Docker image (needs python-3.14-embed).
	gopython.xyz/py/v14 v14.0.0-alpha.0.0.20260510154237-b0bdc04a384b
)

require (
	github.com/atotto/clipboard v0.1.4 // indirect
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/ultraviolet v0.0.0-20260525132238-948f4557a654 // indirect
	github.com/charmbracelet/x/ansi v0.11.7 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.0 // indirect
	github.com/mattn/go-runewidth v0.0.23 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/xo/terminfo v0.0.0-20220910002029-abceb7e1c41e // indirect
	golang.org/x/sys v0.46.0 // indirect
)
