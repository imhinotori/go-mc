package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/imhinotori/sulfur/data/item"
	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/server/command"
)

func registerOpsCommands(g *command.Graph) {
	registerGive(g)
	registerTeleport(g)
	registerTime(g)
	registerEffect(g)
	registerSetblock(g)
	registerSummon(g)
}

const giveMaxAllowedItemStacks = 100

var errGiveUsage = errors.New("usage: /give <player> <item> [count]")

func registerGive(g *command.Graph) {
	h := permissionGated("minecraft.command.give", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok {
			return nil
		}
		fields := commandFields(args)
		if len(fields) < 2 {
			return errGiveUsage
		}
		targets, err := e.t.resolveTargets(e.p, fields[0])
		if err != nil {
			return err
		}
		it, ok := itemByRegistryName(fields[1])
		if !ok {
			return fmt.Errorf("unknown item: %s", fields[1])
		}
		count := 1
		if len(fields) >= 3 {
			n, perr := strconv.Atoi(fields[2])
			if perr != nil || n < 1 {
				return fmt.Errorf("invalid count: %s", fields[2])
			}
			count = n
		}
		maxStack := int(it.StackSize)
		if maxStack <= 0 {
			maxStack = 64
		}
		maxAllowed := maxStack * giveMaxAllowedItemStacks
		if count > maxAllowed {
			return fmt.Errorf("Can't give more than %d of %s", maxAllowed, it.DisplayName)
		}
		for _, tp := range targets {
			e.t.giveItemToPlayer(tp, it, maxStack, count)
		}
		e.t.sendSystemChat(e.p, fmt.Sprintf("Gave %d %s to %s", count, it.DisplayName, targets[0].name))
		return nil
	})
	arg := g.Argument("args", command.StringParser(2)).HandleFunc(h)
	g.AppendLiteral(g.Literal("give").AppendArgument(arg).Unhandle())
}

func (t *TickLoop) giveItemToPlayer(p *tickPlayer, it *item.Item, maxStack, count int) {
	if p == nil {
		return
	}
	inv := ensureInventory(p)
	before := inv.snapshot()
	remaining := count
	for remaining > 0 {
		n := maxStack
		if remaining < n {
			n = remaining
		}
		remaining -= n
		part := component.SlotData{ItemID: pk.VarInt(it.ID), Count: pk.VarInt(n)}
		added := t.inventoryAdd(p, inv, &part)
		if added && part.Count <= 0 {
			continue
		}
		if part.Count > 0 {
			t.playerDrop(p, part, false)
		}
	}
	t.broadcastInventoryChanges(p, inv, before)
}

var errTeleportUsage = errors.New("usage: /tp [target] <x> <y> <z> | /tp [target] <player>")

