package chunkticket

// chunkUpdated mirrors TicketStorage.ChunkUpdated: (chunkKey, ticketLevel, isDecreasing)
// notifications fed to the loading / simulation trackers when a chunk's lowest ticket
// level of the relevant kind changes. CITE: TicketStorage$ChunkUpdated.
type chunkUpdated func(key int64, ticketLevel int, isDecreasing bool)

// ticketStorage is a 1:1 port of net.minecraft.world.level.TicketStorage: the map of
// chunkKey -> tickets, the getTicketLevelAt(simulation) lowest-ticket query, and the
// listener notifications that drive the loading/simulation trackers. Persistence,
// forced-chunk bookkeeping, and the deactivated-ticket set are out of scope for the
// observe-only wiring and are STUBbed (cited inline). CITE: TicketStorage.
type ticketStorage struct {
	tickets map[int64][]*Ticket

	loadingListener    chunkUpdated
	simulationListener chunkUpdated
}

func newTicketStorage() *ticketStorage {
	return &ticketStorage{tickets: make(map[int64][]*Ticket)}
}

func (s *ticketStorage) setLoadingChunkUpdatedListener(f chunkUpdated)    { s.loadingListener = f }
func (s *ticketStorage) setSimulationChunkUpdatedListener(f chunkUpdated) { s.simulationListener = f }

// getTickets mirrors TicketStorage.getTickets(long): the list for a key (may be empty).
func (s *ticketStorage) getTickets(key int64) []*Ticket { return s.tickets[key] }

// getLowestTicket mirrors TicketStorage.getLowestTicket(List, boolean simulation):
// the min-level ticket among those that doesSimulate() (simulation==true) or
// doesLoad() (simulation==false). CITE: TicketStorage.getLowestTicket.
func getLowestTicket(list []*Ticket, simulation bool) *Ticket {
	var best *Ticket
	for _, t := range list {
		if best == nil || t.Level < best.Level {
			if simulation {
				if t.Type.DoesSimulate() {
					best = t
				}
			} else {
				if t.Type.DoesLoad() {
					best = t
				}
			}
		}
	}
	return best
}

// getTicketLevelAtList mirrors the private static TicketStorage.getTicketLevelAt(List,
// boolean): lowest.getTicketLevel() or MAX_LEVEL+1 when none. CITE: TicketStorage.
func getTicketLevelAtList(list []*Ticket, simulation bool) int {
	t := getLowestTicket(list, simulation)
	if t == nil {
		return MaxLevel + 1
	}
	return t.Level
}

// getTicketLevelAt mirrors TicketStorage.getTicketLevelAt(long, boolean).
func (s *ticketStorage) getTicketLevelAt(key int64, simulation bool) int {
	return getTicketLevelAtList(s.getTickets(key), simulation)
}

// isTicketSameTypeAndLevel mirrors the private helper.
func isTicketSameTypeAndLevel(a, b *Ticket) bool {
	return a.Type == b.Type && a.Level == b.Level
}

// addTicket mirrors TicketStorage.addTicket(long, Ticket): resets ticksLeft if a same
// type+level ticket exists (returns false); else appends and fires the simulation /
// loading listeners when this ticket lowers the respective level. CITE:
// TicketStorage.addTicket(long, Ticket).
func (s *ticketStorage) addTicket(key int64, ticket *Ticket) bool {
	list := s.tickets[key]
	for _, existing := range list {
		if isTicketSameTypeAndLevel(ticket, existing) {
			existing.ResetTicksLeft()
			return false
		}
	}
	simBefore := getTicketLevelAtList(list, true)
	loadBefore := getTicketLevelAtList(list, false)
	list = append(list, ticket)
	s.tickets[key] = list

	if ticket.Type.DoesSimulate() && ticket.Level < simBefore && s.simulationListener != nil {
		s.simulationListener(key, ticket.Level, true)
	}
	if ticket.Type.DoesLoad() && ticket.Level < loadBefore && s.loadingListener != nil {
		s.loadingListener(key, ticket.Level, true)
	}
	// STUB (cited): TicketType.FORCED chunksWithForcedTickets bookkeeping omitted (no
	// force-load feature wired). CITE: TicketStorage.addTicket forced branch.
	return true
}

