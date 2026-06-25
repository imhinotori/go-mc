package world

import (
	"strings"
	"testing"

	"github.com/imhinotori/sulfur/world/levelgen/data"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
)

// TestAllEmbeddedOreConfigsDecode guards that EVERY embedded ore configured_feature
// decodes without erroring — so oreBody's decode-or-panic never fires in production.
func TestAllEmbeddedOreConfigsDecode(t *testing.T) {
	ids, err := data.ConfiguredFeatureIDs()
	if err != nil {
		t.Fatalf("ConfiguredFeatureIDs: %v", err)
	}
	reg := feature.NewEmbeddedRegistry()
	n := 0
	for _, id := range ids {
		cf, err := reg.ResolveConfigured("minecraft:" + strings.TrimPrefix(id, "minecraft:"))
		if err != nil {
			t.Fatalf("resolve %s: %v", id, err)
		}
		if cf.Type != "ore" {
			continue
		}
		if _, err := decodeOreConfig(cf.Config.Raw); err != nil {
			t.Errorf("ore config %s failed to decode: %v", id, err)
		}
		n++
	}
	if n == 0 {
		t.Fatalf("no ore configs found in the embed")
	}
	t.Logf("decoded %d ore configs", n)
}
