package save

import "sync/atomic"

// enchantment_registry.go — the numeric-id ⇄ resource-id resolver for the ENCHANTMENT registry,
// threaded into the save layer so the item-component transcoder (item_components.go) can persist
// minecraft:enchantments / minecraft:stored_enchantments 1:1.
//
// WHY AN INJECTED LIST (not a static data/registryid table):
//
// potion / mob_effect are BUILT-IN registries with a compile-time protocol-id order (data/registryid),
// so their resolvers read a static slice. Enchantment is a DATAPACK registry: its numeric id is the
// ORDER the server sends the registry to the client in Configuration (ClientboundConfigRegistryData),
// which is server/registrydata's embedded content — NOT a static table. Baking a fixed enchantment
// list here would violate the datapack-registry 1:1 mandate. So the ordered list is INJECTED at boot
// (SetEnchantmentRegistry) from registrydata.EnchantmentOrder(), the SAME content the server sends,
// making list[i] the resource id of the enchantment whose wire/protocol id is i (Enchantment.
// STREAM_CODEC = ByteBufCodecs.holderRegistry(Registries.ENCHANTMENT) = a VarInt of the registry index).
//
// Before injection the resolver is empty: enchantmentName/enchantmentID report "not resolvable", and
// the transcoder keeps its counted-and-dropped fallback (item_components.go) — never a fabricated id,
// never a silent corruption. This preserves the DEFERRED behaviour for any code path (or test) that
// runs without a wired registry.

// enchantmentOrder holds the injected ordered ENCHANTMENT resource-id list (index == wire/protocol id).
// It is stored behind an atomic.Pointer so the boot-time injection (main, before the listener accepts)
// is safely visible to any goroutine that later runs a save/load transcode, without a lock on the hot
// path. Nil (never injected) => the resolver reports every id/name as unresolvable.
var enchantmentOrder atomic.Pointer[[]string]

// SetEnchantmentRegistry installs the ordered ENCHANTMENT resource-id list (index == the wire numeric
// id). Called once at server boot from registrydata.EnchantmentOrder(). Passing a nil/empty slice
// leaves the resolver empty (the DEFERRED counted-drop fallback). The slice is defensively copied so a
// later mutation of the caller's slice cannot change the resolver mid-run.
func SetEnchantmentRegistry(order []string) {
	if len(order) == 0 {
		enchantmentOrder.Store(nil)
		return
	}
	cp := make([]string, len(order))
	copy(cp, order)
	enchantmentOrder.Store(&cp)
}

// enchantmentName resolves a wire/protocol enchantment id to its resource id string (e.g. 21 ->
// "minecraft:sharpness"), or "" when the resolver is not injected or the id is out of range. Mirrors
// potionName/mobEffectName but reads the injected datapack-registry order instead of a static table.
func enchantmentName(id int32) string {
	order := enchantmentOrder.Load()
	if order == nil {
		return ""
	}
	if id >= 0 && int(id) < len(*order) {
		return (*order)[id]
	}
	return ""
}

// enchantmentID resolves a resource id string to its wire/protocol id (index in the injected order),
// or -1 when the resolver is not injected or the name is unknown. Inverse of enchantmentName.
func enchantmentID(name string) int32 {
	order := enchantmentOrder.Load()
	if order == nil {
		return -1
	}
	for i, n := range *order {
		if n == name {
			return int32(i)
		}
	}
	return -1
}
