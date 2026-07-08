package server

// commands_batch.go -- SECOND wave of operator commands (/clear /xp /weather /difficulty /enchant
// /fill), ported 1:1 from net.minecraft.server.commands.* in the 26.2 jar. Mirrors commands_ops.go:
// a permissionGated handler on minecraft.command.<name>, greedy StringParser(2) in-handler parse,
// vanilla feedback via sendSystemChat, tick-owned mutation. Reuses t.setWeatherParameters (weather.go),
// the xp helpers (xp_orb.go + xp_levels.go), the world SetBlock/drop, and enchantApplyOffers.
//
// PIG-ORACLE SAFETY: every command is a pure operator action unrelated to the pig serverAiStep RNG
// stream, so the pig oracle stays byte-identical.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/imhinotori/sulfur/level/block"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/server/command"
)

func registerBatchCommands(g *command.Graph) {
	registerClear(g)
	registerXp(g)
	registerWeather(g)
	registerDifficulty(g)
	registerEnchant(g)
	registerFill(g)
}

var errClearUsage = errors.New("usage: /clear [target] [item]")

// registerClear ports ClearInventoryCommands. Removes matching items from the targets inventory +
// carried cursor. No item arg == match everything (register lambdas return iconst_1). maxCount == -1
// (clearUnlimited) always REMOVES. count == 0 -> failure; else success feedback.
// VERIFIED javap ClearInventoryCommands.clearInventory: count += getInventory().clearOrCountMatchingItems
// (predicate, maxCount, craftSlots); count==0 -> ERROR_SINGLE/MULTIPLE; else commands.clear.success.*.
func registerClear(g *command.Graph) {
	h := permissionGated("minecraft.command.clear", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok {
			return nil
		}
		fields := commandFields(args)
		targetTok := "@s"
		if len(fields) >= 1 {
			targetTok = fields[0]
		}
		targets, err := e.t.resolveTargets(e.p, targetTok)
		if err != nil {
			return err
		}
		var wantID int32 = -1
		if len(fields) >= 2 {
			it, ok2 := itemByRegistryName(fields[1])
			if !ok2 {
				return fmt.Errorf("unknown item: %s", fields[1])
			}
			wantID = int32(it.ID)
		}
		total := 0
		for _, tp := range targets {
			total += e.t.clearOrCountMatchingItems(tp, wantID, -1)
		}
		if total == 0 {
			if len(targets) == 1 {
				return fmt.Errorf("No items were found on player %s", targets[0].name)
			}
			return fmt.Errorf("No items were found on %d players", len(targets))
		}
		if len(targets) == 1 {
			e.t.sendSystemChat(e.p, fmt.Sprintf("Removed %d item(s) from player %s", total, targets[0].name))
		} else {
			e.t.sendSystemChat(e.p, fmt.Sprintf("Removed %d item(s) from %d players", total, len(targets)))
		}
		return nil
	})
	arg := g.Argument("args", command.StringParser(2)).HandleFunc(h)
	g.AppendLiteral(g.Literal("clear").AppendArgument(arg).HandleFunc(h))
}

// clearOrCountMatchingItems ports Inventory.clearOrCountMatchingItems over the main inventory + carried
// cursor. maxCount == -1 (dryRun false) removes every matching stack (wantID < 0 == match everything)
// and returns the total item count removed. VERIFIED javap Inventory.clearOrCountMatchingItems: sum of
// ContainerHelper.clearOrCountMatchingItems over inventory + craft + carried; dryRun == (maxCount==0).
func (t *TickLoop) clearOrCountMatchingItems(p *tickPlayer, wantID int32, maxCount int) int {
	inv := ensureInventory(p)
	before := inv.snapshot()
	removed := 0
	matches := func(s component.SlotData) bool {
		if s.Count <= 0 {
			return false
		}
		return wantID < 0 || int32(s.ItemID) == wantID
	}
	dryRun := maxCount == 0
	for i := range inv.slots {
		s := inv.slots[i]
		if !matches(s) {
			continue
		}
		removed += int(s.Count)
		if !dryRun {
			inv.slots[i] = component.SlotData{Count: 0}
		}
	}
	if car := inv.getCarried(); matches(car) {
		removed += int(car.Count)
		if !dryRun {
			inv.setCarried(component.SlotData{Count: 0})
		}
	}
	if !dryRun && removed > 0 {
		t.broadcastInventoryChanges(p, inv, before)
	}
	return removed
}

