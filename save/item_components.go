package save

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/registryid"
	"github.com/imhinotori/sulfur/level/component"
	"github.com/imhinotori/sulfur/nbt"
	"github.com/imhinotori/sulfur/nbt/dynbt"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// item_components.go — SUB-ITEMNBT PHASE B: the WIRE↔DISK transcoder for item data components.
//
// The gap (see item_nbt.go header): a component-bearing stack is held tick-side as the verbatim
// WIRE component lists (component.SlotData.RawComponents — the added+removed lists after the
// removedCount header, each added entry = VarInt typeId + type.streamCodec() body, each removed
// entry = VarInt typeId). The DISK form is DataComponentPatch.CODEC: a string-keyed NBT compound
// where a present component is keyed by its type's registry id string ("minecraft:damage", …) with
// type.codecOrThrow() (the DATA/NBT codec) value, and a REMOVED component is keyed "!"+id with an
// empty-compound value (Codec.EMPTY). The two serializers differ, so a per-DataComponentType
// wire→data transcode is required. This file is that transcode.
//
// 1:1 jar port (temp/cache/26.2-inner.jar, javap -c -p / CFR, verified this session). Sources:
//   - net.minecraft.core.component.DataComponentPatch.CODEC:
//       Codec.dispatchedMap(PatchKey.CODEC, PatchKey::valueCodec); present -> id->value,
//       removed -> "!"+id -> Codec.EMPTY ({}).
//   - net.minecraft.core.component.DataComponentPatch$PatchKey.CODEC:
//       key string = removed ? REMOVED_PREFIX+id.toString() : id.toString(); REMOVED_PREFIX = "!".
//       valueCodec() = removed ? Codec.EMPTY.codec() : type.codecOrThrow().
//
// SUPPORTED (real, byte-stable round-trip) — the common inventory set. Each cites its
// DataComponents.<NAME> registration and its DATA codec:
//   - minecraft:damage       DataComponents.DAMAGE       = ExtraCodecs.NON_NEGATIVE_INT   -> TAG_Int
//   - minecraft:max_damage   DataComponents.MAX_DAMAGE   = ExtraCodecs.POSITIVE_INT       -> TAG_Int
//   - minecraft:repair_cost  DataComponents.REPAIR_COST  = ExtraCodecs.NON_NEGATIVE_INT   -> TAG_Int
//   - minecraft:custom_name  DataComponents.CUSTOM_NAME  = ComponentSerialization.CODEC    -> Component NBT
//   - minecraft:lore         DataComponents.LORE         = ItemLore.CODEC (list<Component>)-> TAG_List<Component>
//   - minecraft:potion_contents DataComponents.POTION_CONTENTS = PotionContents.CODEC     -> compound
//
// The Component (custom_name/lore) transcode is direct: chat.Message's WIRE codec is already
// ComponentSerialization NBT (network format: [tagByte][body], no name — see chat/nbtmessage.go),
// which is the SAME serialization DataComponents.CUSTOM_NAME persists (ComponentSerialization.CODEC).
// So decoding the wire component into a chat.Message and re-marshalling its NBT body is a faithful
// inverse; no shape translation is needed for text components.
//
// DEFERRED (counted-and-dropped, cited seam — never a silent loss):
//   - minecraft:enchantments / minecraft:stored_enchantments (ItemEnchantments.CODEC =
//     Codec.unboundedMap(Enchantment.CODEC, level)): the DATA key is the enchantment IDENTIFIER
//     STRING, but the WIRE (Enchantment.STREAM_CODEC = VarInt id) carries the numeric protocol id.
//     Enchantment is a DATAPACK registry (registry/codec.go: Registry[nbt.RawMessage]
//     `registry:"minecraft:enchantment"`), whose numeric↔string mapping is assigned at registry-sync
//     time and is NOT available in the save layer (no static data/registryid/enchantment.go, unlike
//     Item/Potion/MobEffect). Writing a fabricated id would violate the 1:1 mandate, so enchantments
//     stay in the raw drop bucket, COUNTED. SEAM: when the runtime enchantment registry is threaded
//     into the save layer (an id→string resolver mirroring itemName/potionName), add case 13/42 here.
//   - the ~100-component long tail: any component whose wire type has no supported DATA transcode
//     here stays counted-and-dropped. SEAM: DataComponents registry / component.NewComponent — add a
//     case per type as its DATA codec is ported.
//
// A component whose WIRE body cannot even be decoded (component.NewComponent == nil for its type id,
// or a decode error) also falls into the counted drop bucket — the wire reader cannot advance past an
// unknown-length body, matching component.SlotData.ReadFrom's own unknown-component stop.

