package chunkticket

// Ticket-type flag bits. CITE: net.minecraft.server.level.TicketType flag constants
// (FLAG_PERSIST=1, FLAG_LOADING=2, FLAG_SIMULATION=4, FLAG_KEEP_DIMENSION_ACTIVE=8,
// FLAG_CAN_EXPIRE_IF_UNLOADED=16).
const (
	flagPersist             = 1
	flagLoading             = 2
	flagSimulation          = 4
	flagKeepDimensionActive = 8
	flagCanExpireIfUnloaded = 16
)

// noTimeout mirrors TicketType.NO_TIMEOUT (0L).
const noTimeout = int64(0)

// TicketType is a 1:1 record of net.minecraft.server.level.TicketType: a (timeout,
// flags) pair. Timeouts and flags come from the TicketType.<clinit> register(...)
// calls. Instances are values (comparable), matching Java's record equality that the
// ticket-storage same-type check relies on.
// CITE: TicketType(long timeout, int flags) + register(String, long, int).
type TicketType struct {
	Name    string
	Timeout int64
	Flags   int
}

// Persist mirrors TicketType.persist(): (flags & FLAG_PERSIST) != 0.
func (t TicketType) Persist() bool { return t.Flags&flagPersist != 0 }

// DoesLoad mirrors TicketType.doesLoad(): (flags & FLAG_LOADING) != 0.
func (t TicketType) DoesLoad() bool { return t.Flags&flagLoading != 0 }

// DoesSimulate mirrors TicketType.doesSimulate(): (flags & FLAG_SIMULATION) != 0.
func (t TicketType) DoesSimulate() bool { return t.Flags&flagSimulation != 0 }

// ShouldKeepDimensionActive mirrors TicketType.shouldKeepDimensionActive().
func (t TicketType) ShouldKeepDimensionActive() bool { return t.Flags&flagKeepDimensionActive != 0 }

// CanExpireIfUnloaded mirrors TicketType.canExpireIfUnloaded().
func (t TicketType) CanExpireIfUnloaded() bool { return t.Flags&flagCanExpireIfUnloaded != 0 }

// HasTimeout mirrors TicketType.hasTimeout(): timeout != 0.
func (t TicketType) HasTimeout() bool { return t.Timeout != noTimeout }

// The nine registered ticket types, with timeouts/flags EXACTLY as in
// TicketType.<clinit>. CITE: TicketType static initializer register(...) calls:
//
//	player_spawn      = register("player_spawn",      20L, FLAG_LOADING)
//	spawn_search      = register("spawn_search",       1L, FLAG_LOADING)
//	dragon            = register("dragon",             0L, FLAG_LOADING|FLAG_SIMULATION)
//	player_loading    = register("player_loading",     0L, FLAG_LOADING)
//	player_simulation = register("player_simulation",  0L, FLAG_SIMULATION|FLAG_KEEP_DIMENSION_ACTIVE)
//	forced            = register("forced",             0L, FLAG_PERSIST|FLAG_LOADING|FLAG_SIMULATION|FLAG_KEEP_DIMENSION_ACTIVE)
//	portal            = register("portal",           300L, FLAG_PERSIST|FLAG_LOADING|FLAG_SIMULATION|FLAG_KEEP_DIMENSION_ACTIVE)
//	ender_pearl       = register("ender_pearl",       40L, FLAG_LOADING|FLAG_SIMULATION|FLAG_KEEP_DIMENSION_ACTIVE)
//	unknown           = register("unknown",            1L, FLAG_LOADING|FLAG_CAN_EXPIRE_IF_UNLOADED)
var (
	PlayerSpawn      = TicketType{"player_spawn", 20, flagLoading}
	SpawnSearch      = TicketType{"spawn_search", 1, flagLoading}
	Dragon           = TicketType{"dragon", 0, flagLoading | flagSimulation}
	PlayerLoading    = TicketType{"player_loading", 0, flagLoading}
	PlayerSimulation = TicketType{"player_simulation", 0, flagSimulation | flagKeepDimensionActive}
	Forced           = TicketType{"forced", 0, flagPersist | flagLoading | flagSimulation | flagKeepDimensionActive}
	Portal           = TicketType{"portal", 300, flagPersist | flagLoading | flagSimulation | flagKeepDimensionActive}
	EnderPearl       = TicketType{"ender_pearl", 40, flagLoading | flagSimulation | flagKeepDimensionActive}
	Unknown          = TicketType{"unknown", 1, flagLoading | flagCanExpireIfUnloaded}
)

// Ticket is a 1:1 port of net.minecraft.server.level.Ticket: an immutable (type,
// level) with a mutable ticksLeft countdown. CITE: Ticket.
type Ticket struct {
	Type      TicketType
	Level     int
	ticksLeft int64
}

// NewTicket mirrors Ticket(TicketType, int): ticksLeft initialized to type.timeout().
// CITE: Ticket(TicketType type, int level) -> this(type, level, type.timeout()).
func NewTicket(t TicketType, level int) *Ticket {
	return &Ticket{Type: t, Level: level, ticksLeft: t.Timeout}
}

// ResetTicksLeft mirrors Ticket.resetTicksLeft(): ticksLeft = type.timeout().
func (t *Ticket) ResetTicksLeft() { t.ticksLeft = t.Type.Timeout }

// DecreaseTicksLeft mirrors Ticket.decreaseTicksLeft(): if type.hasTimeout(), ticksLeft--.
func (t *Ticket) DecreaseTicksLeft() {
	if t.Type.HasTimeout() {
		t.ticksLeft--
	}
}

// IsTimedOut mirrors Ticket.isTimedOut(): type.hasTimeout() && ticksLeft < 0.
func (t *Ticket) IsTimedOut() bool {
	return t.Type.HasTimeout() && t.ticksLeft < 0
}

// TicksLeft exposes the countdown for tests/debug (no vanilla accessor; internal field).
func (t *Ticket) TicksLeft() int64 { return t.ticksLeft }
