package server

import (
	"reflect"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
)

type recordTickAIOrderGoal struct {
	baseGoal
	order *[]int32
}

func (g *recordTickAIOrderGoal) canUse(*TickLoop, *Entity) bool { return true }

func (g *recordTickAIOrderGoal) tick(_ *TickLoop, e *Entity) {
	*g.order = append(*g.order, e.id)
}

func newRecordingOrderAI(order *[]int32) *mobAI {
	m := &mobAI{
		rng:          newEntityRandom(defaultEntityRandomSeed),
		wantSpeedMod: 1.0,
	}
	m.goals.addGoal(0, &recordTickAIOrderGoal{baseGoal: newBaseGoal(0), order: order})
	return m
}

func tickAIOrderForInsertionOrder(insert []int32) []int32 {
	loop := NewTickLoop(newFakeClock())
	loop.gametime = 1 // keep the natural-spawn cadence out of this narrow order test

	var order []int32
	for _, id := range insert {
		e := NewEntity(id, entity.Pig, float64(id)*4, 64, 0)
		initSpawnHealth(e)
		e.ai = newRecordingOrderAI(&order)
		loop.only().entities.add(e)
	}

	loop.tickAI()
	return order
}

func TestTickAIDeterministicEntityOrderByID(t *testing.T) {
	want := []int32{1, 2}
	for _, tc := range []struct {
		name   string
		insert []int32
	}{
		{name: "ascending insertion", insert: []int32{1, 2}},
		{name: "descending insertion", insert: []int32{2, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tickAIOrderForInsertionOrder(tc.insert)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("tickAI order = %v, want %v", got, want)
			}
		})
	}
}