// wireToDiskComponents transcodes a DiskItem's captured wire component lists (WireComponents, the
// added+removed span with WireAddedCount/WireRemovedCount headers) into the DISK DataComponentPatch
// compound. It returns the built *DiskComponents (nil when nothing was transcoded) and dropped, the
// number of PRESENT (added) components that could not be transcoded to disk (the Phase-A loss, now
// metered per-component). Removed components transcode losslessly (they carry no value) and are never
// counted as dropped.
//
// CITE: DataComponentPatch.CODEC encode lambda — for each present entry emit PatchKey(type,false)->
// value; for each removed entry emit PatchKey(type,true)->Unit (disk {}).
func wireToDiskComponents(it DiskItem) (comps *DiskComponents, dropped int) {
	if len(it.WireComponents) == 0 && it.WireAddedCount == 0 && it.WireRemovedCount == 0 {
		return nil, 0
	}
	disk := DiskComponents{}
	r := bytes.NewReader(it.WireComponents)

	// Present (added) components: VarInt typeId + type.streamCodec() body.
	for i := 0; i < it.WireAddedCount; i++ {
		var typeID pk.VarInt
		if _, err := typeID.ReadFrom(r); err != nil {
			// Truncated header: cannot advance — count the remaining added components as dropped.
			dropped += it.WireAddedCount - i
			break
		}
		comp := component.NewComponent(int32(typeID))
		if comp == nil {
			// Unknown component: its body length is unknown, so the reader cannot be advanced past
			// it — every following component in this span is unreadable. Count them all as dropped
			// and stop (matches SlotData.ReadFrom's own unknown-component early stop).
			dropped += it.WireAddedCount - i
			r = bytes.NewReader(nil) // nothing more is safely readable
			break
		}
		if _, err := comp.ReadFrom(r); err != nil {
			dropped += it.WireAddedCount - i
			break
		}
		key, val, ok := diskEncodeComponent(comp)
		if !ok {
			dropped++
			continue
		}
		disk[key] = val
	}

	// Removed components: VarInt typeId only. Disk key = "!"+id, value = empty compound (Codec.EMPTY).
	for i := 0; i < it.WireRemovedCount; i++ {
		var typeID pk.VarInt
		if _, err := typeID.ReadFrom(r); err != nil {
			break
		}
		id := componentIDByType(int32(typeID))
		if id == "" {
			// Unknown removed id: skip (nothing to drop — a removal carries no value). It cannot be
			// re-expressed faithfully without the id string, so it is not written.
			continue
		}
		disk["!"+id] = emptyCompoundValue()
	}

	if len(disk) == 0 {
		return nil, dropped
	}
	return &disk, dropped
}

