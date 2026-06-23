# NET-04 Capture-Diff: Ender vs. Vanilla 26.2 Configuration Stream

**Status:** APPROVED — real-client test passed (Task 3 blocking checkpoint signed off 2026-06-23)
**Phase / Plan:** 02-net-protocol-state-machine / 02-04
**Requirement:** NET-04 (Configuration → Play, no silent kick), NET-07 (readable disconnects)
**Date captured:** 2026-06-23
**Date signed off:** 2026-06-23

> **REAL-CLIENT RESULT:** A real vanilla 26.2 (PrismLauncher) client connected to
> `cmd/ender` and reached **Play** — it received the readable Phase-2 kick
> "Server not yet playable — gameplay arrives in Phase 3" with NO "Unbound tags"
> crash, NO "Failed to parse value", NO "Loading terrain…" hang, and NO
> login_finished decode error. The full chain works end-to-end: Handshake → Status
> → Login (sessionId fix `e283886c`) → Configuration (29 registries + the
> 15-registry Update Tags `dfb4af04`) → Play. NET-04 and NET-07 are proven against
> a real client.

This document records a byte-level comparison of the Configuration-state packet
stream emitted by a **real vanilla 26.2 server** versus **Ender**, for the same
offline-mode client, captured over a single process with the fork's own protocol
primitives. It is the authoritative NET-04 correctness check: no published spec
covers protocol 776, so matching the real server's stream + confirming a real
client reaches Play is the only proof the sequence is correct.

---

## 1. Capture Method

| Side | What ran | Port |
|------|----------|------|
| Vanilla | `java -Xmx2G -jar temp/cache/26.2-server.jar --nogui` (offline-mode, EULA accepted, flat world) in a scratch dir | 25599 |
| Ender | `cmd/ender` (commit with the 29-registry payload) | 25597 |
| Client | `temp/captureclient` — a throwaway Go harness built on the fork's `net`/`bot`/`packetid` packages: handshake (proto 776, intent=login) → offline Login Start → drain the Configuration state, recording every packet ID, every RegistryData registry id + entry count, the Update Tags registry breakdown, and the packet sequence; echoes Known Packs + acks Finish exactly like the vanilla client. | — |

**Jar integrity:** `temp/cache/26.2-server.jar` sha1 = `823e2250d24b3ddac457a60c92a6a941943fcd6a` (matches the cached authoritative jar). Java: Zulu OpenJDK 25.0.3.

Raw captures:
- `temp/captureclient/vanilla-capture.txt` (vanilla, registry set/order/counts)
- `temp/captureclient/vanilla-capture-tags.txt` (vanilla, with per-registry Update Tags breakdown)
- `temp/captureclient/ender-capture2.txt` (Ender, 29-registry payload)

---

## 2. Registry Set + Order (the core check)

**RESULT: EXACT MATCH.** Ender sends all **29** registries vanilla sends, in the
**identical send order**, with **identical per-registry entry counts**.

A direct comparison of the sorted `registryId=entryCount` line (`DIFFKEY`) from
both captures is byte-identical, and a diff of the ordered `RegistryData(...)`
send sequence is empty.

| # | Registry | Entries (vanilla = Ender) |
|---|----------|---------------------------|
| 1 | minecraft:worldgen/biome | 66 |
| 2 | minecraft:chat_type | 7 |
| 3 | minecraft:trim_pattern | 18 |
| 4 | minecraft:trim_material | 11 |
| 5 | minecraft:wolf_variant | 9 |
| 6 | minecraft:wolf_sound_variant | 7 |
| 7 | minecraft:pig_variant | 3 |
| 8 | minecraft:pig_sound_variant | 3 |
| 9 | minecraft:frog_variant | 3 |
| 10 | minecraft:cat_variant | 11 |
| 11 | minecraft:cat_sound_variant | 2 |
| 12 | minecraft:cow_sound_variant | 2 |
| 13 | minecraft:cow_variant | 3 |
| 14 | minecraft:chicken_sound_variant | 2 |
| 15 | minecraft:chicken_variant | 3 |
| 16 | minecraft:zombie_nautilus_variant | 2 |
| 17 | minecraft:painting_variant | 51 |
| 18 | minecraft:sulfur_cube_archetype | 12 |
| 19 | minecraft:dimension_type | 4 |
| 20 | minecraft:damage_type | 51 |
| 21 | minecraft:banner_pattern | 43 |
| 22 | minecraft:enchantment | 43 |
| 23 | minecraft:jukebox_song | 22 |
| 24 | minecraft:instrument | 8 |
| 25 | minecraft:test_environment | 1 |
| 26 | minecraft:test_instance | 1 |
| 27 | minecraft:dialog | 3 |
| 28 | minecraft:world_clock | 2 |
| 29 | minecraft:timeline | 4 |

