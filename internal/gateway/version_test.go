package gateway

import (
	"net/http/httptest"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	tests := []struct {
		query string
		want  string
		ok    bool
	}{
		{"version=3&gt=x&challenge=y", "v3", true},
		{"version=v3&gt=x&challenge=y", "v3", true},
		{"version=4&captcha_id=x", "v4", true},
		{"version=v4&gt=x", "v4", true},
		{"captcha_id=x", "v4", true},
		{"gt=x&challenge=y", "v3", true},
		{"gt=x", "", false},
		{"version=5&gt=x", "", false},
		{"", "", false},
	}
	for _, test := range tests {
		request := httptest.NewRequest("GET", "/pass_uni?"+test.query, nil)
		got, err := resolveVersion(request)
		if (err == nil) != test.ok || got != test.want {
			t.Fatalf("resolveVersion(%q) = %q, %v; want %q, ok=%v", test.query, got, err, test.want, test.ok)
		}
	}
}
