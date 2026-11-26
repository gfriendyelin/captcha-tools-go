// Package v4 implements the GeeTest V4 load/verify protocol: payload
// encryption, proof of work, the patrol config table, pointer tracks and the
// keyring used to recover a build's RSA modulus.
package v4

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"math/big"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/emmansun/gmsm/sm2"
	"github.com/emmansun/gmsm/sm4"
)

// builtinModulus is the RSA-1024 modulus shipped by the GeeTest V4 widget over
// several releases. Keyring overlays a recovered modulus per static build.
const builtinModulus = "00C1E3934D1614465B33053E7F48EE4EC87B14B95EF88947713D25EECBFF7E74C7977D02DC1D9451F79DD5D1C10C29ACB6A9B4D6FB7D0A0279B6719E1772565F09AF627715919221AEF91899CAE08C0D686D748B20A3603BE2318CA6BC2B59706592A9219D0BF05C9F65023A21D2330807252AE0066D59CEEFA5F2748EA80BAB81"

// sm2PublicKeyHex is the fixed SM2 key used by pt=2; it is unrelated to the
// per-build RSA keyring.
const sm2PublicKeyHex = "049a4ea935b2576f37516d9b29cd8d8cc9bffe548ba6853253ba20f4ba44fba8c9e97a398882769aa0dd1e3e1b5601429287303880ca17bd244ed73bf702a68fc7"

var (
	builtinKey   = mustParseModulus(builtinModulus)
	sm2PublicKey = mustParseSM2Key()
)

func mustParseSM2Key() *ecdsa.PublicKey {
	raw, err := hex.DecodeString(sm2PublicKeyHex)
	if err != nil {
		panic(fmt.Errorf("v4: invalid SM2 public key: %w", err))
	}
	publicKey, err := sm2.NewPublicKey(raw)
	if err != nil {
		panic(fmt.Errorf("v4: invalid SM2 public key: %w", err))
	}
	return publicKey
}

func mustParseModulus(modulusHex string) *rsa.PublicKey {
	key, err := parseModulus(modulusHex)
	if err != nil {
		panic(err)
	}
	return key
}

// parseModulus builds the RSA public key with exponent 65537 from a hex
// modulus, dropping the single leading "00" sign byte like the reference.
func parseModulus(modulusHex string) (*rsa.PublicKey, error) {
	hexValue := strings.TrimPrefix(modulusHex, "00")
	n := new(big.Int)
	if _, ok := n.SetString(hexValue, 16); !ok {
		return nil, fmt.Errorf("v4: invalid RSA modulus")
	}
	return &rsa.PublicKey{N: n, E: 65537}, nil
}

// pkcs7Pad appends PKCS#7 padding to a 16-byte block boundary.
func pkcs7Pad(data []byte) []byte {
	padding := 16 - len(data)%16
	out := make([]byte, len(data)+padding)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(padding)
	}
	return out
}

// EncryptPayload mirrors encryptPayload from the reference adapter.
//
//	0: unpadded base64url (padded to a 4-character boundary with '=')
//	1: AES-128-CBC(hex) + RSA PKCS#1 v1.5(hex)
//	2: SM4-CBC(hex) + SM2 C1C2C3 (hex, without the 0x04 point prefix)
func EncryptPayload(payload string, mode int, key *rsa.PublicKey) (string, error) {
	switch mode {
	case 0:
		encoded := base64.RawURLEncoding.EncodeToString([]byte(payload))
		if remainder := len(payload) % 3; remainder != 0 {
			encoded += strings.Repeat("=", 3-remainder)
		}
		return encoded, nil
	case 1:
		raw := make([]byte, 8)
		if _, err := rand.Read(raw); err != nil {
			return "", err
		}
		// The wire key is the 16 ASCII bytes of the hex representation.
		aesKey := []byte(hex.EncodeToString(raw))
		block, err := aes.NewCipher(aesKey)
		if err != nil {
			return "", err
		}
		padded := pkcs7Pad([]byte(payload))
		encrypted := make([]byte, len(padded))
		cipher.NewCBCEncrypter(block, []byte("0000000000000000")).CryptBlocks(encrypted, padded)
		if key == nil {
			key = builtinKey
		}
		wrapped, err := rsa.EncryptPKCS1v15(rand.Reader, key, aesKey)
		if err != nil {
			return "", err
		}
		return hex.EncodeToString(encrypted) + hex.EncodeToString(wrapped), nil
	case 2:
		raw := make([]byte, 8)
		if _, err := rand.Read(raw); err != nil {
			return "", err
		}
		sm4Key := []byte(hex.EncodeToString(raw))
		return encryptPayloadSM2(payload, sm4Key, sm2PublicKey)
	default:
		return "", fmt.Errorf("Unsupported encryption mode: %d", mode)
	}
}

// encryptPayloadSM2 produces the pt=2 wire form: hex(SM4-CBC) followed by the
// SM2 C1C2C3 wrapper for the ASCII key, without the 0x04 point prefix.
// sm-crypto's doEncrypt(..., 0) uses C1C2C3, so the server expects x||y||C2||C3.
func encryptPayloadSM2(payload string, key []byte, publicKey *ecdsa.PublicKey) (string, error) {
	encrypted, err := sm4CBCEncrypt(payload, key)
	if err != nil {
		return "", err
	}
	wrapped, err := sm2.Encrypt(rand.Reader, publicKey, key,
		sm2.NewPlainEncrypterOpts(sm2.MarshalUncompressed, sm2.C1C2C3))
	if err != nil {
		return "", err
	}
	// sm-crypto strips the uncompressed point prefix, so the server expects
	// x || y || C2 || C3 rather than 04 || x || y || C2 || C3.
	if len(wrapped) > 0 && wrapped[0] == 0x04 {
		wrapped = wrapped[1:]
	}
	return hex.EncodeToString(encrypted) + hex.EncodeToString(wrapped), nil
}

