package auth

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// TestAuthDigest locks the Notchian server-hash vectors (the canonical wiki examples)
// including the negative twos-complement path. authDigest mirrors vanilla
// Crypt.digestData (SHA-1 over serverId.getBytes(ISO_8859_1) + secret.getEncoded() +
// pubkey.getEncoded()) wrapped in new BigInteger(bytes).toString(16); the canonical
// vectors are the SHA-1 of the serverId STRING alone, so we feed the name as serverId
// with empty secret/pubkey. jeb_ exercises the leading-`-` (high-bit-set) branch —
// a regression of the negative-hash rendering fails here.
// [VERIFIED: javap net.minecraft.util.Crypt.digestData
//  + net.minecraft.server.network.ServerLoginPacketListenerImpl.handleKey]
func TestAuthDigest(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"Notch", "4ed1f46bbe04bc756bcb17c0c7ce3e4632f06a48"},
		{"jeb_", "-7c9d5b0044c130109a5d7b5fb5c317c02b4e28c1"}, // negative path
		{"simon", "88e16a1019277b15d58faf0541e11910eb756f6"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := authDigest(c.name, nil, nil)
			if got != c.want {
				t.Errorf("authDigest(%q) = %q, want %q", c.name, got, c.want)
			}
		})
	}
}

func TestResp(t *testing.T) {
	var resp Resp
	err := json.Unmarshal([]byte(`{"id":"853c80ef3c3749fdaa49938b674adae6","name":"jeb_","properties":[{"name":"textures","value":"eyJ0aW1lc3RhbXAiOjE1NTk1NDM5MzMwMjUsInByb2ZpbGVJZCI6Ijg1M2M4MGVmM2MzNzQ5ZmRhYTQ5OTM4YjY3NGFkYWU2IiwicHJvZmlsZU5hbWUiOiJqZWJfIiwidGV4dHVyZXMiOnsiU0tJTiI6eyJ1cmwiOiJodHRwOi8vdGV4dHVyZXMubWluZWNyYWZ0Lm5ldC90ZXh0dXJlLzdmZDliYTQyYTdjODFlZWVhMjJmMTUyNDI3MWFlODVhOGUwNDVjZTBhZjVhNmFlMTZjNjQwNmFlOTE3ZTY4YjUifSwiQ0FQRSI6eyJ1cmwiOiJodHRwOi8vdGV4dHVyZXMubWluZWNyYWZ0Lm5ldC90ZXh0dXJlLzU3ODZmZTk5YmUzNzdkZmI2ODU4ODU5ZjkyNmM0ZGJjOTk1NzUxZTkxY2VlMzczNDY4YzVmYmY0ODY1ZTcxNTEifX19"}]}`), &resp)
	if err != nil {
		panic(err)
	}
	wantID := uuid.Must(uuid.Parse("853c80ef3c3749fdaa49938b674adae6"))

	// check UUID
	if resp.ID != wantID {
		t.Errorf("uuid doesn't match: %v, want %s", resp.ID, wantID)
	}

	// check name
	if resp.Name != "jeb_" {
		t.Errorf("name doesn't match: %s, want %s", resp.Name, "jeb_")
	}

	// check texture
	texture, err := resp.Texture()
	if err != nil {
		t.Fatal(err)
	}

	t.Log(texture.TimeStamp)

	if texture.ID != wantID {
		t.Errorf("uuid doesn't match: %v, want %s", texture.ID, wantID)
	}

	if texture.Name != "jeb_" {
		t.Errorf("name doesn't match: %s, want %s", texture.Name, "jeb_")
	}

	const (
		wantSKIN = "http://textures.minecraft.net/texture/7fd9ba42a7c81eeea22f1524271ae85a8e045ce0af5a6ae16c6406ae917e68b5"
		wantCAPE = "http://textures.minecraft.net/texture/5786fe99be377dfb6858859f926c4dbc995751e91cee373468c5fbf4865e7151"
	)
	if texture.Textures.SKIN.URL != wantSKIN {
		t.Errorf("skin url not match: %s, want %s",
			texture.Textures.SKIN.URL,
			wantSKIN)
	}
	if texture.Textures.CAPE.URL != wantCAPE {
		t.Errorf("cape url not match: %s, want %s",
			texture.Textures.CAPE.URL,
			wantCAPE)
	}
}
