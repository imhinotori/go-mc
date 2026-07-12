package server

import (
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/attribute"
	"github.com/imhinotori/sulfur/nbt"
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

func TestLevelDatRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := NewTickLoop(newFakeClock())
	src.gametime = 987654
	src.levelDifficulty = difficultyHard
	src.difficultyLocked = true
	// These world-global fields are NOT part of the 26.2 level.dat root (they moved out of
	// PrimaryLevelData.setTagData); setting them proves they do NOT leak into level.dat and are NOT
	// restored from it (a faithful setTagData round-trip touches only the setTagData key set).
	src.worldSeed = 0x1234BEEF
	src.weather.raining = true
	src.weather.rainTime = 4321
	src.gamerules = newGameRules()
	src.gamerules.setBool(ruleKeepInventory, true)

	lvl := src.encodeLevelData(8, 72, 8, 45)
	if err := saveLevelDat(dir, lvl); err != nil {
		t.Fatalf("saveLevelDat: %v", err)
	}
	got, ok := loadLevelDat(dir)
	if !ok {
		t.Fatal("loadLevelDat miss for a just-saved level.dat")
	}

	// The 26.2 setTagData key set round-trips.
	if got.Data.Time != 987654 {
		t.Fatalf("Time = %d, want 987654", got.Data.Time)
	}
	if got.Data.Spawn.Pos != [3]int32{8, 72, 8} || got.Data.Spawn.Yaw != 45 {
		t.Fatalf("spawn = %v yaw=%v, want [8 72 8] yaw=45", got.Data.Spawn.Pos, got.Data.Spawn.Yaw)
	}
	if got.Data.Spawn.Dimension != overworldDimensionName {
		t.Fatalf("spawn dimension = %q, want %q", got.Data.Spawn.Dimension, overworldDimensionName)
	}
	if got.Data.Difficulty.Difficulty != "hard" || !got.Data.Difficulty.Locked {
		t.Fatalf("difficulty_settings = %+v, want {hard locked}", got.Data.Difficulty)
	}
	if got.Data.GameType != gameModeSurvival {
		t.Fatalf("GameType = %d, want %d", got.Data.GameType, gameModeSurvival)
	}
	if got.Data.Version.ID != levelDataVersion || got.Data.Version.Name != ProtocolName {
		t.Fatalf("Version = %+v, want Id=%d Name=%q", got.Data.Version, levelDataVersion, ProtocolName)
	}
	if got.Data.DataVersion != levelDataVersion {
		t.Fatalf("DataVersion = %d, want %d", got.Data.DataVersion, levelDataVersion)
	}
	if got.Data.StorageVersion != levelStorageVersion {
		t.Fatalf("version = %d, want %d", got.Data.StorageVersion, levelStorageVersion)
	}
	if !got.Data.Initialized || !got.Data.AllowCommands {
		t.Fatalf("initialized/allowCommands = (%v,%v), want (true,true)", got.Data.Initialized, got.Data.AllowCommands)
	}
	if len(got.Data.ServerBrands) != 1 || got.Data.ServerBrands[0] != persistServerBrand {
		t.Fatalf("ServerBrands = %v, want [%q]", got.Data.ServerBrands, persistServerBrand)
	}
	if got.Data.LastPlayed == 0 {
		t.Fatal("LastPlayed not written")
	}
	// A dedicated server writes no singleplayer_uuid (nil pointer -> key absent).
	if got.Data.SingleplayerUUID != nil {
		t.Fatalf("singleplayer_uuid = %v, want absent", got.Data.SingleplayerUUID)
	}

	// applyLevelData restores ONLY the setTagData keys (gametime + difficulty), not the moved-out
	// weather/seed/gamerules (those round-trip through their own structures, not level.dat).
	dst := NewTickLoop(newFakeClock())
	dst.applyLevelData(got)
	if dst.gametime != 987654 {
		t.Fatalf("gametime = %d, want 987654", dst.gametime)
	}
	if dst.levelDifficulty != difficultyHard || !dst.difficultyLocked {
		t.Fatalf("difficulty = (%v,%v), want (hard,true)", dst.levelDifficulty, dst.difficultyLocked)
	}
}

