package server

// scoreboard_test.go -- pins the clientbound scoreboard/teams WIRE (scoreboard_packets.go) against
// the jar-derived 776 layout (VERIFIED javap this session) and the ServerScoreboard broadcast +
// login-sync behavior (scoreboard.go). The pig oracle is untouched (scoreboard draws no entity RNG);
// TestPluginPigEqualsGoNativePig stays byte-identical.

import (
	"bytes"
	"testing"

	"github.com/imhinotori/sulfur/chat"
	"github.com/imhinotori/sulfur/data/packetid"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// readString decodes a length-prefixed UTF string from r.
func readString(t *testing.T, r *bytes.Reader) string {
	t.Helper()
	var s pk.String
	if _, err := s.ReadFrom(r); err != nil {
		t.Fatalf("read string: %v", err)
	}
	return string(s)
}

func readVarInt(t *testing.T, r *bytes.Reader) int32 {
	t.Helper()
	var v pk.VarInt
	if _, err := v.ReadFrom(r); err != nil {
		t.Fatalf("read varint: %v", err)
	}
	return int32(v)
}

func readByte(t *testing.T, r *bytes.Reader) int8 {
	t.Helper()
	var b pk.Byte
	if _, err := b.ReadFrom(r); err != nil {
		t.Fatalf("read byte: %v", err)
	}
	return int8(b)
}

func readBool(t *testing.T, r *bytes.Reader) bool {
	t.Helper()
	var b pk.Boolean
	if _, err := b.ReadFrom(r); err != nil {
		t.Fatalf("read bool: %v", err)
	}
	return bool(b)
}

func readComponent(t *testing.T, r *bytes.Reader) chat.Message {
	t.Helper()
	var m chat.Message
	if _, err := m.ReadFrom(r); err != nil {
		t.Fatalf("read component: %v", err)
	}
	return m
}

// TestSetObjectiveAddWire pins encodeSetObjectiveAddOrChange(ADD): String name, Byte method(0),
// Component displayName, VarInt renderType, Boolean numberFormat-present(false). VERIFIED javap
// ClientboundSetObjectivePacket.write.
func TestSetObjectiveAddWire(t *testing.T) {
	p := encodeSetObjectiveAddOrChange(objectiveMethodAdd, "kills", chat.Message{Text: "Kills"}, renderTypeInteger, numberFormat{kind: numberFormatNone})
	if p.ID != int32(packetid.ClientboundSetObjective) {
		t.Fatalf("id = %d, want %d", p.ID, packetid.ClientboundSetObjective)
	}
	r := bytes.NewReader(p.Data)
	if got := readString(t, r); got != "kills" {
		t.Fatalf("name = %q, want kills", got)
	}
	if got := readByte(t, r); got != objectiveMethodAdd {
		t.Fatalf("method = %d, want %d (ADD)", got, objectiveMethodAdd)
	}
	if got := readComponent(t, r); got.Text != "Kills" {
		t.Fatalf("displayName = %q, want Kills", got.Text)
	}
	if got := readVarInt(t, r); got != int32(renderTypeInteger) {
		t.Fatalf("renderType = %d, want %d (INTEGER)", got, renderTypeInteger)
	}
	if readBool(t, r) {
		t.Fatal("numberFormat present-flag = true, want false (Optional.empty)")
	}
	if r.Len() != 0 {
		t.Fatalf("trailing bytes: %d", r.Len())
	}
}

// TestSetObjectiveRemoveWire pins the REMOVE form: String name, Byte method(1), no payload.
func TestSetObjectiveRemoveWire(t *testing.T) {
	p := encodeSetObjectiveRemove("kills")
	r := bytes.NewReader(p.Data)
	if got := readString(t, r); got != "kills" {
		t.Fatalf("name = %q", got)
	}
	if got := readByte(t, r); got != objectiveMethodRemove {
		t.Fatalf("method = %d, want %d (REMOVE)", got, objectiveMethodRemove)
	}
	if r.Len() != 0 {
		t.Fatalf("REMOVE has trailing bytes: %d", r.Len())
	}
}

// TestSetDisplayObjectiveWire pins encodeSetDisplayObjective: VarInt slot id, String objectiveName.
// VERIFIED javap ClientboundSetDisplayObjectivePacket.write (writeById -> VarInt).
func TestSetDisplayObjectiveWire(t *testing.T) {
	p := encodeSetDisplayObjective(displaySlotSidebar, "kills")
	if p.ID != int32(packetid.ClientboundSetDisplayObjective) {
		t.Fatalf("id = %d, want %d", p.ID, packetid.ClientboundSetDisplayObjective)
	}
	r := bytes.NewReader(p.Data)
	if got := readVarInt(t, r); got != int32(displaySlotSidebar) {
		t.Fatalf("slot = %d, want %d (sidebar)", got, displaySlotSidebar)
	}
	if got := readString(t, r); got != "kills" {
		t.Fatalf("objectiveName = %q", got)
	}
	if r.Len() != 0 {
		t.Fatalf("trailing bytes: %d", r.Len())
	}
}

// TestSetScoreWire pins encodeSetScore: String owner, String objectiveName, VarInt score,
// Boolean display-present(false), Boolean numberFormat-present(false). VERIFIED javap
// ClientboundSetScorePacket composite order (score is VarInt).
func TestSetScoreWire(t *testing.T) {
	p := encodeSetScore("Steve", "kills", 42, chat.Message{}, false, numberFormat{kind: numberFormatNone})
	if p.ID != int32(packetid.ClientboundSetScore) {
		t.Fatalf("id = %d, want %d", p.ID, packetid.ClientboundSetScore)
	}
	r := bytes.NewReader(p.Data)
	if got := readString(t, r); got != "Steve" {
		t.Fatalf("owner = %q", got)
	}
	if got := readString(t, r); got != "kills" {
		t.Fatalf("objectiveName = %q", got)
	}
	if got := readVarInt(t, r); got != 42 {
		t.Fatalf("score = %d, want 42", got)
	}
	if readBool(t, r) {
		t.Fatal("display present-flag = true, want false")
	}
	if readBool(t, r) {
		t.Fatal("numberFormat present-flag = true, want false")
	}
	if r.Len() != 0 {
		t.Fatalf("trailing bytes: %d", r.Len())
	}
}

// TestResetScoreWire pins encodeResetScore: String owner, Boolean objective-present then String.
// VERIFIED javap ClientboundResetScorePacket.write (writeNullable).
func TestResetScoreWire(t *testing.T) {
	p := encodeResetScore("Steve", "kills", true)
	if p.ID != int32(packetid.ClientboundResetScore) {
		t.Fatalf("id = %d, want %d", p.ID, packetid.ClientboundResetScore)
	}
	r := bytes.NewReader(p.Data)
	if got := readString(t, r); got != "Steve" {
		t.Fatalf("owner = %q", got)
	}
	if !readBool(t, r) {
		t.Fatal("objective present-flag = false, want true")
	}
	if got := readString(t, r); got != "kills" {
		t.Fatalf("objectiveName = %q", got)
	}
	if r.Len() != 0 {
		t.Fatalf("trailing bytes: %d", r.Len())
	}
	// Absent objective: present-flag false, no string.
	p2 := encodeResetScore("Steve", "", false)
	r2 := bytes.NewReader(p2.Data)
	_ = readString(t, r2)
	if readBool(t, r2) {
		t.Fatal("absent objective present-flag = true, want false")
	}
	if r2.Len() != 0 {
		t.Fatalf("absent-objective trailing bytes: %d", r2.Len())
	}
}

// TestSetPlayerTeamAddWire pins encodeSetPlayerTeamAddOrChange(ADD): String name, Byte method(0),
// then the Parameters record (displayName, prefix, suffix Components; VarInt nametagVisibility;
// VarInt collisionRule; Boolean color-present + VarInt color id; Byte options LAST), then the
// member-list (VarInt count + UTF strings). VERIFIED javap ClientboundSetPlayerTeamPacket.write +
// Parameters Function7 order.
func TestSetPlayerTeamAddWire(t *testing.T) {
	team := newScoreboardTeam("red")
	team.displayName = chat.Message{Text: "Red Team"}
	team.playerPrefix = chat.Message{Text: "[R] "}
	team.playerSuffix = chat.Message{}
	team.allowFriendlyFire = false
	team.seeFriendlyInvisibles = true
	team.nameTagVisibility = teamVisibilityHideForOtherTeams
	team.collisionRule = teamCollisionPushOwnTeam
	team.color = teamColor(12)
	team.members["Steve"] = struct{}{}
	team.order = []string{"Steve"}

	p := encodeSetPlayerTeamAddOrChange(teamMethodAdd, team)
	if p.ID != int32(packetid.ClientboundSetPlayerTeam) {
		t.Fatalf("id = %d, want %d", p.ID, packetid.ClientboundSetPlayerTeam)
	}
	r := bytes.NewReader(p.Data)
	if got := readString(t, r); got != "red" {
		t.Fatalf("name = %q", got)
	}
	if got := readByte(t, r); got != teamMethodAdd {
		t.Fatalf("method = %d, want %d (ADD)", got, teamMethodAdd)
	}
	if got := readComponent(t, r); got.Text != "Red Team" {
		t.Fatalf("displayName = %q", got.Text)
	}
	if got := readComponent(t, r); got.Text != "[R] " {
		t.Fatalf("prefix = %q", got.Text)
	}
	_ = readComponent(t, r)
	if got := readVarInt(t, r); got != int32(teamVisibilityHideForOtherTeams) {
		t.Fatalf("nametagVisibility = %d, want %d", got, teamVisibilityHideForOtherTeams)
	}
	if got := readVarInt(t, r); got != int32(teamCollisionPushOwnTeam) {
		t.Fatalf("collisionRule = %d, want %d", got, teamCollisionPushOwnTeam)
	}
	if !readBool(t, r) {
		t.Fatal("color present-flag = false, want true")
	}
	if got := readVarInt(t, r); got != 12 {
		t.Fatalf("color id = %d, want 12 (red)", got)
	}
	if got := readByte(t, r); got != 0x2 {
		t.Fatalf("options = %#x, want 0x2 (see-friendly-invisibles only)", got)
	}
	if got := readVarInt(t, r); got != 1 {
		t.Fatalf("member count = %d, want 1", got)
	}
	if got := readString(t, r); got != "Steve" {
		t.Fatalf("member = %q", got)
	}
	if r.Len() != 0 {
		t.Fatalf("trailing bytes: %d", r.Len())
	}
}

// TestTeamOptionsBothFlags pins packOptions: 0x1|0x2 == 0x3, and the vanilla default is 0x3.
func TestTeamOptionsBothFlags(t *testing.T) {
	team := newScoreboardTeam("t")
	team.allowFriendlyFire = true
	team.seeFriendlyInvisibles = true
	if got := team.packOptions(); got != 0x3 {
		t.Fatalf("packOptions = %#x, want 0x3", got)
	}
	if got := newScoreboardTeam("d").packOptions(); got != 0x3 {
		t.Fatalf("default packOptions = %#x, want 0x3", got)
	}
}

// TestSetPlayerTeamJoinWire pins the JOIN form: String name, Byte method(3), VarInt count, UTF
// members. No Parameters. VERIFIED javap createPlayerPacket + write.
func TestSetPlayerTeamJoinWire(t *testing.T) {
	p := encodeSetPlayerTeamMembers(teamMethodJoin, "red", []string{"Alice", "Bob"})
	r := bytes.NewReader(p.Data)
	if got := readString(t, r); got != "red" {
		t.Fatalf("name = %q", got)
	}
	if got := readByte(t, r); got != teamMethodJoin {
		t.Fatalf("method = %d, want %d (JOIN)", got, teamMethodJoin)
	}
	if got := readVarInt(t, r); got != 2 {
		t.Fatalf("member count = %d, want 2", got)
	}
	if got := readString(t, r); got != "Alice" {
		t.Fatalf("member[0] = %q", got)
	}
	if got := readString(t, r); got != "Bob" {
		t.Fatalf("member[1] = %q", got)
	}
	if r.Len() != 0 {
		t.Fatalf("trailing bytes: %d", r.Len())
	}
}

// countScoreboardID counts packets with the given id in a drained slice.
func countScoreboardID(pkts []pk.Packet, id int32) int {
	n := 0
	for _, p := range pkts {
		if p.ID == id {
			n++
		}
	}
	return n
}

// TestScoreboardBroadcastAndSync exercises the ServerScoreboard broadcast (create objective + set
// display slot + set score -> the right packets reach a connected player) and the login sync (a
// joining player gets the current objectives/display/scores/teams replayed).
func TestScoreboardBroadcastAndSync(t *testing.T) {
	loop := NewTickLoop(nil)
	if loop.scoreboard == nil {
		t.Fatal("NewTickLoop did not seed scoreboard")
	}
	online := &tickPlayer{entityID: 1, name: "Online", client: captureClient(256)}
	loop.players = append(loop.players, online)

	o := loop.scoreboardAddObjective("kills", builtInCriteria["dummy"], chat.Message{Text: "Kills"})
	if o == nil {
		t.Fatal("addObjective returned nil")
	}
	loop.scoreboardSetDisplay(displaySlotSidebar, o)
	loop.scoreboardSetScore("Steve", o, 7)

	team := loop.scoreboardAddTeam("red")
	loop.scoreboardAddPlayerToTeam("Steve", team)

	got := drainPackets(online.client)
	if n := countScoreboardID(got, int32(packetid.ClientboundSetObjective)); n != 1 {
		t.Fatalf("online SetObjective = %d, want 1", n)
	}
	if n := countScoreboardID(got, int32(packetid.ClientboundSetDisplayObjective)); n != 1 {
		t.Fatalf("online SetDisplayObjective = %d, want 1", n)
	}
	if n := countScoreboardID(got, int32(packetid.ClientboundSetScore)); n != 1 {
		t.Fatalf("online SetScore = %d, want 1", n)
	}
	if n := countScoreboardID(got, int32(packetid.ClientboundSetPlayerTeam)); n != 2 {
		t.Fatalf("online SetPlayerTeam = %d, want 2 (ADD + JOIN)", n)
	}

	joiner := &tickPlayer{entityID: 2, name: "Joiner", client: captureClient(256)}
	loop.sendScoreboardStateTo(joiner)
	sync := drainPackets(joiner.client)
	if n := countScoreboardID(sync, int32(packetid.ClientboundSetObjective)); n != 1 {
		t.Fatalf("sync SetObjective = %d, want 1", n)
	}
	if n := countScoreboardID(sync, int32(packetid.ClientboundSetDisplayObjective)); n != 1 {
		t.Fatalf("sync SetDisplayObjective = %d, want 1", n)
	}
	if n := countScoreboardID(sync, int32(packetid.ClientboundSetScore)); n != 1 {
		t.Fatalf("sync SetScore = %d, want 1", n)
	}
	if n := countScoreboardID(sync, int32(packetid.ClientboundSetPlayerTeam)); n != 1 {
		t.Fatalf("sync SetPlayerTeam = %d, want 1 (ADD only)", n)
	}
}

// TestDisplaySlotByName pins DisplaySlot name resolution incl. the sidebar.team.<color> slots.
func TestDisplaySlotByName(t *testing.T) {
	cases := map[string]displaySlot{
		"list":               displaySlotList,
		"sidebar":            displaySlotSidebar,
		"below_name":         displaySlotBelowName,
		"sidebar.team.black": displaySlotTeamBase + 0,
		"sidebar.team.red":   displaySlotTeamBase + 12,
		"sidebar.team.white": displaySlotTeamBase + 15,
	}
	for name, want := range cases {
		got, ok := displaySlotByName(name)
		if !ok || got != want {
			t.Fatalf("displaySlotByName(%q) = %d,%v want %d", name, got, ok, want)
		}
	}
	if _, ok := displaySlotByName("bogus"); ok {
		t.Fatal("displaySlotByName(bogus) ok = true, want false")
	}
}
