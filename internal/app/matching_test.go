package app

import (
	"errors"
	"testing"

	"github.com/openilink/openilink-hub/internal/jsonx"
	"github.com/openilink/openilink-hub/internal/store"
)

// --- Mock DB for matching tests ---

type mockAppStore struct {
	installations []store.AppInstallation
	apps          map[string]*store.App
	listErr       error
	getAppErr     error
}

func (m *mockAppStore) ListInstallationsByBot(_ string) ([]store.AppInstallation, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.installations, nil
}

func (m *mockAppStore) GetApp(id string) (*store.App, error) {
	if m.getAppErr != nil {
		return nil, m.getAppErr
	}
	if app, ok := m.apps[id]; ok {
		return app, nil
	}
	return nil, errors.New("not found")
}

func (m *mockAppStore) GetInstallationByHandle(botID, handle string) (*store.AppInstallation, error) {
	for i := range m.installations {
		if m.installations[i].Handle == handle {
			return &m.installations[i], nil
		}
	}
	return nil, errors.New("not found")
}

func newMatchDispatcher(store *mockAppStore) *Dispatcher {
	return &Dispatcher{
		appDB: store,
		dbLog: &mockLogDB{},
	}
}

// --- parseCommand tests ---

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name    string
		content string
		cmd     string
		args    string
	}{
		{"simple command", "/github list PRs", "github", "list PRs"},
		{"command no args", "/help", "help", ""},
		{"mention then command", "@github /github list PRs", "github", "list PRs"},
		{"mention then command no args", "@bot /status", "status", ""},
		{"mention only", "@github", "", ""},
		{"empty string", "", "", ""},
		{"bare form without slash", "hello world", "hello", "world"},
		{"bare form single token", "hn", "hn", ""},
		{"bare form uppercased", "GitHub PRs", "github", "PRs"},
		{"bare form keeps inner spacing", "cmd  arg1  arg2", "cmd", "arg1  arg2"},
		{"slash only", "/", "", ""},
		{"mention with space no command", "@bot hello", "", ""},
		{"leading whitespace", "  /cmd arg", "cmd", "arg"},
		{"leading whitespace bare form", "  cmd arg", "cmd", "arg"},
		{"uppercase command lowered", "/GitHub PRs", "github", "PRs"},
		{"multiple spaces in args", "/cmd  arg1  arg2", "arg1  arg2", ""},
	}

	// Last test case needs correction -- the code does SplitN with 2 parts on
	// "cmd  arg1  arg2" after stripping "/", so parts[0]="cmd", parts[1]=" arg1  arg2"
	// which gets TrimSpace to "arg1  arg2".
	tests[len(tests)-1].cmd = "cmd"
	tests[len(tests)-1].args = "arg1  arg2"

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, args := parseCommand(tt.content)
			if cmd != tt.cmd {
				t.Errorf("parseCommand(%q) cmd = %q, want %q", tt.content, cmd, tt.cmd)
			}
			if args != tt.args {
				t.Errorf("parseCommand(%q) args = %q, want %q", tt.content, args, tt.args)
			}
		})
	}
}

func TestParseCommandMentionFormats(t *testing.T) {
	// @mention /command args -> command, args
	cmd, args := parseCommand("@mybot /deploy production")
	if cmd != "deploy" {
		t.Errorf("cmd = %q, want %q", cmd, "deploy")
	}
	if args != "production" {
		t.Errorf("args = %q, want %q", args, "production")
	}
}

// --- appHasCommand tests ---