var errXpUsage = errors.New("usage: /xp add|set <target> <amount> [points|levels] | /xp query <target> <points|levels>")

// registerXp ports ExperienceCommand. Type enum POINTS/LEVELS:
// POINTS.add = giveExperiencePoints; POINTS.set = n >= getXpNeededForNextLevel() ? false : setExperiencePoints(n),true.
// LEVELS.add = giveExperienceLevels; LEVELS.set = setExperienceLevels(n),true. query = experienceLevel or
// experienceProgress*getXpNeededForNextLevel(). set counts successes; zero -> ERROR_SET_POINTS_INVALID.
// Default sub-type is points. VERIFIED javap ExperienceCommand.addExperience/setExperience + Type lambdas.
func registerXp(g *command.Graph) {
	h := permissionGated("minecraft.command.xp", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok {
			return nil
		}
		fields := commandFields(args)
		if len(fields) < 2 {
			return errXpUsage
		}
		sub := strings.ToLower(fields[0])
		switch sub {
		case "add", "set":
			if len(fields) < 3 {
				return errXpUsage
			}
			targets, err := e.t.resolveTargets(e.p, fields[1])
			if err != nil {
				return err
			}
			amount, perr := strconv.Atoi(fields[2])
			if perr != nil {
				return fmt.Errorf("invalid amount: %s", fields[2])
			}
			levels := xpTypeIsLevels(fields, 3)
			unit := "points"
			if levels {
				unit = "levels"
			}
			if sub == "add" {
				for _, tp := range targets {
					if levels {
						e.t.giveExperienceLevels(tp, amount)
					} else {
						e.t.giveExperiencePoints(tp, amount)
					}
				}
				if len(targets) == 1 {
					e.t.sendSystemChat(e.p, fmt.Sprintf("Gave %d experience %s to %s", amount, unit, targets[0].name))
				} else {
					e.t.sendSystemChat(e.p, fmt.Sprintf("Gave %d experience %s to %d players", amount, unit, len(targets)))
				}
				return nil
			}
			if amount < 0 {
				return errors.New("invalid amount (set requires >= 0)")
			}
			successes := 0
			for _, tp := range targets {
				if levels {
					e.t.setExperienceLevels(tp, amount)
					successes++
				} else if amount < int(getXpNeededForNextLevel(tp.experienceLevel)) {
					e.t.setExperiencePoints(tp, amount)
					successes++
				}
			}
			if successes == 0 {
				return errors.New("Unable to set experience points above the maximum for the current level")
			}
			if len(targets) == 1 {
				e.t.sendSystemChat(e.p, fmt.Sprintf("Set %d experience %s on %s", amount, unit, targets[0].name))
			} else {
				e.t.sendSystemChat(e.p, fmt.Sprintf("Set %d experience %s on %d players", amount, unit, len(targets)))
			}
			return nil
		case "query":
			targets, err := e.t.resolveTargets(e.p, fields[1])
			if err != nil {
				return err
			}
			tp := targets[0]
			switch strings.ToLower(fields[len(fields)-1]) {
			case "levels":
				e.t.sendSystemChat(e.p, fmt.Sprintf("%s has %d experience levels", tp.name, tp.experienceLevel))
			case "points":
				pts := int(tp.experienceProgress * float32(getXpNeededForNextLevel(tp.experienceLevel)))
				e.t.sendSystemChat(e.p, fmt.Sprintf("%s has %d experience points", tp.name, pts))
			default:
				return errXpUsage
			}
			return nil
		default:
			return errXpUsage
		}
	})
	arg := g.Argument("args", command.StringParser(2)).HandleFunc(h)
	g.AppendLiteral(g.Literal("xp").AppendArgument(arg).Unhandle())
	arg2 := g.Argument("args", command.StringParser(2)).HandleFunc(h)
	g.AppendLiteral(g.Literal("experience").AppendArgument(arg2).Unhandle())
}

func xpTypeIsLevels(fields []string, i int) bool {
	return len(fields) > i && strings.ToLower(fields[i]) == "levels"
}

// weatherDefaultTime is WeatherCommand.DEFAULT_TIME: the -1 sentinel meaning no duration given (roll the
// provider). getDuration(source, -1, provider) == provider.sample(getLevel().getRandom()), else duration.
const weatherDefaultTime = -1

