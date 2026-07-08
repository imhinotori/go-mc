package server

// scoreboard.go -- the server-side Scoreboard / ServerScoreboard data model + broadcast, ported
// 1:1 from the unobfuscated 26.2 inner jar (temp/cache/26.2-inner.jar, javap this session):
//   net.minecraft.world.scores.Scoreboard          (objectives / scores / teams / display slots)
//   net.minecraft.server.ServerScoreboard          (the on* callbacks that broadcast packets)
//   net.minecraft.world.scores.Objective / Score / PlayerTeam / Team / DisplaySlot / TeamColor
//   net.minecraft.world.scores.criteria.ObjectiveCriteria(+RenderType)
//
// This is player-facing state ONLY -- it draws NO entity RNG and touches no mob/damage path, so the
// pig oracle (TestPluginPigEqualsGoNativePig) is byte-identical. The model + broadcast run on the
// tick goroutine (t.scoreboard is TickLoop-owned, TICK-05); the wire encoders live in
// scoreboard_packets.go. The ServerScoreboard.on* callbacks fan the marshalled packet across
// t.players exactly as broadcastSystemChat does (marshal once, loop, skip nil client).

import (
	"sort"

	"github.com/imhinotori/sulfur/chat"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// displaySlot is net.minecraft.world.scores.DisplaySlot. The id() is the wire value (writeById ->
// VarInt id). VERIFIED javap DisplaySlot: LIST=0, SIDEBAR=1, BELOW_NAME=2, then TEAM_<color>=3..18
// in TeamColor declaration order. The serialized names are list/sidebar/below_name and
// sidebar.team.<color>.
type displaySlot int32

const (
	displaySlotList      displaySlot = 0
	displaySlotSidebar   displaySlot = 1
	displaySlotBelowName displaySlot = 2
	// TEAM_<color> slots 3..18 map to the 16 chat colors (TeamColor id 0..15) via 3+id.
	displaySlotTeamBase displaySlot = 3
	displaySlotCount    displaySlot = 19
)

// displaySlotByName resolves a DisplaySlot serialized name to its id. VERIFIED javap
// DisplaySlot.CODEC (StringRepresentable): list/sidebar/below_name + sidebar.team.<color>.
func displaySlotByName(name string) (displaySlot, bool) {
	switch name {
	case "list":
		return displaySlotList, true
	case "sidebar":
		return displaySlotSidebar, true
	case "below_name":
		return displaySlotBelowName, true
	}
	// sidebar.team.<color>
	const prefix = "sidebar.team."
	if len(name) > len(prefix) && name[:len(prefix)] == prefix {
		if c, ok := teamColorByName(name[len(prefix):]); ok {
			return displaySlotTeamBase + displaySlot(c), true
		}
	}
	return 0, false
}

// objectiveRenderType is net.minecraft.world.scores.criteria.ObjectiveCriteria$RenderType. The
// wire value is writeEnum -> VarInt(ordinal). VERIFIED javap: INTEGER=0 ("integer"), HEARTS=1
// ("hearts").
type objectiveRenderType int32

const (
	renderTypeInteger objectiveRenderType = 0
	renderTypeHearts  objectiveRenderType = 1
)

// renderTypeByName resolves the RenderType serialized name.
func renderTypeByName(name string) (objectiveRenderType, bool) {
	switch name {
	case "integer":
		return renderTypeInteger, true
	case "hearts":
		return renderTypeHearts, true
	}
	return 0, false
}

// teamColor is net.minecraft.world.scores.TeamColor. The wire value is its id (0..15, the
// ChatFormatting color order), written via TeamColor.STREAM_CODEC (idMapper -> VarInt id). VERIFIED
// javap TeamColor declaration order. A value < 0 means "no color" (reset -> Optional.empty).
type teamColor int32

const teamColorNone teamColor = -1

// teamColorNames are the 16 TeamColor serialized names in id order (VERIFIED javap TeamColor).
var teamColorNames = []string{
	"black", "dark_blue", "dark_green", "dark_aqua",
	"dark_red", "dark_purple", "gold", "gray",
	"dark_gray", "blue", "green", "aqua",
	"red", "light_purple", "yellow", "white",
}

// teamColorByName resolves a TeamColor serialized name to its id. "reset"/"" -> teamColorNone.
func teamColorByName(name string) (teamColor, bool) {
	if name == "reset" || name == "" {
		return teamColorNone, true
	}
	for i, n := range teamColorNames {
		if n == name {
			return teamColor(i), true
		}
	}
	return teamColorNone, false
}

// teamVisibility is net.minecraft.world.scores.Team$Visibility. Wire value = the enum id (VarInt via
// STREAM_CODEC). VERIFIED javap: ALWAYS=0 ("always"), NEVER=1 ("never"),
// HIDE_FOR_OTHER_TEAMS=2 ("hideForOtherTeams"), HIDE_FOR_OWN_TEAM=3 ("hideForOwnTeam").
type teamVisibility int32

const (
	teamVisibilityAlways            teamVisibility = 0
	teamVisibilityNever             teamVisibility = 1
	teamVisibilityHideForOtherTeams teamVisibility = 2
	teamVisibilityHideForOwnTeam    teamVisibility = 3
)

// teamVisibilityByName resolves a Team$Visibility serialized name.
func teamVisibilityByName(name string) (teamVisibility, bool) {
	switch name {
	case "always":
		return teamVisibilityAlways, true
	case "never":
		return teamVisibilityNever, true
	case "hideForOtherTeams":
		return teamVisibilityHideForOtherTeams, true
	case "hideForOwnTeam":
		return teamVisibilityHideForOwnTeam, true
	}
	return 0, false
}

// teamCollisionRule is net.minecraft.world.scores.Team$CollisionRule. Wire value = the enum id
// (VarInt via STREAM_CODEC). VERIFIED javap: ALWAYS=0 ("always"), NEVER=1 ("never"),
// PUSH_OTHER_TEAMS=2 ("pushOtherTeams"), PUSH_OWN_TEAM=3 ("pushOwnTeam").
type teamCollisionRule int32

const (
	teamCollisionAlways         teamCollisionRule = 0
	teamCollisionNever          teamCollisionRule = 1
	teamCollisionPushOtherTeams teamCollisionRule = 2
	teamCollisionPushOwnTeam    teamCollisionRule = 3
)

// teamCollisionRuleByName resolves a Team$CollisionRule serialized name.
func teamCollisionRuleByName(name string) (teamCollisionRule, bool) {
	switch name {
	case "always":
		return teamCollisionAlways, true
	case "never":
		return teamCollisionNever, true
	case "pushOtherTeams":
		return teamCollisionPushOtherTeams, true
	case "pushOwnTeam":
		return teamCollisionPushOwnTeam, true
	}
	return 0, false
}

// objectiveCriteria is a built-in net.minecraft.world.scores.criteria.ObjectiveCriteria. VERIFIED
// javap ObjectiveCriteria.<clinit>: the built-ins with their default RenderType and readOnly flag.
// v1 fully supports "dummy" (the manually-set criterion, the common case). The stat-fed criteria
// (health/deathCount/playerKillCount/... ) have their render type + read-only flag modeled here so
// /scoreboard accepts them and the objective renders correctly, but the automatic stat FEED that
// updates their scores is DEFERRED (no stat subsystem yet) -- CITE ObjectiveCriteria stat wiring.
type objectiveCriteria struct {
	name       string
	renderType objectiveRenderType
	readOnly   bool
}

// builtInCriteria maps the criterion string to its criteria. VERIFIED javap ObjectiveCriteria:
// dummy/trigger/deathCount/playerKillCount/totalKillCount are read-write INTEGER; health is
// read-only HEARTS; food/air/armor/xp/level are read-only INTEGER.
var builtInCriteria = map[string]objectiveCriteria{
	"dummy":           {"dummy", renderTypeInteger, false},
	"trigger":         {"trigger", renderTypeInteger, false},
	"deathCount":      {"deathCount", renderTypeInteger, false},
	"playerKillCount": {"playerKillCount", renderTypeInteger, false},
	"totalKillCount":  {"totalKillCount", renderTypeInteger, false},
	"health":          {"health", renderTypeHearts, true},
	"food":            {"food", renderTypeInteger, true},
	"air":             {"air", renderTypeInteger, true},
	"armor":           {"armor", renderTypeInteger, true},
	"xp":              {"xp", renderTypeInteger, true},
	"level":           {"level", renderTypeInteger, true},
}

// scoreboardObjective is net.minecraft.world.scores.Objective. displayName is the shown Component;
// renderType is INTEGER/HEARTS; numberFormat is the optional per-objective format (default none).
type scoreboardObjective struct {
	name         string
	criteria     objectiveCriteria
	displayName  chat.Message
	renderType   objectiveRenderType
	numberFormat numberFormat
}

// scoreboardScore is net.minecraft.world.scores.Score: the value plus the optional per-score
// display Component + numberFormat overrides. locked mirrors Score.locked (unused by /scoreboard).
type scoreboardScore struct {
	value        int32
	locked       bool
	hasDisplay   bool
	display      chat.Message
	numberFormat numberFormat
}

// scoreboardTeam is net.minecraft.world.scores.PlayerTeam. members is the set of member names;
// membersOrder preserves insertion order for the ADD member-list wire (vanilla iterates the
// LinkedHashSet-style set). displayName/prefix/suffix are Components; the two booleans pack into the
// options byte (VERIFIED javap PlayerTeam.packOptions: BIT_FRIENDLY_FIRE 0x1, BIT_SEE_INVISIBLES 0x2).
type scoreboardTeam struct {
	name    string
	members map[string]struct{}
	order   []string

	displayName  chat.Message
	playerPrefix chat.Message
	playerSuffix chat.Message

	allowFriendlyFire     bool
	seeFriendlyInvisibles bool
	nameTagVisibility     teamVisibility
	collisionRule         teamCollisionRule
	color                 teamColor
}

// packOptions ports PlayerTeam.packOptions (VERIFIED javap): 0; if allowFriendlyFire OR 0x1; if
// seeFriendlyInvisibles OR 0x2.
func (t *scoreboardTeam) packOptions() int8 {
	var b int8
	if t.allowFriendlyFire {
		b |= 0x1
	}
	if t.seeFriendlyInvisibles {
		b |= 0x2
	}
	return b
}

// orderedMembers returns the member names in insertion order (the ADD/JOIN/LEAVE wire order).
func (t *scoreboardTeam) orderedMembers() []string {
	out := make([]string, 0, len(t.order))
	for _, n := range t.order {
		if _, ok := t.members[n]; ok {
			out = append(out, n)
		}
	}
	return out
}

// newScoreboardTeam builds a team with the vanilla PlayerTeam defaults (VERIFIED javap PlayerTeam
// ctor + Team defaults): allowFriendlyFire=true, seeFriendlyInvisibles=true, nameTagVisibility=
// ALWAYS, collisionRule=ALWAYS, color=none (Optional.empty), empty display/prefix/suffix, displayName
// defaults to the team name (a literal-text Component).
func newScoreboardTeam(name string) *scoreboardTeam {
	return &scoreboardTeam{
		name:                  name,
		members:               map[string]struct{}{},
		displayName:           chat.Message{Text: name},
		allowFriendlyFire:     true,
		seeFriendlyInvisibles: true,
		nameTagVisibility:     teamVisibilityAlways,
		collisionRule:         teamCollisionAlways,
		color:                 teamColorNone,
	}
}

// Scoreboard is net.minecraft.world.scores.Scoreboard: the objective/score/team/display-slot store.
// It is pure state; the ServerScoreboard broadcast (the on* callbacks) lives on *TickLoop below so
// it can reach t.players. TickLoop-owned (TICK-05); no locking.
type Scoreboard struct {
	objectives      map[string]*scoreboardObjective        // objectivesByName
	scores          map[string]map[string]*scoreboardScore // owner -> objectiveName -> score (playerScores)
	teams           map[string]*scoreboardTeam             // teamsByName
	teamsByPlayer   map[string]*scoreboardTeam             // teamsByPlayer
	displayObjectiv [displaySlotCount]*scoreboardObjective // displayObjectives, indexed by slot id
}

// newScoreboard builds an empty Scoreboard.
func newScoreboard() *Scoreboard {
	return &Scoreboard{
		objectives:    map[string]*scoreboardObjective{},
		scores:        map[string]map[string]*scoreboardScore{},
		teams:         map[string]*scoreboardTeam{},
		teamsByPlayer: map[string]*scoreboardTeam{},
	}
}

// getObjective returns the named objective or nil (Scoreboard.getObjective).
func (s *Scoreboard) getObjective(name string) *scoreboardObjective {
	return s.objectives[name]
}

// getOrCreateScore returns the owner+objective Score, creating a zero-valued one if absent
// (Scoreboard.getOrCreatePlayerScore). VERIFIED javap: a new Score defaults value 0.
func (s *Scoreboard) getOrCreateScore(owner, objective string) *scoreboardScore {
	byObj := s.scores[owner]
	if byObj == nil {
		byObj = map[string]*scoreboardScore{}
		s.scores[owner] = byObj
	}
	sc := byObj[objective]
	if sc == nil {
		sc = &scoreboardScore{numberFormat: numberFormat{kind: numberFormatNone}}
		byObj[objective] = sc
	}
	return sc
}

// broadcast marshals once and fans p to every player, exactly like broadcastSystemChat. Runs on the
// tick goroutine over the tick-owned t.players (TICK-05).
func (t *TickLoop) broadcastScoreboardPacket(p pk.Packet) {
	for _, pl := range t.players {
		if pl != nil && pl.client != nil {
			pl.client.Send(p)
		}
	}
}

// scoreboardAddObjective ports Scoreboard.addObjective + ServerScoreboard.onObjectiveAdded: store
// the objective and broadcast the ADD packet (startTracking). Returns false if the name is taken.
//
//	[VERIFIED javap Scoreboard.addObjective -> onObjectiveAdded -> getStartTrackingPackets ->
//	 ClientboundSetObjectivePacket(objective, METHOD_ADD).]
func (t *TickLoop) scoreboardAddObjective(name string, crit objectiveCriteria, displayName chat.Message) *scoreboardObjective {
	s := t.scoreboard
	if s.objectives[name] != nil {
		return nil
	}
	o := &scoreboardObjective{
		name:         name,
		criteria:     crit,
		displayName:  displayName,
		renderType:   crit.renderType,
		numberFormat: numberFormat{kind: numberFormatNone},
	}
	s.objectives[name] = o
	t.broadcastScoreboardPacket(encodeSetObjectiveAddOrChange(objectiveMethodAdd, o.name, o.displayName, o.renderType, o.numberFormat))
	return o
}

// scoreboardRemoveObjective ports Scoreboard.removeObjective + ServerScoreboard.onObjectiveRemoved:
// clear any display slot showing it, drop its scores, and broadcast REMOVE.
//
//	[VERIFIED javap Scoreboard.removeObjective: clears displayObjectives entries pointing at it,
//	 removes playerScores for it; ServerScoreboard.onObjectiveRemoved -> getStopTrackingPackets ->
//	 ClientboundSetObjectivePacket(objective, METHOD_REMOVE).]
func (t *TickLoop) scoreboardRemoveObjective(o *scoreboardObjective) {
	s := t.scoreboard
	if s.objectives[o.name] != o {
		return
	}
	for slot := displaySlot(0); slot < displaySlotCount; slot++ {
		if s.displayObjectiv[slot] == o {
			t.scoreboardSetDisplay(slot, nil)
		}
	}
	for owner, byObj := range s.scores {
		delete(byObj, o.name)
		if len(byObj) == 0 {
			delete(s.scores, owner)
		}
	}
	delete(s.objectives, o.name)
	t.broadcastScoreboardPacket(encodeSetObjectiveRemove(o.name))
}

// scoreboardSetDisplay ports Scoreboard.setDisplayObjective + ServerScoreboard.setDisplayObjective:
// store the slot -> objective mapping and broadcast SetDisplayObjective (a nil objective clears it,
// wire objectiveName == "").
//
//	[VERIFIED javap ServerScoreboard.setDisplayObjective: setDirty; broadcastAll(
//	 ClientboundSetDisplayObjectivePacket(slot, objective)).]
func (t *TickLoop) scoreboardSetDisplay(slot displaySlot, o *scoreboardObjective) {
	if slot < 0 || slot >= displaySlotCount {
		return
	}
	t.scoreboard.displayObjectiv[slot] = o
	name := ""
	if o != nil {
		name = o.name
	}
	t.broadcastScoreboardPacket(encodeSetDisplayObjective(slot, name))
}

// scoreboardSetScore ports Scoreboard.getOrCreatePlayerScore(...).set(value) +
// ServerScoreboard.onScoreChanged: store the value and broadcast SetScore.
//
//	[VERIFIED javap ServerScoreboard.onScoreChanged: setDirty; broadcastAll(
//	 ClientboundSetScorePacket(owner, objective, value, display, numberFormat)).]
func (t *TickLoop) scoreboardSetScore(owner string, o *scoreboardObjective, value int32) {
	sc := t.scoreboard.getOrCreateScore(owner, o.name)
	sc.value = value
	t.broadcastScoreboardPacket(encodeSetScore(owner, o.name, sc.value, sc.display, sc.hasDisplay, sc.numberFormat))
}

// scoreboardResetScore ports Scoreboard.resetSinglePlayerScore / removePlayerScore +
// ServerScoreboard.onPlayerScoreRemoved: drop the owner+objective score (or all objectives if
// o==nil) and broadcast ResetScore.
//
//	[VERIFIED javap ServerScoreboard.onPlayerScoreRemoved -> broadcastAll(
//	 ClientboundResetScorePacket(owner, objectiveName)); a null objective resets across all.]
func (t *TickLoop) scoreboardResetScore(owner string, o *scoreboardObjective) {
	s := t.scoreboard
	if o == nil {
		delete(s.scores, owner)
		t.broadcastScoreboardPacket(encodeResetScore(owner, "", false))
		return
	}
	if byObj := s.scores[owner]; byObj != nil {
		delete(byObj, o.name)
		if len(byObj) == 0 {
			delete(s.scores, owner)
		}
	}
	t.broadcastScoreboardPacket(encodeResetScore(owner, o.name, true))
}

// scoreboardAddTeam ports Scoreboard.addPlayerTeam + ServerScoreboard.onTeamAdded: store the team
// and broadcast the ADD packet. Returns nil if the name is taken.
//
//	[VERIFIED javap Scoreboard.addPlayerTeam -> onTeamAdded -> broadcastAll(
//	 ClientboundSetPlayerTeamPacket.createAddOrModifyPacket(team, true==ADD)).]
func (t *TickLoop) scoreboardAddTeam(name string) *scoreboardTeam {
	s := t.scoreboard
	if s.teams[name] != nil {
		return nil
	}
	team := newScoreboardTeam(name)
	s.teams[name] = team
	t.broadcastScoreboardPacket(encodeSetPlayerTeamAddOrChange(teamMethodAdd, team))
	return team
}

// scoreboardRemoveTeam ports Scoreboard.removePlayerTeam + ServerScoreboard.onTeamRemoved: drop the
// team, un-map its members, and broadcast REMOVE.
//
//	[VERIFIED javap Scoreboard.removePlayerTeam: removes teamsByName + each member from teamsByPlayer;
//	 onTeamRemoved -> broadcastAll(ClientboundSetPlayerTeamPacket.createRemovePacket(team)).]
func (t *TickLoop) scoreboardRemoveTeam(team *scoreboardTeam) {
	s := t.scoreboard
	if s.teams[team.name] != team {
		return
	}
	for name := range team.members {
		if s.teamsByPlayer[name] == team {
			delete(s.teamsByPlayer, name)
		}
	}
	delete(s.teams, team.name)
	t.broadcastScoreboardPacket(encodeSetPlayerTeamRemove(team.name))
}

// scoreboardTeamChanged ports ServerScoreboard.onTeamChanged: broadcast a CHANGE (parameters only,
// no member list). Called after a modify (option/color/display/prefix/suffix change).
//
//	[VERIFIED javap ServerScoreboard.onTeamChanged: broadcastAll(
//	 ClientboundSetPlayerTeamPacket.createAddOrModifyPacket(team, false==CHANGE)).]
func (t *TickLoop) scoreboardTeamChanged(team *scoreboardTeam) {
	t.broadcastScoreboardPacket(encodeSetPlayerTeamAddOrChange(teamMethodChange, team))
}

// scoreboardAddPlayerToTeam ports Scoreboard.addPlayerToTeam: if the player was on another team,
// remove it first (LEAVE broadcast), add to the new team, and broadcast a JOIN with that one member.
//
//	[VERIFIED javap Scoreboard.addPlayerToTeam: if getPlayersTeam(name)!=null removePlayerFromTeam;
//	 team.getPlayers().add(name); teamsByPlayer.put(name, team); onTeamAdded-style member broadcast
//	 (ClientboundSetPlayerTeamPacket.createPlayerPacket(team, name, ADD)).]
func (t *TickLoop) scoreboardAddPlayerToTeam(name string, team *scoreboardTeam) bool {
	s := t.scoreboard
	if prev := s.teamsByPlayer[name]; prev != nil {
		if prev == team {
			return false
		}
		t.scoreboardRemovePlayerFromTeam(name, prev)
	}
	if _, ok := team.members[name]; !ok {
		team.members[name] = struct{}{}
		team.order = append(team.order, name)
	}
	s.teamsByPlayer[name] = team
	t.broadcastScoreboardPacket(encodeSetPlayerTeamMembers(teamMethodJoin, team.name, []string{name}))
	return true
}

// scoreboardRemovePlayerFromTeam ports Scoreboard.removePlayerFromTeam: drop the member and
// broadcast a LEAVE with that one member.
//
//	[VERIFIED javap Scoreboard.removePlayerFromTeam: team.getPlayers().remove(name);
//	 teamsByPlayer.remove(name); broadcast(ClientboundSetPlayerTeamPacket.createPlayerPacket(team,
//	 name, REMOVE==LEAVE)).]
func (t *TickLoop) scoreboardRemovePlayerFromTeam(name string, team *scoreboardTeam) {
	s := t.scoreboard
	if _, ok := team.members[name]; !ok {
		return
	}
	delete(team.members, name)
	if s.teamsByPlayer[name] == team {
		delete(s.teamsByPlayer, name)
	}
	t.broadcastScoreboardPacket(encodeSetPlayerTeamMembers(teamMethodLeave, team.name, []string{name}))
}

// sendScoreboardStateTo ports the join-time sync a fresh client needs: ServerScoreboard.
// getStartTrackingPackets for every objective + display slot, every current score, and every team
// (with its member list). Called from the tick-side join seam (drainRegistrations) alongside the
// tab-list/weather sync -- the SAME "send current world state to the joining player" spot. Sends go
// on the owner goroutine over the joiner connection only (mutates no tick state).
//
//	[VERIFIED javap ServerScoreboard.getStartTrackingPackets(objective) + the display-slot + team
//	 startTracking broadcasts PlayerList.sendLevelInfo-style replays on player add.]
func (t *TickLoop) sendScoreboardStateTo(p *tickPlayer) {
	if p == nil || p.client == nil || t.scoreboard == nil {
		return
	}
	s := t.scoreboard
	// Objectives (ADD) in a stable name order.
	objNames := make([]string, 0, len(s.objectives))
	for name := range s.objectives {
		objNames = append(objNames, name)
	}
	sort.Strings(objNames)
	for _, name := range objNames {
		o := s.objectives[name]
		p.client.Send(encodeSetObjectiveAddOrChange(objectiveMethodAdd, o.name, o.displayName, o.renderType, o.numberFormat))
	}
	// Display slots.
	for slot := displaySlot(0); slot < displaySlotCount; slot++ {
		if o := s.displayObjectiv[slot]; o != nil {
			p.client.Send(encodeSetDisplayObjective(slot, o.name))
		}
	}
	// Scores in a stable owner+objective order.
	owners := make([]string, 0, len(s.scores))
	for owner := range s.scores {
		owners = append(owners, owner)
	}
	sort.Strings(owners)
	for _, owner := range owners {
		byObj := s.scores[owner]
		os := make([]string, 0, len(byObj))
		for on := range byObj {
			os = append(os, on)
		}
		sort.Strings(os)
		for _, on := range os {
			sc := byObj[on]
			p.client.Send(encodeSetScore(owner, on, sc.value, sc.display, sc.hasDisplay, sc.numberFormat))
		}
	}
	// Teams (ADD, with member list) in a stable name order.
	teamNames := make([]string, 0, len(s.teams))
	for name := range s.teams {
		teamNames = append(teamNames, name)
	}
	sort.Strings(teamNames)
	for _, name := range teamNames {
		p.client.Send(encodeSetPlayerTeamAddOrChange(teamMethodAdd, s.teams[name]))
	}
}