func TestAppHasCommand(t *testing.T) {
	tools, _ := jsonx.Marshal([]store.AppTool{
		{Name: "list_prs", Command: "github"},
		{Name: "run_deploy", Command: "Deploy"},
		{Name: "no_slash_tool", Description: "tool without command trigger"},
	})
	app := &store.App{ID: "a1", Tools: tools}

	if !appHasCommand(app, "github") {
		t.Error("should match 'github'")
	}
	if !appHasCommand(app, "deploy") {
		t.Error("should match 'deploy' (case insensitive)")
	}
	if !appHasCommand(app, "GITHUB") {
		t.Error("should match 'GITHUB' (case insensitive)")
	}
	if appHasCommand(app, "unknown") {
		t.Error("should not match 'unknown'")
	}
	if appHasCommand(app, "no_slash_tool") {
		t.Error("should not match tool name when Command is empty")
	}
}

func TestAppHasCommand_NilApp(t *testing.T) {
	if appHasCommand(nil, "cmd") {
		t.Error("nil app should return false")
	}
}

func TestAppHasCommand_EmptyTools(t *testing.T) {
	app := &store.App{Tools: nil}
	if appHasCommand(app, "cmd") {
		t.Error("nil tools should return false")
	}
}

func TestAppHasCommand_InvalidJSON(t *testing.T) {
	app := &store.App{Tools: jsonx.RawMessage(`invalid`)}
	if appHasCommand(app, "cmd") {
		t.Error("invalid JSON should return false")
	}
}

// --- appSubscribesToEvent tests ---

func TestAppSubscribesToEvent(t *testing.T) {
	events, _ := jsonx.Marshal([]string{"message", "reaction.added"})
	app := &store.App{ID: "a1", Events: events}

	tests := []struct {
		eventType string
		want      bool
	}{
		{"message", true},        // exact match
		{"message.text", true},   // wildcard match
		{"message.image", true},  // wildcard match
		{"reaction.added", true}, // exact match
		{"reaction.removed", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.eventType, func(t *testing.T) {
			got := appSubscribesToEvent(app, tt.eventType)
			if got != tt.want {
				t.Errorf("appSubscribesToEvent(%q) = %v, want %v", tt.eventType, got, tt.want)
			}
		})
	}
}

func TestAppSubscribesToEvent_NilApp(t *testing.T) {
	if appSubscribesToEvent(nil, "message") {
		t.Error("nil app should return false")
	}
}

func TestAppSubscribesToEvent_EmptyEvents(t *testing.T) {
	app := &store.App{Events: nil}
	if appSubscribesToEvent(app, "message") {
		t.Error("nil events should return false")
	}
}

func TestAppSubscribesToEvent_InvalidJSON(t *testing.T) {
	app := &store.App{Events: jsonx.RawMessage(`bad`)}
	if appSubscribesToEvent(app, "message") {
		t.Error("invalid JSON should return false")
	}
}

// --- MatchCommand tests ---

func TestMatchCommand_Success(t *testing.T) {
	cmds, _ := jsonx.Marshal([]store.AppTool{{Name: "list_prs", Command: "github"}})
	store := &mockAppStore{
		installations: []store.AppInstallation{
			{ID: "i1", AppID: "a1", BotID: "b1", Enabled: true, AppWebhookURL: "http://example.com"},
		},
		apps: map[string]*store.App{
			"a1": {ID: "a1", Tools: cmds},
		},
	}

	d := newMatchDispatcher(store)
	matched, cmd, args, err := d.MatchCommand("b1", "/github list PRs")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if cmd != "github" {
		t.Errorf("cmd = %q, want %q", cmd, "github")
	}
	if args != "list PRs" {
		t.Errorf("args = %q, want %q", args, "list PRs")
	}
	if len(matched) != 1 {
		t.Errorf("matched %d installations, want 1", len(matched))
	}
}

func TestMatchCommand_NoMatch(t *testing.T) {
	cmds, _ := jsonx.Marshal([]store.AppTool{{Name: "list_prs", Command: "github"}})
	store := &mockAppStore{
		installations: []store.AppInstallation{
			{ID: "i1", AppID: "a1", BotID: "b1", Enabled: true, AppWebhookURL: "http://example.com"},
		},
		apps: map[string]*store.App{
			"a1": {ID: "a1", Tools: cmds},
		},
	}

	d := newMatchDispatcher(store)
	matched, _, _, err := d.MatchCommand("b1", "/unknown foo")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(matched) != 0 {
		t.Errorf("matched %d installations, want 0", len(matched))
	}
}

