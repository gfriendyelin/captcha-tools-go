package geetest

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	mathrand "math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultGetTypeURL    = "https://api.geetest.com/gettype.php"
	DefaultGetURL        = "https://api.geetest.com/get.php"
	DefaultAjaxURL       = "https://api.geetest.com/ajax.php"
	DefaultGetPictureURL = "https://api.geevisit.com/get.php"
	DefaultVerifyURL     = "https://api.geevisit.com/ajax.php"
	DefaultTimeout       = 5 * time.Second
	DefaultMaxBodyBytes  = int64(16 << 20)
	browserUserAgent     = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
)

var ErrResponseTooLarge = errors.New("geetest: response body exceeds configured limit")

// HTTPDoer is implemented by *http.Client. A Client still manages its own
// cookie jar when a custom Doer is supplied.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// HTTPDoerFunc adapts a function for tests or custom transports.
type HTTPDoerFunc func(*http.Request) (*http.Response, error)

func (f HTTPDoerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

// Config controls one Crack-equivalent client. Zero URL values select the
// original production endpoints. AESKey and EncKey are deterministic test
// injection points; production callers should leave both empty.
type Config struct {
	Doer            HTTPDoer
	Timeout         time.Duration // zero: 5s; negative: no additional timeout
	MaxResponseSize int64         // zero: 16 MiB
	Now             func() time.Time
	Float64         func() float64
	Random          io.Reader // RSA PKCS#1 v1.5 randomness
	Sleep           func(context.Context, time.Duration) error

	GetTypeURL    string
	GetURL        string
	AjaxURL       string
	GetPictureURL string
	VerifyURL     string
	PictureScheme string // zero: https; tests may use http

	AESKey string
	EncKey string
}

// Picture contains the downloaded bytes and both path forms. PicPath is the
// unmodified data.pic value; Name is a separator-safe base name. The main
// service remains responsible for its final filesystem policy.
type Picture struct {
	Content []byte
	PicPath string
	Name    string
	PicType string
	URL     string
}

// VerifyResponse preserves the exact inner JSON returned by Crack.verify and
// also exposes its decoded object for service-side result handling.
type VerifyResponse struct {
	Raw    json.RawMessage
	Object map[string]any
}

// Passed mirrors main.py's key-presence check, not a status/result check.
func (r *VerifyResponse) Passed() bool {
	if r == nil {
		return false
	}
	data, ok := r.Object["data"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = data["validate"]
	return ok
}

// Client holds one AES key, RSA wrapping, c/s state, trajectory and cookie jar.
// As with the Python object, one Client is intended for one request attempt,
// not concurrent Prepare/Verify calls.
type Client struct {
	gt        string
	challenge string

	doer      HTTPDoer
	transport *http.Transport
	jar       http.CookieJar
	timeout   time.Duration
	maxBody   int64

	now     func() time.Time
	float64 func() float64
	sleep   func(context.Context, time.Duration) error

	getTypeURL    string
	getURL        string
	ajaxURL       string
	getPictureURL string
	verifyURL     string
	pictureScheme string

	aesKey string
	encKey string
	mouse  []mouseEvent
	c      []int64
	s      string
	hasCS  bool
	pic    string
	hasPic bool
}

func NewClient(gt, challenge string, cfg Config) (*Client, error) {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	maxBody := cfg.MaxResponseSize
	if maxBody == 0 {
		maxBody = DefaultMaxBodyBytes
	}
	if maxBody < 0 {
		return nil, fmt.Errorf("geetest: MaxResponseSize must be positive")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	float64Fn := cfg.Float64
	if float64Fn == nil {
		float64Fn = mathrand.Float64
	}
	sleep := cfg.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("geetest: create cookie jar: %w", err)
	}
	c := &Client{
		gt: gt, challenge: challenge, jar: jar,
		timeout: timeout, maxBody: maxBody,
		now: now, float64: float64Fn, sleep: sleep,
		getTypeURL:    defaultString(cfg.GetTypeURL, DefaultGetTypeURL),
		getURL:        defaultString(cfg.GetURL, DefaultGetURL),
		ajaxURL:       defaultString(cfg.AjaxURL, DefaultAjaxURL),
		getPictureURL: defaultString(cfg.GetPictureURL, DefaultGetPictureURL),
		verifyURL:     defaultString(cfg.VerifyURL, DefaultVerifyURL),
		pictureScheme: defaultString(cfg.PictureScheme, "https"),
	}
	if cfg.Doer == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.ForceAttemptHTTP2 = true
		c.transport = transport
		c.doer = &http.Client{Transport: transport}
	} else {
		c.doer = cfg.Doer
	}

	c.aesKey = cfg.AESKey
	if c.aesKey == "" {
		c.aesKey, err = generateAESKey(float64Fn)
		if err != nil {
			c.Close()
			return nil, err
		}
	}
	if len(c.aesKey) != 16 {
		c.Close()
		return nil, fmt.Errorf("geetest: configured AES key must be 16 bytes")
	}
	c.encKey = cfg.EncKey
	if c.encKey == "" {
		publicKey, parseErr := parsePublicKey()
		if parseErr != nil {
			c.Close()
			return nil, parseErr
		}
		c.encKey, err = encryptAESKey(publicKey, cfg.Random, c.aesKey)
		if err != nil {
			c.Close()
			return nil, err
		}
	}
	if decoded, err := hex.DecodeString(c.encKey); err != nil || len(decoded) != 128 {
		c.Close()
		return nil, fmt.Errorf("geetest: configured RSA key must be 128 bytes of hex")
	}
	c.mouse, err = defaultMousePath()
	if err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// Close releases idle connections. It is safe to call more than once.
func (c *Client) Close() {
	if c == nil {
		return
	}
	if c.transport != nil {
		c.transport.CloseIdleConnections()
	}
	if closer, ok := c.doer.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

// Prepare executes gettype, get_type, get_c_s, the 0.4-0.6 second delay,
// pre-ajax, picture metadata and picture download: six HTTP requests total.
func (c *Client) Prepare(ctx context.Context) (*Picture, error) {
	if _, err := c.getType(ctx, true); err != nil {
		return nil, fmt.Errorf("geetest: initial gettype: %w", err)
	}
	if err := c.getCS(ctx); err != nil {
		return nil, fmt.Errorf("geetest: get c/s: %w", err)
	}
	r := c.float64()
	if r < 0 || r >= 1 || math.IsNaN(r) || math.IsInf(r, 0) {
		return nil, fmt.Errorf("geetest: random source returned %v for delay", r)
	}
	delay := 0.4 + (0.6-0.4)*r
	if err := c.sleep(ctx, time.Duration(delay*float64(time.Second))); err != nil {
		return nil, fmt.Errorf("geetest: prepare delay: %w", err)
	}
	if err := c.preAjax(ctx); err != nil {
		return nil, fmt.Errorf("geetest: pre-ajax: %w", err)
	}
	picture, err := c.getPicture(ctx)
	if err != nil {
		return nil, fmt.Errorf("geetest: get picture: %w", err)
	}
	return picture, nil
}

func (c *Client) getType(ctx context.Context, withCallback bool) (string, error) {
	rawURL := c.getTypeURL + "?gt=" + c.gt
	if withCallback {
		rawURL += "&callback=" + c.callback()
	}
	body, err := c.get(ctx, rawURL)
	return string(body), err
}

func (c *Client) getCS(ctx context.Context) error {
	getTypeBody, err := c.getType(ctx, false)
	if err != nil {
		return err
	}
	getTypeData, err := parseDataObject(getTypeBody, 1)
	if err != nil {
		return fmt.Errorf("get_type response: %w", err)
	}
	payload, err := marshalPythonJSON(buildGetCSPayload(c.gt, c.challenge, getTypeData))
	if err != nil {
		return err
	}
	ciphertext, err := aesEncrypt([]byte(c.aesKey), payload)
	if err != nil {
		return err
	}
	params := c.commonParams(true)
	params = append(params, queryParam{"w", Encode(ciphertext) + c.encKey})
	rawURL, err := addQuery(c.getURL, params)
	if err != nil {
		return err
	}
	body, err := c.get(ctx, rawURL)
	if err != nil {
		return err
	}
	data, err := parseDataObject(string(body), 22)
	if err != nil {
		return err
	}
	cValue, ok := data.get("c")
	if !ok {
		return fmt.Errorf("geetest: get_c_s data has no c")
	}
	cArray, ok := cValue.([]any)
	if !ok {
		return fmt.Errorf("geetest: c is %T, not an array", cValue)
	}
	parsedC := make([]int64, len(cArray))
	for i, value := range cArray {
		parsedC[i], err = jsonInt64(value)
		if err != nil {
			return fmt.Errorf("geetest: c[%d]: %w", i, err)
		}
	}
	if len(parsedC) < 5 {
		return fmt.Errorf("geetest: c has only %d values", len(parsedC))
	}
	sValue, ok := data.get("s")
	if !ok {
		return fmt.Errorf("geetest: get_c_s data has no s")
	}
	s, ok := sValue.(string)
	if !ok {
		return fmt.Errorf("geetest: s is %T, not a string", sValue)
	}
	c.c, c.s, c.hasCS = parsedC, s, true
	return nil
}

func (c *Client) preAjax(ctx context.Context) error {
	if !c.hasCS {
		return fmt.Errorf("geetest: c/s not initialized")
	}
	tt, err := encodeMousePath(ajaxMousePath(), c.c, c.s)
	if err != nil {
		return err
	}
	tt, err = transformAjaxMouse(tt, c.c, c.s)
	if err != nil {
		return err
	}
	payloadObject := buildAjaxPayload(tt, md5Hex(c.gt+c.challenge+c.s))
	payload, err := marshalPythonJSON(payloadObject)
	if err != nil {
		return err
	}
	ciphertext, err := aesEncrypt([]byte(c.aesKey), payload)
	if err != nil {
		return err
	}
	params := c.commonParams(true)
	params = append(params, queryParam{"w", Encode(ciphertext)})
	rawURL, err := addQuery(c.ajaxURL, params)
	if err != nil {
		return err
	}
	body, err := c.get(ctx, rawURL)
	if err != nil {
		return err
	}
	_, err = parseDataObject(string(body), 22)
	return err
}

func (c *Client) getPicture(ctx context.Context) (*Picture, error) {
	params := []queryParam{
		{"is_next", "true"},
		{"type", "click"},
		{"gt", c.gt},
		{"challenge", c.challenge},
		{"lang", "zh-cn"},
		{"https", "true"},
		{"protocol", "https://"},
		{"offline", "false"},
		{"product", "float"},
		{"api_server", "api.geevisit.com"},
		{"isPC", "true"},
		{"autoReset", "true"},
		{"width", "100%"},
		{"callback", c.callback()},
	}
	rawURL, err := addQuery(c.getPictureURL, params)
	if err != nil {
		return nil, err
	}
	body, err := c.get(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	data, err := parseDataObject(string(body), 22)
	if err != nil {
		return nil, err
	}
	pic, err := requiredString(data, "pic")
	if err != nil {
		return nil, err
	}
	c.pic, c.hasPic = pic, true
	picType, err := requiredString(data, "pic_type")
	if err != nil {
		return nil, err
	}
	serversValue, ok := data.get("resource_servers")
	if !ok {
		return nil, fmt.Errorf("geetest: picture data has no resource_servers")
	}
	servers, ok := serversValue.([]any)
	if !ok || len(servers) == 0 {
		return nil, fmt.Errorf("geetest: resource_servers is empty or not an array")
	}
	server, ok := servers[0].(string)
	if !ok {
		return nil, fmt.Errorf("geetest: resource_servers[0] is not a string")
	}
	serverRunes := []rune(server)
	if len(serverRunes) == 0 {
		return nil, fmt.Errorf("geetest: resource_servers[0] is empty")
	}
	pictureURL := c.pictureScheme + "://" + string(serverRunes[:len(serverRunes)-1]) + pic
	pictureBody, err := c.get(ctx, pictureURL)
	if err != nil {
		return nil, err
	}
	if strings.Contains(pictureURL, "80/2023-12-04T12/icon") {
		picType = "icon1"
	}
	name, err := safePictureName(pic)
	if err != nil {
		return nil, err
	}
	return &Picture{
		Content: pictureBody, PicPath: pic, Name: name, PicType: picType, URL: pictureURL,
	}, nil
}

// Verify performs the seventh request. points are joined verbatim with commas.
func (c *Client) Verify(ctx context.Context, points []string) (*VerifyResponse, error) {
	if !c.hasCS || !c.hasPic {
		return nil, fmt.Errorf("geetest: Prepare must succeed before Verify")
	}
	tt, err := encodeMousePath(c.mouse, c.c, c.s)
	if err != nil {
		return nil, err
	}
	payloadObject := buildVerifyPayload(c.gt, c.challenge, strings.Join(points, ","), c.pic, tt)
	payload, err := marshalPythonJSON(payloadObject)
	if err != nil {
		return nil, err
	}
	ciphertext, err := aesEncrypt([]byte(c.aesKey), payload)
	if err != nil {
		return nil, err
	}
	params := c.commonParams(false)
	params = append(params, queryParam{"w", Encode(ciphertext) + c.encKey})
	rawURL, err := addQuery(c.verifyURL, params)
	if err != nil {
		return nil, err
	}
	body, err := c.get(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	inner, err := stripJSONP(string(body), 1)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(strings.NewReader(inner))
	dec.UseNumber()
	var decoded map[string]any
	if err := dec.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("geetest: decode verify response: %w", err)
	}
	if decoded == nil {
		return nil, fmt.Errorf("geetest: verify response is not an object")
	}
	return &VerifyResponse{Raw: json.RawMessage([]byte(inner)), Object: decoded}, nil
}

func requiredString(data object, key string) (string, error) {
	value, ok := data.get(key)
	if !ok {
		return "", fmt.Errorf("geetest: response data has no %s", key)
	}
	s, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("geetest: response %s is %T, not a string", key, value)
	}
	return s, nil
}

func safePictureName(pic string) (string, error) {
	normalized := strings.ReplaceAll(pic, "\\", "/")
	name := path.Base(normalized)
	if name == "" || name == "." || name == ".." || name == "/" ||
		strings.ContainsAny(name, "/\\\x00") {
		return "", fmt.Errorf("geetest: unsafe picture path %q", pic)
	}
	return name, nil
}

type queryParam struct {
	key   string
	value string
}

func (c *Client) commonParams(callback bool) []queryParam {
	params := []queryParam{
		{"gt", c.gt},
		{"challenge", c.challenge},
		{"lang", "zh-cn"},
		{"pt", "0"},
		{"client_type", "web"},
	}
	if callback {
		params = append(params, queryParam{"callback", c.callback()})
	}
	return params
}

func (c *Client) callback() string {
	now := c.now()
	seconds := float64(now.Unix()) + float64(now.Nanosecond())/1e9
	milliseconds := math.RoundToEven(seconds * 1000)
	return "geetest_" + strconv.FormatInt(int64(milliseconds), 10)
}

func addQuery(rawURL string, params []queryParam) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("geetest: parse endpoint: %w", err)
	}
	encoded := make([]string, 0, len(params)+1)
	if u.RawQuery != "" {
		encoded = append(encoded, u.RawQuery)
	}
	for _, p := range params {
		encoded = append(encoded, url.QueryEscape(p.key)+"="+url.QueryEscape(p.value))
	}
	u.RawQuery = strings.Join(encoded, "&")
	return u.String(), nil
}

func (c *Client) get(ctx context.Context, rawURL string) (body []byte, err error) {
	if ctx == nil {
		return nil, fmt.Errorf("geetest: nil context")
	}
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", browserUserAgent)
	for _, cookie := range c.jar.Cookies(req.URL) {
		req.AddCookie(cookie)
	}
	resp, err := c.doer.Do(req)
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, err
	}
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("geetest: HTTP doer returned an empty response")
	}
	c.jar.SetCookies(req.URL, resp.Cookies())
	defer func() {
		if closeErr := resp.Body.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	limited := io.LimitReader(resp.Body, c.maxBody+1)
	body, err = io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > c.maxBody {
		return nil, fmt.Errorf("%w (%d bytes)", ErrResponseTooLarge, c.maxBody)
	}
	return body, nil
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
