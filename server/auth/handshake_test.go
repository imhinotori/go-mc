package auth

import (
	"crypto/aes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	stdnet "net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/imhinotori/sulfur/data/packetid"
	netmc "github.com/imhinotori/sulfur/net"
	"github.com/imhinotori/sulfur/net/CFB8"
	pk "github.com/imhinotori/sulfur/net/packet"
)

// TestOnlineHandshake drives the SERVER side of the 776 online login over an in-memory
// net.Pipe (NO live network). It is the end-to-end regression guard for the encrypted
// handshake: a 4-field EncryptionRequest (with shouldAuthenticate=true) -> a crafted
// EncryptionResponse (RSA/PKCS1v15 of a known shared secret + echoed challenge) ->
// the connection flips to AES-128/CFB8 -> authDigest matches an independently-computed
// hash -> the stubbed hasJoined returns the profile.
//
// The sessionserver GET is stubbed via httptest (sessionServerURL is repointed), so the
// test makes NO live network call and CI stays offline. The crypto surfaces
// (DecryptPKCS1v15, CFB8, authDigest, the cipher-then-auth ordering) are exercised
// byte-identically — none are touched by the test seam.
//
// [VERIFIED: javap net.minecraft.server.network.ServerLoginPacketListenerImpl.handleKey
//  + net.minecraft.util.Crypt.getCipher/digestData]
func TestOnlineHandshake(t *testing.T) {
	const username = "Notch"

	// The server's 1024-bit RSA keypair (jar-faithful key size — Crypt.generateKeyPair).
	serverKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate server RSA key: %v", err)
	}

	// Stub the sessionserver hasJoined GET. The server side calls authentication() after
	// flipping the cipher; we return a minimal valid profile so Encrypt returns cleanly.
	// We also capture the serverId hash the server sent, to assert it equals the digest
	// derived from the shared secret + the server's pubkey (proves the key exchange).
	gotServerID := make(chan string, 1)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("username"); got != username {
			t.Errorf("hasJoined username = %q, want %q", got, username)
		}
		gotServerID <- r.URL.Query().Get("serverId")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"069a79f444e94726a5befca90e38aaf5","name":"Notch","properties":[]}`))
	}))
	defer stub.Close()

	origURL := sessionServerURL
	sessionServerURL = stub.URL
	defer func() { sessionServerURL = origURL }()

	sc, cc := stdnet.Pipe()
	server := netmc.WrapConn(sc)
	client := netmc.WrapConn(cc)
	server.SetThreshold(-1)
	client.SetThreshold(-1)
	t.Cleanup(func() { _ = server.Close(); _ = client.Close() })

	// Known 16-byte shared secret the "client" will RSA-encrypt and send.
	sharedSecret := []byte{
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
		0x88, 0x99, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF,
	}

	// Run the server side of the handshake in a goroutine.
	type result struct {
		resp *Resp
		err  error
	}
	resc := make(chan result, 1)
	go func() {
		resp, err := Encrypt(server, username, serverKey)
		resc <- result{resp, err}
	}()

	// --- CLIENT SIDE ---------------------------------------------------------------

	// 1. Read EncryptionRequest and STRICT-DECODE its four fields in vanilla write order.
	//    This is the regression guard for the shouldAuthenticate boolean (DELTA #1): a
	//    decode that only round-trips through the same writer would NOT catch a missing
	//    boolean, so we decode each field explicitly and in order.
	var p pk.Packet
	if err := client.ReadPacket(&p); err != nil {
		t.Fatalf("read EncryptionRequest: %v", err)
	}
	if packetid.ClientboundPacketID(p.ID) != packetid.ClientboundLoginHello {
		t.Fatalf("EncryptionRequest id = %d, want %d", p.ID, packetid.ClientboundLoginHello)
	}
	var (
		serverID           pk.String
		serverPubKeyDER    pk.ByteArray
		challenge          pk.ByteArray
		shouldAuthenticate pk.Boolean
	)
	if err := p.Scan(&serverID, &serverPubKeyDER, &challenge, &shouldAuthenticate); err != nil {
		t.Fatalf("strict-decode 4 EncryptionRequest fields (missing shouldAuthenticate?): %v", err)
	}
	if !bool(shouldAuthenticate) {
		t.Fatalf("shouldAuthenticate = false, want true")
	}
	if len(challenge) != 4 {
		t.Fatalf("challenge length = %d, want 4 (Ints.toByteArray(nextInt()))", len(challenge))
	}

	// 2. Parse the server's public key (X.509 SubjectPublicKeyInfo = PublicKey.getEncoded()).
	pub, err := x509.ParsePKIXPublicKey(serverPubKeyDER)
	if err != nil {
		t.Fatalf("parse server public key: %v", err)
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("server public key is not RSA")
	}

	// 3. RSA/PKCS1v15-encrypt the shared secret + echo the challenge, then send
	//    EncryptionResponse (ServerboundKeyPacket = two byte arrays).
	encSecret, err := rsa.EncryptPKCS1v15(rand.Reader, rsaPub, sharedSecret)
	if err != nil {
		t.Fatalf("encrypt shared secret: %v", err)
	}
	encChallenge, err := rsa.EncryptPKCS1v15(rand.Reader, rsaPub, challenge)
	if err != nil {
		t.Fatalf("encrypt challenge: %v", err)
	}
	if err := client.WritePacket(pk.Marshal(
		packetid.ServerboundLoginKey,
		pk.ByteArray(encSecret),
		pk.ByteArray(encChallenge),
	)); err != nil {
		t.Fatalf("write EncryptionResponse: %v", err)
	}

	// 4. After the server reads a VALID EncryptionResponse it flips its conn to CFB8
	//    (cipher BEFORE auth — handleKey ordering). Mirror the cipher on the client end so
	//    any subsequent server bytes (there are none here) would decode; the live assertion
	//    is that Encrypt returns the secret + the digest below.
	block, err := aes.NewCipher(sharedSecret)
	if err != nil {
		t.Fatalf("aes cipher: %v", err)
	}
	client.SetCipher(
		CFB8.NewCFB8Encrypt(block, sharedSecret),
		CFB8.NewCFB8Decrypt(block, sharedSecret),
	)

	// --- ASSERT SERVER RESULT ------------------------------------------------------
	res := <-resc
	if res.err != nil {
		t.Fatalf("server Encrypt: %v", res.err)
	}
	if res.resp.Name != username {
		t.Errorf("Resp.Name = %q, want %q", res.resp.Name, username)
	}

	// The serverId hash the server sent to hasJoined must equal the digest independently
	// computed from the shared secret the client encrypted + the server's pubkey read off
	// the wire. This proves Encrypt keyed the digest on the real exchanged secret (the
	// verify-token also had to PKCS1v15-decrypt-match the 4-byte challenge for Encrypt to
	// reach authentication at all — handleKey's validate-then-cipher-then-auth ordering).
	wantDigest := authDigest("", sharedSecret, serverPubKeyDER)
	sentServerID := <-gotServerID
	if sentServerID != wantDigest {
		t.Errorf("hasJoined serverId = %q, want digest %q", sentServerID, wantDigest)
	}
}
