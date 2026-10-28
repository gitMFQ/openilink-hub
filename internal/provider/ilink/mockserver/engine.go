package mockserver

import (
	"net/http"
	"sync"
	"time"

	ilink "github.com/openilink/openilink-sdk-go"

	"github.com/openilink/openilink-hub/internal/jsonx"
)

// mediaEntry stores an uploaded media file in the engine.
type mediaEntry struct {
	ciphertext []byte
	aesKeyHex  string
	aesKeyRaw  []byte
	mediaType  int
	fileName   string
	rawSize    int
	uploadedAt time.Time
}

// qrSession tracks QR login state.
type qrSession struct {
	qrCode string
	state  string
	creds  Credentials
	ch     chan struct{}
	timer  *ClockTimer
	// redirectHost is returned while state == QRStateScanedButRedirect.
	redirectHost string
	// verifyCode is the code the "phone" expects when the state is
	// QRStateNeedVerifyCode. Empty means any non-empty code is accepted.
	verifyCode string
}

// engineOptions holds configuration for the Engine.
type engineOptions struct {
	clock         Clock
	autoConfirmQR bool
	latency       time.Duration
	messageLoss   float64
	sessionTTL    time.Duration
	// cdnBaseURL, when set, makes the mock emit full_url / upload_full_url
	// values that point back at the mock CDN (upstream prefers these fields).
	cdnBaseURL string
	// cdnUploadStatus, when non-zero, makes POST /c2c/upload fail with that
	// HTTP status so clients' retry/abort logic can be exercised.
	cdnUploadStatus int
	// longPollingTimeoutMs is echoed as longpolling_timeout_ms when non-zero.
	longPollingTimeoutMs int64
}

// Option configures the Engine.
type Option func(*engineOptions)

// WithClock sets a custom clock (e.g. FakeClock for tests).
func WithClock(c Clock) Option { return func(o *engineOptions) { o.clock = c } }

// WithAutoConfirmQR makes QR login auto-confirm without waiting.
func WithAutoConfirmQR() Option { return func(o *engineOptions) { o.autoConfirmQR = true } }

// WithLatency adds artificial latency to engine operations.
func WithLatency(d time.Duration) Option { return func(o *engineOptions) { o.latency = d } }

// WithMessageLoss sets a probability (0-1) that a message is silently dropped.
func WithMessageLoss(rate float64) Option { return func(o *engineOptions) { o.messageLoss = rate } }

// WithSessionTTL sets the TTL for QR sessions.
func WithSessionTTL(d time.Duration) Option { return func(o *engineOptions) { o.sessionTTL = d } }

// WithCDNBaseURL points the mock's media URLs (full_url / upload_full_url) at
// the mock CDN instead of leaving them to the client to construct.
func WithCDNBaseURL(base string) Option {
	return func(o *engineOptions) { o.cdnBaseURL = base }
}

// WithCDNUploadStatus makes CDN uploads fail with the given HTTP status.
func WithCDNUploadStatus(status int) Option {
	return func(o *engineOptions) { o.cdnUploadStatus = status }
}

// WithLongPollingTimeout echoes the given duration in the getUpdates response,
// mirroring upstream's longpolling_timeout_ms.
func WithLongPollingTimeout(d time.Duration) Option {
	return func(o *engineOptions) { o.longPollingTimeoutMs = d.Milliseconds() }
}

// Engine is the in-memory mock iLink server. It simulates QR login, message
// polling, sending, media upload/download, and typing indicators.
type Engine struct {
	mu      sync.Mutex
	clock   Clock
	opts    engineOptions
	token   string
	status  string
	inbound chan *ilink.WeixinMessage
	sent    []SentMessage
	sentCh  chan struct{}
	syncBuf string
	msgSeq  int64
	media   map[string]*mediaEntry
	qr      *qrSession
	typing  map[string]bool

	// rawItems holds item JSON that the SDK types cannot represent (types 11
	// and 12, i.e. tool_call_start / tool_call_result), keyed by message ID.
	rawItems map[int64][]jsonx.RawMessage
	// boundTokens are bot tokens already bound to this mock backend; passing
	// one back in local_token_list yields "binded_redirect".
	boundTokens map[string]bool
	// lastUploadReq / lastQRReq / lastHeaders record what the client sent so
	// tests can assert protocol details the SDK does not model.
	lastUploadReq *ilink.GetUploadURLReq
	lastQRReq     QRRequest
	lastHeaders   http.Header
}

// NewEngine creates a new mock engine with the given options.
func NewEngine(opts ...Option) *Engine {
	o := engineOptions{clock: realClock{}}
	for _, opt := range opts {
		opt(&o)
	}
	return &Engine{
		clock:       o.clock,
		opts:        o,
		status:      "disconnected",
		inbound:     make(chan *ilink.WeixinMessage, 100),
		sentCh:      make(chan struct{}, 1),
		media:       make(map[string]*mediaEntry),
		typing:      make(map[string]bool),
		rawItems:    make(map[int64][]jsonx.RawMessage),
		boundTokens: make(map[string]bool),
	}
}
