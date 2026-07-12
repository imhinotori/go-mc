# Serverbound game packet census (protocol 776 / 26.2)

Scope: generated **Game Serverbound** enum in `data/packetid/packetid.go:227`, `TickLoop.dispatch`, `applyInput`, direct handlers, and local `javap` signatures/bytecode from `temp/cache/26.2-inner.jar`.

Notes:
- The generated enum has 70 values including `ServerboundPacketIDGuard` (`id=69`), which is a sentinel, not a vanilla packet. It is included because this task asked for all 70 generated game-state entries.
- Actual explicit packet cases in `TickLoop.dispatch`: **40/70**. A mechanical grep finds 41 `packetid.Serverbound*` refs in `server/tick.go` because one is the type conversion `packetid.ServerboundPacketID(p.ID)`, not a packet case.
- `partial` means routed/implemented with user-visible behavior but not proven method-for-method complete vanilla parity.
- `default-noop` rows are absent from `TickLoop.dispatch`; they fall through `server/tick.go:2468`.

## Totals by status

| status | count |
|---|---:|
| exact | 4 |
| partial | 33 |
| explicit-noop | 3 |
| default-noop | 30 |
| unverified | 0 |
| total | 70 |

## Packet census

| id | packet | vanilla handler | status | evidence | user-visible impact |
|---:|---|---|---|---|---|
| 0 | `ServerboundAcceptTeleportation` | `ServerGamePacketListenerImpl.handleAcceptTeleportPacket` | partial | `data/packetid/packetid.go:229`; dispatch validates id and opens gate at `server/tick.go:2306`; vanilla also consumes `awaitingPositionFromClient` / disconnects invalid movement (verified by `javap -c`). | Correct confirm lets player move; wrong/missing confirm keeps movement gated. Full vanilla awaiting-position semantics are not modeled. |
| 1 | `ServerboundAttack` | `ServerGamePacketListenerImpl.handleAttack` | partial | `data/packetid/packetid.go:230`; buffered at `server/tick.go:2195`; applied at `server/subtick.go:375`; handler starts at `server/attack_dispatch.go:58`. | Melee against players/mobs works with reach/damage subset; enchant/item/edge branches remain deferred. |
| 2 | `ServerboundBlockEntityTagQuery` | `ServerGamePacketListenerImpl.handleBlockEntityTagQuery` | default-noop | `data/packetid/packetid.go:231`; no dispatch case; default at `server/tick.go:2468`. | Block-entity NBT query/debug request gets no response. |
| 3 | `ServerboundBundleItemSelected` | `ServerGamePacketListenerImpl.handleBundleItemSelectedPacket` | default-noop | `data/packetid/packetid.go:232`; no dispatch case; default at `server/tick.go:2468`. | Bundle selected item changes are ignored. |
| 4 | `ServerboundChangeDifficulty` | `ServerGamePacketListenerImpl.handleChangeDifficulty` | exact | `data/packetid/packetid.go:233`; dispatch at `server/tick.go:2451`; handler mirrors permission gate + set/broadcast at `server/state_packets.go:80`. | Operators can change difficulty and all clients receive the update. |
| 5 | `ServerboundChangeGameMode` | `ServerGamePacketListenerImpl.handleChangeGameMode` | partial | `data/packetid/packetid.go:234`; dispatch at `server/tick.go:2441`; handler at `server/state_packets.go:49`. | Operator game-mode requests work; invalid-mode edge behavior differs from vanilla `GameType.byId`. |
| 6 | `ServerboundChatAck` | `ServerGamePacketListenerImpl.handleChatAck` | explicit-noop | `data/packetid/packetid.go:235`; explicit decode-and-ignore case at `server/tick.go:2425`. | Signed-chat acknowledgements are ignored; offline `SystemChat` path is unaffected. |
| 7 | `ServerboundChatCommand` | `ServerGamePacketListenerImpl.handleChatCommand` | partial | `data/packetid/packetid.go:236`; dispatch at `server/tick.go:2362`; decoder/router at `server/commands.go:394`. | Slash commands route to Sulfur's command graph, not a full vanilla Brigadier/signing path. |
| 8 | `ServerboundChatCommandSigned` | `ServerGamePacketListenerImpl.handleSignedChatCommand` | explicit-noop | `data/packetid/packetid.go:237`; explicit no-op at `server/tick.go:2383`. | Signed slash commands are ignored; offline vanilla clients normally use unsigned commands. |
| 9 | `ServerboundChat` | `ServerGamePacketListenerImpl.handleChat` | partial | `data/packetid/packetid.go:238`; dispatch at `server/tick.go:2391`; handler at `server/chat.go:75`. | Chat broadcasts as server-attributed `ClientboundSystemChat`; signed `PlayerChat`, filtering, and last-seen validation are deferred. |
| 10 | `ServerboundChatSessionUpdate` | `ServerGamePacketListenerImpl.handleChatSessionUpdate` | explicit-noop | `data/packetid/packetid.go:239`; explicit decode-and-ignore case at `server/tick.go:2425`. | Signed chat session setup is ignored; offline `SystemChat` path is unaffected. |
| 11 | `ServerboundChunkBatchReceived` | `ServerGamePacketListenerImpl.handleChunkBatchReceived` | exact | `data/packetid/packetid.go:240`; dispatch at `server/tick.go:2242`; `PlayerChunkSender` port at `server/tick_phases.go:1667`. | Client chunk ACKs adjust chunk send budget and avoid streaming deadlock. |
| 12 | `ServerboundClientCommand` | `ServerGamePacketListenerImpl.handleClientCommand` | partial | `data/packetid/packetid.go:241`; dispatch at `server/tick.go:2336`; respawn implementation at `server/combat.go:722`. | Respawn and stats request paths work; full vanilla client-command surface is not proven. |
| 13 | `ServerboundClientTickEnd` | `ServerGamePacketListenerImpl.handleClientTickEnd` | partial | `data/packetid/packetid.go:242`; dispatch records only a boundary at `server/tick.go:2300`; vanilla bytecode resets known movement when no movement arrived. | Subtick boundary is observable, but known-movement reset side effects are missing. |
| 14 | `ServerboundClientInformation` | `ServerGamePacketListenerImpl.handleClientInformation` | partial | `data/packetid/packetid.go:243`; dispatch at `server/tick.go:2255`; handler only applies skin parts at `server/player_visibility.go:146`. | Skin overlay parts update live; locale/view/chat/main-hand/listing options are ignored. |
| 15 | `ServerboundCommandSuggestion` | `ServerGamePacketListenerImpl.handleCustomCommandSuggestions` | partial | `data/packetid/packetid.go:244`; dispatch at `server/tick.go:2375`; handler at `server/commands_suggest.go:25`. | Tab-complete works for Sulfur commands/registries, not the full vanilla Brigadier tree. |
| 16 | `ServerboundConfigurationAcknowledged` | `ServerGamePacketListenerImpl.handleConfigurationAcknowledged` | default-noop | `data/packetid/packetid.go:245`; no dispatch case; default at `server/tick.go:2468`. | Play-to-config acknowledgement is ignored. |
| 17 | `ServerboundContainerButtonClick` | `ServerGamePacketListenerImpl.handleContainerButtonClick` | partial | `data/packetid/packetid.go:246`; buffered at `server/tick.go:2220`; applied at `server/subtick.go:344`; handler at `server/cooking_block.go:49`. | Stonecutter/enchant/loom buttons work for implemented menus; broader menu parity remains unproven. |
| 18 | `ServerboundContainerClick` | `ServerGamePacketListenerImpl.handleContainerClick` | partial | `data/packetid/packetid.go:247`; buffered at `server/tick.go:2213`; applied at `server/subtick.go:321`; handler at `server/inventory.go:173`. | Authoritative inventory clicks work; full vanilla menu/click matrix is not fully proven. |
| 19 | `ServerboundContainerClose` | `ServerGamePacketListenerImpl.handleContainerClose` | partial | `data/packetid/packetid.go:248`; buffered at `server/tick.go:2215`; applied at `server/subtick.go:340`; handler at `server/inventory.go:398`. | Client close tears down supported containers; full persistence/menu close parity is limited. |
| 20 | `ServerboundContainerSlotStateChanged` | `ServerGamePacketListenerImpl.handleContainerSlotStateChanged` | default-noop | `data/packetid/packetid.go:249`; no dispatch case; default at `server/tick.go:2468`. | Slot-state toggles are ignored. |
| 21 | `ServerboundCookieResponse` | `ServerCommonPacketListenerImpl.handleCookieResponse` | default-noop | `data/packetid/packetid.go:250`; no dispatch case; default at `server/tick.go:2468`; common handler verified by `javap -p`. | Server cookie responses are ignored. |
| 22 | `ServerboundCustomPayload` | `ServerGamePacketListenerImpl.handleCustomPayload` | default-noop | `data/packetid/packetid.go:251`; no dispatch case; default at `server/tick.go:2468`. | Plugin/custom payload channels are ignored. |
| 23 | `ServerboundDebugSubscriptionRequest` | `ServerGamePacketListenerImpl.handleDebugSubscriptionRequest` | default-noop | `data/packetid/packetid.go:252`; no dispatch case; default at `server/tick.go:2468`. | Debug subscriptions are unavailable. |
| 24 | `ServerboundEditBook` | `ServerGamePacketListenerImpl.handleEditBook` | default-noop | `data/packetid/packetid.go:253`; no dispatch case; default at `server/tick.go:2468`. | Writable books cannot be saved or signed. |
| 25 | `ServerboundEntityTagQuery` | `ServerGamePacketListenerImpl.handleEntityTagQuery` | default-noop | `data/packetid/packetid.go:254`; no dispatch case; default at `server/tick.go:2468`. | Entity NBT query/debug request gets no response. |
| 26 | `ServerboundInteract` | `ServerGamePacketListenerImpl.handleInteract` | partial | `data/packetid/packetid.go:255`; buffered at `server/tick.go:2196`; applied at `server/subtick.go:384`; handler at `server/attack_dispatch.go:1014`. | Entity right-click/feed/mount/display interactions exist for subsets; cross-region and many entity interactions are limited. |
| 27 | `ServerboundJigsawGenerate` | `ServerGamePacketListenerImpl.handleJigsawGenerate` | default-noop | `data/packetid/packetid.go:256`; no dispatch case; default at `server/tick.go:2468`. | Jigsaw generate action is ignored. |
| 28 | `ServerboundKeepAlive` | `ServerCommonPacketListenerImpl.handleKeepAlive` | partial | `data/packetid/packetid.go:257`; dispatch forwards to keepalive component at `server/tick.go:2326`. | Keep-alive responses can reset Sulfur's watchdog if wired; full vanilla common-listener latency behavior is not proven. |
| 29 | `ServerboundLockDifficulty` | `ServerGamePacketListenerImpl.handleLockDifficulty` | exact | `data/packetid/packetid.go:258`; dispatch at `server/tick.go:2460`; handler at `server/state_packets.go:112`. | Operators can lock/unlock difficulty and all clients receive the update. |
| 30 | `ServerboundMovePlayerPos` | `ServerGamePacketListenerImpl.handleMovePlayer` | partial | `data/packetid/packetid.go:259`; buffered at `server/tick.go:2190`; applied at `server/subtick.go:184`. | Position movement works with teleport gate, finite checks, collision, jump exhaustion; full anti-cheat/known-movement chain is incomplete. |
| 31 | `ServerboundMovePlayerPosRot` | `ServerGamePacketListenerImpl.handleMovePlayer` | partial | `data/packetid/packetid.go:260`; buffered at `server/tick.go:2191`; applied at `server/subtick.go:228`. | Position+look movement works; full vanilla movement side effects are incomplete. |
| 32 | `ServerboundMovePlayerRot` | `ServerGamePacketListenerImpl.handleMovePlayer` | partial | `data/packetid/packetid.go:261`; buffered at `server/tick.go:2192`; applied at `server/subtick.go:265`. | Look/on-ground updates work; full vanilla movement side effects are incomplete. |
| 33 | `ServerboundMovePlayerStatusOnly` | `ServerGamePacketListenerImpl.handleMovePlayer` | partial | `data/packetid/packetid.go:262`; buffered at `server/tick.go:2193`; applied at `server/subtick.go:283`. | On-ground status updates work; full vanilla movement side effects are incomplete. |
| 34 | `ServerboundMoveVehicle` | `ServerGamePacketListenerImpl.handleMoveVehicle` | partial | `data/packetid/packetid.go:263`; buffered at `server/tick.go:2230`; applied at `server/subtick.go:391`; handler at `server/passenger.go:750`. | Controlling passenger can steer vehicles; vanilla anti-cheat/collision is explicitly reduced. |
| 35 | `ServerboundPaddleBoat` | `ServerGamePacketListenerImpl.handlePaddleBoat` | default-noop | `data/packetid/packetid.go:264`; no dispatch case; default at `server/tick.go:2468`. | Boat paddle state/rowing animation is not updated server-side. |
| 36 | `ServerboundPickItemFromBlock` | `ServerGamePacketListenerImpl.handlePickItemFromBlock` | default-noop | `data/packetid/packetid.go:265`; no dispatch case; default at `server/tick.go:2468`. | Creative pick-block from blocks is ignored. |
| 37 | `ServerboundPickItemFromEntity` | `ServerGamePacketListenerImpl.handlePickItemFromEntity` | default-noop | `data/packetid/packetid.go:266`; no dispatch case; default at `server/tick.go:2468`. | Creative pick-block from entities is ignored. |
| 38 | `ServerboundPingRequest` | `ServerGamePacketListenerImpl.handlePingRequest` | default-noop | `data/packetid/packetid.go:267`; no dispatch case; default at `server/tick.go:2468`. | In-game ping requests receive no pong. |
| 39 | `ServerboundPlaceRecipe` | `ServerGamePacketListenerImpl.handlePlaceRecipe` | default-noop | `data/packetid/packetid.go:268`; no dispatch case; default at `server/tick.go:2468`. | Recipe-book auto-place is ignored. |
| 40 | `ServerboundPlayerAbilities` | `ServerGamePacketListenerImpl.handlePlayerAbilities` | exact | `data/packetid/packetid.go:269`; dispatch at `server/tick.go:2432`; handler at `server/state_packets.go:30`; matching bytecode verified by `javap -c`. | Creative/spectator flight toggle is honored; survival/adventure flight claims are rejected. |
| 41 | `ServerboundPlayerAction` | `ServerGamePacketListenerImpl.handlePlayerAction` | partial | `data/packetid/packetid.go:270`; buffered at `server/tick.go:2200`; applied at `server/subtick.go:291`; handler at `server/block_interact.go:80`. | Block breaking and release-use work; drop/swap/other player actions are mostly no-ops. |
| 42 | `ServerboundPlayerCommand` | `ServerGamePacketListenerImpl.handlePlayerCommand` | partial | `data/packetid/packetid.go:271`; direct dispatch at `server/tick.go:2265`. | Sprint toggles, stop-sleep, and fall-flying start are handled; other command actions are deferred. |
| 43 | `ServerboundPlayerInput` | `ServerGamePacketListenerImpl.handlePlayerInput` | partial | `data/packetid/packetid.go:272`; buffered at `server/tick.go:2194`; applied at `server/subtick.go:408`; handler at `server/passenger.go:668`. | Input bitfield is stored and camel jump uses it; shift/sprint bits are not fully consumed. |
| 44 | `ServerboundPlayerLoaded` | `ServerGamePacketListenerImpl.handleAcceptPlayerLoad` | partial | `data/packetid/packetid.go:273`; dispatch records `loaded` at `server/tick.go:2319`; vanilla `markClientLoaded` resets timeout (`javap -c`). | Arrival is recorded, but vanilla client-load timeout semantics are not modeled. |
| 45 | `ServerboundPong` | `ServerCommonPacketListenerImpl.handlePong` | default-noop | `data/packetid/packetid.go:274`; no dispatch case; default at `server/tick.go:2468`; common handler verified by `javap -p`. | Pong latency replies are ignored. |
| 46 | `ServerboundRecipeBookChangeSettings` | `ServerGamePacketListenerImpl.handleRecipeBookChangeSettingsPacket` | default-noop | `data/packetid/packetid.go:275`; no dispatch case; default at `server/tick.go:2468`. | Recipe-book GUI settings are not persisted. |
| 47 | `ServerboundRecipeBookSeenRecipe` | `ServerGamePacketListenerImpl.handleRecipeBookSeenRecipePacket` | default-noop | `data/packetid/packetid.go:276`; no dispatch case; default at `server/tick.go:2468`. | Recipe highlight clearing is ignored. |
| 48 | `ServerboundRenameItem` | `ServerGamePacketListenerImpl.handleRenameItem` | partial | `data/packetid/packetid.go:277`; buffered at `server/tick.go:2223`; applied at `server/subtick.go:360`; handler at `server/anvil_menu.go:583`. | Anvil rename works for implemented anvil behavior; filtering/full anvil parity is limited. |
| 49 | `ServerboundResourcePack` | `ServerCommonPacketListenerImpl.handleResourcePackResponse` | default-noop | `data/packetid/packetid.go:278`; no dispatch case; default at `server/tick.go:2468`. | Resource-pack accept/decline/download status is ignored. |
| 50 | `ServerboundSeenAdvancements` | `ServerGamePacketListenerImpl.handleSeenAdvancements` | partial | `data/packetid/packetid.go:279`; dispatch at `server/tick.go:2406`. | Opening an advancement tab is echoed; full advancement screen/selection state is limited. |
| 51 | `ServerboundSelectTrade` | `ServerGamePacketListenerImpl.handleSelectTrade` | partial | `data/packetid/packetid.go:280`; buffered at `server/tick.go:2221`; applied at `server/subtick.go:352`; handler at `server/merchant_menu.go:322`. | Merchant selection/autofill works for implemented merchant menus. |
| 52 | `ServerboundSetBeacon` | `ServerGamePacketListenerImpl.handleSetBeaconPacket` | partial | `data/packetid/packetid.go:281`; buffered at `server/tick.go:2222`; applied at `server/subtick.go:367`; handler at `server/beacon_menu.go:522`. | Beacon effect/payment path works; invalid effect disconnect behavior and full subsystem parity are limited. |
| 53 | `ServerboundSetCarriedItem` | `ServerGamePacketListenerImpl.handleSetCarriedItem` | partial | `data/packetid/packetid.go:282`; buffered at `server/tick.go:2235`; applied at `server/subtick.go:335`; handler at `server/inventory.go:383`; vanilla bytecode also stops main-hand use on slot change. | Held hotbar slot changes; use-cancel/logging side effects are incomplete. |
| 54 | `ServerboundSetCommandBlock` | `ServerGamePacketListenerImpl.handleSetCommandBlock` | default-noop | `data/packetid/packetid.go:283`; no dispatch case; default at `server/tick.go:2468`. | Command block edits are ignored. |
| 55 | `ServerboundSetCommandMinecart` | `ServerGamePacketListenerImpl.handleSetCommandMinecart` | default-noop | `data/packetid/packetid.go:284`; no dispatch case; default at `server/tick.go:2468`. | Command minecart edits are ignored. |
| 56 | `ServerboundSetCreativeModeSlot` | `ServerGamePacketListenerImpl.handleSetCreativeModeSlot` | partial | `data/packetid/packetid.go:285`; buffered at `server/tick.go:2214`; applied at `server/subtick.go:329`; handler at `server/inventory.go:340`. | Creative inventory slot setting works; negative-slot drop/throttling and feature checks are deferred. |
| 57 | `ServerboundSetGameRule` | `ServerGamePacketListenerImpl.handleSetGameRule` | default-noop | `data/packetid/packetid.go:286`; no dispatch case; default at `server/tick.go:2468`. | Gamerule GUI packet is ignored. |
| 58 | `ServerboundSetJigsawBlock` | `ServerGamePacketListenerImpl.handleSetJigsawBlock` | default-noop | `data/packetid/packetid.go:287`; no dispatch case; default at `server/tick.go:2468`. | Jigsaw block edits are ignored. |
| 59 | `ServerboundSetStructureBlock` | `ServerGamePacketListenerImpl.handleSetStructureBlock` | default-noop | `data/packetid/packetid.go:288`; no dispatch case; default at `server/tick.go:2468`. | Structure block edits are ignored. |
| 60 | `ServerboundSetTestBlock` | `ServerGamePacketListenerImpl.handleSetTestBlock` | default-noop | `data/packetid/packetid.go:289`; no dispatch case; default at `server/tick.go:2468`. | Test block edits are ignored. |
| 61 | `ServerboundSignUpdate` | `ServerGamePacketListenerImpl.handleSignUpdate` | partial | `data/packetid/packetid.go:290`; buffered at `server/tick.go:2226`; applied at `server/subtick.go:305`; handler at `server/sign.go:282`. | Sign edits store and broadcast for valid editor; chat filtering/full sign parity is limited. |
| 62 | `ServerboundSpectatorAction` | `ServerGamePacketListenerImpl.handleSpectatorAction` | default-noop | `data/packetid/packetid.go:291`; no dispatch case; default at `server/tick.go:2468`. | Spectator menu action is ignored. |
| 63 | `ServerboundSwing` | `ServerGamePacketListenerImpl.handleAnimate` | partial | `data/packetid/packetid.go:292`; buffered at `server/tick.go:2197`; applied at `server/subtick.go:401`; handler at `server/entity_events.go:43`; vanilla also resets last action time (`javap -c`). | Other players see arm swings; last-action-time and invalid enum behavior differ. |
| 64 | `ServerboundTeleportToEntity` | `ServerGamePacketListenerImpl.handleTeleportToEntityPacket` | default-noop | `data/packetid/packetid.go:293`; no dispatch case; default at `server/tick.go:2468`. | Spectator teleport-to-entity is ignored. |
| 65 | `ServerboundTestInstanceBlockAction` | `ServerGamePacketListenerImpl.handleTestInstanceBlockAction` | default-noop | `data/packetid/packetid.go:294`; no dispatch case; default at `server/tick.go:2468`. | Test instance block action is ignored. |
| 66 | `ServerboundUseItemOn` | `ServerGamePacketListenerImpl.handleUseItemOn` | partial | `data/packetid/packetid.go:295`; buffered at `server/tick.go:2199`; applied at `server/subtick.go:299`; handler at `server/block_interact.go:117`. | Block place/interact subset works with sequence ACK; full block/item interaction matrix is incomplete. |
| 67 | `ServerboundUseItem` | `ServerGamePacketListenerImpl.handleUseItem` | partial | `data/packetid/packetid.go:296`; buffered at `server/tick.go:2198`; applied at `server/subtick.go:312`; handler at `server/item_use.go:142`. | Right-click-air food/tools/projectiles subset works; full item-use parity is incomplete. |
| 68 | `ServerboundCustomClickAction` | `ServerCommonPacketListenerImpl.handleCustomClickAction` | default-noop | `data/packetid/packetid.go:297`; no dispatch case; default at `server/tick.go:2468`; common handler verified by `javap -p`. | Custom click action responses are ignored. |
| 69 | `ServerboundPacketIDGuard` | n/a generated sentinel | default-noop | `data/packetid/packetid.go:298`; no dispatch case; default at `server/tick.go:2468`. | Not a real vanilla packet; if received as id 69 it is ignored. |

