package mockserver

import (
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	ilink "github.com/openilink/openilink-sdk-go"

	"github.com/openilink/openilink-hub/internal/jsonx"
)

// InjectInbound simulates an incoming message from a user.
func (e *Engine) InjectInbound(req InboundRequest) {
	e.mu.Lock()

	e.msgSeq++
	msgID := e.msgSeq

	recipient := req.Recipient
	if recipient == "" {
		recipient = "bot"
	}

	contextToken := req.ContextToken
	if contextToken == "" {
		contextToken = "ctx-" + randomHex(8)
	}

	// Upstream uses message_type 1 for user messages; default to that.
	messageType := req.MessageType
	if messageType == 0 {
		messageType = int(ilink.MsgTypeUser)
	}

	msg := &ilink.WeixinMessage{
		MessageID:    msgID,
		FromUserID:   req.Sender,
		ToUserID:     recipient,
		CreateTimeMs: e.clock.Now().UnixMilli(),
		MessageType:  ilink.MessageType(messageType),
		ContextToken: contextToken,
		SessionID:    req.SessionID,
		GroupID:      req.GroupID,
		MessageState: ilink.MessageState(req.MessageState),
	}

	if len(req.RawItems) > 0 {
		// Item JSON the SDK types cannot represent (types 11/12); kept aside
		// and spliced back into the getUpdates response.
		raw := make([]jsonx.RawMessage, len(req.RawItems))
		copy(raw, req.RawItems)
		e.rawItems[msgID] = raw
	}

	if len(req.Items) > 0 {
		for _, ir := range req.Items {
			item := e.convertItemRequest(ir)
			msg.ItemList = append(msg.ItemList, item)
		}
	} else if req.Text != "" {
		msg.ItemList = []ilink.MessageItem{
			{
				Type:     ilink.ItemText,
				TextItem: &ilink.TextItem{Text: req.Text},
			},
		}
	}

	// Check message loss.
	if e.opts.messageLoss > 0 && rand.Float64() < e.opts.messageLoss {
		e.mu.Unlock()
		return
	}

	inbound := e.inbound
	e.mu.Unlock()

	// Push to inbound channel (non-blocking if full, but channel is buffered).
	select {
	case inbound <- msg:
	default:
	}
}

// convertItemRequest converts an ItemRequest to an ilink.MessageItem.
// Must be called with e.mu held.
func (e *Engine) convertItemRequest(ir ItemRequest) ilink.MessageItem {
	item := ilink.MessageItem{}
	switch strings.ToLower(ir.Type) {
	case "text":
		item.Type = ilink.ItemText
		item.TextItem = &ilink.TextItem{Text: ir.Text}
	case "image":
		item.Type = ilink.ItemImage
		img := &ilink.ImageItem{URL: ir.URL}
		if len(ir.Data) > 0 {
			sm := e.storeMediaLocked(ir.Data, ir.FileName, int(ilink.MediaImage))
			img.Media = sm.media
			img.AESKey = sm.keyHex // upstream: image_item.aeskey wins over media.aes_key
			img.MidSize = sm.cipherSize
		}
		item.ImageItem = img
	case "voice":
		item.Type = ilink.ItemVoice
		voice := &ilink.VoiceItem{
			Text:          ir.Text,
			PlayTime:      ir.PlayTime,
			SampleRate:    ir.SampleRate,
			EncodeType:    ilink.VoiceFormat(ir.EncodeType),
			BitsPerSample: ir.BitsPerSample,
		}
		if len(ir.Data) > 0 {
			sm := e.storeMediaLocked(ir.Data, ir.FileName, int(ilink.MediaVoice))
			voice.Media = sm.media
		}
		item.VoiceItem = voice
	case "file":
		item.Type = ilink.ItemFile
		file := &ilink.FileItem{FileName: ir.FileName}
		if len(ir.Data) > 0 {
			sm := e.storeMediaLocked(ir.Data, ir.FileName, int(ilink.MediaFile))
			file.Media = sm.media
			file.Len = strconv.FormatInt(sm.rawSize, 10)
			file.MD5 = fmt.Sprintf("%x", md5.Sum(ir.Data))
		}
		item.FileItem = file
	case "video":
		item.Type = ilink.ItemVideo
		video := &ilink.VideoItem{}
		if len(ir.Data) > 0 {
			sm := e.storeMediaLocked(ir.Data, ir.FileName, int(ilink.MediaVideo))
			video.Media = sm.media
			video.VideoSize = sm.cipherSize
			video.VideoMD5 = fmt.Sprintf("%x", md5.Sum(ir.Data))
		}
		item.VideoItem = video
	}

	// Quoted message (ref_msg).
	if ir.Ref != nil {
		refItem := e.convertItemRequest(*ir.Ref)
		item.RefMsg = &ilink.RefMessage{Title: ir.RefTitle, MessageItem: &refItem}
	}

	return item
}

