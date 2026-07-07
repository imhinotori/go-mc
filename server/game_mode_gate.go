package server

// game_mode_gate.go — the game-mode action restrictions (Fable audit F-G2). A spectator (and, for
// block edits, an adventure player without item build/break permissions) cannot break blocks, place
// blocks, or attack. These are the server-authoritative gates vanilla applies in the packet handlers
// (ServerGamePacketListenerImpl.handleInteract isSpectator early-returns; ServerPlayerGameMode /
// Player.blockActionRestricted for block edits).

// blockActionRestricted ports Player.blockActionRestricted(Level, BlockPos, GameType) for the v1 slice:
//
//	if (!gameType.isBlockPlacingRestricted()) return false;   // SURVIVAL/CREATIVE: never restricted
//	if (gameType == SPECTATOR) return true;                   // spectator: always restricted
//	if (mayBuild()) return false;                             // adventure with build perms
//	... adventure canBreakBlockInAdventureMode(mainHand) check
//
// isBlockPlacingRestricted() is true only for ADVENTURE and SPECTATOR. v1 has no adventure item
// build/break permission components (ItemStack.canBreakBlockInAdventureMode / canPlaceOnBlockInAdventure
// read the CAN_BREAK / CAN_PLACE_ON components, unported), so mayBuild() is false for adventure and the
// adventure branch collapses to "restricted". So the v1-faithful result is: SPECTATOR and ADVENTURE are
// restricted, SURVIVAL and CREATIVE are not. Structured so the adventure item-permission read slots in
// later (the collapse becomes a real component check). Cite Player.blockActionRestricted / GameType
// .isBlockPlacingRestricted.
func blockActionRestricted(p *tickPlayer) bool {
	return p.gameMode == gameModeSpectator || p.gameMode == gameModeAdventure
}

// isSpectatorMode reports whether the player is a spectator — the gate for attacks (a spectator may not
// attack; an adventure player MAY attack, so attacks gate on spectator only, not blockActionRestricted).
// Cite ServerGamePacketListenerImpl.handleInteract (isSpectator early-return before the attack dispatch).
func isSpectatorMode(p *tickPlayer) bool {
	return p.gameMode == gameModeSpectator
}
