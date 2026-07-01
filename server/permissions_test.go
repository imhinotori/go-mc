package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

// TestPermWildcardAndNegation pins the node-matching semantics: exact, global "*", "prefix.*", and
// a negated "-node" that overrides a grant.
func TestPermWildcardAndNegation(t *testing.T) {
	cases := []struct {
		nodes []string
		want  string
		grant bool
	}{
		{[]string{"minecraft.command.say"}, "minecraft.command.say", true},
		{[]string{"minecraft.command.say"}, "minecraft.command.kill", false},
		{[]string{"minecraft.command.*"}, "minecraft.command.kill", true},
		{[]string{"*"}, "anything.at.all", true},
		{[]string{"minecraft.command.*", "-minecraft.command.kill"}, "minecraft.command.kill", false}, // negation wins
		{[]string{"minecraft.command.*", "-minecraft.command.kill"}, "minecraft.command.say", true},
		{nil, "minecraft.command.say", false}, // no nodes -> deny
	}
	for i, c := range cases {
		if got := matchPermission(c.nodes, c.want); got != c.grant {
			t.Errorf("case %d: matchPermission(%v, %q) = %v, want %v", i, c.nodes, c.want, got, c.grant)
		}
	}
}

// TestPermGroupsAndInheritance: a user in a group inherits its nodes + parent-group nodes.
func TestPermGroupsAndInheritance(t *testing.T) {
	s := newDefaultPermStore("")
	// admin inherits from a new "mod" parent
	s.Groups["mod"] = &permGroup{Name: "mod", Permissions: []string{"minecraft.command.kick"}}
	s.Groups["staff"] = &permGroup{Name: "staff", Permissions: []string{"minecraft.command.ban"}, Parents: []string{"mod"}}

	u := uuid.New()
	s.Users[u.String()] = &permUser{UUID: u.String(), Groups: []string{"staff"}}

	if !s.HasPermission(u, "minecraft.command.ban") {
		t.Error("staff member should have ban (own node)")
	}
	if !s.HasPermission(u, "minecraft.command.kick") {
		t.Error("staff member should inherit kick from mod parent")
	}
	if s.HasPermission(u, "minecraft.command.gamemode") {
		t.Error("staff member should NOT have gamemode")
	}
	// default-group baseline still applies to any user
	if !s.HasPermission(u, "minecraft.command.say") {
		t.Error("every user should have the default-group say node")
	}
}

// TestPermDefaultGroupForUnknownUser: an unknown UUID gets exactly the default group's nodes.
func TestPermDefaultGroupForUnknownUser(t *testing.T) {
	s := newDefaultPermStore("")
	u := uuid.New() // never added
	if !s.HasPermission(u, "minecraft.command.say") {
		t.Error("unknown user should inherit the default group (say)")
	}
	if s.HasPermission(u, "minecraft.command.kill") {
		t.Error("unknown user should NOT have a non-default node")
	}
}

// TestPermSetOp: /op adds "*" (grants everything), /deop removes it.
func TestPermSetOp(t *testing.T) {
	s := newDefaultPermStore("")
	u := uuid.New()
	if s.isOp(u) {
		t.Fatal("fresh user should not be op")
	}
	if !s.setOp(u, "alice", true) {
		t.Fatal("setOp(true) should report a change")
	}
	if !s.isOp(u) {
		t.Error("user should be op after setOp(true)")
	}
	if !s.HasPermission(u, "minecraft.command.anything") {
		t.Error("op should pass any node via the * grant")
	}
	if s.setOp(u, "alice", true) {
		t.Error("setOp(true) twice should report no change")
	}
	if !s.setOp(u, "alice", false) {
		t.Error("setOp(false) should report a change")
	}
	if s.isOp(u) {
		t.Error("user should not be op after deop")
	}
}

// TestPermStorePersistRoundTrip: save then load reproduces users/groups/ops.
func TestPermStorePersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "permissions.json")

	s, err := LoadPermStore(path) // absent -> default, dirty
	if err != nil {
		t.Fatalf("LoadPermStore: %v", err)
	}
	u := uuid.New()
	s.setOp(u, "bob", true)
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("permissions.json not written: %v", err)
	}

	s2, err := LoadPermStore(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !s2.isOp(u) {
		t.Error("op status did not survive save/load round-trip")
	}
	if s2.DefaultGroup != "default" {
		t.Errorf("default group = %q, want default", s2.DefaultGroup)
	}
}

// TestPlayerHasPermissionFallback: with no store wired, the legacy all-operator fallback holds; with
// a store, the gate authorizes by node.
func TestPlayerHasPermissionFallback(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := &tickPlayer{uuid: uuid.New(), name: "carol"}

	// no store -> all-operator fallback
	if !loop.playerHasPermission(p, "minecraft.command.kill") {
		t.Error("nil store should fall back to all-operator (grant)")
	}

	// with a store -> gated
	loop.SetPermStore(newDefaultPermStore(""))
	if loop.playerHasPermission(p, "minecraft.command.kill") {
		t.Error("default-group player should NOT have kill")
	}
	if !loop.playerHasPermission(p, "minecraft.command.say") {
		t.Error("default-group player should have say")
	}
	// nil player fails closed
	if loop.playerHasPermission(nil, "minecraft.command.say") {
		t.Error("nil player should fail closed with a store present")
	}
}
