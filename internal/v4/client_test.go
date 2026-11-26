package v4

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const testStaticPath = "/v4/static/v1.9.9-11a9a5"

func newTestClient(t *testing.T, load http.HandlerFunc, verify http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/load", load)
	mux.HandleFunc("/verify", verify)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client, err := NewClient(Config{
		LoadURL:    server.URL + "/load",
		VerifyURL:  server.URL + "/verify",
		StaticBase: server.URL + "/",
		Timeout:    5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client, server
}

func aiChallenge(pt string) string {
	return fmt.Sprintf(`geetest_1({"status":"success","data":{
		"captcha_type":"ai",
		"lot_number":"0123456789abcdef0123456789abcdef",
		"static_path":%q,
		"pt":%s,
		"pow_detail":{"version":"1","bits":4,"hashfunc":"md5"},
		"payload":"opaque-payload",
		"process_token":"opaque-token",
		"payload_protocol":1,
		"guard":true
	}})`, testStaticPath, pt)
}

func TestOpenAndSubmitSilent(t *testing.T) {
	var verifyQuery url.Values
	client, _ := newTestClient(t,
		func(w http.ResponseWriter, r *http.Request) {
			query := r.URL.Query()
			if query.Get("callback") == "" || query.Get("captcha_id") != "site-1" {
				t.Errorf("unexpected load query %v", query)
			}
			if query.Get("challenge") == "" || query.Get("client_type") != "web" || query.Get("lang") != "zh" {
				t.Errorf("missing load parameters %v", query)
			}
			fmt.Fprint(w, aiChallenge("0"))
		},
		func(w http.ResponseWriter, r *http.Request) {
			verifyQuery = r.URL.Query()
			fmt.Fprint(w, `geetest_2({"status":"success","data":{"result":"success","seccode":{"captcha_id":"site-1","lot_number":"0123456789abcdef0123456789abcdef","pass_token":"token","gen_time":"1","captcha_output":"output"}}})`)
		})

	session, err := client.Open(context.Background(), Challenge{CaptchaID: "site-1"})
	if err != nil {
		t.Fatal(err)
	}
	if session.Kind() != "silent" {
		t.Fatalf("kind %q", session.Kind())
	}
	raw, passed, err := session.Submit(context.Background(), Answer{Kind: "silent"})
	if err != nil {
		t.Fatal(err)
	}
	if !passed {
		t.Fatal("expected a passing proof")
	}
	if !strings.Contains(string(raw), `"seccode"`) {
		t.Fatalf("raw response lost the seccode: %s", raw)
	}
	if verifyQuery.Get("risk_type") != "ai" || verifyQuery.Get("pt") != "0" ||
		verifyQuery.Get("payload") != "opaque-payload" || verifyQuery.Get("process_token") != "opaque-token" ||
		verifyQuery.Get("payload_protocol") != "1" || verifyQuery.Get("captcha_id") != "site-1" {
		t.Fatalf("unexpected verify query %v", verifyQuery)
	}
	if verifyQuery.Get("td") != "" {
		t.Fatal("silent answers must not send a track")
	}
	wire, err := base64.URLEncoding.DecodeString(verifyQuery.Get("w"))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(wire, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["biht"] != "1426265548" || payload["LwjO"] != "dLVB" {
		t.Fatalf("patrol fields are missing: %#v", payload)
	}
	if payload["lot_number"] != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("lot number missing: %#v", payload)
	}
	if payload["pow_msg"] == nil || payload["pow_sign"] == nil {
		t.Fatalf("proof of work missing: %#v", payload)
	}
	if _, ok := payload["roe"].(map[string]any); !ok {
		t.Fatalf("guard fields missing: %#v", payload["roe"])
	}
}

func TestSubmitRecoversRotatedKey(t *testing.T) {
	script := widgetScript("00"+strings.Repeat("c3", 127), "K3y")
	staticMux := http.NewServeMux()
	staticMux.HandleFunc(testStaticPath+"/js/gcaptcha4.js", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, script)
	})
	staticServer := httptest.NewServer(staticMux)
	t.Cleanup(staticServer.Close)

	client, _ := newTestClient(t,
		func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, aiChallenge("1")) },
		func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `geetest_3({"status":"error","code":-50002})`)
		})
	client.static, _ = url.Parse(DefaultStaticBase)
	client.doer = HTTPDoerFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Hostname() == "static.geetest.com" {
			rewritten := request.Clone(request.Context())
			rewritten.URL.Scheme = "http"
			rewritten.URL.Host = strings.TrimPrefix(staticServer.URL, "http://")
			return http.DefaultClient.Do(rewritten)
		}
		return http.DefaultClient.Do(request)
	})

	session, err := client.Open(context.Background(), Challenge{CaptchaID: "site-1"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = session.Submit(context.Background(), Answer{Kind: "silent"})
	if !errors.Is(err, ErrProtocolKeyUpdated) {
		t.Fatalf("expected a key rotation error, got %v", err)
	}
	if client.keyring.Get(testStaticPath) == nil {
		t.Fatal("the rotated key was not stored")
	}
}

// TestOpenAndSubmitMode2 drives the whole client path with pt=2: load, build
// the answer, encrypt with SM4/SM2 and submit. It needs no real captcha_id.
func TestOpenAndSubmitMode2(t *testing.T) {
	var verifyQuery url.Values
	client, _ := newTestClient(t,
		func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, aiChallenge("2")) },
		func(w http.ResponseWriter, r *http.Request) {
			verifyQuery = r.URL.Query()
			fmt.Fprint(w, `geetest_2({"status":"success","data":{"result":"success","seccode":{"captcha_id":"site-1","lot_number":"0123456789abcdef0123456789abcdef","pass_token":"token","gen_time":"1","captcha_output":"output"}}})`)
		})

	session, err := client.Open(context.Background(), Challenge{CaptchaID: "site-1", ClientType: "web", Lang: "zh"})
	if err != nil {
		t.Fatal(err)
	}
	if session.PT() != 2 {
		t.Fatalf("pt=%d, want 2", session.PT())
	}
	_, passed, err := session.Submit(context.Background(), Answer{Kind: "silent"})
	if err != nil {
		t.Fatal(err)
	}
	if !passed {
		t.Fatal("expected a passing proof")
	}
	if verifyQuery.Get("pt") != "2" {
		t.Fatalf("verify pt=%q, want 2", verifyQuery.Get("pt"))
	}
	if verifyQuery.Get("td") != "" {
		t.Fatal("silent answers must not send a track")
	}
	wire := verifyQuery.Get("w")
	if !isHex(wire) {
		t.Fatalf("pt=2 wire is not hex: %q", wire)
	}
	// hex(SM4-CBC) followed by x||y||C2||C3 (112 bytes) without the 0x04 prefix.
	if (len(wire)-224)%32 != 0 || len(wire) < 256 {
		t.Fatalf("unexpected pt=2 wire length %d", len(wire))
	}
}