var errWeatherUsage = errors.New("usage: /weather <clear|rain|thunder> [duration]")

// registerWeather ports WeatherCommand. clear/rain/thunder [duration]:
// setClear   = setWeatherParameters(getDuration(d, RAIN_DELAY), 0, false, false)
// setRain    = setWeatherParameters(0, getDuration(d, RAIN_DURATION), true, false)
// setThunder = setWeatherParameters(0, getDuration(d, THUNDER_DURATION), true, true)
// VERIFIED javap WeatherCommand.setClear/setRain/setThunder: those exact setWeatherParameters(IIZZ) triples;
// getDuration samples the provider on -1 off getLevel().getRandom(). Reuses t.setWeatherParameters + the
// UniformInt providers (weather.go).
func registerWeather(g *command.Graph) {
	h := permissionGated("minecraft.command.weather", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok {
			return nil
		}
		fields := commandFields(args)
		if len(fields) < 1 {
			return errWeatherUsage
		}
		duration := int32(weatherDefaultTime)
		if len(fields) >= 2 {
			n, perr := strconv.Atoi(fields[1])
			if perr != nil || n < 1 {
				return fmt.Errorf("invalid duration: %s", fields[1])
			}
			duration = int32(n)
		}
		rnd := e.t.regions[globalRegion].levelRandom
		switch strings.ToLower(fields[0]) {
		case "clear":
			e.t.setWeatherParameters(weatherGetDuration(duration, weatherRainDelay, rnd), 0, false, false)
			e.t.sendSystemChat(e.p, "Set the weather to clear")
		case "rain":
			e.t.setWeatherParameters(0, weatherGetDuration(duration, weatherRainDuration, rnd), true, false)
			e.t.sendSystemChat(e.p, "Set the weather to rain")
		case "thunder":
			e.t.setWeatherParameters(0, weatherGetDuration(duration, weatherThunderDuration, rnd), true, true)
			e.t.sendSystemChat(e.p, "Set the weather to thunder")
		default:
			return errWeatherUsage
		}
		return nil
	})
	arg := g.Argument("args", command.StringParser(2)).HandleFunc(h)
	g.AppendLiteral(g.Literal("weather").AppendArgument(arg).Unhandle())
}

// weatherGetDuration ports WeatherCommand.getDuration: duration == -1 -> provider.sample(random), else
// duration. One nextInt draw off the global level RNG in the UniformInt form, outside the pig stream.
func weatherGetDuration(duration int32, provider uniformInt, rnd interface{ NextIntN(int32) int32 }) int32 {
	if duration == -1 {
		return provider.sample(rnd)
	}
	return duration
}

var errDifficultyUsage = errors.New("usage: /difficulty [peaceful|easy|normal|hard]")

// registerDifficulty ports DifficultyCommand. No arg == query (getDifficulty; commands.difficulty.query).
// A name literal sets via setDifficulty: already-that-difficulty -> ERROR_ALREADY_SAME_DIFFICULTY; else set
// + commands.difficulty.success. State lives in t.levelDifficulty (the gameplay hot paths keep reading the
// serverDifficulty const, scope-locked). VERIFIED javap DifficultyCommand.setDifficulty: worldData
// .getDifficulty()==arg -> throw ERROR_ALREADY_SAME_DIFFICULTY; else setDifficulty(arg,true) + sendSuccess.
func registerDifficulty(g *command.Graph) {
	h := permissionGated("minecraft.command.difficulty", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok {
			return nil
		}
		fields := commandFields(args)
		if len(fields) == 0 {
			e.t.sendSystemChat(e.p, fmt.Sprintf("The difficulty is %s", difficultyName(e.t.levelDifficulty)))
			return nil
		}
		want, ok2 := parseDifficulty(fields[0])
		if !ok2 {
			return errDifficultyUsage
		}
		if e.t.levelDifficulty == want {
			return fmt.Errorf("The difficulty did not change; it is already set to %s", difficultyName(want))
		}
		e.t.levelDifficulty = want
		e.t.sendSystemChat(e.p, fmt.Sprintf("Set the difficulty to %s", difficultyName(want)))
		return nil
	})
	arg := g.Argument("args", command.StringParser(2)).HandleFunc(h)
	g.AppendLiteral(g.Literal("difficulty").AppendArgument(arg).HandleFunc(h))
}

