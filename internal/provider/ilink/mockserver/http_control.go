package mockserver

import (
	"net/http"

	"github.com/openilink/openilink-hub/internal/jsonx"
)

// --- Control endpoint handlers ---

func (s *HTTPServer) handleMockInbound(w http.ResponseWriter, r *http.Request) {
	var req InboundRequest
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request: " + err.Error()})
		return
	}
	s.engine.InjectInbound(req)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *HTTPServer) handleMockSent(w http.ResponseWriter, r *http.Request) {
	msgs := s.engine.SentMessages()
	if msgs == nil {
		msgs = []SentMessage{}
	}
	writeJSON(w, http.StatusOK, msgs)
}

func (s *HTTPServer) handleMockScan(w http.ResponseWriter, r *http.Request) {
	s.engine.ScanQR()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *HTTPServer) handleMockConfirm(w http.ResponseWriter, r *http.Request) {
	var creds Credentials
	if err := jsonx.NewDecoder(r.Body).Decode(&creds); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request: " + err.Error()})
		return
	}
	s.engine.ConfirmQR(creds)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleMockQRState forces any upstream QR state so every branch of the state
// machine can be exercised:
//
//	{"status":"need_verifycode","verify_code":"1234"}
//	{"status":"scaned_but_redirect","redirect_host":"mock2.ilink"}
func (s *HTTPServer) handleMockQRState(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Status       string `json:"status"`
		RedirectHost string `json:"redirect_host,omitempty"`
		VerifyCode   string `json:"verify_code,omitempty"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request: " + err.Error()})
		return
	}
	if req.Status == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "status is required"})
		return
	}
	s.engine.SetQRState(req.Status, req.RedirectHost, req.VerifyCode)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleMockBind marks a bot token as already bound, which makes a later
// get_bot_qrcode with that token in local_token_list return binded_redirect.
func (s *HTTPServer) handleMockBind(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BotToken string `json:"bot_token"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request: " + err.Error()})
		return
	}
	s.engine.MarkBoundToken(req.BotToken)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *HTTPServer) handleMockExpire(w http.ResponseWriter, r *http.Request) {
	s.engine.ExpireSession()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *HTTPServer) handleMockListMedia(w http.ResponseWriter, r *http.Request) {
	media := s.engine.ListMedia()
	if media == nil {
		media = []MediaInfo{}
	}
	writeJSON(w, http.StatusOK, media)
}

func (s *HTTPServer) handleMockReset(w http.ResponseWriter, r *http.Request) {
	s.engine.Reset()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
