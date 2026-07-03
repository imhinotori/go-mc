package registrydata

import "fmt"

// enchantment_order.go exposes the ordered ENCHANTMENT registry resource-id list — the
// numeric-id → resource-id mapping the item-component disk transcoder needs to persist
// minecraft:enchantments / minecraft:stored_enchantments.
//
// WHY THIS IS THE AUTHORITATIVE ORDER (the wire numeric id == this index):
//
// Enchantment is a DATAPACK registry (registry/codec.go: Registry[nbt.RawMessage]
// `registry:"minecraft:enchantment"`). Its numeric protocol id is assigned by the ORDER the
// server sends the registry to the client in the Configuration state
// (ClientboundConfigRegistryData). This server sends exactly the entries Load() produces for
// the "minecraft:enchantment" registry, in Load()'s order (registryDirs slice order for
// registries; entryFiles' alphabetical sort WITHIN each registry). So the index of an entry in
// that ordered list IS the numeric id the client and server exchange on the wire —
// Enchantment.STREAM_CODEC = ByteBufCodecs.holderRegistry(Registries.ENCHANTMENT), a VarInt of
// the holder's registry index (verified 26.2 jar: Enchantment static init).
//
// EnchantmentOrder returns that ordered list so the save layer can resolve wire id → resource id
// (write) and resource id → wire id (load) WITHOUT hardcoding a static enchantment table (which
// would violate the datapack-registry 1:1 mandate — the list is derived from the SAME embedded
// registry content the server actually sends). index i => list[i] is the resource id of the
// enchantment whose wire/protocol id is i.
func EnchantmentOrder() ([]string, error) {
	regs, err := Load()
	if err != nil {
		return nil, fmt.Errorf("registrydata: load for enchantment order: %w", err)
	}
	for _, reg := range regs {
		if reg.ID != "minecraft:enchantment" {
			continue
		}
		order := make([]string, len(reg.Entries))
		for i := range reg.Entries {
			order[i] = reg.Entries[i].Key // "minecraft:<name>" (Load builds the key from the filename)
		}
		return order, nil
	}
	return nil, fmt.Errorf("registrydata: minecraft:enchantment registry not found in embedded content")
}
