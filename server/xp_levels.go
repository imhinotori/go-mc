package server

// xp_levels.go — the level-consume + creative + per-player enchant-seed helpers the ANVIL and
// ENCHANTMENT-TABLE ports need, ported from net.minecraft.world.entity.player.Player. The XP-orb
// pickup (xp_orb.go) already ports giveExperiencePoints; this file adds the LEVEL-consuming siblings
// the anvil/enchant paths call (giveExperienceLevels / onEnchantmentPerformed) plus the creative
// gate and the per-player enchant RandomSource.
//
// 1:1 jar ports (temp/cache/26.2-inner.jar, CFR this session, cited per function).

// playerHasInfiniteMaterials ports Player.hasInfiniteMaterials(): true in creative (abilities
// .instabuild). v1 keys on gameMode == creative, the same test block_break.go uses. CITE
// Player.hasInfiniteMaterials.
func playerHasInfiniteMaterials(p *tickPlayer) bool {
	return p != nil && p.gameMode == gameModeCreative
}

// giveExperienceLevels ports Player.giveExperienceLevels(amount):
//
//	experienceLevel = IntMath.saturatedAdd(experienceLevel, amount);
//	if (experienceLevel < 0) { experienceLevel = 0; experienceProgress = 0; totalExperience = 0; }
//	// (the level-up SOUND branch is a cited no-op — no sound subsystem)
//	setExperienceLevels sync -> ClientboundSetExperience.
//
// The anvil (giveExperienceLevels(-cost)) and any level-consume path draw through this. CITE
// Player.giveExperienceLevels.
func (t *TickLoop) giveExperienceLevels(p *tickPlayer, amount int) {
	p.experienceLevel = saturatedAddInt32(p.experienceLevel, int32(amount))
	if p.experienceLevel < 0 {
		p.experienceLevel = 0
		p.experienceProgress = 0
		p.totalExperience = 0
	}
	// The level-up sound (amount>0 && level%5==0 && ...) is a faithful no-op (no sound layer).
	t.sendExperience(p)
}

// onEnchantmentPerformed ports Player.onEnchantmentPerformed(itemStack, enchantmentCost):
//
//	experienceLevel -= enchantmentCost;
//	if (experienceLevel < 0) { experienceLevel = 0; experienceProgress = 0; totalExperience = 0; }
//	enchantmentSeed = random.nextInt();
//
// Called by the enchant table's clickMenuButton when an enchant is applied (before the currency
// consume). CITE Player.onEnchantmentPerformed.
func (t *TickLoop) onEnchantmentPerformed(p *tickPlayer, enchantmentCost int) {
	p.experienceLevel -= int32(enchantmentCost)
	if p.experienceLevel < 0 {
		p.experienceLevel = 0
		p.experienceProgress = 0
		p.totalExperience = 0
	}
	ensurePlayerEnchantState(p)
	p.enchantmentSeed = p.playerEnchantRandom.nextInt() // enchantmentSeed = random.nextInt()
	t.sendExperience(p)
}

// getEnchantmentSeed ports Player.getEnchantmentSeed(): return enchantmentSeed. Seeds a fresh player
// on first read (vanilla's XpSeed defaults to random.nextInt() when 0 at load).
func getEnchantmentSeed(p *tickPlayer) int {
	ensurePlayerEnchantState(p)
	return int(p.enchantmentSeed)
}

// ensurePlayerEnchantState lazily initializes the player's enchant RandomSource + seed. Vanilla
// builds Player.random as a RandomSource at construction; enchantmentSeed loads from XpSeed (0 ->
// random.nextInt()). v1 seeds playerEnchantRandom deterministically from the player's entity id so
// the offers are reproducible per player (the enchantmentSeed itself is a DataSlot the client reads;
// its exact value only affects which offers appear, and it re-rolls on each enchant). Tick-owned.
func ensurePlayerEnchantState(p *tickPlayer) {
	if p.playerEnchantRandom != nil {
		return
	}
	// A per-player deterministic seed (the entity id folded with a fixed constant) so the RandomSource
	// is reproducible for a given player across a session, matching the XpSeed persistence intent.
	seed := uint64(p.entityID)*0x9E3779B97F4A7C15 + 0x2545F4914F6CDD1D
	p.playerEnchantRandom = newLegacyRandom(int64(seed))
	if p.enchantmentSeed == 0 {
		// XpSeed == 0 -> enchantmentSeed = random.nextInt() (the vanilla load default).
		p.enchantmentSeed = p.playerEnchantRandom.nextInt()
	}
}

// saturatedAddInt32 ports com.google.common.math.IntMath.saturatedAdd(int,int): clamp the sum to the
// int32 range instead of overflowing.
func saturatedAddInt32(a, b int32) int32 {
	sum := int64(a) + int64(b)
	if sum > int64(int32(^uint32(0)>>1)) {
		return int32(^uint32(0) >> 1) // Integer.MAX_VALUE
	}
	if sum < int64(int32(-int64(^uint32(0)>>1)-1)) {
		return int32(-int64(^uint32(0)>>1) - 1) // Integer.MIN_VALUE
	}
	return int32(sum)
}

// setExperienceLevels ports net.minecraft.server.level.ServerPlayer.setExperienceLevels(int level):
//
//	if (level == experienceLevel) return;   // no-op guard
//	experienceLevel = level;
//	lastSentExp = -1;                        // force a resend
//
// v1 has no lastSentExp DataSlot on the player; the sendExperience dirty-send discipline (xp_orb.go)
// plays that role, so the resend is achieved by calling sendExperience after the mutation. Used by the
// /xp set levels path (LEVELS.set BiPredicate == this, then return true). CITE ServerPlayer
// .setExperienceLevels.
func (t *TickLoop) setExperienceLevels(p *tickPlayer, level int) {
	if int32(level) == p.experienceLevel {
		return
	}
	p.experienceLevel = int32(level)
	t.sendExperience(p)
}

// setExperiencePoints ports net.minecraft.server.level.ServerPlayer.setExperiencePoints(int points):
//
//	float f = (float) getXpNeededForNextLevel();
//	float g = (f - 1.0F) / f;
//	float h = Mth.clamp((float) points / f, 0.0F, g);
//	if (h != experienceProgress) { experienceProgress = h; lastSentExp = -1; }
//
// Used by the /xp set points path (POINTS.set BiPredicate: if points >= getXpNeededForNextLevel()
// return false, else setExperiencePoints(points) and return true -- see xpSetType in commands_batch.go).
// The lastSentExp resend is achieved via sendExperience. CITE ServerPlayer.setExperiencePoints.
func (t *TickLoop) setExperiencePoints(p *tickPlayer, points int) {
	f := float32(getXpNeededForNextLevel(p.experienceLevel))
	g := (f - 1.0) / f
	h := mthClampF(float32(points)/f, 0.0, g)
	if h != p.experienceProgress {
		p.experienceProgress = h
		t.sendExperience(p)
	}
}
