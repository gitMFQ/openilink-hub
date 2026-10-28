package mockserver

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	ilink "github.com/openilink/openilink-sdk-go"

	"github.com/openilink/openilink-hub/internal/jsonx"
)

// --- helpers ---

type rawUpdatesResp struct {
	Ret     int `json:"ret"`
	ErrCode int `json:"errcode"`
	Msgs    []struct {
		MessageID   int64              `json:"message_id"`
		FromUserID  string             `json:"from_user_id"`
		MessageType int                `json:"message_type"`
		ItemList    []jsonx.RawMessage `json:"item_list"`
	} `json:"msgs"`
	GetUpdatesBuf        string `json:"get_updates_buf"`
	SyncBuf              string `json:"sync_buf"`
	LongPollingTimeoutMs int64  `json:"longpolling_timeout_ms"`
}

var protocolHTTP = &http.Client{Timeout: 5 * time.Second}

func rawGetUpdates(t *testing.T, base, buf string) rawUpdatesResp {
	t.Helper()
	body, _ := jsonx.Marshal(map[string]string{"get_updates_buf": buf})
	resp, err := protocolHTTP.Post(base+"/ilink/bot/getupdates", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("getupdates: %v", err)
	}
	defer resp.Body.Close()
	var out rawUpdatesResp
	if err := jsonx.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode getupdates: %v", err)
	}
	return out
}

func rawPollQR(t *testing.T, base, qrCode, verifyCode string) map[string]any {
	t.Helper()
	u := base + "/ilink/bot/get_qrcode_status?qrcode=" + url.QueryEscape(qrCode)
	if verifyCode != "" {
		u += "&verify_code=" + url.QueryEscape(verifyCode)
	}
	resp, err := protocolHTTP.Get(u)
	if err != nil {
		t.Fatalf("poll qr: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := jsonx.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode qr status: %v", err)
	}
	return out
}

