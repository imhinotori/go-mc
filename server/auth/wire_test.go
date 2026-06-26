package auth

import (
	stdnet "net"
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	netmc "github.com/imhinotori/sulfur/net"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestEncryptionRequestWire is the regression guard for DELTA #1 (the missing
// shouldAuthenticate boolean). It STRICT-DECODES the EncryptionRequest in the exact
// vanilla ClientboundHelloPacket.write field order — serverId(String),
// publicKey(ByteArray), challenge(ByteArray), shouldAuthenticate(Boolean) — rather
// than relying on a self-consistent Go round-trip that would silently tolerate a
// missing trailing boolean (RESEARCH Pitfall 3).
//
// [VERIFIED: javap net.minecraft.network.protocol.login.ClientboundHelloPacket.write]
func TestEncryptionRequestWire(t *testing.T) {
	sc, cc := stdnet.Pipe()
	server := netmc.WrapConn(sc)
	client := netmc.WrapConn(cc)
	server.SetThreshold(-1)
	client.SetThreshold(-1)
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })

	pubKey := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	challenge := []byte{0x01, 0x02, 0x03, 0x04}

	errc := make(chan error, 1)
	go func() { errc <- encryptionRequest(server, pubKey, challenge) }()

	var p pk.Packet
	if err := client.ReadPacket(&p); err != nil {
		t.Fatalf("read EncryptionRequest: %v", err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("encryptionRequest write: %v", err)
	}
	if packetid.ClientboundPacketID(p.ID) != packetid.ClientboundLoginHello {
		t.Fatalf("EncryptionRequest id = %d, want %d", p.ID, packetid.ClientboundLoginHello)
	}

	// Strict-decode all FOUR fields in vanilla write order. If the boolean is missing,
	// Scan errors (short read) — exactly the desync a real client would hit.
	var (
		serverID           pk.String
		gotPubKey          pk.ByteArray
		gotChallenge       pk.ByteArray
		shouldAuthenticate pk.Boolean
	)
	if err := p.Scan(&serverID, &gotPubKey, &gotChallenge, &shouldAuthenticate); err != nil {
		t.Fatalf("strict-decode 4 EncryptionRequest fields (missing shouldAuthenticate?): %v", err)
	}
	if string(serverID) != "" {
		t.Errorf("serverId = %q, want empty", string(serverID))
	}
	if !bytesEqual(gotPubKey, pubKey) {
		t.Errorf("publicKey = %x, want %x", gotPubKey, pubKey)
	}
	if !bytesEqual(gotChallenge, challenge) {
		t.Errorf("challenge = %x, want %x", gotChallenge, challenge)
	}
	if !bool(shouldAuthenticate) {
		t.Errorf("shouldAuthenticate = false, want true (server only sends Hello in online-mode)")
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
