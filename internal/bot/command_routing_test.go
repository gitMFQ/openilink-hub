package bot

import (
	"sync"
	"testing"
	"time"

	appdelivery "github.com/openilink/openilink-hub/internal/app"
	"github.com/openilink/openilink-hub/internal/jsonx"
	"github.com/openilink/openilink-hub/internal/store"
	"github.com/openilink/openilink-hub/internal/store/memstore"
)

// recordingTraceStore captures flushed spans so tests can assert on the trace
// events a delivery path adds (e.g. that an unmatched bare message adds none).
type recordingTraceStore struct {
	noopTraceStore
	mu    sync.Mutex
	spans []recordedSpan
}

type recordedSpan struct {
	name   string
	events []byte
}

func (r *recordingTraceStore) InsertSpan(traceID, spanID, parentSpanID, name, kind, statusCode, statusMessage string,
	startTime, endTime int64, attrsJSON, eventsJSON []byte, botID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spans = append(r.spans, recordedSpan{name: name, events: append([]byte(nil), eventsJSON...)})
	return nil
}

// rootEventNames returns the event names recorded on the "test" root span.
func (r *recordingTraceStore) rootEventNames(t *testing.T) []string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var names []string
	for _, s := range r.spans {
		if s.name != "test" {
			continue
		}
		var events []store.SpanEvent
		if err := jsonx.Unmarshal(s.events, &events); err != nil {
			t.Fatalf("unmarshal span events: %v", err)
		}
		for _, e := range events {
			names = append(names, e.Name)
		}
	}
	return names
}

// routeInstallation describes one app installation for the routing tests.
type routeInstallation struct {
	appID     string
	instID    string
	handle    string
	instTools string // installation-level tools JSON (marketplace/builtin apps)
	appTools  string // app-level tools JSON (local apps)
	events    string // app-level event subscriptions
	enabled   bool
}

// rawJSON returns raw if non-empty, otherwise fallback.
func rawJSON(raw, fallback string) jsonx.RawMessage {
	if raw == "" {
		raw = fallback
	}
	return jsonx.RawMessage(raw)
}

// setupRouting builds a Manager over a memstore holding the given installations
// (each subscribed to message events unless told otherwise) with one active WS
// connection per installation.
func setupRouting(t *testing.T, insts ...routeInstallation) (*Manager, *recordingTraceStore, *store.Tracer, *store.SpanBuilder, map[string]chan []byte) {
	t.Helper()
	const botID = "bot-cmd-route"

	ms := memstore.New()
	hub := appdelivery.NewWSHub()
	chans := make(map[string]chan []byte, len(insts))

	for _, ri := range insts {
		events := rawJSON(ri.events, `["message"]`)
		scopes := jsonx.RawMessage(`["message:read"]`)
		ms.AddApp(&store.App{
			ID: ri.appID, Slug: ri.appID, Status: "active",
			Events: events, Scopes: scopes, Tools: rawJSON(ri.appTools, `[]`),
		})
		ms.AddInstallation(&store.AppInstallation{
			ID: ri.instID, AppID: ri.appID, BotID: botID, Handle: ri.handle,
			AppSlug: ri.appID, AppName: ri.appID, Enabled: ri.enabled,
			Scopes: scopes, Tools: rawJSON(ri.instTools, `[]`),
		})
		ch := make(chan []byte, 8)
		hub.Register(ri.instID, &appdelivery.WSConn{InstID: ri.instID, BotID: botID, Send: ch})
		chans[ri.instID] = ch
	}

	rec := &recordingTraceStore{}
	tracer := store.NewTracer(rec, botID)
	root := tracer.Start("test", store.SpanKindInternal, nil)
	return newTestManager(ms, hub), rec, tracer, root, chans
}