func postJSON(t *testing.T, u, body string) (int, map[string]any) {
	t.Helper()
	resp, err := protocolHTTP.Post(u, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", u, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = jsonx.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// --- getUpdates ---

func TestProtocolGetUpdatesCursorAdvances(t *testing.T) {
	srv := NewHTTPServer(WithLongPollingTimeout(1234 * time.Millisecond))
	base := srv.Start()
	defer srv.Close()

	srv.Engine().InjectInbound(InboundRequest{Sender: "u1", Text: "m1"})
	first := rawGetUpdates(t, base, "buf-0")

	if first.GetUpdatesBuf == "" || first.GetUpdatesBuf == "buf-0" {
		t.Errorf("get_updates_buf = %q, want a fresh cursor (upstream: 客户端把上次响应的游标传回)", first.GetUpdatesBuf)
	}
	if first.SyncBuf != first.GetUpdatesBuf {
		t.Errorf("sync_buf = %q, want it to mirror get_updates_buf %q", first.SyncBuf, first.GetUpdatesBuf)
	}
	if first.LongPollingTimeoutMs != 1234 {
		t.Errorf("longpolling_timeout_ms = %d, want 1234", first.LongPollingTimeoutMs)
	}

	srv.Engine().InjectInbound(InboundRequest{Sender: "u1", Text: "m2"})
	second := rawGetUpdates(t, base, first.GetUpdatesBuf)
	if second.GetUpdatesBuf == first.GetUpdatesBuf {
		t.Errorf("cursor did not advance: still %q", second.GetUpdatesBuf)
	}
}

func TestProtocolGetUpdatesMessageDefaults(t *testing.T) {
	srv := NewHTTPServer()
	base := srv.Start()
	defer srv.Close()

	srv.Engine().InjectInbound(InboundRequest{Sender: "u1", Text: "m1"})
	srv.Engine().InjectInbound(InboundRequest{Sender: "u1", Text: "m2", MessageType: int(ilink.MsgTypeBot)})

	resp := rawGetUpdates(t, base, "")
	if len(resp.Msgs) != 2 {
		t.Fatalf("msgs = %d, want 2", len(resp.Msgs))
	}
	if resp.Msgs[0].MessageType != int(ilink.MsgTypeUser) {
		t.Errorf("default message_type = %d, want %d (user)", resp.Msgs[0].MessageType, ilink.MsgTypeUser)
	}
	if resp.Msgs[1].MessageType != int(ilink.MsgTypeBot) {
		t.Errorf("explicit message_type = %d, want %d (bot)", resp.Msgs[1].MessageType, ilink.MsgTypeBot)
	}
}

func TestProtocolGetUpdatesPassthroughRawItems(t *testing.T) {
	srv := NewHTTPServer()
	base := srv.Start()
	defer srv.Close()

	// Types 11/12 (tool_call_start / tool_call_result) have no SDK type, so
	// they are injected verbatim and must survive the round trip.
	status, body := postJSON(t, base+"/mock/inbound", `{
		"sender": "u9",
		"raw_items": [
			{"type": 11, "tool_call_start_item": {"tool_name": "search", "tool_call_id": "tc-1"}},
			{"type": 12, "tool_call_result_item": {"tool_name": "search", "tool_call_id": "tc-1", "status": "ok"}}
		]
	}`)
	if status != http.StatusOK {
		t.Fatalf("POST /mock/inbound status = %d, body %v", status, body)
	}

	resp := rawGetUpdates(t, base, "")
	if len(resp.Msgs) != 1 {
		t.Fatalf("msgs = %d, want 1", len(resp.Msgs))
	}
	items := resp.Msgs[0].ItemList
	if len(items) != 2 {
		t.Fatalf("item_list = %d items, want 2: %v", len(items), items)
	}
	if !strings.Contains(string(items[0]), "tool_call_start_item") {
		t.Errorf("item[0] = %s, want a tool_call_start item", items[0])
	}
	if !strings.Contains(string(items[1]), "tool_call_result_item") {
		t.Errorf("item[1] = %s, want a tool_call_result item", items[1])
	}
	if got := len(srv.Engine().RawItemsFor(resp.Msgs[0].MessageID)); got != 2 {
		t.Errorf("RawItemsFor = %d, want 2", got)
	}
}

// --- sendMessage ---

func TestProtocolSendMessageRecordsRunIDAndBaseInfo(t *testing.T) {
	srv := NewHTTPServer()
	base := srv.Start()
	defer srv.Close()

	status, body := postJSON(t, base+"/ilink/bot/sendmessage", `{
		"msg": {
			"to_user_id": "u2",
			"client_id": "c1",
			"message_type": 2,
			"message_state": 2,
			"context_token": "ctx-7",
			"run_id": "run-42",
			"item_list": [{"type": 1, "text_item": {"text": "hi"}}]
		},
		"base_info": {"channel_version": "2.4.8", "bot_agent": "OpenClaw"}
	}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %v", status, body)
	}
	if ret, _ := body["ret"].(float64); ret != 0 {
		t.Errorf("ret = %v, want 0", body["ret"])
	}

	sent := srv.Engine().SentMessages()
	if len(sent) != 1 {
		t.Fatalf("sent = %d, want 1", len(sent))
	}
	if sent[0].RunID != "run-42" {
		t.Errorf("run_id = %q, want run-42", sent[0].RunID)
	}
	if sent[0].BaseInfo == nil || sent[0].BaseInfo.ChannelVersion != "2.4.8" {
		t.Errorf("base_info = %+v, want channel_version 2.4.8", sent[0].BaseInfo)
	}
	if sent[0].ContextToken != "ctx-7" {
		t.Errorf("context_token = %q, want ctx-7", sent[0].ContextToken)
	}
}

func TestProtocolSendMessageMissingTargetReturnsRet(t *testing.T) {
	srv := NewHTTPServer()
	base := srv.Start()
	defer srv.Close()

	// Upstream reports business errors as HTTP 200 + non-zero ret.
	status, body := postJSON(t, base+"/ilink/bot/sendmessage", `{
		"msg": {"item_list": [{"type": 1, "text_item": {"text": "hi"}}]}
	}`)
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200 (upstream uses ret for business errors)", status)
	}
	if ret, _ := body["ret"].(float64); ret != -1 {
		t.Errorf("ret = %v, want -1", body["ret"])
	}
	if len(srv.Engine().SentMessages()) != 0 {
		t.Error("invalid message must not be recorded as sent")
	}

	// SDK v0.6.0 does not parse the sendMessage response body, so the same
	// request looks successful through the SDK. See docs/ilink-protocol-sync.md.
	client := ilink.NewClient("t", ilink.WithBaseURL(base))
	if _, err := client.SendText(context.Background(), "", "hi", ""); err != nil {
		t.Errorf("SDK SendText error = %v; SDK v0.6.0 is expected to ignore ret", err)
	}
}

// --- request headers ---

func TestProtocolRequestHeaders(t *testing.T) {
	srv := NewHTTPServer()
	base := srv.Start()
	defer srv.Close()

	client := ilink.NewClient("tok-1", ilink.WithBaseURL(base), ilink.WithRouteTag("route-a"))
	if _, err := client.SendText(context.Background(), "u1", "hi", "ctx"); err != nil {
		t.Fatalf("SendText: %v", err)
	}

	h := srv.Engine().RequestHeaders()
	if h == nil {
		t.Fatal("no request headers recorded")
	}
	if got := h.Get("iLink-App-Id"); got != "bot" {
		t.Errorf("iLink-App-Id = %q, want bot", got)
	}
	if got := h.Get("AuthorizationType"); got != "ilink_bot_token" {
		t.Errorf("AuthorizationType = %q, want ilink_bot_token", got)
	}
	if got := h.Get("Authorization"); got != "Bearer tok-1" {
		t.Errorf("Authorization = %q, want Bearer tok-1", got)
	}
	if got := h.Get("SKRouteTag"); got != "route-a" {
		t.Errorf("SKRouteTag = %q, want route-a", got)
	}
	if got := h.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}

	version, err := strconv.Atoi(h.Get("iLink-App-ClientVersion"))
	if err != nil || version <= 0 {
		t.Errorf("iLink-App-ClientVersion = %q, want a positive decimal (0x00MMNNPP)", h.Get("iLink-App-ClientVersion"))
	}

	uinRaw, err := base64.StdEncoding.DecodeString(h.Get("X-WECHAT-UIN"))
	if err != nil {
		t.Fatalf("X-WECHAT-UIN is not base64: %v", err)
	}
	if _, err := strconv.ParseUint(string(uinRaw), 10, 32); err != nil {
		t.Errorf("X-WECHAT-UIN decodes to %q, want a decimal uint32", uinRaw)
	}
}

func TestProtocolQRRequestHeadersOmitAuth(t *testing.T) {
	srv := NewHTTPServer()
	base := srv.Start()
	defer srv.Close()

	client := ilink.NewClient("tok-2", ilink.WithBaseURL(base))
	if _, err := client.FetchQRCode(context.Background()); err != nil {
		t.Fatalf("FetchQRCode: %v", err)
	}

	h := srv.Engine().RequestHeaders()
	if h == nil {
		t.Fatal("no request headers recorded")
	}
	if got := h.Get("iLink-App-Id"); got != "bot" {
		t.Errorf("iLink-App-Id = %q, want bot", got)
	}
	// Upstream: QR polling carries the app headers but no auth headers.
	for _, k := range []string{"Authorization", "AuthorizationType", "X-WECHAT-UIN"} {
		if got := h.Get(k); got != "" {
			t.Errorf("%s = %q, want empty on QR requests", k, got)
		}
	}
}

// --- QR flows ---

func TestProtocolQRContentIsPayloadNotBase64(t *testing.T) {
	srv := NewHTTPServer()
	base := srv.Start()
	defer srv.Close()

	client := ilink.NewClient("", ilink.WithBaseURL(base))
	qr, err := client.FetchQRCode(context.Background())
	if err != nil {
		t.Fatalf("FetchQRCode: %v", err)
	}
	if want := "https://mock.ilink/qr/" + qr.QRCode; qr.QRCodeImgContent != want {
		t.Errorf("qrcode_img_content = %q, want %q (content to render, not a base64 image)", qr.QRCodeImgContent, want)
	}
	if req := srv.Engine().LastQRRequest(); req.Method != http.MethodGet || req.BotType != "3" {
		t.Errorf("last QR request = %+v, want GET with bot_type=3", req)
	}
}

func TestProtocolQRPostFormAndBindedRedirect(t *testing.T) {
	srv := NewHTTPServer()
	base := srv.Start()
	defer srv.Close()

	srv.Engine().MarkBoundToken("bound-token")

	status, body := postJSON(t, base+"/ilink/bot/get_bot_qrcode?bot_type=3",
		`{"local_token_list": ["other-token", "bound-token"]}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %v", status, body)
	}
	qrCode, _ := body["qrcode"].(string)
	if qrCode == "" {
		t.Fatalf("qrcode missing in %v", body)
	}

	req := srv.Engine().LastQRRequest()
	if req.Method != http.MethodPost || req.BotType != "3" {
		t.Errorf("last QR request = %+v, want POST bot_type=3", req)
	}
	if len(req.LocalTokenList) != 2 {
		t.Errorf("local_token_list = %v, want both tokens", req.LocalTokenList)
	}

	client := ilink.NewClient("", ilink.WithBaseURL(base))
	st, err := client.PollQRStatus(context.Background(), qrCode)
	if err != nil {
		t.Fatalf("PollQRStatus: %v", err)
	}
	if st.Status != QRStateBindedRedirect {
		t.Errorf("status = %q, want %q", st.Status, QRStateBindedRedirect)
	}
}

func TestProtocolQRVerifyCode(t *testing.T) {
	srv := NewHTTPServer()
	base := srv.Start()
	defer srv.Close()

	qr, err := srv.Engine().FetchQRCode()
	if err != nil {
		t.Fatalf("FetchQRCode: %v", err)
	}

	srv.Engine().SetQRState(QRStateNeedVerifyCode, "", "7788")
	if got := rawPollQR(t, base, qr.QRCode, "")["status"]; got != QRStateNeedVerifyCode {
		t.Errorf("status = %v, want %v (returned without blocking)", got, QRStateNeedVerifyCode)
	}

	if got := rawPollQR(t, base, qr.QRCode, "0000")["status"]; got != QRStateVerifyCodeBlocked {
		t.Errorf("status with wrong code = %v, want %v", got, QRStateVerifyCodeBlocked)
	}
	// Blocked is terminal.
	if got := rawPollQR(t, base, qr.QRCode, "7788")["status"]; got != QRStateVerifyCodeBlocked {
		t.Errorf("status after block = %v, want %v", got, QRStateVerifyCodeBlocked)
	}

	// A fresh session with credentials advances on the correct code.
	qr2, err := srv.Engine().FetchQRCode()
	if err != nil {
		t.Fatalf("FetchQRCode: %v", err)
	}
	srv.Engine().ConfirmQR(Credentials{BotID: "bot-v", BotToken: "tok-v", ILinkUserID: "u-v"})
	srv.Engine().SetQRState(QRStateNeedVerifyCode, "", "7788")

	body := rawPollQR(t, base, qr2.QRCode, "7788")
	if body["status"] != QRStateConfirmed {
		t.Fatalf("status = %v, want %v", body["status"], QRStateConfirmed)
	}
	if body["bot_token"] != "tok-v" || body["ilink_bot_id"] != "bot-v" {
		t.Errorf("credentials = %v, want tok-v/bot-v", body)
	}
}

func TestProtocolQRScanedButRedirect(t *testing.T) {
	srv := NewHTTPServer()
	base := srv.Start()
	defer srv.Close()

	qr, err := srv.Engine().FetchQRCode()
	if err != nil {
		t.Fatalf("FetchQRCode: %v", err)
	}

	srv.Engine().SetQRState(QRStateScanedButRedirect, "mock2.ilink", "")
	body := rawPollQR(t, base, qr.QRCode, "")
	if body["status"] != QRStateScanedButRedirect {
		t.Fatalf("status = %v, want %v", body["status"], QRStateScanedButRedirect)
	}
	if body["redirect_host"] != "mock2.ilink" {
		t.Errorf("redirect_host = %v, want mock2.ilink", body["redirect_host"])
	}

	// The session continues after the redirect notification: a poll now blocks
	// (waiting for confirmation) instead of repeating the redirect forever.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	client := ilink.NewClient("", ilink.WithBaseURL(base))
	if _, err := client.PollQRStatus(ctx, qr.QRCode); err == nil {
		t.Error("expected the poll to block after the redirect notification")
	}
}

// TestProtocolMockProviderPassesThroughQRStates checks that the mock provider
// (used by tests of the hub itself) forwards the upstream QR states verbatim.
func TestProtocolMockProviderPassesThroughQRStates(t *testing.T) {
	p := NewProvider()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, err := p.StartBind(ctx)
	if err != nil {
		t.Fatalf("StartBind: %v", err)
	}

	p.Engine().SetQRState(QRStateNeedVerifyCode, "", "1234")
	res, err := session.PollStatus(ctx)
	if err != nil {
		t.Fatalf("PollStatus: %v", err)
	}
	if res.Status != QRStateNeedVerifyCode {
		t.Errorf("status = %q, want %q", res.Status, QRStateNeedVerifyCode)
	}

	p.Engine().SetQRState(QRStateScanedButRedirect, "other.host", "")
	res, err = session.PollStatus(ctx)
	if err != nil {
		t.Fatalf("PollStatus: %v", err)
	}
	if res.Status != QRStateScanedButRedirect {
		t.Errorf("status = %q, want %q", res.Status, QRStateScanedButRedirect)
	}
}

// --- media / CDN ---

func TestProtocolInboundMediaFields(t *testing.T) {
	srv := NewHTTPServer()
	base := srv.Start()
	defer srv.Close()

	data := []byte("image-bytes")
	status, body := postJSON(t, base+"/mock/inbound", `{
		"sender": "u3",
		"items": [{
			"type": "image",
			"data": "`+base64.StdEncoding.EncodeToString(data)+`",
			"file_name": "p.png",
			"url": "https://cdn.example/p.png"
		}]
	}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %v", status, body)
	}

	resp := rawGetUpdates(t, base, "")
	if len(resp.Msgs) != 1 || len(resp.Msgs[0].ItemList) != 1 {
		t.Fatalf("unexpected messages: %+v", resp.Msgs)
	}
	item := string(resp.Msgs[0].ItemList[0])

	if !strings.Contains(item, `"url":"https://cdn.example/p.png"`) {
		t.Errorf("image_item.url missing: %s", item)
	}

	var parsed struct {
		ImageItem struct {
			AESKey  string `json:"aeskey"`
			MidSize int64  `json:"mid_size"`
			Media   struct {
				AESKey  string `json:"aes_key"`
				FullURL string `json:"full_url"`
			} `json:"media"`
		} `json:"image_item"`
	}
	if err := jsonx.Unmarshal(resp.Msgs[0].ItemList[0], &parsed); err != nil {
		t.Fatalf("unmarshal image item: %v", err)
	}

	if len(parsed.ImageItem.AESKey) != 32 {
		t.Errorf("image_item.aeskey = %q, want 32 hex chars", parsed.ImageItem.AESKey)
	}
	decoded, err := base64.StdEncoding.DecodeString(parsed.ImageItem.Media.AESKey)
	if err != nil || len(decoded) != 32 || string(decoded) != parsed.ImageItem.AESKey {
		t.Errorf("media.aes_key = %q (decoded %q, err %v), want base64(hex) of image_item.aeskey",
			parsed.ImageItem.Media.AESKey, decoded, err)
	}
	if parsed.ImageItem.MidSize == 0 {
		t.Error("image_item.mid_size = 0, want the ciphertext size")
	}
}

func TestProtocolUploadFullURLPath(t *testing.T) {
	srv := NewHTTPServer()
	base := srv.Start()
	defer srv.Close()

	// The server advertises a CDN base, the client is configured with a bogus
	// one: only the upload_full_url / full_url fields can make this work.
	srv.Engine().SetCDNBaseURL(base + "/c2c")
	client := ilink.NewClient("t", ilink.WithBaseURL(base), ilink.WithCDNBaseURL(base+"/bogus-cdn"))

	ctx := context.Background()
	plaintext := []byte("payload uploaded through upload_full_url")
	res, err := client.UploadFile(ctx, plaintext, "u1", ilink.MediaFile)
	if err != nil {
		t.Fatalf("UploadFile via upload_full_url: %v", err)
	}

	req := srv.Engine().LastUploadRequest()
	if req == nil {
		t.Fatal("getuploadurl request was not recorded")
	}
	if !req.NoNeedThumb {
		t.Error("no_need_thumb = false, want true")
	}
	if req.MediaType != int(ilink.MediaFile) {
		t.Errorf("media_type = %d, want %d", req.MediaType, ilink.MediaFile)
	}
	if req.RawSize != int64(len(plaintext)) {
		t.Errorf("rawsize = %d, want %d", req.RawSize, len(plaintext))
	}
	if want := int64(ilink.AESECBPaddedSize(len(plaintext))); req.FileSize != want {
		t.Errorf("filesize = %d, want %d (padded)", req.FileSize, want)
	}
	if want := fmt.Sprintf("%x", md5.Sum(plaintext)); req.RawFileMD5 != want {
		t.Errorf("rawfilemd5 = %q, want %q", req.RawFileMD5, want)
	}
	if req.AESKey != res.AESKey || len(req.AESKey) != 32 {
		t.Errorf("aeskey = %q, want the 32-char hex key %q", req.AESKey, res.AESKey)
	}

	// Download through an explicit full_url.
	fullURL := base + "/c2c/download?encrypted_query_param=" + url.QueryEscape(res.DownloadEncryptedQueryParam)
	got, err := client.DownloadMedia(ctx, &ilink.CDNMedia{
		FullURL: fullURL,
		AESKey:  base64.StdEncoding.EncodeToString([]byte(res.AESKey)),
	})
	if err != nil {
		t.Fatalf("DownloadMedia via full_url: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("downloaded %d bytes, want %d", len(got), len(plaintext))
	}
}

func TestProtocolCDNUpload4xxAborts(t *testing.T) {
	srv := NewHTTPServer()
	base := srv.Start()
	defer srv.Close()

	srv.Engine().SetCDNUploadStatus(http.StatusBadRequest)
	client := ilink.NewClient("t", ilink.WithBaseURL(base), ilink.WithCDNBaseURL(base+"/c2c"))

	start := time.Now()
	if _, err := client.UploadFile(context.Background(), []byte("boom"), "u1", ilink.MediaFile); err == nil {
		t.Fatal("expected the upload to fail with 400")
	}
	// 4xx aborts before the 2s retry delay.
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("upload took %v, want an immediate abort on 4xx", elapsed)
	}
}
