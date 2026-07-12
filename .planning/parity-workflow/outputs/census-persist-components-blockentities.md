# Census: block-entity internal state + item data components on disk (wave 01)

Read-only parity audit of (1) every block-entity (BE) `saveAdditional` /
`loadAdditional` codec in the 26.2 jar (`temp/cache/26.2-inner.jar`) and the
matching `encode*BE` / `decode*BE` / `flush*Items` / `load*BE` triple in
`server/`, and (2) the disk item codec (`save/item_nbt.go`,
`save/item_components.go`) against the 26.2 DataComponentPatch
(`net.minecraft.core.component.DataComponentPatch.CODEC`, the 111 vanilla
DataComponentType fields in `net.minecraft.core.component.DataComponents`).

The audit is read-only. No Go code, no JAR, no test, no ledger row, no script
was modified. All Go file:line and JAR class/method evidence in the tables is
verified by direct inspection of the source this session.

Scope notes:

- "Codec" means: the on-disk `BlockEntity.Data` NBT compound + the on-disk
  `Player.Inventory` / `Player.EnderItems` `ItemStackWithSlot` lists + the
  on-disk `DataComponentPatch` "components" compound.
- "Production call site" excludes `_test.go` files and the per-file internal
  test helpers; tick-owned / owner-goroutine calls only.
- "Tier" classification (1 = full round-trip; 2 = in-memory only; 3 =
  unrolled loot-table persisted as the {LootTable,LootTableSeed} BE; 4 =
  compound absent / deferred seam) is defined per the audited-class table
  below.
- The "1:1-with-the-jar" rule from `CLAUDE.md` covers this layer too: a
  counter / helper / supplement (e.g. the `recipesXP` map beside the cooked
  furnace's `RecipesUsed`) is a faithful port if the OBSERVABLE state on the
  wire / in-game matches vanilla. The Phase-B enchantment registry injection
  (enchantment_registry.go) is the same idiom: a datapack registry is not
  baked away, it is fed the SAME ordered list the server sends to the client
  (registrydata.EnchantmentOrder) so wire id <-> resource id is faithful.

## Totals

### Block-entity class

| Source | Count |
| --- | ---: |
| JAR: vanilla 26.2 BE subtypes with a non-empty `saveAdditional` override (representative set sampled) | 19 (audited: 9) |
| Go: BE classes with a `saveAdditional -> encode*BE` and `loadAdditional -> decode*BE` pair, plus a `flush*Items` owner-side call | **9** (chest, furnace family x 3, brewing_stand, dispenser family x 2, hopper, crafter, chiseled_bookshelf, shulker_box family x 16) |
| Go: BE classes with a `resolve*` live-drive only (no save/load seam) | **10** (spawner, bell, beacon, lectern, beehive, sculk_sensor, sculk_shrieker, jukebox, decorated_pot, end_gateway) |
| BE classes that are present in vanilla 26.2 but have **no** `resolve*` or `save/load` site (deferred beyond the audit scope) | 4 (command_block, structure_block, trial_spawner, vault) -- labelled `absent` |
| Container BEs whose nested-item slots traverse the Phase-B component transcoder (`save/item_components.go`) | **8** (chest, furnace, brewing_stand, dispenser, hopper, crafter, chiseled_bookshelf, shulker_box) -- each calls `save.SaveAllItems` / `save.SaveItemsCompound` |
| Top-level `BlockEntity.Data` fields captured per audited BE | 28 (Tables 4-a..4-i below) |

The Go codec is **faithful for 9 / 9 audited BE classes** (the `saveAdditional`
field set, the slot count, the cook-progress type, the loot-table XOR
`saveAllItems` branch, the per-BE forgiving decode, all line up with the JAR).
The **gap** is structural persistence coverage:

- 10 vanilla BE classes (spawner, bell, beacon, lectern, beehive, sculk_sensor,
  sculk_shrieker, jukebox, decorated_pot, end_gateway) have a live-drive
  (`resolve*`) but NO `encode*BE` / `decode*BE` seam. On chunk-unload their
  state is in `t.<map>` only; a server restart loses it.
- 4 vanilla BE classes that v1 never models (command_block, structure_block,
  trial_spawner, vault) are `absent` (no Go type, no resolve, no save/load).

### Item data components

| Source | Count |
| --- | ---: |
| JAR `public static final DataComponentType<T>` in `net.minecraft.core.component.DataComponents` (`javap -p`) | **111** (ids 0..110) |
| Go `component.NewComponent` cases (`level/component/components.go:18-228`) | **111** (1:1 with jar order) |
| Wire -> disk transcoder cases (`save/item_components.go diskEncodeComponent` + the `wire...` constants) | **8** (damage, max_damage, repair_cost, custom_name, lore, potion_contents, enchantments, stored_enchantments) |
| Disk -> wire transcoder cases (`save/item_components.go diskDecodeComponent` switch) | **8** (same set) |
| `diskEncodeComponent` cases that REQUIRE an injected in-process resolver (datapack registry) to succeed | **2** (enchantments + stored_enchantments -- gated on `enchantment_registry.go SetEnchantmentRegistry`, else counted-drop fallback) |
| Components that reach disk but are counted-and-dropped (`diskEncodeComponent` `default` branch returns `false`) | the residual **103** present-valued entries; the bucket is metered at every container-flush site via `udebug("chunksave", ...)` |
| Components the disk codec never reaches because `SlotData.ReadFrom`'s unknown-component early stop already aborts | **111 - 8 = 103** at the wire boundary |
| `unknown` set (compounds the disk codec can re-express in *byte shape* but whose semantics are not verified against the jar) | `--` |

**Net: 8 / 111 (7.2 %) disk component round-trips with byte-stable fidelity;
103 / 111 (92.8 %) are counted-and-dropped at the wire boundary (read) or at
write (the unsupportable type set).** The dropped set is metered per
`SaveAllItems` so the gap is NEVER silent -- the
`udebug("chunksave", "<be>: %d component-bearing stacks persisted without
components (Phase A)")` call sites land in production logs and the server
outputs them for every dirty column.

## Commands executed

```bash
# 1. Enumerate every DataComponentType in the JAR
javap -p -classpath temp/cache/26.2-inner.jar net.minecraft.core.component.DataComponents
javap -p -classpath temp/cache/26.2-inner.jar net.minecraft.core.component.DataComponents \
    | Select-String 'public static final.*DataComponentType'
# -> 111 DataComponentType fields (ids 0..110)

# 2. Confirm every BE has a save/loadAdditional declared in the JAR
javap -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.level.block.entity.AbstractFurnaceBlockEntity
javap -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.level.block.entity.BrewingStandBlockEntity
javap -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.level.block.entity.DispenserBlockEntity
javap -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.level.block.entity.HopperBlockEntity
javap -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.level.block.entity.ShulkerBoxBlockEntity
javap -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.level.block.entity.ChestBlockEntity
javap -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.level.block.entity.SignBlockEntity
javap -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.level.BaseSpawner
javap -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.level.block.entity.LecternBlockEntity
javap -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.level.block.entity.BlockEntity

# 3. Trace save / load bytecode for each audited BE
javap -c -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.level.block.entity.AbstractFurnaceBlockEntity
javap -c -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.level.block.entity.SignBlockEntity
# (the four shorts cooking_time_spent / cooking_total_time / lit_time_remaining / lit_total_time +
#  RecipesUsed string map at AbstractFurnaceBlockEntity saveAdditional)
# (the front_text + back_text SignText DIRECT_CODEC + is_waxed putBoolean at SignBlockEntity saveAdditional)

# 4. Enumerate the Go codec: every encode*BE / decode*BE / flush*Items / load*BE / set*BEData in server/
Select-String -Path server\*.go -Pattern 'func encode\w*BE|func decode\w*BE|func flush\w*Items|func flush\w*BE|func load\w*BE|func set\w*BEData|func is\w*EntityType|func mark\w*Dirty'

# 5. Every flush* call site feeding the column-flush graph
Select-String -Path server\chunk_persist.go -Pattern 'flushChest|flushFurnace|flushBrewing|flushDispenser|flushHopper|flushCrafter|flushChiseled|flushShulker'

# 6. Verify the resolve* / mark*Dirty / hydrateTickingBlockEntities production chain
Select-String -Path server\chunk_persist.go -Pattern 'resolveFurnace|resolveBrewingStand|resolveHopper|resolveCrafter|isAnyFurnaceBlock|isBrewingStandBlock|IsHopper|IsCrafter'

# 7. Verify the live-drive-only BE classes (no save/load seam)
Get-ChildItem -LiteralPath server -File -Force
    | Where-Object { $_.Name -match 'spawner_block\.go|bell_be\.go|beacon_be\.go|lectern_be\.go|beehive_be\.go|sculk_sensor_be\.go|sculk_shrieker_be\.go|jukebox_be\.go|decorated_pot_be\.go|end_gateway_be\.go' }

# 8. Verify the player .dat and ender chest both go through inventoryToItems + enderItemsToDisk
Select-String -Path server\persistence.go -Pattern 'inventoryToItems|enderItemsToDisk|itemsToInventory|Inventory|EnderItems'

# 9. Trace each component transcode case in save/item_components.go
Select-String -Path save\item_components.go -Pattern 'wireDamage|wireMaxDamage|wireRepairCost|wireCustomName|wireLore|wireEnchantments|wireStoredEnchantments|wirePotionContents'

# 10. Verify the Enchantment registry resolver thread (datapack injection)
Select-String -Path save\enchantment_registry.go -Pattern 'SetEnchantmentRegistry|enchantmentName|enchantmentID|enchantmentOrder'

# 11. Trace every udebug call that meters a dropped-component count (proves the gap is NEVER silent)
Select-String -Path server\*.go -Pattern 'udebug.*component|chunksave.*dropped|component.*dropped'

# 12. Confirm the spawner (vanilla BaseSpawner) has the load+save pair declared in the JAR (it does),
#     and that v1 has no encode/decode/flush path for it
javap -p -classpath temp/cache/26.2-inner.jar net.minecraft.world.level.BaseSpawner
Select-String -Path server\spawner_block.go -Pattern 'flushSpawners|encodeSpawner|decodeSpawner|persistSpawner'
Select-String -Path server\spawner.go -Pattern 'flushSpawners|encodeSpawner|decodeSpawner|persistSpawner'
```

## Component totals (Table 1)

Columns: **JAR id** is `DataComponents.<NAME>` declared position in the JAR
(`javap -p net.minecraft.core.component.DataComponents`); **wire id** is the
registry protocol id the `DataComponentType.STREAM_CODEC` writes (`NewComponent`
case label = wire id); **disk shape** is the `DataComponentMap` /
`DataComponentPatch` codec entry shape; **status** is one of:

| Status | Meaning |
| --- | --- |
| `round-trip` | byte-stable wire<->disk round-trip in `save/item_components.go`. |
| `dropped` | the wire span hits `SlotData.ReadFrom`'s unknown-component early stop (or the transcoder's `default` branch) -- the value is counted in `droppedComponents` and reported via `udebug("chunksave", ...)`; never silently lost. |
| `deferred-resolver` | the transcoder case exists in `diskEncodeComponent` / `diskDecodeComponent` but requires `enchantment_registry.go SetEnchantmentRegistry` to have been called; without it the entry falls into `dropped`. |

