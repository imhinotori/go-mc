package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestManifestValid: a well-formed plugin.toml decodes all fields incl. the
// optional capabilities list.
func TestManifestValid(t *testing.T) {
	m, err := readManifest("testdata/plugins/greeter/plugin.toml")
	if err != nil {
		t.Fatalf("readManifest(greeter): unexpected error: %v", err)
	}
	if m.Name != "greeter" || m.Version != "0.1.0" || m.Entrypoint != "main.star" || m.Runtime != "starlark" {
		t.Fatalf("readManifest(greeter): unexpected fields: %+v", m)
	}
	if len(m.Capabilities) != 1 || m.Capabilities[0] != "events" {
		t.Fatalf("readManifest(greeter): capabilities = %v, want [events]", m.Capabilities)
	}
}

// TestManifestMissingRequired: omitting a required field errors.
func TestManifestMissingRequired(t *testing.T) {
	cases := map[string]string{
		"missing name":       "version=\"1\"\nentrypoint=\"m.star\"\nruntime=\"starlark\"\n",
		"missing version":    "name=\"p\"\nentrypoint=\"m.star\"\nruntime=\"starlark\"\n",
		"missing entrypoint": "name=\"p\"\nversion=\"1\"\nruntime=\"starlark\"\n",
		"missing runtime":    "name=\"p\"\nversion=\"1\"\nentrypoint=\"m.star\"\n",
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			path := writeTempManifest(t, body)
			if _, err := readManifest(path); err == nil {
				t.Fatalf("%s: want error, got nil", label)
			}
		})
	}
}

// TestManifestPathTraversal: an entrypoint with ".." or an absolute path is
// rejected (T-22-03).
func TestManifestPathTraversal(t *testing.T) {
	bad := []string{
		"../evil.star",
		"../../etc/passwd",
		"sub/../../escape.star",
	}
	for _, ep := range bad {
		t.Run(ep, func(t *testing.T) {
			body := "name=\"p\"\nversion=\"1\"\nentrypoint=\"" + ep + "\"\nruntime=\"starlark\"\n"
			path := writeTempManifest(t, body)
			_, err := readManifest(path)
			if err == nil {
				t.Fatalf("entrypoint %q: want path-traversal error, got nil", ep)
			}
			if !strings.Contains(err.Error(), "invalid entrypoint") {
				t.Fatalf("entrypoint %q: error = %v, want invalid entrypoint", ep, err)
			}
		})
	}
}

func writeTempManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write temp manifest: %v", err)
	}
	return path
}
