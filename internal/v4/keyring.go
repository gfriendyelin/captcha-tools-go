package v4

import (
	"crypto/rsa"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// The widget ships its RSA modulus inside a XOR-obfuscated table. Only data
// literals are parsed here; downloaded JavaScript is never executed.
var (
	decodeURIPattern = regexp.MustCompile(`decodeURI\("((?:[^"\\]|\\.)*)"\)`)
	keyTablePattern  = regexp.MustCompile(`\}\}\("(.+?)"\)\}`)
	hexRunPattern    = regexp.MustCompile(`[0-9a-fA-F]{200,}`)
)

// ExtractModulus recovers the RSA modulus from a gcaptcha4.js build script.
func ExtractModulus(script string) (string, error) {
	blobMatch := decodeURIPattern.FindStringSubmatch(script)
	keyMatch := keyTablePattern.FindStringSubmatch(script)
	if blobMatch == nil || keyMatch == nil {
		return "", fmt.Errorf("Widget has no recognized key table")
	}
	raw, err := decodeURIComponent(blobMatch[1])
	if err != nil {
		return "", fmt.Errorf("Widget has no recognized key table")
	}
	bytes := latin1Bytes(raw)
	keyBytes := latin1Bytes(keyMatch[1])
	if len(keyBytes) == 0 {
		return "", fmt.Errorf("Widget has no recognized key table")
	}
	for i := range bytes {
		bytes[i] ^= keyBytes[i%len(keyBytes)]
	}
	candidates := hexRunPattern.FindAllString(string(bytes), -1)
	if len(candidates) == 0 {
		return "", fmt.Errorf("Widget RSA modulus not found")
	}
	modulus := candidates[0]
	for _, candidate := range candidates[1:] {
		if len(candidate) > len(modulus) {
			modulus = candidate
		}
	}
	if len(modulus) > 1024 || len(modulus)%2 != 0 {
		return "", fmt.Errorf("Widget RSA modulus not found")
	}
	return strings.TrimPrefix(modulus, "00"), nil
}

// decodeURIComponent decodes %XX escapes and leaves literal characters as is.
func decodeURIComponent(input string) (string, error) {
	out := make([]byte, 0, len(input))
	for i := 0; i < len(input); i++ {
		if input[i] != '%' {
			out = append(out, input[i])
			continue
		}
		if i+2 >= len(input) {
			return "", fmt.Errorf("invalid escape")
		}
		high, okHigh := hexNibble(input[i+1])
		low, okLow := hexNibble(input[i+2])
		if !okHigh || !okLow {
			return "", fmt.Errorf("invalid escape")
		}
		out = append(out, high<<4|low)
		i += 2
	}
	return string(out), nil
}

func hexNibble(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}

// latin1Bytes truncates every rune to its low byte, matching Buffer latin1.
func latin1Bytes(value string) []byte {
	out := make([]byte, 0, len(value))
	for _, r := range value {
		out = append(out, byte(r))
	}
	return out
}

// Keyring caches the RSA public key of each static build.
type Keyring struct {
	mu   sync.RWMutex
	keys map[string]*rsa.PublicKey
}

func NewKeyring() *Keyring { return &Keyring{keys: map[string]*rsa.PublicKey{}} }

// Get returns the recovered key for a build, or nil when the builtin applies.
func (k *Keyring) Get(build string) *rsa.PublicKey {
	if k == nil {
		return nil
	}
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.keys[build]
}

// Recover extracts and stores the modulus embedded in a build script.
func (k *Keyring) Recover(build, script string) error {
	modulus, err := ExtractModulus(script)
	if err != nil {
		return err
	}
	key, err := parseModulus(modulus)
	if err != nil {
		return err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.keys[build] = key
	return nil
}
