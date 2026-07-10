package server

// game_event.go -- the GAME-EVENT dispatch layer: a 1:1 port of net.minecraft.world.level.gameevent.
// GameEvent (the event-identity + notification radius) + GameEventDispatcher / GameEvent.Context, the
// level.gameEvent(source, event, pos) broadcast that walks every nearby VibrationListener. Over the
// unobfuscated 26.2 jar (temp/cache/26.2-inner.jar, javap -c -p GameEvent + GameEventDispatcher).
//
// VANILLA (verified javap this task):
//   GameEvent: every event has a notificationRadius (DEFAULT_NOTIFICATION_RADIUS = 16). The registry
//     builds one GameEvent per id (STEP, BLOCK_PLACE, BLOCK_DESTROY, PROJECTILE_LAND, ENTITY_INTERACT).
//     The frequency 1..15 a listener reads is a SEPARATE table (VibrationSystem.VIBRATION_FREQUENCY_FOR_
//     EVENT, ported in sculk_gameevent.go).
//   ServerLevel.gameEvent(Holder<GameEvent>, Vec3 pos, GameEvent.Context ctx): forwards to the chunk
//     GameEventListenerRegistry, which calls listener.handleGameEvent(level, event, ctx, pos) on every
//     registered listener whose position is within getListenerRadius() + notificationRadius of pos.
//   GameEvent.Context(sourceEntity, affectedState): the emit carries the source entity (may be null for
//     a world-driven edit) + the affected block state. VibrationSystem.User.canReceiveVibration reads
//     ctx.sourceEntity() to decide the anger source.
//
// v1 SCOPE: Sulfur has ONE server level (no per-chunk GameEventListenerRegistry). The dispatcher keeps a
// flat set of live VibrationListeners (the Warden, in this task; the sculk sensor/shrieker still use
// their direct per-tick step scans -- extending them onto this bus is cited follow-up). gameEvent(...)
// walks that set and delivers to every listener in range. This is the load-bearing observable: an event
// emitted near a Warden reaches its VibrationUser and drives anger.
//
// RNG: gameEvent(...) draws NOTHING -- no nextFloat/nextInt anywhere on the emit or the listener-walk
// path (the vanilla GameEventDispatcher.post is pure iteration; the sound/particle side effects that DO
// draw are client cosmetics not modeled here). So emitting an event NEVER perturbs any RNG stream -- the
// pig oracle (a pig emits STEP with no listener registered) stays byte-identical. Cite GameEventDispatcher
// .post (no RNG) + GameEvent (pure data).

import (
	pk "github.com/imhinotori/sulfur/net/packet"
)

// gameEventID is a GameEvent identity (its registry id, e.g. "step"). It is the SAME string space as the
// vibration frequency table sculkGameEvent (sculk_gameevent.go); a gameEventID converts to that key for
// the frequency lookup. Cite net.minecraft.world.level.gameevent.GameEvent (registry id).
type gameEventID string

// The core events emitted by common actions (a subset of net.minecraft.world.level.gameevent.GameEvent
// registered constants). The FULL set (~90 events) is in the jar GameEvent class; these are the ones a
// wired emitter posts in v1. The rest are cited follow-up. Cite GameEvent constants.
const (
	geStep           gameEventID = "step"            // Entity step (Entity.gameEvent STEP)
	geBlockPlace     gameEventID = "block_place"     // Level.setBlock place notify (BLOCK_PLACE)
	geBlockDestroy   gameEventID = "block_destroy"   // Level.destroyBlock (BLOCK_DESTROY)
	geProjectileLand gameEventID = "projectile_land" // Projectile.onHit (PROJECTILE_LAND)
	geEntityInteract gameEventID = "entity_interact" // Entity interaction (ENTITY_INTERACT)
	geEntityDie      gameEventID = "entity_die"      // LivingEntity.die (ENTITY_DIE)
	// The remaining GameEvent registry ids in the WARDEN_CAN_LISTEN / vibration-frequency set. Each has a
	// frequency in vibrationFrequencyTable; the const exists so an emitter posts the correct id. Cite
	// GameEvent constants + VibrationSystem.VIBRATION_FREQUENCY_FOR_EVENT.
	geBlockActivate gameEventID = "block_activate" // BLOCK_ACTIVATE (dispenser empty-fire click)
	geBlockOpen     gameEventID = "block_open"     // BLOCK_OPEN (door/trapdoor/fence-gate open)
	geBlockClose    gameEventID = "block_close"    // BLOCK_CLOSE (door/trapdoor/fence-gate close)
	gePrimeFuse     gameEventID = "prime_fuse"     // PRIME_FUSE (creeper/tnt fuse start)
	geExplode       gameEventID = "explode"        // EXPLODE (explosion)
	geEat           gameEventID = "eat"            // EAT (LivingEntity finish consuming; DRINK shares freq 8)
	geFluidPickup   gameEventID = "fluid_pickup"    // FLUID_PICKUP (bucket fill)
	geFluidPlace    gameEventID = "fluid_place"     // FLUID_PLACE (bucket empty)
	geNoteBlockPlay  gameEventID = "note_block_play" // NOTE_BLOCK_PLAY (note block struck)
	geShear          gameEventID = "shear"           // SHEAR (shears on a sheep/mob)
	geContainerOpen  gameEventID = "container_open"  // CONTAINER_OPEN (chest/barrel/shulker open, 0->1 edge)
	geContainerClose gameEventID = "container_close" // CONTAINER_CLOSE (container close, 1->0 edge)
	// FOLLOW-UP (cited, not yet wired at an emitter): block_change (needs the interacting player threaded
	// through the block-entity updateState chain), block_deactivate, drink, splash, swim, entity_place,
	// lightning_strike, flap, bounce, hit_ground, projectile_shoot, instrument_play, entity_action,
	// elytra_glide, unequip, entity_dismount, equip, entity_mount, entity_damage, block_attach/detach,
	// teleport, item_interact_finish. Every one already has a frequency in vibrationFrequencyTable; wiring
	// its emitter is a follow-up per subsystem.
)

