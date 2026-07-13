package server

import "testing"

func TestVibrationCandidateSelection(t *testing.T) {
	tests := []struct {
		name       string
		advance    bool
		distance   float32
		event      gameEventID
		wantEvent  gameEventID
		wantDist   float32
		wantSource int32
	}{
		{name: "farther same tick is ignored", distance: 6, event: geBlockPlace, wantEvent: geStep, wantDist: 5, wantSource: 11},
		{name: "closer same tick replaces", distance: 4, event: geBlockPlace, wantEvent: geBlockPlace, wantDist: 4, wantSource: 22},
		{name: "equal distance higher frequency replaces", distance: 5, event: geBlockPlace, wantEvent: geBlockPlace, wantDist: 5, wantSource: 22},
		{name: "later tick is ignored", advance: true, distance: 1, event: geBlockPlace, wantEvent: geStep, wantDist: 5, wantSource: 11},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			loop := &TickLoop{gametime: 100}
			data := &vibrationData{}
			loop.vibrationScheduleCandidate(data, geStep, 11, 0, 1, 2, 3, 5)
			if tc.advance {
				loop.gametime++
			}
			loop.vibrationScheduleCandidate(data, tc.event, 22, 0, 4, 5, 6, tc.distance)

			if data.candEvent != tc.wantEvent || data.candDistance != tc.wantDist || data.candSourceID != tc.wantSource {
				t.Fatalf("candidate = (%q, %.1f, source %d), want (%q, %.1f, source %d)", data.candEvent, data.candDistance, data.candSourceID, tc.wantEvent, tc.wantDist, tc.wantSource)
			}
		})
	}
}

func TestVibrationChosenCandidateRequiresEarlierTick(t *testing.T) {
	data := &vibrationData{hasCandidate: true, candGameTime: 100}
	if data.chosenCandidate(100) {
		t.Fatal("candidate scheduled at current gameTime must remain pending")
	}
	if data.chosenCandidate(99) {
		t.Fatal("future candidate must not be selected")
	}
	if !data.chosenCandidate(101) {
		t.Fatal("candidate from an earlier gameTime must be selectable")
	}
}
