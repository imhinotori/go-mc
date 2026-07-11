package server

// commands_vanilla.go — the vanilla 26.2 command set built on the existing command.Graph seam
// (buildCommandGraph, commands.go). Each command is registered via registerVanillaCommands(g) and
// permission-gated on its vanilla node ("minecraft.command.<name>") so the LuckPerms-style store
// (permissions.go) authorizes it. Handlers run on the tick goroutine (executorFrom(ctx) yields the
// issuing tickPlayer + TickLoop), so they mutate tick-owned state directly (TICK-05).
//
// ARG STYLE: v1 uses the existing StringParser (word/greedy) + in-handler parsing (the same posture
// /tp already uses), which keeps the command graph a valid brigadier tree the client accepts + tab-
// completes at the literal level. Typed brigadier arg parsers (integer/vec3/entity/gamemode) are a
// follow-up; the wire graph stays valid because every arg is a brigadier:string node.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/imhinotori/sulfur/server/command"
)

// gameMode ids (GameType.getId): survival 0 / creative 1 are in play_join.go; adventure 2 /
// spectator 3 are added here for /gamemode. gameEventChangeGameMode is ClientboundGameEvent's
// CHANGE_GAME_MODE type id (3): the client updates its local GameType from the Float param.
//
//	[VERIFIED javap net.minecraft.world.level.GameType: SURVIVAL 0, CREATIVE 1, ADVENTURE 2,
//	 SPECTATOR 3; ClientboundGameEvent Type.CHANGE_GAME_MODE id == 3.]
const (
	gameModeAdventure       = 2
	gameModeSpectator       = 3
	gameEventChangeGameMode = 3
)

// registerVanillaCommands adds the vanilla command literals to g. Called from buildCommandGraph
// after the dev commands. Every command is permissionGated on minecraft.command.<name>.
func registerVanillaCommands(g *command.Graph) {
	registerGameMode(g)
	registerOpDeop(g)
	registerKill(g)
	registerList(g)
	registerSeed(g)
	registerHelp(g)
	registerMsg(g)
	registerGamerule(g)
}

// /gamerule <rule> [value] — query or set a game rule on the level's GameRules store (gamerules.go).
// No value -> query (commands.gamerule.query "%s is currently set to: %s"); a value -> set
// (commands.gamerule.set "Gamerule %s is now set to: %s"). A boolean rule accepts true/false; an integer
// rule parses a decimal int. An unknown rule id errors. v1 uses the greedy-string in-handler parse (the /tp
// posture); a typed brigadier gamerule arg is a follow-up.
//
//	[VERIFIED javap net.minecraft.server.commands.GameRuleCommand: query "commands.gamerule.query",
//	 set "commands.gamerule.set"; GameRule.set / GameRules.getRule.]
func registerGamerule(g *command.Graph) {
	h := permissionGated("minecraft.command.gamerule", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok || e.p == nil {
			return nil
		}
		if len(args) == 0 {
			return errGameruleUsage
		}
		raw := commandRaw(args)
		fields := strings.Fields(strings.TrimSpace(raw))
		if len(fields) == 0 {
			return errGameruleUsage
		}
		if e.t.gamerules == nil {
			e.t.gamerules = newGameRules()
		}
		rule := fields[0]
		gr := e.t.gamerules
		// A rule is a boolean OR integer rule (or neither -> unknown).
		_, isBool := gr.bools[rule]
		_, isInt := gr.ints[rule]
		if !isBool && !isInt {
			return fmt.Errorf("unknown game rule: %s", rule)
		}
		// Query (no value provided): report the current value.
		if len(fields) == 1 {
			if isBool {
				e.t.sendSystemChat(e.p, fmt.Sprintf("%s is currently set to: %t", rule, gr.getBool(rule)))
			} else {
				e.t.sendSystemChat(e.p, fmt.Sprintf("%s is currently set to: %d", rule, gr.getInt(rule)))
			}
			return nil
		}
		// Set: parse the value against the rule's type.
		val := fields[1]
		if isBool {
			switch strings.ToLower(val) {
			case "true":
				gr.setBool(rule, true)
			case "false":
				gr.setBool(rule, false)
			default:
				return fmt.Errorf("invalid boolean for %s: %s (expected true/false)", rule, val)
			}
			e.t.sendSystemChat(e.p, fmt.Sprintf("Gamerule %s is now set to: %t", rule, gr.getBool(rule)))
		} else {
			n, perr := strconv.Atoi(val)
			if perr != nil {
				return fmt.Errorf("invalid integer for %s: %s", rule, val)
			}
			gr.setInt(rule, n)
			e.t.sendSystemChat(e.p, fmt.Sprintf("Gamerule %s is now set to: %d", rule, gr.getInt(rule)))
		}
		return nil
	})
	// GameRuleCommand vanilla is a literal-per-rule + a typed value arg; we keep a single
	// brigadier:string word rule node (id 5) whose suggestions are the rule names, then an
	// optional greedy value node. Handler parses rule+value from the rebuilt tail.
	valueArg := g.Argument("value", command.StringParser(2)).HandleFunc(h)
	ruleArg := g.Argument("rule", command.StringParser(0)).Suggests(command.SuggestGamerule, "").AppendArgument(valueArg).HandleFunc(h)
	g.AppendLiteral(g.Literal("gamerule").AppendArgument(ruleArg).Unhandle())
}

