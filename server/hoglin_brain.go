package server

import (
	"math"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/world/levelgen"
)

func (t *TickLoop) hoglinLevelRandom(e *Entity) *levelgen.LegacyRandomSource {
	if owner := t.regionForEntity(e); owner != nil && owner.levelRandom != nil {
		return owner.levelRandom
	}
	return t.cur().levelRandom
}

func (t *TickLoop) hoglinIsPacified(e *Entity) bool { return e.hoglinPacifiedTicks > 0 }

func (t *TickLoop) hoglinIsBreeding(e *Entity) bool { return e.hoglinBreedTarget }

func hoglinIsRepellentBlock(s block.StateID) bool {
	if int(s) < 0 || int(s) >= len(block.StateList) {
		return false
	}
	switch block.StateList[s].(type) {
	case block.WarpedFungus, block.NetherPortal, block.RespawnAnchor:
		return true
	}
	return false
}

func hoglinAbsInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func (t *TickLoop) hoglinRepellentSensor(e *Entity) {
	w := t.world()
	if w == nil {
		return
	}
	ex := int(math.Floor(e.x))
	ey := int(math.Floor(e.y))
	ez := int(math.Floor(e.z))
	for r := 0; r <= hoglinRepellentRangeH; r++ {
		found := false
		for dx := -r; dx <= r && !found; dx++ {
			for dz := -r; dz <= r && !found; dz++ {
				if hoglinAbsInt(dx) != r && hoglinAbsInt(dz) != r {
					continue
				}
				for dy := -hoglinRepellentRangeV; dy <= hoglinRepellentRangeV; dy++ {
					s, ok := w.GetBlock(pk.Position{X: ex + dx, Y: ey + dy, Z: ez + dz}, dimMinY)
					if ok && hoglinIsRepellentBlock(s) {
						found = true
						break
					}
				}
			}
		}
		if found {
			e.hoglinPacifiedTicks = hoglinRepellentPacify
			t.hoglinFleeFrom(e, float64(ex), float64(ez), hoglinFleeSpeedRepellent)
			return
		}
	}
}

func (t *TickLoop) hoglinFleeFrom(e *Entity, avoidX, avoidZ, speedModifier float64) {
	if e.ai == nil {
		return
	}
	x, y, z, ok := getPosAway(mobRandom(e), e, avoidX, avoidZ)
	if !ok {
		return
	}
	e.ai.setWantTargetSpeed(x, y, z, e.getAttributeValue(attribute.MovementSpeed)*speedModifier)
}

func (t *TickLoop) hoglinFleeAvoidTarget(e *Entity) {
	ax, _, az, ok := t.hoglinEntityPos(e.hoglinAvoidTargetID)
	if !ok {
		e.hoglinAvoidTargetID = 0
		e.hoglinAvoidTicks = 0
		return
	}
	t.hoglinFleeFrom(e, ax, az, hoglinAvoidSpeed)
}

func (t *TickLoop) hoglinPiglinsOutnumber(e *Entity) bool {
	if e.isBaby() {
		return false
	}
	piglinCount := 0
	hoglinCount := 0
	for _, m := range t.cur().entities.near(e.x, e.z, 1) {
		if m == nil || m.dead || m.id == e.id || m.isBaby() {
			continue
		}
		switch m.typ {
		case entity.Piglin.ID:
			piglinCount++
		case entity.Hoglin.ID:
			hoglinCount++
		}
	}
	return piglinCount > hoglinCount+1
}

func (t *TickLoop) hoglinWasHurtBy(e *Entity, attackerID int32) {
	e.hoglinPacifiedTicks = 0
	e.hoglinBreedTarget = false
	if attackerID == 0 {
		return
	}
	if e.isBaby() {
		t.hoglinSetAvoidTarget(e, attackerID)
		return
	}
	t.hoglinMaybeRetaliate(e, attackerID)
}

func (t *TickLoop) hoglinMaybeRetaliate(e *Entity, attackerID int32) {
	if e.hoglinAvoidTargetID != 0 && t.hoglinAttackerIsType(attackerID, entity.Piglin.ID) {
		return
	}
	if t.hoglinAttackerIsType(attackerID, entity.Hoglin.ID) {
		return
	}
	ax, ay, az, ok := t.hoglinEntityPos(attackerID)
	if !ok {
		return
	}
	if cur := t.hoglinTarget(e); cur != nil {
		curD := sqrDist(e.x, e.y, e.z, cur.x, cur.y, cur.z)
		atkD := sqrDist(e.x, e.y, e.z, ax, ay, az)
		if math.Sqrt(atkD)-math.Sqrt(curD) > 4.0 {
			return
		}
	}
	if !t.hoglinAttackerAttackable(attackerID) {
		return
	}
	if e.ai != nil {
		e.hoglinBreedTarget = false
		e.ai.attackTargetID = attackerID
	}
}

func (t *TickLoop) hoglinSetAvoidTarget(e *Entity, avoidID int32) {
	if e.ai != nil {
		e.ai.attackTargetID = 0
		e.ai.clearWantTarget()
	}
	e.hoglinAvoidTargetID = avoidID
	e.hoglinAvoidTicks = 100 + int(t.hoglinLevelRandom(e).NextIntN(301))
}

func (t *TickLoop) hoglinPiglinAvoidCheck(e *Entity) {
	if e.hoglinAvoidTargetID != 0 {
		return
	}
	if !t.hoglinPiglinsOutnumber(e) {
		return
	}
	var nearest *Entity
	bestSq := math.Inf(1)
	for _, m := range t.cur().entities.near(e.x, e.z, 1) {
		if m == nil || m.dead || m.isBaby() || m.typ != entity.Piglin.ID {
			continue
		}
		d := sqrDist(e.x, e.y, e.z, m.x, m.y, m.z)
		if d < bestSq {
			bestSq = d
			nearest = m
		}
	}
	if nearest != nil {
		t.hoglinSetAvoidTarget(e, nearest.id)
	}
}

func (t *TickLoop) hoglinAttackerIsType(attackerID int32, typ entity.ID) bool {
	if m, ok := t.cur().entities.get(attackerID); ok && m != nil {
		return m.typ == typ
	}
	return false
}

func (t *TickLoop) hoglinAttackerAttackable(attackerID int32) bool {
	if p := t.playerByEntityID(attackerID); p != nil && !p.dead {
		return true
	}
	if m, ok := t.cur().entities.get(attackerID); ok && m != nil && !m.dead {
		return true
	}
	return false
}

func (t *TickLoop) hoglinEntityPos(id int32) (x, y, z float64, ok bool) {
	if p := t.playerByEntityID(id); p != nil {
		return p.x, p.y, p.z, true
	}
	if m, ok2 := t.cur().entities.get(id); ok2 && m != nil {
		return m.x, m.y, m.z, true
	}
	return 0, 0, 0, false
}
