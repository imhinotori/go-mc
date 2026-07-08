package server

// schedule.go -- the villager day-cycle ACTIVITY SCHEDULE, ported 1:1 from the unobfuscated 26.2 jar.
// In 26.2 the Schedule class is GONE: the villager schedule is DATA-DRIVEN as a timeline
// (data/minecraft/timeline/villager_schedule.json) sampled through the EnvironmentAttribute system.
// Brain.updateActivityFromSchedule samples the timeline every 20 ticks and, when the sampled Activity
// differs from the active one, calls setActiveActivityIfPossible. Cite Brain + villager_schedule.json.

const schedulePeriodTicks = 24000
const scheduleUpdateDelay int64 = 20

type scheduleKeyframe struct {
	ticks int
	act   activity
}

type schedule struct {
	keyframes []scheduleKeyframe
	period    int
}

// villagerDefaultSchedule ports villager_schedule.json track minecraft:gameplay/villager_activity
// (VERIFIED keyframes): 10->IDLE, 2000->WORK, 9000->MEET, 11000->IDLE, 12000->REST ; period 24000.
var villagerDefaultSchedule = &schedule{
	keyframes: []scheduleKeyframe{
		{ticks: 10, act: activityIdle},
		{ticks: 2000, act: activityWork},
		{ticks: 9000, act: activityMeet},
		{ticks: 11000, act: activityIdle},
		{ticks: 12000, act: activityRest},
	},
	period: schedulePeriodTicks,
}

// villagerBabySchedule ports villager_schedule.json track minecraft:gameplay/baby_villager_activity
// (VERIFIED keyframes): 10->IDLE, 3000->PLAY, 6000->IDLE, 10000->PLAY, 12000->REST ; period 24000.
// PLAY is cite-reduced to IDLE: the villager PLAY package (child social play) is not ported, so a baby
// PLAY window falls back to IDLE. REST is faithful so a baby still sleeps at night. Cite getPlayPackage.
var villagerBabySchedule = &schedule{
	keyframes: []scheduleKeyframe{
		{ticks: 10, act: activityIdle},
		{ticks: 3000, act: activityIdle},
		{ticks: 6000, act: activityIdle},
		{ticks: 10000, act: activityIdle},
		{ticks: 12000, act: activityRest},
	},
	period: schedulePeriodTicks,
}

// sample ports KeyframeTrackSampler.sample(long) for a discrete CONSTANT-easing Activity track: the
// value at loopTicks(t)=floorMod(t,period) is the covering segment fromValue -- the last keyframe whose
// ticks<=looped, wrapping to the LAST keyframe when looped is before the first (the bakeSegments wrap
// segment carries the last keyframe value across the period seam). VERIFIED KeyframeTrackSampler.sample/
// getSegmentAt/loopTicks/bakeSegments + EasingType.CONSTANT (Activity is not lerpable -> lerp=fromValue).
func (s *schedule) sample(t int64) activity {
	if len(s.keyframes) == 0 {
		return activityIdle
	}
	looped := int(floorModI64(t, int64(s.period)))
	result := s.keyframes[len(s.keyframes)-1].act
	for _, kf := range s.keyframes {
		if kf.ticks <= looped {
			result = kf.act
		} else {
			break
		}
	}
	return result
}

func floorModI64(v, m int64) int64 {
	if m == 0 {
		return 0
	}
	r := v % m
	if (r < 0) != (m < 0) && r != 0 {
		r += m
	}
	return r
}

// updateActivityFromSchedule ports Brain.updateActivityFromSchedule: rate-limited to once per 20 ticks;
// samples the schedule (or IDLE when nil) at the day-time; if the result is not already active, activates
// it via setActiveActivityIfPossible. VERIFIED javap Brain.updateActivityFromSchedule (guard
// gameTime-lastScheduleUpdate>20; act=schedule!=null?sample:IDLE; if !contains(act) setActiveActivityIfPossible).
func (b *brain) updateActivityFromSchedule(dayTime int64) {
	if dayTime-b.lastScheduleUpdate <= scheduleUpdateDelay {
		return
	}
	b.lastScheduleUpdate = dayTime
	act := activityIdle
	if b.schedule != nil {
		act = b.schedule.sample(dayTime)
	}
	if !b.activeActivities[act] {
		b.setActiveActivityIfPossible(act)
	}
}

// setActiveActivityIfPossible ports Brain.setActiveActivityIfPossible: if requirements met setActiveActivity,
// else useDefaultActivity(IDLE). VERIFIED javap Brain.setActiveActivityIfPossible.
func (b *brain) setActiveActivityIfPossible(a activity) {
	if b.activityRequirementsAreMet(a) {
		b.setActiveActivity(a)
	} else {
		b.useDefaultActivity()
	}
}

// setSchedule ports Brain.setSchedule(EnvironmentAttribute<Activity>): install the timeline the schedule
// tick samples. VERIFIED javap Villager.registerBrainGoals (VILLAGER_ACTIVITY adult / BABY_VILLAGER_ACTIVITY baby).
func (b *brain) setSchedule(s *schedule) { b.schedule = s }
