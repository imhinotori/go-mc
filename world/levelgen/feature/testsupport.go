package feature

// testsupport.go exposes a narrow hook for DOWNSTREAM-package tests (world/feature_selector_test.go)
// to reserve a sentinel configured-feature type that parses through ParseConfiguredFeature's
// recognized-type gate without colliding with any real ported feature body. Every real 26.2
// feature type now carries a production body, so no real type is free to borrow as a fake test
// leaf. This lives in a non-test file (a downstream package's _test.go cannot run an init in THIS
// package) but is inert in production: it only mutates the recognized set when a caller invokes it,
// which only the world selector test does. The sentinel type is not in the jar roster and never
// appears in an embedded configured_feature JSON, so LoadAll cross-checks are unaffected.
func RegisterTestFeatureType(typ string) {
	recognizedFeatureTypes[typ] = struct{}{}
}
