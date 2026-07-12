package server

// Environment-attribute / Timeline subsystem: the minimal faithful chain that produces the overworld
// SKY_LIGHT_LEVEL curve, and from it Level.getSkyDarken(). Replaces the former day/night gametime proxy
// (skyDarkenDay = 0 cited constant in light.go) for the sky-darkness value with the real,
// bytecode-verified timeline the vanilla server drives off its WorldClock.
//
// VANILLA CHAIN (verified via javap -c -p over temp/cache/26.2-inner.jar):
//
//	Level.updateSkyBrightness(): skyDarken = (int)(15.0f - getDimensionValue(SKY_LIGHT_LEVEL));
//	SKY_LIGHT_LEVEL: FLOAT, defaultValue 15.0f, valueRange [0,15], notPositional, syncable.
//	The overworld dimension timelines HolderSet (tag in_overworld -> day) supplies the day timeline
//	(data/minecraft/timeline/day.json) with the gameplay/sky_light_level track:
//	  clock minecraft:overworld (getTotalTicks == day-time tick); period_ticks 24000; modifier multiply
//	  (FloatModifier.MULTIPLY: base*arg); ease unspecified -> LINEAR;
//	  keyframes [ {133,1.0},{11867,1.0},{13670,0.26666668},{22330,0.26666668} ].
//	Layered ValueSampler: base 15.0f, constant dimension layer (no SKY_LIGHT_LEVEL override), then the
//	timeline TimeBased layer MULTIPLY(15.0f, trackSample(dayTime)); sanitizeValue clamps to [0,15].
//
//	KeyframeTrackSampler.sample(ticks): looped=floorMod(ticks,period); pick first segment with
//	looped<toTicks (else last); if looped<=fromTicks return fromValue; if looped>=toTicks return toValue;
//	else t=(float)(looped-fromTicks)/(toTicks-fromTicks); LINEAR apply(t)==t; return from+t*(to-from).
//	bakeSegments (period present): leading wrap [last.ticks-period -> first.ticks] (lastVal->firstVal),
//	interior segments between consecutive keyframes, trailing wrap [last.ticks -> first.ticks+period]
//	(lastVal->firstVal); makes the track continuous across the period boundary.
//
// CITE: net.minecraft.world.level.Level.updateSkyBrightness / getSkyDarken;
// net.minecraft.world.attribute.EnvironmentAttributes.SKY_LIGHT_LEVEL;
// net.minecraft.world.attribute.EnvironmentAttributeSystem.getDimensionValue + ValueSampler;
// net.minecraft.world.timeline.Timeline / AttributeTrackSampler; net.minecraft.util.KeyframeTrackSampler;
// net.minecraft.util.EasingType.LINEAR; net.minecraft.world.attribute.modifier.FloatModifier.MULTIPLY;
// data/minecraft/timeline/day.json; data/minecraft/tags/timeline/in_overworld.json.

// skyLightLevelKeyframe is one (ticks,value) pair of a KeyframeTrack.
type skyLightLevelKeyframe struct {
	ticks int
	value float32
}

// overworldSkyLightLevelKeyframes is the exact gameplay/sky_light_level track from
// data/minecraft/timeline/day.json, in keyframe order. Values are the LITERAL floats in the JSON
// (0.26666668 is the exact 32-bit float Mojang shipped, == 4.0f/15.0f rounded to float32).
var overworldSkyLightLevelKeyframes = []skyLightLevelKeyframe{
	{ticks: 133, value: 1.0},
	{ticks: 11867, value: 1.0},
	{ticks: 13670, value: 0.26666668},
	{ticks: 22330, value: 0.26666668},
}

const skyLightLevelPeriodTicks = 24000 // day.json period_ticks

// floatSegment is one baked KeyframeTrackSampler segment (LINEAR easing throughout this track).
type floatSegment struct {
	fromValue float32
	fromTicks int
	toValue   float32
	toTicks   int
}

// bakedOverworldSkyLightSegments is the segment list KeyframeTrackSampler bakes for the overworld
// sky_light_level track (period present, LINEAR easing), computed once at package init from the
// keyframes, mirroring KeyframeTrackSampler.bakeSegments. See sampleSkyLightLevelTrack for the sample.
var bakedOverworldSkyLightSegments = bakeSkyLightSegments(overworldSkyLightLevelKeyframes, skyLightLevelPeriodTicks)

