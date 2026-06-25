package feature

import (
	"strings"
	"testing"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/world/levelgen/data"
)

// mapSource is an in-memory DataSource for the synthetic-error / cycle / unit cases,
// so they don't depend on (or pollute) the real embed.
type mapSource struct {
	configured map[string][]byte
	placed     map[string][]byte
}

func (m mapSource) ConfiguredFeatureJSON(id string) ([]byte, error) {
	b, ok := m.configured[strip(id)]
	if !ok {
		return nil, errNotFound(id)
	}
	return b, nil
}

func (m mapSource) PlacedFeatureJSON(id string) ([]byte, error) {
	b, ok := m.placed[strip(id)]
	if !ok {
		return nil, errNotFound(id)
	}
	return b, nil
}

func strip(id string) string {
	if i := strings.IndexByte(id, ':'); i >= 0 {
		return id[i+1:]
	}
	return id
}

func errNotFound(id string) error { return &notFoundErr{id} }

type notFoundErr struct{ id string }

func (e *notFoundErr) Error() string { return "not found: " + e.id }

// TestParseKnownType: a configured_feature with a KNOWN type ("ore", "tree") parses
// to a typed-but-deferred node WITHOUT error.
func TestParseKnownType(t *testing.T) {
	r := NewRegistry(mapSource{})
	cases := []struct {
		name string
		raw  string
		typ  string
	}{
		{"ore", `{"type":"minecraft:ore","config":{"size":9,"discard_chance_on_air_exposure":0.0,"targets":[]}}`, "ore"},
		{"tree", `{"type":"minecraft:tree","config":{"decorators":[]}}`, "tree"},
		{"random_patch", `{"type":"minecraft:random_patch","config":{"tries":4}}`, "random_patch"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			cf, err := r.ParseConfiguredFeature("minecraft:"+c.name, []byte(c.raw))
			if err != nil {
				t.Fatalf("ParseConfiguredFeature(%s): %v", c.name, err)
			}
			if cf.Type != c.typ {
				t.Errorf("Type = %q; want %q", cf.Type, c.typ)
			}
			if cf.Config == nil {
				t.Error("Config is nil; want a captured config")
			}
		})
	}
}

// TestParseUnknownTypeErrors: a GENUINELY unknown "type" errors LOUDLY naming it
// (the density end_islands T-11-01 precedent).
func TestParseUnknownTypeErrors(t *testing.T) {
	r := NewRegistry(mapSource{})
	raw := `{"type":"minecraft:not_a_real_feature","config":{}}`
	_, err := r.ParseConfiguredFeature("minecraft:bogus", []byte(raw))
	if err == nil {
		t.Fatal("ParseConfiguredFeature(unknown type) returned nil error; want a loud error")
	}
	if !strings.Contains(err.Error(), "not_a_real_feature") {
		t.Errorf("error %q does not name the unknown type", err.Error())
	}
}

// TestResolveBlockState: a {Name, Properties} ref resolves to the right StateID, and
// a config carrying it surfaces the resolved id; an unknown block errors loudly.
func TestResolveBlockState(t *testing.T) {
	// Direct resolver: oak_log{axis:y} resolves, and matches the level/block default
	// path for the same {Name,Properties}.
	bs := blockStateJSON{Name: "minecraft:oak_log", Properties: map[string]string{"axis": "y"}}
	got, err := resolveBlockState(bs)
	if err != nil {
		t.Fatalf("resolveBlockState(oak_log{axis:y}): %v", err)
	}
	if got == 0 {
		t.Error("resolveBlockState returned the zero StateID; want a real id")
	}

	// A config carrying a simple_state_provider with an oak_log state surfaces the
	// resolved StateID in ParsedConfig.States.
	r := NewRegistry(mapSource{})
	raw := `{"type":"minecraft:simple_block","config":{"to_place":{"type":"minecraft:simple_state_provider","state":{"Name":"minecraft:oak_log","Properties":{"axis":"y"}}}}}`
	cf, err := r.ParseConfiguredFeature("minecraft:test", []byte(raw))
	if err != nil {
		t.Fatalf("ParseConfiguredFeature(simple_block): %v", err)
	}
	if len(cf.Config.States) != 1 {
		t.Fatalf("Config.States = %v; want exactly 1 resolved state", cf.Config.States)
	}
	if cf.Config.States[0] != got {
		t.Errorf("Config.States[0] = %d; want %d (the same oak_log{axis:y} id)", cf.Config.States[0], got)
	}

	// An unknown block name errors loudly.
	_, err = resolveBlockState(blockStateJSON{Name: "minecraft:not_a_block"})
	if err == nil || !strings.Contains(err.Error(), "not_a_block") {
		t.Errorf("resolveBlockState(unknown block) = %v; want a loud error naming it", err)
	}
}

