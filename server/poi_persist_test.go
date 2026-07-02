package server

// poi_persist_test.go — round-trips the POI SectionStorage persistence (poi_persist.go): register a
// handful of HOME + MEETING POIs (across multiple sections + chunk columns, some with acquired tickets),
// save to <tmp>/poi/*.mca, reload, and assert every record (pos, type, free_tickets) and the section
// map survive. Persistence draws ZERO from any pig stream, so the pig oracle is untouched.

import (
	"testing"

	pk "github.com/imhinotori/sulfur/net/packet"
)

func TestPoiSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()

	m := newPoiManager()

	// A HOME (bed) at a positive column; a MEETING (bell) in the same section; a HOME in a DIFFERENT
	// section (different section-Y); and a MEETING far away in another region entirely — exercising the
	// per-section keying, the Integer.toString(sectionY) Sections map, and multi-region region files.
	homeA := pk.Position{X: 10, Y: 65, Z: 20}
	bellA := pk.Position{X: 12, Y: 66, Z: 21}
	homeB := pk.Position{X: 11, Y: 40, Z: 22}    // different section-Y (65>>4=4 vs 40>>4=2)
	bellFar := pk.Position{X: 1000, Y: 70, Z: -800} // a separate region file

	m.add(homeA, poiTypeHome)
	m.add(bellA, poiTypeMeeting)
	m.add(homeB, poiTypeHome)
	rec := m.add(bellFar, poiTypeMeeting)
	// Acquire two tickets on the far meeting so free_tickets != maxTickets (proves the count persists).
	if rec == nil {
		t.Fatal("add(bellFar) returned nil")
	}
	rec.acquireTicket()
	rec.acquireTicket()
	wantFar := rec.freeTickets // 32 - 2 = 30

	if !m.isDirty() {
		t.Fatal("poiManager must be dirty after add()")
	}

	// Save the immutable snapshot, then reload.
	if err := savePoi(dir, snapshotPoi(m)); err != nil {
		t.Fatalf("savePoi: %v", err)
	}
	got, ok, err := loadPoi(dir)
	if err != nil {
		t.Fatalf("loadPoi: %v", err)
	}
	if !ok {
		t.Fatal("loadPoi: miss on POI we just wrote")
	}
	if got.isDirty() {
		t.Error("a freshly-loaded poiManager must be clean, got dirty")
	}

	assertPoiRecord(t, got, homeA, poiTypeHome, poiTypeHome.maxTickets)
	assertPoiRecord(t, got, bellA, poiTypeMeeting, poiTypeMeeting.maxTickets)
	assertPoiRecord(t, got, homeB, poiTypeHome, poiTypeHome.maxTickets)
	assertPoiRecord(t, got, bellFar, poiTypeMeeting, wantFar)

	// Total record count must match (no phantom / dropped records).
	total := 0
	for _, s := range got.sections {
		total += len(s.records)
	}
	if total != 4 {
		t.Fatalf("loaded POI record count = %d, want 4", total)
	}
}

// assertPoiRecord checks the record at pos loaded with the expected type + free-ticket count.
func assertPoiRecord(t *testing.T, m *poiManager, pos pk.Position, wantType *poiType, wantFree int) {
	t.Helper()
	rec := m.recordAt(pos)
	if rec == nil {
		t.Fatalf("no POI record at %v after load", pos)
	}
	if rec.poiType != wantType {
		t.Errorf("%v type = %v, want %v", pos, rec.poiType.key, wantType.key)
	}
	if rec.pos != pos {
		t.Errorf("%v pos = %v, want %v", pos, rec.pos, pos)
	}
	if rec.freeTickets != wantFree {
		t.Errorf("%v free_tickets = %d, want %d", pos, rec.freeTickets, wantFree)
	}
}

// TestLoadPoiMissingDir confirms a first boot (no poi/ dir) is a clean (nil,false,nil) miss.
func TestLoadPoiMissingDir(t *testing.T) {
	dir := t.TempDir()
	m, ok, err := loadPoi(dir)
	if err != nil {
		t.Fatalf("loadPoi on empty dir: unexpected error %v", err)
	}
	if ok || m != nil {
		t.Fatalf("loadPoi on empty dir = (%v,%v), want (nil,false)", m, ok)
	}
}

// TestPoiVillageQuerySurvivesReload proves the reloaded manager answers the village-center query the
// raid trigger depends on: an occupied #village POI restores isVillageCenter for its section.
func TestPoiVillageQuerySurvivesReload(t *testing.T) {
	dir := t.TempDir()
	m := newPoiManager()
	bell := pk.Position{X: 5, Y: 64, Z: 5}
	rec := m.add(bell, poiTypeMeeting)
	rec.acquireTicket() // make it IS_OCCUPIED (freeTickets != maxTickets)

	if err := savePoi(dir, snapshotPoi(m)); err != nil {
		t.Fatalf("savePoi: %v", err)
	}
	got, ok, _ := loadPoi(dir)
	if !ok {
		t.Fatal("loadPoi miss")
	}
	if !got.isVillageCenter(sectionPosLong(bell)) {
		t.Error("reloaded manager: occupied #village POI must restore isVillageCenter")
	}
}
