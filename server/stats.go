package server

// stats.go - the STATISTICS system ported 1:1 from the unobfuscated 26.2 inner
// jar (net.minecraft.stats.Stats / Stat / StatType / StatsCounter /
// ServerStatsCounter). It gives each player a StatsCounter (increment + read),
// persists it as world/stats/<uuid>.json in the vanilla format, and answers a
// ServerboundClientCommand(request_stats) with ClientboundAwardStats.
//
// WIRE (VERIFIED javap ClientboundAwardStatsPacket STREAM_CODEC static +
// net.minecraft.stats.Stat.STREAM_CODEC static):
//
//	ClientboundAwardStats = ByteBufCodecs.map(VAR_INT_countPrefix, Stat.STREAM_CODEC, VAR_INT):
//	    VarInt count; then per entry: Stat (key) + VarInt (the count value).
//	Stat.STREAM_CODEC = ByteBufCodecs.registry(STAT_TYPE).dispatch(...):
//	    VarInt statTypeRegistryId; then the type streamCodec writes
//	    VarInt valueRegistryId (the value id within THAT stat type backing registry).
//
// So each stat on the wire is TWO VarInts (statType id, value id) + the count VarInt.
//
// SAVE FORMAT (VERIFIED javap ServerStatsCounter.toJson + STATS_CODEC):
//	{"stats": {"<statTypeId>": {"<valueId>": count, ...}, ...}, "DataVersion": N}
// grouped by stat type, mapping the full value id -> count.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/data/registryid"
	pk "github.com/imhinotori/sulfur/net/packet"

	"github.com/google/uuid"
)

// StatType is the index into registryid.StatType (0=mined .. 8=custom). It is the
// FIRST VarInt of a Stat on the wire.
//
//	[VERIFIED registryid.StatType order == Stats static init: mined, crafted, used,
//	 broken, picked_up, dropped, killed, killed_by, custom.]
type StatType int32

const (
	StatTypeMined    StatType = 0 // BLOCK_MINED (value id = Block registry id)
	StatTypeCrafted  StatType = 1 // ITEM_CRAFTED (value id = Item registry id)
	StatTypeUsed     StatType = 2 // ITEM_USED
	StatTypeBroken   StatType = 3 // ITEM_BROKEN
	StatTypePickedUp StatType = 4 // ITEM_PICKED_UP
	StatTypeDropped  StatType = 5 // ITEM_DROPPED
	StatTypeKilled   StatType = 6 // ENTITY_KILLED (value id = EntityType registry id)
	StatTypeKilledBy StatType = 7 // ENTITY_KILLED_BY
	StatTypeCustom   StatType = 8 // CUSTOM (value id = CustomStat registry id)
)

// statKey is one net.minecraft.stats.Stat: a stat type + the value registry id
// WITHIN that type backing registry. It is the map key of a StatsCounter (the
// Object2IntMap<Stat<?>>). Comparable so it can be a Go map key.
type statKey struct {
	typeID  StatType
	valueID int32
}

// StatsCounter is net.minecraft.stats.StatsCounter: the per-player
// Object2IntMap<Stat<?>>. Tick-owned (mutated only on the tick goroutine, like the
// inventory); the save snapshot is taken on the owner then handed off-tick.
type StatsCounter struct {
	stats map[statKey]int32
}

func newStatsCounter() *StatsCounter { return &StatsCounter{stats: make(map[statKey]int32)} }

// increment is StatsCounter.increment: setValue(getValue+amount).
//	[VERIFIED javap StatsCounter.increment: setValue(player, stat, getValue(stat)+amount).]
func (c *StatsCounter) increment(key statKey, amount int32) { c.setValue(key, c.getValue(key)+amount) }

// setValue is StatsCounter.setValue: stats.put(stat, value).
func (c *StatsCounter) setValue(key statKey, value int32) { c.stats[key] = value }

// getValue is StatsCounter.getValue: stats.getInt(stat) (0 default).
func (c *StatsCounter) getValue(key statKey) int32 { return c.stats[key] }

// incrementCustom increments a CUSTOM stat by its CustomStat registry id name
// (e.g. "minecraft:mob_kills"). An unknown name is a silent no-op.
func (c *StatsCounter) incrementCustom(name string, amount int32) {
	id := indexOf(registryid.CustomStat, name)
	if id < 0 {
		return
	}
	c.increment(statKey{typeID: StatTypeCustom, valueID: id}, amount)
}

// indexOf returns the registry index of name in a registry slice, or -1.
func indexOf(reg []string, name string) int32 {
	for i, n := range reg {
		if n == name {
			return int32(i)
		}
	}
	return -1
}

