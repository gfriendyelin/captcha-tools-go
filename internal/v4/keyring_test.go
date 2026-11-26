package v4

import (
	"fmt"
	"strings"
	"testing"
)

// widgetScript builds the decodeURI/key-table shape used by gcaptcha4.js.
func widgetScript(modulus, key string) string {
	raw := []byte(modulus)
	keyBytes := []byte(key)
	obfuscated := make([]byte, len(raw))
	for i := range raw {
		obfuscated[i] = raw[i] ^ keyBytes[i%len(keyBytes)]
	}
	var builder strings.Builder
	builder.WriteString(`var table=decodeURI("`)
	for _, value := range obfuscated {
		fmt.Fprintf(&builder, "%%%02X", value)
	}
	builder.WriteString(`");}}("` + key + `")}`)
	return builder.String()
}

func TestExtractModulus(t *testing.T) {
	modulus := "00" + strings.Repeat("a1", 127)
	script := widgetScript(modulus, "K3y!")
	got, err := ExtractModulus(script)
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.TrimPrefix(modulus, "00") {
		t.Fatalf("modulus mismatch: got %q", got)
	}
}

func TestExtractModulusRejectsUnknownScript(t *testing.T) {
	for _, script := range []string{"", "var a=1;", `decodeURI("abc")`} {
		if _, err := ExtractModulus(script); err == nil {
			t.Fatalf("script %q should fail", script)
		}
	}
}

func TestKeyringRecoverAndGet(t *testing.T) {
	keyring := NewKeyring()
	if keyring.Get("/v4/static/v1.9.9-x") != nil {
		t.Fatal("unexpected key before recovery")
	}
	script := widgetScript("00"+strings.Repeat("b2", 127), "key")
	if err := keyring.Recover("/v4/static/v1.9.9-x", script); err != nil {
		t.Fatal(err)
	}
	if keyring.Get("/v4/static/v1.9.9-x") == nil {
		t.Fatal("recovered key is missing")
	}
	if keyring.Get("/v4/static/other") != nil {
		t.Fatal("key leaked to another build")
	}
}