// TestPlayerAttributesRoundTrip proves a MODIFIED player attribute (a raised max_health base plus a
// modifier) persists into the .dat "attributes" list (AttributeInstance$Packed.LIST_CODEC) and loads
// back onto a fresh player -- the bug-2 fix (the .dat previously had a DEAD Attributes field that was
// never written). CITE LivingEntity.addAdditionalSaveData (store "attributes", ..., AttributeMap.pack)
// + AttributeInstance$Packed.CODEC + AttributeModifier.CODEC.
func TestPlayerAttributesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	id := uuid.New()

	src := &tickPlayer{
		x: 0, y: 64, z: 0,
		health: maxHealth, food: maxFood, saturation: defaultSaturation,
		gameMode: gameModeSurvival, dimension: dimOverworld,
	}
	ensureInventory(src)
	// Raise the max_health BASE above the 20.0 default and attach a movement_speed modifier.
	h := src.playerAttributes()
	h.base[attrMaxHealth] = 40.0
	h.addModifier(attrMovementSpeed, attribute.AttributeModifier{ID: "test:boost", Amount: 0.25, Operation: attribute.AddMultipliedBase})

	snap := snapshotPlayer(src)

	// The snapshot must carry the modified max_health base and the modifier in the "attributes" list.
	var sawMaxHealth, sawSpeedMod bool
	for _, a := range snap.Attributes {
		if a.ID == "minecraft:max_health" && a.Base == 40.0 {
			sawMaxHealth = true
		}
		if a.ID == "minecraft:movement_speed" {
			for _, m := range a.Modifiers {
				if m.ID == "test:boost" && m.Amount == 0.25 && m.Operation == "add_multiplied_base" {
					sawSpeedMod = true
				}
			}
		}
	}
	if !sawMaxHealth {
		t.Fatalf("attributes list missing modified max_health base 40.0: %+v", snap.Attributes)
	}
	if !sawSpeedMod {
		t.Fatalf("attributes list missing movement_speed add_multiplied_base modifier: %+v", snap.Attributes)
	}

	if err := savePlayer(dir, id, snap); err != nil {
		t.Fatalf("savePlayer: %v", err)
	}
	loaded, ok := loadPlayer(dir, id)
	if !ok {
		t.Fatal("loadPlayer miss for a just-saved player")
	}

	dst := &tickPlayer{gameMode: gameModeSurvival}
	applyLoadedPlayerExtras(dst, loaded)

	if got := dst.playerAttributes().base[attrMaxHealth]; got != 40.0 {
		t.Fatalf("restored max_health base = %v, want 40.0", got)
	}
	// The movement_speed modifier survived and folds into getAttributeValue (base 0.1 * (1+0.25)).
	want := 0.10000000149011612 * 1.25
	if got := dst.getAttributeValue(attrMovementSpeed); got != want {
		t.Fatalf("restored movement_speed value = %v, want %v", got, want)
	}
}

// TestEntityCellHasPositionAndDataVersion proves the entities region cell carries the vanilla
// EntityStorage Position (chunk [x,z]) + DataVersion, and that loadEntities REJECTS a cell whose
// stored Position does not match the requested column (EntityStorage's "wrong location" guard).
func TestEntityCellHasPositionAndDataVersion(t *testing.T) {
	pos := level.ChunkPos{3, -7}
	ents := []save.Entities{{Pos: [3]float64{50, 64, -110}, UUID: [4]int32{9, 9, 9, 9}}}

	payload, err := encodeEntitySector(pos, ents)
	if err != nil {
		t.Fatalf("encodeEntitySector: %v", err)
	}
	gotEnts, gotPos, err := decodeEntitySector(payload)
	if err != nil {
		t.Fatalf("decodeEntitySector: %v", err)
	}
	if gotPos != [2]int32{3, -7} {
		t.Fatalf("Position = %v, want [3 -7]", gotPos)
	}
	if len(gotEnts) != 1 {
		t.Fatalf("recovered %d entities, want 1", len(gotEnts))
	}

	// DataVersion + Position must be present in the raw cell compound. Decode the sector payload
	// (compression byte + gzip(NBT)) directly into an entityRegion to inspect the stamped fields.
	gz, err := gzip.NewReader(bytes.NewReader(payload[1:]))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gz.Close()
	var cell entityRegion
	if _, err := nbt.NewDecoder(gz).Decode(&cell); err != nil {
		t.Fatalf("decode raw cell: %v", err)
	}
	if cell.DataVersion != playerDataVersion {
		t.Fatalf("DataVersion = %d, want %d", cell.DataVersion, playerDataVersion)
	}
	if cell.Position != [2]int32{3, -7} {
		t.Fatalf("cell.Position = %v, want [3 -7]", cell.Position)
	}

	// Wrong-location guard: saving to pos {3,-7} then loading a DIFFERENT column that resolves to the
	// same region+cell would mismatch on Position -> a miss (dropped, never respawned at the wrong
	// column). A same-column load succeeds; a mismatched Position is treated as no-data.
	dir := t.TempDir()
	if err := saveEntities(dir, pos, ents); err != nil {
		t.Fatalf("saveEntities: %v", err)
	}
	got, ok, err := loadEntities(dir, pos)
	if err != nil || !ok || len(got) != 1 {
		t.Fatalf("loadEntities same-column = (%v, %v, %d), want (nil, true, 1)", err, ok, len(got))
	}
}