func TestMatchCommand_DisabledInstallation(t *testing.T) {
	cmds, _ := jsonx.Marshal([]store.AppTool{{Name: "run_cmd", Command: "cmd"}})
	store := &mockAppStore{
		installations: []store.AppInstallation{
			{ID: "i1", AppID: "a1", BotID: "b1", Enabled: false, AppWebhookURL: "http://example.com"},
		},
		apps: map[string]*store.App{
			"a1": {ID: "a1", Tools: cmds},
		},
	}

	d := newMatchDispatcher(store)
	matched, _, _, _ := d.MatchCommand("b1", "/cmd")
	if len(matched) != 0 {
		t.Error("disabled installation should be excluded")
	}
}

func TestMatchCommand_NoRequestURL(t *testing.T) {
	cmds, _ := jsonx.Marshal([]store.AppTool{{Name: "run_cmd", Command: "cmd"}})
	store := &mockAppStore{
		installations: []store.AppInstallation{
			{ID: "i1", AppID: "a1", BotID: "b1", Enabled: true, AppWebhookURL: ""},
		},
		apps: map[string]*store.App{
			"a1": {ID: "a1", Tools: cmds},
		},
	}

	d := newMatchDispatcher(store)
	matched, _, _, _ := d.MatchCommand("b1", "/cmd")
	if len(matched) != 1 {
		t.Errorf("installation without request_url should still be included, got %d", len(matched))
	}
}

func TestMatchCommand_NotACommand(t *testing.T) {
	// "hello world" is a bare-form *candidate* ("hello"), but no enabled
	// installation registers that trigger, so nothing is matched and the
	// candidate carries no installations — the caller falls through to the
	// message.* flow.
	cmds, _ := jsonx.Marshal([]store.AppTool{{Name: "list_prs", Command: "github"}})
	store := &mockAppStore{
		installations: []store.AppInstallation{
			{ID: "i1", AppID: "a1", BotID: "b1", Enabled: true},
		},
		apps: map[string]*store.App{"a1": {ID: "a1", Tools: cmds}},
	}
	d := newMatchDispatcher(store)
	matched, cmd, _, err := d.MatchCommand("b1", "hello world")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if cmd != "hello" {
		t.Errorf("cmd = %q, want %q (bare candidate)", cmd, "hello")
	}
	if len(matched) != 0 {
		t.Errorf("matched %d installations, want 0 for non-trigger content", len(matched))
	}
}

// TestMatchCommand_BareForm covers the "/"-optional behavior: a bare first word
// that exactly matches a registered command trigger (case-insensitively) is
// routed as a command; anything else matches nothing.
func TestMatchCommand_BareForm(t *testing.T) {
	cmds, _ := jsonx.Marshal([]store.AppTool{
		{Name: "list_prs", Command: "github"},
		{Name: "run_deploy", Command: "Deploy"},
	})
	store := &mockAppStore{
		installations: []store.AppInstallation{
			{ID: "i1", AppID: "a1", BotID: "b1", Enabled: true},
		},
		apps: map[string]*store.App{"a1": {ID: "a1", Tools: cmds}},
	}

	tests := []struct {
		name      string
		content   string
		wantCmd   string
		wantArgs  string
		wantMatch bool
	}{
		{"slash form still works", "/github list PRs", "github", "list PRs", true},
		{"bare form", "github list PRs", "github", "list PRs", true},
		{"bare form no args", "github", "github", "", true},
		{"bare form case insensitive", "GITHUB", "github", "", true},
		{"bare form mixed case trigger", "deploy prod", "deploy", "prod", true},
		{"bare form unicode whitespace", "github\u2005list PRs", "github", "list PRs", true},
		{"bare form full-width whitespace", "github\u3000list", "github", "list", true},
		{"bare non-trigger", "hello world", "hello", "world", false},
		{"bare tool name without command trigger", "list_prs", "list_prs", "", false},
		{"mention then bare word is not a command", "@bot github list", "", "", false},
		{"slash unknown command matches nothing", "/unknown foo", "unknown", "foo", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matched, cmd, args, err := newMatchDispatcher(store).MatchCommand("b1", tt.content)
			if err != nil {
				t.Fatalf("error: %v", err)
			}
			if cmd != tt.wantCmd {
				t.Errorf("cmd = %q, want %q", cmd, tt.wantCmd)
			}
			if args != tt.wantArgs {
				t.Errorf("args = %q, want %q", args, tt.wantArgs)
			}
			if got := len(matched) > 0; got != tt.wantMatch {
				t.Errorf("matched = %v, want %v", got, tt.wantMatch)
			}
		})
	}
}

