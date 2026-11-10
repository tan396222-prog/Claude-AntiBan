package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func parityJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func parityUI(t *testing.T, s *Server, method, path string, value any, status int) []byte {
	t.Helper()
	body := ""
	if value != nil {
		body = string(parityJSON(t, value))
	}
	response := request(s, method, path, s.origin, body, true)
	if response.Code != status {
		t.Fatalf("UI %s %s: HTTP %d: %s", method, path, response.Code, response.Body.String())
	}
	return response.Body.Bytes()
}

func parityAgent(t *testing.T, s *Server, method, path string, value any, status int) []byte {
	t.Helper()
	var input io.Reader
	if value != nil {
		input = strings.NewReader(string(parityJSON(t, value)))
	}
	response, err := AgentRequest(context.Background(), s.configPath, AgentTarget{Scope: "local"}, method, path, input, "application/json", "")
	if response.HTTPStatus != status {
		t.Fatalf("Agent %s %s: HTTP %d, error %v", method, path, response.HTTPStatus, err)
	}
	if status == http.StatusOK && err != nil {
		t.Fatalf("Agent %s %s: %v", method, path, err)
	}
	if status >= 400 && err == nil {
		t.Fatalf("Agent reported success for HTTP %d", status)
	}
	return response.Data
}

func parityDecode[T any](t *testing.T, body []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("decode: %v: %s", err, body)
	}
	return value
}

