package bot_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AnthonyPoschen/genos-discord/internal/bot"
	"github.com/AnthonyPoschen/genos-discord/internal/genos"
)

const (
	testGuild   = "guild-1"
	testRole    = "role-customer"
	testUser    = "user-1"
	testSecret  = "genos-bot-secret"
	testToken   = "genos_discord_user_token"
	testDiscord = "discord-bot-token"
)

var (
	uuidPattern    = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	rolePattern    = regexp.MustCompile(`^/api/v10/guilds/[^/]+/members/[^/]+/roles/[^/]+$`)
	detailPattern  = regexp.MustCompile(`^/api/v1/servers/[^/]+$`)
	actionPattern  = regexp.MustCompile(`^/api/v1/servers/[^/]+/actions$`)
	channelPattern = regexp.MustCompile(`^/api/v1/servers/[^/]+/runtime-channels$`)
	commandPattern = regexp.MustCompile(`^/api/v1/servers/[^/]+/runtime-channels/[^/]+/commands$`)
)

func TestCustomerRoleFollowsCurrentFleet(t *testing.T) {
	if bot.CustomerRoleAction(0) != bot.RoleRevoke || bot.CustomerRoleAction(2) != bot.RoleGrant {
		t.Fatal("role decision")
	}

	t.Run("grant when at least one current server", func(t *testing.T) {
		h := newHarness(t, harnessConfig{servers: []genos.Server{
			testServer("srv-1", "Alpha", 0, nil),
			testServer("srv-2", "Beta", 1, nil),
		}})
		resp := h.post(t, h.command("servers", "", ""))
		if resp.Flags != 64 {
			t.Fatalf("flags = %d", resp.Flags)
		}
		if calls := h.discord.calls(); len(calls) != 1 || calls[0].Method != http.MethodPut || calls[0].Path != "/api/v10/guilds/guild-1/members/user-1/roles/role-customer" {
			t.Fatalf("role calls = %+v", calls)
		}
		if len(h.genos.actions()) != 0 {
			t.Fatal("listing servers posted an action")
		}
	})

	t.Run("start with zero servers does not call an action", func(t *testing.T) {
		h := newHarness(t, harnessConfig{servers: []genos.Server{}})
		resp := h.post(t, h.command("start", "", ""))
		if len(h.genos.actions()) != 0 || resp.Approve != nil || !strings.Contains(resp.Content, "no servers") {
			t.Fatalf("response = %+v actions = %+v", resp, h.genos.actions())
		}
		if calls := h.discord.calls(); len(calls) != 1 || calls[0].Method != http.MethodDelete {
			t.Fatalf("role calls = %+v", calls)
		}
	})

	t.Run("revoke when the fleet is empty", func(t *testing.T) {
		h := newHarness(t, harnessConfig{servers: []genos.Server{}})
		resp := h.post(t, h.command("servers", "", ""))
		if !strings.Contains(resp.Content, "no servers") {
			t.Fatalf("content %q", resp.Content)
		}
		if calls := h.discord.calls(); len(calls) != 1 || calls[0].Method != http.MethodDelete {
			t.Fatalf("role calls = %+v", calls)
		}
	})

	t.Run("observe link", func(t *testing.T) {
		h := newHarness(t, harnessConfig{servers: []genos.Server{testServer("srv-1", "Alpha", 0, nil)}})
		if err := h.svc.ObserveLink(t.Context(), testUser); err != nil {
			t.Fatal(err)
		}
		if calls := h.discord.calls(); len(calls) != 1 || calls[0].Method != http.MethodPut {
			t.Fatalf("link role calls = %+v", calls)
		}
	})
}

