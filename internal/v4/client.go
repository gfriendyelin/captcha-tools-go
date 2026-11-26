package v4

import (
	"bytes"
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultLoadURL    = "https://gcaptcha4.geetest.com/load"
	DefaultVerifyURL  = "https://gcaptcha4.geetest.com/verify"
	DefaultStaticBase = "https://static.geetest.com/"

	defaultTimeout = 10 * time.Second
	jsonpLimit     = 1 << 20
	resourceLimit  = 8 << 20
)

//go:embed resources/*-config.json
var configFS embed.FS

var (
	lotPattern        = regexp.MustCompile(`(?i)^[a-f\d]{32}$`)
	releasePattern    = regexp.MustCompile(`^v?\d+\.\d+\.\d+`)
	jsonpPattern      = regexp.MustCompile(`(?s)^(?:[\w$]+)?\((.*)\);?$`)
	allowedHostSuffix = []string{"geetest.com", "geevisit.com", "geetest.net"}
)

// HTTPDoer is implemented by *http.Client.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// HTTPDoerFunc adapts a function for tests.
type HTTPDoerFunc func(*http.Request) (*http.Response, error)

func (f HTTPDoerFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

// Config controls a V4 client. Zero endpoints select production defaults.
type Config struct {
	Doer            HTTPDoer
	Timeout         time.Duration // zero: 10s; negative: no extra timeout
	MaxResponseSize int64         // zero: 8 MiB
	Now             func() time.Time
	LoadURL         string
	VerifyURL       string
	StaticBase      string
	Keyring         *Keyring
}

// Error is a protocol error with an optional upstream code.
type Error struct {
	Code         string
	Message      string
	Retryable    bool
	UpstreamCode int
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Code + ": " + e.Message
	}
	return e.Code
}

// ErrProtocolKeyUpdated signals that a recovered RSA key must be used by
// opening a fresh session.
var ErrProtocolKeyUpdated = &Error{Code: "protocol_key_updated", Retryable: true}

// Challenge identifies the GeeTest V4 site.
type Challenge struct {
	CaptchaID  string
	ClientType string
	RiskType   string
	Lang       string
	Voice      bool
}

// Patrol is one release config entry from resources/<release>-config.json.
type Patrol struct {
	Biht        string `json:"biht"`
	LibKey      string `json:"lib_key"`
	LibVal      string `json:"lib_val"`
	AboKey      string `json:"abo_key"`
	AboVal      string `json:"abo_val"`
	TrackEnable *bool  `json:"track_enable"`
}

// Asset is a saveable puzzle input.
type Asset struct {
	Name string
	Data []byte
}

// Puzzle is the protocol-independent recognition input.
type Puzzle struct {
	Kind            string
	Image           []byte
	Cells           [][]byte
	Prompts         [][]byte
	Count           int
	Style           string
	Background      []byte
	Piece           []byte
	Y               int
	Board           [][]int
	Audio           []byte
	Language        string
	TemplateVersion string
	Question        string
	Answers         []byte
}

// SaveAssets returns the inputs worth persisting for save_result.
func (p Puzzle) SaveAssets() []Asset {
	switch p.Kind {
	case "slide":
		return []Asset{
			{Name: "background" + imageExt(p.Background), Data: p.Background},
			{Name: "piece" + imageExt(p.Piece), Data: p.Piece},
		}
	case "click":
		assets := []Asset{{Name: "image" + imageExt(p.Image), Data: p.Image}}
		for i, prompt := range p.Prompts {
			assets = append(assets, Asset{Name: fmt.Sprintf("prompt_%d%s", i, imageExt(prompt)), Data: prompt})
		}
		return assets
	case "grid":
		assets := []Asset{{Name: "image" + imageExt(p.Image), Data: p.Image}}
		for i, cell := range p.Cells {
			assets = append(assets, Asset{Name: fmt.Sprintf("cell_%d.png", i), Data: cell})
		}
		for i, prompt := range p.Prompts {
			assets = append(assets, Asset{Name: fmt.Sprintf("prompt_%d%s", i, imageExt(prompt)), Data: prompt})
		}
		return assets
	case "voice":
		return []Asset{{Name: "audio.mp3", Data: p.Audio}}
	case "svg":
		return []Asset{
			{Name: "question.txt", Data: []byte(p.Question)},
			{Name: "answers.bin", Data: p.Answers},
		}
	default:
		return nil
	}
}