// parseDifficulty maps a serialized name to the difficulty enum. VERIFIED javap Difficulty.<clinit>:
// PEACEFUL peaceful 0, EASY easy 1, NORMAL normal 2, HARD hard 3.
func parseDifficulty(s string) (difficulty, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "peaceful", "p", "0":
		return difficultyPeaceful, true
	case "easy", "e", "1":
		return difficultyEasy, true
	case "normal", "n", "2":
		return difficultyNormal, true
	case "hard", "h", "3":
		return difficultyHard, true
	}
	return difficultyPeaceful, false
}

// difficultyName is Difficulty.getSerializedName() (the lowercase key), used for feedback.
func difficultyName(d difficulty) string {
	switch d {
	case difficultyPeaceful:
		return "peaceful"
	case difficultyEasy:
		return "easy"
	case difficultyNormal:
		return "normal"
	case difficultyHard:
		return "hard"
	}
	return "normal"
}

var errEnchantUsage = errors.New("usage: /enchant <target> <enchantment> [level]")

// registerEnchant ports EnchantCommand. Applies the enchant to each living targets mainhand item:
// level > getMaxLevel() -> ERROR_LEVEL_TOO_HIGH; empty stack -> ERROR_NO_ITEM (single); canEnchant(stack)
// && isEnchantmentCompatible(existing, holder) -> stack.enchant(holder, level), i++; else ERROR_INCOMPATIBLE
// (single); i==0 -> ERROR_NOTHING_HAPPENED. Reuses enchantApplyOffers (== ItemStack.enchant, upgrade to max),
// enchantCanEnchant, enchantAreCompatible, enchantMaxLevelOf. Default level 1. VERIFIED javap EnchantCommand.enchant.
func registerEnchant(g *command.Graph) {
	h := permissionGated("minecraft.command.enchant", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok {
			return nil
		}
		fields := commandFields(args)
		if len(fields) < 2 {
			return errEnchantUsage
		}
		targets, err := e.t.resolveTargets(e.p, fields[0])
		if err != nil {
			return err
		}
		id := normalizeEnchantResource(strings.ToLower(strings.TrimSpace(fields[1])))
		if enchantWireID(id) < 0 {
			return fmt.Errorf("unknown enchantment: %s", fields[1])
		}
		level := 1
		if len(fields) >= 3 {
			n, perr := strconv.Atoi(fields[2])
			if perr != nil {
				return fmt.Errorf("invalid level: %s", fields[2])
			}
			level = n
		}
		if maxLvl := enchantMaxLevelOf(id); level > maxLvl {
			return fmt.Errorf("Level %d is higher than the maximum of %d supported by this enchantment", level, maxLvl)
		}
		single := len(targets) == 1
		applied := 0
		for _, tp := range targets {
			inv := ensureInventory(tp)
			slot := heldWindowSlot(inv.heldSlot)
			stack := inv.get(slot)
			if stack.Count <= 0 {
				if single {
					return fmt.Errorf("%s is not holding any item", tp.name)
				}
				continue
			}
			compatible := true
			for existing := range stackEnchantments(stack) {
				if !enchantAreCompatible(existing, id) {
					compatible = false
					break
				}
			}
			if enchantCanEnchant(id, stack) && compatible {
				enchanted := enchantApplyOffers(stack, []enchantInstance{{id: id, level: level}})
				inv.set(slot, enchanted)
				applied++
			} else if single {
				return errors.New("The target item is incompatible with the provided enchantment")
			}
		}
		if applied == 0 {
			return errors.New("Nothing changed. The enchantments provided cannot be applied")
		}
		enchName := stripNamespace(id)
		if single {
			e.t.sendSystemChat(e.p, fmt.Sprintf("Applied enchantment %s to %s item", enchName, targets[0].name))
		} else {
			e.t.sendSystemChat(e.p, fmt.Sprintf("Applied enchantment %s to %d entities", enchName, applied))
		}
		for _, tp := range targets {
			e.t.sendContent(tp)
		}
		return nil
	})
	arg := g.Argument("args", command.StringParser(2)).HandleFunc(h)
	g.AppendLiteral(g.Literal("enchant").AppendArgument(arg).Unhandle())
}

// fillMaxBlocks is GameRules.MAX_BLOCK_MODIFICATIONS default (32768): the fill region volume may not
// exceed it or the command errors ERROR_AREA_TOO_LARGE. VERIFIED javap GameRules.<clinit>:
// registerInteger(max_block_modifications, MISC, 32768, 1); FillCommand.fillBlocks: if
// ((long)xSpan*ySpan*zSpan > maxBlockModifications) throw ERROR_AREA_TOO_LARGE.
const fillMaxBlocks = 32768