// diskToWireComponents is the faithful inverse: it parses a DISK DataComponentPatch compound back into
// the WIRE component lists (added body span + counts) so a loaded stack round-trips its supported
// components. Present entries (key not "!"-prefixed) re-encode via the component's streamCodec; removed
// entries (key "!"+id) contribute only a removed typeId. Keys whose id is unsupported/unknown are
// skipped (they were never written by wireToDiskComponents, so a faithful save never produces them;
// a hand-edited save that does is tolerated by dropping the unknown key rather than corrupting the wire).
//
// Returns the wire component span (added bodies then removed ids) plus the two counts, ready to place
// into a component.SlotData (AddedCount/RemovedCount/RawComponents). CITE: DataComponentPatch.CODEC
// decode lambda (id/"!"+id keys) + STREAM_CODEC.encode (added: type then value; removed: type only).
func diskToWireComponents(comps *DiskComponents) (raw []byte, added, removed int) {
	if comps == nil || len(*comps) == 0 {
		return nil, 0, 0
	}
	var addedBuf bytes.Buffer
	var removedBuf bytes.Buffer
	for key, val := range *comps {
		if len(key) > 0 && key[0] == '!' {
			// Removed component: "!"+id -> a removed typeId on the wire (no value).
			id := key[1:]
			typeID, ok := componentTypeByID(id)
			if !ok {
				continue
			}
			_, _ = pk.VarInt(typeID).WriteTo(&removedBuf)
			removed++
			continue
		}
		typeID, ok := componentTypeByID(key)
		if !ok {
			continue
		}
		body, ok := diskDecodeComponent(int32(typeID), val)
		if !ok {
			continue
		}
		_, _ = pk.VarInt(typeID).WriteTo(&addedBuf)
		addedBuf.Write(body)
		added++
	}
	if added == 0 && removed == 0 {
		return nil, 0, 0
	}
	// Wire order is added bodies then removed ids (SlotData layout).
	out := make([]byte, 0, addedBuf.Len()+removedBuf.Len())
	out = append(out, addedBuf.Bytes()...)
	out = append(out, removedBuf.Bytes()...)
	return out, added, removed
}

// diskEncodeComponent maps a decoded WIRE component to its DISK (id string, NBT value) pair. The bool
// is false for a component with no supported DATA transcode (the counted drop). CITE: each component's
// DataComponents.<NAME> registration DATA codec (see file header).
func diskEncodeComponent(comp component.DataComponent) (id string, val nbt.RawMessage, ok bool) {
	switch c := comp.(type) {
	case *component.Damage: // minecraft:damage -> TAG_Int (NON_NEGATIVE_INT)
		return c.ID(), intValue(int32(c.VarInt)), true
	case *component.MaxDamage: // minecraft:max_damage -> TAG_Int (POSITIVE_INT)
		return c.ID(), intValue(int32(c.VarInt)), true
	case *component.RepairCost: // minecraft:repair_cost -> TAG_Int (NON_NEGATIVE_INT)
		return c.ID(), intValue(int32(c.VarInt)), true
	case *component.CustomName: // minecraft:custom_name -> Component NBT (ComponentSerialization.CODEC)
		v, err := componentValue(c.Name)
		if err != nil {
			return "", nbt.RawMessage{}, false
		}
		return c.ID(), v, true
	case *component.Lore: // minecraft:lore -> TAG_List<Component> (ItemLore.CODEC)
		v, err := loreValue(c.Lines)
		if err != nil {
			return "", nbt.RawMessage{}, false
		}
		return c.ID(), v, true
	case *component.PotionContents: // minecraft:potion_contents -> compound (PotionContents.CODEC)
		v, err := potionContentsValue(c)
		if err != nil {
			return "", nbt.RawMessage{}, false
		}
		return c.ID(), v, true
	default:
		return "", nbt.RawMessage{}, false // long tail / enchantments: counted-and-dropped (header SEAM)
	}
}

// diskDecodeComponent is the inverse of diskEncodeComponent: it turns a DISK NBT value for the given
// wire type id back into the WIRE streamCodec body. The bool is false when the value cannot be decoded
// (skipped). Only the supported set is handled — an unsupported id is never written by the encoder.
func diskDecodeComponent(typeID int32, val nbt.RawMessage) (body []byte, ok bool) {
	switch typeID {
	case wireDamage, wireMaxDamage, wireRepairCost: // VarInt body
		n, ok := intFromValue(val)
		if !ok {
			return nil, false
		}
		var buf bytes.Buffer
		_, _ = pk.VarInt(n).WriteTo(&buf)
		return buf.Bytes(), true
	case wireCustomName: // Component NBT -> chat.Message wire body
		msg, ok := messageFromValue(val)
		if !ok {
			return nil, false
		}
		var buf bytes.Buffer
		if _, err := msg.WriteTo(&buf); err != nil {
			return nil, false
		}
		return buf.Bytes(), true
	case wireLore: // list<Component> -> pk.Array(chat.Message)
		lines, ok := loreFromValue(val)
		if !ok {
			return nil, false
		}
		var buf bytes.Buffer
		if _, err := pk.Array(&lines).WriteTo(&buf); err != nil {
			return nil, false
		}
		return buf.Bytes(), true
	case wirePotionContents: // compound -> PotionContents wire
		pc, ok := potionContentsFromValue(val)
		if !ok {
			return nil, false
		}
		var buf bytes.Buffer
		if _, err := pc.WriteTo(&buf); err != nil {
			return nil, false
		}
		return buf.Bytes(), true
	default:
		return nil, false
	}
}

