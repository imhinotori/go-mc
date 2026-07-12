package save

import (
	"testing"

	"github.com/imhinotori/sulfur/nbt"
	"github.com/imhinotori/sulfur/nbt/dynbt"
)

// TestItemEntityAgeHealthPickupDelayAreShort is the regression gate for the widened-Age bug: a dropped
// ItemEntity writes Age, Health and PickupDelay ALL via ValueOutput.putShort (TAG_Short). A vanilla
// reader's getShortOr("Age"/"Health"/"PickupDelay") type-mismatches on a TAG_Int, so the on-disk tag
// TYPE (not just the value) must be Short. CITE ItemEntity.addAdditionalSaveData (putShort x3).
func TestItemEntityAgeHealthPickupDelayAreShort(t *testing.T) {
	rec := Entities{
		ID:          "minecraft:item",
		Age:         ShortNum(42),
		Health:      ShortNum(5),
		PickupDelay: 7,
	}
	data, err := nbt.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var v dynbt.Value
	if err := nbt.Unmarshal(data, &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, tc := range []struct {
		key     string
		wantTag byte
		wantVal int64
	}{
		{"Age", nbt.TagShort, 42},
		{"Health", nbt.TagShort, 5},
		{"PickupDelay", nbt.TagShort, 7},
	} {
		f := v.Get(tc.key)
		if f == nil {
			t.Fatalf("%s: missing key", tc.key)
		}
		if got := f.TagType(); got != tc.wantTag {
			t.Fatalf("%s: tag type = 0x%02x, want TAG_Short 0x%02x", tc.key, got, tc.wantTag)
		}
		if got := f.Short(); int64(got) != tc.wantVal {
			t.Fatalf("%s: value = %d, want %d", tc.key, got, tc.wantVal)
		}
	}

	// Round-trip back into a record: the item Age reads back as an int (getShortOr widens to int).
	var back Entities
	if err := nbt.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal into record: %v", err)
	}
	if back.Age == nil || back.Age.TagType() != nbt.TagShort || back.Age.IntValue() != 42 {
		t.Fatalf("round-trip Age = %+v, want TAG_Short 42", back.Age)
	}
	if back.Health == nil || back.Health.TagType() != nbt.TagShort || back.Health.IntValue() != 5 {
		t.Fatalf("round-trip Health = %+v, want TAG_Short 5", back.Health)
	}
}

// TestAgeableMobAgeForcedAgeAreInt asserts an AgeableMob writes Age (and ForcedAge) via putInt
// (TAG_Int) and Health via putFloat (TAG_Float), matching the vanilla reader (getIntOr / getFloatOr).
// CITE AgeableMob.addAdditionalSaveData (putInt "Age"/"ForcedAge") + LivingEntity (putFloat "Health").
func TestAgeableMobAgeForcedAgeAreInt(t *testing.T) {
	rec := Entities{
		ID:        "minecraft:pig",
		Age:       IntNum(-24000),
		ForcedAge: 100,
		Health:    FloatNum(8),
	}
	data, err := nbt.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var v dynbt.Value
	if err := nbt.Unmarshal(data, &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if f := v.Get("Age"); f == nil || f.TagType() != nbt.TagInt {
		t.Fatalf("Age tag = %v, want TAG_Int", f)
	} else if f.Int() != -24000 {
		t.Fatalf("Age value = %d, want -24000", f.Int())
	}
	if f := v.Get("ForcedAge"); f == nil || f.TagType() != nbt.TagInt {
		t.Fatalf("ForcedAge tag = %v, want TAG_Int", f)
	}
	if f := v.Get("Health"); f == nil || f.TagType() != nbt.TagFloat {
		t.Fatalf("Health tag = %v, want TAG_Float", f)
	} else if f.Float() != 8 {
		t.Fatalf("Health value = %v, want 8", f.Float())
	}

	var back Entities
	if err := nbt.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal into record: %v", err)
	}
	if back.Age == nil || back.Age.TagType() != nbt.TagInt || back.Age.IntValue() != -24000 {
		t.Fatalf("round-trip Age = %+v, want TAG_Int -24000", back.Age)
	}
	if back.Health == nil || back.Health.TagType() != nbt.TagFloat || back.Health.FloatValue() != 8 {
		t.Fatalf("round-trip Health = %+v, want TAG_Float 8", back.Health)
	}
}

// TestPlainEntityOmitsAgeHealth asserts a non-item, non-living record (a projectile) emits NEITHER Age
// nor Health -- byte-identical to the pre-regression plain record (both fields nil => omitempty drop).
func TestPlainEntityOmitsAgeHealth(t *testing.T) {
	rec := Entities{ID: "minecraft:arrow"}
	data, err := nbt.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var v dynbt.Value
	if err := nbt.Unmarshal(data, &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if f := v.Get("Age"); f != nil {
		t.Fatalf("plain entity emitted Age (tag 0x%02x), want no key", f.TagType())
	}
	if f := v.Get("Health"); f != nil {
		t.Fatalf("plain entity emitted Health (tag 0x%02x), want no key", f.TagType())
	}
}
