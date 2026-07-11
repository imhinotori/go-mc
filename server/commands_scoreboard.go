package server

// commands_scoreboard.go -- the vanilla 26.2 /scoreboard and /team commands, built on the existing
// command.Graph seam (buildCommandGraph, commands.go) and permission-gated on their vanilla nodes
// (minecraft.command.scoreboard / minecraft.command.team). Handlers run on the tick goroutine
// (executorFrom(ctx) yields the issuing tickPlayer + TickLoop), so they mutate the tick-owned
// t.scoreboard directly (TICK-05) and the on* callbacks broadcast the clientbound packets.
//
// ARG STYLE: v1 uses the greedy StringParser(2) + in-handler strings.Fields parse (the /gamerule
// posture) so the wire command graph stays a valid brigadier tree the client tab-completes at the
// literal level. CITE net.minecraft.server.commands.ScoreboardCommand + TeamCommand.
//
// The stat-fed criteria feed (health/deathCount/...) is DEFERRED (no stat subsystem yet) -- the
// "dummy" criterion (the manually-set common case) is fully supported. CITE ObjectiveCriteria.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/server/command"
)

var (
	errScoreboardUsage = errors.New("usage: /scoreboard <objectives|players> ...")
	errTeamUsage       = errors.New("usage: /team <add|remove|empty|join|leave|modify|list> ...")
)

// registerScoreboardCommands adds /scoreboard and /team. Called from buildCommandGraph.
func registerScoreboardCommands(g *command.Graph) {
	registerScoreboard(g)
	registerTeam(g)
}

// registerScoreboard wires /scoreboard objectives (add/remove/list/setdisplay) and
// /scoreboard players (set/add/remove/reset/list). CITE ScoreboardCommand.
func registerScoreboard(g *command.Graph) {
	h := permissionGated("minecraft.command.scoreboard", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok || e.p == nil {
			return nil
		}
		if len(args) == 0 {
			return errScoreboardUsage
		}
		raw := commandRaw(args)
		f := strings.Fields(strings.TrimSpace(raw))
		if len(f) == 0 {
			return errScoreboardUsage
		}
		switch f[0] {
		case "objectives":
			return e.scoreboardObjectivesCmd(f[1:])
		case "players":
			return e.scoreboardPlayersCmd(f[1:])
		}
		return errScoreboardUsage
	})
	// ScoreboardCommand typed tree. The handler switches on the token stream, so we build the
	// vanilla literal/typed leaves and route every terminal to h. objective id 24,
	// objective_criteria id 25, scoreboard_slot id 30, score_holder id 31 (multiple), integer id 3.
	// objectives add <name:word> <criteria:objective_criteria> [display:component]
	objAddDisplay := g.Argument("displayName", command.ComponentParser()).HandleFunc(h)
	objAddCrit := g.Argument("criteria", command.ObjectiveCriteriaParser()).AppendArgument(objAddDisplay).HandleFunc(h)
	objAddName := g.Argument("objective", command.StringParser(0)).AppendArgument(objAddCrit).Unhandle()
	// objectives remove <objective>
	objRemove := g.Argument("objective", command.ObjectiveParser()).Suggests(command.SuggestObjective, "").HandleFunc(h)
	// objectives setdisplay <slot:scoreboard_slot> [objective]
	sdObj := g.Argument("objective", command.ObjectiveParser()).Suggests(command.SuggestObjective, "").HandleFunc(h)
	sdSlot := g.Argument("slot", command.ScoreboardSlotParser()).AppendArgument(sdObj).HandleFunc(h)
	objectives := g.Literal("objectives").
		AppendLiteral(g.Literal("list").HandleFunc(h)).
		AppendLiteral(g.Literal("add").AppendArgument(objAddName).Unhandle()).
		AppendLiteral(g.Literal("remove").AppendArgument(objRemove).Unhandle()).
		AppendLiteral(g.Literal("setdisplay").AppendArgument(sdSlot).Unhandle()).
		Unhandle()
	// players set|add|remove <targets:score_holder> <objective> <score:int>
	mkScore := func() *command.Argument {
		score := g.Argument("score", command.IntegerParser{}).HandleFunc(h)
		obj := g.Argument("objective", command.ObjectiveParser()).Suggests(command.SuggestObjective, "").AppendArgument(score).Unhandle()
		return g.Argument("targets", command.ScoreHolderParser{Multiple: true}).Suggests(command.SuggestPlayers, "").AppendArgument(obj).Unhandle()
	}
	// players reset <targets:score_holder> [objective]
	resetObj := g.Argument("objective", command.ObjectiveParser()).Suggests(command.SuggestObjective, "").HandleFunc(h)
	resetTargets := g.Argument("targets", command.ScoreHolderParser{Multiple: true}).Suggests(command.SuggestPlayers, "").AppendArgument(resetObj).HandleFunc(h)
	// players list [target:score_holder]
	listTarget := g.Argument("target", command.ScoreHolderParser{Multiple: true}).Suggests(command.SuggestPlayers, "").HandleFunc(h)
	players := g.Literal("players").
		AppendLiteral(g.Literal("set").AppendArgument(mkScore()).Unhandle()).
		AppendLiteral(g.Literal("add").AppendArgument(mkScore()).Unhandle()).
		AppendLiteral(g.Literal("remove").AppendArgument(mkScore()).Unhandle()).
		AppendLiteral(g.Literal("reset").AppendArgument(resetTargets).Unhandle()).
		AppendLiteral(g.Literal("list").AppendArgument(listTarget).HandleFunc(h)).
		Unhandle()
	g.AppendLiteral(g.Literal("scoreboard").
		AppendLiteral(objectives).
		AppendLiteral(players).
		Unhandle())
}

