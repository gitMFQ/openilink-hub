package mockserver

import (
	"errors"
	"io"
	"net/http"

	ilink "github.com/openilink/openilink-sdk-go"

	"github.com/openilink/openilink-hub/internal/jsonx"
)

// --- SDK endpoint handlers ---

// apiMessage is the wire form of a message. It embeds the SDK message but
// replaces item_list so items with no SDK equivalent (types 11/12, i.e.
// tool_call_start / tool_call_result) can be passed through unchanged.
type apiMessage struct {
	*ilink.WeixinMessage
	ItemList []jsonx.RawMessage `json:"item_list,omitempty"`
}

func (s *HTTPServer) handleGetUpdates(w http.ResponseWriter, r *http.Request) {
	var req ilink.GetUpdatesReq
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ret": -1, "errmsg": "bad request"})
		return
	}

	result, err := s.engine.GetUpdates(r.Context(), req.GetUpdatesBuf)
	if err != nil {
		// Session expired returns errcode -14, matching SDK expectations.
		if err.Error() == "session expired" {
			writeJSON(w, http.StatusOK, map[string]any{
				"ret":     0,
				"errcode": -14,
				"errmsg":  "session expired",
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ret":    0,
			"msgs":   []any{},
			"errmsg": err.Error(),
		})
		return
	}

	msgs := make([]apiMessage, 0, len(result.Messages))
	for _, m := range result.Messages {
		items := make([]jsonx.RawMessage, 0, len(m.ItemList)+1)
		for _, item := range m.ItemList {
			raw, err := jsonx.Marshal(item)
			if err != nil {
				continue
			}
			items = append(items, raw)
		}
		items = append(items, s.engine.RawItemsFor(m.MessageID)...)
		msgs = append(msgs, apiMessage{WeixinMessage: m, ItemList: items})
	}

	// The cursor the client must send back. Upstream deprecated sync_buf, but
	// it is still emitted for older clients that read it.
	cursor := result.SyncBuf
	if cursor == "" {
		cursor = req.GetUpdatesBuf
	}
	resp := map[string]any{
		"ret":             0,
		"msgs":            msgs,
		"get_updates_buf": cursor,
	}
	if result.SyncBuf != "" {
		resp["sync_buf"] = result.SyncBuf
	}
	if result.LongPollingTimeoutMs > 0 {
		resp["longpolling_timeout_ms"] = result.LongPollingTimeoutMs
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *HTTPServer) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ret": -1, "errmsg": "read body failed"})
		return
	}

	var req ilink.SendMessageReq
	if err := jsonx.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ret": -1, "errmsg": "bad request"})
		return
	}

	if req.Msg == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ret": -1, "errmsg": "missing msg"})
		return
	}

	// run_id and base_info have no field in the SDK message types; read them
	// from the raw body so they can be recorded.
	var meta struct {
		Msg struct {
			RunID string `json:"run_id"`
		} `json:"msg"`
		BaseInfo *ilink.BaseInfo `json:"base_info"`
	}
	_ = jsonx.Unmarshal(body, &meta)

	// Upstream reports business failures with HTTP 200 + non-zero ret.
	// Note: SDK v0.6.0 does not parse ret, see docs/ilink-protocol-sync.md.
	if req.Msg.ToUserID == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ret": -1, "errmsg": "missing to_user_id"})
		return
	}

	if err := s.engine.SendMessageWithMeta(req.Msg, SendMeta{
		RunID:    meta.Msg.RunID,
		BaseInfo: meta.BaseInfo,
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ret": -1, "errmsg": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ret": 0, "errmsg": ""})
}

func (s *HTTPServer) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	var req ilink.GetConfigReq
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ret": -1, "errmsg": "bad request"})
		return
	}

	result, err := s.engine.GetConfig(req.ILinkUserID, req.ContextToken)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ret": -1, "errmsg": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ret":           0,
		"typing_ticket": result.TypingTicket,
	})
}

func (s *HTTPServer) handleSendTyping(w http.ResponseWriter, r *http.Request) {
	var req ilink.SendTypingReq
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ret": -1, "errmsg": "bad request"})
		return
	}

	typing := req.Status == ilink.Typing
	if err := s.engine.SendTyping(req.ILinkUserID, req.TypingTicket, typing); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ret": -1, "errmsg": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ret": 0})
}