var errFillUsage = errors.New("usage: /fill <x1> <y1> <z1> <x2> <y2> <z2> <block> [replace|destroy|keep|hollow|outline]")

// registerFill ports FillCommand.fillBlocks. Fills the inclusive box; volume > MAX_BLOCK_MODIFICATIONS ->
// ERROR_AREA_TOO_LARGE. Per-mode: REPLACE places everywhere; DESTROY drops old then places; KEEP places
// only over air; HOLLOW shell solid + interior air; OUTLINE shell only. i==0 -> ERROR_FAILED; else
// commands.fill.success(i). Reuses world SetBlock + spawnBlockDrop. VERIFIED javap FillCommand.fillBlocks.
func registerFill(g *command.Graph) {
	h := permissionGated("minecraft.command.fill", func(ctx context.Context, args []command.ParsedData) error {
		e, ok := executorFrom(ctx)
		if !ok {
			return nil
		}
		fields := commandFields(args)
		if len(fields) < 7 {
			return errFillUsage
		}
		x1, err := parseBlockCoord(fields[0], e.p.x)
		if err != nil {
			return err
		}
		y1, err := parseBlockCoord(fields[1], e.p.y)
		if err != nil {
			return err
		}
		z1, err := parseBlockCoord(fields[2], e.p.z)
		if err != nil {
			return err
		}
		x2, err := parseBlockCoord(fields[3], e.p.x)
		if err != nil {
			return err
		}
		y2, err := parseBlockCoord(fields[4], e.p.y)
		if err != nil {
			return err
		}
		z2, err := parseBlockCoord(fields[5], e.p.z)
		if err != nil {
			return err
		}
		state, ok2 := blockStateByName(fields[6])
		if !ok2 {
			return fmt.Errorf("unknown block: %s", fields[6])
		}
		mode := "replace"
		if len(fields) >= 8 {
			mode = strings.ToLower(fields[7])
			switch mode {
			case "replace", "destroy", "keep", "hollow", "outline":
			default:
				return errFillUsage
			}
		}
		minX, maxX := minMaxInt(x1, x2)
		minY, maxY := minMaxInt(y1, y2)
		minZ, maxZ := minMaxInt(z1, z2)
		volume := int64(maxX-minX+1) * int64(maxY-minY+1) * int64(maxZ-minZ+1)
		if volume > int64(fillMaxBlocks) {
			return fmt.Errorf("Too many blocks in the specified area (maximum %d, specified %d)", fillMaxBlocks, volume)
		}
		if e.t.world() == nil {
			return errors.New("Could not set the block")
		}
		airState, _ := blockStateByName("air")
		count := 0
		for bx := minX; bx <= maxX; bx++ {
			for by := minY; by <= maxY; by++ {
				for bz := minZ; bz <= maxZ; bz++ {
					onShell := bx == minX || bx == maxX || by == minY || by == maxY || bz == minZ || bz == maxZ
					target := state
					pos := pk.Position{X: bx, Y: by, Z: bz}
					cur, _ := e.t.world().GetBlock(pos, dimMinY)
					switch mode {
					case "keep":
						if !block.IsAir(cur) {
							continue
						}
					case "destroy":
						if !block.IsAir(cur) {
							e.t.spawnBlockDrop(e.p, pos, cur)
						}
					case "hollow":
						if !onShell {
							target = airState
						}
					case "outline":
						if !onShell {
							continue
						}
					}
					if e.t.world().SetBlock(pos, target, dimMinY) {
						e.t.broadcastBlockUpdate(pos, target)
						count++
					}
				}
			}
		}
		if count == 0 {
			return errors.New("Could not set any blocks")
		}
		e.t.sendSystemChat(e.p, fmt.Sprintf("Successfully filled %d block(s)", count))
		return nil
	})
	arg := g.Argument("args", command.StringParser(2)).HandleFunc(h)
	g.AppendLiteral(g.Literal("fill").AppendArgument(arg).Unhandle())
}

// minMaxInt returns (min, max) of two ints (BoundingBox.fromCorners corner-normalization).
func minMaxInt(a, b int) (int, int) {
	if a <= b {
		return a, b
	}
	return b, a
}