// Wire type ids of the supported components (data/registryid/datacomponenttype.go / components.go
// NewComponent switch). Kept as named constants so diskDecodeComponent reads faithfully.
const (
	wireMaxDamage      = 2
	wireDamage         = 3
	wireCustomName     = 6
	wireLore           = 11
	wireRepairCost     = 19
	wirePotionContents = 51
)

// componentIDByType returns the registry id string for a wire type id, for the SUPPORTED set only
// (removed-component keys). An unsupported id returns "" (the removal is not re-expressible on disk).
func componentIDByType(typeID int32) string {
	comp := component.NewComponent(typeID)
	if comp == nil {
		return ""
	}
	// Only the supported set is re-expressible; gate on diskEncodeComponent's coverage so a removed
	// key is written iff its present form would also transcode (symmetry with the added path).
	switch typeID {
	case wireDamage, wireMaxDamage, wireRepairCost, wireCustomName, wireLore, wirePotionContents:
		return comp.ID()
	default:
		return ""
	}
}

// componentTypeByID is the reverse: registry id string -> wire type id, for the SUPPORTED set only.
func componentTypeByID(id string) (int32, bool) {
	switch id {
	case "minecraft:damage":
		return wireDamage, true
	case "minecraft:max_damage":
		return wireMaxDamage, true
	case "minecraft:repair_cost":
		return wireRepairCost, true
	case "minecraft:custom_name":
		return wireCustomName, true
	case "minecraft:lore":
		return wireLore, true
	case "minecraft:potion_contents":
		return wirePotionContents, true
	default:
		return 0, false
	}
}

// ---- disk NBT value builders / parsers ----------------------------------------------------------

// intValue builds a TAG_Int RawMessage (4-byte big-endian body). CITE: NBT TAG_Int is a signed
// 32-bit big-endian int.
func intValue(v int32) nbt.RawMessage {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(v))
	return nbt.RawMessage{Type: nbt.TagInt, Data: b[:]}
}

// intFromValue reads a TAG_Int (or any integral tag) body back to int32.
func intFromValue(m nbt.RawMessage) (int32, bool) {
	switch m.Type {
	case nbt.TagInt:
		if len(m.Data) != 4 {
			return 0, false
		}
		return int32(binary.BigEndian.Uint32(m.Data)), true
	case nbt.TagByte:
		if len(m.Data) != 1 {
			return 0, false
		}
		return int32(int8(m.Data[0])), true
	case nbt.TagShort:
		if len(m.Data) != 2 {
			return 0, false
		}
		return int32(int16(binary.BigEndian.Uint16(m.Data))), true
	default:
		return 0, false
	}
}

// marshalDiskValue encodes a Go value into a disk NBT RawMessage (tag byte + body) using the NBT
// encoder in network format ([tagByte][body], no name), then splits the tag byte off. This is the
// same [tagByte][body] framing chat.Message.MarshalNBT produces, so a component body captured here is
// byte-identical to the value the vanilla codec would persist.
func marshalDiskValue(v any) (nbt.RawMessage, error) {
	var buf bytes.Buffer
	enc := nbt.NewEncoder(&buf)
	enc.NetworkFormat(true)
	if err := enc.Encode(v, ""); err != nil {
		return nbt.RawMessage{}, err
	}
	out := buf.Bytes()
	if len(out) == 0 {
		return nbt.RawMessage{}, fmt.Errorf("save: empty nbt encoding")
	}
	return nbt.RawMessage{Type: out[0], Data: out[1:]}, nil
}

// componentValue builds the disk value for a minecraft:custom_name (a single Component). It marshals
// the chat.Message via the shared NBT framing (identical to the wire body), yielding the same
// TAG_String (text-only) / TAG_Compound shape the vanilla ComponentSerialization.CODEC persists.
func componentValue(msg chat.Message) (nbt.RawMessage, error) {
	return marshalDiskValue(msg)
}

