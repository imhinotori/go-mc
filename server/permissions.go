package server

// permissions.go — a LuckPerms-style permission system (users + groups + permission nodes +
// wildcards + inheritance + a default group), uuid-keyed and JSON-persisted. It plugs into the
// SINGLE command-authorization seam (t.playerHasPermission, commands.go), replacing the v1
// all-players-operator policy. Read-only on the tick goroutine (HasPermission); mutations
// (op/deop, group edits) run on the tick goroutine too and mark the store dirty for the off-tick
// save. Kept a CORE service (not a plugin) so the per-command check has no plugin-dispatch latency
// and can never soft-fail — a command gate must be deterministic.
//
// Design (LuckPerms essentials mapped onto this codebase):
//   - User: keyed by player UUID (tickPlayer.uuid); carries direct permission nodes + group names.
//   - Group: named; carries permission nodes + parent groups (inheritance) — a track/tree.
//   - Node: a dot-hierarchy string ("minecraft.command.gamemode"); "*" and "prefix.*" wildcards;
//     a leading "-" NEGATES (an explicit deny that overrides a grant — LuckPerms negation).
//   - defaultGroup: every player is implicitly a member (so a fresh player gets baseline perms).
//   - Operator shortcut: the "*" node (or being in a group with "*") grants everything — the
//     op/deop commands add/remove "*" on the user.

