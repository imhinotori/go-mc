package server

// STUB created by 17-01. 17-03 (GAMEPLAY-04) OWNS and OVERWRITES this file with the real
// fall-damage body. The call site (tick_phases.go tickEntities -> t.tickFallDamage()) and the
// tickPlayer fall-damage fields (fallDistance/wasOnGround/lastY, declared in tick.go) are NOT
// touched by 17-03 — 17-03 edits ONLY this file. The reverse-lookup helper 17-03's PvP target
// resolution needs (lookupPlayerByEntityID) is provided by 17-01 in player_visibility.go, so
// 17-03 never edits tick.go either.

// tickFallDamage is the GAMEPLAY-04 fall-damage dispatcher, called from tickEntities each
// tick. STUB: a no-op until 17-03 ports Entity.checkFallDamage / LivingEntity.causeFallDamage
// (accumulate fallDistance while airborne via lastY; on the onGround false->true edge apply
// floor(fallDistance-3) half-hearts through applyDamage, then reset).
func (t *TickLoop) tickFallDamage() {}
