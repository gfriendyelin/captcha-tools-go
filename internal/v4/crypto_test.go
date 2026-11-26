package v4

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func intPointer(value int) *int          { return &value }
func stringPointer(value string) *string { return &value }

func TestEncryptPayloadMode0(t *testing.T) {
	for _, payload := range []string{`{"text":"验证码🙂"}`, "{}", "abc"} {
		encoded, err := EncryptPayload(payload, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded)%4 != 0 {
			t.Fatalf("expected a padded base64url boundary, got %q", encoded)
		}
		decoded, err := base64.URLEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if string(decoded) != payload {
			t.Fatalf("decoded %q, want %q", decoded, payload)
		}
	}
	if encoded, _ := EncryptPayload("{}", 0, nil); encoded != "e30=" {
		t.Fatalf("unexpected padding: %q", encoded)
	}
}

func TestEncryptPayloadMode1(t *testing.T) {
	private, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"text":"验证码🙂"}`
	wire, err := EncryptPayload(payload, 1, &private.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(wire) <= 256 {
		t.Fatalf("wire is too short: %d", len(wire))
	}
	encrypted, err := hex.DecodeString(wire[:len(wire)-256])
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := hex.DecodeString(wire[len(wire)-256:])
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.DecryptPKCS1v15(rand.Reader, private, wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 16 {
		t.Fatalf("wrapped key is %d bytes", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	plain := make([]byte, len(encrypted))
	cipher.NewCBCDecrypter(block, []byte("0000000000000000")).CryptBlocks(plain, encrypted)
	padding := int(plain[len(plain)-1])
	if padding < 1 || padding > 16 {
		t.Fatalf("invalid padding %d", padding)
	}
	if string(plain[:len(plain)-padding]) != payload {
		t.Fatalf("decoded payload mismatch")
	}
}

func TestEncryptPayloadRejectsUnsupportedModes(t *testing.T) {
	for _, mode := range []int{3, -1} {
		if _, err := EncryptPayload("{}", mode, nil); err == nil {
			t.Fatalf("mode %d should fail", mode)
		}
	}
}

// Vectors generated independently with OpenSSL SM4-CBC, ASCII key
// "0123456789abcdef", ASCII IV "0000000000000000" and PKCS#7 padding.
func TestSM4CBCVectors(t *testing.T) {
	key := []byte("0123456789abcdef")
	vectors := []struct {
		plain  string
		cipher string
	}{
		{"", "1799d4bb520210d6c9b0b557e3e50b6d"},
		{"0123456789abcdef", "17eb982fad4defa2c429a3d4c3503b751be0c7019ea07667e52c46ad65a2c00a"},
		{"验证码🙂", "a3e1f8ea4e9ccf273bc052a24c9833f5"},
		{strings.Repeat("x", 33), "998083532a181a8d85f636dde5491e93e665e76ff2732bfda7fab4eebb56cfe3dc5462e25580ac534481f6351718fb57"},
	}
	for _, vector := range vectors {
		encrypted, err := sm4CBCEncrypt(vector.plain, key)
		if err != nil {
			t.Fatal(err)
		}
		if got := hex.EncodeToString(encrypted); got != vector.cipher {
			t.Fatalf("plain %q: got %s, want %s", vector.plain, got, vector.cipher)
		}
	}
}

func TestEncryptPayloadMode2(t *testing.T) {
	for _, payload := range []string{"", "0123456789abcdef", strings.Repeat("x", 33)} {
		wire, err := EncryptPayload(payload, 2, nil)
		if err != nil {
			t.Fatal(err)
		}
		blockCount := (len(payload)/16 + 1) * 16
		want := blockCount*2 + 224
		if len(wire) != want {
			t.Fatalf("payload %q: wire length %d, want %d", payload, len(wire), want)
		}
		if _, err := hex.DecodeString(wire); err != nil {
			t.Fatalf("payload %q: wire is not hex: %v", payload, err)
		}
	}
}

func TestProofOfWork(t *testing.T) {
	detail := PowDetail{
		Version:  stringPointer("1"),
		Bits:     intPointer(4),
		HashFunc: stringPointer("md5"),
		Extra:    stringPointer("extra"),
	}
	message, sign, err := ProofOfWork(context.Background(), "lot", "captcha", detail)
	if err != nil {
		t.Fatal(err)
	}
	if len(sign) != 32 || sign[0] != '0' {
		t.Fatalf("unexpected sign %q", sign)
	}
	parts := strings.Split(message, "|")
	if len(parts) != 8 {
		t.Fatalf("message has %d fields: %q", len(parts), message)
	}
	if parts[0] != "1" || parts[1] != "4" || parts[2] != "md5" || parts[4] != "captcha" || parts[5] != "lot" || parts[6] != "extra" {
		t.Fatalf("unexpected message %q", message)
	}
	if len(parts[7]) != 32 {
		t.Fatalf("nonce %q is not a dash-free UUID", parts[7])
	}
}

func TestProofOfWorkRejectsInvalidParameters(t *testing.T) {
	for _, detail := range []PowDetail{
		{Bits: intPointer(40)},
		{HashFunc: stringPointer("sha512")},
		{Bits: intPointer(-1)},
	} {
		if _, _, err := ProofOfWork(context.Background(), "lot", "captcha", detail); err == nil {
			t.Fatalf("detail %+v should fail", detail)
		}
	}
}

func TestLotFields(t *testing.T) {
	lot := "0123456789abcdef0123456789abcdef"
	fields, err := lotFields("n[26:28]+n[24:26]", "n[1:8]", lot)
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 1 || fields["abc89a"] != "12345678" {
		t.Fatalf("unexpected fields %#v", fields)
	}
	fields, err = lotFields("(n[18:23])+.+(n[20:20]+n[29:29]+n[11:11]+n[6:6])+.+(n[24:27]+n[20:23])", "n[23:30]", lot)
	if err != nil {
		t.Fatal(err)
	}
	first, ok := fields["234567"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected nesting %#v", fields)
	}
	second, ok := first["4db6"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected nesting %#v", fields)
	}
	if second["89ab4567"] != "789abcde" {
		t.Fatalf("unexpected value %#v", fields)
	}
}

func TestLotFieldsRejectsInvalidPattern(t *testing.T) {
	for _, pattern := range []string{"", "n[]", "n[1:2"} {
		if _, err := lotFields(pattern, "n[1:2]", "0123456789abcdef0123456789abcdef"); err == nil {
			t.Fatalf("pattern %q should fail", pattern)
		}
	}
	if _, err := lotFields("__proto__", "n[1:2]", "0123456789abcdef0123456789abcdef"); err == nil {
		t.Fatal("prototype keys must be rejected")
	}
}

func TestTrackSignature(t *testing.T) {
	mac := hmac.New(sha256.New, []byte("lot"))
	mac.Write([]byte("track"))
	want := hex.EncodeToString(mac.Sum(nil))
	if got := TrackSignature("lot", "track"); got != want {
		t.Fatalf("signature %q, want %q", got, want)
	}
}

func TestParseJSONP(t *testing.T) {
	object, err := parseJSONP([]byte(`geetest_1699999999999({"status":"success","code":1});`))
	if err != nil {
		t.Fatal(err)
	}
	if object["status"] != "success" {
		t.Fatalf("unexpected object %#v", object)
	}
	if _, err := parseJSONP([]byte(`[]`)); err == nil {
		t.Fatal("arrays must be rejected")
	}
}
