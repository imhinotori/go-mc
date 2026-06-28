package host

// Blank import anchors a go.mod require that lands with this phase but is first
// imported for real in Plan 02 (the fsnotify hot-reload watcher). `go mod tidy`
// prunes a require with no importing package, so this keeps fsnotify until the
// watcher imports it. (toml is imported for real by manifest.go in this plan.)
import (
	_ "github.com/fsnotify/fsnotify"
)