func parityAccount(t *testing.T, s *Server) {
	t.Helper()
	s.account.lock()
	a, err := loadLocalAccount(s.profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.create("Parity account", "parity-old-password", ""); err != nil {
		t.Fatal(err)
	}
	s.account = a
	t.Cleanup(a.lock)
}

func TestAgentParityAccountProfilePasswordAndLock(t *testing.T) {
	s := agentTestController(t)
	parityAccount(t, s)
	parityUI(t, s, "POST", "/api/account/profile", map[string]any{"username": "From UI", "avatar": ""}, 200)
	view := parityDecode[accountView](t, parityAgent(t, s, "GET", "/api/account", nil, 200))
	if view.Username != "From UI" {
		t.Fatal("Agent did not read UI profile", view.Username)
	}
	data, err := AgentCall(context.Background(), s.configPath, "account.profile", parityJSON(t, map[string]any{"username": "From Agent", "avatar": ""}))
	if err != nil {
		t.Fatal(err)
	}
	if parityDecode[accountView](t, data).Username != "From Agent" {
		t.Fatal("Agent profile not confirmed")
	}
	view = parityDecode[accountView](t, parityUI(t, s, "GET", "/api/account", nil, 200))
	if view.Username != "From Agent" {
		t.Fatal("UI did not read Agent profile")
	}
	stored, err := loadLocalAccount(s.profile)
	if err != nil {
		t.Fatal(err)
	}
	if stored.view().Username != "From Agent" {
		t.Fatal("profile success preceded persistence")
	}
	before, err := os.ReadFile(s.account.path)
	if err != nil {
		t.Fatal(err)
	}
	wrong := map[string]any{"old_password": "wrong", "password": "parity-new-password", "confirm_password": "parity-new-password"}
	parityAgent(t, s, "POST", "/api/account/password", wrong, 400)
	s.account.mu.Lock()
	s.account.nextAttempt = time.Time{}
	s.account.mu.Unlock()
	parityUI(t, s, "POST", "/api/account/password", wrong, 400)
	s.account.mu.Lock()
	s.account.nextAttempt = time.Time{}
	s.account.mu.Unlock()
	after, _ := os.ReadFile(s.account.path)
	if string(before) != string(after) {
		t.Fatal("rejected password change modified account")
	}
	parityAgent(t, s, "POST", "/api/account/password", map[string]any{"old_password": "parity-old-password", "password": "parity-new-password", "confirm_password": "parity-new-password"}, 200)
	parityUI(t, s, "POST", "/api/account/lock", map[string]any{}, 200)
	parityAgent(t, s, "GET", "/api/settings", nil, 423)
	parityUI(t, s, "GET", "/api/settings", nil, 423)
	parityUI(t, s, "POST", "/api/account/unlock", map[string]any{"password": "parity-old-password"}, 400)
	s.account.mu.Lock()
	s.account.nextAttempt = time.Time{}
	s.account.mu.Unlock()
	parityAgent(t, s, "POST", "/api/account/unlock", map[string]any{"password": "parity-new-password"}, 200)
	parityAgent(t, s, "POST", "/api/account/lock", map[string]any{}, 200)
	parityUI(t, s, "GET", "/api/settings", nil, 423)
	parityUI(t, s, "POST", "/api/account/unlock", map[string]any{"password": "parity-new-password"}, 200)
	stored, err = loadLocalAccount(s.profile)
	if err != nil {
		t.Fatal(err)
	}
	defer stored.lock()
	if err := stored.unlock("parity-new-password"); err != nil {
		t.Fatal("password update did not survive reload", err)
	}
}

func TestAgentParityAppearanceNestedValuesAndFavorites(t *testing.T) {
	s := agentTestController(t)
	appearance := defaultAppearance()
	appearance.Mode = "dark"
	appearance.UIFontSize = 18
	appearance.Dark.Colors = map[string]string{"sidebar": "#123456", "focus": "#abcdef"}
	appearance.Dark.Terminal = map[string]string{"background": "#101010", "brightRed": "#ee1111"}
	appearance.CustomThemes = []SavedAppearanceTheme{{ID: "parity", Name: "Saved by UI", Light: appearance.Light, Dark: appearance.Dark}}
	parityUI(t, s, "POST", "/api/appearance", appearance, 200)
	read := parityDecode[Appearance](t, parityAgent(t, s, "GET", "/api/appearance", nil, 200))
	if !reflect.DeepEqual(appearance, read) {
		t.Fatalf("Agent lost nested appearance values: %+v", read)
	}
	read.CustomThemes[0].Name = "Renamed by Agent"
	read.CodeFontSize = 17
	read.Dark.Fonts.Code = "JetBrains Mono"
	data, err := AgentCall(context.Background(), s.configPath, "appearance.set", parityJSON(t, read))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read, parityDecode[Appearance](t, data)) {
		t.Fatal("Agent appearance confirmation differs")
	}
	if !reflect.DeepEqual(read, parityDecode[Appearance](t, parityUI(t, s, "GET", "/api/appearance", nil, 200))) {
		t.Fatal("UI cannot see saved Agent appearance")
	}
	file, err := os.ReadFile(filepath.Join(s.profile, "appearance.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read, parityDecode[Appearance](t, file)) {
		t.Fatal("appearance not durable")
	}
	folders := parityDecode[Favorites](t, parityUI(t, s, "POST", "/api/favorites", map[string]any{"action": "save_folder", "title": "UI folder"}, 200))
	if len(folders.Folders) != 1 {
		t.Fatal("missing saved folder")
	}
	data, err = AgentCall(context.Background(), s.configPath, "favorites.change", parityJSON(t, map[string]any{"action": "save", "title": "Agent bookmark", "url": "https://example.test/path?q=one", "folder_id": folders.Folders[0].ID}))
	if err != nil {
		t.Fatal(err)
	}
	favorites := parityDecode[Favorites](t, data)
	if len(favorites.Items) != 1 || favorites.Items[0].FolderID != folders.Folders[0].ID {
		t.Fatal("Agent failed to use UI-created folder")
	}
	parityUI(t, s, "POST", "/api/favorites", map[string]any{"action": "save", "id": favorites.Items[0].ID, "title": "UI rename", "url": favorites.Items[0].URL, "folder_id": ""}, 200)
	favorites = parityDecode[Favorites](t, parityAgent(t, s, "GET", "/api/favorites", nil, 200))
	if favorites.Items[0].Title != "UI rename" || favorites.Items[0].FolderID != "" {
		t.Fatal("Agent lost latest UI bookmark metadata")
	}
	file, err = os.ReadFile(filepath.Join(s.profile, "favorites.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(favorites, parityDecode[Favorites](t, file)) {
		t.Fatal("favorites not durable")
	}
	parityAgent(t, s, "POST", "/api/account/lock", map[string]any{}, 200)
	parityAgent(t, s, "POST", "/api/appearance", appearance, 423)
	parityUI(t, s, "POST", "/api/appearance", appearance, 423)
	parityAgent(t, s, "GET", "/api/favorites", nil, 423)
	parityUI(t, s, "GET", "/api/favorites", nil, 423)
}

func TestAgentParityVaultMetadataAndReauthentication(t *testing.T) {
	s := agentTestController(t)
	parityAccount(t, s)
	const secret = "parity-website-secret-should-not-be-redacted"
	entry := map[string]any{"origin": "https://vault.example.test/login", "username": "owner@example.test", "password": secret}
	parityUI(t, s, "POST", "/api/vault", map[string]any{"action": "save", "entry": entry}, 200)
	listBody := parityAgent(t, s, "GET", "/api/vault", nil, 200)
	if strings.Contains(string(listBody), secret) {
		t.Fatal("ordinary Agent list leaked password")
	}
	list := parityDecode[vaultData](t, listBody)
	if len(list.Entries) != 1 || list.Entries[0].Origin != "https://vault.example.test" || list.Entries[0].Username != "owner@example.test" {
		t.Fatal("Agent cannot discover UI record", string(listBody))
	}
	id := list.Entries[0].ID
	reveal := map[string]any{"action": "reveal", "id": id, "account_password": "parity-old-password"}
	result, err := AgentCall(context.Background(), s.configPath, "vault.change", parityJSON(t, reveal))
	if err != nil {
		t.Fatal(err)
	}
	if parityDecode[struct {
		Password string `json:"password"`
	}](t, result).Password != secret {
		t.Fatal("explicit authenticated reveal was redacted")
	}
	wrong := map[string]any{"action": "reveal", "id": id, "account_password": "wrong"}
	parityAgent(t, s, "POST", "/api/vault", wrong, 400)
	s.account.mu.Lock()
	s.account.nextAttempt = time.Time{}
	s.account.mu.Unlock()
	parityUI(t, s, "POST", "/api/vault", wrong, 400)
	s.account.mu.Lock()
	s.account.nextAttempt = time.Time{}
	s.account.mu.Unlock()
	parityAgent(t, s, "POST", "/api/vault", map[string]any{"action": "save", "entry": map[string]any{"id": id, "origin": "https://vault.example.test", "username": "changed-by-agent", "password": ""}}, 200)
	if parityDecode[struct {
		Password string `json:"password"`
	}](t, parityUI(t, s, "POST", "/api/vault", reveal, 200)).Password != secret {
		t.Fatal("Agent metadata edit erased password")
	}
	list = parityDecode[vaultData](t, parityUI(t, s, "GET", "/api/vault", nil, 200))
	if list.Entries[0].Username != "changed-by-agent" {
		t.Fatal("UI missed Agent edit")
	}
	file, err := os.ReadFile(filepath.Join(s.profile, "password-vault.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(file), secret) || strings.Contains(string(file), "changed-by-agent") {
		t.Fatal("vault persisted plaintext")
	}
	parityAgent(t, s, "POST", "/api/account/lock", map[string]any{}, 200)
	parityAgent(t, s, "POST", "/api/vault", reveal, 423)
	parityUI(t, s, "POST", "/api/vault", reveal, 423)
}

func TestAgentParityBrowserHomeFailureAndLength(t *testing.T) {
	s := agentTestController(t)
	s.preferences.BrowserHomeURL = "https://old.example.test/"
	s.preferences.ClaudeTerminalCommand = "claude --continue"
	if err := writePrivate(filepath.Join(s.profile, "workbench.json"), s.preferences); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(s.profile, "workbench.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.ready = true
	guestCalls := 0
	s.browserControl = browserClientForTest(t, s, browserTransport(func(r *http.Request) (*http.Response, error) {
		guestCalls++
		if r.URL.Path != "/homepage" {
			t.Fatal("unexpected guest lifecycle operation", r.URL.Path)
		}
		return nil, errors.New("simulated offline guest transport")
	}))
	const replacement = "https://new.example.test/"
	parityUI(t, s, "POST", "/api/browser/preferences", map[string]any{"browser_home_url": replacement}, 400)
	_, err = AgentCall(context.Background(), s.configPath, "settings.patch", parityJSON(t, map[string]any{"patch": map[string]any{"browser_home_url": replacement}}))
	if err == nil {
		t.Fatal("Agent accepted homepage runtime failure")
	}
	if guestCalls != 2 {
		t.Fatal("both entry points must reach the live browser", guestCalls)
	}
	after, _ := os.ReadFile(filepath.Join(s.profile, "workbench.json"))
	if string(before) != string(after) || s.preferences.BrowserHomeURL != "https://old.example.test/" {
		t.Fatal("failed update changed persisted or visible homepage")
	}
	tooLong := "https://example.test/" + strings.Repeat("a", 8192)
	parityUI(t, s, "POST", "/api/browser/preferences", map[string]any{"browser_home_url": tooLong}, 400)
	parityAgent(t, s, "POST", "/api/browser/preferences", map[string]any{"browser_home_url": tooLong}, 400)
	_, err = AgentCall(context.Background(), s.configPath, "settings.patch", parityJSON(t, map[string]any{"patch": map[string]any{"browser_home_url": tooLong}}))
	if err == nil {
		t.Fatal("Agent patch bypassed homepage limit")
	}
	if guestCalls != 2 {
		t.Fatal("invalid homepage reached guest")
	}
	s.ready = false
	parityUI(t, s, "POST", "/api/browser/preferences", map[string]any{"browser_home_url": replacement}, 200)
	data, err := AgentCall(context.Background(), s.configPath, "browser.preferences.get", nil)
	if err != nil {
		t.Fatal(err)
	}
	if parityDecode[map[string]string](t, data)["browser_home_url"] != replacement {
		t.Fatal("Agent cannot read UI homepage")
	}
	const final = "https://agent.example.test/"
	_, err = AgentCall(context.Background(), s.configPath, "settings.patch", parityJSON(t, map[string]any{"patch": map[string]any{"browser_home_url": final}}))
	if err != nil {
		t.Fatal(err)
	}
	if parityDecode[map[string]string](t, parityUI(t, s, "GET", "/api/browser/preferences", nil, 200))["browser_home_url"] != final {
		t.Fatal("UI missed Agent homepage")
	}
	var saved Preferences
	body, _ := os.ReadFile(filepath.Join(s.profile, "workbench.json"))
	_ = json.Unmarshal(body, &saved)
	if saved.BrowserHomeURL != final || saved.ClaudeTerminalCommand != "claude --continue" {
		t.Fatal("homepage success discarded unrelated preferences", saved)
	}
}

func TestAgentParityClaudeSchedulingPointerValues(t *testing.T) {
	s := agentTestController(t)
	firstID, secondID := strings.Repeat("a", 24), strings.Repeat("b", 24)
	list := claudeAccountList{Accounts: []claudeAccount{
		{ID: firstID, Source: "default", Email: "first@example.test", Label: "First", Enabled: true, HandoffGroup: "shared"},
		{ID: secondID, Source: "default", Email: "second@example.test", Label: "Second", Enabled: true, HandoffGroup: "shared"},
	}, Scheduling: (claudeScheduling{}).defaults()}
	if err := s.saveClaudeAccounts(list); err != nil {
		t.Fatal(err)
	}
	// UI input explicitly sets a pointer bool false and clears a pointer string.
	parityUI(t, s, "POST", "/api/claude-accounts", map[string]any{"action": "update", "id": firstID, "enabled": false, "handoff_group": ""}, 200)
	data, err := AgentCall(context.Background(), s.configPath, "claude-accounts.get", nil)
	if err != nil {
		t.Fatal(err)
	}
	view := parityDecode[claudePanel](t, data)
	if view.Accounts[0].Enabled || view.Accounts[0].HandoffGroup != "" {
		t.Fatal("Agent lost explicit false/empty UI fields")
	}
	data, err = AgentCall(context.Background(), s.configPath, "claude-accounts.change", parityJSON(t, map[string]any{"action": "update", "id": firstID, "enabled": true, "handoff_group": "from-agent"}))
	if err != nil {
		t.Fatal(err)
	}
	view = parityDecode[claudePanel](t, parityUI(t, s, "GET", "/api/claude-accounts", nil, 200))
	if !view.Accounts[0].Enabled || view.Accounts[0].HandoffGroup != "from-agent" {
		t.Fatal("UI missed Agent pointer-field update")
	}
	_, err = AgentCall(context.Background(), s.configPath, "claude-accounts.change", parityJSON(t, map[string]any{"action": "scheduling", "scheduling": map[string]any{"mode": "sequential", "switch_policy": "wait"}, "order": []string{secondID, firstID}}))
	if err != nil {
		t.Fatal(err)
	}
	view = parityDecode[claudePanel](t, parityUI(t, s, "GET", "/api/claude-accounts", nil, 200))
	if view.Scheduling.Mode != "sequential" || view.Scheduling.SwitchPolicy != "wait" || view.Accounts[0].ID != secondID {
		t.Fatal("UI lost nested scheduling or order", view)
	}
	stored, err := s.loadClaudeAccounts()
	if err != nil {
		t.Fatal(err)
	}
	if stored.Scheduling != view.Scheduling || stored.Accounts[0].ID != secondID {
		t.Fatal("scheduling/order success was not durable")
	}
	for _, input := range []map[string]any{
		{"action": "update", "id": firstID, "enabled": "false"},
		{"action": "scheduling", "scheduling": map[string]any{"mode": "sequential", "switch_policy": "wait", "unknown": true}},
	} {
		body, _ := os.ReadFile(filepath.Join(s.profile, "claude-accounts.json"))
		if _, err := AgentCall(context.Background(), s.configPath, "claude-accounts.change", parityJSON(t, input)); err == nil {
			t.Fatal("Agent accepted invalid nested/pointer value", input)
		}
		parityUI(t, s, "POST", "/api/claude-accounts", input, 400)
		after, _ := os.ReadFile(filepath.Join(s.profile, "claude-accounts.json"))
		if string(body) != string(after) {
			t.Fatal("invalid scheduling write changed saved registry")
		}
	}
}