// sm4CBCEncrypt encrypts with SM4-CBC, the ASCII IV and PKCS#7 padding used by
// the reference widget.
func sm4CBCEncrypt(payload string, key []byte) ([]byte, error) {
	block, err := sm4.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad([]byte(payload))
	encrypted := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, []byte("0000000000000000")).CryptBlocks(encrypted, padded)
	return encrypted, nil
}

// PowDetail is the proof-of-work directive returned by /load.
type PowDetail struct {
	Version  *string `json:"version"`
	Bits     *int    `json:"bits"`
	HashFunc *string `json:"hashfunc"`
	DateTime *string `json:"datetime"`
	Extra    *string `json:"extra"`
}

func (d PowDetail) values() (version string, bits int, hashFunc, datetime, extra string) {
	version, bits, hashFunc = "1", 10, "md5"
	if d.Version != nil {
		version = *d.Version
	}
	if d.Bits != nil {
		bits = *d.Bits
	}
	if d.HashFunc != nil {
		hashFunc = *d.HashFunc
	}
	if d.DateTime != nil {
		datetime = *d.DateTime
	}
	if d.Extra != nil {
		extra = *d.Extra
	}
	return
}

func powHash(name string) (hash.Hash, error) {
	switch name {
	case "md5":
		return md5.New(), nil
	case "sha1":
		return sha1.New(), nil
	case "sha256":
		return sha256.New(), nil
	default:
		return nil, fmt.Errorf("Invalid PoW parameters")
	}
}

// ProofOfWork searches for a nonce whose hash has the requested leading zero
// nibbles, mirroring the reference message layout.
func ProofOfWork(ctx context.Context, lot, captchaID string, detail PowDetail) (msg, sign string, err error) {
	version, bits, hashFunc, datetime, extra := detail.values()
	hasher, err := powHash(hashFunc)
	if err != nil {
		return "", "", err
	}
	if bits < 0 || bits > 28 {
		return "", "", fmt.Errorf("Invalid PoW parameters")
	}
	prefix := strings.Repeat("0", bits/4)
	threshold := []byte{'f', '7', '3', '1'}[bits%4]
	for attempt := 0; ; attempt++ {
		if attempt%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return "", "", err
			}
			runtime.Gosched()
		}
		message := version + "|" + strconv.Itoa(bits) + "|" + hashFunc + "|" + datetime + "|" +
			captchaID + "|" + lot + "|" + extra + "|" + strings.ReplaceAll(newUUID(), "-", "")
		hasher.Reset()
		hasher.Write([]byte(message))
		digest := hex.EncodeToString(hasher.Sum(nil))
		if strings.HasPrefix(digest, prefix) && digest[len(prefix)] <= threshold {
			return message, digest, nil
		}
	}
}

var lotPartPattern = regexp.MustCompile(`\[(-?\d+)(?::(-?\d+))?\]`)

// lotFields expands the abo key/value slice patterns against the lot number.
func lotFields(keyPattern, valuePattern, lot string) (map[string]any, error) {
	extract := func(pattern string) (string, error) {
		groups := strings.Split(pattern, "+.+")
		values := make([]string, len(groups))
		for i, group := range groups {
			var b strings.Builder
			for _, part := range strings.Split(group, "+") {
				match := lotPartPattern.FindStringSubmatch(part)
				if match == nil {
					return "", fmt.Errorf("Invalid lot pattern")
				}
				start, err := strconv.Atoi(match[1])
				if err != nil {
					return "", fmt.Errorf("Invalid lot pattern")
				}
				end := start + 1
				if match[2] != "" {
					value, err := strconv.Atoi(match[2])
					if err != nil {
						return "", fmt.Errorf("Invalid lot pattern")
					}
					end = value + 1
				}
				b.WriteString(jsSlice(lot, start, end))
			}
			values[i] = b.String()
		}
		return strings.Join(values, "."), nil
	}
	keyPath, err := extract(keyPattern)
	if err != nil {
		return nil, err
	}
	value, err := extract(valuePattern)
	if err != nil {
		return nil, err
	}
	root := map[string]any{}
	current := root
	keys := strings.Split(keyPath, ".")
	for _, key := range keys[:len(keys)-1] {
		switch key {
		case "__proto__", "constructor", "prototype":
			return nil, fmt.Errorf("Invalid lot key")
		}
		child := map[string]any{}
		current[key] = child
		current = child
	}
	last := keys[len(keys)-1]
	switch last {
	case "__proto__", "constructor", "prototype":
		return nil, fmt.Errorf("Invalid lot key")
	}
	current[last] = value
	return root, nil
}

// jsSlice mirrors JavaScript String.prototype.slice on runes, including
// negative offsets.
func jsSlice(value string, start, end int) string {
	runes := []rune(value)
	length := len(runes)
	if start < 0 {
		start += length
	}
	if end < 0 {
		end += length
	}
	if start < 0 {
		start = 0
	}
	if end > length {
		end = length
	}
	if start >= end || start >= length {
		return ""
	}
	return string(runes[start:end])
}

// TrackSignature is the HMAC-SHA256 of the zipped track keyed by the lot.
func TrackSignature(lot, track string) string {
	mac := hmac.New(sha256.New, []byte(lot))
	mac.Write([]byte(track))
	return hex.EncodeToString(mac.Sum(nil))
}

// newUUID returns a random RFC 4122 version 4 UUID.
func newUUID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		panic(fmt.Errorf("v4: read random bytes: %w", err))
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16])
}