// bakeSkyLightSegments ports KeyframeTrackSampler.bakeSegments for the period-present, multi-keyframe
// case (the only case this track hits). Leading + trailing wrap segments make the track continuous
// across the period boundary. CITE: KeyframeTrackSampler.bakeSegments / addSegmentsFromKeyframes.
func bakeSkyLightSegments(kf []skyLightLevelKeyframe, period int) []floatSegment {
	first := kf[0]
	last := kf[len(kf)-1]
	segs := make([]floatSegment, 0, len(kf)+1)
	// Leading wrap: Segment(last, last.ticks-period, first, first.ticks).
	segs = append(segs, floatSegment{fromValue: last.value, fromTicks: last.ticks - period, toValue: first.value, toTicks: first.ticks})
	// Interior segments between consecutive keyframes.
	for i := 0; i < len(kf)-1; i++ {
		segs = append(segs, floatSegment{fromValue: kf[i].value, fromTicks: kf[i].ticks, toValue: kf[i+1].value, toTicks: kf[i+1].ticks})
	}
	// Trailing wrap: Segment(last, last.ticks, first, first.ticks+period).
	segs = append(segs, floatSegment{fromValue: last.value, fromTicks: last.ticks, toValue: first.value, toTicks: first.ticks + period})
	return segs
}

// sampleSkyLightLevelTrack ports KeyframeTrackSampler.sample(long) for the overworld sky_light_level
// track: floorMod the ticks into [0,period), find the first segment whose looped < toTicks (else last),
// then edge-return or LINEAR-lerp. CITE: KeyframeTrackSampler.sample / getSegmentAt / loopTicks;
// EasingType.LINEAR (apply(t)==t); LerpFunction float (from + t*(to-from)).
func sampleSkyLightLevelTrack(ticks int64) float32 {
	// loopTicks: Math.floorMod(ticks, period). Go modulo differs for negatives, so replicate floorMod.
	period := int64(skyLightLevelPeriodTicks)
	looped := ticks % period
	if looped < 0 {
		looped += period
	}
	// getSegmentAt: first segment with looped < toTicks, else the last segment.
	seg := bakedOverworldSkyLightSegments[len(bakedOverworldSkyLightSegments)-1]
	for _, s := range bakedOverworldSkyLightSegments {
		if looped < int64(s.toTicks) {
			seg = s
			break
		}
	}
	if looped <= int64(seg.fromTicks) {
		return seg.fromValue
	}
	if looped >= int64(seg.toTicks) {
		return seg.toValue
	}
	// t = (float)(looped - fromTicks) / (toTicks - fromTicks); LINEAR easing: e == t.
	tf := float32(looped-int64(seg.fromTicks)) / float32(seg.toTicks-seg.fromTicks)
	// LerpFunction (Float): from + t*(to-from).
	return seg.fromValue + tf*(seg.toValue-seg.fromValue)
}

const (
	// skyLightLevelDefault is SKY_LIGHT_LEVEL defaultValue (15.0f): the attribute base the layered
	// ValueSampler starts from and the timeline MULTIPLY layer scales.
	skyLightLevelDefault = float32(15.0)
	// skyLightLevelMin / skyLightLevelMax are the attribute valueRange [0,15] the sanitizeValue clamp
	// enforces (AttributeRange.ofFloat(0.0f, 15.0f)).
	skyLightLevelMin = float32(0.0)
	skyLightLevelMax = float32(15.0)
)

// dimensionSkyLightLevel ports getDimensionValue(SKY_LIGHT_LEVEL) for the overworld at a given day-time
// tick: base 15.0f, MULTIPLY by the timeline track sample, then clamp to [0,15] (sanitizeValue). CITE:
// EnvironmentAttributeSystem.getDimensionValue + ValueSampler (Constant->TimeBased layer order);
// FloatModifier.MULTIPLY; EnvironmentAttribute.sanitizeValue.
func dimensionSkyLightLevel(dayTime int64) float32 {
	v := skyLightLevelDefault * sampleSkyLightLevelTrack(dayTime)
	if v < skyLightLevelMin {
		v = skyLightLevelMin
	}
	if v > skyLightLevelMax {
		v = skyLightLevelMax
	}
	return v
}

// getSkyDarken ports Level.updateSkyBrightness/getSkyDarken(): skyDarken = (int)(15.0f -
// SKY_LIGHT_LEVEL). Deterministic from the day-time tick, so this reads t.gametime on demand (which
// equals the overworld WorldClock getTotalTicks in this server: dayTime is derived as gametime, and
// setDayTime rewrites gametime) instead of caching a per-tick field. Provably-identical optimization of
// vanilla updateSkyBrightness-then-read-field: skyDarken depends only on dayTime, stable within a tick,
// so an on-demand compute yields the exact value updateSkyBrightness would have cached. CITE:
// net.minecraft.world.level.Level.updateSkyBrightness / getSkyDarken.
func (t *TickLoop) getSkyDarken() int {
	return int(15.0 - dimensionSkyLightLevel(t.gametime))
}