## Priority list

1. **Core client UX / survival gameplay default-noops:** `ServerboundPlaceRecipe`, `ServerboundRecipeBookSeenRecipe`, `ServerboundRecipeBookChangeSettings`, `ServerboundEditBook`, `ServerboundContainerSlotStateChanged`, `ServerboundBundleItemSelected`, `ServerboundPaddleBoat`.
2. **Protocol/common correctness default-noops:** `ServerboundConfigurationAcknowledged`, `ServerboundCustomPayload`, `ServerboundCookieResponse`, `ServerboundResourcePack`, `ServerboundPingRequest`, `ServerboundPong`, `ServerboundCustomClickAction`.
3. **Spectator/creative parity:** `ServerboundSpectatorAction`, `ServerboundTeleportToEntity`, `ServerboundPickItemFromBlock`, `ServerboundPickItemFromEntity`.
4. **Operator/admin block packets:** `ServerboundSetCommandBlock`, `ServerboundSetCommandMinecart`, `ServerboundSetGameRule`, `ServerboundSetJigsawBlock`, `ServerboundJigsawGenerate`, `ServerboundSetStructureBlock`, `ServerboundSetTestBlock`, `ServerboundTestInstanceBlockAction`.
5. **Debug/NBT requests:** `ServerboundBlockEntityTagQuery`, `ServerboundEntityTagQuery`, `ServerboundDebugSubscriptionRequest`.
6. **Upgrade partial handlers toward exact:** movement (`MovePlayer*`, `ClientTickEnd`, `AcceptTeleportation`), inventory/menu (`ContainerClick`, `SetCarriedItem`, `SetCreativeModeSlot`), chat/signing, and entity interaction/action surfaces.

