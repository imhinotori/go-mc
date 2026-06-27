package server

import (
	"testing"
)

// TestRemovePlayerDoubleLeave proves the deferred double-leave hardening (STATE.md
// Deferred Items / research Robustness note): a player the keep-alive already removed
// (e.g. a 30s-timeout kick) that then leaves AGAIN via ClientLeft → a second
// removePlayer for a now-missing listIndex key must NOT panic / nil-deref.
//
// Before the fix, removePlayer read `elem := k.listIndex[c]` (a missing key yields a
// nil *list.Element) and then called elem.Prev() / Remove(elem), panicking. The fix
// mirrors the tickPlayer ok-guard: a missing key returns early.
func TestRemovePlayerDoubleLeave(t *testing.T) {
	k := NewKeepAlive()
	c := newFakeKeepAliveClient()

	// Register the player on the ping list, then remove it once (the normal leave).
	k.pushPlayer(c)
	k.removePlayer(c) // first leave: present → removed cleanly

	// Second leave for the same, now-missing client. This is the double-leave the
	// deferred item warns about (timeout-kick THEN ClientLeft). It must be a no-op,
	// never a panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("removePlayer panicked on a double-leave (missing listIndex key): %v", r)
		}
	}()
	k.removePlayer(c) // second leave: missing → early return, no panic
}

// TestReasonHumanString maps each disconnect-reason token to the one-line human string
// the TUI-02 leave log surfaces. The five TUI-02 categories (kick / timeout / protocol
// error / clean quit / login failure) must each be reachable; unknown tokens fall back
// to the token itself so a new reason is never silently lost.
func TestReasonHumanString(t *testing.T) {
	cases := []struct {
		token string
		want  string
	}{
		{"timeout", "timeout"},
		{"protocol_error", "protocol error"},
		{"protocol_mismatch", "protocol error"},
		{"login_failure", "login failure"},
		{"config_failure", "login failure"},
		{"kicked", "kick"},
		{"backpressure", "kick"},
		{"write_error", "kick"},
		{"quit", "clean quit"},
		{"", "clean quit"},
		{"something_new", "something_new"}, // unknown → passthrough, never lost
	}
	for _, tc := range cases {
		if got := reasonHuman(tc.token); got != tc.want {
			t.Errorf("reasonHuman(%q) = %q, want %q", tc.token, got, tc.want)
		}
	}
}