func registerTeleport(g *command.Graph) {
	h := permissionGated("minecraft.command.tp", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok {
			return nil
		}
		fields := commandFields(args)
		if len(fields) == 0 {
			return errTeleportUsage
		}
		switch len(fields) {
		case 1:
			dest := e.t.playerByName(fields[0])
			if dest == nil {
				return fmt.Errorf("player not found: %s", fields[0])
			}
			e.t.teleportPlayer(e.p, dest.x, dest.y, dest.z)
			e.t.sendSystemChat(e.p, fmt.Sprintf("Teleported %s to %s", e.p.name, dest.name))
			return nil
		case 2:
			targets, err := e.t.resolveTargets(e.p, fields[0])
			if err != nil {
				return err
			}
			dest := e.t.playerByName(fields[1])
			if dest == nil {
				return fmt.Errorf("player not found: %s", fields[1])
			}
			for _, tp := range targets {
				e.t.teleportPlayer(tp, dest.x, dest.y, dest.z)
			}
			e.t.sendSystemChat(e.p, fmt.Sprintf("Teleported %s to %s", targets[0].name, dest.name))
			return nil
		case 3:
			x, y, z, err := parseTpCoordsRelative(fields[0], fields[1], fields[2], e.p.x, e.p.y, e.p.z)
			if err != nil {
				return err
			}
			e.t.teleportPlayer(e.p, x, y, z)
			e.t.sendSystemChat(e.p, fmt.Sprintf("Teleported %s to %s, %s, %s", e.p.name, formatTpCoord(x), formatTpCoord(y), formatTpCoord(z)))
			return nil
		case 4:
			targets, err := e.t.resolveTargets(e.p, fields[0])
			if err != nil {
				return err
			}
			for _, tp := range targets {
				x, y, z, cerr := parseTpCoordsRelative(fields[1], fields[2], fields[3], tp.x, tp.y, tp.z)
				if cerr != nil {
					return cerr
				}
				e.t.teleportPlayer(tp, x, y, z)
			}
			x, y, z, _ := parseTpCoordsRelative(fields[1], fields[2], fields[3], targets[0].x, targets[0].y, targets[0].z)
			e.t.sendSystemChat(e.p, fmt.Sprintf("Teleported %s to %s, %s, %s", targets[0].name, formatTpCoord(x), formatTpCoord(y), formatTpCoord(z)))
			return nil
		default:
			return errTeleportUsage
		}
	})
	arg := g.Argument("args", command.StringParser(2)).HandleFunc(h)
	g.AppendLiteral(g.Literal("tp").AppendArgument(arg).HandleFunc(h))
}

const (
	timeMarkerDay      = 1000
	timeMarkerNoon     = 6000
	timeMarkerNight    = 13000
	timeMarkerMidnight = 18000
	dayLengthTicksTime = 24000
)

var errTimeUsage = errors.New("usage: /time set <value|day|noon|night|midnight> | /time add <value> | /time query <daytime|gametime|day>")

func registerTime(g *command.Graph) {
	h := permissionGated("minecraft.command.time", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok {
			return nil
		}
		fields := commandFields(args)
		if len(fields) < 2 {
			return errTimeUsage
		}
		switch strings.ToLower(fields[0]) {
		case "set":
			ticks, err := parseTimeValue(fields[1])
			if err != nil {
				return err
			}
			e.t.setDayTime(ticks)
			e.t.broadcastClockModify() // ServerClockManager.modifyClock broadcast (setTotalTicks)
			e.t.sendSystemChat(e.p, fmt.Sprintf("Set the time to %d", ticks))
			return nil
		case "add":
			ticks, err := parseTimeValue(fields[1])
			if err != nil {
				return err
			}
			e.t.gametime += int64(ticks)
			e.t.broadcastClockModify() // ServerClockManager.modifyClock broadcast (addTicks)
			e.t.sendSystemChat(e.p, fmt.Sprintf("Set the time to %d", int(e.t.gametime%dayLengthTicksTime)))
			return nil
		case "query":
			switch strings.ToLower(fields[1]) {
			case "gametime":
				e.t.sendSystemChat(e.p, fmt.Sprintf("The game time is %d tick(s)", e.t.gametime))
			case "daytime":
				e.t.sendSystemChat(e.p, fmt.Sprintf("The time is %d", e.t.gametime%dayLengthTicksTime))
			case "day":
				e.t.sendSystemChat(e.p, fmt.Sprintf("The day is %d", e.t.gametime/dayLengthTicksTime))
			default:
				return errTimeUsage
			}
			return nil
		default:
			return errTimeUsage
		}
	})
	arg := g.Argument("args", command.StringParser(2)).HandleFunc(h)
	g.AppendLiteral(g.Literal("time").AppendArgument(arg).Unhandle())
}

func (t *TickLoop) setDayTime(ticks int) {
	day := int64(dayLengthTicksTime)
	target := int64(((ticks % dayLengthTicksTime) + dayLengthTicksTime) % dayLengthTicksTime)
	base := t.gametime - t.gametime%day
	newTime := base + target
	if newTime < t.gametime {
		newTime += day
	}
	t.gametime = newTime
}