## Proposed ledger rows (do not edit `PARITY-LEDGER.csv`)

```csv
id,domain,jar_class,jar_method,status,go_path,jar_evidence,test,rng_verified,owner,last_verified_commit,notes
NET-SERVERBOUND-CENSUS,protocol,net.minecraft.server.network.ServerGamePacketListenerImpl,<all serverbound game handlers>,partial,.planning/parity-workflow/outputs/census-serverbound.md,javap -p/-c ServerGamePacketListenerImpl + packetid enum,,n/a,minimax-census,,70 enum entries counted: exact=4 partial=33 explicit-noop=3 default-noop=30 unverified=0; 40 actual explicit dispatch packet cases
NET-SERVERBOUND-DEFAULT-NOOPS,protocol,net.minecraft.server.network.ServerGamePacketListenerImpl,<missing dispatch cases>,partial,server/tick.go:2468,javap ServerGamePacketListener + ServerCommonPacketListener,,n/a,minimax-census,,30 enum entries fall through default no-op including generated guard; prioritize recipe/book/common/spectator/admin/debug groups
NET-SERVERBOUND-PARTIALS,protocol,net.minecraft.server.network.ServerGamePacketListenerImpl,<partial handlers>,partial,server/tick.go;server/subtick.go,javap -c selected handlers,,n/a,minimax-census,,33 routed handlers have user-visible behavior but deferred vanilla branches/subsystems remain
```

