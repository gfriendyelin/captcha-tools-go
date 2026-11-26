package v4

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/emmansun/gmsm/sm2"
	"github.com/emmansun/gmsm/sm4"
)

// TestEncryptPayloadMode2RoundTrip exercises the exact pt=2 path with a
// throwaway SM2 key pair and decrypts the result, which validates the C1C2C3
// ordering, the stripped 0x04 prefix and the SM4/PKCS#7 framing.
//
// It must stay in package v4: the test swaps the package-level sm2PublicKey
// and calls the production EncryptPayload path. Do not add t.Parallel().
func TestEncryptPayloadMode2RoundTrip(t *testing.T) {
	private, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	previous := sm2PublicKey
	sm2PublicKey = &private.PublicKey
	t.Cleanup(func() { sm2PublicKey = previous })
	decrypterOpts := sm2.NewPlainDecrypterOpts(sm2.C1C2C3)

	payloads := []string{"", "0123456789abcdef", strings.Repeat("x", 33), `{"text":"验证码🙂"}`}
	for _, payload := range payloads {
		wire, err := EncryptPayload(payload, 2, nil)
		if err != nil {
			t.Fatal(err)
		}
		split := (len(payload)/16 + 1) * 16 * 2
		if len(wire) != split+224 {
			t.Fatalf("payload %q: wire length %d", payload, len(wire))
		}
		encrypted, err := hex.DecodeString(wire[:split])
		if err != nil {
			t.Fatal(err)
		}
		wrapped, err := hex.DecodeString(wire[split:])
		if err != nil {
			t.Fatal(err)
		}
		// x(32) || y(32) || C2(16) || C3(32); the 0x04 point prefix is stripped.
		if len(wrapped) != 112 {
			t.Fatalf("payload %q: SM2 wrapper is %d bytes, want 112", payload, len(wrapped))
		}
		key, err := private.Decrypt(rand.Reader, append([]byte{0x04}, wrapped...), decrypterOpts)
		if err != nil {
			t.Fatalf("payload %q: decrypt SM2 wrapper: %v", payload, err)
		}
		if len(key) != 16 {
			t.Fatalf("payload %q: unwrapped key is %d bytes", payload, len(key))
		}
		block, err := sm4.NewCipher(key)
		if err != nil {
			t.Fatal(err)
		}
		plain := make([]byte, len(encrypted))
		cipher.NewCBCDecrypter(block, []byte("0000000000000000")).CryptBlocks(plain, encrypted)
		padding := int(plain[len(plain)-1])
		if padding < 1 || padding > 16 {
			t.Fatalf("payload %q: invalid padding %d", payload, padding)
		}
		if string(plain[:len(plain)-padding]) != payload {
			t.Fatalf("payload %q: round trip mismatch", payload)
		}
	}
}