// errGameruleUsage is the /gamerule usage error (no rule given).
var errGameruleUsage = errors.New("usage: /gamerule <rule> [value]")

// /gamemode <mode> [player] — set the issuer's (or a target's) GameType. mode accepts the vanilla
// name (survival/creative/adventure/spectator) or its short/number form (s/c/a/sp, 0/1/2/3).
//
//	[VERIFIED javap net.minecraft.server.commands.GameModeCommand: setGameMode -> player.setGameMode
//	 (mode); the client is told via the CHANGE_GAME_MODE game event + the player-info gamemode update.]
func registerGameMode(g *command.Graph) {
	h := permissionGated("minecraft.command.gamemode", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok {
			return nil
		}
		if len(args) == 0 {
			return errors.New("usage: /gamemode <survival|creative|adventure|spectator> [player]")
		}
		raw := commandRaw(args)
		fields := strings.Fields(raw)
		if len(fields) == 0 {
			return errors.New("usage: /gamemode <mode> [player]")
		}
		mode, err := parseGameMode(fields[0])
		if err != nil {
			return err
		}
		target := e.p
		if len(fields) >= 2 {
			if tp := e.t.playerByName(fields[1]); tp != nil {
				target = tp
			} else {
				return fmt.Errorf("player not found: %s", fields[1])
			}
		}
		e.t.setPlayerGameMode(target, mode)
		e.t.sendSystemChat(e.p, "Set game mode to "+gameModeName(mode)+" for "+target.name)
		return nil
	})
	// Typed tree mirroring GameModeCommand: <gamemode> then an optional <target:entity>.
	// mode -> minecraft:gamemode (id 42, no props); player -> minecraft:entity (id 6,
	// single+playersOnly). Both nodes run h; commandRaw rebuilds the greedy tail the handler parses.
	playerArg := g.Argument("target", command.EntityParser{Single: true, PlayersOnly: true}).Suggests(command.SuggestPlayers, "").HandleFunc(h)
	modeArg := g.Argument("gamemode", command.GameModeParser()).AppendArgument(playerArg).HandleFunc(h)
	g.AppendLiteral(g.Literal("gamemode").AppendArgument(modeArg).Unhandle())
}