func TestMatchCommand_BareFormDisabledInstallation(t *testing.T) {
	cmds, _ := jsonx.Marshal([]store.AppTool{{Name: "run_cmd", Command: "cmd"}})
	store := &mockAppStore{
		installations: []store.AppInstallation{
			{ID: "i1", AppID: "a1", BotID: "b1", Enabled: false},
		},
		apps: map[string]*store.App{"a1": {ID: "a1", Tools: cmds}},
	}
	matched, _, _, err := newMatchDispatcher(store).MatchCommand("b1", "cmd now")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(matched) != 0 {
		t.Error("disabled installation must not match a bare command")
	}
}

func TestMatchCommand_BareFormInstallationTools(t *testing.T) {
	// Marketplace/builtin apps (e.g. Runner) keep their commands per
	// installation instead of on the shared app record.
	cmds, _ := jsonx.Marshal([]store.AppTool{{Name: "hn", Command: "hn"}})
	store := &mockAppStore{
		installations: []store.AppInstallation{
			{ID: "i1", AppID: "a1", BotID: "b1", Enabled: true, Tools: cmds},
		},
		apps: map[string]*store.App{"a1": {ID: "a1", Tools: jsonx.RawMessage(`[]`)}},
	}
	matched, cmd, args, err := newMatchDispatcher(store).MatchCommand("b1", "HN top 10")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if cmd != "hn" || args != "top 10" || len(matched) != 1 {
		t.Errorf("MatchCommand = (%v, %q, %q), want 1 installation, hn, %q", matched, cmd, args, "top 10")
	}
}