// nextWSEvent waits for the next WS envelope and returns its event type/data.
func nextWSEvent(t *testing.T, ch chan []byte) (string, map[string]any) {
	t.Helper()
	select {
	case data := <-ch:
		var env map[string]any
		if err := jsonx.Unmarshal(data, &env); err != nil {
			t.Fatalf("unmarshal ws payload: %v", err)
		}
		event, _ := env["event"].(map[string]any)
		typ, _ := event["type"].(string)
		payload, _ := event["data"].(map[string]any)
		return typ, payload
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for a WS event")
		return "", nil
	}
}

// expectNoWSEvent fails if any event was delivered to the channel.
func expectNoWSEvent(t *testing.T, ch chan []byte) {
	t.Helper()
	select {
	case data := <-ch:
		t.Fatalf("unexpected WS event: %s", data)
	case <-time.After(150 * time.Millisecond):
	}
}

// runnerInstall is a Runner-shaped installation: command triggers live in
// per-installation tools and it only subscribes to the command event.
func runnerInstall(handle string, enabled bool, commands ...string) routeInstallation {
	tools := make([]store.AppTool, 0, len(commands))
	for _, c := range commands {
		tools = append(tools, store.AppTool{Name: c, Command: c})
	}
	raw, _ := jsonx.Marshal(tools)
	return routeInstallation{
		appID: "app-runner-" + handle, instID: "inst-runner-" + handle, handle: handle,
		instTools: string(raw), events: `["command"]`, enabled: enabled,
	}
}

// chatInstall is an ordinary chat app subscribed to message.* events.
func chatInstall(handle string) routeInstallation {
	return routeInstallation{
		appID: "app-chat-" + handle, instID: "inst-chat-" + handle, handle: handle,
		events: `["message"]`, enabled: true,
	}
}

// TestCommandRouting_MentionSlashPrefixStillWorks is the no-regression guard for
// the original behavior: "@handle /cmd args" must keep producing a command event
// with the command name stripped of "/".
func TestCommandRouting_MentionSlashPrefixStillWorks(t *testing.T) {
	m, _, tracer, root, chans := setupRouting(t, runnerInstall("runner", true, "hn"))
	msg, p := textMessage("user-1", "@runner /hn top")
	m.deliverToApps(&Instance{DBID: "bot-cmd-route"}, msg, p, tracer, root)

	typ, data := nextWSEvent(t, chans["inst-runner-runner"])
	if typ != "command" {
		t.Errorf("event type = %q, want command", typ)
	}
	if data["command"] != "hn" {
		t.Errorf("command = %v, want %q (no slash)", data["command"], "hn")
	}
	if data["text"] != "top" {
		t.Errorf("text = %v, want %q", data["text"], "top")
	}
}

// TestCommandRouting_MentionBareCommand covers "@handle cmd args" without "/":
// the command is delivered when the mentioned installation registers the trigger.
func TestCommandRouting_MentionBareCommand(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantCommand string
		wantText    string
	}{
		{"bare", "@runner hn top", "hn", "top"},
		{"bare no args", "@runner hn", "hn", ""},
		{"bare uppercase", "@runner HN top", "hn", "top"},
		{"slash still works", "@runner /hn top", "hn", "top"},
		{"unicode whitespace", "@runner\u2005hn\u2005top", "hn", "top"},
		{"full-width whitespace", "@runner\u3000hn\u3000top", "hn", "top"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, _, tracer, root, chans := setupRouting(t, runnerInstall("runner", true, "hn"))
			msg, p := textMessage("user-1", tt.content)
			m.deliverToApps(&Instance{DBID: "bot-cmd-route"}, msg, p, tracer, root)

			typ, data := nextWSEvent(t, chans["inst-runner-runner"])
			if typ != "command" {
				t.Fatalf("event type = %q, want command", typ)
			}
			if data["command"] != tt.wantCommand {
				t.Errorf("command = %v, want %q", data["command"], tt.wantCommand)
			}
			if data["text"] != tt.wantText {
				t.Errorf("text = %v, want %q", data["text"], tt.wantText)
			}
			if data["handle"] != "runner" {
				t.Errorf("handle = %v, want runner", data["handle"])
			}
		})
	}
}

