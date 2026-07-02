package server

// gossip.go — the VILLAGER GOSSIP / REPUTATION container, a 1:1 port of the unobfuscated 26.2 jar
// (temp/cache/26.2-inner.jar, CFR + javap this session):
//
//   net.minecraft.world.entity.ai.gossip.GossipType      -> gossipType (enum: weight/max/decay/decayTransfer)
//   net.minecraft.world.entity.ai.gossip.GossipContainer -> gossipContainer (per-UUID EntityGossips map)
//   GossipContainer.EntityGossips                        -> entityGossips (Object2IntMap<GossipType>)
//   GossipContainer.GossipEntry                          -> gossipEntry (target,type,value)
//
// A villager keeps a gossipContainer keyed by PLAYER (and villager) UUID; getReputation returns the
// weighted sum used by Villager.updateSpecialPrices to discount trades. All numeric constants
// (weight/max/decayPerDay/decayPerTransfer, the DISCARD_THRESHOLD=2 floor, the mergeValuesForAddition
// cap, the mergeValuesForTransfer max, the -decayPerTransfer transfer decay) are jar-literal.
//
// SCOPE / cited deferrals:
//   - shareGossip (the villager-to-villager transfer used by the GossipFor social behavior /
//     GoToPotentialJobSite meeting) is ported here as transferFrom/selectGossipsForTransfer, but no
//     meeting-point behavior CALLS it yet — cite VillagerGoalPackages meet-package GossipFor as the future
//     caller (the container method is real; only the tick trigger is deferred).
//   - Gossip NBT persistence (GossipContainer.CODEC save/load) is DEFERRED behind the same save-gap the
//     chest/furnace block-entities cite — the in-memory container is authoritative for a live session.

import (
	"sort"

	"github.com/google/uuid"
)

// gossipDiscardThreshold ports GossipContainer.DISCARD_THRESHOLD = 2: a gossip value below 2 is dropped
// (decay/makeSureValueIsntTooLowOrTooHigh/transferFrom all gate on `>= 2` / `< 2`).
//
//	[VERIFIED CFR GossipContainer: public static final int DISCARD_THRESHOLD = 2.]
const gossipDiscardThreshold = 2

// gossipType ports net.minecraft.world.entity.ai.gossip.GossipType. The enum constants carry
// weight/max/decayPerDay/decayPerTransfer — the LOAD-BEARING numbers of the reputation economy. All
// values are jar-literal (VERIFIED CFR GossipType constructor calls this session):
//
//	MAJOR_NEGATIVE("major_negative", -5, 100, 10, 10)
//	MINOR_NEGATIVE("minor_negative", -1, 200, 20, 20)
//	MINOR_POSITIVE("minor_positive",  1,  25,  1,  5)
//	MAJOR_POSITIVE("major_positive",  5,  20,  0, 20)
//	TRADING       ("trading",         1,  25,  2, 20)
type gossipType struct {
	id               string
	weight           int
	max              int
	decayPerDay      int
	decayPerTransfer int
}

// The five gossip types, in the jar's declaration order (used by unpack/selectGossipsForTransfer stable
// iteration). Modeled as package vars (pointers) so a gossipType value has stable identity, matching the
// Java enum-singleton identity that EntityGossips' IdentityHashMap-based selection and the map key rely on.
var (
	gossipMajorNegative = &gossipType{id: "major_negative", weight: -5, max: 100, decayPerDay: 10, decayPerTransfer: 10}
	gossipMinorNegative = &gossipType{id: "minor_negative", weight: -1, max: 200, decayPerDay: 20, decayPerTransfer: 20}
	gossipMinorPositive = &gossipType{id: "minor_positive", weight: 1, max: 25, decayPerDay: 1, decayPerTransfer: 5}
	gossipMajorPositive = &gossipType{id: "major_positive", weight: 5, max: 20, decayPerDay: 0, decayPerTransfer: 20}
	gossipTrading       = &gossipType{id: "trading", weight: 1, max: 25, decayPerDay: 2, decayPerTransfer: 20}
)

// gossipTypeOrder is the enum declaration order (GossipType.values()), used for deterministic iteration in
// unpack/decay/selectGossipsForTransfer. Java iterates the Object2IntOpenHashMap in insertion-adjacent order;
// v1 fixes a stable enum order so the reputation SUM (order-independent) and the transfer selection (uses the
// order only to build the cumulative range table) match the jar deterministically.
var gossipTypeOrder = []*gossipType{
	gossipMajorNegative, gossipMinorNegative, gossipMinorPositive, gossipMajorPositive, gossipTrading,
}