// scoreboardObjectivesCmd handles /scoreboard objectives <add|remove|list|setdisplay>.
//
//	[VERIFIED javap ScoreboardCommand: objectives add <name> <criteria> [displayName]; objectives
//	 remove <name>; objectives list; objectives setdisplay <slot> [objective].]
func (e cmdExecutor) scoreboardObjectivesCmd(f []string) error {
	if len(f) == 0 {
		return errors.New("usage: /scoreboard objectives <add|remove|list|setdisplay> ...")
	}
	switch f[0] {
	case "add":
		if len(f) < 3 {
			return errors.New("usage: /scoreboard objectives add <name> <criteria> [displayName]")
		}
		name, critName := f[1], f[2]
		if e.t.scoreboard.getObjective(name) != nil {
			return fmt.Errorf("an objective already exists by that name: %s", name)
		}
		crit, ok := builtInCriteria[critName]
		if !ok {
			return fmt.Errorf("unknown criterion: %s", critName)
		}
		display := chat.Message{Text: name}
		if len(f) > 3 {
			display = chat.Message{Text: strings.Join(f[3:], " ")}
		}
		e.t.scoreboardAddObjective(name, crit, display)
		e.t.sendSystemChat(e.p, "Created new objective ["+name+"]")
		return nil
	case "remove":
		if len(f) < 2 {
			return errors.New("usage: /scoreboard objectives remove <name>")
		}
		o := e.t.scoreboard.getObjective(f[1])
		if o == nil {
			return fmt.Errorf("unknown scoreboard objective: %s", f[1])
		}
		e.t.scoreboardRemoveObjective(o)
		e.t.sendSystemChat(e.p, "Removed objective ["+f[1]+"]")
		return nil
	case "list":
		names := make([]string, 0, len(e.t.scoreboard.objectives))
		for n := range e.t.scoreboard.objectives {
			names = append(names, n)
		}
		e.t.sendSystemChat(e.p, fmt.Sprintf("There are %d objective(s): %s", len(names), strings.Join(names, ", ")))
		return nil
	case "setdisplay":
		if len(f) < 2 {
			return errors.New("usage: /scoreboard objectives setdisplay <slot> [objective]")
		}
		slot, ok := displaySlotByName(f[1])
		if !ok {
			return fmt.Errorf("unknown display slot: %s", f[1])
		}
		if len(f) < 3 {
			e.t.scoreboardSetDisplay(slot, nil)
			e.t.sendSystemChat(e.p, "Cleared display slot ["+f[1]+"]")
			return nil
		}
		o := e.t.scoreboard.getObjective(f[2])
		if o == nil {
			return fmt.Errorf("unknown scoreboard objective: %s", f[2])
		}
		e.t.scoreboardSetDisplay(slot, o)
		e.t.sendSystemChat(e.p, "Set display slot ["+f[1]+"] to show objective ["+f[2]+"]")
		return nil
	}
	return errors.New("usage: /scoreboard objectives <add|remove|list|setdisplay> ...")
}