// TestCommandRouting_MentionUnregisteredStaysMessageText locks the fallback for
// "@handle <ordinary text>": it must stay message.text and add no command.
func TestCommandRouting_MentionUnregisteredStaysMessageText(t *testing.T) {
	m, rec, tracer, root, chans := setupRouting(t, runnerInstall("runner", true, "hn"))
	msg, p := textMessage("user-1", "@runner 今天天气怎么样")
	m.deliverToApps(&Instance{DBID: "bot-cmd-route"}, msg, p, tracer, root)

	typ, data := nextWSEvent(t, chans["inst-runner-runner"])
	if typ != "message.text" {
		t.Errorf("event type = %q, want message.text", typ)
	}
	if data["content"] != "今天天气怎么样" {
		t.Errorf("content = %v, want %q", data["content"], "今天天气怎么样")
	}
	if data["handle"] != "runner" {
		t.Errorf("handle = %v, want runner", data["handle"])
	}

	tracer.Flush()
	if names := rec.rootEventNames(t); len(names) != 1 || names[0] != "match_handle" {
		t.Errorf("root trace events = %v, want [match_handle] (unchanged)", names)
	}
}

// TestCommandRouting_BareCommand covers a bare "cmd args" message: it routes as
// a command to the installation that registers the trigger, and the ordinary
// chat app must not receive a message.* event.
func TestCommandRouting_BareCommand(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantCommand string
		wantText    string
	}{
		{"bare", "hn top", "hn", "top"},
		{"bare no args", "hn", "hn", ""},
		{"bare uppercase", "HN top", "hn", "top"},
		{"bare slash form", "/hn top", "hn", "top"},
		{"bare unicode whitespace", "hn\u2005top", "hn", "top"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, rec, tracer, root, chans := setupRouting(t,
				runnerInstall("runner", true, "hn"),
				chatInstall("chat"),
			)
			msg, p := textMessage("user-1", tt.content)
			m.deliverToApps(&Instance{DBID: "bot-cmd-route"}, msg, p, tracer, root)

			typ, data := nextWSEvent(t, chans["inst-runner-runner"])
			if typ != "command" {
				t.Fatalf("event type = %q, want command", typ)
			}
			if data["command"] != tt.wantCommand {
				t.Errorf("command = %v, want %q", data["command"], tt.wantCommand)
			}
			if data["text"] != tt.wantText {
				t.Errorf("text = %v, want %q", data["text"], tt.wantText)
			}
			if _, ok := data["handle"]; ok {
				t.Errorf("bare command must not carry a handle, got %v", data["handle"])
			}

			expectNoWSEvent(t, chans["inst-chat-chat"])

			tracer.Flush()
			if names := rec.rootEventNames(t); len(names) != 1 || names[0] != "match_command" {
				t.Errorf("root trace events = %v, want [match_command]", names)
			}
		})
	}
}