// /op <player> and /deop <player> — grant/revoke operator (the "*" permission node) on a player by
// name. Persists via the permission store's dirty flag (saved by RunSaveLoop). Vanilla ops also
// gain the operator permission level; here the "*" node grants every command node.
//
//	[VERIFIED javap net.minecraft.server.commands.OpCommand/DeopCommand: PlayerList.op/deop mutate
//	 the ops list; the effect is "this player may run privileged commands".]
func registerOpDeop(g *command.Graph) {
	mk := func(node string, op bool) command.HandlerFunc {
		return permissionGated(node, func(ctx context.Context, args []command.ParsedData) error {
			e, ok := executorFrom(ctx)
			if !ok {
				return nil
			}
			if len(args) == 0 {
				return errors.New("usage: /op <player>")
			}
			name := strings.TrimSpace(pickWord(args))
			if name == "" {
				return errors.New("usage: /op <player>")
			}
			target := e.t.playerByName(name)
			if target == nil {
				return fmt.Errorf("player not found: %s", name)
			}
			if e.t.perms == nil {
				return errors.New("no permission store configured")
			}
			changed := e.t.perms.setOp(target.uuid, target.name, op)
			verb := "opped"
			if !op {
				verb = "de-opped"
			}
			if changed {
				e.t.sendSystemChat(e.p, "Made "+target.name+" a server operator ("+verb+")")
			} else {
				e.t.sendSystemChat(e.p, "Nothing changed. "+target.name+" already "+verb)
			}
			return nil
		})
	}
	// OpCommand/DeopCommand target a <targets:game_profile> (id 7, no props).
	opArg := g.Argument("targets", command.GameProfileParser()).Suggests(command.SuggestPlayers, "").HandleFunc(mk("minecraft.command.op", true))
	g.AppendLiteral(g.Literal("op").AppendArgument(opArg).Unhandle())
	deopArg := g.Argument("targets", command.GameProfileParser()).Suggests(command.SuggestPlayers, "").HandleFunc(mk("minecraft.command.deop", false))
	g.AppendLiteral(g.Literal("deop").AppendArgument(deopArg).Unhandle())
}

// /kill [player] — kill the issuer (or a named target) via the authoritative death path (die()).
//
//	[VERIFIED javap net.minecraft.server.commands.KillCommand: entity.kill() -> hurt(OUT_OF_WORLD,
//	 Float.MAX_VALUE) / die(). v1 routes through die() (the PlayerCombatKill + respawn-screen path).]
func registerKill(g *command.Graph) {
	h := permissionGated("minecraft.command.kill", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok {
			return nil
		}
		target := e.p
		if name := strings.TrimSpace(pickWord(args)); name != "" {
			if tp := e.t.playerByName(name); tp != nil {
				target = tp
			} else {
				return fmt.Errorf("player not found: %s", name)
			}
		}
		target.health = 0
		e.t.die(target)
		return nil
	})
	// KillCommand: /kill (self) + /kill <targets:entity> (id 6, no single/playersOnly flags).
	arg := g.Argument("targets", command.EntityParser{}).Suggests(command.SuggestPlayers, "").HandleFunc(h)
	g.AppendLiteral(g.Literal("kill").AppendArgument(arg).HandleFunc(h))
}

// /list — list online players (name + count). Vanilla PlayerList reply.
//
//	[VERIFIED javap net.minecraft.server.commands.ListCommand: "There are %s of a max of %s players
//	 online: %s".]
func registerList(g *command.Graph) {
	h := permissionGated("minecraft.command.list", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok {
			return nil
		}
		names := make([]string, 0, len(e.t.players))
		for _, p := range e.t.players {
			names = append(names, p.name)
		}
		e.t.sendSystemChat(e.p, fmt.Sprintf("There are %d players online: %s", len(names), strings.Join(names, ", ")))
		return nil
	})
	g.AppendLiteral(g.Literal("list").HandleFunc(h))
}

// /seed — report the world seed.
//
//	[VERIFIED javap net.minecraft.server.commands.SeedCommand: "Seed: [%s]".]
func registerSeed(g *command.Graph) {
	h := permissionGated("minecraft.command.seed", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok {
			return nil
		}
		e.t.sendSystemChat(e.p, fmt.Sprintf("Seed: [%d]", e.t.worldSeed))
		return nil
	})
	g.AppendLiteral(g.Literal("seed").HandleFunc(h))
}

