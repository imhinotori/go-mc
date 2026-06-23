package server

import (
	"testing"

	"github.com/google/uuid"

	"github.com/imhinotori/sulfur/data/packetid"
	netmc "github.com/imhinotori/sulfur/net"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/offline"
	"github.com/imhinotori/sulfur/yggdrasil/user"
)

// reachConfig is a ConfigHandler stub that records that AcceptConfig was reached —
// i.e. that login (including LoginAcknowledged) completed — then returns nil so the
// flow proceeds to AcceptPlayer. It writes nothing, so the client never blocks on a
// config read it didn't expect.
type reachConfig struct{ reached chan struct{} }

func (r *reachConfig) AcceptConfig(conn *netmc.Conn) error {
	close(r.reached)
	return nil
}

// noopGamePlay satisfies GamePlay without doing anything, so AcceptConn returns
// promptly after login+config in the test.
type noopGamePlay struct{}

func (noopGamePlay) AcceptPlayer(string, uuid.UUID, *user.PublicKey, []user.Property, int32, *netmc.Conn) {
}

// TestOfflineLogin covers NET-03: offline-mode login completes end-to-end —
// LoginStart -> Set Compression at the configured threshold -> LoginSuccess carrying
// the offline UUID -> server reads LoginAcknowledged -> flow proceeds to config.
func TestOfflineLogin(t *testing.T) {
	const (
		username  = "Steve"
		threshold = 256
	)

	server, client := newPipe(t)
	reached := make(chan struct{})
	srv := &Server{
		LoginHandler:  &MojangLoginHandler{OnlineMode: false, Threshold: threshold},
		ConfigHandler: &reachConfig{reached: reached},
		GamePlay:      noopGamePlay{},
	}
	runAcceptConn(t, srv, server)

	// 1. Handshake: protocol 776, login intent.
	sendHandshake(t, client, ProtocolVersion, 2)

	// 2. LoginStart (ServerboundLoginHello): username + a client-proposed UUID
	//    (offline servers ignore it and derive the UUID from the name).
	if err := client.WritePacket(pk.Marshal(
		packetid.ServerboundLoginHello,
		pk.String(username),
		pk.UUID(uuid.Nil),
	)); err != nil {
		t.Fatalf("write LoginStart: %v", err)
	}

	// 3. Expect Set Compression at the configured threshold, BEFORE LoginSuccess.
	var p pk.Packet
	if err := client.ReadPacket(&p); err != nil {
		t.Fatalf("read Set Compression: %v", err)
	}
	if packetid.ClientboundPacketID(p.ID) != packetid.ClientboundLoginLoginCompression {
		t.Fatalf("expected Set Compression (id %d), got id %d",
			packetid.ClientboundLoginLoginCompression, p.ID)
	}
	var gotThreshold pk.VarInt
	if err := p.Scan(&gotThreshold); err != nil {
		t.Fatalf("scan compression threshold: %v", err)
	}
	if int(gotThreshold) != threshold {
		t.Fatalf("Set Compression threshold = %d, want %d", gotThreshold, threshold)
	}
	// The server enabled compression on its side after this packet; mirror it on the
	// client end so subsequent reads decode the compressed frames, exactly as a real
	// client does on receiving Set Compression.
	client.SetThreshold(threshold)

	// 4. Expect LoginSuccess (ClientboundLoginLoginFinished) carrying the offline UUID.
	if err := client.ReadPacket(&p); err != nil {
		t.Fatalf("read LoginSuccess: %v", err)
	}
	if packetid.ClientboundPacketID(p.ID) != packetid.ClientboundLoginLoginFinished {
		t.Fatalf("expected LoginSuccess (id %d), got id %d",
			packetid.ClientboundLoginLoginFinished, p.ID)
	}
	var (
		gotID   pk.UUID
		gotName pk.String
	)
	if err := p.Scan(&gotID, &gotName); err != nil {
		t.Fatalf("scan LoginSuccess: %v", err)
	}
	wantID := offline.NameToUUID(username)
	if uuid.UUID(gotID) != wantID {
		t.Fatalf("LoginSuccess UUID = %s, want offline UUID %s", uuid.UUID(gotID), wantID)
	}
	if string(gotName) != username {
		t.Fatalf("LoginSuccess name = %q, want %q", string(gotName), username)
	}

	// 5. Send LoginAcknowledged; the server must read it and proceed to config.
	if err := client.WritePacket(pk.Marshal(
		packetid.ServerboundLoginLoginAcknowledged,
	)); err != nil {
		t.Fatalf("write LoginAcknowledged: %v", err)
	}

	select {
	case <-reached:
		// AcceptConfig was reached: LoginAcknowledged was read and login completed.
	default:
		// Block until the server goroutine advances; if it never reaches config the
		// test will time out via the test deadline.
		<-reached
	}

	// 6. The negotiated threshold is active on the server connection.
	// (Already exercised end-to-end: the LoginSuccess frame above decoded only because
	// both ends agreed on the threshold.)
}
