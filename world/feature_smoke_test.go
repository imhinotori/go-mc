package world

import (
	"fmt"
	"sort"
	"testing"

	levelbiome "github.com/imhinotori/sulfur/level/biome"
	"github.com/imhinotori/sulfur/world/levelgen"
	"github.com/imhinotori/sulfur/world/levelgen/data"
	"github.com/imhinotori/sulfur/world/levelgen/feature"
	"github.com/imhinotori/sulfur/world/levelgen/placement"
)

// TestAllConfiguredFeatureBodiesDoNotPanic is the DECORATION-CRASH smoke gate. A feature
// body decodes its config LAZILY at first placement; a deferred/unported sub-type (provider,
// int-provider, placer, predicate) surfaces as a panic(decode.err) INSIDE the body -- exactly
// the noise_threshold_provider crash that killed the live server ("Loading terrain" panic ->
// 0 TPS). LoadAll only captures the raw config (deferred decode), so it does NOT catch this.
//
// This test resolves every embedded configured_feature, dispatches its registered body once
// under recover(), and FAILS listing every type that panicked -- the authoritative list of
// remaining decoration bombs. A feature with no registered body is a content gap (silent
// no-op), not a crash, so it is skipped here.
func TestAllConfiguredFeatureBodiesDoNotPanic(t *testing.T) {
	const minY, height = -64, 384
	reg := feature.NewEmbeddedRegistry()
	if err := reg.LoadAllEmbedded(); err != nil {
		t.Fatalf("LoadAllEmbedded: %v", err)
	}
	ids, err := data.ConfiguredFeatureIDs()
	if err != nil {
		t.Fatalf("ConfiguredFeatureIDs: %v", err)
	}

	type crash struct {
		id, typ string
		val     any
	}
	var crashes []crash

	for _, id := range ids {
		ref := "minecraft:" + id
		cf, err := reg.ResolveConfigured(ref)
		if err != nil {
			t.Errorf("ResolveConfigured %q: %v", id, err)
			continue
		}
		body := lookupFeatureBody(cf.Type)
		if body == nil {
			continue // no body registered -> silent no-op, not a crash vector
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					crashes = append(crashes, crash{id: id, typ: cf.Type, val: r})
				}
			}()
			// A fresh 3x3 over empty chunks + a real placement context; run the body at a
			// handful of positions (some bodies short-circuit before decode on the first pos).
			view := build3x3([2]int{0, 0}, minY, height)
			placer := newConfiguredPlacer(cf, view, reg, 0, false, nil, 63, 0)
			// A non-nil biomeAt (a constant biome): production always threads bc.get, never nil,
			// so a nil here is a test artifact (freeze_top_layer's BiomeAt), not a prod bomb.
			ctx := newPlacementContext(view, minY, height, func(x, y, z int) levelbiome.Type { return levelbiome.Type(0) })
			for i, seed := range []int64{1, 7, 42, 99} {
				rng := levelgen.NewLegacyRandomSource(seed)
				pos := placement.BlockPos{X: 8 + i, Y: 64, Z: 8 + i}
				placer(ctx, rng, pos)
			}
		}()
	}

	if len(crashes) > 0 {
		sort.Slice(crashes, func(i, j int) bool { return crashes[i].id < crashes[j].id })
		msg := fmt.Sprintf("%d configured_feature body(ies) PANIC on decode (decoration bombs):\n", len(crashes))
		for _, c := range crashes {
			msg += fmt.Sprintf("  - %s (type=%s): %v\n", c.id, c.typ, c.val)
		}
		t.Fatal(msg)
	}
}