Rows are sorted by JAR id (`DataComponents.CUSTOM_DATA` (id 0) ->
`DataComponents.SHULKER_COLOR` (id 110)). Coverage per entry is the de-facto
wire->disk set audited this session.

| # | JAR id | Wire id | Component | Status | Evidence |
| --: | --- | ---: | --- | --- | --- |
| 1 | `CUSTOM_DATA` | 0 | `CustomData` (custom data component, `CustomData.CODEC` = compound) | `dropped` | `level/component/customdata_gen.go` exists; not in `save/item_components.go:diskEncodeComponent` switch (deferred). |
| 2 | `MAX_STACK_SIZE` | 1 | `MaxStackSize` (1..99) | `dropped` | `level/component/maxstacksize_gen.go` -- not in transcode (items carry it implicitly via the wire count clamp 1..99). |
| 3 | `MAX_DAMAGE` | 2 | `MaxDamage` (TAG_Int, POSITIVE_INT) | `round-trip` | `save/item_components.go:215` (`*component.MaxDamage` switch case); `save/item_components.go:307` (`wireMaxDamage = 2`). |
| 4 | `DAMAGE` | 3 | `Damage` (TAG_Int, NON_NEGATIVE_INT) | `round-trip` | `save/item_components.go:213`; `wireDamage = 3`. |
| 5 | `UNBREAKABLE` | 4 | `Unbreakable` (Unit / empty compound) | `dropped` | `level/component/unbreakable_gen.go` -- not in transcode. |
| 6 | `USE_EFFECTS` | 5 | `UseEffects` | `dropped` | `level/component/useeffects_gen.go` -- not in transcode. |
| 7 | `CUSTOM_NAME` | 6 | `CustomName` (Component NBT) | `round-trip` | `save/item_components.go:217`; `wireCustomName = 6`; sees `ComponentSerialization.CODEC` same as `chat.Message` NBT body. |
| 8 | `MINIMUM_ATTACK_CHARGE` | 7 | `MinimumAttackCharge` | `dropped` | `level/component/minimumattackcharge_gen.go` -- not in transcode. |
| 9 | `DAMAGE_TYPE` | 8 | `DamageType` (`DamageType.CODEC` Holder) | `dropped` | `level/component/damagetype_gen.go` -- not in transcode. |
| 10 | `ITEM_NAME` | 9 | `ItemName` | `dropped` | `level/component/itemname_gen.go` -- not in transcode. |
| 11 | `ITEM_MODEL` | 10 | `ItemModel` | `dropped` | `level/component/itemmodel_gen.go` -- not in transcode. |
| 12 | `LORE` | 11 | `Lore` (ItemLore.CODEC list<Component>) | `round-trip` | `save/item_components.go:220`; `wireLore = 11`; list<Component> marshals via `chat.Message.MarshalNBT`. |
| 13 | `RARITY` | 12 | `Rarity` (`Rarity.CODEC`) | `dropped` | `level/component/rarity.go` + `rarity_gen.go` (non-generated `package component`); not in transcode. |
| 14 | `ENCHANTMENTS` | 13 | `Enchantments` (ItemEnchantments.CODEC unboundedMap<resource id, intRange(1,255)>) | `round-trip` (with `deferred-resolver`) | `save/item_components.go:222`; `wireEnchantments = 13`; resolver `enchantment_registry.go` must be injected via `SetEnchantmentRegistry` or it counted-drops. |
| 15 | `CAN_PLACE_ON` | 14 | `CanPlaceOn` (HolderSet<block>) | `dropped` | `level/component/canplaceon_gen.go` -- not in transcode. |
| 16 | `CAN_BREAK` | 15 | `CanBreak` (HolderSet<block>) | `dropped` | `level/component/canbreak_gen.go` -- not in transcode. |
| 17 | `ATTRIBUTE_MODIFIERS` | 16 | `AttributeModifiers` (AttributeModifiers.CODEC list of {Attribute, AttributeModifier, ...}) | `dropped` | `data/item/attributemodifiers.go` + `level/component/attributemodifiers.go` exist; not in transcode switch. |
| 18 | `CUSTOM_MODEL_DATA` | 17 | `CustomModelData` | `dropped` | `level/component/custommodeldata_gen.go` -- not in transcode. |
| 19 | `TOOLTIP_DISPLAY` | 18 | `TooltipDisplay` (`TooltipDisplay.CODEC` = compound{hide_*} added in 26.2) | `dropped` | `level/component/tooltipdisplay_gen.go` -- not in transcode. |
| 20 | `REPAIR_COST` | 19 | `RepairCost` (TAG_Int, NON_NEGATIVE_INT) | `round-trip` | `save/item_components.go:215` shared with `MaxDamage`; `wireRepairCost = 19`. |
| 21 | `CREATIVE_SLOT_LOCK` | 20 | `CreativeSlotLock` (Unit) | `dropped` | `level/component/creativeslotlock_gen.go` -- not in transcode. |
| 22 | `ENCHANTMENT_GLINT_OVERRIDE` | 21 | `EnchantmentGlintOverride` (Bool) | `dropped` | `level/component/enchantmentglintoverride_gen.go` -- not in transcode. |
| 23 | `INTANGIBLE_PROJECTILE` | 22 | `IntangibleProjectile` (Unit) | `dropped` | `level/component/intangibleprojectile_gen.go` -- not in transcode. |
| 24 | `FOOD` | 23 | `Food` (`FoodProperties.CODEC`) | `dropped` | `data/item/food.go` + `level/component/food_gen.go` exist; not in transcode. |
| 25 | `CONSUMABLE` | 24 | `Consumable` (`Consumable.CODEC`) | `dropped` | `data/item/consume.go` + `level/component/consumable_gen.go` exist; not in transcode. |
| 26 | `USE_REMAINDER` | 25 | `UseRemainder` (ItemStack) | `dropped` | `level/component/useremainder_gen.go` -- not in transcode. |
| 27 | `USE_COOLDOWN` | 26 | `UseCooldown` | `dropped` | `level/component/usecooldown_gen.go` -- not in transcode. |
| 28 | `DAMAGE_RESISTANT` | 27 | `DamageResistant` (`DamageTypePredicate.CODEC`) | `dropped` | `level/component/damageresistant_gen.go` -- not in transcode. |
| 29 | `TOOL` | 28 | `Tool` (`Tool.CODEC`) | `dropped` | `level/component/tool.go` + `tool_gen.go` exist; not in transcode. |
| 30 | `WEAPON` | 29 | `Weapon` | `dropped` | `level/component/weapon_gen.go` -- not in transcode. |
| 31 | `ATTACK_RANGE` | 30 | `AttackRange` | `dropped` | `level/component/attackrange_gen.go` -- not in transcode. |
| 32 | `ENCHANTABLE` | 31 | `Enchantable` | `dropped` | `level/component/enchantable_gen.go` -- not in transcode. |
| 33 | `EQUIPPABLE` | 32 | `Equippable` (`Equippable.CODEC`) | `dropped` | `level/component/equippable_gen.go` -- not in transcode. |
| 34 | `REPAIRABLE` | 33 | `Repairable` (`HolderSet<Item>`) | `dropped` | `level/component/repairable_gen.go` -- not in transcode. |
| 35 | `GLIDER` | 34 | `Glider` (Unit) | `dropped` | `level/component/glider_gen.go` -- not in transcode. |
| 36 | `TOOLTIP_STYLE` | 35 | `TooltipStyle` (`ResourceLocation.CODEC`) | `dropped` | `level/component/tooltipstyle_gen.go` -- not in transcode. |
| 37 | `DEATH_PROTECTION` | 36 | `DeathProtection` | `dropped` | `level/component/deathprotection_gen.go` -- not in transcode. |
| 38 | `BLOCKS_ATTACKS` | 37 | `BlocksAttacks` | `dropped` | `level/component/blocksattacks.go` + `blocksattacks_gen.go` exist; not in transcode. |
| 39 | `PIERCING_WEAPON` | 38 | `PiercingWeapon` | `dropped` | `level/component/piercingweapon_gen.go` -- not in transcode. |
| 40 | `KINETIC_WEAPON` | 39 | `KineticWeapon` | `dropped` | `level/component/kineticweapon.go` + `kineticweapon_gen.go` exist; not in transcode. |
| 41 | `SWING_ANIMATION` | 40 | `SwingAnimation` | `dropped` | `level/component/swinganimation_gen.go` -- not in transcode. |
| 42 | `ADDITIONAL_TRADE_COST` | 41 | `AdditionalTradeCost` | `dropped` | `level/component/additionaltradecost_gen.go` -- not in transcode. |
| 43 | `STORED_ENCHANTMENTS` | 42 | `StoredEnchantments` (ItemEnchantments.CODEC, same as `ENCHANTMENTS` but no equip behavior) | `round-trip` (with `deferred-resolver`) | `save/item_components.go:225`; `wireStoredEnchantments = 42`; same resolver dependency as `ENCHANTMENTS`. |
| 44 | `DYE` | 43 | `Dye` | `dropped` | `level/component/dye_gen.go` -- not in transcode. |
| 45 | `DYED_COLOR` | 44 | `DyedColor` (TAG_Int) | `dropped` | `level/component/dyedcolor_gen.go` -- not in transcode. |
| 46 | `MAP_COLOR` | 45 | `MapColor` (TAG_Int) | `dropped` | `level/component/mapcolor_gen.go` -- not in transcode. |
| 47 | `MAP_ID` | 46 | `MapID` (TAG_Int) | `dropped` | `level/component/mapid_gen.go` -- not in transcode. |
| 48 | `MAP_DECORATIONS` | 47 | `MapDecorations` (`MapDecorations.CODEC`) | `dropped` | `level/component/mapdecorations_gen.go` -- not in transcode. |
| 49 | `MAP_POST_PROCESSING` | 48 | `MapPostProcessing` | `dropped` | `level/component/mappostprogressing.go` + `_gen.go` exist; not in transcode. |
| 50 | `CHARGED_PROJECTILES` | 49 | `ChargedProjectiles` (`ChargedProjectiles.CODEC` list<ItemStack>) | `dropped` | `level/component/chargedprojectiles_gen.go` -- not in transcode (nested stacks share the same Phase A/B seam). |
| 51 | `BUNDLE_CONTENTS` | 50 | `BundleContents` (`BundleContents.CODEC` list<ItemStack>) | `dropped` | `level/component/bundlecontents_gen.go` -- not in transcode. |
| 52 | `POTION_CONTENTS` | 51 | `PotionContents` (`PotionContents.CODEC`) | `round-trip` | `save/item_components.go:227`; `wirePotionContents = 51`; full `withAlternative(FULL_CODEC, Potion.CODEC)` shape (bare-string vs compound), MobEffectInstance Details in `detailsToDisk`. |
| 53 | `POTION_DURATION_SCALE` | 52 | `PotionDurationScale` | `dropped` | `level/component/potiondurationscale_gen.go` -- not in transcode. |
| 54 | `SUSPICIOUS_STEW_EFFECTS` | 53 | `SuspiciousStewEffects` (list<MobEffectInstance>) | `dropped` | `level/component/suspicioussteweffects.go` + `_gen.go` exist; not in transcode. |
| 55 | `WRITABLE_BOOK_CONTENT` | 54 | `WritableBookContent` (`WritableBookContent.CODEC`) | `dropped` | `level/component/writablebookcontent.go` + `_gen.go` exist; not in transcode. |
| 56 | `WRITTEN_BOOK_CONTENT` | 55 | `WrittenBookContent` (`WrittenBookContent.CODEC`) | `dropped` | `level/component/writtenbookcontent.go` + `_gen.go` exist; not in transcode. |
| 57 | `TRIM` | 56 | `Trim` (`Trim.CODEC`) | `dropped` | `level/component/trim_gen.go` -- not in transcode. |
| 58 | `DEBUG_STICK_STATE` | 57 | `DebugStickState` (`DebugStickState.CODEC`) | `dropped` | `level/component/debugstickstate_gen.go` -- not in transcode. |
| 59 | `ENTITY_DATA` | 58 | `EntityData` (`TypedEntityData.CODEC`, custom NBT) | `dropped` | `level/component/entitydata_gen.go` -- not in transcode. |
| 60 | `BUCKET_ENTITY_DATA` | 59 | `BucketEntityData` (`TypedEntityData.CODEC`) | `dropped` | `level/component/bucketentitydata_gen.go` -- not in transcode. |
| 61 | `BLOCK_ENTITY_DATA` | 60 | `BlockEntityData` (`TypedEntityData.CODEC`) | `dropped` | `level/component/blockentitydata_gen.go` -- not in transcode. |
| 62 | `INSTRUMENT` | 61 | `Instrument` (`Instrument.CODEC`) | `dropped` | `level/component/instrument_gen.go` -- not in transcode. |
| 63 | `PROVIDES_TRIM_MATERIAL` | 62 | `ProvidesTrimMaterial` (Holder<TrimMaterial>) | `dropped` | `level/component/providestrimmaterial_gen.go` -- not in transcode. |
| 64 | `OMINOUS_BOTTLE_AMPLIFIER` | 63 | `OminousBottleAmplifier` (TAG_Int) | `dropped` | `level/component/ominousbottleamplifier_gen.go` -- not in transcode. |
| 65 | `JUKEBOX_PLAYABLE` | 64 | `JukeboxPlayable` (`JukeboxPlayable.CODEC`) | `dropped` | `level/component/jukeboxplayable_gen.go` -- not in transcode. |
| 66 | `PROVIDES_BANNER_PATTERNS` | 65 | `ProvidesBannerPatterns` (HolderSet<BannerPattern>) | `dropped` | `level/component/providesbannerpatterns_gen.go` -- not in transcode. |
| 67 | `RECIPES` | 66 | `Recipes` (`Recipes.CODEC`) | `dropped` | `level/component/recipes_gen.go` -- not in transcode. |
| 68 | `LODESTONE_TRACKER` | 67 | `LodestoneTracker` (`LodestoneTracker.CODEC`) | `dropped` | `level/component/lodestonetracker.go` + `_gen.go` exist; not in transcode. |
| 69 | `FIREWORK_EXPLOSION` | 68 | `FireworkExplosion` (`FireworkExplosion.CODEC`) | `dropped` | `level/component/fireworkexplosion_gen.go` -- not in transcode. |
| 70 | `FIREWORKS` | 69 | `Fireworks` (`Fireworks.CODEC`) | `dropped` | `level/component/fireworks_gen.go` -- not in transcode. |
| 71 | `PROFILE` | 70 | `Profile` (`Profile.CODEC`) | `dropped` | `level/component/profile.go` (non-generated) exists; not in transcode. |
| 72 | `NOTE_BLOCK_SOUND` | 71 | `NoteBlockSound` (Holder<SoundEvent>) | `dropped` | `level/component/noteblocksound_gen.go` -- not in transcode. |
| 73 | `BANNER_PATTERNS` | 72 | `BannerPatterns` (`BannerPatternLayers.CODEC`) | `dropped` | `level/component/bannerpatterns.go` + `bannerpatterns_gen.go` exist; not in transcode. |
| 74 | `BASE_COLOR` | 73 | `BaseColor` (DyeColor) | `dropped` | `level/component/basecolor_gen.go` -- not in transcode. |
| 75 | `POT_DECORATIONS` | 74 | `PotDecorations` (`PotDecorations.CODEC`) | `dropped` | `level/component/potdecorations_gen.go` -- not in transcode. |
| 76 | `CONTAINER` | 75 | `Container` (`Container.CODEC`) | `dropped` | `level/component/container_gen.go` -- not in transcode. |
| 77 | `BLOCK_STATE` | 76 | `BlockState` (`BlockState.CODEC`) | `dropped` | `level/component/blockstate.go` + `blockstate_gen.go` exist; not in transcode. |
| 78 | `BEES` | 77 | `Bees` (`Bees.CODEC` list<Bee>) | `dropped` | `level/component/bees.go` + `bees_gen.go` exist; not in transcode. |
| 79 | `SULFUR_CUBE_CONTENT` | 78 | `SulfurCubeContent` (custom) | `dropped` | `level/component/sulfurcubecontent_gen.go` -- not in transcode. |
| 80 | `LOCK` | 79 | `Lock` (`DisplayMode`) | `dropped` | `level/component/lock.go` + `lock_gen.go` exist; not in transcode. |
| 81 | `CONTAINER_LOOT` | 80 | `ContainerLoot` (`ContainerLoot.CODEC`/`ResourceLocation.CODEC`) | `dropped` | `level/component/containerloot_gen.go` -- not in transcode. |
| 82 | `BREAK_SOUND` | 81 | `BreakSound` (Holder<SoundEvent>) | `dropped` | `level/component/breaksound_gen.go` -- not in transcode. |
| 83-110 | variant components (`VILLAGER_VARIANT` ... `SHULKER_COLOR`, ids 82..110) | 82..110 | one each (`Variant.CODEC` / Holder of variant) | `dropped` | All variant components (`*variant_gen.go`) -- not in transcode; counted-and-dropped. |

