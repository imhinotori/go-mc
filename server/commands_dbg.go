package server

import (
	"fmt"

	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// runDbgCommand is a DEV-only debug dispatcher (wired to /dbg <sub>) so the operator can spawn mobs and
// build a water box on demand to reproduce mob-physics bugs in-game without world editing. Operator-gated
// via the same command.tp permission. Runs on the issuer's region/tick context.
func (t *TickLoop) runDbgCommand(p *tickPlayer, sub string) {
	switch sub {
	case "pig":
		e := t.spawnVanillaPig(p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned pig eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "cow":
		e := t.spawnVanillaMob(vanillaCowMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned cow eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "sheep":
		e := t.spawnVanillaMob(vanillaSheepMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned sheep eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "chicken":
		e := t.spawnVanillaMob(vanillaChickenMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned chicken eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "zombie":
		e := t.spawnVanillaMob(vanillaZombieMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned zombie eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "skeleton":
		e := t.spawnVanillaMob(vanillaSkeletonMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned skeleton eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "spider":
		e := t.spawnVanillaMob(vanillaSpiderMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned spider eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "wolf":
		// MOB-NEUT-01 (Phase 36): spawn a vanilla wolf. spawnVanillaMob returns nil until Plan C's
		// vanilla_wolf declaration boot-loads (the registry lookup is a graceful nil, never a panic — the
		// `if e != nil` guard mirrors every other arm), so /dbg wolf is a safe no-op until the .star lands.
		e := t.spawnVanillaMob(vanillaWolfMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned wolf eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "husk":
		// MOB-VARIANT (Task #9): spawn a vanilla husk (Zombie behavior, Husk wire type).
		e := t.spawnVanillaMob(vanillaHuskMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned husk eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "mooshroom":
		// MOB-VARIANT (Task #9): spawn a vanilla mooshroom (Cow behavior, Mooshroom wire type).
		e := t.spawnVanillaMob(vanillaMooshroomMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned mooshroom eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "silverfish":
		// MOB-HOST-05 (Task #9): spawn a vanilla silverfish (hunt + melee, Silverfish wire type).
		e := t.spawnVanillaMob(vanillaSilverfishMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned silverfish eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "creeper":
		// MOB-HOST-06 (Task #9): spawn a vanilla creeper (hunt + swell fuse + explosion, Creeper wire type).
		e := t.spawnVanillaMob(vanillaCreeperMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned creeper eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "witch":
		// MOB-HOST-07 (Task #9): spawn a vanilla witch (hunt + splash-potion attack, Witch wire type).
		e := t.spawnVanillaMob(vanillaWitchMobName, p.x, p.y, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] spawned witch eid=%d at (%.1f,%.1f,%.1f)", e.id, p.x, p.y, p.z))
		}
	case "water":
		t.dbgFillWater(p)
		t.broadcastSystemChat("[dbg] filled a water box around you")
	case "pig-in-water", "piw":
		t.dbgFillWater(p)
		e := t.spawnVanillaPig(p.x, p.y+1, p.z)
		if e != nil {
			t.broadcastSystemChat(fmt.Sprintf("[dbg] water box + pig eid=%d dropped in", e.id))
		}
	default:
		t.broadcastSystemChat("[dbg] usage: /dbg pig | cow | sheep | chicken | zombie | skeleton | spider | wolf | husk | mooshroom | silverfish | creeper | witch | water | pig-in-water")
	}
}

// dbgFillWater fills a 5x5 (x,z) by 5-deep (y) water box centered on the player, on a solid stone floor,
// so a mob spawned in it is genuinely submerged with fluid above its head (FloatGoal.canUse precondition).
func (t *TickLoop) dbgFillWater(p *tickPlayer) {
	if t.world() == nil {
		return
	}
	cx, cy, cz := int(p.x), int(p.y), int(p.z)
	water := waterStateID(0)
	stone := block.DefaultStateID["minecraft:stone"]
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			// solid floor 1 below the box
			t.world().SetBlock(pk.Position{X: cx + dx, Y: cy - 1, Z: cz + dz}, stone, dimMinY)
			for dy := 0; dy <= 4; dy++ {
				t.world().SetBlock(pk.Position{X: cx + dx, Y: cy + dy, Z: cz + dz}, water, dimMinY)
			}
		}
	}
}