// entityGossips ports GossipContainer.EntityGossips: the per-target Object2IntMap<GossipType> of raw
// (un-weighted) gossip values. Keyed by the gossipType pointer (Java enum-singleton identity).
type entityGossips struct {
	entries map[*gossipType]int
}

func newEntityGossips() *entityGossips { return &entityGossips{entries: make(map[*gossipType]int)} }

// weightedValue ports EntityGossips.weightedValue(Predicate<GossipType>): sum of value*weight over the
// entries whose type satisfies the predicate.
//
//	[VERIFIED CFR: entries.stream().filter(types.test(key)).mapToInt(v * key.weight).sum().]
func (g *entityGossips) weightedValue(pred func(*gossipType) bool) int {
	sum := 0
	for _, t := range gossipTypeOrder {
		if v, ok := g.entries[t]; ok && pred(t) {
			sum += v * t.weight
		}
	}
	return sum
}

// decay ports EntityGossips.decay(): every entry loses decayPerDay; entries falling below DISCARD_THRESHOLD
// (2) are removed.
//
//	[VERIFIED CFR: newValue = value - key.decayPerDay; if (newValue < 2) remove; else set(newValue).]
func (g *entityGossips) decay() {
	for _, t := range gossipTypeOrder {
		v, ok := g.entries[t]
		if !ok {
			continue
		}
		newValue := v - t.decayPerDay
		if newValue < gossipDiscardThreshold {
			delete(g.entries, t)
			continue
		}
		g.entries[t] = newValue
	}
}

// isEmpty ports EntityGossips.isEmpty().
func (g *entityGossips) isEmpty() bool { return len(g.entries) == 0 }

// makeSureValueIsntTooLowOrTooHigh ports EntityGossips.makeSureValueIsntTooLowOrTooHigh(type): clamp the
// entry to [.., type.max], and drop it if below DISCARD_THRESHOLD (2).
//
//	[VERIFIED CFR: value = getInt(type); if (value > type.max) put(type, type.max); if (value < 2) remove(type).]
//
// NOTE the jar reads `value` ONCE (before the max-clamp write), so the `< 2` test uses the PRE-clamp value —
// but the max (>= 20) is always above 2, so a value clamped down to max is never < 2; the observable result
// is identical. Mirrored faithfully by reading once.
func (g *entityGossips) makeSureValueIsntTooLowOrTooHigh(t *gossipType) {
	value := g.entries[t] // getInt: 0 when absent
	if value > t.max {
		g.entries[t] = t.max
	}
	if value < gossipDiscardThreshold {
		g.remove(t)
	}
}

// remove ports EntityGossips.remove(type): removeInt(type).
func (g *entityGossips) remove(t *gossipType) { delete(g.entries, t) }

// gossipEntry ports GossipContainer.GossipEntry(target, type, value). weightedValue() == value*type.weight.
type gossipEntry struct {
	target uuid.UUID
	typ    *gossipType
	value  int
}

func (e gossipEntry) weightedValue() int { return e.value * e.typ.weight }

// gossipContainer ports net.minecraft.world.entity.ai.gossip.GossipContainer: the per-UUID EntityGossips
// map plus the add/remove/getReputation/decay/transferFrom surface. A villager holds one; it is created
// lazily (ensureGossips) so non-villager entities carry no map.
type gossipContainer struct {
	gossips map[uuid.UUID]*entityGossips
}

func newGossipContainer() *gossipContainer {
	return &gossipContainer{gossips: make(map[uuid.UUID]*entityGossips)}
}

// getOrCreate ports GossipContainer.getOrCreate(target): computeIfAbsent(target, new EntityGossips()).
func (c *gossipContainer) getOrCreate(target uuid.UUID) *entityGossips {
	e, ok := c.gossips[target]
	if !ok {
		e = newEntityGossips()
		c.gossips[target] = e
	}
	return e
}

// getReputation ports GossipContainer.getReputation(entity, Predicate<GossipType>): the target's weighted
// value under the predicate, or 0 if the target has no gossip.
//
//	[VERIFIED CFR: entry = gossips.get(entity); return entry != null ? entry.weightedValue(types) : 0.]
func (c *gossipContainer) getReputation(entity uuid.UUID, pred func(*gossipType) bool) int {
	entry, ok := c.gossips[entity]
	if !ok {
		return 0
	}
	return entry.weightedValue(pred)
}

