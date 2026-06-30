package server

// item_tag.go — the item-tag membership read (MOB-SUB-07), the item analog of
// damageSource.is(tag) (damage_source.go:78). A goal asks "is this item id a member of the named
// item tag set?" without duplicating the id set into the goal/plugin — exactly as PanicGoal asks
// damageSource.is("panic_causes") for the damage-type side.
//
// JAR AUTHORITY: vanilla TemptGoal's pig predicate is `i -> i.is(ItemTags.PIG_FOOD)` and
// `i -> i.is(Items.CARROT_ON_A_STICK)` (Pig.registerGoals @4, javap temp/cache/26.2-inner.jar).
// ItemStack.is(TagKey) is the holder's membership in the tag set, which is exactly the generated
// data/tag.ItemTags membership map. The carrot_on_a_stick literal is a direct id compare
// (data/item/item.go:5340 CarrotOnAStick.ID = 887), not a tag.

import (
	"github.com/imhinotori/sulfur/data/tag"
)

// itemInTag reports whether itemID is a member of the named item tag — the item analog of
// damageSource.is(tag) (damage_source.go:78). A nil/absent tag yields false (the zero-value of the
// inner map read), exactly like the damage-tag read over a tag with no members. Source:
// data/tag/tags.go ItemTags (jar-extracted, e.g. "pig_food" = {1257 carrot, 1258 potato,
// 1317 beetroot}; tags.go:305). The OTHER pig tempt predicate is the carrot_on_a_stick literal
// (ItemTags.PIG_FOOD vs Items.CARROT_ON_A_STICK id 887), wired Go-side at the goal ctor.
func itemInTag(itemID int32, tagName string) bool {
	return tag.ItemTags[tagName][itemID]
}
