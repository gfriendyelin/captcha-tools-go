package gateway

import (
	"fmt"
	"net/http"
	"strings"
)

func resolveVersion(r *http.Request) (string, error) {
	query := r.URL.Query()
	if raw := strings.ToLower(strings.TrimSpace(query.Get("version"))); raw != "" {
		switch raw {
		case "3", "v3":
			return "v3", nil
		case "4", "v4":
			return "v4", nil
		default:
			return "", fmt.Errorf("version must be 3 or 4")
		}
	}
	if query.Get("captcha_id") != "" {
		return "v4", nil
	}
	if query.Get("challenge") != "" {
		return "v3", nil
	}
	return "", fmt.Errorf("cannot infer version; pass version=3 or version=4")
}