// messageFromValue is the inverse: decode a disk Component value back into a chat.Message.
func messageFromValue(m nbt.RawMessage) (chat.Message, bool) {
	var msg chat.Message
	if err := m.Unmarshal(&msg); err != nil {
		return chat.Message{}, false
	}
	return msg, true
}

// loreValue builds the disk value for minecraft:lore (ItemLore.CODEC = list<Component>): a TAG_List
// whose elements are Component NBT. Encoding []chat.Message via the NBT encoder yields a TAG_List of
// the per-element chat.Message bodies (each text-only -> TAG_String, else TAG_Compound), matching
// ComponentSerialization.CODEC.listOf().
func loreValue(lines []chat.Message) (nbt.RawMessage, error) {
	return marshalDiskValue(lines)
}

// loreFromValue decodes a disk lore TAG_List back into []chat.Message.
func loreFromValue(m nbt.RawMessage) ([]chat.Message, bool) {
	var lines []chat.Message
	if err := m.Unmarshal(&lines); err != nil {
		return nil, false
	}
	return lines, true
}

// emptyCompoundValue is the disk value for a REMOVED component: an empty compound {} (Codec.EMPTY —
// a single TAG_End body). CITE: PatchKey.valueCodec() removed branch = Codec.EMPTY.codec().
func emptyCompoundValue() nbt.RawMessage {
	return nbt.RawMessage{Type: nbt.TagCompound, Data: []byte{nbt.TagEnd}}
}

// ---- potion_contents disk shape -----------------------------------------------------------------

// mobEffectDisk is the READ shape of MobEffectInstance / Details.MAP_CODEC: pointer fields so a MISSING
// key is distinguishable from a present zero, letting the decode apply the exact vanilla optionalFieldOf
// DEFAULTS (amplifier 0, duration 0, ambient false, show_particles true, show_icon = show_particles).
// The WRITE side does NOT use this struct — it builds the compound via dynbt (detailsToDisk) so the
// omission rules match Details.MAP_CODEC byte-for-byte (a bool+omitempty cannot express "omit when
// true", which show_particles requires). CITE: MobEffectInstance.Details.MAP_CODEC + Details.create
// (showIcon.orElse(showParticles)).
type mobEffectDisk struct {
	ID            string         `nbt:"id"`
	Amplifier     *int8          `nbt:"amplifier"`
	Duration      *int32         `nbt:"duration"`
	Ambient       *bool          `nbt:"ambient"`
	ShowParticles *bool          `nbt:"show_particles"`
	ShowIcon      *bool          `nbt:"show_icon"`
	HiddenEffect  *mobEffectDisk `nbt:"hidden_effect"`
}

// potionContentsDisk is the READ shape of PotionContents.FULL_CODEC: potion (string, optional),
// custom_color (int, optional), custom_effects (list<MobEffectInstance>, default empty), custom_name
// (string, optional). Pointer/slice fields so absence is faithful. The WRITE side builds the compound
// via dynbt (potionContentsValue) to apply the exact optionalFieldOf omission. CITE: PotionContents.
// FULL_CODEC.
type potionContentsDisk struct {
	Potion        *string         `nbt:"potion"`
	CustomColor   *int32          `nbt:"custom_color"`
	CustomEffects []mobEffectDisk `nbt:"custom_effects"`
	CustomName    *string         `nbt:"custom_name"`
}

