package component

// patch.go — a decode/rebuild view over a SlotData's RawComponents wire span, so gameplay code that
// must READ or EDIT specific data components on an item stack (the grindstone enchant strip + XP read;
// the smithing component preserve) can do so without importing every component schema by hand.
//
// The SlotData model stores components as RawComponents: the verbatim wire bytes AFTER the
// AddedCount/RemovedCount header, i.e. AddedCount×(VarInt typeId + value) followed by
// RemovedCount×(VarInt typeId). This file parses that span into a Patch (an ordered list of added
// entries, each with its decoded DataComponent, plus a list of removed type ids) and re-encodes it,
// keeping the AddedCount/RemovedCount headers consistent.
//
// This is NOT a new wire format — it decodes+re-encodes the SAME bytes SlotData.ReadFrom/WriteTo use,
// via the same NewComponent registry. The added-entry ORDER is preserved (vanilla DataComponentPatch is
// an insertion-ordered map, and the wire is emitted in that order), so a decode∘encode with no edits
// reproduces byte-identical RawComponents whenever every component type round-trips (the common case).

import (
	"bytes"

	pk "github.com/imhinotori/sulfur/net/packet"
)

// PatchEntry is one added component in a stack's component patch: its wire type id + the decoded value.
type PatchEntry struct {
	TypeID int32
	Value  DataComponent
}

// Patch is the decoded added+removed component patch of a SlotData. Added preserves wire order; Removed
// is the list of removed component type ids (the "!id" disk keys). A stack with no components has an
// empty Patch.
type Patch struct {
	Added   []PatchEntry
	Removed []int32
}

// DecodePatch parses a SlotData's added+removed component lists into a Patch. It uses the SlotData's
// AddedCount/RemovedCount headers to frame the RawComponents span, decoding each added value via the
// same NewComponent registry SlotData.ReadFrom uses. An unknown added component type or a truncated
// span stops decoding early (returning what parsed cleanly) — the caller edits only the components it
// understands, and an unedited stack's original RawComponents can still be re-emitted verbatim.
func DecodePatch(s SlotData) Patch {
	var p Patch
	if len(s.RawComponents) == 0 {
		return p
	}
	r := bytes.NewReader(s.RawComponents)
	for i := int32(0); i < int32(s.AddedCount); i++ {
		var typeID pk.VarInt
		if _, err := typeID.ReadFrom(r); err != nil {
			return p
		}
		comp := NewComponent(int32(typeID))
		if comp == nil {
			return p // unknown type: cannot safely consume its value — stop (matches SlotData.ReadFrom).
		}
		if _, err := comp.ReadFrom(r); err != nil {
			return p
		}
		p.Added = append(p.Added, PatchEntry{TypeID: int32(typeID), Value: comp})
	}
	for i := int32(0); i < int32(s.RemovedCount); i++ {
		var typeID pk.VarInt
		if _, err := typeID.ReadFrom(r); err != nil {
			return p
		}
		p.Removed = append(p.Removed, int32(typeID))
	}
	return p
}

// Get returns the decoded added component with the given wire type id, or nil if the stack has no such
// added component. (Removed entries carry no value and are not returned here.)
func (p Patch) Get(typeID int32) DataComponent {
	for i := range p.Added {
		if p.Added[i].TypeID == typeID {
			return p.Added[i].Value
		}
	}
	return nil
}

// Has reports whether the patch carries an added component with the given type id.
func (p Patch) Has(typeID int32) bool { return p.Get(typeID) != nil }

// Set inserts or replaces the added component for typeID, preserving the existing position when
// replacing and appending (in call order) when new. A component present in Removed is un-removed.
func (p *Patch) Set(typeID int32, value DataComponent) {
	for i := range p.Added {
		if p.Added[i].TypeID == typeID {
			p.Added[i].Value = value
			p.unremove(typeID)
			return
		}
	}
	p.Added = append(p.Added, PatchEntry{TypeID: typeID, Value: value})
	p.unremove(typeID)
}

// Remove drops the added component with the given type id (if present). It does NOT add a "removed"
// marker — it simply clears the added value, which is the correct edit when stripping an added
// component whose default is empty/absent (enchantments, damage overrides in this server's model).
func (p *Patch) Remove(typeID int32) {
	out := p.Added[:0]
	for i := range p.Added {
		if p.Added[i].TypeID != typeID {
			out = append(out, p.Added[i])
		}
	}
	p.Added = out
}

func (p *Patch) unremove(typeID int32) {
	out := p.Removed[:0]
	for _, id := range p.Removed {
		if id != typeID {
			out = append(out, id)
		}
	}
	p.Removed = out
}

// Encode re-serializes the patch into the (addedCount, removedCount, RawComponents) triple, writing the
// added entries (VarInt typeId + value) then the removed type ids (VarInt) into a fresh RawComponents
// span. The returned counts + bytes are consistent for SlotData.WriteTo.
func (p Patch) Encode() (added, removed pk.VarInt, raw []byte) {
	var buf bytes.Buffer
	for i := range p.Added {
		_, _ = pk.VarInt(p.Added[i].TypeID).WriteTo(&buf)
		_, _ = p.Added[i].Value.WriteTo(&buf)
	}
	for _, id := range p.Removed {
		_, _ = pk.VarInt(id).WriteTo(&buf)
	}
	return pk.VarInt(len(p.Added)), pk.VarInt(len(p.Removed)), buf.Bytes()
}

// ApplyTo writes the patch back onto a stack (its AddedCount/RemovedCount headers + RawComponents),
// leaving Count/ItemID untouched. The returned stack is a value copy (SlotData is a value type with a
// slice field; ApplyTo replaces that slice).
func (p Patch) ApplyTo(s SlotData) SlotData {
	added, removed, raw := p.Encode()
	s.AddedCount = added
	s.RemovedCount = removed
	s.RawComponents = raw
	return s
}