func parseTimeValue(s string) (int, error) {
	switch strings.ToLower(s) {
	case "day":
		return timeMarkerDay, nil
	case "noon":
		return timeMarkerNoon, nil
	case "night":
		return timeMarkerNight, nil
	case "midnight":
		return timeMarkerMidnight, nil
	}
	unit := 1
	num := s
	if n := len(s); n > 0 {
		switch s[n-1] {
		case 'd', 'D':
			unit, num = dayLengthTicksTime, s[:n-1]
		case 's', 'S':
			unit, num = 20, s[:n-1]
		case 't', 'T':
			unit, num = 1, s[:n-1]
		}
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid time: %s", s)
	}
	return int(f * float64(unit)), nil
}

const (
	effectDefaultDurationTicks = 600
	effectMaxSeconds           = 1000000
	effectMaxAmplifier         = 255
)

var errEffectUsage = errors.New("usage: /effect give <target> <effect> [seconds] [amplifier] [hideParticles] | /effect clear <target> [effect]")

func registerEffect(g *command.Graph) {
	h := permissionGated("minecraft.command.effect", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok {
			return nil
		}
		fields := commandFields(args)
		if len(fields) < 2 {
			return errEffectUsage
		}
		switch strings.ToLower(fields[0]) {
		case "give":
			if len(fields) < 3 {
				return errEffectUsage
			}
			targets, err := e.t.resolveTargets(e.p, fields[1])
			if err != nil {
				return err
			}
			id := normalizeEffectID(strings.ToLower(strings.TrimSpace(fields[2])))
			instant := isInstantEffect(id)
			var duration int
			if len(fields) >= 4 {
				secs, perr := strconv.Atoi(fields[3])
				if perr != nil || (secs != -1 && (secs < 1 || secs > effectMaxSeconds)) {
					return fmt.Errorf("invalid seconds: %s", fields[3])
				}
				switch {
				case instant:
					duration = secs
				case secs == -1:
					duration = -1
				default:
					duration = secs * 20
				}
			} else if instant {
				duration = 1
			} else {
				duration = effectDefaultDurationTicks
			}
			amplifier := 0
			if len(fields) >= 5 {
				amp, perr := strconv.Atoi(fields[4])
				if perr != nil || amp < 0 || amp > effectMaxAmplifier {
					return fmt.Errorf("invalid amplifier: %s", fields[4])
				}
				amplifier = amp
			}
			if len(fields) >= 6 {
				if _, perr := strconv.ParseBool(fields[5]); perr != nil {
					return fmt.Errorf("invalid hideParticles: %s", fields[5])
				}
			}
			applied := 0
			for _, tp := range targets {
				before := len(tp.activeEffects)
				live := tp != nil && !tp.dead
				e.t.addPlayerEffect(tp, 0, id, duration, amplifier, 1.0)
				if instant {
					if live {
						applied++
					}
				} else if len(tp.activeEffects) > before || playerHasEffect(tp, id) {
					applied++
				}
			}
			if applied == 0 {
				return errors.New("Unable to apply this effect (target is either immune to effects, or has something stronger)")
			}
			e.t.sendSystemChat(e.p, fmt.Sprintf("Applied effect %s to %s", id, targets[0].name))
			return nil
		case "clear":
			targets, err := e.t.resolveTargets(e.p, fields[1])
			if err != nil {
				return err
			}
			if len(fields) >= 3 {
				id := normalizeEffectID(strings.ToLower(strings.TrimSpace(fields[2])))
				removed := 0
				for _, tp := range targets {
					if playerHasEffect(tp, id) {
						e.t.clearPlayerEffect(tp, id)
						removed++
					}
				}
				if removed == 0 {
					return errors.New("Target doesn't have the requested effect")
				}
				e.t.sendSystemChat(e.p, fmt.Sprintf("Removed effect %s from %s", id, targets[0].name))
				return nil
			}
			removed := 0
			for _, tp := range targets {
				if len(tp.activeEffects) > 0 {
					removed += len(tp.activeEffects)
					e.t.clearAllPlayerEffects(tp)
				}
			}
			if removed == 0 {
				return errors.New("Target has no effects to remove")
			}
			e.t.sendSystemChat(e.p, fmt.Sprintf("Removed every effect from %s", targets[0].name))
			return nil
		default:
			return errEffectUsage
		}
	})
	arg := g.Argument("args", command.StringParser(2)).HandleFunc(h)
	g.AppendLiteral(g.Literal("effect").AppendArgument(arg).Unhandle())
}

