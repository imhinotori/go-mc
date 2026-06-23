package server

import (
	"encoding/json"
	"testing"

	"github.com/imhinotori/go-mc/chat"
	"github.com/imhinotori/go-mc/data/packetid"
	pk "github.com/imhinotori/go-mc/net/packet"
)

// pingHandler776 is the concrete ListPingHandler used by the assembled server. It
// composes the fork's PingInfo (version label + MOTD + favicon, Protocol fixed to
// 776) with a PlayerList (max/online/sample), which is exactly how a real server
// reports its status. Name/Protocol come from PingInfo; player counts from PlayerList.
func newPingHandler776(t *testing.T, motd string, maxPlayers int) ListPingHandler {
	t.Helper()
	return &struct {
		*PingInfo
		*PlayerList
	}{
		PingInfo:   NewPingInfo(ProtocolName, ProtocolVersion, chat.Text(motd), nil),
		PlayerList: NewPlayerList(maxPlayers),
	}
}

// statusList mirrors the JSON shape produced by Server.listResp.
type statusList struct {
	Version struct {
		Name     string `json:"name"`
		Protocol int    `json:"protocol"`
	} `json:"version"`
	Players struct {
		Max    int `json:"max"`
		Online int `json:"online"`
	} `json:"players"`
	Description *chat.Message `json:"description"`
}

// TestStatusPing covers NET-02: a Status ping returns a well-formed list response
// advertising version 26.2, protocol 776, a non-empty MOTD, and the configured
// player counts.
func TestStatusPing(t *testing.T) {
	server, client := newPipe(t)
	srv := &Server{ListPingHandler: newPingHandler776(t, "Ender MOTD", 42)}
	runAcceptConn(t, srv, server)

	// Drive the gate: a status-intent handshake (intention 1) with an arbitrary
	// client protocol, then a Status Request to elicit the list response.
	sendHandshake(t, client, ProtocolVersion, 1)
	if err := client.WritePacket(pk.Marshal(0x00)); err != nil { // Status Request
		t.Fatalf("write status request: %v", err)
	}

	var p pk.Packet
	if err := client.ReadPacket(&p); err != nil {
		t.Fatalf("read status response: %v", err)
	}
	if packetid.ClientboundPacketID(p.ID) != packetid.ClientboundStatusStatusResponse {
		t.Fatalf("expected status response (id %d), got id %d",
			packetid.ClientboundStatusStatusResponse, p.ID)
	}

	var raw pk.String
	if err := p.Scan(&raw); err != nil {
		t.Fatalf("scan status JSON string: %v", err)
	}

	var list statusList
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatalf("unmarshal status JSON %q: %v", string(raw), err)
	}

	if list.Version.Name != ProtocolName {
		t.Errorf("version.name = %q, want %q", list.Version.Name, ProtocolName)
	}
	if list.Version.Protocol != ProtocolVersion {
		t.Errorf("version.protocol = %d, want %d", list.Version.Protocol, ProtocolVersion)
	}
	if list.Players.Max != 42 {
		t.Errorf("players.max = %d, want 42", list.Players.Max)
	}
	if list.Players.Online != 0 {
		t.Errorf("players.online = %d, want 0", list.Players.Online)
	}
	if list.Description == nil || list.Description.ClearString() == "" {
		t.Errorf("description (MOTD) is empty, want %q", "Ender MOTD")
	}
}