func imageExt(data []byte) string {
	switch http.DetectContentType(data) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	default:
		return ".bin"
	}
}

// Answer is a solver result expressed without wire knowledge.
type Answer struct {
	Kind    string
	Indices []int
	Points  [][2]float64
	Offset  int
	Cells   [][2]int
	Text    string
}

type wireChallenge struct {
	CaptchaType     string          `json:"captcha_type"`
	LotNumber       string          `json:"lot_number"`
	StaticPath      string          `json:"static_path"`
	PT              json.RawMessage `json:"pt"`
	PowDetail       PowDetail       `json:"pow_detail"`
	Payload         string          `json:"payload"`
	ProcessToken    string          `json:"process_token"`
	PayloadProtocol *int            `json:"payload_protocol"`
	Guard           bool            `json:"guard"`
	BG              string          `json:"bg"`
	Slice           string          `json:"slice"`
	YPos            json.RawMessage `json:"ypos"`
	Ques            json.RawMessage `json:"ques"`
	NineNums        json.RawMessage `json:"nine_nums"`
	VoicePath       string          `json:"voice_path"`
	Imgs            string          `json:"imgs"`
	QuestionPath    string          `json:"question_path"`
	AnswerPath      string          `json:"answer_path"`
}

// Client owns the endpoint configuration, patrol cache and keyring.
type Client struct {
	doer      HTTPDoer
	transport *http.Transport
	loadURL   *url.URL
	verifyURL *url.URL
	static    *url.URL
	timeout   time.Duration
	maxBody   int64
	now       func() time.Time
	keyring   *Keyring

	patrolMu sync.Mutex
	patrols  map[string]Patrol
}