### Resolution of Open Question 1 (exact registry set/order)

The plan/research working assumption was "3 mandatory registries + Known Packs
first." **The capture proved that assumption insufficient:** vanilla 26.2 emits
**29** registries in config. The initial Ender payload (Plan 02-03) embedded only
4 (chat_type, damage_type, dimension_type, worldgen/biome). The remaining 25
include entity-variant registries (wolf/pig/frog/cat/cow/chicken/zombie_nautilus
variants + sound variants), painting_variant, banner_pattern, enchantment,
jukebox_song, instrument, trim_pattern/material, sulfur_cube_archetype, dialog,
world_clock, timeline, and the two test registries.

**Action taken (deviation Rule 2 — missing critical functionality):** Ender's
embedded registry set was expanded from 4 to the full vanilla 29, in vanilla's
exact send order, sourced from the authoritative 26.2 datapack JSON
(`temp/cache/26.2-datagen/.../data/minecraft/`) through the existing
type-faithful JSON→NBT converter. The fork's bot/ client decoder round-trips all
29 without error (TestConfigSequence green). See commit `fdbfca0d`.

---

## 3. NBT Shape (dimension_type / worldgen/biome)

The fork bot/ decoder (the authoritative client-side decode) successfully decodes
every entry of all 29 registries from Ender's stream — including the typed
`dimension_type` decode of `minecraft:overworld` and the `worldgen/biome` decode
of `minecraft:plains`. The Wave-2 type-faithfulness guard (`TestRegistryNBTTagTypes`)
confirms the 26.2 nested schema survives: `min_y`/`height`/`monster_spawn_block_light_limit`
are TagInt (not TagDouble), `has_skylight`/`has_ceiling` are TagByte, the nested
`attributes` compound and `default_clock` string are present (not the stale flat
pre-26.2 schema).

**No NBT-shape divergence was observed** in the registries the fork decodes
typedly. The 25 newly-added registries decode as RawMessage (the fork has no typed
struct for them), so their internal NBT shape is carried verbatim from the
authoritative jar datagen — the same source vanilla serializes from.

> Open caveat for the reviewer: the type-faithful converter's `floatFields`
> override list was tuned for the original 4 registries. The 25 new registries may
> contain float-vs-double fields where the lexical int/decimal heuristic defaults a
> vanilla TAG_Float to TAG_Double (e.g. probability/weight-like fields in
> enchantment or variant registries). This does not change registry set/order/count
> (which match exactly) and the fork decoder accepts the bytes, but a real-client
> check is the final word on whether any such field is rejected. If the real client
> reaches Play and renders correctly, these defaults are confirmed acceptable.

---

## 4. Update Tags

| | Vanilla | Ender |
|--|---------|-------|
| Update Tags packet present | yes | yes |
| Registries carrying tags | **15** | **15** (real vanilla set — see below) |

**RESOLVED (W3, commit `feat(02-04): send real vanilla 26.2 Update Tags`).** The
earlier "present-but-empty (VarInt count = 0)" stub was a **confirmed HARD BLOCKER**,
not acceptable polish: a real PrismLauncher 26.2 client connected to Ender and
crashed during Registry Loading with `IllegalStateException: Failed to load
registries due to errors` →

- `Unbound tags in registry … enchantment: [exclusive_set/armor, …/boots, …/bow,
  …/crossbow, …/damage, …/mining, …/riptide]`
- `Unbound tags in registry … timeline: [in_end, in_nether, in_overworld]`
- `Unbound tags in registry … dialog: [pause_screen_additions, quick_actions]`
- `Failed to parse value … for key minecraft:overworld` for dimension_type,
  enchantment, and all 14 sulfur_cube_archetype entries — because those entries
  reference tags (`timelines:"#minecraft:in_overworld"`,
  `exclusive_set:"#minecraft:exclusive_set/armor"`,
  `items:"#minecraft:sulfur_cube_archetype/bouncy"`) that an empty Update Tags
  never binds.

