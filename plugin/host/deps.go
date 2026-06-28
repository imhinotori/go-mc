package host

// Blank imports keep go.mod requires that land with this phase but are first
// imported for real in Plan 02 (the fsnotify hot-reload watcher). BurntSushi's
// toml is imported for real by manifest.go in this plan; fsnotify is reserved
// for the Plan-02 watcher. `go mod tidy` prunes a require with no importing
// package, so this anchors fsnotify until the watcher imports it.
import (
	_ "github.com/BurntSushi/toml"
	_ "github.com/fsnotify/fsnotify"
)