func NewClient(cfg Config) (*Client, error) {
	loadURL, err := url.Parse(defaultString(cfg.LoadURL, DefaultLoadURL))
	if err != nil {
		return nil, fmt.Errorf("v4: parse load url: %w", err)
	}
	verifyURL, err := url.Parse(defaultString(cfg.VerifyURL, DefaultVerifyURL))
	if err != nil {
		return nil, fmt.Errorf("v4: parse verify url: %w", err)
	}
	static, err := url.Parse(defaultString(cfg.StaticBase, DefaultStaticBase))
	if err != nil {
		return nil, fmt.Errorf("v4: parse static base: %w", err)
	}
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	maxBody := cfg.MaxResponseSize
	if maxBody == 0 {
		maxBody = resourceLimit
	}
	if maxBody < 0 {
		return nil, fmt.Errorf("v4: MaxResponseSize must be positive")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	keyring := cfg.Keyring
	if keyring == nil {
		keyring = NewKeyring()
	}
	c := &Client{
		loadURL: loadURL, verifyURL: verifyURL, static: static,
		timeout: timeout, maxBody: maxBody, now: now, keyring: keyring,
		patrols: map[string]Patrol{},
	}
	if cfg.Doer == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.ForceAttemptHTTP2 = true
		c.transport = transport
		c.doer = &http.Client{Transport: transport}
	} else {
		c.doer = cfg.Doer
	}
	return c, nil
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// Close releases idle connections.
func (c *Client) Close() {
	if c == nil {
		return
	}
	if c.transport != nil {
		c.transport.CloseIdleConnections()
	}
}

// Session is one loaded V4 challenge.
type Session struct {
	client    *Client
	challenge Challenge
	data      wireChallenge
	patrol    Patrol
	puzzle    Puzzle
	pt        int
}

// Open performs /load, resolves the patrol config and downloads the puzzle.
func (c *Client) Open(ctx context.Context, challenge Challenge) (*Session, error) {
	if challenge.CaptchaID == "" {
		return nil, &Error{Code: "invalid_captcha_id", Message: "captcha_id is required"}
	}
	clientType := challenge.ClientType
	if clientType == "" {
		clientType = "web"
	}
	lang := challenge.Lang
	if lang == "" {
		lang = "zh"
	}
	challenge.ClientType, challenge.Lang = clientType, lang
	query := url.Values{}
	query.Set("callback", c.callback())
	query.Set("captcha_id", challenge.CaptchaID)
	query.Set("client_type", clientType)
	if challenge.RiskType != "" && challenge.RiskType != "auto" {
		query.Set("risk_type", challenge.RiskType)
	}
	query.Set("challenge", newUUID())
	query.Set("lang", lang)
	if challenge.Voice {
		query.Set("switch_to", "voice")
	}
	root, err := c.api(ctx, c.loadURL, query)
	if err != nil {
		return nil, err
	}
	data, err := decodeData(root)
	if err != nil {
		return nil, err
	}
	if !lotPattern.MatchString(data.LotNumber) {
		return nil, &Error{Code: "invalid_v4_challenge", Message: "lot_number is not a 32 character hex string"}
	}
	pt, err := numberInt(data.PT)
	if err != nil {
		return nil, &Error{Code: "invalid_v4_challenge", Message: "pt is missing"}
	}
	patrol, err := c.patrol(data.StaticPath)
	if err != nil {
		return nil, err
	}
	puzzle, err := c.puzzle(ctx, data)
	if err != nil {
		return nil, err
	}
	return &Session{client: c, challenge: challenge, data: data, patrol: patrol, puzzle: puzzle, pt: pt}, nil
}

// Puzzle returns the loaded recognition input.
func (s *Session) Puzzle() Puzzle { return s.puzzle }

// Kind returns the puzzle kind.
func (s *Session) Kind() string { return s.puzzle.Kind }

// PT returns the payload protocol (encryption mode) requested by /load.
func (s *Session) PT() int { return s.pt }

func decodeData(root map[string]any) (wireChallenge, error) {
	var data wireChallenge
	value, ok := root["data"]
	if !ok || value == nil {
		return data, &Error{Code: "invalid_v4_challenge", Message: "load response has no data"}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return data, &Error{Code: "invalid_v4_challenge", Message: err.Error()}
	}
	if err := json.Unmarshal(encoded, &data); err != nil {
		return data, &Error{Code: "invalid_v4_challenge", Message: err.Error()}
	}
	return data, nil
}

func (c *Client) patrol(staticPath string) (Patrol, error) {
	version := strings.Replace(staticPath, "/v4/static/", "", 1)
	release := releasePattern.FindString(version)
	if release == "" {
		return Patrol{}, unsupportedBuild()
	}
	c.patrolMu.Lock()
	defer c.patrolMu.Unlock()
	if _, ok := c.patrols[version]; !ok {
		content, err := configFS.ReadFile("resources/" + release + "-config.json")
		if err != nil {
			return Patrol{}, unsupportedBuild()
		}
		var entries map[string]Patrol
		if err := json.Unmarshal(content, &entries); err != nil {
			return Patrol{}, unsupportedBuild()
		}
		for name, entry := range entries {
			c.patrols[name] = entry
		}
	}
	patrol, ok := c.patrols[version]
	if !ok {
		return Patrol{}, unsupportedBuild()
	}
	return patrol, nil
}

func unsupportedBuild() error {
	return &Error{Code: "unsupported_build", Message: "no patrol config for this static build"}
}

func (c *Client) callback() string {
	return "geetest_" + strconv.FormatInt(c.now().UnixMilli(), 10)
}

// api issues a JSONP request and returns the decoded root object.
func (c *Client) api(ctx context.Context, endpoint *url.URL, query url.Values) (map[string]any, error) {
	target := *endpoint
	target.RawQuery = query.Encode()
	body, err := c.get(ctx, target.String(), jsonpLimit)
	if err != nil {
		return nil, err
	}
	parsed, err := parseJSONP(body)
	if err != nil {
		return nil, &Error{Code: "invalid_upstream_response", Message: err.Error()}
	}
	if status, _ := parsed["status"].(string); status == "error" {
		return nil, &Error{Code: "upstream_api_error", Message: "upstream returned an error", UpstreamCode: upstreamCode(parsed)}
	}
	return parsed, nil
}

func upstreamCode(parsed map[string]any) int {
	if code, ok := intValue(parsed["code"]); ok {
		return code
	}
	if data, ok := parsed["data"].(map[string]any); ok {
		if code, ok := intValue(data["code"]); ok {
			return code
		}
	}
	return 0
}

func intValue(value any) (int, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			asFloat, floatErr := typed.Float64()
			if floatErr != nil {
				return 0, false
			}
			parsed = int64(asFloat)
		}
		return int(parsed), true
	case float64:
		return int(typed), true
	case int:
		return typed, true
	default:
		return 0, false
	}
}

