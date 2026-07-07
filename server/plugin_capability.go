package server

import "fmt"

// plugin_capability.go — the capability vocabulary + enforcement for the Phase-23 entity/world
// handle bridge (CONTEXT decision 4, LOCKED). The `capabilities` manifest field has been parsed
// since Phase 22 but UNENFORCED; now that real entity/world handles expose live tick-owned state to
// Starlark, every handle read/mutate op checks the owning plugin's declared capabilities at the
// handle-op boundary. A denied call returns a Starlark error the author SEES (capError), never a
// silent no-op.

// capSet is a bitset of the capabilities a plugin has been granted by its manifest `capabilities`
// field. It is the least-privilege grant the handle enforces per-op. Stored on each handle
// (entityHandle/worldHandle) so the check is local to the op — no back-reference to the manifest at
// call time. uint16 (widened from uint8 with the SKILL verbs) leaves headroom for the reserved
// skills.spawn / skills.projectile / models.declare verbs the design doc plans.
type capSet uint16

const (
	// capEntitiesRead grants reading entity state (health/pos/type/on_ground/velocity/attribute).
	capEntitiesRead capSet = 1 << iota
	// capEntitiesWrite grants mutating an entity (set_velocity / set_attribute / move_to).
	capEntitiesWrite
	// capWorldRead grants reading the world (block_at / entities_near).
	capWorldRead
	// capWorldWrite grants mutating the world (set_block).
	capWorldWrite
	// capNav grants issuing navigation requests (move_to routes through the nav requestPath seam).
	capNav
	// capSkillDamage grants the declared-skill "damage" mechanic (plugin_skill_decl.go): a skill that
	// deals damage through the ported applyDamage/applyDamageEntity paths. Enforced at LOAD (skills are
	// data — a mechanic("damage") under a manifest without skills.damage is a loud load error, the
	// fail-closed twin of the handle-op denial).
	capSkillDamage
	// capSkillEffects grants the declared-skill "effect" mechanic: applying mob effects (potion buffs/
	// debuffs) through addPlayerEffect/addEntityEffect. Enforced at LOAD like capSkillDamage.
	capSkillEffects
	// capModelsDeclare grants the native-model declaration verbs (plugin_model_decl.go): declare_model +
	// declare_mob(model=). A model is DATA known at load, so — like the skill caps — this is enforced
	// EARLIER than the handle-op boundary: declare_model under a manifest without models.declare is a
	// LOUD LOAD error (fail-closed), the parse-time twin of capError. The design doc names it a reserved
	// verb; M2 lands it. (models.animate — play_animation + animation triggers — is the M3/M4 sibling.)
	capModelsDeclare
)

// capAll is every capability — used by tests that exercise the handle ops without a denial, and by a
// trusted/internal handle. Wave 2 derives a real per-plugin capSet from the manifest via
// parseCapabilities.
const capAll = capEntitiesRead | capEntitiesWrite | capWorldRead | capWorldWrite | capNav |
	capSkillDamage | capSkillEffects | capModelsDeclare

// capByName maps a manifest capability STRING to its bit. The LOCKED vocabulary (CONTEXT decision 4):
// entities.read / entities.write / world.read / world.write / nav. A manifest capability not in this
// table is rejected LOUDLY at parse (parseCapabilities errors) — consistent with Phase 22's "unknown
// event errors at load" rule, so a typo'd capability never silently grants nothing.
var capByName = map[string]capSet{
	"entities.read":  capEntitiesRead,
	"entities.write": capEntitiesWrite,
	"world.read":     capWorldRead,
	"world.write":    capWorldWrite,
	"nav":            capNav,
	"skills.damage":  capSkillDamage,
	"skills.effects": capSkillEffects,
	"models.declare": capModelsDeclare,
}

// parseCapabilities ORs the bits for a manifest's capability strings, returning an error that names
// any unknown capability string (rejected at load, never silently ignored). An empty/nil list yields
// the zero capSet (no capabilities — the least-privilege default).
func parseCapabilities(strs []string) (capSet, error) {
	var c capSet
	for _, s := range strs {
		bit, ok := capByName[s]
		if !ok {
			return 0, fmt.Errorf("unknown capability %q (valid: entities.read, entities.write, world.read, world.write, nav, skills.damage, skills.effects, models.declare)", s)
		}
		c |= bit
	}
	return c, nil
}

// has reports whether the set grants ALL of the requested bits (an AND-style check — move_to requires
// both capEntitiesWrite AND capNav, so the caller passes the OR of both).
func (c capSet) has(want capSet) bool { return c&want == want }

// capError is the Starlark-surfaced error a denied handle op returns — a dynamic error the plugin
// author sees (NOT a silent no-op, CONTEXT decision 4). `missing` names the human-readable capability
// the op required.
func capError(missing string) error {
	return fmt.Errorf("capability denied: this plugin lacks %q", missing)
}