// backingRegistry returns the registry a stat type value id indexes into.
func backingRegistry(t StatType) []string {
	switch t {
	case StatTypeMined:
		return registryid.Block
	case StatTypeKilled, StatTypeKilledBy:
		return registryid.EntityType
	case StatTypeCustom:
		return registryid.CustomStat
	default: // crafted/used/broken/picked_up/dropped are Item-valued
		return registryid.Item
	}
}

// encodeAwardStats builds ClientboundAwardStats: VarInt count, then per entry
// VarInt(statTypeId) VarInt(valueId) VarInt(count).
func encodeAwardStats(c *StatsCounter) pk.Packet {
	fields := make([]pk.FieldEncoder, 0, len(c.stats)*3+1)
	fields = append(fields, pk.VarInt(int32(len(c.stats))))
	for k, v := range c.stats {
		fields = append(fields,
			pk.VarInt(int32(k.typeID)),
			pk.VarInt(k.valueID),
			pk.VarInt(v),
		)
	}
	return pk.Marshal(int32(packetid.ClientboundAwardStats), fields...)
}

// --- SAVE / LOAD (world/stats/<uuid>.json, the vanilla ServerStatsCounter format) ---

const statsDir = "stats"

type statsFileJSON struct {
	Stats       map[string]map[string]int32 `json:"stats"`
	DataVersion int32                        `json:"DataVersion,omitempty"`
}

// toJSON serializes to the vanilla format (grouped by stat type, value name -> count).
//	[VERIFIED javap ServerStatsCounter.toJson: {"stats": STATS_CODEC, "DataVersion": N}.]
func (c *StatsCounter) toJSON() ([]byte, error) {
	grouped := make(map[string]map[string]int32)
	for k, v := range c.stats {
		typeName := registryid.StatType[int(k.typeID)]
		reg := backingRegistry(k.typeID)
		if int(k.valueID) < 0 || int(k.valueID) >= len(reg) {
			continue
		}
		valueName := reg[k.valueID]
		m := grouped[typeName]
		if m == nil {
			m = make(map[string]int32)
			grouped[typeName] = m
		}
		m[valueName] = v
	}
	return json.Marshal(statsFileJSON{Stats: grouped})
}

// parseStatsJSON is ServerStatsCounter.parse: resolve each type/value NAME to its
// registry id. Unknown names are silently dropped (never a load crash).
func parseStatsJSON(b []byte) *StatsCounter {
	c := newStatsCounter()
	if len(b) == 0 {
		return c
	}
	var f statsFileJSON
	if err := json.Unmarshal(b, &f); err != nil {
		return c
	}
	for typeName, values := range f.Stats {
		tid := indexOf(registryid.StatType, typeName)
		if tid < 0 {
			continue
		}
		reg := backingRegistry(StatType(tid))
		for valueName, count := range values {
			vid := indexOf(reg, valueName)
			if vid < 0 {
				continue
			}
			c.stats[statKey{typeID: StatType(tid), valueID: vid}] = count
		}
	}
	return c
}

// saveStats writes world/stats/<uuid>.json (off-tick, over a snapshot).
func saveStats(dir string, id uuid.UUID, c *StatsCounter) error {
	b, err := c.toJSON()
	if err != nil {
		return fmt.Errorf("stats: encode %s: %w", id, err)
	}
	sDir := filepath.Join(dir, statsDir)
	if err := os.MkdirAll(sDir, 0o755); err != nil {
		return fmt.Errorf("stats: mkdir %s: %w", sDir, err)
	}
	path := filepath.Join(sDir, id.String()+".json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("stats: write %s: %w", path, err)
	}
	return nil
}

// loadStats reads world/stats/<uuid>.json (missing/corrupt -> empty counter).
func loadStats(dir string, id uuid.UUID) *StatsCounter {
	path := filepath.Join(dir, statsDir, id.String()+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		return newStatsCounter()
	}
	return parseStatsJSON(b)
}

// snapshotStats deep-copies a counter for off-tick save.
func snapshotStats(c *StatsCounter) *StatsCounter {
	if c == nil {
		return newStatsCounter()
	}
	cp := &StatsCounter{stats: make(map[statKey]int32, len(c.stats))}
	for k, v := range c.stats {
		cp.stats[k] = v
	}
	return cp
}

// statsOrEmpty returns c, or a fresh empty counter when c is nil, so the
// AwardStats send path never dereferences a nil counter (a request_stats from a
// stats-less player answers with an empty map).
func statsOrEmpty(c *StatsCounter) *StatsCounter {
	if c == nil {
		return newStatsCounter()
	}
	return c
}