func TestInstallationSupportsCommand(t *testing.T) {
	appTools, _ := jsonx.Marshal([]store.AppTool{{Name: "run", Command: "deploy"}})
	instTools, _ := jsonx.Marshal([]store.AppTool{{Name: "hn", Command: "hn"}})
	mock := &mockAppStore{
		installations: []store.AppInstallation{
			{ID: "i1", AppID: "a1", BotID: "b1", Enabled: true, Tools: instTools},
		},
		apps: map[string]*store.App{"a1": {ID: "a1", Tools: appTools}},
	}
	d := newMatchDispatcher(mock)
	inst := &store.AppInstallation{ID: "i1", AppID: "a1", Enabled: true, Tools: instTools}

	tests := []struct {
		name string
		inst *store.AppInstallation
		cmd  string
		want bool
	}{
		{"installation trigger", inst, "hn", true},
		{"installation trigger case insensitive", inst, "HN", true},
		{"app-level trigger", inst, "deploy", true},
		{"app-level trigger case insensitive", inst, "Deploy", true},
		{"unknown trigger", inst, "unknown", false},
		{"empty name", inst, "", false},
		{"nil installation", nil, "hn", false},
		{"installation without tools still sees app tools", &store.AppInstallation{ID: "i1", AppID: "a1"}, "deploy", true},
		{"missing app", &store.AppInstallation{ID: "i2", AppID: "missing"}, "hn", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := d.InstallationSupportsCommand(tt.inst, tt.cmd); got != tt.want {
				t.Errorf("InstallationSupportsCommand(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestMatchCommand_ListError(t *testing.T) {
	store := &mockAppStore{listErr: errFake}
	d := newMatchDispatcher(store)
	_, _, _, err := d.MatchCommand("b1", "/cmd")
	if err == nil {
		t.Error("expected error from list")
	}
}

// --- MatchEvent tests ---

func TestMatchEvent_Success(t *testing.T) {
	events, _ := jsonx.Marshal([]string{"message"})
	scopes, _ := jsonx.Marshal([]string{"message:read"})
	store := &mockAppStore{
		installations: []store.AppInstallation{
			{ID: "i1", AppID: "a1", BotID: "b1", Enabled: true, AppWebhookURL: "http://example.com", Scopes: scopes},
		},
		apps: map[string]*store.App{
			"a1": {ID: "a1", Events: events, Scopes: scopes},
		},
	}

	d := newMatchDispatcher(store)
	matched, err := d.MatchEvent("b1", "message.text")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(matched) != 1 {
		t.Errorf("matched %d installations, want 1", len(matched))
	}
}

func TestMatchEvent_NoSubscription(t *testing.T) {
	events, _ := jsonx.Marshal([]string{"reaction.added"})
	scopes, _ := jsonx.Marshal([]string{"message:read"})
	store := &mockAppStore{
		installations: []store.AppInstallation{
			{ID: "i1", AppID: "a1", BotID: "b1", Enabled: true, AppWebhookURL: "http://example.com", Scopes: scopes},
		},
		apps: map[string]*store.App{
			"a1": {ID: "a1", Events: events, Scopes: scopes},
		},
	}

	d := newMatchDispatcher(store)
	matched, err := d.MatchEvent("b1", "message.text")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(matched) != 0 {
		t.Errorf("matched %d, want 0", len(matched))
	}
}

func TestMatchEvent_DisabledExcluded(t *testing.T) {
	events, _ := jsonx.Marshal([]string{"message"})
	scopes, _ := jsonx.Marshal([]string{"message:read"})
	store := &mockAppStore{
		installations: []store.AppInstallation{
			{ID: "i1", AppID: "a1", BotID: "b1", Enabled: false, AppWebhookURL: "http://example.com", Scopes: scopes},
		},
		apps: map[string]*store.App{
			"a1": {ID: "a1", Events: events, Scopes: scopes},
		},
	}

	d := newMatchDispatcher(store)
	matched, _ := d.MatchEvent("b1", "message.text")
	if len(matched) != 0 {
		t.Error("disabled installations should be excluded")
	}
}

func TestMatchEvent_ListError(t *testing.T) {
	store := &mockAppStore{listErr: errFake}
	d := newMatchDispatcher(store)
	_, err := d.MatchEvent("b1", "message.text")
	if err == nil {
		t.Error("expected error")
	}
}

func TestMatchEvent_GetAppError(t *testing.T) {
	store := &mockAppStore{
		installations: []store.AppInstallation{
			{ID: "i1", AppID: "a1", BotID: "b1", Enabled: true, AppWebhookURL: "http://example.com"},
		},
		apps:      map[string]*store.App{},
		getAppErr: errFake,
	}

	d := newMatchDispatcher(store)
	matched, err := d.MatchEvent("b1", "message.text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// App lookup fails, so no match.
	if len(matched) != 0 {
		t.Errorf("matched %d, want 0", len(matched))
	}
}

// ==================== ParseMention tests ====================

func TestParseMention(t *testing.T) {
	tests := []struct {
		input   string
		handle  string
		command string
		text    string
	}{
		{"@echo-work hello", "echo-work", "", "hello"},
		{"@echo-work /echo hello world", "echo-work", "/echo", "hello world"},
		{"@echo-work /ECHO", "echo-work", "/echo", ""},
		{"@Echo-Work", "echo-work", "", ""},
		{"@github", "github", "", ""},
		{"@github /prs list", "github", "/prs", "list"},
		{"hello", "", "", ""},
		{"", "", "", ""},
		{"/echo hello", "", "", ""},
		{"@ hello", "", "", ""},
		{"  @echo hello  ", "echo", "", "hello"},
	}

	for _, tt := range tests {
		handle, command, text := ParseMention(tt.input)
		if handle != tt.handle || command != tt.command || text != tt.text {
			t.Errorf("ParseMention(%q) = (%q, %q, %q), want (%q, %q, %q)",
				tt.input, handle, command, text, tt.handle, tt.command, tt.text)
		}
	}
}

// TestParseMention_UnicodeWhitespace verifies that handles still parse when the
// separator after @handle is not an ASCII space. WeChat inserts U+2005 (FOUR-PER-EM
// SPACE) right after an @mention, and some clients use a full-width space (U+3000).
func TestParseMention_UnicodeWhitespace(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		handle  string
		command string
		text    string
	}{
		{"four-per-em space (WeChat @)", "@echo-work hello", "echo-work", "", "hello"},
		{"full-width space", "@echo-work　hello", "echo-work", "", "hello"},
		{"four-per-em then command", "@echo-work /echo hi", "echo-work", "/echo", "hi"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handle, command, text := ParseMention(tt.input)
			if handle != tt.handle || command != tt.command || text != tt.text {
				t.Errorf("ParseMention(%q) = (%q, %q, %q), want (%q, %q, %q)",
					tt.input, handle, command, text, tt.handle, tt.command, tt.text)
			}
		})
	}
}

// TestParseCommand_UnicodeWhitespace locks the second parsing path: a U+2005
// separator between an @mention and the following /command must still parse.
func TestParseCommand_UnicodeWhitespace(t *testing.T) {
	cmd, args := parseCommand("@echo-work /deploy prod")
	if cmd != "deploy" || args != "prod" {
		t.Errorf("parseCommand(@echo-work\\u2005/deploy prod) = (%q, %q), want (deploy, prod)", cmd, args)
	}
}

// ==================== MatchHandle tests ====================

func TestMatchHandle_Success(t *testing.T) {
	store := &mockAppStore{
		installations: []store.AppInstallation{
			{ID: "i1", AppID: "a1", BotID: "b1", Handle: "echo-work", Enabled: true, AppWebhookURL: "http://a.com"},
			{ID: "i2", AppID: "a1", BotID: "b1", Handle: "echo-family", Enabled: true, AppWebhookURL: "http://b.com"},
		},
		apps: map[string]*store.App{"a1": {ID: "a1"}},
	}
	d := newMatchDispatcher(store)

	inst, err := d.MatchHandle("b1", "echo-work")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inst == nil || inst.ID != "i1" {
		t.Errorf("expected i1, got %v", inst)
	}
}

func TestMatchHandle_NotFound(t *testing.T) {
	store := &mockAppStore{
		installations: []store.AppInstallation{
			{ID: "i1", AppID: "a1", BotID: "b1", Handle: "echo-work", Enabled: true, AppWebhookURL: "http://a.com"},
		},
		apps: map[string]*store.App{"a1": {ID: "a1"}},
	}
	d := newMatchDispatcher(store)

	inst, _ := d.MatchHandle("b1", "nonexistent")
	if inst != nil {
		t.Errorf("expected nil, got %v", inst)
	}
}

func TestMatchHandle_Disabled(t *testing.T) {
	store := &mockAppStore{
		installations: []store.AppInstallation{
			{ID: "i1", AppID: "a1", BotID: "b1", Handle: "echo-work", Enabled: false, AppWebhookURL: "http://a.com"},
		},
		apps: map[string]*store.App{"a1": {ID: "a1"}},
	}
	d := newMatchDispatcher(store)

	inst, _ := d.MatchHandle("b1", "echo-work")
	if inst != nil {
		t.Errorf("expected nil for disabled, got %v", inst)
	}
}

func TestMatchHandle_NoRequestURL(t *testing.T) {
	store := &mockAppStore{
		installations: []store.AppInstallation{
			{ID: "i1", AppID: "a1", BotID: "b1", Handle: "echo-work", Enabled: true, AppWebhookURL: ""},
		},
		apps: map[string]*store.App{"a1": {ID: "a1"}},
	}
	d := newMatchDispatcher(store)

	inst, _ := d.MatchHandle("b1", "echo-work")
	if inst == nil {
		t.Error("installation without request_url should still be returned")
	}
}

func TestMatchEvent_MultipleInstallations(t *testing.T) {
	events1, _ := jsonx.Marshal([]string{"message"})
	scopes1, _ := jsonx.Marshal([]string{"message:read"})
	events2, _ := jsonx.Marshal([]string{"reaction"})
	scopes2, _ := jsonx.Marshal([]string{"message:read"})
	store := &mockAppStore{
		installations: []store.AppInstallation{
			{ID: "i1", AppID: "a1", BotID: "b1", Enabled: true, AppWebhookURL: "http://a.com", Scopes: scopes1},
			{ID: "i2", AppID: "a2", BotID: "b1", Enabled: true, AppWebhookURL: "http://b.com", Scopes: scopes2},
		},
		apps: map[string]*store.App{
			"a1": {ID: "a1", Events: events1, Scopes: scopes1},
			"a2": {ID: "a2", Events: events2, Scopes: scopes2},
		},
	}

	d := newMatchDispatcher(store)
	matched, _ := d.MatchEvent("b1", "message.text")
	if len(matched) != 1 {
		t.Errorf("matched %d, want 1 (only app with message subscription)", len(matched))
	}
	if matched[0].ID != "i1" {
		t.Errorf("matched[0].ID = %q, want %q", matched[0].ID, "i1")
	}
}

// TestChannelTaggedReplyIsInert guards issue #248 Part 2: the channel-reply
// prefix uses 【handle】 rather than @handle precisely so that if such a reply
// ever echoes back as inbound it is NOT re-parsed as a mention or command
// (which would risk a routing loop). With "/" now optional for bare messages,
// the guard is asserted where it matters: routing never resolves a tagged reply
// to a command, neither slash-prefixed nor bare.
func TestChannelTaggedReplyIsInert(t *testing.T) {
	if h, c, txt := ParseMention("【openclaw】 hello"); h != "" || c != "" || txt != "" {
		t.Errorf("ParseMention(【openclaw】...) = (%q, %q, %q), want all empty", h, c, txt)
	}

	cmds, _ := jsonx.Marshal([]store.AppTool{{Name: "run_deploy", Command: "deploy"}})
	store := &mockAppStore{
		installations: []store.AppInstallation{
			{ID: "i1", AppID: "a1", BotID: "b1", Enabled: true},
		},
		apps: map[string]*store.App{"a1": {ID: "a1", Tools: cmds}},
	}
	d := newMatchDispatcher(store)
	for _, content := range []string{"【openclaw】 /deploy prod", "【openclaw】 deploy prod", "【openclaw】 deploy", "【openclaw】"} {
		matched, cmd, _, err := d.MatchCommand("b1", content)
		if err != nil {
			t.Fatalf("MatchCommand(%q) error: %v", content, err)
		}
		if len(matched) != 0 {
			t.Errorf("MatchCommand(%q) matched %d installations, want 0 (tagged replies must stay inert)", content, len(matched))
		}
		if cmd == "deploy" {
			t.Errorf("MatchCommand(%q) cmd = %q, want not the registered trigger", content, cmd)
		}
	}
}