func TestStartDoesNotRequireCustomerRoleOrApprove(t *testing.T) {
	server := testServer("srv-1", "Alpha", 5, []string{"patch"})
	decision := bot.Decide(bot.DecisionInput{Command: "start", InGuild: true, Servers: []genos.Server{server}, ServerID: server.ID})
	if decision.Action != "start" || decision.ApprovalCustomID != "" || decision.ConfirmUnsavedProgressLoss {
		t.Fatalf("decision = %+v", decision)
	}

	h := newHarness(t, harnessConfig{servers: []genos.Server{server}, discordStatus: http.StatusForbidden})
	resp := h.post(t, h.command("start", "", ""))
	if resp.Approve != nil {
		t.Fatalf("start asked for approval: %+v", resp)
	}
	if resp.Flags != 64 || !strings.Contains(resp.Content, "Start requested") {
		t.Fatalf("response = %+v", resp)
	}
	posts := h.genos.actions()
	if len(posts) != 1 {
		t.Fatalf("actions = %+v", posts)
	}
	assertAction(t, posts[0], "srv-1", "start", false)
	for _, call := range h.discord.calls() {
		if call.Method == http.MethodGet {
			t.Fatalf("checked role membership before the command: %+v", call)
		}
	}
	if len(h.discord.calls()) != 1 || h.discord.calls()[0].Method != http.MethodPut {
		t.Fatalf("expected a best-effort grant, got %+v", h.discord.calls())
	}
	if len(h.genos.mints()) != 1 {
		t.Fatalf("mints = %d", len(h.genos.mints()))
	}
	assertMintAndFollowUp(t, h)
}

func TestGuardedActionsDoNotCallGenosUntilApprove(t *testing.T) {
	cases := []struct {
		name        string
		command     string
		players     int64
		notable     []string
		wantConfirm bool
		phrase      string
	}{
		{name: "stop", command: "stop"},
		{name: "force-stop", command: "force-stop", wantConfirm: true, phrase: "unsaved progress"},
		{name: "restart with players", command: "restart", players: 3},
		{name: "restart with notable update", command: "restart", notable: []string{"Factorio 2.1.13"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := testServer("srv-1", "Alpha", tc.players, tc.notable)
			decision := bot.Decide(bot.DecisionInput{Command: tc.command, InGuild: true, Servers: []genos.Server{server}, ServerID: server.ID})
			if decision.Action != "" || decision.ApprovalCustomID == "" || decision.ApprovalLabel != "Approve" {
				t.Fatalf("decision = %+v", decision)
			}
			if tc.phrase != "" && !strings.Contains(decision.Content, tc.phrase) {
				t.Fatalf("decision content %q", decision.Content)
			}
			approval, ok := bot.DecideApproval(decision.ApprovalCustomID)
			if !ok || approval.ConfirmUnsavedProgressLoss != tc.wantConfirm {
				t.Fatalf("approval = %+v", approval)
			}

			h := newHarness(t, harnessConfig{servers: []genos.Server{server}})
			resp := h.post(t, h.command(tc.command, server.ID, ""))
			if len(h.genos.actions()) != 0 {
				t.Fatalf("called Genos action before Approve: %+v", h.genos.actions())
			}
			if resp.Approve == nil || resp.Approve.Label != "Approve" || resp.Flags != 64 {
				t.Fatalf("response = %+v", resp)
			}
			if tc.phrase != "" && !strings.Contains(resp.Content, tc.phrase) {
				t.Fatalf("content %q", resp.Content)
			}
			approved := h.post(t, bot.Request{
				Kind:          bot.KindApprove,
				CustomID:      resp.Approve.CustomID,
				DiscordUserID: testUser,
				GuildID:       testGuild,
			})
			if approved.Approve != nil {
				t.Fatalf("second approval: %+v", approved)
			}
			posts := h.genos.actions()
			if len(posts) != 1 {
				t.Fatalf("actions after Approve = %+v", posts)
			}
			assertAction(t, posts[0], "srv-1", tc.command, tc.wantConfirm)
		})
	}
}

func TestQuietRestartCallsGenosWithoutApprove(t *testing.T) {
	server := testServer("srv-1", "Alpha", 0, nil)
	decision := bot.Decide(bot.DecisionInput{Command: "restart", InGuild: true, Servers: []genos.Server{server}})
	if decision.Action != "restart" || decision.ApprovalCustomID != "" {
		t.Fatalf("decision = %+v", decision)
	}
	h := newHarness(t, harnessConfig{servers: []genos.Server{server}})
	resp := h.post(t, h.command("restart", "", ""))
	if resp.Approve != nil {
		t.Fatalf("quiet restart waited: %+v", resp)
	}
	posts := h.genos.actions()
	if len(posts) != 1 {
		t.Fatalf("actions = %+v", posts)
	}
	assertAction(t, posts[0], "srv-1", "restart", false)
}