func parseJSONP(body []byte) (map[string]any, error) {
	text := strings.TrimSpace(string(body))
	if match := jsonpPattern.FindStringSubmatch(text); match != nil {
		text = match[1]
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok || object == nil {
		return nil, fmt.Errorf("upstream response is not an object")
	}
	return object, nil
}

func (c *Client) resource(ctx context.Context, raw string) ([]byte, error) {
	target, err := c.static.Parse(raw)
	if err != nil {
		return nil, &Error{Code: "invalid_resource_url", Message: err.Error()}
	}
	if target.Scheme != "https" || !allowedHost(target.Hostname()) {
		return nil, &Error{Code: "invalid_resource_url", Message: target.String()}
	}
	return c.get(ctx, target.String(), c.maxBody)
}

func allowedHost(host string) bool {
	host = strings.ToLower(host)
	for _, suffix := range allowedHostSuffix {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

func (c *Client) get(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 Chrome/147.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, &Error{Code: "upstream_http_error", Message: err.Error(), Retryable: true}
	}
	if resp == nil || resp.Body == nil {
		return nil, &Error{Code: "empty_response", Message: "upstream returned no body", Retryable: true}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		code := "upstream_http_error"
		if resp.StatusCode == http.StatusTooManyRequests {
			code = "rate_limited"
		}
		return nil, &Error{Code: code, Message: "HTTP " + resp.Status, Retryable: resp.StatusCode >= 500}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, &Error{Code: "upstream_http_error", Message: err.Error(), Retryable: true}
	}
	if int64(len(body)) > limit {
		return nil, &Error{Code: "upstream_response_too_large", Message: "response exceeds limit"}
	}
	return body, nil
}

func (c *Client) puzzle(ctx context.Context, data wireChallenge) (Puzzle, error) {
	switch data.CaptchaType {
	case "ai":
		return Puzzle{Kind: "silent"}, nil
	case "slide":
		background, err := c.resource(ctx, data.BG)
		if err != nil {
			return Puzzle{}, err
		}
		piece, err := c.resource(ctx, data.Slice)
		if err != nil {
			return Puzzle{}, err
		}
		y := 0
		if len(data.YPos) > 0 {
			y, _ = numberInt(data.YPos)
		}
		return Puzzle{Kind: "slide", Background: background, Piece: piece, Y: y}, nil
	case "match", "winlinze":
		board, err := parseBoard(data.Ques)
		if err != nil {
			return Puzzle{}, err
		}
		kind := "match"
		if data.CaptchaType == "winlinze" {
			kind = "gomoku"
		}
		return Puzzle{Kind: kind, Board: board}, nil
	case "svg_seed", "svg_icon":
		question := data.QuestionPath
		var answers []byte
		if data.CaptchaType == "svg_icon" {
			content, err := c.resource(ctx, data.QuestionPath)
			if err != nil {
				return Puzzle{}, err
			}
			question = string(content)
			if answers, err = c.resource(ctx, data.AnswerPath); err != nil {
				return Puzzle{}, err
			}
		} else {
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(data.AnswerPath))
			if err != nil {
				return Puzzle{}, &Error{Code: "invalid_puzzle", Message: "answers are not base64"}
			}
			answers = decoded
		}
		return Puzzle{Kind: "svg", Question: question, Answers: answers}, nil
	case "voice":
		parts := strings.Split(data.VoicePath, "/")
		if len(parts) < 3 {
			return Puzzle{}, &Error{Code: "invalid_puzzle", Message: "invalid voice path"}
		}
		audio, err := c.resource(ctx, data.VoicePath)
		if err != nil {
			return Puzzle{}, err
		}
		return Puzzle{
			Kind: "voice", Audio: audio,
			Language:        parts[len(parts)-2],
			TemplateVersion: parts[len(parts)-3],
		}, nil
	case "icon", "word", "phrase", "nine":
		imageBytes, err := c.resource(ctx, data.Imgs)
		if err != nil {
			return Puzzle{}, err
		}
		promptURLs, err := parseStringArray(data.Ques)
		if err != nil {
			return Puzzle{}, err
		}
		prompts := make([][]byte, 0, len(promptURLs))
		for _, promptURL := range promptURLs {
			prompt, err := c.resource(ctx, promptURL)
			if err != nil {
				return Puzzle{}, err
			}
			prompts = append(prompts, prompt)
		}
		if data.CaptchaType != "nine" {
			style := "symbols"
			switch data.CaptchaType {
			case "word":
				style = "text"
			case "phrase":
				style = "phrase"
			}
			return Puzzle{Kind: "click", Image: imageBytes, Prompts: prompts, Style: style}, nil
		}
		count, err := integerNumber(data.NineNums)
		if err != nil || count < 1 || count > 9 {
			return Puzzle{}, &Error{Code: "invalid_grid", Message: "invalid nine_nums"}
		}
		cells, err := cropCells(imageBytes)
		if err != nil {
			return Puzzle{}, &Error{Code: "invalid_grid", Message: err.Error()}
		}
		return Puzzle{Kind: "grid", Image: imageBytes, Cells: cells, Prompts: prompts, Count: count}, nil
	default:
		return Puzzle{}, &Error{Code: "unsupported_puzzle", Message: "Unsupported puzzle: " + data.CaptchaType}
	}
}

// cropCells performs the reference equal-thirds crop and re-encodes PNG cells.
func cropCells(data []byte) ([][]byte, error) {
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode puzzle image: %w", err)
	}
	bounds := decoded.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width == 0 || height == 0 {
		return nil, fmt.Errorf("empty puzzle image")
	}
	cells := make([][]byte, 0, 9)
	for row := 0; row < 3; row++ {
		for column := 0; column < 3; column++ {
			left := bounds.Min.X + column*width/3
			top := bounds.Min.Y + row*height/3
			right := bounds.Min.X + (column+1)*width/3
			bottom := bounds.Min.Y + (row+1)*height/3
			cell := image.NewNRGBA(image.Rect(0, 0, right-left, bottom-top))
			draw.Draw(cell, cell.Bounds(), decoded, image.Pt(left, top), draw.Src)
			var buffer bytes.Buffer
			if err := png.Encode(&buffer, cell); err != nil {
				return nil, err
			}
			cells = append(cells, buffer.Bytes())
		}
	}
	return cells, nil
}

