package geetest

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testAESKey = "0123456789abcdef"
)

func testEncKey() string { return strings.Repeat("ab", 128) }

func TestMockFullProtocolChain(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	capturedW := make(map[string]string)
	cookieSeen := false
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.Header.Get("User-Agent") != browserUserAgent {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		writeJSONP := func(value any) {
			body, _ := json.Marshal(value)
			callback := r.URL.Query().Get("callback")
			if callback == "" {
				fmt.Fprintf(w, "(%s)", body)
			} else {
				fmt.Fprintf(w, "%s(%s)", callback, body)
			}
		}
		switch r.URL.Path {
		case "/gettype.php":
			if r.URL.Query().Get("callback") == "" {
				writeJSONP(map[string]any{
					"status": "success",
					"data":   map[string]any{"type": "fullpage", "mark": "便笺", "cc": 99},
				})
				return
			}
			writeJSONP(map[string]any{"status": "success", "data": map[string]any{}})
		case "/init.php":
			capturedW[r.URL.Path] = r.URL.Query().Get("w")
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc", Path: "/"})
			writeJSONP(map[string]any{
				"status": "success",
				"data":   map[string]any{"c": []int{1, 2, 3, 4, 5}, "s": "4142"},
			})
		case "/pre-ajax.php":
			capturedW[r.URL.Path] = r.URL.Query().Get("w")
			writeJSONP(map[string]any{"status": "success", "data": map[string]any{"result": "success"}})
		case "/picture.php":
			if got := r.URL.Query().Get("width"); got != "100%" {
				t.Errorf("width = %q", got)
			}
			for key, want := range map[string]string{
				"is_next": "true", "type": "click", "https": "true", "offline": "false",
				"product": "float", "api_server": "api.geevisit.com", "isPC": "true", "autoReset": "true",
			} {
				if got := r.URL.Query().Get(key); got != want {
					t.Errorf("%s = %q, want %q", key, got, want)
				}
			}
			writeJSONP(map[string]any{
				"status": "success",
				"data": map[string]any{
					"pic": "/image/pic.jpg", "pic_type": "nine",
					"resource_servers": []string{strings.TrimPrefix(server.URL, "http://") + "/"},
				},
			})
		case "/image/pic.jpg":
			_, _ = w.Write([]byte{0, 1, 2, 255})
		case "/verify.php":
			capturedW[r.URL.Path] = r.URL.Query().Get("w")
			cookie, err := r.Cookie("session")
			cookieSeen = err == nil && cookie.Value == "abc"
			if r.URL.Query().Get("callback") != "" {
				t.Error("verify unexpectedly has callback")
			}
			writeJSONP(map[string]any{
				"status": "success",
				"data":   map[string]any{"result": "success", "validate": "abc"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var slept time.Duration
	client, err := NewClient("gt-value", "challenge-value", Config{
		Timeout: time.Second,
		Now:     func() time.Time { return time.UnixMilli(1724572150164) },
		Float64: func() float64 { return 0.5 },
		Sleep: func(_ context.Context, d time.Duration) error {
			slept = d
			return nil
		},
		GetTypeURL:    server.URL + "/gettype.php",
		GetURL:        server.URL + "/init.php",
		AjaxURL:       server.URL + "/pre-ajax.php",
		GetPictureURL: server.URL + "/picture.php",
		VerifyURL:     server.URL + "/verify.php",
		PictureScheme: "http",
		AESKey:        testAESKey,
		EncKey:        testEncKey(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	picture, err := client.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if slept != 500*time.Millisecond {
		t.Fatalf("prepare sleep = %v", slept)
	}
	if picture.PicPath != "/image/pic.jpg" || picture.Name != "pic.jpg" || picture.PicType != "nine" {
		t.Fatalf("picture = %+v", picture)
	}
	if picture.URL != server.URL+"/image/pic.jpg" {
		t.Fatalf("picture URL = %q", picture.URL)
	}
	if len(picture.Content) != 4 || picture.Content[3] != 255 {
		t.Fatalf("picture bytes = %v", picture.Content)
	}

	verify, err := client.Verify(context.Background(), []string{"1_2", "300_400"})
	if err != nil {
		t.Fatal(err)
	}
	if !verify.Passed() {
		t.Fatal("verify.Passed() = false")
	}
	wantRaw := "{\"data\":{\"result\":\"success\",\"validate\":\"abc\"},\"status\":\"success\"}"
	if string(verify.Raw) != wantRaw {
		t.Fatalf("verify raw = %s", verify.Raw)
	}
	if !cookieSeen {
		t.Fatal("verify did not receive cookie from init.php")
	}

	mu.Lock()
	gotPaths := append([]string(nil), paths...)
	mu.Unlock()
	wantPaths := []string{
		"/gettype.php", "/gettype.php", "/init.php", "/pre-ajax.php",
		"/picture.php", "/image/pic.jpg", "/verify.php",
	}
	if len(gotPaths) != len(wantPaths) {
		t.Fatalf("request count = %d (%v), want 7", len(gotPaths), gotPaths)
	}
	for i := range wantPaths {
		if gotPaths[i] != wantPaths[i] {
			t.Fatalf("request %d = %q, want %q", i, gotPaths[i], wantPaths[i])
		}
	}

	getCS := decryptWForTest(t, capturedW["/init.php"], true)
	for _, fragment := range []string{
		"\"gt\":\"gt-value\"", "\"product\":\"embed\"", "\"type\":\"fullpage\"",
		"\"mark\":\"\\u4fbf\\u7b3a\"", "\"cc\":16", "\"ww\":true",
	} {
		if !strings.Contains(getCS, fragment) {
			t.Errorf("get_c_s payload lacks %q: %s", fragment, getCS)
		}
	}
	if !strings.HasPrefix(getCS, "{\"gt\":\"gt-value\",\"challenge\":\"challenge-value\",\"offline\":false") {
		t.Errorf("get_c_s key order changed: %s", getCS)
	}

	ajax := decryptWForTest(t, capturedW["/pre-ajax.php"], false)
	for _, fragment := range []string{
		"\"type\":\"fullpage\"", "\"captcha_token\":\"2064329542\"",
		"\"rp\":\"" + md5Hex("gt-value"+"challenge-value"+"4142") + "\"",
	} {
		if !strings.Contains(ajax, fragment) {
			t.Errorf("ajax payload lacks %q: %s", fragment, ajax)
		}
	}
	if !strings.Contains(ajax, "\"tt\":\"") {
		t.Errorf("ajax has no encoded trajectory: %s", ajax)
	}

	verifyPayload := decryptWForTest(t, capturedW["/verify.php"], true)
	for _, fragment := range []string{
		"\"a\":\"1_2,300_400\"", "\"pic\":\"/image/pic.jpg\"",
		"\"rp\":\"" + md5Hex("gt-value"+"challenge-value"+"1600") + "\"",
		"\"$_FB\":false", "\"h9s9\":\"1816378497\"",
	} {
		if !strings.Contains(verifyPayload, fragment) {
			t.Errorf("verify payload lacks %q: %s", fragment, verifyPayload)
		}
	}
}

func decryptWForTest(t *testing.T, w string, rsaSuffix bool) string {
	t.Helper()
	if rsaSuffix {
		if !strings.HasSuffix(w, testEncKey()) {
			t.Fatalf("w has no RSA suffix")
		}
		w = strings.TrimSuffix(w, testEncKey())
	}
	ciphertext, err := decodeCustomForTest(w)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher([]byte(testAESKey))
	if err != nil {
		t.Fatal(err)
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		t.Fatalf("ciphertext length = %d", len(ciphertext))
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, []byte("0000000000000000")).CryptBlocks(plain, ciphertext)
	padding := int(plain[len(plain)-1])
	if padding < 1 || padding > aes.BlockSize || padding > len(plain) {
		t.Fatalf("invalid padding %d", padding)
	}
	for _, b := range plain[len(plain)-padding:] {
		if int(b) != padding {
			t.Fatal("invalid PKCS7 bytes")
		}
	}
	return string(plain[:len(plain)-padding])
}

func decodeCustomForTest(encoded string) ([]byte, error) {
	encoded = strings.TrimRight(encoded, ".")
	var output []byte
	for i := 0; i < len(encoded); i += 4 {
		n := len(encoded) - i
		if n > 4 {
			n = 4
		}
		var value uint32
		for j := 0; j < n; j++ {
			index := strings.IndexByte(encodeAlphabet, encoded[i+j])
			if index < 0 {
				return nil, fmt.Errorf("invalid encoded character %q", encoded[i+j])
			}
			v := uint32(index)
			for bit := 0; bit < 24; bit++ {
				if (encodeMasks[j]>>bit)&1 != 0 {
					value |= (v & 1) << bit
					v >>= 1
				}
			}
			if v != 0 {
				return nil, fmt.Errorf("invalid six-bit index %d", index)
			}
		}
		output = append(output, byte(value>>16))
		if n >= 3 {
			output = append(output, byte(value>>8))
		}
		if n == 4 {
			output = append(output, byte(value))
		}
	}
	return output, nil
}

type trackingBody struct {
	io.Reader
	closed bool
}

func (b *trackingBody) Close() error {
	b.closed = true
	return nil
}

func TestResponseLimitAndBodyClose(t *testing.T) {
	body := &trackingBody{Reader: strings.NewReader("12345")}
	client, err := NewClient("gt", "challenge", Config{
		Doer: HTTPDoerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: body}, nil
		}),
		MaxResponseSize: 4,
		AESKey:          testAESKey,
		EncKey:          testEncKey(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, err = client.get(context.Background(), "http://example.test/data")
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Fatalf("error = %v", err)
	}
	if !body.closed {
		t.Fatal("response body was not closed")
	}
}

func TestContextTimeout(t *testing.T) {
	client, err := NewClient("gt", "challenge", Config{
		Doer: HTTPDoerFunc(func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			return nil, r.Context().Err()
		}),
		Timeout: 20 * time.Millisecond,
		AESKey:  testAESKey,
		EncKey:  testEncKey(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, err = client.get(context.Background(), "http://example.test/slow")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
}

func TestVerifyRequiresPrepare(t *testing.T) {
	client, err := NewClient("gt", "challenge", Config{
		Doer: HTTPDoerFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("unexpected HTTP request")
			return nil, nil
		}),
		AESKey: testAESKey,
		EncKey: testEncKey(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Verify(context.Background(), nil); err == nil {
		t.Fatal("expected state error")
	}
}

func TestSafePictureName(t *testing.T) {
	for input, want := range map[string]string{
		"/image/pic.jpg": "pic.jpg",
		"..\\evil.jpg":   "evil.jpg",
		"plain.png":      "plain.png",
	} {
		got, err := safePictureName(input)
		if err != nil || got != want {
			t.Errorf("safePictureName(%q) = %q, %v", input, got, err)
		}
	}
	for _, input := range []string{"", ".", "..", "/"} {
		if _, err := safePictureName(input); err == nil {
			t.Errorf("safePictureName(%q) should fail", input)
		}
	}
}
