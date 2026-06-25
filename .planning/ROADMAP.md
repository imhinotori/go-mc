# Roadmap: Sulfur — Minecraft Java 26.2 Server in Go

## Shipped Milestones

- **v1.0** (2026-06-24) — Full playable vanilla-faithful 26.2 server: a real client logs in and plays a persistent, ticking, biome-varied **noise world** (caves/ravines/aquifers/ore-veins) with entities, mob AI, inventory, combat/respawn, commands, chat — all Leaf-style async-optimized and `-race` clean. 9 phases, 46 plans, 45/45 v1 requirements + PARITY-01. → [archive](milestones/v1.0-ROADMAP.md) · [requirements](milestones/v1.0-REQUIREMENTS.md) · [audit](v1.0-MILESTONE-AUDIT.md)

## Current Milestone

_None active. Run `/gsd-new-milestone` to scope v2._

**Next (user-chosen direction):** Worldgen v2 deferrals — trees/vegetation (feature subsystem) + structures (mineshafts/villages) + ONLINE-01/02 (Mojang auth + protocol encryption) + REGION-01 (Folia-style per-region tick threading). All enabled by the ownership-isolated v1 core.