func isHex(value string) bool {
	if value == "" || len(value)%2 != 0 {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}

func TestPatrolSupportsLiveBuild(t *testing.T) {
	client, err := NewClient(Config{})
	if err != nil {
		t.Fatal(err)
	}
	patrol, err := client.patrol("/v4/static/v1.9.10-be7fe1")
	if err != nil {
		t.Fatalf("live build config: %v", err)
	}
	if patrol.Biht != "1426265548" || patrol.LibKey != "BHGy" || patrol.LibVal != "z1RX" ||
		patrol.AboKey != "n[23:26]+n[2:5]" || patrol.AboVal != "n[2:7]" {
		t.Fatalf("unexpected patrol %+v", patrol)
	}
}

func TestOpenRejectsUnknownBuild(t *testing.T) {
	client, _ := newTestClient(t,
		func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `geetest_1({"status":"success","data":{"captcha_type":"ai","lot_number":"0123456789abcdef0123456789abcdef","static_path":"/v4/static/v9.9.9-zzz","pt":0,"pow_detail":{}}})`)
		},
		func(w http.ResponseWriter, _ *http.Request) {})
	_, err := client.Open(context.Background(), Challenge{CaptchaID: "site-1"})
	var protocolErr *Error
	if !errors.As(err, &protocolErr) || protocolErr.Code != "unsupported_build" {
		t.Fatalf("expected unsupported_build, got %v", err)
	}
}

func TestOpenRejectsInvalidChallenge(t *testing.T) {
	client, _ := newTestClient(t,
		func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, `geetest_1({"status":"success","data":{"captcha_type":"ai","lot_number":"short","static_path":"`+testStaticPath+`","pt":0}})`)
		},
		func(w http.ResponseWriter, _ *http.Request) {})
	_, err := client.Open(context.Background(), Challenge{CaptchaID: "site-1"})
	var protocolErr *Error
	if !errors.As(err, &protocolErr) || protocolErr.Code != "invalid_v4_challenge" {
		t.Fatalf("expected invalid_v4_challenge, got %v", err)
	}
}