func TestRestartUsesDetailBeforeCallingGenos(t *testing.T) {
	listed := testServer("srv-1", "Alpha", 0, nil)
	detail := testServer("srv-1", "Alpha", 4, nil)
	h := newHarness(t, harnessConfig{
		servers: []genos.Server{listed},
		details: map[string]genos.Server{"srv-1": detail},
	})
	resp := h.post(t, h.command("restart", "", ""))
	if len(h.genos.actions()) != 0 {
		t.Fatalf("restart with detail players called Genos before Approve: %+v", h.genos.actions())
	}
	if resp.Approve == nil || !strings.Contains(resp.Content, "Players online: 4") {
		t.Fatalf("response = %+v", resp)
	}
}

func TestServerOptionResolution(t *testing.T) {
	servers := []genos.Server{
		testServer("srv-1", "Alpha", 0, nil),
		testServer("srv-2", "Beta", 0, nil),
	}
	h := newHarness(t, harnessConfig{servers: servers})
	resp := h.post(t, h.command("start", "", ""))
	if len(h.genos.actions()) != 0 || !strings.Contains(resp.Content, "Choose a server") {
		t.Fatalf("response = %+v actions = %+v", resp, h.genos.actions())
	}

	h = newHarness(t, harnessConfig{servers: servers})
	resp = h.post(t, h.command("start", "srv-2", ""))
	posts := h.genos.actions()
	if resp.Approve != nil || len(posts) != 1 || !strings.Contains(posts[0].Path, "/servers/srv-2/actions") {
		t.Fatalf("direct id response = %+v actions = %+v", resp, posts)
	}
}

func TestConsoleUsesFirstInteractiveChannel(t *testing.T) {
	h := newHarness(t, harnessConfig{servers: []genos.Server{testServer("srv-1", "Alpha", 2, []string{"patch"})}})
	decision := bot.Decide(bot.DecisionInput{
		Command: "console",
		InGuild: true,
		Servers: []genos.Server{testServer("srv-1", "Alpha", 2, []string{"patch"})},
		Text:    "say hi",
	})
	if decision.ConsoleText != "say hi" || decision.ApprovalCustomID != "" {
		t.Fatalf("decision = %+v", decision)
	}
	resp := h.post(t, h.command("console", "", "say hi"))
	if resp.Approve != nil || !strings.Contains(resp.Content, "Sent console command") {
		t.Fatalf("response = %+v", resp)
	}
	if len(h.genos.actions()) != 0 {
		t.Fatal("console posted a lifecycle action")
	}
	commands := h.genos.commands()
	if len(commands) != 1 {
		t.Fatalf("console posts = %+v", commands)
	}
	if !strings.HasSuffix(commands[0].Path, "/runtime-channels/rcon/commands") {
		t.Fatalf("path = %s", commands[0].Path)
	}
	if strings.Contains(commands[0].Path, "container-stdout") || strings.Contains(commands[0].Path, "/later/") {
		t.Fatalf("did not use the first interactive channel: %s", commands[0].Path)
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(commands[0].Body), &body); err != nil || body.Text != "say hi" {
		t.Fatalf("body %s", commands[0].Body)
	}
	if commands[0].Auth != "Bearer "+testToken {
		t.Fatalf("console auth = %s", commands[0].Auth)
	}
}

func TestConsoleWithoutInteractiveChannelDoesNotPost(t *testing.T) {
	h := newHarness(t, harnessConfig{
		servers:  []genos.Server{testServer("srv-1", "Alpha", 0, nil)},
		channels: []genos.Channel{{ID: "container-stdout", Interaction: "read-only"}},
	})
	resp := h.post(t, h.command("console", "srv-1", "say hi"))
	if len(h.genos.commands()) != 0 || !strings.Contains(resp.Content, "No interactive console channel") {
		t.Fatalf("response = %+v posts = %+v", resp, h.genos.commands())
	}
}