Ender now sends the **real vanilla 26.2 tag set for all 15 registries**, matching
vanilla's per-registry tagCount exactly. The tag JSON is the authoritative 26.2
datagen output, copied into the committed `server/registrydata/tags/` tree and
`//go:embed`-ed (runtime reads only the embedded FS; never `temp/`).

| Registry | Tags (== vanilla) | Entry refs* | | Registry | Tags (== vanilla) | Entry refs* |
|----------|------|------|--|----------|------|------|
| minecraft:block | 265 | 4126 | | minecraft:enchantment | 22 | 308 |
| minecraft:item | 224 | 2712 | | minecraft:banner_pattern | 11 | 42 |
| minecraft:worldgen/biome | 68 | 581 | | minecraft:fluid | 6 | 9 |
| minecraft:entity_type | 48 | 375 | | minecraft:game_event | 5 | 121 |
| minecraft:damage_type | 34 | 229 | | minecraft:timeline | 4 | 7 |
| minecraft:point_of_interest_type | 3 | 30 | | minecraft:instrument | 3 | 16 |
| minecraft:dialog | 2 | 0 | | minecraft:painting_variant | 1 | 47 |
| minecraft:potion | 1 | 41 | | | | |

*Entry refs = total numeric entry indices emitted across that registry's tags
after expanding tag-of-tags (`#minecraft:other_tag`) to the union of their leaf
entries and de-duplicating. `dialog` tags are legitimately empty in vanilla
(`values: []`) — presence alone binds them, which is exactly what the client's
`pause_screen_additions`/`quick_actions` unbound errors required.

**Index resolution (entry resource-location → numeric network index):**
- Built-in registries (`block`, `item`, `entity_type`, `fluid`, `game_event`,
  `point_of_interest_type`, `potion`): index = position in the generated
  `data/registryid` table (the authoritative jar-extracted network id order).
- Datapack registries (`worldgen/biome`, `damage_type`, `enchantment`,
  `banner_pattern`, `instrument`, `painting_variant`, `dialog`, `timeline`): index
  = position in that registry's RegistryData send order, which `Load()` defines as
  the sorted embedded entry filenames. `resolveDatapackIndices` reproduces exactly
  that order, so a tag member's index equals the slot the entry occupies in the
  RegistryData packet Ender actually sends.

**Validation:** `TestUpdateTagsMatchesVanillaCounts` proves every member of every
tag in all 15 registries resolves to a known entry index (no unknown-entry / no
unresolvable `#tag` reference — i.e. no HARD blocker), and that the registry set +
per-registry tag counts match vanilla 26.2 exactly. `TestUpdateTagsBindsReferencedTags`
asserts the specific tags the real client reported UNBOUND
(enchantment `exclusive_set/*`, `timeline in_*`, dialog) are present (and non-empty
where they must bind). `TestWriteTagsWireRoundTrip` re-decodes the wire bytes at
threshold −1 with zero trailing bytes. All pass natively and under `-race`
(golang:1.26 Docker).

### Open Question 2 (must Update Tags be non-empty?) — RESOLVED

**ANSWERED by the real client: YES, it must be the real non-empty vanilla set.**
The prior hypothesis (present-but-empty is enough) was DISPROVEN by a real 26.2
client crash at Registry Loading. The fix is the full vanilla 15-registry tag set
above; the §6 real-client re-test confirms a client now binds the referenced tags
and proceeds past Registry/Tag Loading.

---

## 5. Packet IDs & Pre-Registry Ordering (non-blocking deltas)

| Aspect | Vanilla | Ender | Blocking? |
|--------|---------|-------|-----------|
| Pre-registry packets | `CustomPayload(brand, id 0x1)` → Update Enabled Features → Select Known Packs | Select Known Packs → Update Enabled Features | No |
| Known Packs before Registry Data | yes | yes | — (the only ordering constraint that matters) |
| Feature flags value | `[minecraft:vanilla]` | `[minecraft:vanilla]` | — (identical) |
| Known packs value | `[minecraft:core/26.2]` | `[minecraft:core/26.2]` | — (identical) |
| CodeOfConduct sent | no | no | — (correctly not sent for v1) |
| Packet IDs | config clientbound IDs align (RegistryData, UpdateTags, SelectKnownPacks, Finish all decoded by ID with no 775/776 reshuffle) | same | No |

**Deltas, all non-blocking:**
1. Vanilla sends a `minecraft:brand` Custom Payload (id 0x1) first; Ender does not.
   This is informational (server brand string) and not required to reach Play.