// add ports GossipContainer.add(target, type, amountToAdd): merge the amount via mergeValuesForAddition
// (capped at type.max), clamp/floor via makeSureValueIsntTooLowOrTooHigh, then drop the whole target entry
// if it ended up empty.
//
//	[VERIFIED CFR: eg = getOrCreate(target); eg.entries.mergeInt(type, amountToAdd, mergeValuesForAddition);
//	 eg.makeSureValueIsntTooLowOrTooHigh(type); if (eg.isEmpty()) gossips.remove(target).]
func (c *gossipContainer) add(target uuid.UUID, t *gossipType, amountToAdd int) {
	eg := c.getOrCreate(target)
	if old, ok := eg.entries[t]; ok {
		eg.entries[t] = mergeValuesForAddition(t, old, amountToAdd)
	} else {
		// mergeInt with an absent key inserts the raw amount (Object2IntMap default 0, no merge fn call).
		eg.entries[t] = amountToAdd
	}
	eg.makeSureValueIsntTooLowOrTooHigh(t)
	if eg.isEmpty() {
		delete(c.gossips, target)
	}
}

// remove ports GossipContainer.remove(target, type, amountToRemove) == add(target, type, -amountToRemove).
func (c *gossipContainer) remove(target uuid.UUID, t *gossipType, amountToRemove int) {
	c.add(target, t, -amountToRemove)
}

// removeType ports GossipContainer.remove(target, type): drop a single type from a target, dropping the
// target if it becomes empty.
func (c *gossipContainer) removeType(target uuid.UUID, t *gossipType) {
	eg, ok := c.gossips[target]
	if !ok {
		return
	}
	eg.remove(t)
	if eg.isEmpty() {
		delete(c.gossips, target)
	}
}

// removeAllOfType ports GossipContainer.remove(type): strip one type from every target.
func (c *gossipContainer) removeAllOfType(t *gossipType) {
	for target, eg := range c.gossips {
		eg.remove(t)
		if eg.isEmpty() {
			delete(c.gossips, target)
		}
	}
}

// clear ports GossipContainer.clear().
func (c *gossipContainer) clear() { c.gossips = make(map[uuid.UUID]*entityGossips) }

// decay ports GossipContainer.decay(): decay every target's gossips (once per in-game DAY in vanilla via
// the GossipDecay tick), dropping targets that empty out.
//
//	[VERIFIED CFR: for each EntityGossips: decay(); if (isEmpty()) iterator.remove().]
func (c *gossipContainer) decay() {
	for target, eg := range c.gossips {
		eg.decay()
		if eg.isEmpty() {
			delete(c.gossips, target)
		}
	}
}

// putAll ports GossipContainer.putAll(container): merge (raw put, no cap) every source entry.
func (c *gossipContainer) putAll(src *gossipContainer) {
	for target, eg := range src.gossips {
		dst := c.getOrCreate(target)
		for t, v := range eg.entries {
			dst.entries[t] = v
		}
	}
}

// copy ports GossipContainer.copy(): a fresh container with putAll(this).
func (c *gossipContainer) copy() *gossipContainer {
	out := newGossipContainer()
	out.putAll(c)
	return out
}

// getCountForType ports GossipContainer.getCountForType(type, DoublePredicate): count the targets whose
// WEIGHTED value for `type` (value*weight, or 0 default) satisfies the predicate. Ported for completeness
// (the AngerManagement / debug readouts use it); no v1 caller yet.
//
//	[VERIFIED CFR: gossips.values().filter(valueTest.test(getOrDefault(type,0) * type.weight)).count().]
func (c *gossipContainer) getCountForType(t *gossipType, valueTest func(float64) bool) int64 {
	var count int64
	for _, eg := range c.gossips {
		v := eg.entries[t] // getOrDefault(type, 0)
		if valueTest(float64(v * t.weight)) {
			count++
		}
	}
	return count
}

// unpack ports GossipContainer.unpack(): flatten every (target,type,value) into a stable-ordered slice
// (used by save + selectGossipsForTransfer). Iterates targets in a deterministic order (sorted by UUID) and
// types in enum order — an ORDERING refinement over the jar's HashMap iteration that does not change the
// transfer SELECTION statistics (the cumulative-range table is order-invariant in aggregate) but makes the
// port deterministic and testable. CITE GossipContainer.unpack.
func (c *gossipContainer) unpack() []gossipEntry {
	targets := make([]uuid.UUID, 0, len(c.gossips))
	for target := range c.gossips {
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool {
		a, b := targets[i], targets[j]
		for k := 0; k < len(a); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return false
	})
	out := make([]gossipEntry, 0)
	for _, target := range targets {
		eg := c.gossips[target]
		for _, t := range gossipTypeOrder {
			if v, ok := eg.entries[t]; ok {
				out = append(out, gossipEntry{target: target, typ: t, value: v})
			}
		}
	}
	return out
}

