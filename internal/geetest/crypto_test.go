package geetest

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"testing"
)

func TestEncodeVectors(t *testing.T) {
	tests := []struct {
		hexInput string
		want     string
	}{
		{"", ""},
		{"00", "AA.."},
		{"ff", ")w.."},
		{"0000", "AAA."},
		{"ffff", "))8."},
		{"000000", "AAAA"},
		{"ffffff", "))))"},
		{"61", "xA.."},
		{"6162", "xEo."},
		{"616263", "xEob"},
		{"4d616e", "tEle"},
		{"01020304", "BAIDEA.."},
	}
	for _, tt := range tests {
		input, err := hex.DecodeString(tt.hexInput)
		if err != nil {
			t.Fatal(err)
		}
		if got := Encode(input); got != tt.want {
			t.Errorf("Encode(%s) = %q, want %q", tt.hexInput, got, tt.want)
		}
	}
}

func TestAES128CBC_PKCS7Vector(t *testing.T) {
	got, err := aesEncrypt([]byte("0123456789abcdef"), []byte("abc"))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := hex.DecodeString("e76c28de433010129d238f592f0f8e61")
	if !bytes.Equal(got, want) {
		t.Fatalf("ciphertext = %x, want %x", got, want)
	}
	if len(got) != aesBlockSizeForTest {
		t.Fatalf("PKCS7 output length = %d", len(got))
	}
}

const aesBlockSizeForTest = 16

func TestGenerateAESKey(t *testing.T) {
	values := []float64{0, 0.5, 0.999999, 0.25}
	i := 0
	key, err := generateAESKey(func() float64 {
		v := values[i]
		i++
		return v
	})
	if err != nil {
		t.Fatal(err)
	}
	if key != "00008000ffff4000" {
		t.Fatalf("key = %q", key)
	}
	if _, err := generateAESKey(func() float64 { return 1 }); err == nil {
		t.Fatal("expected range error")
	}
}

func TestRSAPKCS1v15RoundTrip(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	const plain = "0123456789abcdef"
	encoded, err := encryptAESKey(&privateKey.PublicKey, bytes.NewReader(bytes.Repeat([]byte{0x42}, 256)), plain)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != 256 {
		t.Fatalf("RSA hex length = %d", len(encoded))
	}
	ciphertext, _ := hex.DecodeString(encoded)
	decrypted, err := rsa.DecryptPKCS1v15(nil, privateKey, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if string(decrypted) != plain {
		t.Fatalf("decrypted = %q", decrypted)
	}
}

func TestProductionPublicKey(t *testing.T) {
	key, err := parsePublicKey()
	if err != nil {
		t.Fatal(err)
	}
	if key.N.BitLen() != 1024 || key.E != 65537 {
		t.Fatalf("unexpected RSA parameters: %d/%d", key.N.BitLen(), key.E)
	}
}
