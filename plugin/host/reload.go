package host

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.starlark.net/starlark"
)

// reload.go is the FULL hot-reload watcher (PLUGIN-02 / Plan 22-02, the LOCKED operator decision):
// a pure-Go fsnotify watcher over the plugins/ root that, on a .star/.toml change, REBUILDS a fresh
// *Manager OFF the tick goroutine and hands the new pointer to the tick goroutine via a swap channel
// the caller owns (TickLoop.PluginSwapChan). The tick owner is the SOLE writer of the live *Manager
// pointer and swaps it on-thread between ticks (TICK-05 / T-22-05), so dispatch never reads a
// half-updated map and a reload concurrent with Emit is a pointer swap, never a mid-dispatch map
// mutation.
//
// The watcher NEVER touches the live Manager or any live hook map. It only:
//   1. observes a relevant filesystem change,
//   2. constructs a BRAND-NEW Manager (New + LoadDirWith — a fresh, fully-populated map),
//   3. sends that new pointer on the swap channel.
// All shared-state mutation happens on the tick owner, which receives the finished pointer.
//
// fsnotify is non-recursive, so the watcher Adds the root AND every plugin sub-dir. New plugin dirs
// created at runtime are Added when their create event is seen. Rapid successive events (an editor
// save fires several Write/Create/Chmod events) are COALESCED behind a short debounce timer so one
// save triggers exactly one rebuild.

// reloadDebounce coalesces the burst of filesystem events a single save emits (an editor typically
// fires multiple Write/Create/Chmod within milliseconds) into ONE rebuild. Short enough to feel
// instant, long enough to swallow the burst.
const reloadDebounce = 150 * time.Millisecond

// Watcher is the off-tick hot-reload file watcher. It owns an fsnotify watcher over the plugins/
// root + sub-dirs and, on a debounced .star/.toml change, rebuilds a fresh Manager and publishes it
// on the swap channel for the tick owner to install. Close stops the watcher goroutine and releases
// the fsnotify handle.
type Watcher struct {
	root    string
	swap    chan<- *Manager
	extra   starlark.StringDict
	fsw     *fsnotify.Watcher
	done    chan struct{}
	closeMu sync.Mutex
	closed  bool

	// rebuildCount is incremented once per published rebuild. Exposed via RebuildCount for tests to
	// observe that a change drove exactly one (debounced) rebuild. Written only on the watcher
	// goroutine; read via the accessor under the same goroutine guarantees in tests (which Sync()).
	rebuildCount int
}

// NewWatcher creates a hot-reload watcher over root, publishing each freshly-rebuilt *Manager on
// swap. extra is the same predeclared-builtin set (may be nil) the initial LoadDirWith used, so a
// reloaded Manager carries the host's observability/host builtins unchanged (tests inject a count
// builtin here). It Adds the root and every existing plugin sub-dir, then starts the watch loop.
// The caller owns swap (TickLoop.PluginSwapChan) and drains it on the tick goroutine.
func NewWatcher(root string, swap chan<- *Manager, extra starlark.StringDict) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		root:  root,
		swap:  swap,
		extra: extra,
		fsw:   fsw,
		done:  make(chan struct{}),
	}
	// Watch the root and every existing plugin sub-dir (fsnotify is non-recursive).
	if err := fsw.Add(root); err != nil {
		fsw.Close()
		return nil, err
	}
	w.addSubdirs()
	go w.run()
	return w, nil
}

// addSubdirs Adds every immediate sub-directory of root to the fsnotify watch set (plugin dirs hold
// the .star/.toml files). A failure to add one dir is logged and skipped — the root watch still
// catches dir-level creates, and a best-effort sub-watch is the right resilience posture for a
// developer convenience feature.
func (w *Watcher) addSubdirs() {
	entries, err := os.ReadDir(w.root)
	if err != nil {
		return // root unreadable: the run loop will surface errors; nothing to sub-watch
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if err := w.fsw.Add(filepath.Join(w.root, e.Name())); err != nil {
			log.Printf("host: watch subdir %s: %v", e.Name(), err)
		}
	}
}

// run is the watcher goroutine: it coalesces relevant events behind a debounce timer and, when the
// timer fires, rebuilds + publishes a fresh Manager. It also keeps the sub-dir watch set current by
// Adding a newly-created plugin directory.
func (w *Watcher) run() {
	var timer *time.Timer
	var timerC <-chan time.Time
	for {
		select {
		case event, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			// A newly-created directory under root is a new plugin dir — watch it too.
			if event.Has(fsnotify.Create) && isDir(event.Name) {
				if err := w.fsw.Add(event.Name); err != nil {
					log.Printf("host: watch new dir %s: %v", event.Name, err)
				}
			}
			if !relevantEvent(event) {
				continue
			}
			// Debounce: (re)arm the coalesce timer; the rebuild fires once the burst settles.
			if timer == nil {
				timer = time.NewTimer(reloadDebounce)
				timerC = timer.C
			} else {
				if !timer.Stop() {
					// Drain a fired-but-unreceived timer so Reset is clean.
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(reloadDebounce)
			}
		case <-timerC:
			timer = nil
			timerC = nil
			w.rebuild()
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			log.Printf("host: watcher error: %v", err)
		case <-w.done:
			return
		}
	}
}

// rebuild constructs a BRAND-NEW Manager off-tick (New + LoadDirWith over the whole root) and
// publishes it on the swap channel for the tick owner to install. A load error (a plugin saved
// mid-edit with a syntax error) is logged and the swap is SKIPPED — the live Manager keeps running
// the last-good plugins rather than swapping in a broken set. The publish BLOCKS on the swap channel
// (buffered 1 on the TickLoop side) so the rebuild is guaranteed delivered to the owner; the owner
// drains pluginSwap every tick (drainRegistrations), so a send never parks for more than one tick.
func (w *Watcher) rebuild() {
	m := New()
	if err := m.LoadDirWith(w.root, w.extra); err != nil {
		log.Printf("host: hot-reload rebuild failed (keeping current plugins): %v", err)
		return
	}
	w.rebuildCount++
	// Publish the fresh Manager. Select on done so a Close() concurrent with a stalled send (no
	// owner draining — e.g. shutdown) never leaks the watcher goroutine.
	select {
	case w.swap <- m:
	case <-w.done:
	}
}

// RebuildCount returns how many successful rebuilds the watcher has published. Test-only
// observability (a test triggers a change, Syncs, and asserts a rebuild happened).
func (w *Watcher) RebuildCount() int { return w.rebuildCount }

// Close stops the watcher goroutine and releases the fsnotify handle. Idempotent and safe to call
// multiple times.
func (w *Watcher) Close() error {
	w.closeMu.Lock()
	defer w.closeMu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	close(w.done)
	return w.fsw.Close()
}

// relevantEvent reports whether an fsnotify event is a plugin source change worth a rebuild: a
// Write/Create/Remove/Rename on a .star or .toml file. Chmod-only events (and changes to unrelated
// files) are ignored so editor metadata churn does not trigger reloads.
func relevantEvent(event fsnotify.Event) bool {
	if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) &&
		!event.Has(fsnotify.Remove) && !event.Has(fsnotify.Rename) {
		return false
	}
	name := strings.ToLower(event.Name)
	return strings.HasSuffix(name, ".star") || strings.HasSuffix(name, ".toml")
}

// isDir reports whether path is an existing directory (best-effort: a stat error -> false).
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