// potionContentsValue builds the disk value for minecraft:potion_contents. It mirrors
// PotionContents.CODEC = withAlternative(FULL_CODEC, Potion.CODEC): a bottle whose ONLY set field is
// the potion Holder serializes via the alternative as a BARE TAG_String (the potion id) — this is the
// vanilla shape for the common brewing/creative bottle and is what createItemStack round-trips. Any
// bottle with custom_color/custom_effects/custom_name uses the FULL_CODEC compound.
func potionContentsValue(pc *component.PotionContents) (nbt.RawMessage, error) {
	hasColor := bool(pc.CustomColor.Has)
	hasEffects := len(pc.CustomEffects) > 0
	hasName := bool(pc.CustomName.Has)
	hasPotion := bool(pc.PotionID.Has)

	// withAlternative bare-string form: potion set, nothing else.
	if hasPotion && !hasColor && !hasEffects && !hasName {
		name := potionName(int32(pc.PotionID.Val))
		if name == "" {
			return nbt.RawMessage{}, fmt.Errorf("save: unknown potion id %d", int32(pc.PotionID.Val))
		}
		return marshalDiskValue(name)
	}

	// FULL_CODEC compound, built via dynbt so each optionalFieldOf omits at its exact vanilla default.
	root := dynbt.NewCompound()
	if hasPotion {
		name := potionName(int32(pc.PotionID.Val))
		if name == "" {
			return nbt.RawMessage{}, fmt.Errorf("save: unknown potion id %d", int32(pc.PotionID.Val))
		}
		root.Set("potion", dynbt.NewString(name))
	}
	if hasColor {
		root.Set("custom_color", dynbt.NewInt(int32(pc.CustomColor.Val)))
	}
	if hasName {
		root.Set("custom_name", dynbt.NewString(string(pc.CustomName.Val)))
	}
	if hasEffects {
		elems := make([]*dynbt.Value, 0, len(pc.CustomEffects))
		for i := range pc.CustomEffects {
			eff, err := potionEffectToDisk(pc.CustomEffects[i])
			if err != nil {
				return nbt.RawMessage{}, err
			}
			elems = append(elems, eff)
		}
		root.Set("custom_effects", dynbt.NewList(elems...))
	}
	return marshalDiskValue(root)
}

// potionEffectToDisk maps a wire ItemPotionEffect to the disk MobEffectInstance compound (id string +
// Details), built via dynbt so each Details field omits at its exact vanilla default. CITE:
// MobEffectInstance.CODEC (id + Details.MAP_CODEC).
func potionEffectToDisk(e component.ItemPotionEffect) (*dynbt.Value, error) {
	id := mobEffectName(int32(e.ID))
	if id == "" {
		return nil, fmt.Errorf("save: unknown mob effect id %d", int32(e.ID))
	}
	return detailsToDisk(id, e.Details), nil
}

// detailsToDisk builds a MobEffectInstance compound applying Details.MAP_CODEC's optionalFieldOf
// omission EXACTLY: amplifier omitted at 0 (default), duration omitted at 0, ambient omitted at false,
// show_particles omitted at TRUE (its default — a bool+omitempty cannot express this, hence dynbt),
// show_icon ALWAYS written (getter Optional.of(showIcon())), hidden_effect present only when set. The
// nested hidden_effect reuses the SAME id (Details.STREAM_CODEC carries no id; vanilla writes the
// parent effect id for the nested MobEffectInstance). CITE: Details.MAP_CODEC + Details.create.
func detailsToDisk(id string, d component.ItemEffectDetail) *dynbt.Value {
	c := dynbt.NewCompound()
	c.Set("id", dynbt.NewString(id))
	if int32(d.Amplifier) != 0 { // UNSIGNED_BYTE, default 0
		c.Set("amplifier", dynbt.NewByte(int8(d.Amplifier)))
	}
	if int32(d.Duration) != 0 { // INT, default 0
		c.Set("duration", dynbt.NewInt(int32(d.Duration)))
	}
	if bool(d.Ambient) { // BOOL, default false
		c.Set("ambient", dynbt.NewBoolean(true))
	}
	if !bool(d.ShowParticles) { // BOOL, default TRUE -> written only when false
		c.Set("show_particles", dynbt.NewBoolean(false))
	}
	c.Set("show_icon", dynbt.NewBoolean(bool(d.ShowIcon))) // always written (getter Optional.of)
	if d.HasHidden && d.HiddenEffect != nil {
		c.Set("hidden_effect", detailsToDisk(id, *d.HiddenEffect))
	}
	return c
}