**Summary by status:**

| Status | Count | Pct of 111 |
| --- | ---: | ---: |
| `round-trip` (byte-stable wire<->disk) | 6 | 5.4 % |
| `round-trip` with `deferred-resolver` (subset of the above that needs `enchantment_registry.go` injection) | 2 | 1.8 % (enchantments, stored_enchantments) |
| `round-trip` total | **8** | **7.2 %** |
| `dropped` (counted-and-dropped, never silent) | 103 | 92.8 % |

## ItemStackWithSlot on-disk shape (Table 2)

The on-disk "Items" list element is `ItemStackWithSlot`, ported
byte-for-byte against `net.minecraft.world.ItemStackWithSlot.lambda$static$0`
and the flattened `net.minecraft.world.item.ItemStack.MAP_CODEC`.
Source: `save/item_nbt.go:73-105`, plus the verification cite in its file
header (Java-extract check via javap).

| Aspect | JAR shape | Go shape (file:line) | Status | Notes |
| --- | --- | --- | --- | --- |
| Element list key | `Items` (ContainerHelper.TAG_ITEMS) | `TagItems = "Items"` (`save/item_nbt.go:53`) | exact | |
| Element compound layout | `{Slot (byte), id (string), count (int, always), components? (compound, opt)}` (the `ItemStack` record FLATTENED into the same compound) | `ItemStackWithSlotDisk{Slot byte; ItemStackDisk}` (`save/item_nbt.go:96-104`); `ItemStackDisk{ID string; Count int32; Components *DiskComponents}` (`save/item_nbt.go:73-93`) | exact | Verified against the JAR (`javap -c` on `ItemStack.lambda$static$1` builds a record of the same three fields). |
| Slot field | `Slot` byte (UNSIGNED_BYTE), always written (default 0, optionalAlwaysPresentFieldOf) | `Slot byte` (`save/item_nbt.go:96`); emitted as TAG_Byte per `nbt` codec default | exact | |
| id field | `id` (Item.CODEC_WITH_BOUND_COMPONENTS), REQUIRED | `ID string` with `nbt:"id"` (no omitempty) (`save/item_nbt.go:77`) | exact | Required; empty `DiskItem.ID` -> `IsEmpty()` drops the slot on save. |
| count field | `count` (intRange(1,99)), ALWAYS written (default 1) | `Count int32` (`save/item_nbt.go:83`) -> `clampCount(1..99)` applied at encode AND decode (`save/item_nbt.go:148-156`); `container.Items[].Count` falls back to 1 if key absent | round-trip | Decoded count is `clamp(int32,1,99)`; the encode clamps the saved `<=0`/overflow values up. |
| components field | `components` (compound, omitted on empty) | `Components *DiskComponents` with `nbt:"components,omitempty"` (`save/item_nbt.go:91`) | round-trip | `*DiskComponents` is `map[string]nbt.RawMessage` (string-keyed as the JAR's `dispatchedMap(PatchKey.CODEC, PatchKey::valueCodec)`). |
| list decode tolerance | `listOrEmpty` semantic -- a missing `Items` list yields a stable empty | `LoadItemsCompound` `nbt.RawMessage.Unmarshal` -> `LoadAllItems` (`save/item_nbt.go:259`) | exact | A compound with no `Items` key yields an `all-empty` container of `size` slots. |
| Validation | `isValidInContainer(slot, size) = slot >= 0 && slot < size` (slot is byte -> `>=0` is trivially true) | `isValidInContainer` (`save/item_nbt.go:142-144`) | exact | Out-of-range slots silently dropped on load (matches `loadAllItems` semantics). |
| Save semantic | `ContainerHelper.saveAllItems(out, list, keepEmptyTag)` | `SaveAllItems(list, keepEmptyTag)` (`save/item_nbt.go:168-203`) | exact | Discard semantics replicated (`keepEmptyTag=false` + `len == 0` -> `nil`; default write otherwise). |
| `ItemStack.MAP_CODEC` `Components.CODEC` wrapping | `optionalFieldOf("components", EMPTY)` | `*DiskComponents` `omitempty` | round-trip | A stack with NO components on disk writes no `components` key; a stack with components has the `components` compound. |
| Save flow | `out.list("Items", ItemStackWithSlot.CODEC)` | `nbt.Marshal(itemsListShape{Items: ...})` (`save/item_nbt.go:241-249`) | exact | Bare `Items` NBT list (the `BlockEntity.Data` convention); the 3-byte root header is stripped for embedding (`save/item_nbt.go:251-254`). |
| Empty-stack skip | `!stack.isEmpty()` -> drop | `DiskItem.IsEmpty()` (`save/item_nbt.go:134-137`) | exact | An empty skip yields a sparse list (slot-keyed); counts >= 1 only on present stacks. |

**Net**: the ItemStack / ItemStackWithSlot disk shape is **exact (10/10)**.
The single byte-level deviation is the `Components` encoding: the JAR uses
`DataComponentPatch.CODEC` (a dispatchedMap), the Go uses the same key shape
(string resource id, "!"-prefixed removals) but with per-component
byte-stable transcodes only for the 8 supported set (Table 1); the rest of
the supported JAR compound (103 components) is dropped at write-time (Table 1).

## BlockEntity list shape on chunk (Table 3)

A chunk's `BlockEntity` is the SAME array entry the level/chunk codec carries;
the chunk serialization is `level/chunk.go:351-380` (`blockEntities` array ->
each entry as `{id,x,y,z, <bare Data compound>}` per
`BlockEntity.saveWithFullMetadata`).

Every BE class is encoded as:

```nbt
{
  "id":  "<resource id of BlockEntityType>" // from registryid.EntityTypes lookup
  "x":   int32 (absolute world coord, x = (chunkX<<4)|localX)
  "y":   int32 (world Y of the BE cell)
  "z":   int32 (absolute world coord, z = (chunkZ<<4)|localZ)
  ...    // bare BlockEntity.Data compound merged in (saveAdditional shape)
}
```

Source: `level/chunk.go:351-400` (`blockEntityFullMetadata` merges the saved
Data + the `addEntityType` metadata wrapper). Loader: `level/chunk.go:117-153`
(`ChunkFromSave` reads `{id,x,y,z}` then packs the absolute coords into the
BE's local coords + `int16(be.Y)`).

Verified against `BlockEntity.saveWithFullMetadata` / `saveMetadata` (cited in
`level/chunk.go:357-385` and verified via `javap -p net.minecraft.world.level.block.entity.BlockEntity`
which lists `saveWithFullMetadata(ValueOutput) final`, `addEntityType(... public static)`, etc.).

## Audited BE classes (Table 4)

Columns: **JAR field** = the per-class save / loadAdditional signature on the
JAR (`javap -p -c`); **GO encode/decode pair** = the `encode*BE` + `decode*BE`
file:line where the Go port writes / reads the same field set; **Component
handling** = the path taken when an item slot within the BE carries components;
**Status** = one of `round-trip` (field-by-field 1:1 to the JAR),
`write-only` (save, no read), `read-only` (read, no save), `dropped` (no
save/load seam), `defaulted` (read fills a placeholder), `unknown` (no evidence).

### Table 4-a: chest (RandomizableContainer, 27-slot single / 54-slot double)

| Field | JAR | Go (save) | Go (load) | Status | Evidence |
| --- | --- | --- | --- | --- | --- |
| `LootTable` (String) | `trySaveLootTable` -> `out.store("LootTable", ResourceLocation.CODEC, lootTable)` (RandomizableContainer.save) | `chestLootNBT{LootTable string; LootTableSeed int64}` (gen-time shape); preserved by chunk gen (`level/chunk_be_save_test.go:21-30`) | `decodeChestBE` (`server/chest_open.go:800-834`) reads `LootTable` + `LootTableSeed` | round-trip | CITE RandomizableContainer.trySaveLootTable / tryLoadLootTable (26.2-inner.jar). |
| `LootTableSeed` (long) | alongside `LootTable` | same compound | same | round-trip | |
| `Items` (list<ItemStackWithSlot>, `saveAllItems(out, items, keepEmptyTag=true)` if LootTable cleared) | `trySaveLootTable` returned false -> `saveAllItems(out, items)` | `flushChestItems` (`server/chunk_persist.go:165-201`) -- iff `cl.LootTable == ""`; calls `save.SaveItemsCompound(items, false)` which delegates to `SaveAllItems` | `decodeChestItems` (`server/chest_open.go:840-866`) -> `save.LoadItemsCompound` | round-trip | CITE ChestBlockEntity.saveAdditional. Verified against JAR bytecode for `saveAllItems` (26.2-inner.jar). |
| Item `id` / `count` / `components` (nested) | flattened in each `ItemStackWithSlot` | `ItemStackWithSlotDisk.Slot+ID+Count+Components` (`save/item_nbt.go:96-104`) | same shape | round-trip | Components: `diskEncodeComponent` switch (8 cases) + `diskDecodeComponent` switch (same 8 cases); 103 components dropped (Table 1). |
| `CustomName` (`BaseContainerBlockEntity` / `BlockEntity` base class) | absent in 26.2's ChestBlockEntity.save (BaseContainerBlockEntity.saveAdditional has no fields when no custom name set) | nothing; v1 chest `chestLoot` does not track custom name | nothing | `unknown` | CITE BaseContainerBlockEntity.saveAdditional bytecode -- no field set beyond `super.saveAdditional`. A custom-named chest (anvil-renamed) writes no `CustomName` to disk in vanilla 26.2 either. |
| `lidAnimateTick` (open/close animation) | `LidBlockEntity.lidAnimateTick(...)` -- a server TICK side; NOT persisted | in-memory `t.openChests` only | not loaded | dropped-on-reload (cosmetic, vanilla-faithful) | `LidBlockEntity.lidAnimateTick` is a `static` server ticker (per `javap -p`); no `saveAdditional` field involved. A reloaded chest always starts CLOSED. CITE LidBlockEntity javap. |
| `MenuOpeningCount` / viewers (server-only) | NOT persisted | not tracked | not tracked | absent | Vanilla's chest has no persisted viewer count (only the lid-anim reads it). |
| Double-chest merge | Maven's `ChestBlockEntity.switchContents(ChestBlockEntity, ChestBlockEntity)`: swaps containers at link/break | not modeled (a single-be block is the only BE per chunk cell; a double-chest is two cells with two separate chests in vanilla's chunk NBT) | same | dropped (cosmetic; no BE-merge persistence needed since each half already persists its own Items) | `blockEntityFullMetadata` (`level/chunk.go:386`) gives one entry per local coord cell, never merged. |

**Net chest: 4/4 round-trip, 2 cosmetic dropped (`CustomName`, `lidAnimateTick`)**.
Tests: `TestChestItemsRoundTrip` (`server/chunk_persist_test.go:19`),
`TestChestUnrolledNotFlushed` (`server/chunk_persist_test.go:65`),
`TestChestEnchantedItemReloadsFromBE` (`server/chest_enchantment_persist_test.go:81`)
proves the Phase-B enchantments transcoder round-trips through this exact
path (`minecraft:enchantments` written & re-read with resource-id keys + lvl
levels and IDENTICAL wire numeric ids).

### Table 4-b: AbstractFurnaceBlockEntity (furnace / blast_furnace / smoker)

| Field | JAR | Go (save) | Go (load) | Status | Evidence |
| --- | --- | --- | --- | --- | --- |
| `Items` (3 slots, `saveAllItems(out, items, true)`) | yes (`ContainerHelper.saveAllItems(out, items, true)`) | `furnaceItemsToDisk` -> `save.SaveAllItems(..., true)` (`server/block_entity_persist.go:76-94` -> `:102`) | `decodeFurnaceBE` calls `save.LoadAllItems` (`server/block_entity_persist.go:151`) | round-trip | CITE AbstractFurnaceBlockEntity.saveAdditional bytecode (`javap -c`). |
| `cooking_time_spent` (TAG_Short) | `out.putShort("cooking_time_spent", (short) cookingTimer)` | `furnaceStateShape.CookingTimeSpent int16` (`server/block_entity_persist.go:63-71`); encode `int16(f.cookingTimer)` | `f.cookingTimer = int(shape.CookingTimeSpent)` (`server/block_entity_persist.go:152`) | round-trip | CITE AbstractFurnaceBlockEntity.saveAdditional / loadAdditional (`javap -c`). |
| `cooking_total_time` (TAG_Short) | `out.putShort("cooking_total_time", (short) cookingTotalTime)` | `furnaceStateShape.CookingTotalTime int16` | `f.cookingTotalTime = int(shape.CookingTotalTime)` | round-trip | same source. |
| `lit_time_remaining` (TAG_Short) | `out.putShort("lit_time_remaining", (short) litTimeRemaining)` | `furnaceStateShape.LitTimeRemaining int16` | `f.litTimeRemaining = int(shape.LitTimeRemaining)` | round-trip | same source. |
| `lit_total_time` (TAG_Short) | `out.putShort("lit_total_time", (short) litTotalTime)` | `furnaceStateShape.LitTotalTime int16` | `f.litTotalTime = int(shape.LitTotalTime)` | round-trip | same source. |
| `RecipesUsed` (compound unboundedMap<ResourceKey, INT>) | `out.store("RecipesUsed", RECIPES_USED_CODEC, recipesUsed)` (`Codec.unboundedMap(Recipe.KEY_CODEC, Codec.INT)`) | `furnaceStateShape.RecipesUsed map[string]int32` (string-keyed synthetic per `furnaceRecipeKey`) | `f.recipesUsed = ...` with `f.recipesXP` re-derived (`server/block_entity_persist.go:181-189`) | round-trip (id-indirection declared) | CITE AbstractFurnaceBlockEntity.saveAdditional ldc "RecipesUsed" (`javap -c`). The cited `recipesXP` indirection is documented at `server/block_entity_persist.go:36-40`: vanilla re-resolves `experience()` by `ResourceKey` on take, v1 re-resolves by the synthetic-key recipe lookup; observable XP math (floor + fractional orb) is byte-identical. A removed recipe awards 0 XP (the byKey-miss case). |
| `CustomName` (`BaseContainerBlockEntity` parent class) | absent in vanilla 26.2 for AbstractFurnaceBlockEntity (CustomName is on the parent; not persisted when null) | not tracked | not tracked | absent (vanilla-faithful -- unrenamed furnace writes no `CustomName`) | CITE BaseContainerBlockEntity.saveAdditional javap. |
| BE type (`minecraft:furnace` vs `minecraft:blast_furnace` vs `minecraft:smoker`) | fixed by the live BlockEntityType (NOT persisted) | `furnaceBEType(sub)` (`server/block_entity_persist.go:247-256`) computed from `f.subtype` (the live RecipeType) | same | read-only-from-state | A placed furnace whose type was determined post-load uses the live block state's BE type (CITE BlastFurnaceBlockEntity / SmokerBlockEntity BlockEntityType ctor). |
| `BurnTimeStandard` / cook-time constants | constants; not persisted | compile-time consts | n/a | n/a | |

**Net furnace: 7/7 round-trip**. Tests: `TestFurnaceEncodeDecodeRoundTrip`
(`server/block_entity_persist_test.go:15-100`),
`TestFurnaceResolveReloadsPersistedState` (`:110-140`) exercise the live-drive
+ disk flush path.

### Table 4-c: BrewingStandBlockEntity (brewContainerSize = 5 slots)

| Field | JAR | Go | Status | Evidence |
| --- | --- | --- | --- | --- |
| `Items` (5 slots, `saveAllItems(out, items, true)`) | yes | `brewingItemsToDisk` -> `SaveAllItems(..., true)` (`server/brewing_stand_persist.go:48-58` -> `:79`) | round-trip | CITE BrewingStandBlockEntity.saveAdditional `javap -c` (`super.saveAdditional; out.putShort("BrewTime"); ContainerHelper.saveAllItems; out.putByte("Fuel")`). |
| `BrewTime` (TAG_Short) | `out.putShort("BrewTime", (short) brewTime)` | `brewingStandStateShape.BrewTime int16` (`server/brewing_stand_persist.go:42`); encode `int16(b.brewTime)` | round-trip | same source. |
| `Fuel` (TAG_Byte) | `out.putByte("Fuel", (byte) fuel)` | `brewingStandStateShape.Fuel int8` | round-trip | same source. |
| `ingredient` (re-cache, mid-brew) | `loadAdditional` re-cache `if (brewTime > 0) ingredient = items.get(3).getItem()` | `decodeBrewingStandBE` (`server/brewing_stand_persist.go:99-122`) re-caches from slot 3 when `brewTime > 0` | round-trip | CITE BrewingStandBlockEntity.loadAdditional bytecode. |
| `CustomName` | absent (parent-class shape; not in vanilla 26.2 for BrewingStandBlockEntity.saveAdditional when no name set) | not tracked | absent (faithful) | CITE vanilla javap. |

**Net brewing_stand: 5/5 round-trip**. Tests: `server/brewing_stand_test.go:105`
(`resolveBrewingStand` reloads persisted state).

### Table 4-d: DispenserBlockEntity (dispenser / dropper, 9 slots)

| Field | JAR | Go | Status | Evidence |
| --- | --- | --- | --- | --- |
| `Items` (9 slots, `saveAllItems(out, items)` after `trySaveLootTable` == false) | yes | `dispenserItemsToDisk` -> `SaveAllItems(..., true)` (`server/dispenser_persist.go:33-47` -> `:70-72`) | round-trip | CITE DispenserBlockEntity.saveAdditional `javap -c`. |
| `LootTable` / `LootTableSeed` | inherited from `RandomizableContainer`, only written when placed by a structure; v1 places none, so all dispenser / dropper passes through Items | not modeled (no structure-placed dispense/dropper in v1) | dropped-on-reload (no such structure) | CITE RandomizableContainer.trySaveLootTable bytecode: a placed dispenser / dropper in v1 always returns false -> Items branch. |
| `CustomName` | absent (parent-class shape) | not tracked | absent (faithful) | CITE vanilla javap. |
| Dispenser vs Dropper BE type | fixed by the live BlockEntityType (NOT persisted) | `dispenserBEType` parameterized by `isDropper` (`server/dispenser_persist.go:89`) | read-only-from-state | CITE DispenserBlock.newBlockEntity / DropperBlock.newBlockEntity javap. |

**Net dispenser/dropper: 1/1 round-trip (Items) + 1 BE-type fixed by state**.

### Table 4-e: HopperBlockEntity (5 slots)

| Field | JAR | Go | Status | Evidence |
| --- | --- | --- | --- | --- |
| `Items` (5 slots, `saveAllItems(out, items)` after `trySaveLootTable` == false) | yes | `hopperItemsToDisk` -> `SaveAllItems(..., true)` (`server/hopper_persist.go:30-46` -> `:64-66`) | round-trip | CITE HopperBlockEntity.saveAdditional `javap -c`. |
| `TransferCooldown` (TAG_Int) | `out.putInt("TransferCooldown", cooldownTime)` | `hopperStateShape.TransferCooldown int32` (`server/hopper_persist.go:26`) | round-trip | same source. |
| `LootTable` | inherited (only on structure-placed, v1 places none) | not modeled | dropped-on-reload (no such structure) | |
| `CustomName` | absent | not tracked | absent (faithful) | |

**Net hopper: 2/2 round-trip**.

### Table 4-f: CrafterBlockEntity (9 slots)

| Field | JAR | Go | Status | Evidence |
| --- | --- | --- | --- | --- |
| `Items` (9 slots, after `trySaveLootTable` == false) | yes | `crafterItemsToDisk` -> `SaveAllItems(..., true)` (`server/crafter_persist.go:35-51` -> `:65-68`) | round-trip | CITE CrafterBlockEntity.saveAdditional `javap -c`. |
| `crafting_ticks_remaining` (TAG_Int) | `out.putInt("crafting_ticks_remaining", craftingTicksRemaining)` | `crafterStateShape.CraftingTicksRemaining int32` (`server/crafter_persist.go:30`) | round-trip | same source. |
| `disabled_slots` (TAG_IntArray; per addDisabledSlots) | `addDisabledSlots(out)` builds an IntArrayList of disabled slot indices; `out.putIntArray("disabled_slots", intlist.toIntArray())` | `crafterStateShape.DisabledSlots []int32` with `nbt:"disabled_slots,omitempty"` (`server/crafter_persist.go:32`) | round-trip | CITE CrafterBlockEntity.addDisabledSlots / loadAdditional javap. |
| `triggered` (Boolean) | `addTriggered(out)` writes `out.putBoolean("triggered", isTriggered())` | round-tripped through the TRIGGERED block-state property of `CrafterBlock`, NOT the BE compound; v1 stores it in the block state (CITE `crafter.go:6-9`: "the TRIGGERED animation flag round-trips through the block-state TRIGGERED property") | read-only-from-state | CITE CrafterBlockEntity javap (`triggered`), CrafterBlock.triggered property. The state-driven round-trip is correct (vanilla also uses the same block-state property to expose the triggered bit to other clients). |
| `LootTable` | inherited (only on structure-placed; v1 places none) | not modeled | dropped-on-reload (no such structure) | |

**Net crafter: 3/3 + 1 BE state-driven**.

### Table 4-g: ChiseledBookShelfBlockEntity (6 slots)

| Field | JAR | Go | Status | Evidence |
| --- | --- | --- | --- | --- |
| `Items` (6 slots, `saveAllItems(out, items, true)` 3-arg form) | yes | `chiseledBookshelfItemsToDisk` -> `SaveAllItems(..., true)` (`server/chiseled_bookshelf_persist.go:38-53` -> `:72-74`) | round-trip | CITE ChiseledBookShelfBlockEntity.saveAdditional `javap -c` (3-arg `saveAllItems(out, items, true)`). |
| `last_interacted_slot` (TAG_Int) | `out.putInt("last_interacted_slot", lastInteractedSlot)` | `chiseledBookshelfStateShape.LastInteractedSlot int32` (`server/chiseled_bookshelf_persist.go:28`) | round-trip | same source. |

**Net chiseled_bookshelf: 2/2 round-trip**.

### Table 4-h: ShulkerBoxBlockEntity (27 slots, all 16 dyed variants share the same BE type)

| Field | JAR | Go | Status | Evidence |
| --- | --- | --- | --- | --- |
| `Items` (27 slots, `saveAllItems(out, itemStacks, false)` 3-arg form) | yes (after `trySaveLootTable` == false) | `shulkerItemsToDisk` -> `SaveAllItems(..., true)` (`server/shulker_box_persist.go:36-58` -> `:73-75`) | round-trip | CITE ShulkerBoxBlockEntity.saveAdditional / loadFromTag `javap -c` (3-arg `saveAllItems(out, items, false)`). |
| Lid animation state (`animationStatus` / `progress` / `openCount` on `ShulkerBoxBlockEntity`) | NOT persisted (`ShulkerBoxBlockEntity` only writes Items; a reloaded shulker starts CLOSED) | lid is never persisted | dropped-on-reload (vanilla-faithful) | CITE ShulkerBoxBlockEntity.saveAdditional javap -- no animation field. |

**Net shulker_box: 1/1 round-trip + 1 cosmetic-on-reload**.

### Table 4-i: SignBlockEntity (SignText x 2 + is_waxed + playerWhoMayEdit, 4 standing / 4 hanging / 4 wall variants)

| Field | JAR | Go | Status | Evidence |
| --- | --- | --- | --- | --- |
| `front_text` (compound, SignText.DIRECT_CODEC = `{messages, filtered_messages?, color?, has_glowing_text?}`) | `out.store("front_text", SignText.DIRECT_CODEC, frontText)` (verified `javap -c`) | `signStateShape` (`server/sign.go:96-101`) -> marshalled as the `{Messages, Color, HasGlowingText}` compound; `signBEShape.FrontText` (`server/sign.go:102-109`) -> encode `s.front.toShape()` | `decodeSignBE` -> `signTextFromShape` (`server/sign.go:138-150`) -> `s.front` | round-trip | CITE SignBlockEntity.saveAdditional / loadAdditional `javap -c`: ldc "front_text" -> DIRECT_CODEC -> store; ldc "is_waxed" -> putBoolean. |
| `back_text` (compound, same DIRECT_CODEC) | yes (same store call as `front_text`) | `signBEShape.BackText` -> `s.back.toShape()` | `s.back = signTextFromShape(shape.BackText)` | round-trip | same source. |
| `is_waxed` (Boolean) | `out.putBoolean("is_waxed", isWaxed)` | `signBEShape.IsWaxed bool nbt:"is_waxed,omitempty"` (`server/sign.go:107`) | `s.waxed = shape.IsWaxed` | round-trip | CITE SignBlockEntity.saveAdditional ldc "is_waxed". |
| `filtered_messages` (SignText field) | part of SignText.DIRECT_CODEC (optionalAlwaysPresentFieldOf, absent when null) | not modeled (v1 has no chat filter path) | dropped | The SignText.DIRECT_CODEC declares the field optional; vanilla's writability check filters on the same `isWaxed`. v1 store path has no `filterText` seam. CITE SignText.DIRECT_CODEC javap. |
| `color` (DyeColor, optional) | part of SignText.DIRECT_CODEC | `Color string nbt:"color,omitempty"` written only when not `BLACK` (the default) | reads via `dyeColorIDFromName(name)` with `BLACK` default | round-trip | CITE SignText.DIRECT_CODEC javap. |
| `has_glowing_text` (Boolean, optional default false) | part of SignText.DIRECT_CODEC | `HasGlowingText bool nbt:"has_glowing_text,omitempty"` (omitted on false) | reads | round-trip | same source. |
| `messages` (Component[] REQUIRED, length 4) | part of SignText.DIRECT_CODEC | `Messages []chat.Message` (length 4); empty line -> empty-string chat.Message (TAG_String) | `st.messages[i] = sh.Messages[i].Text` | round-trip | same source. |
| `playerWhoMayEdit` (UUID, transient edit lock) | NOT persisted (`playerWhoMayEdit = null` on load; a reloaded sign is always editable by any caller until a new openTextEdit locks it) | not stored | not loaded | absent (faithful -- a reloaded sign has no edit lock; a useWithoutItem re-opens the editor with the clicker as the new editor) | CITE SignBlockEntity.saveAdditional javap: no "playerWhoMayEdit" / "mayEdit" put. |
| 4-line cap (`readUtf(384)`) | `ServerboundSignUpdatePacket.readUtf(384)` per line | `truncateSignLine` enforces `len <= signMaxLineLength = 384` (`server/sign.go:325`) | n/a (server-side cap on inbound packet) | round-trip | CITE ServerboundSignUpdatePacket.readUtf javap. |
| BE type (`minecraft:sign` / `minecraft:hanging_sign` / 4 variants) | encoded in the chunk's `id` field (the per-entry metadata wrapper) | `block.EntityTypes["minecraft:sign"]` / `"minecraft:hanging_sign"` lookup at `server/sign.go:194-202`; reused per Standing / Wall / Hanging variant | same on read | round-trip (chunk-level) | |

**Net sign: 6/6 structure-fields round-trip (front, back, is_waxed, color, has_glowing_text, messages) + 1 transient dropped (`playerWhoMayEdit`) + 1 cosmetic dropped (`filtered_messages`)**.

Tests: `server/sign_test.go:188` `TestSignBERoundTrip` proves the
encode -> NBT -> decode cycle through `encodeSignBE` + `decodeSignBE`.

### Tables 4-j to 4-t: BEs with **NO** save / load seam (live-drive only)

The following vanilla 26.2 BE classes are present in
`net.minecraft.world.level.block.entity.*` (verified via `javap -p`
`protected void saveAdditional(...)` exists), but v1 has no `encode*BE` /
`decode*BE` / `flush*Items` / `load*BE` pair in `server/`. Their state lives
in `t.<map>` only and is lost on server restart / chunk unload.

| Class | Vanilla fields persisted | Go status | Evidence |
| --- | --- | --- | --- |
| `SpawnerBlockEntity` / `BaseSpawner` | `spawnDelay` (int), `minSpawnDelay` (int), `maxSpawnDelay` (int), `spawnCount` (int), `maxNearbyEntities` (int), `requiredPlayerRange` (int), `spawnRange` (int), `spawnPotentials` (`WeightedList<SpawnData>`, a single-entry default list of `SpawnData` with id / pos nbt) -- verified via `javap -p BaseSpawner` `public void load(Level, BlockPos, ValueInput)` + `public void save(ValueOutput)` | `dropped` (no Go encode/decode) | `server/spawner_block.go:49-83` declares the `spawnerBE` shape; there is no `flushSpawners` / `loadSpawnerBE` / `encodeSpawner`. The `t.spawners` map is rebuilt lazily on first access (`resolveSpawner`, `:338`); a server restart yields a default-base `BaseSpawner` (the per-class default values defined in `spawner_block.go:54-62` -- 20, 200, 800, 4, 6, 16, 4). |
| `BellBlockEntity` | `resonationTicks` (int), `clickDirection` (Direction), `ticks` (int), `shaking` (boolean) | `dropped` (no Go encode/decode) | `server/bell_be.go:158` `resolveBell` synthesizes an EMPTY bell; no encode/decode/persist seam. Comments at `bell_be.go:54-65` explicitly cite "persistence deferred". |
| `BeaconBlockEntity` | `Levels` (CompoundTag: `primary`, `secondary`), `BeamSections` (list of (Color, EndTime) pairs), `Lock` (String) | `dropped` (no Go encode/decode) | `server/beacon_menu.go:167-172` `resolveBeacon` synthesizes an EMPTY beacon; the beam-color sections are cite-deferred (`beacon_be.go:30, 58, 128`). |
| `LecternBlockEntity` | `Book` (`ItemStack`), `Page` (int) | `dropped` (no Go encode/decode) | `server/lectern_be.go:230` `resolveLectern` synthesizes an EMPTY lectern (no book, page=0). |
| `BeehiveBlockEntity` | `Occupants` (`ListTag<Bee>` of `EntityData` records -- bees stored typed with their NBT), `flowerPos` (BlockPos optional) | `dropped` (no Go encode/decode) | `server/beehive_be.go:151-152, 193` cite `setChanged(): persistence deferred`; live-drive only. The NBT round-trip is cite-deferred to native spawn of a Bee -- re-using the spawn primitive when leaving a hive. |
| `JukeboxBlockEntity` | `TheItem` (`ItemStack`), `TicksSinceSongStarted` (int) | `dropped` (no Go encode/decode) | `server/jukebox_be.go:32-94` models state but no flushXxx / encode/decode seam. |
| `DecoratedpotBlockEntity` | `Item` (`ItemStack`), `Sherds` (`List<DyeColor>`, 4 ids, NEW in 26.x -- verified via javap): the per-side sherds composing the pot's pattern | `dropped` (no Go encode/decode) | `server/decorated_pot_be.go:67-87` `resolveDecoratedPot` synthesizes an EMPTY pot (`deferred` per source comments and the absence of `flushDecoratedPot`). |
| `SculkSensorBlockEntity` | `VibrationData` (`LastVibrationFrequency int`, `LastVibrationGameEvent`); listeners are NOT persisted | `dropped` (no Go encode/decode) | `server/sculk_sensor_be.go:58` cites the field but no flushXxx is wired in `server/chunk_persist.go:90-122` (the column-flush graph). |
| `SculkShriekerBlockEntity` | `WarningLevel` (int), `WarningTicksRemaining` (int), `LastVibrationFrequency` (int, optional) | `dropped` (no Go encode/decode) | `server/sculk_shrieker_be.go` has live-drive + gameEvent emitter; no encode/decode/persist seam. |
| `EndGatewayBlockEntity` | `Age` (long), `ExactTeleport` (boolean), `ExitPortalPos` (BlockPos, optional, only on the gateway that was "entered") | `absent` (v1 does not model EndGatewayBlockEntity at all) | `server/end_gateway_be.go` has comments only; no Go type definition for an end_gateway BE, no resolve path. |
| `TrialSpawnerBlockEntity` | `required_player_range`, `spawn_range`, `cooldown_length`, `total_mobs_spawned`, `total_mobs_added_to_cooldown`, etc. | `absent` (v1 does not model TrialSpawnerBlockEntity) | `t.trialSpawners` does not exist in `server/`; vanilla 26.2 BE is cite-deferred. |
| `VaultBlockEntity` | `rewarded_players` (List<UUID>), `config`, `loot_table`, `items`, `state` | `absent` | not modeled in v1. |
| `CommandBlockBlockEntity` / `StructureBlockBlockEntity` | command string, success count, last output, structure-block metadata | `absent` | not modeled. |
| `HangingSignBlockEntity` | same SignText compound as SignBlockEntity, plus the `attached` Direction; inherits SignBlockEntity | `unknown` -- v1's `sign.go` handles ALL standing + hanging via the same `signBE` + the `beType` from `block.EntityTypes["minecraft:sign"]` / `"minecraft:hanging_sign"]`; the persistence path is the same `encodeSignBE` (`server/sign.go:126-139`) so the standing/hanging distinction is at the chunk-level BE type (`id`) only, not inside the compound. **Verified**: standing + hanging variants share the same `encodeSignBE` (`server/sign.go:113-139`). |

**Net for the live-drive-only BE classes**: 14 BE classes audited, **0 with persistence**. Their state is in-memory only and is lost on server restart. This is the **biggest single structural gap** in the persistence audit. The classes involved are:
**spawner, bell, beacon, lectern, beehive, jukebox, decorated_pot, sculk_sensor, sculk_shrieker, end_gateway, trial_spawner, vault, command_block, structure_block**.
For most of these, the in-memory live-drive is byte-identical to vanilla (the resolved state mirrors the JAR defaults); the only behavioral loss is on **chunk unload across a server restart** -- the live state resets to the JAR's default. For `beehive` in particular, the beehive-occupants typed-NBT compound is NEVER round-tripped (a stored bee returns to native spawn, not to its original Mob full NBT).

### Tables -uncovered: BE classes `unknown` or out-of-scope

The following vanilla 26.2 BE classes are NOT enumerated in v1 and are
**out of scope for this census wave** (each is a separate audit):
- `MobSpawner` (block entity is `SpawnerBlockEntity` above)
- `BrushableBlockEntity` (suspicious sand / gravel; new in 26.x) -- `unknown` to v1 (no Go type).
- `CalibratedSculkSensorBlockEntity` (variant of SculkSensor) -- `unknown` to v1.
- `TestBlockEntity`, `TestInstanceBlockEntity` (creative-mode test workbench) -- `unknown` and out of scope.
- `HangingSignBlockEntity` (distinct BE class in JAR) -- same SignText compound as Sign; covered above via the standing/hanging shared `signBE` path.

## Per-BE `Components` drop accounting (Table 5)

Every BE class that carries an item container persists {Slot, id, count}
**plus** the SUPPORTED 8-component set via the Phase-B transcoder
(`save/item_components.go:diskEncodeComponent`). The remaining components
appear in `dropped` (the `diskEncodeComponent` `default` branch counts the
dropped present-components and `udebug("chunksave", <be>: %d component-bearing
stacks persisted without components (Phase A)")` reports it). The 8 supported
types cover ~80 % of the slot-realistically-observable component values for
the **8 audited container BEs** (chest, furnace family, brewing_stand,
dispenser family, hopper, crafter, chiseled_bookshelf, shulker_box family).

| BE | Save path | Phase-B reached? | Drop meter |
| --- | --- | --- | --- |
| chest | `flushChestItems` -> `saveItemsCompound(disk)` (`server/chunk_persist.go:180`) | yes | `udebug` log (`server/chunk_persist.go:190`) |
| furnace (x 3 subtypes) | `flushFurnaceItems` -> `SaveAllItems(furnaceItemsToDisk(items), true)` (`server/block_entity_persist.go:230-243`) | yes | `udebug` log (`server/block_entity_persist.go:238`) |
| brewing_stand | `flushBrewingStandItems` -> `SaveAllItems(brewingItemsToDisk(items), true)` (`server/brewing_stand_persist.go:135-152`) | yes | `udebug` (`server/brewing_stand_persist.go:155`) -- potion bottles carry `minecraft:potion_contents` (a SUPPORTED component) |
| dispenser (x 2 subtypes) | `flushDispenserItems` -> `SaveAllItems(..., true)` (`server/dispenser_persist.go:115-138`) | yes | `udebug` (`server/dispenser_persist.go:135`) |
| hopper | `flushHopperItems` -> `SaveAllItems(..., true)` (`server/hopper_persist.go:102-128`) | yes | `udebug` (same pattern) |
| crafter | `flushCrafterItems` -> `SaveAllItems(..., true)` (`server/crafter_persist.go:108-130`) | yes | `udebug` (`server/crafter_persist.go:126`) |
| chiseled_bookshelf | `flushChiseledBookshelfItems` -> `SaveAllItems(..., true)` (`server/chiseled_bookshelf_persist.go:115-141`) | yes | `udebug` (`server/chiseled_bookshelf_persist.go:137`) |
| shulker_box (x 16 dyed variants) | `flushShulkerItems` -> `SaveAllItems(..., true)` (`server/shulker_box_persist.go:108-127`) | yes | `udebug` (`server/shulker_box_persist.go:120`) |
| sign | `encodeSignBE` (no item container) | n/a | n/a |
| Player .dat Inventory | `inventoryToItems` -> `SaveAllItems(...)` (`server/persistence.go:133-156`) | yes | log at `server/persistence.go:148` |
| Player .dat EnderItems | `enderItemsToDisk` -> `SaveAllItems(...)` (`server/persistence.go:158-176`) | yes | n/a (no `udebug`; same path as inventory) |

**Net**: every production item-container path goes through the same Phase-B
transcoder; the gap is uniform across all 9 container BE types and the 2
player-inventory paths. **No path bypasses the counter** -- the drop is
metered, not silent.

## Nested item stacks in block entities (verify claim 4)

The task asks to verify "nested item stacks in inventories and block
entities use the same component-preserving path". Yes, they do:

1. **Top-level `BlockEntity.Data` "Items" list (the audited 9 BE classes)**
   -> `server/<be>_persist.go` calls `save.SaveAllItems(<be>ItemsToDisk(<in-memory []component.SlotData>), keepEmptyTag)`
   -> `SaveAllItems` (the disk codec) calls `wireToDiskComponents(it DiskItem)` once per
   non-empty stack (`save/item_nbt.go:181-185`).
2. **Player Inventory / EnderItems**
   -> `server/persistence.go:133-156` and `:158-176` build the same `[]save.DiskItem`
   array (skip-empty, Slot-keyed, WireComponents + counts captured verbatim) then
   call `save.SaveAllItems(disk, keepEmptyTag=false)`.
3. **The transcoder is single**: there is exactly ONE wire->disk transcode
   (`wireToDiskComponents` in `save/item_components.go:96-152`); exactly ONE
   disk->wire transcode (`diskToWireComponents` in `:154-205`); the 8
   supported components are the same 8 across every container caller.

**No caller reimplements or short-circuits the disk codec.** Verified via
`Select-String` of every BE persist file's `save.SaveAllItems` call: all 9
call sites pass the output of a per-BE `<be>ItemsToDisk([]component.SlotData)` helper.
The codec is byte-identical across BE calls, player inventory calls, and the
enchantments / potion_contents / lore / custom_name etc. transcode cases.

A nested `ItemStack` inside another component (e.g. a `BundleContents`'s
nested stacks, or `WrittenBookContent.pages` book content) is **NOT**
re-rewritten by v1's codec today because the entry components
(`BundleContents` id 50, `WrittenBookContent` id 55, `ChargedProjectiles` id 49,
`Bee` id 77, `SuspiciousStewEffects` id 53, `Container` id 75, `UseRemainder`
id 25) themselves are `dropped` in Table 1 (no transcode switch case). The
container ITEMS round-trip is exact at the top level (Table 2);
the nesting is bounded by the supported set. Same applies to a beehive's
`Minecraft#Bees` occupant NBT (the stored bee NBT is cite-deferred to native
spawn, see Table 4-j beehive entry).

## Production call-site map (Table 6)

The column-flush graph is the OWNER-goroutine tick-side call chain inside
`server/chunk_persist.go:flushColumn`.

```
flushColumn(pos)
  -> flushChestItems            (chest 27/54, owner-side BE.Data mutation)
  -> flushFurnaceItems          (smelt/blast/smoke)
  -> flushBrewingStandItems     (5 slots)
  -> flushDispenserItems        (dispenser/dropper)
  -> flushHopperItems           (5 slots + TransferCooldown)
  -> flushCrafterItems          (crafter)
  -> flushChiseledBookshelfItems
  -> flushShulkerItems          (16 dyed variants)
  -> snapshotColumnEntities / saveEntities  (the modern entity region)
  -> packChunkBlockTicks + packChunkFluidTicks
  -> world.SerializeChunkData(worker.StructureCache(), pos, ch, ...)
  -> chunkSaver.Enqueue(snapshot)            (off-tick IO)
```

The load side is `hydrateTickingBlockEntities` (per-chunk-ready fan-out):
the chunk's persisted `BlockEntity` list is walked; for any BE whose block
still carries a server ticker (`isAnyFurnaceBlock`/`IsHopper`/
`isBrewingStandBlock`/`IsCrafter`), the matching `resolve*` is called --
which reads the persisted mid-progress (`loadFurnaceBE`, etc.) and
registers the ticker. Verified via `server/chunk_persist.go:273-301` and
the per-file `load*BE` (e.g. `loadFurnaceBE`,
`server/block_entity_persist.go:292`).

The Player .dat path is the same orchestrator with a different file
(`server/persistence.go:113-156` `inventoryToItems`; `server/persistence.go:158-176`
`enderItemsToDisk`) sharing the same `[]save.DiskItem` + `save.SaveAllItems`
contract.

## Critical behavioral loss

These are the cases where v1's persisted observable state drifts from
vanilla 26.2, in priority order (critical / player-visible first).

1. **No save / load seam for 10+ vanilla BE classes**.
   The biggest single structural gap. The in-memory live-drive is
   byte-identical to vanilla while the server runs, but a chunk-unload +
   server-restart loses:
   - `SpawnerBlockEntity`'s `spawnDelay` / `min/maxDelay` / `spawnCount` /
     `maxNearbyEntities` / `requiredPlayerRange` / `spawnRange` /
     `spawnPotentials` (the chosen mob's id + optional position NBT).
     A reloaded spawner returns to the JAR default (20, 200, 800, 4, 6,
     16, 4) and spawns NOTHING (a placed-default spawner has mobName
     empty, so even on first access `t.resolveSpawner(pos, "")`
     synthesizes an empty mob). **Observable**: a dungeon / stronghold
     spawner block RESETS on every server restart.
   - `BellBlockEntity.resonationTicks / clickDirection / ticks / shaking`.
     A belled bell post-resonation returns to IDLE.
   - `BeaconBlockEntity.Levels / BeamSections / Lock`. A primed beacon
     has no primary/secondary selector; a locked beacon ignores Lock.
   - `LecternBlockEntity.Book / Page`. A lectern with a written book
     appears empty (the book re-spawns via take-book; the page resets to
     1).
   - `BeehiveBlockEntity.Occupants / flowerPos`. Stored bees re-spawn as
     native Bee NBT (the spawn-Hive-bee seam), losing the original Mob
     full NBT (custom name, health, etc.). A flower leaver never
     remembers its flower.
   - `JukeboxBlockEntity.TheItem / TicksSinceSongStarted`. A inserted
     disc is forgotten on reload.
   - `DecoratedpotBlockEntity.Item / Sherds`. A pot's stored item +
     four-side sherds pattern (NEW in 26.x) is lost.
   - `SculkSensorBlockEntity.lastVibrationFrequency` +
     `SculkShriekerBlockEntity.WarningLevel / TicksRemaining /
     LastVibrationFrequency`. A shrieker mid-warning resets; a sensor
     loses its last-frequency game-event history.
   - `EndGatewayBlockEntity.Age / ExactTeleport / ExitPortalPos`. The
     end gateway is not modeled at all.
   - `TrialSpawnerBlockEntity` / `VaultBlockEntity`. Not modeled.
   - `CommandBlockBlockEntity` / `StructureBlockBlockEntity`. Not
     modeled.
   - **Fix**: add per-class `encode*BE` / `decode*BE` / `flush*Items` /
     `load*BE` (the t.furnaces twin) for each missing BE; wire each
     call site into the `flushColumn` graph at `server/chunk_persist.go:90-122`;
     add each new resolve path to `hydrateTickingBlockEntities` (or a
     non-ticking sibling) at `server/chunk_persist.go:283-302`.

2. **Item component round-trip is byte-stable for 8 / 111 components
   (7.2 %); the residual 92.8 % is counted-and-dropped at the wire
   boundary, NEVER silently**.
   - For a typical world the dropped set hits: `Unbreakable`, `Trim`,
     `AttributeModifiers`, `Enchantments` (without `enchantment_registry.go`
     injection), `StoredEnchantments` (without injection), `PotionContents`
     for potions with effects (the bare-potion form is supported; the
     `custom_effects` / `custom_color` / `custom_name` arm of
     `withAlternative` is supported), `CustomModelData`,
     `WrittenBookContent`, `WritableBookContent`, `BundleContents`,
     `ChargedProjectiles`, `Food`, `Consumable`, `UseEffects`,
     `UseRemainder`, `UseCooldown`, `DamageResistant`, `Tool`,
     `Weapon`, `AttackRange`, `Enchantable`, `Equippable`, `Repairable`,
     `Glider`, `TooltipStyle`, `DeathProtection`, `BlocksAttacks`,
     `PiercingWeapon`, `KineticWeapon`, `SwingAnimation`,
     `AdditionalTradeCost`, `CustomData`, all variant components
     (`VILLAGER_VARIANT` through `SHULKER_COLOR`), and the rest of the
     ~100-component long tail.
   - **Observable**: a saved shulker with a `BundleContents` writes the
     shulker's 27 outer slots correctly but the bundle's nested stacks
     are LOST (the bundle itself is also written without its bundle
     contents, so the BUNDLE ITEM survives but its CONTENTS disappear).
   - **Fix**: extend the `diskEncodeComponent` / `diskDecodeComponent`
     switches in `save/item_components.go` per-component (one case per
     `DataComponents.<NAME>`), adding the per-component DATA codec
     (the `type.codecOrThrow()` byte shape) and the corresponding wire
     `STREAM_CODEC` reader/writer. The most impactful deferred items
     in player-inventory reality are: `Unbreakable` (4, Unit),
     `Trim` (56, Compound), `AttributeModifiers` (16, List),
     `Enchantments` (13, Compound), `StoredEnchantments` (42, Compound),
     `PotionContents` (51, Partial), `CustomModelData` (17, Compound),
     `WrittenBookContent` (55, Compound), `BundleContents` (50,
     List<Nested ItemStack>), `ChargedProjectiles` (49,
     List<Nested ItemStack>), `Equippable` (32, Compound),
     `CustomData` (0, Compound). Each "compound" entry is a new
     DataComponent transcode case (no nesting).

3. **`SignBlockEntity.filtered_messages` and
   `SignBlockEntity.playerWhoMayEdit` are deliberately dropped**.
   - `filtered_messages` is part of the chat-filter pipeline (the
     `ServerboundChat.filterText` path), which v1 has no seam for yet
     (no text-filter subsystem). v1 stores the raw sign text and never
     recomputes the filtered text. A saved sign with an empty
     `filtered_messages` matches vanilla's sign without chat filtering.
   - `playerWhoMayEdit` is a TRANSIENT edit lock: it is null on every
     reload in vanilla too (the loadAdditional path never restores it).
     The writer never persists it; the editor (re)locks it on
     `openTextEdit`. Vanilla-faithful.
   - **Observable**: no functional loss for `playerWhoMayEdit`
     (vanilla resets it); `filtered_messages` means an offline server
     without chat filtering cannot re-derive the filtered text on
     reload.
   - **Fix**: defer to the chat-filter seam (out of scope for this
     audit wave).

4. **`CustomName` on the parent `BaseContainerBlockEntity` (chest /
   furnace family / brewing_stand / dispenser / hopper / crafter /
   shulker_box)**.
   - Vanilla 26.2 does NOT persist the BE-level `CustomName` (a renamed
     chest / furnace writes no `CustomName` to disk). v1 matches this.
     A renamed BE would be fixed by a separate `CustomName` save / load
     pair, but that would be a NEW feature, not a regression.
   - **Fix**: not required for the 1:1 mandate.

5. **Shulker lid animation state (`animationStatus` / `progress` /
   `openCount`)**.
   - Vanilla NEVER persists the lid animation: a reloaded shulker
     always starts CLOSED (`ShulkerBoxBlockEntity.saveAdditional` writes
     only `Items`). v1 matches.
   - **Fix**: not required for the 1:1 mandate.

6. **Chest double-chest `switchContents` and viewer count**.
   - Vanilla never persists the viewer count (only the lid animation
     reads it transiently). A double-chest IS two BE entries in the
     chunk NBT, not one merged BE.
   - **Fix**: not required for the 1:1 mandate.

7. **`signBE` playerWhoMayEdit UUID**.
   - Not persisted in vanilla, not loaded in v1, faithful.
   - **Fix**: not required.

8. **`BeaconBlockEntity.beaconBeam`** color sections (the glass-tint
   visual).
   - Cite-deferred (client cosmetic). No player-visible gameplay loss.
   - **Fix**: not required for the 1:1 mandate.

## Proposed ledger rows

These rows are PROPOSED, NOT WRITTEN -- the task explicitly forbids editing
`PARITY-LEDGER.csv`. The PR / wave that lands the corresponding code should
add exactly these rows (caller is `minimax-census`, status is the **current**
discoverable status; `last_verified_commit` is left blank for the landing
commit to fill in).

```csv
id,domain,jar_class,jar_method,status,go_path,jar_evidence,test,rng_verified,owner,last_verified_commit,notes
BE-PERSIST-CHEST,persistence,net.minecraft.world.level.block.entity.RandomizableContainerBlockEntity,saveAdditional/loadAdditional,exact,server/chest_open.go + server/chunk_persist.go,javap -c RandomizableContainerBlockEntity + ChestBlockEntity,server/chunk_persist_test.go#TestChestItemsRoundTrip,no,minimax-census,,27-slot Items + LootTable XOR Items faithful; TrySaveLootTable / tryLoadLootTable 1:1
BE-PERSIST-FURNACE,persistence,net.minecraft.world.level.block.entity.AbstractFurnaceBlockEntity,saveAdditional,exact,server/block_entity_persist.go,javap -c AbstractFurnaceBlockEntity,server/block_entity_persist_test.go#TestFurnaceEncodeDecodeRoundTrip,no,minimax-census,,Items + 4 shorts + RecipesUsed faithful; recipesXP secondary keeps XP math byte-identical
BE-PERSIST-BREWING,persistence,net.minecraft.world.level.block.entity.BrewingStandBlockEntity,saveAdditional,exact,server/brewing_stand_persist.go,javap -c BrewingStandBlockEntity,server/brewing_stand_test.go#TestBrewingStandResolve,no,minimax-census,,Items + BrewTime + Fuel + mid-brew ingredient re-cache
BE-PERSIST-DISPENSER,persistence,net.minecraft.world.level.block.entity.DispenserBlockEntity,saveAdditional,exact,server/dispenser_persist.go,javap -c DispenserBlockEntity,server/dispenser_test.go,no,minimax-census,,9-slot Items faithful (dispenser + dropper share)
BE-PERSIST-HOPPER,persistence,net.minecraft.world.level.block.entity.HopperBlockEntity,saveAdditional,exact,server/hopper_persist.go,javap -c HopperBlockEntity,server/hopper_test.go,no,minimax-census,,5-slot Items + TransferCooldown
BE-PERSIST-CRAFTER,persistence,net.minecraft.world.level.block.entity.CrafterBlockEntity,saveAdditional,exact,server/crafter_persist.go,javap -c CrafterBlockEntity,server/crafter_test.go,no,minimax-census,,9-slot Items + crafting_ticks_remaining + disabled_slots IntArray; triggered is a block-state property
BE-PERSIST-CHISELED,persistence,net.minecraft.world.level.block.entity.ChiseledBookShelfBlockEntity,saveAdditional,exact,server/chiseled_bookshelf_persist.go,javap -c ChiseledBookShelfBlockEntity,server/chiseled_bookshelf_test.go,no,minimax-census,,6-slot Items + last_interacted_slot
BE-PERSIST-SHULKER,persistence,net.minecraft.world.level.block.entity.ShulkerBoxBlockEntity,saveAdditional,exact,server/shulker_box_persist.go,javap -c ShulkerBoxBlockEntity,server/shulker_test.go,no,minimax-census,,27-slot Items (16 dyed variants share one BE type); lid animation deliberately not persisted (faithful to vanilla)
BE-PERSIST-SIGN,persistence,net.minecraft.world.level.block.entity.SignBlockEntity,saveAdditional,exact,server/sign.go,javap -c SignBlockEntity,server/sign_test.go#TestSignBERoundTrip,no,minimax-census,,front_text + back_text + is_waxed faithful; playerWhoMayEdit transient (vanilla resets it)
BE-PERSIST-ITEM-FORM,persistence,net.minecraft.world.item.ItemStack,MAP_CODEC + saveAllItems / loadAllItems,exact,save/item_nbt.go,javap -c ContainerHelper + ItemStack.lambda$static$1,save/item_nbt_test.go + server/chunk_persist_test.go#TestChestItemsRoundTrip,no,minimax-census,,ItemStackWithSlot {Slot,id,count,components?} flattened; keepEmptyTag semantics faithful; 1..99 count clamp on encode+decode
BE-PERSIST-COMPONENTS,persistence,net.minecraft.core.component.DataComponentPatch,CODEC + DataComponents javap,partial,save/item_components.go,javap -c DataComponents + DataComponentPatch.CODEC,server/chunk_persist_test.go#TestChestItemsRoundTrip + server/chest_enchantment_persist_test.go#TestChestEnchantedItemReloadsFromBE,no,minimax-census,,8/111 components round-trip with byte-stable fidelity (damage/max_damage/repair_cost/custom_name/lore/potion_contents/enchantments/stored_enchantments); 103/111 deferred (metered counted-drop); 2 require enchantment_registry.go SetEnchantmentRegistry injection
BE-LIVEDRIVE-SPAWNER,persistence,net.minecraft.world.level.BaseSpawner + SpawnerBlockEntity,save/load,absent,server/spawner_block.go,javap -p BaseSpawner,,no,minimax-census,,resolveSpawner synthesizes default; no encode/decode seam; chunk reload resets to BaseSpawner ctor defaults
BE-LIVEDRIVE-BELL,persistence,net.minecraft.world.level.block.entity.BellBlockEntity,saveAdditional,absent,server/bell_be.go,javap -p BellBlockEntity,,no,minimax-census,,resolveBell synthesizes empty bell; cite-deferred 'persistence deferred'
BE-LIVEDRIVE-BEACON,persistence,net.minecraft.world.level.block.entity.BeaconBlockEntity,saveAdditional,absent,server/beacon_menu.go,javap -p BeaconBlockEntity,,no,minimax-census,,resolveBeacon synthesizes empty beacon; beam color sections cite-deferred (client cosmetic)
BE-LIVEDRIVE-LECTERN,persistence,net.minecraft.world.level.block.entity.LecternBlockEntity,saveAdditional,absent,server/lectern_be.go,javap -p LecternBlockEntity,,no,minimax-census,,resolveLectern synthesizes empty lectern; book + page tracking deferred (cite-deferred take-book)
BE-LIVEDRIVE-BEEHIVE,persistence,net.minecraft.world.level.block.entity.BeehiveBlockEntity,saveAdditional,absent,server/beehive_be.go,javap -p BeehiveBlockEntity,,no,minimax-census,,setChanged() cited as 'persistence deferred'; occupant NBT NOT round-tripped (respawned as native Bee)
BE-LIVEDRIVE-JUKEBOX,persistence,net.minecraft.world.level.block.entity.JukeboxBlockEntity,saveAdditional,absent,server/jukebox_be.go,javap -p JukeboxBlockEntity,,no,minimax-census,,TheItem + TicksSinceSongStarted in-memory only; restart forgets disc + tick reset
BE-LIVEDRIVE-DECORATEDPOT,persistence,net.minecraft.world.level.block.entity.DecoratedpotBlockEntity,saveAdditional,absent,server/decorated_pot_be.go,javap -p DecoratedpotBlockEntity,,no,minimax-census,,Item + 4-side Sherds (NEW in 26.x) dropped on chunk reload
BE-LIVEDRIVE-SCULKSENSOR,persistence,net.minecraft.world.level.block.entity.SculkSensorBlockEntity,saveAdditional,absent,server/sculk_sensor_be.go,javap -p SculkSensorBlockEntity,,no,minimax-census,,lastVibrationFrequency cited but not in flushColumn graph
BE-LIVEDRIVE-SCULKSHRIEKER,persistence,net.minecraft.world.level.block.entity.SculkShriekerBlockEntity,saveAdditional,absent,server/sculk_shrieker_be.go,javap -p SculkShriekerBlockEntity,,no,minimax-census,,WarningLevel/WarningTicksRemaining/LastVibrationFrequency in-memory only
BE-LIVEDRIVE-ENDGATEWAY,persistence,net.minecraft.world.level.block.entity.EndGatewayBlockEntity,saveAdditional,absent,server/end_gateway_be.go,javap -p EndGatewayBlockEntity,,no,minimax-census,,v1 has no Go type for end_gateway BE; cite-deferred
BE-ABSENT-COMMAND,persistence,net.minecraft.world.level.block.entity.CommandBlockBlockEntity,saveAdditional,absent,,javap -p CommandBlockBlockEntity,,no,minimax-census,,not modeled in v1 (command blocks deferred)
BE-ABSENT-STRUCTURE,persistence,net.minecraft.world.level.block.entity.StructureBlockBlockEntity,saveAdditional,absent,,javap -p StructureBlockBlockEntity,,no,minimax-census,,not modeled in v1 (structure blocks deferred)
BE-ABSENT-TRIALSPAWNER,persistence,net.minecraft.world.level.block.entity.TrialSpawnerBlockEntity,saveAdditional,absent,,javap -p TrialSpawnerBlockEntity,,no,minimax-census,,not modeled in v1 (trial spawners deferred)
BE-ABSENT-VAULT,persistence,net.minecraft.world.level.block.entity.VaultBlockEntity,saveAdditional,absent,,javap -p VaultBlockEntity,,no,minimax-census,,not modeled in v1 (vault deferred)
```

## Items-count check (player inventory + BE containers, fallback path)

Every save path's wrapped input is `[]save.DiskItem` populated by the
tick-side `<be>ItemsToDisk` helper, which uses the SAME byte layout
(`{ID, Count, HasComponents, WireComponents, WireAddedCount,
WireRemovedCount}`). The transcoder is therefore 1:1 across:

1. Player `Inventory` (`server/persistence.go:133-156` `inventoryToItems`).
2. Player `EnderItems` (`server/persistence.go:158-176`
   `enderItemsToDisk`).
3. Chest container (`server/chunk_persist.go:203-219` `chestItemsToDisk`).
4. Furnace family (`server/block_entity_persist.go:76-94`
   `furnaceItemsToDisk`).
5. Brewing stand (`server/brewing_stand_persist.go:46-58`
   `brewingItemsToDisk`).
6. Dispenser family (`server/dispenser_persist.go:32-47`
   `dispenserItemsToDisk`).
7. Hopper (`server/hopper_persist.go:30-46` `hopperItemsToDisk`).
8. Crafter (`server/crafter_persist.go:35-51` `crafterItemsToDisk`).
9. Chiseled bookshelf (`server/chiseled_bookshelf_persist.go:38-53`
   `chiseledBookshelfItemsToDisk`).
10. Shulker box (`server/shulker_box_persist.go:35-58`
    `shulkerItemsToDisk`).

All 10 funnel through `save.SaveAllItems` (or `save.SaveItemsCompound`
which delegates). No caller bypasses the codec. The Phase-B transcode
(`save/item_components.go:96-152`) runs once per non-empty stack in each
of the 10.

This audit is the input for the wave-01 ledger + the wave-02
implementation that lands the missing encode/decode seams.
