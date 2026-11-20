package geetest

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"math"
	"strings"
)

const publicKeyPEM = `-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDB45NNFhRGWzMFPn9I7k7IexS5
XviJR3E9Je7L/350x5d9AtwdlFH3ndXRwQwprLaptNb7fQoCebZxnhdyVl8Jr2J3
FZGSIa75GJnK4IwNaG10iyCjYDviMYymvCtZcGWSqSGdC/Bcn2UCOiHSMwgHJSrg
Bm1Zzu+l8nSOqAurgQIDAQAB
-----END PUBLIC KEY-----`

const encodeAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789()"

var encodeMasks = [...]uint32{7274496, 9483264, 19220, 235}

// Encode implements Geetest's custom 24-bit transform, not ordinary Base64.
func Encode(input []byte) string {
	var out strings.Builder
	out.Grow((len(input) + 2) / 3 * 4)
	padding := ""
	for i := 0; i < len(input); i += 3 {
		n := len(input) - i
		if n > 3 {
			n = 3
		}
		value := uint32(input[i]) << 16
		if n >= 2 {
			value |= uint32(input[i+1]) << 8
		}
		if n == 3 {
			value |= uint32(input[i+2])
		}
		chars := n + 1
		if n == 3 {
			chars = 4
		}
		for j := 0; j < chars; j++ {
			out.WriteByte(encodeAlphabet[transformValue(value, encodeMasks[j])])
		}
		switch n {
		case 1:
			padding = ".."
		case 2:
			padding = "."
		}
	}
	return out.String() + padding
}

func transformValue(value, mask uint32) uint32 {
	var result uint32
	for r := 23; r >= 0; r-- {
		if (mask>>r)&1 != 0 {
			result = (result << 1) | ((value >> r) & 1)
		}
	}
	return result
}

func aesEncrypt(key, plaintext []byte) ([]byte, error) {
	if len(key) != 16 {
		return nil, fmt.Errorf("geetest: AES key must be 16 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(plaintext, block.BlockSize())
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, []byte("0000000000000000")).CryptBlocks(ciphertext, padded)
	return ciphertext, nil
}

func pkcs7Pad(input []byte, blockSize int) []byte {
	padding := blockSize - len(input)%blockSize
	out := make([]byte, len(input)+padding)
	copy(out, input)
	for i := len(input); i < len(out); i++ {
		out[i] = byte(padding)
	}
	return out
}

func generateAESKey(float64Fn func() float64) (string, error) {
	if float64Fn == nil {
		return "", fmt.Errorf("geetest: nil random source")
	}
	var key strings.Builder
	key.Grow(16)
	for i := 0; i < 4; i++ {
		r := float64Fn()
		if math.IsNaN(r) || math.IsInf(r, 0) || r < 0 || r >= 1 {
			return "", fmt.Errorf("geetest: random source returned %v, want [0,1)", r)
		}
		v := int((1.0 + r) * 65536.0)
		formatted := fmt.Sprintf("%04x", v)
		if len(formatted) != 5 || formatted[0] != '1' {
			return "", fmt.Errorf("geetest: invalid AES key fragment %q", formatted)
		}
		key.WriteString(formatted[1:])
	}
	return key.String(), nil
}

func encryptAESKey(publicKey *rsa.PublicKey, random io.Reader, aesKey string) (string, error) {
	if len(aesKey) != 16 {
		return "", fmt.Errorf("geetest: AES key must contain 16 ASCII characters")
	}
	if random == nil {
		random = rand.Reader
	}
	ciphertext, err := rsa.EncryptPKCS1v15(random, publicKey, []byte(aesKey))
	if err != nil {
		return "", fmt.Errorf("geetest: RSA encrypt AES key: %w", err)
	}
	return hex.EncodeToString(ciphertext), nil
}

func parsePublicKey() (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(publicKeyPEM))
	if block == nil {
		return nil, fmt.Errorf("geetest: invalid public key PEM")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("geetest: parse public key: %w", err)
	}
	publicKey, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("geetest: public key is %T, not RSA", key)
	}
	return publicKey, nil
}

func md5Hex(text string) string {
	sum := md5.Sum([]byte(text))
	return hex.EncodeToString(sum[:])
}