// storedMedia is the result of storing an injected media payload.
type storedMedia struct {
	media      *ilink.CDNMedia
	keyHex     string
	rawSize    int64
	cipherSize int64
}

// storeMediaLocked encrypts data and stores it in e.media, returning a CDNMedia
// reference. The AES key is emitted as base64(hex), matching what the upstream
// image/video/file senders put in media.aes_key, and full_url is filled in when
// a CDN base URL is configured. Must be called with e.mu held.
func (e *Engine) storeMediaLocked(data []byte, fileName string, mediaType int) storedMedia {
	keyRaw, keyHex := generateAESKey()
	ciphertext, err := encryptMedia(data, keyRaw)
	if err != nil {
		return storedMedia{}
	}
	eqp := generateEQP()
	e.media[eqp] = &mediaEntry{
		ciphertext: ciphertext,
		aesKeyHex:  keyHex,
		aesKeyRaw:  keyRaw,
		mediaType:  mediaType,
		fileName:   fileName,
		rawSize:    len(data),
		uploadedAt: e.clock.Now(),
	}
	media := &ilink.CDNMedia{
		EncryptQueryParam: eqp,
		AESKey:            base64.StdEncoding.EncodeToString([]byte(keyHex)),
		EncryptType:       ilink.EncryptAES128ECB,
	}
	if e.opts.cdnBaseURL != "" {
		media.FullURL = e.opts.cdnBaseURL + "/download?encrypted_query_param=" + url.QueryEscape(eqp)
	}
	return storedMedia{
		media:      media,
		keyHex:     keyHex,
		rawSize:    int64(len(data)),
		cipherSize: int64(len(ciphertext)),
	}
}

// SentMessages returns a copy of all sent messages.
func (e *Engine) SentMessages() []SentMessage {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]SentMessage, len(e.sent))
	copy(result, e.sent)
	return result
}

// WaitForSent waits until at least one new message has been sent, or timeout is reached.
func (e *Engine) WaitForSent(t testing.TB, timeout time.Duration) []SentMessage {
	t.Helper()
	e.mu.Lock()
	initial := len(e.sent)
	e.mu.Unlock()

	deadline := time.After(timeout)
	for {
		e.mu.Lock()
		current := len(e.sent)
		e.mu.Unlock()
		if current > initial {
			return e.SentMessages()
		}
		select {
		case <-deadline:
			t.Fatalf("WaitForSent: timed out after %v waiting for new messages", timeout)
			return nil
		case <-e.sentCh:
			// Check again in next loop iteration.
		}
	}
}

// AssertSentCount asserts that exactly n messages have been sent.
func (e *Engine) AssertSentCount(t testing.TB, n int) {
	t.Helper()
	e.mu.Lock()
	got := len(e.sent)
	e.mu.Unlock()
	if got != n {
		t.Errorf("sent count = %d, want %d", got, n)
	}
}

// AssertSentContains asserts that at least one sent message contains the substring.
func (e *Engine) AssertSentContains(t testing.TB, substring string) {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, m := range e.sent {
		if strings.Contains(m.Text, substring) {
			return
		}
	}
	t.Errorf("no sent message contains %q", substring)
}

// ExpireSession expires the current session, unblocking any GetUpdates calls.
func (e *Engine) ExpireSession() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.status = "session_expired"
	close(e.inbound)
	e.inbound = make(chan *ilink.WeixinMessage, 100)
	if e.qr != nil && e.qr.timer != nil {
		e.qr.timer.Stop()
	}
}

// ScanQR simulates scanning the QR code.
func (e *Engine) ScanQR() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.qr != nil {
		e.qr.state = QRStateScanned
		select {
		case e.qr.ch <- struct{}{}:
		default:
		}
	}
}

// SetQRState forces the QR session into an arbitrary state, so every state the
// upstream protocol defines can be exercised. redirectHost and verifyCode are
// only meaningful for "scaned_but_redirect" and "need_verifycode".
func (e *Engine) SetQRState(state, redirectHost, verifyCode string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.qr == nil {
		return
	}
	e.qr.state = state
	e.qr.redirectHost = redirectHost
	e.qr.verifyCode = verifyCode
	select {
	case e.qr.ch <- struct{}{}:
	default:
	}
}

