package server

// husk_conversion_test.go -- pins the Husk underwater conversion (VERIFIED javap Zombie.tick @16-119 +
// Husk.doUnderWaterConversion this session). A submerged Husk (isEyeInFluid(WATER)) accumulates inWaterTime;
// at >= 600 it starts a 300-tick drowning countdown (startUnderWaterConversion(300)); when the countdown
// runs out it converts to a ZOMBIE (Husk.doUnderWaterConversion -> convertToZombieType(ZOMBIE)). A dry husk
// resets inWaterTime to -1 and never converts. The pig oracle is untouched (a husk is a separate mob; the
// hook is zombie/husk-gated).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/imhinotori/sulfur/data/entity"
	"github.com/imhinotori/sulfur/level"
	"github.com/imhinotori/sulfur/level/block"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/plugin/host"
	"github.com/imhinotori/sulfur/world"
	"go.starlark.net/starlark"
)

// huskConversionLoop builds a physics loop whose registry holds vanilla_husk (the convert source) AND
// vanilla_zombie (the convert TARGET must exist for the in-place convert's declByBaseType, matching how
// spawnDeclaredMob would build one). Returns the loop + floor Y.
func huskConversionLoop(t *testing.T) (*TickLoop, *world.ChunkManager, int) {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"vanilla_husk", "vanilla_zombie"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		for _, f := range []string{"plugin.toml", "main.star"} {
			data, err := os.ReadFile(filepath.Join("..", "plugins", "mobs", name, f))
			if err != nil {
				t.Fatalf("read %s/%s: %v", name, f, err)
			}
			if err := os.WriteFile(filepath.Join(dir, f), data, 0o644); err != nil {
				t.Fatalf("write %s/%s: %v", name, f, err)
			}
		}
	}
	r := newMobRegistry()
	m := host.New()
	extra := starlark.StringDict{
		"declare_mob": r.declareMobBuiltin(),
		"goal":        r.goalBuiltin(),
	}
	if err := m.LoadDirWith(root, extra); err != nil {
		t.Fatalf("LoadDirWith(husk+zombie): %v", err)
	}
	loop, mgr := newPhysicsLoop()
	const floorY = 63
	ch := putChunk(mgr, level.ChunkPos{0, 0})
	fillFloor(ch, floorY)
	loop.SetMobRegistry(r)
	clock := loop.clock.(*fakeClock)
	loop.start(clock.Now())
	return loop, mgr, floorY
}

// TestHuskSubmergedConvertsToZombie: a Husk whose eye is in water accumulates inWaterTime; at 600 it starts
// the 300-tick countdown; when the countdown expires it converts to a Zombie (Husk.doUnderWaterConversion).
func TestHuskSubmergedConvertsToZombie(t *testing.T) {
	loop, mgr, floorY := huskConversionLoop(t)

	husk := loop.spawnDeclaredMob(loop.mobRegistry.byName["vanilla_husk"], 8.5, float64(floorY+1), 8.5)
	if husk.typ != entity.Husk.ID {
		t.Fatalf("spawned husk typ = %d, want Husk.ID %d", husk.typ, entity.Husk.ID)
	}

	// Flood the husk's EYE cell (y + height*0.85) with water so isEyeInFluid(WATER) is true.
	water := block.DefaultStateID["minecraft:water"]
	eyeY := int(husk.y + float64(husk.height)*0.85)
	for dy := 0; dy <= 3; dy++ {
		mgr.SetBlock(pk.Position{X: 8, Y: eyeY + dy, Z: 8}, water, dimMinY)
	}
	loop.withRegion(loop.only(), func() {
		if !loop.zombieEyeInWater(husk) {
			t.Fatal("husk eye not detected in water after flooding the eye cell")
		}

		// Tick the conversion machine until inWaterTime crosses 600 -> countdown starts.
		for i := 0; i < zombieInWaterConvertThreshold; i++ {
			loop.zombieWaterConversionTick(husk)
		}
		if !husk.zombieUnderWaterConverting {
			t.Fatalf("husk did not start underwater conversion after %d in-water ticks (inWaterTime %d)", zombieInWaterConvertThreshold, husk.zombieInWaterTime)
		}
		if husk.zombieConversionTime != zombieUnderWaterConvertTime {
			t.Fatalf("husk conversionTime = %d, want %d (startUnderWaterConversion(300))", husk.zombieConversionTime, zombieUnderWaterConvertTime)
		}

		// Run the countdown out (300 more ticks): conversionTime-- each tick, converts at < 0.
		for i := 0; i <= zombieUnderWaterConvertTime; i++ {
			loop.zombieWaterConversionTick(husk)
			if husk.typ == entity.Zombie.ID {
				break
			}
		}
	})

	if husk.typ != entity.Zombie.ID {
		t.Fatalf("husk did not convert to a Zombie (typ = %d, want %d)", husk.typ, entity.Zombie.ID)
	}
	if husk.zombieUnderWaterConverting {
		t.Fatal("converted zombie still marked underWaterConverting (state not cleared)")
	}
}

// TestHuskDryResetsInWaterTime: a Husk NOT in water has its inWaterTime reset to -1 each tick and never
// starts a conversion (Zombie.tick else-branch: inWaterTime = -1).
func TestHuskDryResetsInWaterTime(t *testing.T) {
	loop, _, floorY := huskConversionLoop(t)

	husk := loop.spawnDeclaredMob(loop.mobRegistry.byName["vanilla_husk"], 8.5, float64(floorY+1), 8.5)
	husk.zombieInWaterTime = 500 // pretend it had been in water
	loop.withRegion(loop.only(), func() {
		loop.zombieWaterConversionTick(husk)
	})
	if husk.zombieInWaterTime != -1 {
		t.Fatalf("dry husk inWaterTime = %d, want -1 (the not-in-water reset)", husk.zombieInWaterTime)
	}
	if husk.zombieUnderWaterConverting {
		t.Fatal("dry husk wrongly started an underwater conversion")
	}
	if husk.typ != entity.Husk.ID {
		t.Fatalf("dry husk changed type to %d (want Husk %d)", husk.typ, entity.Husk.ID)
	}
}