func TestDMReplyIsNotEphemeral(t *testing.T) {
	h := newHarness(t, harnessConfig{servers: []genos.Server{testServer("srv-1", "Alpha", 0, nil)}})
	req := h.command("stop", "srv-1", "")
	req.GuildID = ""
	resp := h.post(t, req)
	if resp.Flags != 0 || resp.Approve == nil {
		t.Fatalf("dm response = %+v", resp)
	}
	if len(h.genos.actions()) != 0 {
		t.Fatal("dm stop called Genos before Approve")
	}
}

func TestInvalidApprovalDoesNotCallGenos(t *testing.T) {
	h := newHarness(t, harnessConfig{servers: []genos.Server{testServer("srv-1", "Alpha", 0, nil)}})
	resp := h.post(t, bot.Request{Kind: bot.KindApprove, CustomID: "approve:start:srv-1", DiscordUserID: testUser, GuildID: testGuild})
	if len(h.genos.actions()) != 0 || !strings.Contains(resp.Content, "not valid") {
		t.Fatalf("response = %+v actions = %+v", resp, h.genos.actions())
	}
}

type harnessConfig struct {
	servers       []genos.Server
	details       map[string]genos.Server
	channels      []genos.Channel
	discordStatus int
}

type harness struct {
	svc     *bot.Service
	genos   *fakeGenos
	discord *fakeDiscord
	botURL  string
}

func newHarness(t *testing.T, cfg harnessConfig) *harness {
	t.Helper()
	genosFake := &fakeGenos{
		servers:  append([]genos.Server(nil), cfg.servers...),
		details:  cfg.details,
		channels: cfg.channels,
		secret:   testSecret,
		token:    testToken,
		expires:  time.Now().Add(15 * time.Minute).UTC(),
	}
	discordFake := &fakeDiscord{status: cfg.discordStatus}
	genosServer := httptest.NewServer(genosFake)
	discordServer := httptest.NewServer(discordFake)
	t.Cleanup(genosServer.Close)
	t.Cleanup(discordServer.Close)
	svc := bot.New(bot.Options{
		HTTP:            &http.Client{Timeout: 5 * time.Second},
		GenosOrigin:     genosServer.URL,
		GenosBotSecret:  testSecret,
		DiscordAPIBase:  discordServer.URL + "/api/v10",
		DiscordBotToken: testDiscord,
		GuildID:         testGuild,
		CustomerRoleID:  testRole,
	})
	botServer := httptest.NewServer(svc)
	t.Cleanup(botServer.Close)
	h := &harness{svc: svc, genos: genosFake, discord: discordFake, botURL: botServer.URL}
	t.Cleanup(func() { h.assertInvariants(t) })
	return h
}

func (h *harness) command(name, serverID, text string) bot.Request {
	return bot.Request{
		Kind:          bot.KindCommand,
		Command:       name,
		DiscordUserID: testUser,
		ServerID:      serverID,
		Text:          text,
		GuildID:       testGuild,
	}
}

func (h *harness) post(t *testing.T, req bot.Request) bot.Response {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(h.botURL+"/interactions", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("handler status %d body %s", resp.StatusCode, body)
	}
	var out bot.Response
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out
}