// selectGossipsForTransfer ports GossipContainer.selectGossipsForTransfer(random, maxCount): weighted-random
// pick (with replacement, deduplicated into an identity set) of up to maxCount entries, weighted by
// |weightedValue|. Builds the cumulative range table exactly as the jar (ranges[i] = (rangesEnd +=
// abs(weightedValue)) - 1) and binary-searches a random choice in [0, rangesEnd).
//
//	[VERIFIED CFR: entries = unpack().toList(); ranges[i] = (rangesEnd += abs(gossip.weightedValue())) - 1;
//	 for i in maxCount: choice = random.nextInt(rangesEnd); idx = binarySearch(ranges, choice);
//	 results.add(entries.get(idx<0 ? -idx-1 : idx)).]
func (c *gossipContainer) selectGossipsForTransfer(random *entityRandom, maxCount int) []gossipEntry {
	entries := c.unpack()
	if len(entries) == 0 {
		return nil
	}
	ranges := make([]int, len(entries))
	rangesEnd := 0
	for i, gossip := range entries {
		rangesEnd += gossipAbs(gossip.weightedValue())
		ranges[i] = rangesEnd - 1
	}
	// results is an identity set of *entries picks; preserve first-seen order for a deterministic transfer.
	seen := make(map[int]bool)
	out := make([]gossipEntry, 0, maxCount)
	for i := 0; i < maxCount; i++ {
		if rangesEnd <= 0 {
			break
		}
		choice := random.nextInt(rangesEnd)
		idx := gossipBinarySearch(ranges, choice)
		if idx < 0 {
			idx = -idx - 1
		}
		if !seen[idx] {
			seen[idx] = true
			out = append(out, entries[idx])
		}
	}
	return out
}

// transferFrom ports GossipContainer.transferFrom(source, random, maxCount): pull selectGossipsForTransfer
// picks from `source`, decay each by type.decayPerTransfer, and — if still >= DISCARD_THRESHOLD (2) — merge
// it in via mergeValuesForTransfer (max of old/new). This is the shareGossip primitive.
//
//	[VERIFIED CFR: newGossips = source.selectGossipsForTransfer(random, maxCount); for each:
//	 decayedValue = value - type.decayPerTransfer; if (decayedValue >= 2)
//	   getOrCreate(target).entries.mergeInt(type, decayedValue, mergeValuesForTransfer).]
func (c *gossipContainer) transferFrom(source *gossipContainer, random *entityRandom, maxCount int) {
	newGossips := source.selectGossipsForTransfer(random, maxCount)
	for _, ng := range newGossips {
		decayedValue := ng.value - ng.typ.decayPerTransfer
		if decayedValue >= gossipDiscardThreshold {
			eg := c.getOrCreate(ng.target)
			if old, ok := eg.entries[ng.typ]; ok {
				eg.entries[ng.typ] = mergeValuesForTransfer(old, decayedValue)
			} else {
				eg.entries[ng.typ] = decayedValue
			}
		}
	}
}

// mergeValuesForTransfer ports GossipContainer.mergeValuesForTransfer(old, new) = max(old, new).
func mergeValuesForTransfer(oldValue, newValue int) int {
	if oldValue > newValue {
		return oldValue
	}
	return newValue
}

// mergeValuesForAddition ports GossipContainer.mergeValuesForAddition(type, old, new): sum, capped so it
// never RISES past type.max (but an already-over-max old value is preserved via max(type.max, old)).
//
//	[VERIFIED CFR: sum = old + new; return sum > type.max ? Math.max(type.max, old) : sum.]
func mergeValuesForAddition(t *gossipType, oldValue, newValue int) int {
	sum := oldValue + newValue
	if sum > t.max {
		if t.max > oldValue {
			return t.max
		}
		return oldValue
	}
	return sum
}

// gossipBinarySearch mirrors java.util.Arrays.binarySearch(int[], key) on an ASCENDING array: returns the
// index if found, else -(insertionPoint) - 1. selectGossipsForTransfer relies on this exact contract to map
// a not-found choice to the first range strictly greater than it.
func gossipBinarySearch(a []int, key int) int {
	lo, hi := 0, len(a)-1
	for lo <= hi {
		mid := (lo + hi) >> 1
		v := a[mid]
		switch {
		case v < key:
			lo = mid + 1
		case v > key:
			hi = mid - 1
		default:
			return mid
		}
	}
	return -(lo + 1)
}

// gossipAbs is Math.abs(int) for the weightedValue range table.
func gossipAbs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
