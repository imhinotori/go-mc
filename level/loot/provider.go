package loot

// provider.go — the number providers (the only two used by the 5 chest groups:
// the implicit `constant` from a bare float, and `uniform`) plus the Mth helpers
// they call. Every body is a literal port of the decompiled bytecode.
//
// Sources (javap -c, 26.2-inner.jar):
//   - net.minecraft.world.level.storage.loot.providers.number.ConstantValue.getFloat
//   - net.minecraft.world.level.storage.loot.providers.number.NumberProvider.getInt (default)
//   - net.minecraft.world.level.storage.loot.providers.number.UniformGenerator.getInt/getFloat
//   - net.minecraft.util.Mth.nextInt(RandomSource,int,int) / Mth.nextFloat / Mth.floor

import "math"

// ConstantValue mirrors
// net.minecraft.world.level.storage.loot.providers.number.ConstantValue: a fixed
// float. getFloat returns the value verbatim; getInt is the NumberProvider DEFAULT
// (NOT a Mth.floor) — the bytecode shows getInt = Math.round(getFloat(ctx)).
type ConstantValue float32

// GetFloat mirrors ConstantValue.getFloat: `return this.value;`.
//
// Source: javap ConstantValue.getFloat -> getfield value:F; freturn.
func (c ConstantValue) GetFloat(ctx *LootContext) float32 { return float32(c) }

// GetInt mirrors the NumberProvider DEFAULT getInt: `Math.round(this.getFloat(ctx))`.
// java.lang.Math.round(float) = (int)Math.floor(x + 0.5f) (the documented
// half-up-toward-+inf rounding). It is NOT Mth.floor — the decompiled default
// getInt invokes Math.round.
//
// Source: javap NumberProvider.getInt (default) -> invokestatic java/lang/Math.round.
func (c ConstantValue) GetInt(ctx *LootContext) int { return mathRound(float32(c)) }

// Uniform mirrors
// net.minecraft.world.level.storage.loot.providers.number.UniformGenerator: an
// inclusive-both-ends range [min,max]. getInt = Mth.nextInt(rng, min.getInt,
// max.getInt); getFloat = Mth.nextFloat(rng, min.getFloat, max.getFloat).
type Uniform struct {
	Min NumberProvider
	Max NumberProvider
}

// GetInt mirrors UniformGenerator.getInt: `Mth.nextInt(ctx.getRandom(),
// min.getInt(ctx), max.getInt(ctx))`. The min/max are themselves number providers
// (in the chest tables they are constant bare floats).
//
// Source: javap UniformGenerator.getInt -> Mth.nextInt(RandomSource,II).
func (u *Uniform) GetInt(ctx *LootContext) int {
	return mthNextInt(ctx.Random(), u.Min.GetInt(ctx), u.Max.GetInt(ctx))
}

// GetFloat mirrors UniformGenerator.getFloat: `Mth.nextFloat(ctx.getRandom(),
// min.getFloat(ctx), max.getFloat(ctx))`.
//
// Source: javap UniformGenerator.getFloat -> Mth.nextFloat(RandomSource,FF).
func (u *Uniform) GetFloat(ctx *LootContext) float32 {
	return mthNextFloat(ctx.Random(), u.Min.GetFloat(ctx), u.Max.GetFloat(ctx))
}

// mthNextInt mirrors net.minecraft.util.Mth.nextInt(RandomSource, int, int):
//
//	if (min >= max) return min;
//	return min + rng.nextInt(max - min + 1);   // INCLUSIVE on both ends
//
// The bytecode is `if_icmplt 7` (jump to the draw when min < max), else `return
// min` — i.e. the guard is `min >= max -> min`.
//
// Source: javap Mth.nextInt(RandomSource,II).
func mthNextInt(rng interface{ NextIntN(int32) int32 }, min, max int) int {
	if min >= max {
		return min
	}
	return min + int(rng.NextIntN(int32(max-min+1)))
}

// mthNextFloat mirrors net.minecraft.util.Mth.nextFloat(RandomSource, float, float):
//
//	if (min >= max) return min;
//	return min + rng.nextFloat() * (max - min);
//
// Source: javap Mth.nextFloat(RandomSource,FF).
func mthNextFloat(rng interface{ NextFloat() float32 }, min, max float32) float32 {
	if min >= max {
		return min
	}
	return min + rng.NextFloat()*(max-min)
}

// mthFloor mirrors net.minecraft.util.Mth.floor(float): `(int)Math.floor((double)f)`.
//
// Source: javap Mth.floor(F) -> f2d; Math.floor(D); d2i.
func mthFloor(f float32) int { return int(math.Floor(float64(f))) }

// mathRound mirrors java.lang.Math.round(float): `(int)Math.floor(x + 0.5f)`. The
// +0.5f is done in float precision (matching the JDK), then floored to int.
//
// Source: java.lang.Math.round(float) contract (used by NumberProvider.getInt default).
func mathRound(f float32) int { return int(math.Floor(float64(f + 0.5))) }