func (t *TickLoop) clearPlayerEffect(p *tickPlayer, id string) {
	if p == nil || p.activeEffects == nil {
		return
	}
	if _, ok := p.activeEffects[id]; !ok {
		return
	}
	delete(p.activeEffects, id)
	t.removeEffectModifiers(p, id)
	t.onPlayerEffectRemoved(p, id)
}

func (t *TickLoop) clearAllPlayerEffects(p *tickPlayer) {
	if p == nil || p.activeEffects == nil {
		return
	}
	for id := range p.activeEffects {
		delete(p.activeEffects, id)
		t.removeEffectModifiers(p, id)
		t.onPlayerEffectRemoved(p, id)
	}
}

var errSetblockUsage = errors.New("usage: /setblock <x> <y> <z> <block> [replace|destroy|keep]")

func registerSetblock(g *command.Graph) {
	h := permissionGated("minecraft.command.setblock", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok {
			return nil
		}
		fields := commandFields(args)
		if len(fields) < 4 {
			return errSetblockUsage
		}
		bx, err := parseBlockCoord(fields[0], e.p.x)
		if err != nil {
			return err
		}
		by, err := parseBlockCoord(fields[1], e.p.y)
		if err != nil {
			return err
		}
		bz, err := parseBlockCoord(fields[2], e.p.z)
		if err != nil {
			return err
		}
		state, ok2 := blockStateByName(fields[3])
		if !ok2 {
			return fmt.Errorf("unknown block: %s", fields[3])
		}
		mode := "replace"
		if len(fields) >= 5 {
			mode = strings.ToLower(fields[4])
			if mode != "replace" && mode != "destroy" && mode != "keep" {
				return errSetblockUsage
			}
		}
		if e.t.world() == nil {
			return errors.New("Could not set the block")
		}
		pos := pk.Position{X: bx, Y: by, Z: bz}
		cur, _ := e.t.world().GetBlock(pos, dimMinY)
		switch mode {
		case "keep":
			if !block.IsAir(cur) {
				return errors.New("Could not set the block")
			}
		case "destroy":
			if !(block.IsAir(state) && block.IsAir(cur)) && !block.IsAir(cur) {
				e.t.spawnBlockDrop(e.p, pos, cur)
			}
		}
		if !e.t.world().SetBlock(pos, state, dimMinY) {
			return errors.New("Could not set the block")
		}
		e.t.broadcastBlockUpdate(pos, state)
		e.t.sendSystemChat(e.p, fmt.Sprintf("Changed the block at %d, %d, %d", bx, by, bz))
		return nil
	})
	arg := g.Argument("args", command.StringParser(2)).HandleFunc(h)
	g.AppendLiteral(g.Literal("setblock").AppendArgument(arg).Unhandle())
}

var errSummonUsage = errors.New("usage: /summon <entity> [x] [y] [z]")