func parseStringArray(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, &Error{Code: "invalid_puzzle", Message: "ques is not a string array"}
	}
	return values, nil
}

func parseBoard(raw json.RawMessage) ([][]int, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, &Error{Code: "invalid_board", Message: "ques is missing"}
	}
	var board [][]int
	if err := json.Unmarshal(raw, &board); err != nil {
		return nil, &Error{Code: "invalid_board", Message: err.Error()}
	}
	return board, nil
}

func numberString(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("missing number")
	}
	if raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", err
		}
		return value, nil
	}
	var value json.Number
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	return value.String(), nil
}

func numberInt(raw json.RawMessage) (int, error) {
	value, err := numberString(raw)
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, err
	}
	return int(parsed), nil
}

func integerNumber(raw json.RawMessage) (int, error) {
	value, err := numberString(raw)
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.Trunc(parsed) != parsed {
		return 0, fmt.Errorf("not an integer")
	}
	return int(parsed), nil
}

// Submit encrypts the answer, calls /verify and returns the upstream JSON.
func (s *Session) Submit(ctx context.Context, answer Answer) ([]byte, bool, error) {
	fields, track, err := answerFields(s.data.CaptchaType, answer)
	if err != nil {
		return nil, false, err
	}
	td := ""
	if track != nil && (s.patrol.TrackEnable == nil || *s.patrol.TrackEnable) {
		if td, err = zipTrack(*track); err != nil {
			return nil, false, err
		}
	}
	powMessage, powSign, err := ProofOfWork(ctx, s.data.LotNumber, s.challenge.CaptchaID, s.data.PowDetail)
	if err != nil {
		return nil, false, err
	}
	lot, err := lotFields(s.patrol.AboKey, s.patrol.AboVal, s.data.LotNumber)
	if err != nil {
		return nil, false, err
	}
	payload := map[string]any{
		"pow_msg":  powMessage,
		"pow_sign": powSign,
	}
	// The reference spreads the lot fields next, then baseline fields, then the
	// answer, so a lot key never shadows protocol constants.
	for key, value := range lot {
		payload[key] = value
	}
	payload[s.patrol.LibKey] = s.patrol.LibVal
	payload["biht"] = s.patrol.Biht
	payload["device_id"] = ""
	payload["em"] = map[string]any{"cp": 0, "ek": "11", "nt": 0, "ph": 0, "sc": 0, "si": 0, "wd": 1}
	payload["ep"] = "123"
	payload["geetest"] = "captcha"
	payload["lang"] = "zh"
	payload["lot_number"] = s.data.LotNumber
	payload["nd"] = []int{0, 0, 0, 0, 0, 0, 0}
	payload["tc"] = map[string]any{"mtp": 0, "te": false, "ts": false}
	payload["tz"] = map[string]any{"name": "Asia/Shanghai", "offset": 480}
	for key, value := range fields {
		payload[key] = value
	}
	if td != "" {
		payload["td_sign"] = TrackSignature(s.data.LotNumber, td)
	}
	if s.data.Guard {
		payload["roe"] = map[string]any{
			"aup": "3", "sep": "3", "egp": "3", "auh": "3",
			"rew": "3", "snh": "3", "res": "3", "cdc": "3",
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, false, err
	}
	key := s.client.keyring.Get(s.data.StaticPath)
	wire, err := EncryptPayload(string(encoded), s.pt, key)
	if err != nil {
		return nil, false, err
	}
	riskType := s.challenge.RiskType
	if riskType == "" || riskType == "auto" {
		riskType = s.data.CaptchaType
	}
	query := url.Values{}
	query.Set("callback", s.client.callback())
	query.Set("captcha_id", s.challenge.CaptchaID)
	query.Set("client_type", s.challenge.ClientType)
	query.Set("risk_type", riskType)
	query.Set("lot_number", s.data.LotNumber)
	query.Set("payload", s.data.Payload)
	query.Set("process_token", s.data.ProcessToken)
	if s.data.PayloadProtocol != nil {
		query.Set("payload_protocol", strconv.Itoa(*s.data.PayloadProtocol))
	}
	query.Set("pt", strconv.Itoa(s.pt))
	query.Set("w", wire)
	if td != "" {
		query.Set("td", td)
	}
	root, err := s.client.api(ctx, s.client.verifyURL, query)
	if err != nil {
		if s.pt == 1 && isKeyRotation(err) {
			if recoverErr := s.recoverKey(ctx); recoverErr != nil {
				return nil, false, errors.Join(err, recoverErr)
			}
			return nil, false, fmt.Errorf("%w: %v", ErrProtocolKeyUpdated, err)
		}
		return nil, false, err
	}
	verified, _ := root["data"].(map[string]any)
	if s.pt == 1 && verified != nil && keyRotationCode(verified) {
		if recoverErr := s.recoverKey(ctx); recoverErr != nil {
			return nil, false, recoverErr
		}
		return nil, false, ErrProtocolKeyUpdated
	}
	raw, err := json.Marshal(root)
	if err != nil {
		return nil, false, err
	}
	passed := false
	if verified != nil {
		if result, _ := verified["result"].(string); result == "success" {
			if seccode, ok := verified["seccode"].(map[string]any); ok && len(seccode) > 0 {
				passed = true
			}
		}
	}
	return raw, passed, nil
}

func (s *Session) recoverKey(ctx context.Context) error {
	target := strings.TrimSuffix(s.data.StaticPath, "/") + "/js/gcaptcha4.js"
	script, err := s.client.resource(ctx, target)
	if err != nil {
		return err
	}
	return s.client.keyring.Recover(s.data.StaticPath, string(script))
}

func isKeyRotation(err error) bool {
	var protocolErr *Error
	if !errors.As(err, &protocolErr) {
		return false
	}
	return protocolErr.UpstreamCode == -50002 || protocolErr.UpstreamCode == -50003
}

func keyRotationCode(verified map[string]any) bool {
	for _, key := range []string{"code", "status"} {
		if value, ok := intValue(verified[key]); ok {
			return value == -50002 || value == -50003
		}
	}
	return false
}

// answerFields builds the wire fields for one answer; track is nil for voice.
func answerFields(captchaType string, answer Answer) (map[string]any, *Track, error) {
	switch captchaType {
	case "ai":
		if answer.Kind == "silent" {
			return map[string]any{}, nil, nil
		}
	case "slide":
		if answer.Kind == "slide" {
			track, passtime := slideTrack(answer.Offset)
			return map[string]any{
				"setLeft":      answer.Offset,
				"userresponse": float64(answer.Offset)/1.0059466666666665 + 2,
				"passtime":     passtime,
			}, &track, nil
		}
	case "nine":
		if answer.Kind == "grid" {
			cells := make([][2]int, len(answer.Indices))
			for i, index := range answer.Indices {
				cells[i] = [2]int{index/3 + 1, index%3 + 1}
			}
			track, passtime := gridTrack(answer.Indices, 3)
			return map[string]any{"userresponse": cells, "passtime": passtime}, &track, nil
		}
	case "icon", "word", "phrase":
		if answer.Kind == "points" {
			cells := make([][2]int, len(answer.Points))
			points := make([]Point, len(answer.Points))
			for i, point := range answer.Points {
				cells[i] = [2]int{int(math.Round(point[0] * 10000)), int(math.Round(point[1] * 10000))}
				points[i] = Point{point[0], point[1]}
			}
			track, passtime := clickTrack(points)
			return map[string]any{"userresponse": cells, "passtime": passtime}, &track, nil
		}
	case "match", "winlinze":
		if answer.Kind == "board" {
			track, passtime := boardTrack(answer.Cells, captchaType == "winlinze")
			return map[string]any{"userresponse": answer.Cells, "passtime": passtime}, &track, nil
		}
	case "voice":
		if answer.Kind == "text" {
			return map[string]any{"userresponse": answer.Text, "passtime": integer(20000, 30000)}, nil, nil
		}
	}
	return nil, nil, &Error{Code: "answer_type_mismatch", Message: "answer does not match " + captchaType}
}
