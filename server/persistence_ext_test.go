package server

import (
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level/component"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/save"

	"github.com/google/uuid"
)

func TestPlayerExtrasRoundTrip(t *testing.T) {
	dir := t.TempDir()
	id := uuid.New()

	src := &tickPlayer{
		x: 10, y: 70, z: -5,
		yaw: 12, pitch: -3,
		health:             maxHealth,
		food:               maxFood,
		saturation:         defaultSaturation,
		gameMode:           gameModeCreative,
		experienceLevel:    30,
		experienceProgress: 0.5,
		totalExperience:    825,
		enchantmentSeed:    123456,
		dimension:          dimOverworld,
		respawnPos:         &pk.Position{X: 100, Y: 64, Z: 200},
		respawnDimension:   dimOverworld,
		respawnYaw:         90,
		respawnForced:      true,
	}
	ensureInventory(src)
	src.activeEffects = map[string]*activeEffect{
		effectPoison: {id: effectPoison, duration: 200, amplifier: 1, ambient: false, visible: true, showIcon: true},
		effectSpeed:  {id: effectSpeed, duration: 600, amplifier: 0, ambient: true, visible: false, showIcon: true},
	}

	snap := snapshotPlayer(src)
	if err := savePlayer(dir, id, snap); err != nil {
		t.Fatalf("savePlayer: %v", err)
	}
	loaded, ok := loadPlayer(dir, id)
	if !ok {
		t.Fatal("loadPlayer miss for a just-saved player")
	}

	dst := &tickPlayer{gameMode: gameModeSurvival}
	applyLoadedPlayerExtras(dst, loaded)

	if dst.experienceLevel != 30 || dst.experienceProgress != 0.5 || dst.totalExperience != 825 {
		t.Fatalf("XP = (%d, %v, %d), want (30, 0.5, 825)", dst.experienceLevel, dst.experienceProgress, dst.totalExperience)
	}
	if dst.enchantmentSeed != 123456 {
		t.Fatalf("XpSeed = %d, want 123456", dst.enchantmentSeed)
	}
	if dst.gameMode != gameModeCreative {
		t.Fatalf("gameMode = %d, want creative(%d)", dst.gameMode, gameModeCreative)
	}
	invuln, flying, mayfly, instabuild, mayBuild := abilitiesForGameType(dst.gameMode)
	if !(invuln && mayfly && instabuild && mayBuild) || flying {
		t.Fatalf("creative abilities = invuln=%v flying=%v mayfly=%v instabuild=%v mayBuild=%v", invuln, flying, mayfly, instabuild, mayBuild)
	}
	if loaded.Abilities.MayFly != 1 || loaded.Abilities.InstantBuild != 1 || loaded.Abilities.Invulnerable != 1 {
		t.Fatalf("saved abilities compound not creative: %+v", loaded.Abilities)
	}
	if loaded.Abilities.FlySpeed != defaultFlyingSpeed || loaded.Abilities.WalkSpeed != defaultWalkingSpeed {
		t.Fatalf("saved fly/walk speed = (%v, %v)", loaded.Abilities.FlySpeed, loaded.Abilities.WalkSpeed)
	}
	if dst.respawnPos == nil || *dst.respawnPos != (pk.Position{X: 100, Y: 64, Z: 200}) {
		t.Fatalf("respawnPos = %v, want 100/64/200", dst.respawnPos)
	}
	if dst.respawnYaw != 90 || !dst.respawnForced {
		t.Fatalf("respawn yaw/forced = (%v, %v), want (90, true)", dst.respawnYaw, dst.respawnForced)
	}
	if len(dst.activeEffects) != 2 {
		t.Fatalf("restored %d effects, want 2", len(dst.activeEffects))
	}
	poison := dst.activeEffects[effectPoison]
	if poison == nil || poison.duration != 200 || poison.amplifier != 1 {
		t.Fatalf("poison effect = %+v, want dur 200 amp 1", poison)
	}
	speed := dst.activeEffects[effectSpeed]
	if speed == nil || !speed.ambient || speed.visible {
		t.Fatalf("speed effect flags = %+v, want ambient=true visible=false", speed)
	}
}

func TestEntityPersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	loop := NewTickLoop(newFakeClock())
	pos := columnOf(8.5, 8.5)

	diamondID := itemNameToID("minecraft:diamond")
	item := NewItemEntity(loop.idAlloc.AllocID(), 8.5, 64, 8.5, component.SlotData{ItemID: pk.VarInt(diamondID), Count: 5})
	item.vx, item.vy, item.vz = 0.01, 0.2, -0.01
	item.age = 42
	item.pickupDelay = 7

	pig := NewEntity(loop.idAlloc.AllocID(), entity.Pig, 8.2, 64, 8.4)
	initSpawnHealth(pig)
	pig.health = 8
	pig.yaw = 45

	ir, ok1 := entityToDisk(item)
	pr, ok2 := entityToDisk(pig)
	if !ok1 || !ok2 {
		t.Fatalf("entityToDisk failed: item=%v pig=%v", ok1, ok2)
	}
	if err := saveEntities(dir, pos, []save.Entities{ir, pr}); err != nil {
		t.Fatalf("saveEntities: %v", err)
	}
	restored, ok, err := loadEntities(dir, pos)
	if err != nil || !ok {
		t.Fatalf("loadEntities err=%v ok=%v", err, ok)
	}
	if len(restored) != 2 {
		t.Fatalf("recovered %d entities, want 2", len(restored))
	}

	var gotItem, gotMob *Entity
	for _, rec := range restored {
		e, ok := diskToEntity(loop, rec)
		if !ok {
			t.Fatalf("diskToEntity failed for %q", rec.ID)
		}
		if e.isItem {
			gotItem = e
		} else {
			gotMob = e
		}
	}
	if gotItem == nil || gotMob == nil {
		t.Fatalf("missing reconstructed entity: item=%v mob=%v", gotItem, gotMob)
	}
	if gotItem.itemStack.ItemID != pk.VarInt(diamondID) || gotItem.itemStack.Count != 5 {
		t.Fatalf("item stack = id %d count %d, want diamond x5", gotItem.itemStack.ItemID, gotItem.itemStack.Count)
	}
	if gotItem.age != 42 || gotItem.pickupDelay != 7 {
		t.Fatalf("item age/pickupDelay = (%d, %d), want (42, 7)", gotItem.age, gotItem.pickupDelay)
	}
	if gotItem.vy != 0.2 {
		t.Fatalf("item vy = %v, want 0.2 (motion restored, no RNG re-toss)", gotItem.vy)
	}
	if gotMob.typ != entity.Pig.ID {
		t.Fatalf("mob typ = %d, want pig(%d)", gotMob.typ, entity.Pig.ID)
	}
	if gotMob.health != 8 {
		t.Fatalf("mob health = %v, want 8", gotMob.health)
	}
	if gotMob.yaw != 45 {
		t.Fatalf("mob yaw = %v, want 45", gotMob.yaw)
	}
}

func TestLevelDatRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := NewTickLoop(newFakeClock())
	src.gametime = 987654
	src.worldSeed = 0x1234BEEF
	src.weather.raining = true
	src.weather.rainTime = 4321
	src.weather.thundering = true
	src.weather.thunderTime = 999
	src.weather.clearWeatherTime = 0
	src.worldBorder.centerX = 12
	src.worldBorder.centerZ = -34
	src.worldBorder.size = 5000
	src.gamerules = newGameRules()
	src.gamerules.setBool(ruleKeepInventory, true)
	src.gamerules.setInt(ruleRandomTickSpeed, 7)

	lvl := src.encodeLevelData(8, 72, 8, 45)
	if err := saveLevelDat(dir, lvl); err != nil {
		t.Fatalf("saveLevelDat: %v", err)
	}
	got, ok := loadLevelDat(dir)
	if !ok {
		t.Fatal("loadLevelDat miss for a just-saved level.dat")
	}

	dst := NewTickLoop(newFakeClock())
	dst.applyLevelData(got)

	if dst.gametime != 987654 {
		t.Fatalf("gametime = %d, want 987654", dst.gametime)
	}
	if dst.worldSeed != 0x1234BEEF {
		t.Fatalf("worldSeed = %#x, want 0x1234BEEF", dst.worldSeed)
	}
	if !dst.weather.raining || dst.weather.rainTime != 4321 {
		t.Fatalf("weather rain = (%v, %d), want (true, 4321)", dst.weather.raining, dst.weather.rainTime)
	}
	if !dst.weather.thundering || dst.weather.thunderTime != 999 {
		t.Fatalf("weather thunder = (%v, %d), want (true, 999)", dst.weather.thundering, dst.weather.thunderTime)
	}
	if dst.worldBorder.centerX != 12 || dst.worldBorder.centerZ != -34 || dst.worldBorder.size != 5000 {
		t.Fatalf("border = (%v, %v, %v), want (12, -34, 5000)", dst.worldBorder.centerX, dst.worldBorder.centerZ, dst.worldBorder.size)
	}
	if !dst.gamerules.getBool(ruleKeepInventory) {
		t.Fatal("keepInventory gamerule not restored to true")
	}
	if dst.gamerules.getInt(ruleRandomTickSpeed) != 7 {
		t.Fatalf("randomTickSpeed = %d, want 7", dst.gamerules.getInt(ruleRandomTickSpeed))
	}
	if got.Data.SpawnX != 8 || got.Data.SpawnY != 72 || got.Data.SpawnZ != 8 || got.Data.SpawnAngle != 45 {
		t.Fatalf("spawn = (%d,%d,%d,%v), want (8,72,8,45)", got.Data.SpawnX, got.Data.SpawnY, got.Data.SpawnZ, got.Data.SpawnAngle)
	}
}
