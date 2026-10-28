package mockserver_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/openilink/openilink-hub/internal/jsonx"
	"github.com/openilink/openilink-hub/internal/provider"
	ilinkProvider "github.com/openilink/openilink-hub/internal/provider/ilink"
	"github.com/openilink/openilink-hub/internal/provider/ilink/mockserver"
)

// startProviderWithMock starts the real iLink provider against the mock HTTP
// server and returns it together with a channel of inbound messages.
func startProviderWithMock(t *testing.T, srv *mockserver.HTTPServer, base string) (*ilinkProvider.Provider, <-chan provider.InboundMessage) {
	t.Helper()

	srv.Engine().SetToken("test-token")
	srv.Engine().SetStatus("connected")

	creds, _ := jsonx.Marshal(ilinkProvider.Credentials{BotToken: "test-token", BaseURL: base})

	inbox := make(chan provider.InboundMessage, 8)
	ctx, cancel := context.WithCancel(context.Background())

	p := &ilinkProvider.Provider{}
	if err := p.Start(ctx, provider.StartOptions{
		Credentials:  creds,
		OnMessage:    func(msg provider.InboundMessage) { inbox <- msg },
		OnStatus:     func(string) {},
		OnSyncUpdate: func(jsonx.RawMessage) {},
	}); err != nil {
		cancel()
		t.Fatalf("provider Start: %v", err)
	}
	t.Cleanup(func() {
		p.Stop()
		cancel()
	})

	return p, inbox
}

func nextInbound(t *testing.T, inbox <-chan provider.InboundMessage) provider.InboundMessage {
	t.Helper()
	select {
	case msg := <-inbox:
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for inbound message")
		return provider.InboundMessage{}
	}
}

// TestHTTPModeMediaFullURLDownload proves the mock's full_url support makes the
// real provider's media download work: the provider client keeps the production
// CDN base URL, so only media.full_url can point it back at the mock.
func TestHTTPModeMediaFullURLDownload(t *testing.T) {
	srv := mockserver.NewHTTPServer()
	base := srv.Start()
	// Registered before the provider so cleanup stops the poller first.
	t.Cleanup(srv.Close)
	srv.Engine().SetCDNBaseURL(base + "/c2c")

	p, inbox := startProviderWithMock(t, srv, base)

	imageData := []byte("fake-png-bytes-downloaded-through-full-url")
	srv.Engine().InjectInbound(mockserver.InboundRequest{
		Sender: "user1@wx",
		Items: []mockserver.ItemRequest{
			{Type: "image", Data: imageData, FileName: "photo.png"},
		},
	})

	msg := nextInbound(t, inbox)
	if len(msg.Items) == 0 || msg.Items[0].Media == nil {
		t.Fatalf("unexpected items: %+v", msg.Items)
	}
	media := msg.Items[0].Media
	if media.URL == "" {
		t.Fatal("media full_url was not propagated to provider.Media.URL")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	downloaded, err := p.DownloadMedia(ctx, media)
	if err != nil {
		t.Fatalf("provider media download via full_url: %v", err)
	}
	if !bytes.Equal(downloaded, imageData) {
		t.Errorf("downloaded %d bytes, want %d", len(downloaded), len(imageData))
	}
}

// TestHTTPModeInboundItemMetadata checks ref_msg, voice transcription and the
// media key encoding injected by the mock.
func TestHTTPModeInboundItemMetadata(t *testing.T) {
	srv := mockserver.NewHTTPServer()
	base := srv.Start()
	// Registered before the provider so cleanup stops the poller first.
	t.Cleanup(srv.Close)
	srv.Engine().SetCDNBaseURL(base + "/c2c")

	_, inbox := startProviderWithMock(t, srv, base)

	voiceData := []byte("silk-bytes")
	srv.Engine().InjectInbound(mockserver.InboundRequest{
		Sender: "user2@wx",
		Items: []mockserver.ItemRequest{
			{Type: "voice", Text: "语音转写文本", PlayTime: 1234, SampleRate: 16000, Data: voiceData, FileName: "v.silk"},
			{Type: "text", Text: "收到", Ref: &mockserver.ItemRequest{Type: "text", Text: "原始消息"}, RefTitle: "引用"},
		},
	})

	msg := nextInbound(t, inbox)
	if len(msg.Items) != 2 {
		t.Fatalf("items = %d, want 2 (%+v)", len(msg.Items), msg.Items)
	}

	voice := msg.Items[0]
	if voice.Type != "voice" || voice.Text != "语音转写文本" {
		t.Errorf("voice item = %+v, want transcription text", voice)
	}
	if voice.Media == nil || voice.Media.PlayTime != 1234 {
		t.Errorf("voice media = %+v, want play_time 1234", voice.Media)
	}

	// Upstream encodes media.aes_key as base64 of the 32-char hex key.
	if voice.Media != nil {
		decoded, err := base64.StdEncoding.DecodeString(voice.Media.AESKey)
		if err != nil || len(decoded) != 32 || !isHexString(string(decoded)) {
			t.Errorf("media.aes_key = %q (decoded %q, err %v), want base64(hex)", voice.Media.AESKey, decoded, err)
		}
	}

	reply := msg.Items[1]
	if reply.RefMsg == nil {
		t.Fatalf("ref_msg missing on %+v", reply)
	}
	if reply.RefMsg.Title != "引用" || reply.RefMsg.Item.Text != "原始消息" {
		t.Errorf("ref_msg = %+v, want title 引用 / text 原始消息", reply.RefMsg)
	}
}

func isHexString(s string) bool {
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}