func registerSummon(g *command.Graph) {
	h := permissionGated("minecraft.command.summon", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok {
			return nil
		}
		fields := commandFields(args)
		if len(fields) < 1 {
			return errSummonUsage
		}
		regName, ok2 := summonRegistryName(e.t, fields[0])
		if !ok2 {
			return fmt.Errorf("Unable to summon entity: %s", fields[0])
		}
		x, y, z := e.p.x, e.p.y, e.p.z
		if len(fields) >= 4 {
			var err error
			if x, err = parseTpCoord(fields[1], e.p.x); err != nil {
				return err
			}
			if y, err = parseTpCoord(fields[2], e.p.y); err != nil {
				return err
			}
			if z, err = parseTpCoord(fields[3], e.p.z); err != nil {
				return err
			}
		}
		ent := e.t.spawnVanillaMob(regName, x, y, z)
		if ent == nil {
			return errors.New("Unable to summon entity")
		}
		e.t.sendSystemChat(e.p, fmt.Sprintf("Summoned new %s", fields[0]))
		return nil
	})
	arg := g.Argument("args", command.StringParser(2)).HandleFunc(h)
	g.AppendLiteral(g.Literal("summon").AppendArgument(arg).Unhandle())
}

func summonRegistryName(t *TickLoop, raw string) (string, bool) {
	name := strings.ToLower(strings.TrimSpace(raw))
	name = strings.TrimPrefix(name, "minecraft:")
	if name == "" || t.mobRegistry == nil {
		return "", false
	}
	regName := "vanilla_" + name
	if _, ok := t.mobRegistry.byName[regName]; ok {
		return regName, true
	}
	return "", false
}

func commandFields(args []command.ParsedData) []string {
	if len(args) == 0 {
		return nil
	}
	raw, _ := args[len(args)-1].(string)
	return strings.Fields(strings.TrimSpace(raw))
}

func (t *TickLoop) resolveTargets(self *tickPlayer, token string) ([]*tickPlayer, error) {
	switch strings.ToLower(token) {
	case "@s", "@p":
		if self == nil {
			return nil, errors.New("no self target")
		}
		return []*tickPlayer{self}, nil
	case "@a":
		out := make([]*tickPlayer, 0, len(t.players))
		for _, p := range t.players {
			out = append(out, p)
		}
		if len(out) == 0 {
			return nil, errors.New("no players online")
		}
		return out, nil
	}
	if tp := t.playerByName(token); tp != nil {
		return []*tickPlayer{tp}, nil
	}
	return nil, fmt.Errorf("player not found: %s", token)
}

func itemByRegistryName(raw string) (*item.Item, bool) {
	name := strings.ToLower(strings.TrimSpace(raw))
	name = strings.TrimPrefix(name, "minecraft:")
	for id := range item.ByID {
		it := item.ByID[id]
		if it != nil && it.Name == name {
			return it, true
		}
	}
	return nil, false
}

func blockStateByName(raw string) (block.StateID, bool) {
	name := strings.ToLower(strings.TrimSpace(raw))
	if !strings.Contains(name, ":") {
		name = "minecraft:" + name
	}
	s, ok := block.DefaultStateID[name]
	return s, ok
}

func parseTpCoord(s string, base float64) (float64, error) {
	if strings.HasPrefix(s, "~") {
		rest := strings.TrimPrefix(s, "~")
		if rest == "" {
			return base, nil
		}
		d, err := strconv.ParseFloat(rest, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid coordinate: %s", s)
		}
		return base + d, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid coordinate: %s", s)
	}
	return v, nil
}

func parseTpCoordsRelative(sx, sy, sz string, bx, by, bz float64) (x, y, z float64, err error) {
	if x, err = parseTpCoord(sx, bx); err != nil {
		return
	}
	if y, err = parseTpCoord(sy, by); err != nil {
		return
	}
	if z, err = parseTpCoord(sz, bz); err != nil {
		return
	}
	return
}

func parseBlockCoord(s string, base float64) (int, error) {
	if strings.HasPrefix(s, "~") {
		rest := strings.TrimPrefix(s, "~")
		if rest == "" {
			return int(math.Floor(base)), nil
		}
		d, err := strconv.ParseFloat(rest, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid coordinate: %s", s)
		}
		return int(math.Floor(base)) + int(math.Floor(d)), nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid coordinate: %s", s)
	}
	return int(math.Floor(v)), nil
}

func formatTpCoord(v float64) string {
	if v == math.Trunc(v) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}
