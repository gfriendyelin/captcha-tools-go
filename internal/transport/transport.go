// Package transport builds the HTTP transport used by the upstream geetest
// clients. Proxy settings keep coming from the standard environment variables
// (HTTPS_PROXY, NO_PROXY, ...). Setting CAPTCHA_TLS_FINGERPRINT to a supported
// browser profile replaces the TLS ClientHello with that profile through uTLS;
// it is off by default so the standard library fingerprint stays in use.
package transport

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
)

// EnvFingerprint selects the TLS ClientHello profile. An unknown value keeps
// the Go default rather than failing the request.
const EnvFingerprint = "CAPTCHA_TLS_FINGERPRINT"

var profiles = map[string]utls.ClientHelloID{
	"chrome":  utls.HelloChrome_Auto,
	"firefox": utls.HelloFirefox_Auto,
	"safari":  utls.HelloSafari_Auto,
	"edge":    utls.HelloEdge_Auto,
	"random":  utls.HelloRandomizedALPN,
}

// New clones the default transport, so HTTP(S)_PROXY and NO_PROXY keep working,
// and optionally installs a browser-like TLS handshake.
func New() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = true
	profile, ok := profiles[strings.ToLower(strings.TrimSpace(os.Getenv(EnvFingerprint)))]
	if !ok {
		return transport
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		// HTTP/1.1 only: the standard transport would need its own h2 wiring to
		// speak HTTP/2 over a connection it did not dial itself.
		config := &utls.Config{ServerName: host, NextProtos: []string{"http/1.1"}}
		uconn := utls.UClient(conn, config, profile)
		if err := uconn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, err
		}
		return uconn, nil
	}
	transport.ForceAttemptHTTP2 = false
	return transport
}
