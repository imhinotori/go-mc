package server

// external_mob_dir_test.go -- Bug #11 (plugin-loader): locks the on-disk mob-plugin loader
// (LoadExternalMobs / loadOneExternalMob, external_mob_dir.go) that makes plugins/mobs/ live:
//   1. a genuinely NEW operator mob (a name not in the registry) LOADS + becomes declared;
//   2. a disk copy whose name is ALREADY registered (an embedded vanilla mob) is DEDUP-skipped
//      so the embedded declaration stays authoritative (the pig oracle is untouched);
//   3. a missing dir is a clean no-op.

import (
	"os"
	"path/filepath"
	"testing"
)

// writeExternalMobPlugin materializes a leaf mob plugin (plugin.toml + main.star) under root/<name>/.
func writeExternalMobPlugin(t *testing.T, root, dir, manifestName, declName string) {
	t.Helper()
	leaf := filepath.Join(root, dir)
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", leaf, err)
	}
	toml := "name = \"" + manifestName + "\"\n" +
		"version = \"0.1.0\"\n" +
		"entrypoint = \"main.star\"\n" +
		"runtime = \"starlark\"\n" +
		"capabilities = [\"entities.read\", \"entities.write\", \"nav\"]\n"
	if err := os.WriteFile(filepath.Join(leaf, "plugin.toml"), []byte(toml), 0o644); err != nil {
		t.Fatalf("write plugin.toml: %v", err)
	}
	star := "def on_tick(entity, world, nav):\n" +
		"    nav.path_to(entity.x + 1.0, entity.y, entity.z + 1.0)\n" +
		"\n" +
		"declare_mob(\n" +
		"    name = \"" + declName + "\",\n" +
		"    base_type = \"pig\",\n" +
		"    goals = [goal(priority = 6, flags = [\"MOVE\"], tick = on_tick)],\n" +
		")\n"
	if err := os.WriteFile(filepath.Join(leaf, "main.star"), []byte(star), 0o644); err != nil {
		t.Fatalf("write main.star: %v", err)
	}
}

// TestLoadExternalMobsNewMob: a NEW operator mob on disk loads into the tick-owned registry.
func TestLoadExternalMobsNewMob(t *testing.T) {
	tl := &TickLoop{mobRegistry: newMobRegistry()}
	dir := t.TempDir()
	writeExternalMobPlugin(t, dir, "op_new_mob", "op_new_mob", "op_new_mob")

	n, err := tl.LoadExternalMobs(dir)
	if err != nil {
		t.Fatalf("LoadExternalMobs: %v", err)
	}
	if n != 1 {
		t.Fatalf("loaded count = %d, want 1", n)
	}
	if _, ok := tl.mobRegistry.byName["op_new_mob"]; !ok {
		t.Fatal("op_new_mob was not declared into the registry")
	}
}

// TestLoadExternalMobsDedupsEmbedded: a disk copy whose name is already registered (the embedded
// vanilla mob case) is skipped, and the ALREADY-present declaration is left byte-identically in place.
func TestLoadExternalMobsDedupsEmbedded(t *testing.T) {
	tl := &TickLoop{mobRegistry: newMobRegistry()}
	// Pre-seed "vanilla_pig" as if the embedded boot-load already declared it. A sentinel *mobDecl
	// (same pointer identity) lets us assert the disk copy did NOT overwrite it.
	pre := &mobDecl{name: "vanilla_pig", baseType: baseTypeByName["pig"]}
	tl.mobRegistry.byName["vanilla_pig"] = pre

	dir := t.TempDir()
	writeExternalMobPlugin(t, dir, "vanilla_pig", "vanilla_pig", "vanilla_pig")

	n, err := tl.LoadExternalMobs(dir)
	if err != nil {
		t.Fatalf("LoadExternalMobs: %v", err)
	}
	if n != 0 {
		t.Fatalf("loaded count = %d, want 0 (the embedded copy is authoritative)", n)
	}
	if got := tl.mobRegistry.byName["vanilla_pig"]; got != pre {
		t.Fatal("the embedded vanilla_pig declaration was overwritten by the disk copy (dedup failed)")
	}
}

// TestLoadExternalMobsMissingDir: a missing plugins/mobs/ dir is a clean no-op.
func TestLoadExternalMobsMissingDir(t *testing.T) {
	tl := &TickLoop{mobRegistry: newMobRegistry()}
	n, err := tl.LoadExternalMobs(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("LoadExternalMobs(missing): %v", err)
	}
	if n != 0 {
		t.Fatalf("loaded count = %d, want 0", n)
	}
}