// scoreboardPlayersCmd handles /scoreboard players <set|add|remove|reset|list>.
//
//	[VERIFIED javap ScoreboardCommand: players set <target> <objective> <score>; players add ...;
//	 players remove ...; players reset <target> [objective]; players list [target].]
func (e cmdExecutor) scoreboardPlayersCmd(f []string) error {
	if len(f) == 0 {
		return errors.New("usage: /scoreboard players <set|add|remove|reset|list> ...")
	}
	switch f[0] {
	case "set", "add", "remove":
		if len(f) < 4 {
			return fmt.Errorf("usage: /scoreboard players %s <target> <objective> <count>", f[0])
		}
		owner, objName := f[1], f[2]
		n, err := strconv.ParseInt(f[3], 10, 32)
		if err != nil {
			return fmt.Errorf("invalid count: %s", f[3])
		}
		o := e.t.scoreboard.getObjective(objName)
		if o == nil {
			return fmt.Errorf("unknown scoreboard objective: %s", objName)
		}
		cur := e.t.scoreboard.getOrCreateScore(owner, objName).value
		var val int32
		switch f[0] {
		case "set":
			val = int32(n)
		case "add":
			val = cur + int32(n)
		case "remove":
			val = cur - int32(n)
		}
		e.t.scoreboardSetScore(owner, o, val)
		e.t.sendSystemChat(e.p, fmt.Sprintf("Set [%s] for %s to %d", objName, owner, val))
		return nil
	case "reset":
		if len(f) < 2 {
			return errors.New("usage: /scoreboard players reset <target> [objective]")
		}
		owner := f[1]
		if len(f) < 3 {
			e.t.scoreboardResetScore(owner, nil)
			e.t.sendSystemChat(e.p, "Reset all scores of "+owner)
			return nil
		}
		o := e.t.scoreboard.getObjective(f[2])
		if o == nil {
			return fmt.Errorf("unknown scoreboard objective: %s", f[2])
		}
		e.t.scoreboardResetScore(owner, o)
		e.t.sendSystemChat(e.p, fmt.Sprintf("Reset [%s] for %s", f[2], owner))
		return nil
	case "list":
		return nil
	}
	return errors.New("usage: /scoreboard players <set|add|remove|reset|list> ...")
}

// registerTeam wires /team (add/remove/empty/join/leave/modify/list). CITE TeamCommand.
func registerTeam(g *command.Graph) {
	h := permissionGated("minecraft.command.team", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok || e.p == nil {
			return nil
		}
		if len(args) == 0 {
			return errTeamUsage
		}
		raw := commandRaw(args)
		f := strings.Fields(strings.TrimSpace(raw))
		if len(f) == 0 {
			return errTeamUsage
		}
		return e.teamCmd(f)
	})
	// TeamCommand typed tree. team id 33 (no props), score_holder id 31, team_color id 16,
	// component id 18. The handler reads the token stream, so all terminals run h.
	addDisplay := g.Argument("displayName", command.ComponentParser()).HandleFunc(h)
	addName := g.Argument("team", command.StringParser(0)).AppendArgument(addDisplay).HandleFunc(h)
	removeTeam := g.Argument("team", command.TeamParser()).Suggests(command.SuggestTeam, "").HandleFunc(h)
	emptyTeam := g.Argument("team", command.TeamParser()).Suggests(command.SuggestTeam, "").HandleFunc(h)
	joinMembers := g.Argument("members", command.ScoreHolderParser{Multiple: true}).Suggests(command.SuggestPlayers, "").HandleFunc(h)
	joinTeam := g.Argument("team", command.TeamParser()).Suggests(command.SuggestTeam, "").AppendArgument(joinMembers).HandleFunc(h)
	leaveMembers := g.Argument("members", command.ScoreHolderParser{Multiple: true}).Suggests(command.SuggestPlayers, "").HandleFunc(h)
	listTeam := g.Argument("team", command.TeamParser()).Suggests(command.SuggestTeam, "").HandleFunc(h)
	// modify <team> <option> <value> -- value is option-dependent; word is a safe brigadier:string.
	modValue := g.Argument("value", command.StringParser(2)).HandleFunc(h)
	modOption := g.Argument("option", command.StringParser(0)).AppendArgument(modValue).Unhandle()
	modTeam := g.Argument("team", command.TeamParser()).Suggests(command.SuggestTeam, "").AppendArgument(modOption).Unhandle()
	g.AppendLiteral(g.Literal("team").
		AppendLiteral(g.Literal("list").AppendArgument(listTeam).HandleFunc(h)).
		AppendLiteral(g.Literal("add").AppendArgument(addName).Unhandle()).
		AppendLiteral(g.Literal("remove").AppendArgument(removeTeam).Unhandle()).
		AppendLiteral(g.Literal("empty").AppendArgument(emptyTeam).Unhandle()).
		AppendLiteral(g.Literal("join").AppendArgument(joinTeam).Unhandle()).
		AppendLiteral(g.Literal("leave").AppendArgument(leaveMembers).Unhandle()).
		AppendLiteral(g.Literal("modify").AppendArgument(modTeam).Unhandle()).
		Unhandle())
}

