package auth

import (
	"bytes"
	"crypto/aes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"

	"github.com/imhinotori/sulfur/data/packetid"
	"github.com/imhinotori/sulfur/net"
	"github.com/imhinotori/sulfur/net/CFB8"
	pk "github.com/imhinotori/sulfur/net/packet"
	"github.com/imhinotori/sulfur/yggdrasil/user"
)

// Deprecated: Moved to go-mc/yggdrasil/user, because go-mc/bot also needs them
type (
	Texture   = user.Texture
	Property  = user.Property
	PublicKey = user.PublicKey
)

// verifyTokenLen is the EncryptionRequest challenge length. Vanilla derives the
// challenge as Ints.toByteArray(RandomSource.create().nextInt()) = exactly 4 bytes
// (one int's big-endian encoding). The client echoes back whatever length it received,
// so this is observably harmless either way, but 4 is the strict 1:1 value. We keep the
// existing rand.Read mechanism (4 cryptographically-random bytes is round-trip
// equivalent to one nextInt's big-endian bytes).
// [VERIFIED: javap net.minecraft.server.network.ServerLoginPacketListenerImpl
//  <init> challenge = Ints.toByteArray(nextInt())]
const verifyTokenLen = 4

// Encrypt a connection, with authentication
func Encrypt(conn *net.Conn, name string, serverKey *rsa.PrivateKey) (*Resp, error) {
	publicKey, err := x509.MarshalPKIXPublicKey(&serverKey.PublicKey)
	if err != nil {
		return nil, err
	}

	verifyToken := make([]byte, verifyTokenLen)
	_, err = rand.Read(verifyToken)
	if err != nil {
		return nil, err
	}

	// encryption request
	err = encryptionRequest(conn, publicKey, verifyToken)
	if err != nil {
		return nil, err
	}

	// encryption response
	SharedSecret, err := encryptionResponse(conn, serverKey, verifyToken)
	if err != nil {
		return nil, err
	}

	// encryption the connection
	block, err := aes.NewCipher(SharedSecret)
	if err != nil {
		return nil, errors.New("load aes encryption key fail")
	}

	conn.SetCipher( // 启用加密
		CFB8.NewCFB8Encrypt(block, SharedSecret),
		CFB8.NewCFB8Decrypt(block, SharedSecret),
	)
	hash := authDigest("", SharedSecret, publicKey)
	resp, err := authentication(name, hash) // auth
	if err != nil {
		return nil, errors.New("auth servers down")
	}

	return resp, nil
}

func encryptionRequest(conn *net.Conn, publicKey, verifyToken []byte) error {
	// Wire order is the jar-exact ClientboundHelloPacket.write field sequence:
	//   writeUtf(serverId), writeByteArray(publicKey), writeByteArray(challenge),
	//   writeBoolean(shouldAuthenticate).
	// The trailing shouldAuthenticate boolean (added in the 1.20.5 era) is the 4th
	// field a real 26.2 client readBoolean()s; omitting it desyncs the decode. We send
	// `true` unconditionally because the server only ever sends the EncryptionRequest in
	// online-mode (MinecraftServer.usesAuthentication()), which is exactly when
	// ServerLoginPacketListenerImpl.handleHello constructs the packet with iconst_1.
	// [VERIFIED: javap net.minecraft.network.protocol.login.ClientboundHelloPacket.write
	//  + net.minecraft.server.network.ServerLoginPacketListenerImpl.handleHello]
	return conn.WritePacket(pk.Marshal(
		packetid.ClientboundLoginHello,
		pk.String(""),
		pk.ByteArray(publicKey),
		pk.ByteArray(verifyToken),
		pk.Boolean(true),
	))
}

func encryptionResponse(conn *net.Conn, serverKey *rsa.PrivateKey, verifyToken []byte) ([]byte, error) {
	var p pk.Packet
	err := conn.ReadPacket(&p)
	if err != nil {
		return nil, err
	}
	if packetid.ServerboundPacketID(p.ID) != packetid.ServerboundLoginKey {
		return nil, fmt.Errorf("0x%02X is not Encryption Response", p.ID)
	}

	var keyBytes pk.ByteArray
	var encryptedVerifyToken pk.ByteArray

	err = p.Scan(&keyBytes, &encryptedVerifyToken)
	if err != nil {
		return nil, err
	}

	// confirm to verify token
	decryptedVerifyToken, err := rsa.DecryptPKCS1v15(rand.Reader, serverKey, encryptedVerifyToken)
	if err != nil {
		return nil, err
	} else if !bytes.Equal(verifyToken, decryptedVerifyToken) {
		return nil, errors.New("verifyToken not match")
	}

	// get sharedSecret
	sharedSecret, err := rsa.DecryptPKCS1v15(rand.Reader, serverKey, keyBytes)
	if err != nil {
		return nil, err
	}

	return sharedSecret, nil
}

// sessionServerURL is the FAITHFUL Yggdrasil hasJoined base URL: authlib
// YggdrasilEnvironment PROD session host + the hasJoinedServer path. It is a package
// var (not a const) ONLY so tests can point it at an httptest stub and keep CI offline;
// production never reassigns it, so the live endpoint is byte-identical to vanilla.
// [VERIFIED: javap authlib YggdrasilMinecraftSessionService.hasJoinedServer
//  + YggdrasilEnvironment PROD]
var sessionServerURL = "https://sessionserver.mojang.com/session/minecraft/hasJoined"

func authentication(name, hash string) (*Resp, error) {
	// Build the query with net/url so username/serverId are correctly percent-encoded
	// (the serverId hash is hex/`-`-prefixed and safe, but valid usernames and the
	// encoding are made correct here rather than string-concatenated). Base URL + path
	// are FAITHFUL — only the query construction changes. The optional `ip` param
	// (authlib preventProxyConnections) is intentionally omitted (out of scope).
	q := url.Values{"username": {name}, "serverId": {hash}}
	resp, err := http.Get(sessionServerURL + "?" + q.Encode())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var Resp Resp
	err = json.Unmarshal(body, &Resp)

	return &Resp, err
}

// authDigest computes a special SHA-1 digest required for Minecraft web
// authentication on Premium servers (online-mode=true).
// Source: http://wiki.vg/Protocol_Encryption#Server
//
// Also many, many thanks to SirCmpwn and his wonderful gist (C#):
// https://gist.github.com/SirCmpwn/404223052379e82f91e6
func authDigest(serverID string, sharedSecret, publicKey []byte) string {
	h := sha1.New()
	h.Write([]byte(serverID))
	h.Write(sharedSecret)
	h.Write(publicKey)
	hash := h.Sum(nil)

	// Check for negative hashes
	negative := (hash[0] & 0x80) == 0x80
	if negative {
		hash = twosComplement(hash)
	}

	// Trim away zeroes
	res := strings.TrimLeft(fmt.Sprintf("%x", hash), "0")
	if negative {
		res = "-" + res
	}

	return res
}

// little endian
func twosComplement(p []byte) []byte {
	carry := true
	for i := len(p) - 1; i >= 0; i-- {
		p[i] = byte(^p[i])
		if carry {
			carry = p[i] == 0xff
			p[i]++
		}
	}
	return p
}

// Resp is the response of authentication
type Resp struct {
	Name       string
	ID         uuid.UUID
	Properties []user.Property
}

// Texture unmarshal the base64 encoded texture of Resp
func (r *Resp) Texture() (t user.Texture, err error) {
	var texture []byte
	texture, err = base64.StdEncoding.DecodeString(r.Properties[0].Value)
	if err != nil {
		return
	}

	err = json.Unmarshal(texture, &t)
	return
}