// /help — list the available commands. A minimal help (vanilla lists the command tree usages).
func registerHelp(g *command.Graph) {
	h := permissionGated("minecraft.command.help", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok {
			return nil
		}
		e.t.sendSystemChat(e.p, "Commands: /gamemode /gamerule /op /deop /kill /list /seed /msg /say /me /tp /help")
		return nil
	})
	g.AppendLiteral(g.Literal("help").HandleFunc(h))
}

// /msg <player> <message> — private message to a named player (echoed to sender).
//
//	[VERIFIED javap net.minecraft.server.commands.MsgCommand: ChatType.MSG_COMMAND_INCOMING/OUTGOING
//	 "You whisper to %s: %s" / "%s whispers to you: %s".]
func registerMsg(g *command.Graph) {
	h := permissionGated("minecraft.command.msg", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok {
			return nil
		}
		raw := commandRaw(args)
		parts := strings.SplitN(strings.TrimSpace(raw), " ", 2)
		if len(parts) < 2 {
			return errors.New("usage: /msg <player> <message>")
		}
		target := e.t.playerByName(parts[0])
		if target == nil {
			return fmt.Errorf("player not found: %s", parts[0])
		}
		e.t.sendSystemChat(target, e.p.name+" whispers to you: "+parts[1])
		e.t.sendSystemChat(e.p, "You whisper to "+target.name+": "+parts[1])
		return nil
	})
	// MsgCommand: <targets:entity> <message:message>. entity id 6 (single false), message id 20
	// (greedy). Handler parses "target rest..." from the rebuilt tail exactly as before.
	msgArg := g.Argument("message", command.MessageParser()).HandleFunc(h)
	tgtArg := g.Argument("targets", command.EntityParser{}).Suggests(command.SuggestPlayers, "").AppendArgument(msgArg).Unhandle()
	g.AppendLiteral(g.Literal("msg").AppendArgument(tgtArg).Unhandle())
}

// --- helpers -------------------------------------------------------------------------------

// pickWord returns the last parsed arg as a trimmed string (the greedy/word capture), "" if none.
func pickWord(args []command.ParsedData) string {
	if len(args) == 0 {
		return ""
	}
	return strings.TrimSpace(commandRaw(args))
}

// parseGameMode maps a vanilla mode name/short/number to a GameType id.
func parseGameMode(s string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "survival", "s", "0":
		return gameModeSurvival, nil
	case "creative", "c", "1":
		return gameModeCreative, nil
	case "adventure", "a", "2":
		return gameModeAdventure, nil
	case "spectator", "sp", "3":
		return gameModeSpectator, nil
	}
	return 0, fmt.Errorf("unknown game mode: %s (survival|creative|adventure|spectator)", s)
}

// gameModeName is the display name for a GameType id.
func gameModeName(mode int) string {
	switch mode {
	case gameModeSurvival:
		return "Survival Mode"
	case gameModeCreative:
		return "Creative Mode"
	case gameModeAdventure:
		return "Adventure Mode"
	case gameModeSpectator:
		return "Spectator Mode"
	}
	return "Unknown"
}

// setPlayerGameMode sets the player's GameType + tells the client via the CHANGE_GAME_MODE game
// event (the client updates its local mode: creative flight, block-break rules, etc.). Tick-owned.
//
//	[VERIFIED javap ServerPlayer.setGameMode -> gameMode.setGameModeForPlayer; the client receives
//	 ClientboundGameEvent(CHANGE_GAME_MODE, (float) gameType.getId()).]
func (t *TickLoop) setPlayerGameMode(p *tickPlayer, mode int) {
	p.gameMode = int32(mode)
	if p.client != nil {
		p.client.Send(writeGameEventPacket(gameEventChangeGameMode, float32(mode)))
	}
}

// playerByName resolves an online player by (case-insensitive) login name, or nil. Tick-owned scan
// (player counts are small).
func (t *TickLoop) playerByName(name string) *tickPlayer {
	for _, p := range t.players {
		if strings.EqualFold(p.name, name) {
			return p
		}
	}
	return nil
}