// teamCmd handles the /team subcommands.
//
//	[VERIFIED javap TeamCommand: add <name> [displayName]; remove <name>; empty <name>;
//	 join <name> [members...]; leave <members...>; modify <name> <option> <value>; list [name].]
func (e cmdExecutor) teamCmd(f []string) error {
	switch f[0] {
	case "add":
		if len(f) < 2 {
			return errors.New("usage: /team add <name> [displayName]")
		}
		if e.t.scoreboard.teams[f[1]] != nil {
			return fmt.Errorf("a team already exists with that name: %s", f[1])
		}
		team := e.t.scoreboardAddTeam(f[1])
		if len(f) > 2 {
			team.displayName = chat.Message{Text: strings.Join(f[2:], " ")}
			e.t.scoreboardTeamChanged(team)
		}
		e.t.sendSystemChat(e.p, "Created team ["+f[1]+"]")
		return nil
	case "remove":
		if len(f) < 2 {
			return errors.New("usage: /team remove <name>")
		}
		team := e.t.scoreboard.teams[f[1]]
		if team == nil {
			return fmt.Errorf("unknown team: %s", f[1])
		}
		e.t.scoreboardRemoveTeam(team)
		e.t.sendSystemChat(e.p, "Removed team ["+f[1]+"]")
		return nil
	case "empty":
		if len(f) < 2 {
			return errors.New("usage: /team empty <name>")
		}
		team := e.t.scoreboard.teams[f[1]]
		if team == nil {
			return fmt.Errorf("unknown team: %s", f[1])
		}
		for _, m := range team.orderedMembers() {
			e.t.scoreboardRemovePlayerFromTeam(m, team)
		}
		e.t.sendSystemChat(e.p, "Removed all members from team ["+f[1]+"]")
		return nil
	case "join":
		if len(f) < 2 {
			return errors.New("usage: /team join <name> [members...]")
		}
		team := e.t.scoreboard.teams[f[1]]
		if team == nil {
			return fmt.Errorf("unknown team: %s", f[1])
		}
		members := f[2:]
		if len(members) == 0 {
			members = []string{e.p.name} // no members -> the issuing player joins
		}
		for _, m := range members {
			e.t.scoreboardAddPlayerToTeam(m, team)
		}
		e.t.sendSystemChat(e.p, fmt.Sprintf("Added %d member(s) to team [%s]", len(members), f[1]))
		return nil
	case "leave":
		members := f[1:]
		if len(members) == 0 {
			members = []string{e.p.name}
		}
		for _, m := range members {
			if team := e.t.scoreboard.teamsByPlayer[m]; team != nil {
				e.t.scoreboardRemovePlayerFromTeam(m, team)
			}
		}
		e.t.sendSystemChat(e.p, fmt.Sprintf("Removed %d member(s) from their team", len(members)))
		return nil
	case "modify":
		return e.teamModifyCmd(f[1:])
	case "list":
		names := make([]string, 0, len(e.t.scoreboard.teams))
		for n := range e.t.scoreboard.teams {
			names = append(names, n)
		}
		e.t.sendSystemChat(e.p, fmt.Sprintf("There are %d team(s): %s", len(names), strings.Join(names, ", ")))
		return nil
	}
	return errTeamUsage
}

// teamModifyCmd handles /team modify <name> <option> <value>.
//
//	[VERIFIED javap TeamCommand modify options: displayName, prefix, suffix, color, friendlyFire,
//	 seeFriendlyInvisibles, nametagVisibility, collisionRule.]
func (e cmdExecutor) teamModifyCmd(f []string) error {
	if len(f) < 3 {
		return errors.New("usage: /team modify <name> <option> <value>")
	}
	team := e.t.scoreboard.teams[f[0]]
	if team == nil {
		return fmt.Errorf("unknown team: %s", f[0])
	}
	option, value := f[1], strings.Join(f[2:], " ")
	switch option {
	case "displayName":
		team.displayName = chat.Message{Text: value}
	case "prefix":
		team.playerPrefix = chat.Message{Text: value}
	case "suffix":
		team.playerSuffix = chat.Message{Text: value}
	case "color":
		c, ok := teamColorByName(value)
		if !ok {
			return fmt.Errorf("invalid color: %s", value)
		}
		team.color = c
	case "friendlyFire":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid boolean: %s", value)
		}
		team.allowFriendlyFire = b
	case "seeFriendlyInvisibles":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid boolean: %s", value)
		}
		team.seeFriendlyInvisibles = b
	case "nametagVisibility":
		v, ok := teamVisibilityByName(value)
		if !ok {
			return fmt.Errorf("invalid nametag visibility: %s", value)
		}
		team.nameTagVisibility = v
	case "collisionRule":
		v, ok := teamCollisionRuleByName(value)
		if !ok {
			return fmt.Errorf("invalid collision rule: %s", value)
		}
		team.collisionRule = v
	default:
		return fmt.Errorf("unknown team option: %s", option)
	}
	e.t.scoreboardTeamChanged(team)
	e.t.sendSystemChat(e.p, fmt.Sprintf("Updated %s of team [%s]", option, f[0]))
	return nil
}