// potionContentsFromValue decodes a disk potion_contents value (bare potion string OR FULL_CODEC
// compound) back into a wire *component.PotionContents.
func potionContentsFromValue(m nbt.RawMessage) (*component.PotionContents, bool) {
	pc := &component.PotionContents{}
	if m.Type == nbt.TagString {
		// withAlternative bare-string form: only the potion Holder.
		var name string
		if err := m.Unmarshal(&name); err != nil {
			return nil, false
		}
		id := potionID(name)
		if id < 0 {
			return nil, false
		}
		pc.PotionID.Has = true
		pc.PotionID.Val = pk.VarInt(id)
		return pc, true
	}
	var disk potionContentsDisk
	if err := m.Unmarshal(&disk); err != nil {
		return nil, false
	}
	if disk.Potion != nil {
		id := potionID(*disk.Potion)
		if id < 0 {
			return nil, false
		}
		pc.PotionID.Has = true
		pc.PotionID.Val = pk.VarInt(id)
	}
	if disk.CustomColor != nil {
		pc.CustomColor.Has = true
		pc.CustomColor.Val = pk.Int(*disk.CustomColor)
	}
	if disk.CustomName != nil {
		pc.CustomName.Has = true
		pc.CustomName.Val = pk.String(*disk.CustomName)
	}
	for i := range disk.CustomEffects {
		eff, ok := diskToPotionEffect(disk.CustomEffects[i])
		if !ok {
			return nil, false
		}
		pc.CustomEffects = append(pc.CustomEffects, eff)
	}
	return pc, true
}

func diskToPotionEffect(d mobEffectDisk) (component.ItemPotionEffect, bool) {
	id := mobEffectID(d.ID)
	if id < 0 {
		return component.ItemPotionEffect{}, false
	}
	details, ok := diskToDetails(d)
	if !ok {
		return component.ItemPotionEffect{}, false
	}
	return component.ItemPotionEffect{ID: pk.VarInt(id), Details: details}, true
}

// diskToDetails applies Details.MAP_CODEC defaults for MISSING keys (amplifier 0, duration 0, ambient
// false, show_particles true, show_icon = show_particles per Details.create's showIcon.orElse). CITE:
// Details.MAP_CODEC + Details.create.
func diskToDetails(d mobEffectDisk) (component.ItemEffectDetail, bool) {
	amplifier := int32(0)
	if d.Amplifier != nil {
		amplifier = int32(*d.Amplifier)
	}
	duration := int32(0)
	if d.Duration != nil {
		duration = *d.Duration
	}
	ambient := false
	if d.Ambient != nil {
		ambient = *d.Ambient
	}
	showParticles := true // optionalFieldOf("show_particles", true)
	if d.ShowParticles != nil {
		showParticles = *d.ShowParticles
	}
	showIcon := showParticles // showIcon.orElse(showParticles)
	if d.ShowIcon != nil {
		showIcon = *d.ShowIcon
	}
	out := component.ItemEffectDetail{
		Amplifier:     pk.VarInt(amplifier),
		Duration:      pk.VarInt(duration),
		Ambient:       pk.Boolean(ambient),
		ShowParticles: pk.Boolean(showParticles),
		ShowIcon:      pk.Boolean(showIcon),
	}
	if d.HiddenEffect != nil {
		hidden, ok := diskToDetails(*d.HiddenEffect)
		if !ok {
			return component.ItemEffectDetail{}, false
		}
		out.HasHidden = true
		out.HiddenEffect = &hidden
	}
	return out, true
}

// ---- potion / mob_effect registry resolvers -----------------------------------------------------
//
// potion and mob_effect are BUILT-IN registries with static protocol-id order (data/registryid), so
// their numeric↔string mapping is available in the save layer (unlike the datapack enchantment
// registry — see file header SEAM). index == protocol id.

func potionName(id int32) string {
	if id >= 0 && int(id) < len(registryid.Potion) {
		return registryid.Potion[id]
	}
	return ""
}

func potionID(name string) int32 {
	for i, n := range registryid.Potion {
		if n == name {
			return int32(i)
		}
	}
	return -1
}

func mobEffectName(id int32) string {
	if id >= 0 && int(id) < len(registryid.MobEffect) {
		return registryid.MobEffect[id]
	}
	return ""
}

func mobEffectID(name string) int32 {
	for i, n := range registryid.MobEffect {
		if n == name {
			return int32(i)
		}
	}
	return -1
}