// TestCommandRouting_BareNonTriggerFallsBackToMessage is the core safety guard:
// a bare message whose first word is not a registered trigger must run the
// unchanged message.* flow, with the same event payload and trace events as
// before (no match_command event).
func TestCommandRouting_BareNonTriggerFallsBackToMessage(t *testing.T) {
	const content = "hello world 今天天气怎么样"

	t.Run("with an unrelated command registered", func(t *testing.T) {
		m, rec, tracer, root, chans := setupRouting(t,
			runnerInstall("runner", true, "hn"),
			chatInstall("chat"),
		)
		msg, p := textMessage("user-1", content)
		m.deliverToApps(&Instance{DBID: "bot-cmd-route"}, msg, p, tracer, root)

		typ, data := nextWSEvent(t, chans["inst-chat-chat"])
		if typ != "message.text" {
			t.Fatalf("event type = %q, want message.text", typ)
		}
		if data["content"] != content {
			t.Errorf("content = %v, want %q", data["content"], content)
		}
		expectNoWSEvent(t, chans["inst-runner-runner"])

		tracer.Flush()
		if names := rec.rootEventNames(t); len(names) != 0 {
			t.Errorf("root trace events = %v, want none (command fallback must not add events)", names)
		}
	})

	t.Run("without any command registered", func(t *testing.T) {
		// Control run: same message on a bot where nothing registers commands.
		m, rec, tracer, root, chans := setupRouting(t,
			runnerInstall("runner", true),
			chatInstall("chat"),
		)
		msg, p := textMessage("user-1", content)
		m.deliverToApps(&Instance{DBID: "bot-cmd-route"}, msg, p, tracer, root)

		typ, data := nextWSEvent(t, chans["inst-chat-chat"])
		if typ != "message.text" {
			t.Fatalf("event type = %q, want message.text", typ)
		}
		if data["content"] != content {
			t.Errorf("content = %v, want %q", data["content"], content)
		}
		tracer.Flush()
		if names := rec.rootEventNames(t); len(names) != 0 {
			t.Errorf("root trace events = %v, want none", names)
		}
	})
}

// TestCommandRouting_MentionPriority verifies @handle routing still wins: a
// message addressed to one installation is never re-routed to another
// installation that happens to register the same first word as a command.
func TestCommandRouting_MentionPriority(t *testing.T) {
	setup := func(t *testing.T) (*Manager, *store.Tracer, *store.SpanBuilder, map[string]chan []byte) {
		t.Helper()
		m, _, tracer, root, chans := setupRouting(t,
			runnerInstall("aa", true, "echo"),
			runnerInstall("bb", true),
		)
		return m, tracer, root, chans
	}

	t.Run("addressed installation without the trigger gets message.text", func(t *testing.T) {
		m, tracer, root, chans := setup(t)
		msg, p := textMessage("user-1", "@bb echo hi")
		m.deliverToApps(&Instance{DBID: "bot-cmd-route"}, msg, p, tracer, root)

		typ, data := nextWSEvent(t, chans["inst-runner-bb"])
		if typ != "message.text" {
			t.Errorf("event type = %q, want message.text", typ)
		}
		if data["content"] != "echo hi" {
			t.Errorf("content = %v, want %q", data["content"], "echo hi")
		}
		// The installation that does register "echo" must stay untouched.
		expectNoWSEvent(t, chans["inst-runner-aa"])
	})

	t.Run("addressed installation with the trigger gets command", func(t *testing.T) {
		m, tracer, root, chans := setup(t)
		msg, p := textMessage("user-1", "@aa echo hi")
		m.deliverToApps(&Instance{DBID: "bot-cmd-route"}, msg, p, tracer, root)

		typ, data := nextWSEvent(t, chans["inst-runner-aa"])
		if typ != "command" {
			t.Errorf("event type = %q, want command", typ)
		}
		if data["command"] != "echo" || data["text"] != "hi" {
			t.Errorf("command/text = %v/%v, want echo/hi", data["command"], data["text"])
		}
		expectNoWSEvent(t, chans["inst-runner-bb"])
	})

	t.Run("disabled addressee falls through to message flow", func(t *testing.T) {
		m, _, tracer, root, chans := setupRouting(t,
			runnerInstall("aa", false, "echo"),
			chatInstall("chat"),
		)
		msg, p := textMessage("user-1", "@aa echo hi")
		m.deliverToApps(&Instance{DBID: "bot-cmd-route"}, msg, p, tracer, root)

		// Disabled installation is skipped by MatchHandle, so the message falls
		// through to the generic message.* flow for other apps.
		typ, _ := nextWSEvent(t, chans["inst-chat-chat"])
		if typ != "message.text" {
			t.Errorf("event type = %q, want message.text", typ)
		}
		expectNoWSEvent(t, chans["inst-runner-aa"])
	})
}