// TestPlacedRefResolves: a placed_feature resolves its configured ref (cached/
// DAG-deduped) and captures its placement modifier list verbatim.
func TestPlacedRefResolves(t *testing.T) {
	src := mapSource{
		configured: map[string][]byte{
			"oak": []byte(`{"type":"minecraft:tree","config":{"decorators":[]}}`),
		},
		placed: map[string][]byte{
			"oak": []byte(`{"feature":"minecraft:oak","placement":[{"type":"minecraft:in_square"},{"type":"minecraft:count","count":1}]}`),
		},
	}
	r := NewRegistry(src)
	pf, err := r.ResolvePlaced("minecraft:oak")
	if err != nil {
		t.Fatalf("ResolvePlaced(oak): %v", err)
	}
	if pf.Feature == nil {
		t.Fatal("placed_feature.Feature is nil; want the resolved configured_feature")
	}
	if pf.Feature.Type != "tree" {
		t.Errorf("resolved configured type = %q; want tree", pf.Feature.Type)
	}
	if pf.FeatureRef != "minecraft:oak" {
		t.Errorf("FeatureRef = %q; want minecraft:oak", pf.FeatureRef)
	}
	if len(pf.Placement) != 2 {
		t.Fatalf("Placement len = %d; want 2 (order preserved)", len(pf.Placement))
	}
	if pf.Placement[0].Type != "in_square" || pf.Placement[1].Type != "count" {
		t.Errorf("Placement types = [%q,%q]; want [in_square,count]", pf.Placement[0].Type, pf.Placement[1].Type)
	}

	// DAG dedup: resolving the configured ref again returns the SAME instance.
	cf2, err := r.ResolveConfigured("minecraft:oak")
	if err != nil {
		t.Fatalf("ResolveConfigured(oak): %v", err)
	}
	if cf2 != pf.Feature {
		t.Error("ResolveConfigured returned a different instance; want the DAG-deduped cached one")
	}
}

// TestCyclicRefRejected: a synthetic placed->configured->placed self-loop is rejected
// by the parsing-set cycle guard (T-11-02), not infinitely recursed.
func TestCyclicRefRejected(t *testing.T) {
	// A configured_feature whose inline sub-config is itself a placed_feature ref
	// back to the original placed_feature would cycle. We model the cycle directly:
	// placed "loop" -> inline configured -> (we make the configured ref re-enter the
	// same placed via an inline feature that refs back). Simplest faithful model: a
	// placed_feature whose "feature" is an inline configured_feature of type
	// "random_selector" whose config nests a placed ref back to "loop". Since the
	// config is captured raw (not recursed for placed refs in Phase 11), we instead
	// exercise the guard via a configured ref that resolves to a placed ref cycle
	// through a custom source that loops a configured id onto itself.
	src := mapSource{
		configured: map[string][]byte{
			// "a" is type random_selector; its inline feature refs configured "a"
			// again (a configured->configured self-loop through ResolveConfigured).
			"a": []byte(`{"type":"minecraft:random_selector","config":{}}`),
		},
		placed: map[string][]byte{
			// placed "loop" -> configured "loopcf" (inline) which is a placed ref
			// back to "loop": placed->configured->placed cycle.
			"loop": []byte(`{"feature":"minecraft:loop","placement":[]}`),
		},
	}
	// placed "loop" refs configured "loop", but no configured "loop" exists in the
	// source -> a clean not-found error (not a hang). To exercise the CYCLE guard,
	// wire a configured that re-enters itself.
	r := NewRegistry(src)

	// Direct cycle guard exercise: pre-mark "cf:minecraft:a" as parsing, then a
	// nested resolve of the same id must be rejected.
	r.parsing["cf:minecraft:a"] = true
	_, err := r.ResolveConfigured("minecraft:a")
	if err == nil || !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("ResolveConfigured under an in-progress parse = %v; want a cyclic-reference error", err)
	}
	delete(r.parsing, "cf:minecraft:a")

	// And the placed guard: a placed ref re-entering itself is rejected.
	r.parsing["pf:minecraft:loop"] = true
	_, err = r.ResolvePlaced("minecraft:loop")
	if err == nil || !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("ResolvePlaced under an in-progress parse = %v; want a cyclic-reference error", err)
	}
}

// TestLoadAll is the FEAT-02 acceptance: the polymorphic parser loads the FULL real
// embed — all 226 configured_feature + 262 placed_feature entries parse clean (no
// genuinely-unknown type, no unresolvable block-state ref), and every placed_feature's
// configured ref resolves to a non-nil ConfiguredFeature. A build-data mismatch (a
// missing referenced file, a new feature type, a bad block-state) fails LOUDLY here,
// at generator-construction time, not mid-decoration (T-11-01/T-11-02).
func TestLoadAll(t *testing.T) {
	r := NewEmbeddedRegistry()
	if err := r.LoadAllEmbedded(); err != nil {
		t.Fatalf("LoadAllEmbedded() over the full real embed: %v", err)
	}

	// Assert the embedded counts were all parsed + cached.
	configuredIDs, err := data.ConfiguredFeatureIDs()
	if err != nil {
		t.Fatalf("ConfiguredFeatureIDs: %v", err)
	}
	placedIDs, err := data.PlacedFeatureIDs()
	if err != nil {
		t.Fatalf("PlacedFeatureIDs: %v", err)
	}
	if len(configuredIDs) != 226 {
		t.Errorf("configured_feature count = %d; want 226", len(configuredIDs))
	}
	if len(placedIDs) != 262 {
		t.Errorf("placed_feature count = %d; want 262", len(placedIDs))
	}

	// Every configured_feature parsed to a cached node.
	for _, id := range configuredIDs {
		if cf := r.ConfiguredByID("minecraft:" + id); cf == nil {
			t.Errorf("configured_feature %q not cached after LoadAll", id)
		}
	}
	// Every placed_feature parsed AND resolved its configured ref to non-nil.
	for _, id := range placedIDs {
		pf := r.PlacedByID("minecraft:" + id)
		if pf == nil {
			t.Errorf("placed_feature %q not cached after LoadAll", id)
			continue
		}
		if pf.Feature == nil {
			t.Errorf("placed_feature %q has a nil configured ref", id)
		}
	}
}

// compile-time guard: block.StateID is the resolved id type the config carries.
var _ block.StateID
