// enchantments.go contains helper types for the Enchantments data component.
package component

import pk "github.com/imhinotori/sulfur/net/packet"

type EnchantmentEntry struct {
	ID    pk.VarInt
	Level pk.VarInt
}