import (
	"encoding/json"
	"os"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// permGroup is one named permission group: its own nodes + the parents it inherits from.
type permGroup struct {
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
	Parents     []string `json:"parents,omitempty"`
}

// permUser is one player's permission record, keyed by UUID (string form on disk). Username is
// display-only (identity is the UUID). Groups + direct Permissions both contribute; direct nodes
// take precedence for negation resolution (checked last so a user "-node" can override a group grant).
type permUser struct {
	UUID        string   `json:"uuid"`
	Username    string   `json:"username,omitempty"`
	Groups      []string `json:"groups,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
}

// permStore is the whole permission database. Concurrency: HasPermission is a read; the tick
// goroutine is the only writer (op/deop). A RWMutex guards it so the off-tick save can snapshot
// safely and a future async reload can't tear a read. dirty tracks unsaved mutations.
type permStore struct {
	mu           sync.RWMutex
	Users        map[string]*permUser  `json:"users"`
	Groups       map[string]*permGroup `json:"groups"`
	DefaultGroup string                `json:"default_group"`

	path  string // where to persist (world/permissions.json); "" = in-memory only
	dirty bool
}

// newDefaultPermStore builds the baseline store used when no permissions.json exists yet: a
// "default" group (everyone) with the harmless social commands, an "admin" group with "*" (all),
// and default_group = "default". Ops are added to "admin" (or given the "*" node) via /op.
func newDefaultPermStore(path string) *permStore {
	return &permStore{
		Users: map[string]*permUser{},
		Groups: map[string]*permGroup{
			"default": {
				Name: "default",
				Permissions: []string{
					"minecraft.command.help",
					"minecraft.command.list",
					"minecraft.command.me",
					"minecraft.command.say",
					"minecraft.command.seed",
					"minecraft.command.msg",
				},
			},
			"admin": {
				Name:        "admin",
				Permissions: []string{"*"},
			},
		},
		DefaultGroup: "default",
		path:         path,
	}
}

// loadPermStore reads world/permissions.json, or returns a fresh default store (and marks it dirty
// so the first save writes the baseline file) when the file is absent. A malformed file is a LOUD
// failure (returns the error) rather than silently resetting a server's op list. path "" = in-memory.
func LoadPermStore(path string) (*permStore, error) {
	if path == "" {
		return newDefaultPermStore(""), nil
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		s := newDefaultPermStore(path)
		s.dirty = true // first save writes the baseline
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var s permStore
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	if s.Users == nil {
		s.Users = map[string]*permUser{}
	}
	if s.Groups == nil {
		s.Groups = map[string]*permGroup{}
	}
	if s.DefaultGroup == "" {
		s.DefaultGroup = "default"
	}
	s.path = path
	return &s, nil
}

// save persists the store to its path (JSON, indented). Called off-tick or on a save edge; a
// no-op when not dirty or in-memory. Snapshots under the read lock so it never tears a concurrent
// read/mutation. Best-effort: a write error is returned to the caller to log, not panicked.
func (s *permStore) Save() error {
	s.mu.Lock()
	if !s.dirty || s.path == "" {
		s.mu.Unlock()
		return nil
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.dirty = false
	path := s.path
	s.mu.Unlock()
	// Write outside the lock (IO): a tmp+rename would be safer, but a direct write matches the
	// playerdata path's discipline (the file is small + rewritten wholesale).
	return os.WriteFile(path, raw, 0o644)
}

// HasPermission resolves whether the user (by UUID) has the given node. Resolution order mirrors
// LuckPerms: collect the user's group nodes (with parent inheritance) + the user's direct nodes,
// then match. A "*" (or "prefix.*") grant matches; a negated "-node" (or "-prefix.*") DENIES and
// overrides any grant. Direct user nodes are applied AFTER group nodes so a user-level deny wins.
// Unknown user → the default group only. Read-locked; safe from the tick goroutine.
func (s *permStore) HasPermission(u uuid.UUID, node string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := u.String()
	user := s.Users[key]

	// Gather the effective node list in precedence order: default group → user's groups (with
	// inheritance) → user's direct nodes. Later entries override earlier ones for the same target.
	var nodes []string
	seen := map[string]bool{}
	nodes = append(nodes, s.groupNodes(s.DefaultGroup, seen)...)
	if user != nil {
		for _, g := range user.Groups {
			nodes = append(nodes, s.groupNodes(g, seen)...)
		}
		nodes = append(nodes, user.Permissions...)
	}

	return matchPermission(nodes, node)
}

// groupNodes returns a group's permission nodes plus every parent's (depth-first, cycle-guarded via
// seen). Called under the read lock.
func (s *permStore) groupNodes(name string, seen map[string]bool) []string {
	if name == "" || seen[name] {
		return nil
	}
	seen[name] = true
	g := s.Groups[name]
	if g == nil {
		return nil
	}
	out := append([]string(nil), g.Permissions...)
	for _, p := range g.Parents {
		out = append(out, s.groupNodes(p, seen)...)
	}
	return out
}

// matchPermission evaluates an ordered node list against a requested node. A grant is any matching
// positive node ("node", "*", "prefix.*"); a deny is a matching negated node ("-node", "-*",
// "-prefix.*"). The result is grant AND NOT deny — a deny anywhere in the effective set overrides
// a grant (LuckPerms's negation semantics; the most specific/last write already ordered by caller).
func matchPermission(nodes []string, want string) bool {
	granted := false
	for _, n := range nodes {
		neg := false
		if strings.HasPrefix(n, "-") {
			neg = true
			n = n[1:]
		}
		if !nodeMatches(n, want) {
			continue
		}
		if neg {
			return false // an explicit deny overrides everything
		}
		granted = true
	}
	return granted
}

// nodeMatches reports whether a permission node pattern matches the requested node. Exact match,
// the global "*", and a trailing "prefix.*" (matches any node under prefix). Case-sensitive
// (command nodes are lowercase by convention).
func nodeMatches(pattern, want string) bool {
	if pattern == "*" || pattern == want {
		return true
	}
	if strings.HasSuffix(pattern, ".*") {
		prefix := strings.TrimSuffix(pattern, "*") // keep the dot: "minecraft.command."
		return strings.HasPrefix(want, prefix)
	}
	return false
}

// setOp grants (op=true) or revokes (op=false) operator status for a user by adding/removing the
// "*" node on the user record. Tick-owned mutation; marks dirty for the off-tick save. Records the
// username for the on-disk display. Returns whether the state actually changed.
func (s *permStore) setOp(u uuid.UUID, username string, op bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := u.String()
	user := s.Users[key]
	if user == nil {
		if !op {
			return false // deop a non-existent user: nothing to do
		}
		user = &permUser{UUID: key, Username: username}
		s.Users[key] = user
	}
	if username != "" {
		user.Username = username
	}
	has := false
	for _, p := range user.Permissions {
		if p == "*" {
			has = true
			break
		}
	}
	if op && !has {
		user.Permissions = append(user.Permissions, "*")
		s.dirty = true
		return true
	}
	if !op && has {
		out := user.Permissions[:0]
		for _, p := range user.Permissions {
			if p != "*" {
				out = append(out, p)
			}
		}
		user.Permissions = out
		s.dirty = true
		return true
	}
	return false
}

// isOp reports whether the user has the "*" node directly or via a group — the operator shortcut.
func (s *permStore) isOp(u uuid.UUID) bool {
	return s.HasPermission(u, "*")
}
