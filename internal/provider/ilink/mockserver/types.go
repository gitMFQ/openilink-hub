package mockserver

import (
	ilink "github.com/openilink/openilink-sdk-go"

	"github.com/openilink/openilink-hub/internal/jsonx"
)

// QR login states. The engine keeps the legacy internal spelling "scanned"
// for ScanQR; the HTTP layer translates it to the wire spelling "scaned".
const (
	QRStateWait              = "wait"
	QRStateScanned           = "scanned" // engine-internal (ScanQR)
	QRStateScaned            = "scaned"  // wire spelling used by the real API
	QRStateConfirmed         = "confirmed"
	QRStateExpired           = "expired"
	QRStateNeedVerifyCode    = "need_verifycode"
	QRStateVerifyCodeBlocked = "verify_code_blocked"
	QRStateScanedButRedirect = "scaned_but_redirect"
	QRStateBindedRedirect    = "binded_redirect"
)

// InboundRequest represents a simulated incoming message from a user.
type InboundRequest struct {
	Sender       string             `json:"sender"`
	Recipient    string             `json:"recipient,omitempty"`
	Text         string             `json:"text,omitempty"`
	Items        []ItemRequest      `json:"items,omitempty"`
	RawItems     []jsonx.RawMessage `json:"raw_items,omitempty"`
	GroupID      string             `json:"group_id,omitempty"`
	ContextToken string             `json:"context_token,omitempty"`
	SessionID    string             `json:"session_id,omitempty"`
	MessageType  int                `json:"message_type,omitempty"`
	MessageState int                `json:"message_state,omitempty"`
}

// ItemRequest describes a single item (text, file, etc.) in an InboundRequest.
type ItemRequest struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	FileName string `json:"file_name,omitempty"`
	Data     []byte `json:"data,omitempty"`
	// URL is the optional direct image/video URL (image_item.url).
	URL string `json:"url,omitempty"`
	// Ref describes the quoted message this item replies to (ref_msg).
	Ref *ItemRequest `json:"ref,omitempty"`
	// RefTitle is the summary shown for the quoted message (ref_msg.title).
	RefTitle string `json:"ref_title,omitempty"`
	// Voice fields.
	PlayTime      int `json:"playtime,omitempty"`
	SampleRate    int `json:"sample_rate,omitempty"`
	EncodeType    int `json:"encode_type,omitempty"`
	BitsPerSample int `json:"bits_per_sample,omitempty"`
}

// SendMeta carries request-level metadata that the SDK message types cannot
// express (for example upstream's msg.run_id) so the mock can record it.
type SendMeta struct {
	RunID    string
	BaseInfo *ilink.BaseInfo
}

// QRRequest records the last get_bot_qrcode request.
type QRRequest struct {
	Method         string   `json:"method"`
	BotType        string   `json:"bot_type,omitempty"`
	LocalTokenList []string `json:"local_token_list,omitempty"`
}

// SentMessage records a message sent by the bot via the mock engine.
type SentMessage struct {
	Recipient    string           `json:"recipient"`
	Text         string           `json:"text,omitempty"`
	ContextToken string           `json:"context_token,omitempty"`
	ClientID     string           `json:"client_id"`
	RunID        string           `json:"run_id,omitempty"`
	BaseInfo     *ilink.BaseInfo  `json:"base_info,omitempty"`
	Items        jsonx.RawMessage `json:"items,omitempty"`
	MediaData    []byte           `json:"-"`
	FileName     string           `json:"file_name,omitempty"`
	Timestamp    int64            `json:"timestamp"`
}

// MediaInfo describes an uploaded media file.
type MediaInfo struct {
	EQP       string `json:"eqp"`
	AESKey    string `json:"aes_key"`
	MediaType int    `json:"media_type"`
	FileName  string `json:"file_name,omitempty"`
	Size      int    `json:"size"`
}

// Credentials holds the bot login credentials returned after QR confirmation.
type Credentials struct {
	BotID       string `json:"bot_id"`
	BotToken    string `json:"bot_token"`
	BaseURL     string `json:"base_url,omitempty"`
	ILinkUserID string `json:"ilink_user_id,omitempty"`
}

// GetUpdatesResult is returned by Engine.GetUpdates.
type GetUpdatesResult struct {
	Messages []*ilink.WeixinMessage
	SyncBuf  string
	// LongPollingTimeoutMs mirrors the upstream longpolling_timeout_ms hint.
	// Zero means "do not send the field" (the client keeps its own default).
	LongPollingTimeoutMs int64
}

// QRResult is returned by Engine.FetchQRCode.
type QRResult struct {
	QRCode    string
	QRContent string
}

// QRStatusResult is returned by Engine.PollQRStatus.
type QRStatusResult struct {
	Status string
	Creds  *Credentials // set when Status == QRStateConfirmed
	// RedirectHost is set when Status == QRStateScanedButRedirect.
	RedirectHost string
}

// ConfigResult is returned by Engine.GetConfig.
type ConfigResult struct {
	TypingTicket string
}

// UploadResult is returned by Engine.GetUploadURL.
type UploadResult struct {
	UploadParam string
}
