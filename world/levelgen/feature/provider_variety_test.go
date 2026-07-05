package feature
import "testing"
func TestFlowerMeadowVarietyArray(t *testing.T) {
	// flower_meadow dual_noise_provider with variety as an ARRAY [1,3] (the InclusiveRange
	// list form) must parse without panic (previously crashed decoration).
	raw := []byte(`{"type":"minecraft:dual_noise_provider","seed":2345,"noise":{"firstOctave":-3,"amplitudes":[1.0]},"scale":1.0,"slow_noise":{"firstOctave":-10,"amplitudes":[1.0]},"slow_scale":1.0,"variety":[1,3],"states":[{"Name":"minecraft:tall_grass"},{"Name":"minecraft:grass_block"}]}`)
	p, err := ParseProvider(raw)
	if err != nil { t.Fatalf("parse dual_noise variety-array: %v", err) }
	if p == nil { t.Fatal("nil provider") }
}
