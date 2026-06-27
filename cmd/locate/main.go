// locate scans the overworld structure starts around spawn for a seed and prints their
// id + center block coords — a diagnostic for "where are the structures" (and to drive the
// testbot to them). Run: go run ./cmd/locate -seed 777 -radius 24
package main

import (
	"flag"
	"fmt"
	"sort"

	"github.com/imhinotori/sulfur/world"
)

func main() {
	seed := flag.Int64("seed", 777, "overworld seed")
	radius := flag.Int("radius", 24, "chunk radius around (0,0) to scan")
	flag.Parse()

	ng := world.NewNoiseGenerator(*seed, 24, -64)
	found := ng.LocateStructures(*radius)

	sort.Slice(found, func(i, j int) bool {
		if found[i].ID != found[j].ID {
			return found[i].ID < found[j].ID
		}
		di := found[i].CenterX*found[i].CenterX + found[i].CenterZ*found[i].CenterZ
		dj := found[j].CenterX*found[j].CenterX + found[j].CenterZ*found[j].CenterZ
		return di < dj
	})

	fmt.Printf("seed=%d radius=%d chunks -> %d structures\n", *seed, *radius, len(found))
	fmt.Printf("%-34s %8s %8s %6s  (dist from spawn)\n", "structure", "x", "z", "minY")
	for _, s := range found {
		dist := 0
		// integer sqrt of x^2+z^2
		sq := s.CenterX*s.CenterX + s.CenterZ*s.CenterZ
		for dist*dist <= sq {
			dist++
		}
		dist--
		fmt.Printf("%-34s %8d %8d %6d  (~%d blocks)\n", s.ID, s.CenterX, s.CenterZ, s.MinY, dist)
	}
}