## Commands and read-only tool actions executed

- `Get-Content -LiteralPath "CLAUDE.md"`
- Glob/Grep searches over `**/*.go`, `**/*.md`, `.planning/**/*.csv`, and serverbound/dispatch handler patterns.
- `Test-Path -LiteralPath "temp\cache\26.2-inner.jar"`
- `Get-Content` line-range reads for `data/packetid/packetid.go`, `server/tick.go`, `server/subtick.go`, `server/state_packets.go`, `server/player_visibility.go`, `server/tick_phases.go`, `server/inventory.go`, `server/block_interact.go`, `server/item_use.go`, `server/attack_dispatch.go`, `server/passenger.go`, `server/entity_events.go`, `server/chat.go`, `server/commands.go`, `server/commands_suggest.go`, `server/combat.go`, `server/sign.go`, `server/cooking_block.go`, `server/merchant_menu.go`, `server/anvil_menu.go`, `server/beacon_menu.go`.
- `javap -classpath "temp\cache\26.2-inner.jar" -p net.minecraft.server.network.ServerGamePacketListenerImpl`
- `javap -classpath "temp\cache\26.2-inner.jar" -p net.minecraft.network.protocol.game.ServerGamePacketListener`
- `javap -classpath "temp\cache\26.2-inner.jar" -p net.minecraft.network.protocol.common.ServerCommonPacketListener`
- `javap -classpath "temp\cache\26.2-inner.jar" -p net.minecraft.server.network.ServerCommonPacketListenerImpl`
- `javap -classpath "temp\cache\26.2-inner.jar" -p net.minecraft.network.protocol.cookie.ServerCookiePacketListener`
- `javap -classpath "temp\cache\26.2-inner.jar" -c -p net.minecraft.server.network.ServerGamePacketListenerImpl | Select-String -Pattern ... -Context ...`
- `Get-Content -LiteralPath ".planning\parity-workflow\PARITY-LEDGER.csv"`
- PowerShell coverage script comparing generated enum names to `server/tick.go` dispatch references.

No Go code, tests, scripts, or ledger files were edited.
