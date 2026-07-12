package save

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/imhinotori/sulfur/nbt"
)

// OptNum is an optional NBT numeric whose on-disk TAG TYPE is carried in the value itself, not inferred
// from a Go field kind. It exists because a single save.Entities record is (ab)used for BOTH a dropped
// ItemEntity and an AgeableMob, which write the SAME NBT keys with DIFFERENT vanilla tag types:
//
//   - "Age":    ItemEntity.addAdditionalSaveData writes it via ValueOutput.putShort (TAG_Short); an item's
//               age is a short. AgeableMob.addAdditionalSaveData writes it via ValueOutput.putInt
//               (TAG_Int); a mob's breeding age is an int.
//   - "Health": ItemEntity.addAdditionalSaveData writes it via putShort (TAG_Short); LivingEntity
//               .addAdditionalSaveData writes it via putFloat (TAG_Float).
//
// An entity is EITHER an item OR a mob, so a given record only ever populates one variant, but the two
// share the key name -- a plain int32/float32 Go field would force a single tag type and emit the wrong
// one for the other kind. (This is the exact regression: a widened Age int32 field emitted TAG_Int for a
// dropped item, where a vanilla reader's getShortOr("Age") expects TAG_Short.)
//
// OptNum implements nbt.Marshaler + nbt.Unmarshaler so it self-describes its tag type on write and
// accepts whatever tag type is on disk on read. It is used as a pointer field (*optNum) with
// nbt:",omitempty" so a record that carries neither an item nor a mob age/health (a projectile, a
// falling block) emits no key at all -- byte-identical to the pre-existing plain-entity record.
type OptNum struct {
	// tag is the NBT tag type this value serializes as: nbt.TagShort, nbt.TagInt, or nbt.TagFloat.
	tag byte
	// bits holds the value. For TagShort/TagInt it is the signed integer (as int64); for TagFloat it is
	// the float32 stored via math.Float32bits (as uint64) so the exact bit pattern round-trips.
	bits uint64
}

// ShortNum builds an OptNum that serializes as TAG_Short (the ItemEntity Age/Health/PickupDelay form).
func ShortNum(v int16) *OptNum { return &OptNum{tag: nbt.TagShort, bits: uint64(int64(v))} }

// IntNum builds an OptNum that serializes as TAG_Int (the AgeableMob Age/ForcedAge form).
func IntNum(v int32) *OptNum { return &OptNum{tag: nbt.TagInt, bits: uint64(int64(v))} }

// FloatNum builds an OptNum that serializes as TAG_Float (the LivingEntity Health form).
func FloatNum(v float32) *OptNum {
	return &OptNum{tag: nbt.TagFloat, bits: uint64(math.Float32bits(v))}
}

// intValue returns the value as an int (valid for TagShort/TagInt records, matching vanilla's getShortOr/
// getIntOr which both widen to int).
func (n *OptNum) IntValue() int {
	if n == nil {
		return 0
	}
	if n.tag == nbt.TagFloat {
		return int(math.Float32frombits(uint32(n.bits)))
	}
	return int(int64(n.bits))
}

// floatValue returns the value as a float32 (valid for a TagFloat Health record).
func (n *OptNum) FloatValue() float32 {
	if n == nil {
		return 0
	}
	if n.tag == nbt.TagFloat {
		return math.Float32frombits(uint32(n.bits))
	}
	return float32(int64(n.bits))
}

// TagType reports the NBT tag byte written for this value -- the whole point of the type. Implements
// nbt.Marshaler.
func (n OptNum) TagType() byte { return n.tag }

// MarshalNBT writes the value payload in the on-disk (big-endian) form for its tag type. Implements
// nbt.Marshaler.
func (n OptNum) MarshalNBT(w io.Writer) error {
	switch n.tag {
	case nbt.TagShort:
		return binary.Write(w, binary.BigEndian, int16(int64(n.bits)))
	case nbt.TagInt:
		return binary.Write(w, binary.BigEndian, int32(int64(n.bits)))
	case nbt.TagFloat:
		return binary.Write(w, binary.BigEndian, uint32(n.bits))
	default:
		return fmt.Errorf("save: OptNum unsupported tag type 0x%02x", n.tag)
	}
}

// UnmarshalNBT reads the value using whatever tag type is on disk, so a record written by either kind
// (item TAG_Short or mob TAG_Int/TAG_Float) round-trips faithfully. Implements nbt.Unmarshaler.
func (n *OptNum) UnmarshalNBT(tagType byte, r nbt.DecoderReader) error {
	switch tagType {
	case nbt.TagShort:
		var v int16
		if err := binary.Read(r, binary.BigEndian, &v); err != nil {
			return err
		}
		n.tag, n.bits = nbt.TagShort, uint64(int64(v))
	case nbt.TagInt:
		var v int32
		if err := binary.Read(r, binary.BigEndian, &v); err != nil {
			return err
		}
		n.tag, n.bits = nbt.TagInt, uint64(int64(v))
	case nbt.TagFloat:
		var v uint32
		if err := binary.Read(r, binary.BigEndian, &v); err != nil {
			return err
		}
		n.tag, n.bits = nbt.TagFloat, uint64(v)
	default:
		return fmt.Errorf("save: OptNum cannot decode tag type 0x%02x", tagType)
	}
	return nil
}