// removeTicket mirrors TicketStorage.removeTicket(long, Ticket): removes a same
// type+level ticket and fires the listeners (isDecreasing=false) when the level rises.
// CITE: TicketStorage.removeTicket(long, Ticket).
func (s *ticketStorage) removeTicket(key int64, ticket *Ticket) bool {
	list := s.tickets[key]
	if len(list) == 0 {
		return false
	}
	idx := -1
	for i, existing := range list {
		if isTicketSameTypeAndLevel(ticket, existing) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}
	simBefore := getTicketLevelAtList(list, true)
	loadBefore := getTicketLevelAtList(list, false)

	list = append(list[:idx], list[idx+1:]...)
	if len(list) == 0 {
		delete(s.tickets, key)
	} else {
		s.tickets[key] = list
	}

	simAfter := getTicketLevelAtList(list, true)
	loadAfter := getTicketLevelAtList(list, false)
	if simAfter > simBefore && s.simulationListener != nil {
		s.simulationListener(key, simAfter, false)
	}
	if loadAfter > loadBefore && s.loadingListener != nil {
		s.loadingListener(key, loadAfter, false)
	}
	return true
}

// removeTicketWithRadius / addTicketWithRadius mirror the radius helpers used by the
// spawn/portal machinery: level = ByStatus(FULL) - radius. CITE:
// TicketStorage.addTicketWithRadius (new Ticket(type, ChunkLevel.byStatus(FULL)-radius)).
func (s *ticketStorage) addTicketWithRadius(t TicketType, key int64, radius int) {
	s.addTicket(key, NewTicket(t, ByStatus(Full)-radius))
}

// replaceTicketLevelOfType mirrors TicketStorage.replaceTicketLevelOfType(int, TicketType):
// re-level every ticket of the given type. Used when simulation distance changes.
// CITE: TicketStorage.replaceTicketLevelOfType.
func (s *ticketStorage) replaceTicketLevelOfType(newLevel int, t TicketType) {
	// Snapshot the (key, old-level) pairs to re-level; removeTicket/addTicket mutate the
	// map, so we must not iterate it concurrently. Each matching ticket is removed (firing
	// the increase notification) and re-added at newLevel (firing the decrease one).
	type entry struct {
		key      int64
		oldLevel int
	}
	var matches []entry
	for key, list := range s.tickets {
		for _, tk := range list {
			if tk.Type == t {
				matches = append(matches, entry{key, tk.Level})
			}
		}
	}
	for _, m := range matches {
		s.removeTicket(m.key, NewTicket(t, m.oldLevel))
		s.addTicket(m.key, NewTicket(t, newLevel))
	}
}

// purgeStaleTickets mirrors the timed-ticket fade: decrease every timed ticket by one
// tick and remove those that have timed out, firing listeners so the trackers relevel.
// Vanilla splits this between Ticket.decreaseTicksLeft (driven from the chunk-holder
// scheduler) and canTicketExpire in TicketStorage.purgeStaleTickets; here it is folded
// into one per-tick pass over active tickets. CITE: Ticket.decreaseTicksLeft /
// isTimedOut + TicketStorage.purgeStaleTickets.
func (s *ticketStorage) purgeStaleTickets() {
	// Snapshot keys so removeTicket can mutate the map.
	keys := make([]int64, 0, len(s.tickets))
	for k := range s.tickets {
		keys = append(keys, k)
	}
	for _, key := range keys {
		list := s.tickets[key]
		var expired []*Ticket
		for _, t := range list {
			t.DecreaseTicksLeft()
			if t.IsTimedOut() {
				expired = append(expired, t)
			}
		}
		for _, t := range expired {
			s.removeTicket(key, t)
		}
	}
}