// ConfirmQR simulates confirming the QR code login with credentials. The bot
// token is remembered as bound so a later local_token_list containing it leads
// to "binded_redirect".
func (e *Engine) ConfirmQR(creds Credentials) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.qr != nil {
		e.qr.state = QRStateConfirmed
		e.qr.creds = creds
		select {
		case e.qr.ch <- struct{}{}:
		default:
		}
	}
	if creds.BotToken != "" {
		e.boundTokens[creds.BotToken] = true
	}
}

// MarkBoundToken records a bot token as already bound to this mock instance.
func (e *Engine) MarkBoundToken(token string) {
	if token == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.boundTokens == nil {
		e.boundTokens = make(map[string]bool)
	}
	e.boundTokens[token] = true
}

// BoundTokens returns the tokens already bound to this mock instance.
func (e *Engine) BoundTokens() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.boundTokens))
	for tok := range e.boundTokens {
		out = append(out, tok)
	}
	return out
}

// RawItemsFor returns item JSON injected verbatim for a message ID.
func (e *Engine) RawItemsFor(messageID int64) []jsonx.RawMessage {
	e.mu.Lock()
	defer e.mu.Unlock()
	items := e.rawItems[messageID]
	if len(items) == 0 {
		return nil
	}
	out := make([]jsonx.RawMessage, len(items))
	copy(out, items)
	return out
}

// SetCDNBaseURL points the mock's full_url / upload_full_url values at the
// given CDN base URL (for example "<server>/c2c").
func (e *Engine) SetCDNBaseURL(base string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.opts.cdnBaseURL = strings.TrimSuffix(base, "/")
}

// SetCDNUploadStatus makes POST /c2c/upload fail with the given HTTP status.
// Zero (the default) restores normal uploads.
func (e *Engine) SetCDNUploadStatus(status int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.opts.cdnUploadStatus = status
}

// CDNUploadStatus returns the injected CDN upload failure status.
func (e *Engine) CDNUploadStatus() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.opts.cdnUploadStatus
}

// recordHeaders stores the headers of the most recent /ilink/ request.
func (e *Engine) recordHeaders(h http.Header) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastHeaders = h.Clone()
}

// RequestHeaders returns the headers of the most recent /ilink/ request.
func (e *Engine) RequestHeaders() http.Header {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lastHeaders == nil {
		return nil
	}
	return e.lastHeaders.Clone()
}

// recordQRRequest stores metadata about the most recent get_bot_qrcode call.
func (e *Engine) recordQRRequest(method, botType string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastQRReq = QRRequest{Method: method, BotType: botType}
}

// LastQRRequest returns metadata about the most recent get_bot_qrcode call.
func (e *Engine) LastQRRequest() QRRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	req := e.lastQRReq
	req.LocalTokenList = append([]string(nil), req.LocalTokenList...)
	return req
}

// LastUploadRequest returns a copy of the most recent getuploadurl request.
func (e *Engine) LastUploadRequest() *ilink.GetUploadURLReq {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lastUploadReq == nil {
		return nil
	}
	req := *e.lastUploadReq
	return &req
}

// ListMedia returns info about all stored media files.
func (e *Engine) ListMedia() []MediaInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	var result []MediaInfo
	for eqp, entry := range e.media {
		result = append(result, MediaInfo{
			EQP:       eqp,
			AESKey:    entry.aesKeyHex,
			MediaType: entry.mediaType,
			FileName:  entry.fileName,
			Size:      entry.rawSize,
		})
	}
	return result
}

// Reset clears all engine state and recreates channels.
func (e *Engine) Reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.qr != nil && e.qr.timer != nil {
		e.qr.timer.Stop()
	}
	e.token = ""
	e.status = "disconnected"
	e.inbound = make(chan *ilink.WeixinMessage, 100)
	e.sent = nil
	e.sentCh = make(chan struct{}, 1)
	e.syncBuf = ""
	e.msgSeq = 0
	e.media = make(map[string]*mediaEntry)
	e.qr = nil
	e.typing = make(map[string]bool)
	e.rawItems = make(map[int64][]jsonx.RawMessage)
	e.boundTokens = make(map[string]bool)
	e.lastUploadReq = nil
	e.lastQRReq = QRRequest{}
	e.lastHeaders = nil
	e.opts.cdnUploadStatus = 0
}

// SetToken sets the engine's authentication token.
func (e *Engine) SetToken(token string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.token = token
}

// SetStatus sets the engine's connection status.
func (e *Engine) SetStatus(status string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.status = status
}