func (h *harness) assertInvariants(t *testing.T) {
	t.Helper()
	for _, call := range h.genos.requests() {
		blob := strings.ToLower(call.Method + " " + call.Path + " " + call.Auth + " " + call.Body)
		if strings.Contains(blob, "kube") {
			t.Fatalf("kube material on genos request: %+v", call)
		}
		if strings.Contains(call.Auth, testDiscord) {
			t.Fatalf("discord bot token sent to genos: %+v", call)
		}
		if call.Path == "/api/v1/internal/discord/impersonation-tokens" {
			if call.Method != http.MethodPost || call.Auth != "Bearer "+testSecret {
				t.Fatalf("mint = %+v", call)
			}
			var body struct {
				DiscordUserID string `json:"discordUserID"`
			}
			if err := json.Unmarshal([]byte(call.Body), &body); err != nil || body.DiscordUserID == "" {
				t.Fatalf("mint body %s", call.Body)
			}
			continue
		}
		if call.Auth != "Bearer "+testToken {
			t.Fatalf("follow-up auth = %s path %s", call.Auth, call.Path)
		}
		if call.Auth == "Bearer "+testSecret {
			t.Fatalf("bot secret used after mint on %s", call.Path)
		}
	}
	if len(h.genos.unexpected()) != 0 {
		t.Fatalf("unexpected genos calls: %+v", h.genos.unexpected())
	}
	for _, call := range h.discord.calls() {
		blob := strings.ToLower(call.Method + " " + call.Path + " " + call.Auth)
		if strings.Contains(blob, "kube") || strings.Contains(call.Auth, testToken) || strings.Contains(call.Auth, testSecret) {
			t.Fatalf("bad discord call: %+v", call)
		}
		if call.Auth != "Bot "+testDiscord {
			t.Fatalf("discord auth = %s", call.Auth)
		}
		if !rolePattern.MatchString(call.Path) || (call.Method != http.MethodPut && call.Method != http.MethodDelete) {
			t.Fatalf("discord role call = %+v", call)
		}
	}
	if len(h.discord.unexpected()) != 0 {
		t.Fatalf("unexpected discord calls: %+v", h.discord.unexpected())
	}
}

func assertMintAndFollowUp(t *testing.T, h *harness) {
	t.Helper()
	mints := h.genos.mints()
	if len(mints) != 1 || mints[0].Path != "/api/v1/internal/discord/impersonation-tokens" || mints[0].Method != http.MethodPost {
		t.Fatalf("mints = %+v", mints)
	}
	if mints[0].Auth != "Bearer "+testSecret {
		t.Fatalf("mint auth = %s", mints[0].Auth)
	}
	var follow int
	for _, call := range h.genos.requests() {
		if call.Path == mints[0].Path {
			continue
		}
		follow++
		if call.Auth != "Bearer "+testToken || strings.Contains(strings.ToLower(call.Auth), "kube") || call.Auth == "Bearer "+testSecret {
			t.Fatalf("follow-up = %+v", call)
		}
	}
	if follow == 0 {
		t.Fatal("no follow-up Genos call")
	}
}

