// suspicioussteweffects.go contains helper types for the SuspiciousStewEffects data component.
package component

import pk "github.com/imhinotori/sulfur/net/packet"

type StewEffect struct {
	Effect   pk.VarInt
	Duration pk.VarInt
}