// gameEventDefaultNotificationRadius is GameEvent.DEFAULT_NOTIFICATION_RADIUS (16). Every core event uses
// it. Cite GameEvent.DEFAULT_NOTIFICATION_RADIUS (bipush 16).
const gameEventDefaultNotificationRadius = 16

// gameEventContext is a 1:1 port of net.minecraft.world.level.gameevent.GameEvent$Context: the emit
// source entity id (0 == no source, the Optional.empty analogue) + the affected block state (0 == none).
// A VibrationSystem.User reads sourceEntity to pick the anger source. Cite GameEvent$Context.
type gameEventContext struct {
	sourceEntityID    int32 // ctx.sourceEntity() entity id (0 == null source)
	affectedState     int   // ctx.affectedState() block state id (0 == none)
	projectileOwnerID int32 // for a projectile source: the owning entity id (0 == not a projectile)
}

// vibrationListener is the dispatcher-side registration of one VibrationSystem.Listener (game_event.go
// keeps a flat live set; vanilla keeps one registry per chunk). Each listener exposes its owner entity so
// the dispatcher can walk it. Cite GameEventListener + VibrationSystem$Listener.
type vibrationListener struct {
	owner *Entity // the entity whose VibrationSystem.User this listener belongs to (nil == world listener)
}

// gameEvent ports ServerLevel.gameEvent(Holder<GameEvent>, Vec3 pos, GameEvent.Context): broadcast the
// event at pos to every registered VibrationListener within its listen range. Walks t.vibrationListeners
// (the flat live set), delivering to each in range via handleGameEvent. Draws NO RNG (pure iteration).
//
// IMPORTANT (pig oracle): when NO listener is registered (the pig-oracle loops register none), this is a
// pure no-op that touches no entity state and draws no RNG -- byte-identical. Cite ServerLevel.gameEvent
// + GameEventDispatcher.post.
func (t *TickLoop) gameEvent(event gameEventID, x, y, z float64, ctx gameEventContext) {
	if len(t.vibrationListeners) == 0 {
		return // no listeners -> pure no-op (the common case; the pig oracle path)
	}
	for _, ln := range t.vibrationListeners {
		if ln == nil || ln.owner == nil || ln.owner.dead {
			continue
		}
		t.vibrationHandleGameEvent(ln, event, x, y, z, ctx)
	}
}

// gameEventAt is the BlockPos convenience overload: emit at the CENTER of a block cell (pos.getCenter(),
// x+0.5,y+0.5,z+0.5) -- the vanilla Level.gameEvent(Holder, BlockPos, Context) form, which passes
// Vec3.atCenterOf(pos). Cite Level.gameEvent(Holder,BlockPos,Context).
func (t *TickLoop) gameEventAt(event gameEventID, pos pk.Position, ctx gameEventContext) {
	t.gameEvent(event, float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, ctx)
}

// registerVibrationListener adds a listener to the live set (the chunk registry .register analogue).
// Idempotent per owner. Cite GameEventListenerRegistry.register.
func (t *TickLoop) registerVibrationListener(owner *Entity) {
	if owner == nil {
		return
	}
	for _, ln := range t.vibrationListeners {
		if ln != nil && ln.owner == owner {
			return
		}
	}
	t.vibrationListeners = append(t.vibrationListeners, &vibrationListener{owner: owner})
}

// unregisterVibrationListener removes a listener (on entity removal / discard). Cite
// GameEventListenerRegistry.unregister.
func (t *TickLoop) unregisterVibrationListener(owner *Entity) {
	if owner == nil {
		return
	}
	out := t.vibrationListeners[:0]
	for _, ln := range t.vibrationListeners {
		if ln != nil && ln.owner != owner {
			out = append(out, ln)
		}
	}
	t.vibrationListeners = out
}