// TestCommandRouting_BareTriggerOnDisabledInstallation verifies a disabled
// installation's command trigger never matches: the message stays ordinary chat.
func TestCommandRouting_BareTriggerOnDisabledInstallation(t *testing.T) {
	m, _, tracer, root, chans := setupRouting(t,
		runnerInstall("runner", false, "hn"),
		chatInstall("chat"),
	)
	msg, p := textMessage("user-1", "hn top")
	m.deliverToApps(&Instance{DBID: "bot-cmd-route"}, msg, p, tracer, root)

	typ, data := nextWSEvent(t, chans["inst-chat-chat"])
	if typ != "message.text" {
		t.Errorf("event type = %q, want message.text", typ)
	}
	if data["content"] != "hn top" {
		t.Errorf("content = %v, want %q", data["content"], "hn top")
	}
	expectNoWSEvent(t, chans["inst-runner-runner"])
}

// TestCommandRouting_BareCommandAppLevelTools covers local apps whose command
// triggers live on the app record rather than on the installation.
func TestCommandRouting_BareCommandAppLevelTools(t *testing.T) {
	m, _, tracer, root, chans := setupRouting(t, routeInstallation{
		appID: "app-local", instID: "inst-local", handle: "local",
		appTools: `[{"name":"deploy","command":"deploy"}]`,
		events:   `["command"]`, enabled: true,
	})
	msg, p := textMessage("user-1", "deploy prod")
	m.deliverToApps(&Instance{DBID: "bot-cmd-route"}, msg, p, tracer, root)

	typ, data := nextWSEvent(t, chans["inst-local"])
	if typ != "command" {
		t.Fatalf("event type = %q, want command", typ)
	}
	if data["command"] != "deploy" || data["text"] != "prod" {
		t.Errorf("command/text = %v/%v, want deploy/prod", data["command"], data["text"])
	}
}

// TestTryDeliverCommand_BareFormWithoutRegistration is a focused unit check that
// tryDeliverCommand returns false (i.e. the caller continues to message.*) when
// nothing registers the bare word.
func TestTryDeliverCommand_BareFormWithoutRegistration(t *testing.T) {
	m, _, tracer, root, _ := setupRouting(t, chatInstall("chat"))
	msg, p := textMessage("user-1", "hello world")
	if m.tryDeliverCommand(&Instance{DBID: "bot-cmd-route"}, msg, p, p.content, tracer, root) {
		t.Error("tryDeliverCommand = true for an unregistered bare word, want false")
	}
}

// TestCommandRouting_BareCandidateAppLookupFailure checks trace parity on the
// error path: a bare candidate whose app record is missing must not add a
// match_command_error event — the trace keeps exactly the events it had before
// "/" became optional (here: match_event_none, since the orphan installation
// cannot receive message events either).
func TestCommandRouting_BareCandidateAppLookupFailure(t *testing.T) {
	const botID = "bot-cmd-route"
	ms := memstore.New()
	ms.AddInstallation(&store.AppInstallation{
		ID: "inst-orphan", AppID: "missing-app", BotID: botID, Handle: "orphan",
		Enabled: true, Scopes: jsonx.RawMessage(`["message:read"]`),
	})

	hub := appdelivery.NewWSHub()
	ch := make(chan []byte, 4)
	hub.Register("inst-orphan", &appdelivery.WSConn{InstID: "inst-orphan", BotID: botID, Send: ch})

	rec := &recordingTraceStore{}
	tracer := store.NewTracer(rec, botID)
	root := tracer.Start("test", store.SpanKindInternal, nil)
	m := newTestManager(ms, hub)

	msg, p := textMessage("user-1", "hello world 你好")
	m.deliverToApps(&Instance{DBID: botID}, msg, p, tracer, root)

	tracer.Flush()
	if names := rec.rootEventNames(t); len(names) != 1 || names[0] != "match_event_none" {
		t.Errorf("root trace events = %v, want [match_event_none] (unchanged)", names)
	}
	expectNoWSEvent(t, ch)
}