2. Vanilla orders Feature Flags **before** Known Packs; Ender sends Known Packs
   first. The only spec constraint is "Known Packs **before** Registry Data," which
   both satisfy. The client does not require a fixed Feature-Flags/Known-Packs order.

No packet-ID drift was observed: every config packet decoded by symbolic ID on
both sides.

---

## 6. Decisive Check — Real Client Reaches Play  ⟵ REVIEWER ACTION REQUIRED

The automatable capture/diff above is complete and shows an exact registry
set/order/count match. The one thing this harness **cannot** assert is what an
**unmodified vanilla 26.2 client** does, because that requires the real game
client's strict registry/tag validation and the visual "stands in the world, not
stuck at Loading terrain…" outcome.

**Please perform and record:**

1. Run `cmd/ender` (`go run ./cmd/ender -addr :25565`).
2. Connect with an unmodified vanilla **26.2** client (offline/cracked launcher or
   a legit 26.2 client in offline mode).
3. Confirm: the client passes Configuration and **reaches Play** — i.e. it does NOT
   hang at "Loading terrain…", and it receives the Phase-2 stub's readable Play
   Disconnect ("Server is not yet playable — gameplay arrives in Phase 3.") rather
   than a silent freeze. Reaching the readable Play Disconnect proves the full
   Configuration leg (Known Packs → Feature Flags → Registry Data ×29 → Update Tags
   → Finish → Acknowledge) completed and the connection transitioned into Play.

> Note: the Phase-2 stub GamePlay intentionally disconnects at Play with a readable
> message; "reaches Play" here means the client got past Configuration into the Play
> state (readable kick), NOT that it spawns into a tickable world (that is Phase 3).

---

## 7. Self-Consistency Evidence (already green)

- `go build ./...`, `go vet ./...` — clean.
- `go test ./server/ -run 'TestConfigSequence|TestConfigNoSilentKickOnError' -count=1 -timeout 60s` — pass (AcceptConfig + the fork's bot client complete the full 29-registry leg with no deadlock; bot decodes overworld + plains; Acknowledge round-trips; mid-sequence failure yields a ConfigFailErr with readable text).
- `go test ./server/registrydata/ -run 'TestUpdateTags|TestWriteTags' -count=1 -timeout 60s` — pass (Update Tags: all 15 registries' members resolve to known indices, counts match vanilla exactly, the client's previously-unbound enchantment/timeline/dialog tags are bound, and the wire bytes round-trip with no trailing bytes).
- `-race` (golang:1.26 Docker) on `./server/` + `./server/registrydata/` (incl. the new Update Tags tests) — pass.
- Full suite `go test ./...` — green.

---

## 8. Sign-Off

- [x] Registry set + order match vanilla 26.2 — **machine-verified EXACT MATCH (29/29)**.
- [x] Per-registry entry counts match vanilla — **machine-verified EXACT MATCH**.
- [x] NBT shape (dimension_type/biome nested 26.2 schema) confirmed — **type-faithful guard green**.
- [x] Update Tags: **real vanilla 15-registry set now sent (empty stub was a confirmed HARD BLOCKER — real client crashed at Registry Loading on unbound tags); counts machine-verified EXACT MATCH (15/15)** — real client now passes Registry/Tag Loading (§6).
- [x] Packet IDs align (no 775/776 reshuffle) — **verified, no drift**.
- [x] **Unmodified vanilla 26.2 client reaches Play with no silent "Loading terrain…" hang** — **CONFIRMED (§6, real-client test)**.

**Sign-off:** `approved` — real-client test passed (2026-06-23).

_Reviewer notes:_

```
Real vanilla 26.2 (PrismLauncher) client → cmd/ender → reached Play.
Received the readable Phase-2 kick "Server not yet playable - gameplay arrives in
Phase 3". NO "Unbound tags" crash, NO "Failed to parse value", NO "Loading terrain"
hang, NO login_finished decode error.

Two real bugs found & fixed during the test:
  - e283886c: proto-776 login_finished needed a trailing sessionId UUID
    (GAME_PROFILE + UUIDUtil.STREAM_CODEC); fork omitted it; net.Pipe missed it
    (bot scans only UUID+name).
  - dfb4af04: empty Update Tags → "Unbound tags in registry" (enchantment
    exclusive_sets / timeline / dialog) + "Failed to parse value"
    (dimension_type / enchantment / sulfur_cube_archetype); fixed by sending the
    real 15-registry vanilla tag set.

NET-04 and NET-07 proven against a real client.
```