func (s *HTTPServer) handleGetUploadURL(w http.ResponseWriter, r *http.Request) {
	var req ilink.GetUploadURLReq
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ret": -1, "errmsg": "bad request"})
		return
	}

	resp, err := s.engine.GetUploadURL(&req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ret": -1, "errmsg": err.Error()})
		return
	}

	out := map[string]any{
		"ret":          resp.Ret,
		"upload_param": resp.UploadParam,
	}
	if resp.UploadFullURL != "" {
		out["upload_full_url"] = resp.UploadFullURL
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *HTTPServer) handleCDNUpload(w http.ResponseWriter, r *http.Request) {
	if status := s.engine.CDNUploadStatus(); status != 0 {
		http.Error(w, "injected cdn failure", status)
		return
	}

	uploadParam := r.URL.Query().Get("encrypted_query_param")
	filekey := r.URL.Query().Get("filekey")

	if uploadParam == "" {
		http.Error(w, "missing encrypted_query_param", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 100<<20)) // 100MB max
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}

	downloadEQP, err := s.engine.UploadToCDN(uploadParam, filekey, body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("x-encrypted-param", downloadEQP)
	w.WriteHeader(http.StatusOK)
}

func (s *HTTPServer) handleCDNDownload(w http.ResponseWriter, r *http.Request) {
	eqp := r.URL.Query().Get("encrypted_query_param")
	if eqp == "" {
		http.Error(w, "missing encrypted_query_param", http.StatusBadRequest)
		return
	}

	// Return raw ciphertext; the SDK decrypts client-side.
	ciphertext, err := s.engine.DownloadRaw(eqp)
	if err != nil {
		http.Error(w, "media not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	w.Write(ciphertext)
}

func (s *HTTPServer) handleFetchQR(w http.ResponseWriter, r *http.Request) {
	// POST form (upstream protocol): {"local_token_list":[...]}; an empty body
	// and the SDK's bare GET are both accepted.
	var req struct {
		LocalTokenList []string `json:"local_token_list"`
	}
	if r.Method == http.MethodPost && r.Body != nil {
		if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"errmsg": "bad request"})
			return
		}
	}

	s.engine.recordQRRequest(r.Method, r.URL.Query().Get("bot_type"))

	result, err := s.engine.FetchQRCodeWithTokens(req.LocalTokenList)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"errmsg": err.Error()})
		return
	}

	// qrcode_img_content is the content to render as a QR code (a URL or
	// payload), not an image: the clients encode it into a QR image.
	writeJSON(w, http.StatusOK, map[string]any{
		"qrcode":             result.QRCode,
		"qrcode_img_content": result.QRContent,
	})
}

func (s *HTTPServer) handlePollQR(w http.ResponseWriter, r *http.Request) {
	qrCode := r.URL.Query().Get("qrcode")
	if qrCode == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"status": "error", "errmsg": "missing qrcode"})
		return
	}

	result, err := s.engine.PollQRStatus(r.Context(), qrCode, r.URL.Query().Get("verify_code"))
	if err != nil {
		// On context cancellation (long-poll timeout), return "wait".
		if r.Context().Err() != nil {
			writeJSON(w, http.StatusOK, map[string]any{"status": "wait"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"status": "error", "errmsg": err.Error()})
		return
	}

	resp := map[string]any{"status": result.Status}

	// The SDK uses "scaned" (one 'n') not "scanned".
	if result.Status == QRStateScanned {
		resp["status"] = QRStateScaned
	}
	if result.RedirectHost != "" {
		resp["redirect_host"] = result.RedirectHost
	}

	if result.Creds != nil {
		resp["bot_token"] = result.Creds.BotToken
		resp["ilink_bot_id"] = result.Creds.BotID
		resp["baseurl"] = result.Creds.BaseURL
		resp["ilink_user_id"] = result.Creds.ILinkUserID
	}

	writeJSON(w, http.StatusOK, resp)
}

// writeJSON marshals v as JSON and writes it to w.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	jsonx.NewEncoder(w).Encode(v)
}
