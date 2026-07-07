package server

import (
	"math"

	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// world_border.go ports net.minecraft.world.level.border.WorldBorder (the static,
// non-moving border — the only extent v1 uses) 1:1: the world-global border state
// (center/size + the damage/warning constants), its geometry (getMinX/getMaxX/getMinZ/
// getMaxZ, isWithinBounds, getDistanceToBorder, clampVec3ToBound), the on-join
// ClientboundInitializeBorderPacket, the per-tick out-of-bounds damage the border deals
// to players outside the safe zone (LivingEntity.baseTick border branch), and the
// movement clamp collision.go consults so a player cannot walk through the border.
//
// All constants and formulas are jar-derived from the unobfuscated 26.2 inner jar
// (net.minecraft.world.level.border.WorldBorder + WorldBorder$StaticBorderExtent +
// LivingEntity.baseTick + ClientboundInitializeBorderPacket). The default border is
// 5.9999968E7 wide centered at (0,0), so an in-bounds entity (the pig at spawn, any
// player near origin) triggers ZERO border effect — the checks are a no-op deep inside
// the border, byte-identical to a server with no border logic at all.

const (
	// worldBorderDefaultSize is WorldBorder$Settings.DEFAULT.size == 5.9999968E7 (the value
	// passed to `new StaticBorderExtent(this, 5.9999968E7)` in the WorldBorder() ctor). Cite
	// WorldBorder.<init> (ldc2_w 5.9999968E7d).
	worldBorderDefaultSize = 5.9999968e7

	// worldBorderDefaultCenterX / worldBorderDefaultCenterZ are the ctor defaults: centerX/centerZ
	// are 0.0 (zero-valued fields, never assigned a non-zero literal in <init>). Cite WorldBorder.<init>.
	worldBorderDefaultCenterX = 0.0
	worldBorderDefaultCenterZ = 0.0

	// worldBorderAbsoluteMaxSize is WorldBorder.absoluteMaxSize == 29999984 (ldc int 29999984 in the
	// ctor). getMinX/getMaxX clamp the raw center +/- size/2 into [-absoluteMaxSize, absoluteMaxSize].
	// It is also sent as newAbsoluteMaxSize in ClientboundInitializeBorderPacket. Cite WorldBorder.<init>
	// (MAX_CENTER_COORDINATE / absoluteMaxSize) + WorldBorder$StaticBorderExtent.updateBox.
	worldBorderAbsoluteMaxSize = 29999984

	// worldBorderDamagePerBlock is WorldBorder.damagePerBlock == 0.2 (ldc2_w 0.2d in the ctor). The
	// per-block-outside damage multiplier the baseTick border branch applies. Cite WorldBorder.<init>.
	worldBorderDamagePerBlock = 0.2

	// worldBorderSafeZone is WorldBorder.safeZone == 5.0 (ldc2_w 5.0d in the ctor). The buffer beyond
	// the border edge within which a player takes NO damage. Cite WorldBorder.<init>.
	worldBorderSafeZone = 5.0

	// worldBorderWarningTime / worldBorderWarningBlocks are the ctor defaults warningTime == 15
	// (bipush 15) and warningBlocks == 5 (iconst_5). Client-side red-vignette cues; sent in
	// ClientboundInitializeBorderPacket. Cite WorldBorder.<init>.
	worldBorderWarningTime   = 15
	worldBorderWarningBlocks = 5
)

// worldBorderEpsilon is the 9.999999747378752E-6 double the isWithinBounds(AABB) and
// clampVec3ToBound overloads subtract from the max edge (ldc2_w #116 in WorldBorder). It
// is the (double)(float)1.0E-5 narrowing of the vanilla 1.0E-5 constant. Cite
// WorldBorder.isWithinBounds(AABB) / WorldBorder.clampVec3ToBound.
const worldBorderEpsilon = 9.999999747378752e-6

// worldBorder is the ported net.minecraft.world.level.border.WorldBorder value for the
// static (non-moving) extent v1 supports. It carries the mutable center + size and the
// damage/warning fields; the geometry methods derive the edges on demand (matching
// StaticBorderExtent.updateBox, which clamps center +/- size/2 into the absoluteMaxSize
// box). It is WORLD-GLOBAL (one per level, like weatherState), owned by the tick goroutine.
//
// The moving/lerp border (MovingBorderExtent) and the listener/BorderChangeListener fan-out
// (which pushes SetBorderSize/Center/etc. to clients on a runtime change) are not yet wired:
// v1 has no /worldborder command, so the border never changes after boot and only the
// on-join snapshot (ClientboundInitializeBorderPacket) is needed. getLerpTarget()==size and
// getLerpTime()==0 for the static extent, so the packet's newSize==oldSize and lerpTime==0.
type worldBorder struct {
	centerX float64
	centerZ float64
	size    float64

	damagePerBlock float64
	safeZone       float64
	warningTime    int
	warningBlocks  int

	absoluteMaxSize int
}

// defaultWorldBorder returns the WorldBorder() ctor default: a 5.9999968E7-wide static
// border centered at (0,0) with the vanilla damage/warning constants. Cite WorldBorder.<init>.
func defaultWorldBorder() worldBorder {
	return worldBorder{
		centerX:         worldBorderDefaultCenterX,
		centerZ:         worldBorderDefaultCenterZ,
		size:            worldBorderDefaultSize,
		damagePerBlock:  worldBorderDamagePerBlock,
		safeZone:        worldBorderSafeZone,
		warningTime:     worldBorderWarningTime,
		warningBlocks:   worldBorderWarningBlocks,
		absoluteMaxSize: worldBorderAbsoluteMaxSize,
	}
}

// getMinX ports WorldBorder.getMinX() -> StaticBorderExtent.getMinX(float): the cached
// updateBox value `Mth.clamp(centerX - size/2, -absoluteMaxSize, absoluteMaxSize)`. Cite
// WorldBorder$StaticBorderExtent.updateBox (minX).
func (b worldBorder) getMinX() float64 {
	return mthClampD(b.centerX-b.size/2.0, float64(-b.absoluteMaxSize), float64(b.absoluteMaxSize))
}

// getMaxX ports WorldBorder.getMaxX() -> StaticBorderExtent.getMaxX(float): the cached
// `Mth.clamp(centerX + size/2, -absoluteMaxSize, absoluteMaxSize)`. Cite updateBox (maxX).
func (b worldBorder) getMaxX() float64 {
	return mthClampD(b.centerX+b.size/2.0, float64(-b.absoluteMaxSize), float64(b.absoluteMaxSize))
}

// getMinZ ports WorldBorder.getMinZ() -> updateBox minZ. Cite updateBox (minZ).
func (b worldBorder) getMinZ() float64 {
	return mthClampD(b.centerZ-b.size/2.0, float64(-b.absoluteMaxSize), float64(b.absoluteMaxSize))
}

// getMaxZ ports WorldBorder.getMaxZ() -> updateBox maxZ. Cite updateBox (maxZ).
func (b worldBorder) getMaxZ() float64 {
	return mthClampD(b.centerZ+b.size/2.0, float64(-b.absoluteMaxSize), float64(b.absoluteMaxSize))
}

// isWithinBoundsXZD ports WorldBorder.isWithinBounds(double, double, double) — the point
// overload with an inset `d`:
//
//	x > getMinX() - d && x < getMaxX() + d && z > getMinZ() - d && z < getMaxZ() + d
//
// Cite WorldBorder.isWithinBounds(DDD).
func (b worldBorder) isWithinBoundsXZD(x, z, d float64) bool {
	return x > b.getMinX()-d && x < b.getMaxX()+d && z > b.getMinZ()-d && z < b.getMaxZ()+d
}

// isWithinBoundsXZ ports WorldBorder.isWithinBounds(double, double) == isWithinBounds(x, z, 0.0).
// Cite WorldBorder.isWithinBounds(DD).
func (b worldBorder) isWithinBoundsXZ(x, z float64) bool {
	return b.isWithinBoundsXZD(x, z, 0.0)
}

// isWithinBoundsAABB ports WorldBorder.isWithinBounds(AABB):
//
//	isWithinBounds(minX, minZ, maxX - EPS, maxZ - EPS)
//
// where the private isWithinBounds(DDDD) is `isWithinBounds(minX,minZ) && isWithinBounds(maxX,maxZ)`.
// EPS is the 9.999999747378752E-6 constant subtracted from the max edge. Cite
// WorldBorder.isWithinBounds(AABB) / WorldBorder.isWithinBounds(DDDD).
func (b worldBorder) isWithinBoundsAABB(minX, minZ, maxX, maxZ float64) bool {
	adjMaxX := maxX - worldBorderEpsilon
	adjMaxZ := maxZ - worldBorderEpsilon
	return b.isWithinBoundsXZ(minX, minZ) && b.isWithinBoundsXZ(adjMaxX, adjMaxZ)
}

// getDistanceToBorderXZ ports WorldBorder.getDistanceToBorder(double x, double z):
//
//	d0 = z - getMinZ(); d1 = getMaxZ() - z; d2 = x - getMinX(); d3 = getMaxX() - x;
//	d4 = Math.min(d2, d3); d4 = Math.min(d4, d0); return Math.min(d4, d1);
//
// A POSITIVE result is the distance to the nearest edge from inside; a NEGATIVE result means
// the point is outside (used by the baseTick damage branch as distance + safeZone < 0). Cite
// WorldBorder.getDistanceToBorder(DD).
func (b worldBorder) getDistanceToBorderXZ(x, z float64) float64 {
	d0 := z - b.getMinZ()
	d1 := b.getMaxZ() - z
	d2 := x - b.getMinX()
	d3 := b.getMaxX() - x
	d4 := math.Min(d2, d3)
	d4 = math.Min(d4, d0)
	return math.Min(d4, d1)
}

// clampVec3ToBoundXZ ports the X/Z halves of WorldBorder.clampVec3ToBound(double x, y, z):
//
//	x' = Mth.clamp(x, getMinX(), getMaxX() - EPS)
//	z' = Mth.clamp(z, getMinZ(), getMaxZ() - EPS)
//
// (Y is untouched by the border; the caller keeps its own y.) Used by the movement clamp so a
// player's post-move position never leaves the border. Cite WorldBorder.clampVec3ToBound(DDD).
func (b worldBorder) clampVec3ToBoundXZ(x, z float64) (float64, float64) {
	cx := mthClampD(x, b.getMinX(), b.getMaxX()-worldBorderEpsilon)
	cz := mthClampD(z, b.getMinZ(), b.getMaxZ()-worldBorderEpsilon)
	return cx, cz
}

// mthClampD ports net.minecraft.util.Mth.clamp(double value, double min, double max) ==
// value < min ? min : (value > max ? max : value). math.Min/math.Max would mishandle a NaN
// differently, so the vanilla branch form is reproduced verbatim. Cite Mth.clamp(DDD).
func mthClampD(value, minV, maxV float64) float64 {
	if value < minV {
		return minV
	}
	if value > maxV {
		return maxV
	}
	return value
}

// writeInitializeBorderPacket builds proto-776 ClientboundInitializeBorderPacket from a
// WorldBorder. Wire order is jar-derived from ClientboundInitializeBorderPacket.write:
//
//	Double  newCenterX          (WorldBorder.getCenterX)
//	Double  newCenterZ          (WorldBorder.getCenterZ)
//	Double  oldSize             (WorldBorder.getSize)
//	Double  newSize             (WorldBorder.getLerpTarget — == size for the static extent)
//	VarLong lerpTime            (WorldBorder.getLerpTime — == 0 for the static extent)
//	VarInt  newAbsoluteMaxSize  (WorldBorder.getAbsoluteMaxSize)
//	VarInt  warningBlocks       (WorldBorder.getWarningBlocks)
//	VarInt  warningTime         (WorldBorder.getWarningTime)
//
// Cite ClientboundInitializeBorderPacket.<init>(WorldBorder) + .write(FriendlyByteBuf).
func writeInitializeBorderPacket(b worldBorder) pk.Packet {
	return pk.Marshal(
		int32(packetid.ClientboundInitializeBorder),
		pk.Double(b.centerX),         // newCenterX
		pk.Double(b.centerZ),         // newCenterZ
		pk.Double(b.size),            // oldSize
		pk.Double(b.size),            // newSize (getLerpTarget == size, static extent)
		pk.VarLong(0),                // lerpTime (getLerpTime == 0, static extent)
		pk.VarInt(b.absoluteMaxSize), // newAbsoluteMaxSize
		pk.VarInt(b.warningBlocks),   // warningBlocks
		pk.VarInt(b.warningTime),     // warningTime
	)
}

// tickWorldBorder ports the world-border branch of LivingEntity.baseTick, run for PLAYERS
// only (the `iload_3` isPlayer gate). The bytecode (LivingEntity.baseTick, offsets ~120-205):
//
//	if (isPlayer) {
//	    WorldBorder wb = level.getWorldBorder();
//	    if (!wb.isWithinBounds(getBoundingBox())) {
//	        double d = wb.getDistanceToBorder(this) + wb.getSafeZone();
//	        if (d < 0.0) {
//	            double dpb = wb.getDamagePerBlock();
//	            if (dpb > 0.0) {
//	                hurtServer(damageSources().outOfBorder(),
//	                           (float) Math.max(1, Mth.floor(-d * dpb)));
//	            }
//	        }
//	    }
//	}
//
// A mob (the pig) is NOT a player, so it is never in this branch — its border effect is
// unconditionally zero. A player deep inside the default 6e7 border passes isWithinBounds
// (a no-op). Runs in tickEntities alongside tickSuffocation (both are baseTick isPlayer-block
// checks); ADDITIVE. Cite LivingEntity.baseTick.
func (t *TickLoop) tickWorldBorder() {
	wb := t.worldBorder
	for _, p := range t.players {
		if p == nil || p.dead {
			continue
		}
		// isWithinBounds(getBoundingBox()): the player's feet-anchored AABB (width playerWidth,
		// height playerHeight). Only X/Z matter to the border. minX/minZ/maxX/maxZ are the
		// horizontal box edges — the same box playerInWater/playerInLava build.
		hw := playerWidth / 2
		minX := p.x - hw
		maxX := p.x + hw
		minZ := p.z - hw
		maxZ := p.z + hw
		if wb.isWithinBoundsAABB(minX, minZ, maxX, maxZ) {
			continue
		}
		// getDistanceToBorder(this): the ENTITY overload reads getX()/getZ() (the entity CENTER,
		// NOT the box edges). distance + safeZone; damage only when strictly negative.
		d := wb.getDistanceToBorderXZ(p.x, p.z) + wb.safeZone
		if d >= 0.0 {
			continue
		}
		dpb := wb.damagePerBlock
		if dpb <= 0.0 {
			continue
		}
		// (float) Math.max(1, Mth.floor(-d * damagePerBlock)): the int floor, clamped to a floor
		// of 1, widened to float. Mth.floor(double) is the standard floor-cast-to-int.
		dmg := maxI(1, mthFloor(-d*dpb))
		t.applyDamage(p, damageSourceOf(damageTypeOutsideBorder), float32(dmg))
	}
}

// getWorldBorderCollision is the collision.go hook: it clamps a proposed post-move (x, z)
// back inside the border so a player can never step through it. It ports the observable
// effect of the border's collision shape (WorldBorder.getCollisionShape /
// clampVec3ToBound): a movement that would cross the edge is pinned at the edge (minus the
// epsilon on the max side). Deep inside the default 6e7 border the clamp is a no-op (the
// clamp bounds are +/- ~3e7), so it never alters an in-bounds player's motion — byte-identical
// to no border. Cite WorldBorder.getCollisionShape / WorldBorder.clampVec3ToBound.
func (t *TickLoop) getWorldBorderCollision(x, z float64) (float64, float64) {
	return t.worldBorder.clampVec3ToBoundXZ(x, z)
}

// maxI ports java.lang.Math.max(int, int). (mthFloor lives in fall_damage.go.)
func maxI(a, b int) int {
	if a > b {
		return a
	}
	return b
}
