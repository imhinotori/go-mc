package server

// model_physics.go -- MODEL-M6 (spec Section E "M6 = opt-in native physics", spec G.4 "Pose-driven
// REAL entity dimensions"): a declared model's ACTIVE nav-state can override the mob's REAL
// server-side bounding box. When a modeled mob enters a state that declares state_dims=(w,h), its
// ACTUAL collision/hitbox AABB shrinks/grows to that box -- not merely the cosmetic Display transform.
// This is the native-advantage capstone: a Bukkit client-illusion plugin (BetterModel/ModelEngine)
// can only swap the cosmetic DATA_POSE byte; the server AABB never moves for it, because it does not
// own the tick/physics/collision core. Sulfur owns refreshDimensions, so a "crouch"/"attack" clip can
// genuinely make projectiles miss.
//
// VANILLA PRECEDENT (1:1 seam, verified against the 26.2 jar via javap):
//
//	net.minecraft.world.entity.Entity.refreshDimensions():
//	    EntityDimensions old = this.dimensions;
//	    Pose pose = this.getPose();
//	    EntityDimensions dims = this.getDimensions(pose);   // pose selects the dimensions
//	    this.dimensions = dims;
//	    this.eyeHeight = dims.eyeHeight();
//	    this.reapplyPosition();                             // rebuilds the AABB from the new dims
//	    ... (the >4-block shrink push-out branch; N/A for a small model box)
//	net.minecraft.world.entity.EntityDimensions.makeBoundingBox(x,y,z):
//	    float hw = width/2; AABB(x-hw, y, z-hw, x+hw, y+height, z+hw)   // feet-anchored, centered x/z
//
// Sulfur's Entity.AABB() derives the box from width/height the identical feet-anchored way, so writing
// e.width/e.height IS the makeBoundingBox recompute; Sulfur caches no AABB field, so reapplyPosition is
// a structural no-op. The active MODEL nav-state is the Sulfur analogue of the vanilla Pose, and
// modelDecl.stateDims is the per-pose EntityDimensions table (getDimensions(Pose)).
//
// GATE (the pig-oracle guarantee): every entry point is gated on e.model != nil AND a non-nil
// stateDims map. A vanilla mob (e.model == nil) never enters any function here; a modeled mob whose
// model declares no state_dims early-returns before touching a single field. So a pig -- which never
// carries a model -- has a byte-identical refreshDimensions/AABB/oracle. VERIFIED: modelPoseDimensions
// returns ok=false for both, and refreshDimensions falls straight through to the baby/adult logic.

// modelPoseDimensions returns the REAL (width, height) override the mob's model declares for its
// CURRENTLY-ACTIVE nav-state, or ok=false when there is no override in effect. It is the read
// refreshDimensions consults BEFORE the baby/adult default (the vanilla getDimensions(getPose())
// lookup). ok=false in three cases, each falling through to the vanilla default box byte-identically:
//   - the mob carries no model (e.model == nil) -- every vanilla mob, the pig oracle;
//   - the model declares no state_dims map (a rig with cosmetic animation only);
//   - the model declares state_dims but not for THIS active state (that state keeps the default box).
//
// The active state is the pose the animator last committed (poseState/poseInit); before the first
// commit it falls back to computeModelState(e) so a mob spawned directly INTO an override state (or
// queried before its first animator tick) still reads the right box. This mirrors vanilla reading the
// live DATA_POSE via getPose().
func modelPoseDimensions(e *Entity) (float32, float32, bool) {
	m := e.model
	if m == nil || m.decl == nil || m.decl.stateDims == nil {
		return 0, 0, false
	}
	st := m.poseState
	if !m.poseInit {
		st = computeModelState(e)
	}
	dims, ok := m.decl.stateDims[st]
	if !ok {
		return 0, 0, false
	}
	return dims[0], dims[1], true
}

// tickModelPoseDimensions is the MODEL-M6 per-tick pose->dimensions applier: it computes the mob's
// current nav-state and, when that state CHANGES (or on the first tick), commits it as the model's
// poseState and calls refreshDimensions so the REAL AABB resizes to the new state's declared box --
// or restores the default (baby/adult) box when the new state carries no override. It is the Sulfur
// analogue of the vanilla setPose(...) -> refreshDimensions() call chain (setPose writes DATA_POSE;
// the pose change drives a getDimensions(pose) resize). Idempotent: a state that has not changed does
// no work, so a settled mob never re-runs refreshDimensions.
//
// Gated on the model declaring state_dims (a nil map => no native dimensions => early return, zero
// cost). Called from tickModelAnimator (H.0 tick slot), AFTER the animator's own state selection, so
// the real box tracks the same nav-state the visible clip does. A modelless mob never reaches here
// (the caller gates on e.model != nil), preserving the pig oracle.
//
//	[VERIFIED javap Entity.setPose: entityData.set(DATA_POSE, pose); the POSE change is what later
//	 drives refreshDimensions in the vanilla tick. Here the nav-state IS the pose, and a change
//	 triggers the same refreshDimensions resize.]
func (t *TickLoop) tickModelPoseDimensions(e *Entity) {
	m := e.model
	if m == nil || m.decl == nil || m.decl.stateDims == nil {
		return // no model, or the model declares no real-dimension overrides: nothing to do
	}
	st := computeModelState(e)
	if m.poseInit && m.poseState == st {
		return // unchanged: the real box already matches this state (idempotent)
	}
	m.poseState = st
	m.poseInit = true
	// refreshDimensions re-reads modelPoseDimensions (now returning THIS state's box, or falling through
	// to the default when this state has no override) and writes e.width/e.height -- the makeBoundingBox
	// recompute. This is the ONLY seam that resizes the real AABB for a pose change.
	e.refreshDimensions()
}