func assertAction(t *testing.T, call recorded, serverID, action string, confirm bool) {
	t.Helper()
	if call.Method != http.MethodPost || !strings.Contains(call.Path, "/api/v1/servers/"+serverID+"/actions") {
		t.Fatalf("action path = %s %s", call.Method, call.Path)
	}
	if !uuidPattern.MatchString(call.Key) {
		t.Fatalf("Idempotency-Key = %q", call.Key)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(call.Body), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["type"]; !ok {
		t.Fatalf("body missing type: %s", call.Body)
	}
	if _, ok := raw["confirmUnsavedProgressLoss"]; !ok {
		t.Fatalf("body missing confirmUnsavedProgressLoss: %s", call.Body)
	}
	var body struct {
		Type    string `json:"type"`
		Confirm bool   `json:"confirmUnsavedProgressLoss"`
	}
	if err := json.Unmarshal([]byte(call.Body), &body); err != nil {
		t.Fatal(err)
	}
	if body.Type != action || body.Confirm != confirm {
		t.Fatalf("action body = %+v, want type %s confirm %v", body, action, confirm)
	}
	if call.Auth != "Bearer "+testToken {
		t.Fatalf("action auth = %s", call.Auth)
	}
}

func testServer(id, name string, players int64, notable []string) genos.Server {
	if notable == nil {
		notable = []string{}
	}
	count := players
	return genos.Server{
		ID:             id,
		Name:           name,
		Status:         "Running",
		Game:           genos.Game{Name: "Factorio"},
		NotableUpdates: notable,
		Metrics:        genos.Metrics{PlayerCount: &count},
	}
}

type recorded struct {
	Method string
	Path   string
	Auth   string
	Body   string
	Key    string
}

type fakeGenos struct {
	mu       sync.Mutex
	servers  []genos.Server
	details  map[string]genos.Server
	channels []genos.Channel
	secret   string
	token    string
	expires  time.Time
	reqs     []recorded
	bad      []recorded
}

func (f *fakeGenos) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	call := recorded{
		Method: r.Method,
		Path:   r.URL.Path,
		Auth:   r.Header.Get("Authorization"),
		Body:   string(body),
		Key:    r.Header.Get("Idempotency-Key"),
	}
	f.mu.Lock()
	f.reqs = append(f.reqs, call)
	f.mu.Unlock()

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/internal/discord/impersonation-tokens":
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || call.Auth != "Bearer "+f.secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var payload struct {
			DiscordUserID string `json:"discordUserID"`
		}
		if json.Unmarshal(body, &payload) != nil || payload.DiscordUserID == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"token":     f.token,
			"expiresAt": f.expires.Format(time.RFC3339),
			"tokenKind": "discord",
		})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/servers":
		if !f.userAuth(w, call.Auth) {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"servers": f.servers})
	case r.Method == http.MethodGet && detailPattern.MatchString(r.URL.Path):
		if !f.userAuth(w, call.Auth) {
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/servers/")
		server, ok := f.detail(id)
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"server": server})
	case r.Method == http.MethodPost && actionPattern.MatchString(r.URL.Path):
		if !f.userAuth(w, call.Auth) {
			return
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || call.Key == "" {
			http.Error(w, "bad action", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"disposition":"accepted"}`))
	case r.Method == http.MethodGet && channelPattern.MatchString(r.URL.Path):
		if !f.userAuth(w, call.Auth) {
			return
		}
		channels := f.channels
		if channels == nil {
			channels = []genos.Channel{
				{ID: "container-stdout", Interaction: "read-only"},
				{ID: "rcon", Interaction: "interactive"},
				{ID: "later", Interaction: "interactive"},
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"channels": channels})
	case r.Method == http.MethodPost && commandPattern.MatchString(r.URL.Path):
		if !f.userAuth(w, call.Auth) {
			return
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "bad command", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	default:
		f.mu.Lock()
		f.bad = append(f.bad, call)
		f.mu.Unlock()
		http.NotFound(w, r)
	}
}

func (f *fakeGenos) userAuth(w http.ResponseWriter, auth string) bool {
	if auth != "Bearer "+f.token {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func (f *fakeGenos) detail(id string) (genos.Server, bool) {
	if f.details != nil {
		if server, ok := f.details[id]; ok {
			return server, true
		}
	}
	for _, server := range f.servers {
		if server.ID == id {
			return server, true
		}
	}
	return genos.Server{}, false
}

func (f *fakeGenos) requests() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.reqs...)
}

func (f *fakeGenos) unexpected() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.bad...)
}

func (f *fakeGenos) mints() []recorded {
	return f.filter(func(call recorded) bool {
		return call.Path == "/api/v1/internal/discord/impersonation-tokens"
	})
}

func (f *fakeGenos) actions() []recorded {
	return f.filter(func(call recorded) bool {
		return call.Method == http.MethodPost && actionPattern.MatchString(call.Path)
	})
}

func (f *fakeGenos) commands() []recorded {
	return f.filter(func(call recorded) bool {
		return call.Method == http.MethodPost && commandPattern.MatchString(call.Path)
	})
}

func (f *fakeGenos) filter(match func(recorded) bool) []recorded {
	var out []recorded
	for _, call := range f.requests() {
		if match(call) {
			out = append(out, call)
		}
	}
	return out
}

type fakeDiscord struct {
	mu     sync.Mutex
	status int
	reqs   []recorded
	bad    []recorded
}

func (f *fakeDiscord) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	call := recorded{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization")}
	f.mu.Lock()
	f.reqs = append(f.reqs, call)
	f.mu.Unlock()
	if rolePattern.MatchString(r.URL.Path) && (r.Method == http.MethodPut || r.Method == http.MethodDelete) {
		status := f.status
		if status == 0 {
			status = http.StatusNoContent
		}
		w.WriteHeader(status)
		return
	}
	f.mu.Lock()
	f.bad = append(f.bad, call)
	f.mu.Unlock()
	http.NotFound(w, r)
}

func (f *fakeDiscord) calls() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.reqs...)
}

func (f *fakeDiscord) unexpected() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.bad...)
}
