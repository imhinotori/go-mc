package server

import (
	"testing"

	"github.com/google/uuid"

	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// statePlayer registers a tick player with a capturing client, a known name and a distinct
// UUID, wired into the loop clientIndex so dispatch can resolve it. gameMode defaults to
// survival (the zero value); callers override it before dispatching an abilities toggle.
func statePlayer(loop *TickLoop, name string) *tickPlayer {
	p := &tickPlayer{
		client:   captureClient(64),
		entityID: 4000 + int32(len(loop.players)),
		name:     name,
		uuid:     uuid.New(),
	}
	loop.players = append(loop.players, p)
	if loop.clientIndex == nil {
		loop.clientIndex = make(map[*Client]*tickPlayer)
	}
	loop.clientIndex[p.client] = p
	return p
}

// abilitiesPacket builds a ServerboundPlayerAbilitiesPacket carrying the single packed byte
// (FLAG_FLYING == 2 when flying). VERIFIED javap ServerboundPlayerAbilitiesPacket.write.
func abilitiesPacket(flying bool) pk.Packet {
	var b byte
	if flying {
		b |= abilitiesFlagFlying
	}
	return pk.Marshal(int32(packetid.ServerboundPlayerAbilities), pk.UnsignedByte(b))
}

// TestPlayerAbilitiesToggleAppliedWhenMayFly asserts a creative player (mayfly true) has its
// flying state applied from the packet: dispatch -> handlePlayerAbilities sets p.flying =
// isFlying && mayfly. Mirrors ServerGamePacketListenerImpl.handlePlayerAbilities.
func TestPlayerAbilitiesToggleAppliedWhenMayFly(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := statePlayer(loop, "flyer")
	p.gameMode = gameModeCreative

	loop.dispatch(p.client, abilitiesPacket(true))
	if !p.flying {
		t.Fatalf("creative flight toggle ON not applied: p.flying=%v, want true", p.flying)
	}

	loop.dispatch(p.client, abilitiesPacket(false))
	if p.flying {
		t.Fatalf("flight toggle OFF not applied: p.flying=%v, want false", p.flying)
	}
}

// TestPlayerAbilitiesToggleClampedWhenCannotFly asserts a survival player (mayfly false) cannot
// set flying via the packet: isFlying AND mayfly == false, so p.flying stays false even when the
// client claims flight. Mirrors the getAbilities().mayfly guard.
func TestPlayerAbilitiesToggleClampedWhenCannotFly(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	p := statePlayer(loop, "survivor")
	p.gameMode = gameModeSurvival

	loop.dispatch(p.client, abilitiesPacket(true))
	if p.flying {
		t.Fatalf("survival player must not fly: p.flying=%v, want false", p.flying)
	}
}

// TestChangeDifficultyRejectedForNonOp asserts a non-operator ChangeDifficulty is refused: the
// level difficulty is unchanged and NO ClientboundChangeDifficulty is broadcast. Mirrors the
// COMMANDS_GAMEMASTER gate in handleChangeDifficulty.
func TestChangeDifficultyRejectedForNonOp(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	loop.SetPermStore(newDefaultPermStore(""))
	p := statePlayer(loop, "nonop")

	before := loop.levelDifficulty
	loop.dispatch(p.client, pk.Marshal(int32(packetid.ServerboundChangeDifficulty), pk.VarInt(difficultyHard)))

	if loop.levelDifficulty != before {
		t.Fatalf("non-op ChangeDifficulty mutated difficulty: got %v, want unchanged %v", loop.levelDifficulty, before)
	}
	if n := countID(drainPackets(p.client), packetid.ClientboundChangeDifficulty); n != 0 {
		t.Fatalf("non-op ChangeDifficulty broadcast %d packets, want 0", n)
	}
}

// TestChangeDifficultyAppliedForOp asserts an operator ChangeDifficulty is honored: the level
// difficulty is set AND a ClientboundChangeDifficulty is broadcast to every player carrying the
// new (difficulty id, locked=false). Mirrors handleChangeDifficulty -> setDifficulty.
func TestChangeDifficultyAppliedForOp(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	store := newDefaultPermStore("")
	loop.SetPermStore(store)
	op := statePlayer(loop, "op")
	other := statePlayer(loop, "witness")
	store.setOp(op.uuid, op.name, true)

	loop.dispatch(op.client, pk.Marshal(int32(packetid.ServerboundChangeDifficulty), pk.VarInt(difficultyHard)))

	if loop.levelDifficulty != difficultyHard {
		t.Fatalf("op ChangeDifficulty not applied: got %v, want difficultyHard", loop.levelDifficulty)
	}
	for _, pl := range []*tickPlayer{op, other} {
		got := drainPackets(pl.client)
		var found bool
		for _, pkt := range got {
			if pkt.ID != int32(packetid.ClientboundChangeDifficulty) {
				continue
			}
			var id pk.VarInt
			var locked pk.Boolean
			if err := pkt.Scan(&id, &locked); err != nil {
				t.Fatalf("scan ClientboundChangeDifficulty for %q: %v", pl.name, err)
			}
			if int(id) != int(difficultyHard) || bool(locked) {
				t.Fatalf("broadcast to %q carried (id=%d locked=%v), want (%d false)", pl.name, int(id), bool(locked), int(difficultyHard))
			}
			found = true
		}
		if !found {
			t.Fatalf("player %q did not receive a ClientboundChangeDifficulty broadcast", pl.name)
		}
	}
}

// TestLockDifficultyAppliedForOp asserts an operator LockDifficulty sets the lock flag and
// broadcasts ClientboundChangeDifficulty with locked=true. Mirrors handleLockDifficulty ->
// setDifficultyLocked.
func TestLockDifficultyAppliedForOp(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	store := newDefaultPermStore("")
	loop.SetPermStore(store)
	op := statePlayer(loop, "op")
	store.setOp(op.uuid, op.name, true)

	loop.dispatch(op.client, pk.Marshal(int32(packetid.ServerboundLockDifficulty), pk.Boolean(true)))

	if !loop.difficultyLocked {
		t.Fatalf("op LockDifficulty did not set difficultyLocked, got false")
	}
	got := drainPackets(op.client)
	var locked pk.Boolean
	var id pk.VarInt
	var found bool
	for _, pkt := range got {
		if pkt.ID == int32(packetid.ClientboundChangeDifficulty) {
			if err := pkt.Scan(&id, &locked); err == nil && bool(locked) {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("op LockDifficulty did not broadcast ClientboundChangeDifficulty with locked=true")
	}
}

// TestChangeGameModeAppliedForOp asserts an operator ChangeGameMode applies the requested mode via
// setPlayerGameMode. A serverbound change-gamemode packet DOES exist in protocol 776
// (GamePacketTypes.SERVERBOUND_CHANGE_GAME_MODE).
func TestChangeGameModeAppliedForOp(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	store := newDefaultPermStore("")
	loop.SetPermStore(store)
	op := statePlayer(loop, "op")
	store.setOp(op.uuid, op.name, true)
	op.gameMode = gameModeSurvival

	loop.dispatch(op.client, pk.Marshal(int32(packetid.ServerboundChangeGameMode), pk.VarInt(gameModeCreative)))

	if op.gameMode != gameModeCreative {
		t.Fatalf("op ChangeGameMode not applied: got %d, want gameModeCreative", op.gameMode)
	}
}

// TestChangeGameModeRejectedForNonOp asserts a non-operator ChangeGameMode is refused: the game
// mode is unchanged. Mirrors the PERMISSION_CHECK (COMMANDS_GAMEMASTER) gate.
func TestChangeGameModeRejectedForNonOp(t *testing.T) {
	loop := NewTickLoop(newFakeClock())
	loop.SetPermStore(newDefaultPermStore(""))
	p := statePlayer(loop, "nonop")
	p.gameMode = gameModeSurvival

	loop.dispatch(p.client, pk.Marshal(int32(packetid.ServerboundChangeGameMode), pk.VarInt(gameModeCreative)))

	if p.gameMode != gameModeSurvival {
		t.Fatalf("non-op ChangeGameMode mutated game mode: got %d, want unchanged gameModeSurvival", p.gameMode)
	}
}
