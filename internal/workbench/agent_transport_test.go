package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentRoutesComeFromTheActualListener(t *testing.T) {
	s := agentTestController(t)
	response, err := AgentRequest(context.Background(), s.configPath, AgentTarget{}, "GET", "/api/agent/routes", nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var routes []string
	_ = json.Unmarshal(response.Data, &routes)
	for _, pattern := range []string{"POST /api/agent/native", "POST /api/browser/extensions/upload", "POST /api/account/{action}", "GET /api/proxy"} {
		found := false
		for _, route := range routes {
			found = found || route == pattern
		}
		if !found {
			t.Fatal("missing registered UI route", pattern)
		}
	}
}

func TestAgentRawInputAndBinaryOutputUseProductionHandler(t *testing.T) {
	s := agentTestController(t)
	s.ready = true
	var received string
	s.browserControl = browserClientForTest(t, s, browserTransport(func(r *http.Request) (*http.Response, error) {
		body := make([]byte, 4)
		_, _ = r.Body.Read(body)
		received = string(body)
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"upload":"accepted"}`))}, nil
	}))
	// Existing UI upload boundary validates and forwards the binary archive.
	upload, err := AgentRequest(context.Background(), s.configPath, AgentTarget{}, "POST", "/api/browser/extensions/upload", strings.NewReader("ZIP!"), "application/octet-stream", "")
	if err != nil || !strings.Contains(string(upload.Data), "accepted") {
		t.Fatal(upload, err)
	}
	if received != "ZIP!" {
		t.Fatal("raw upload never reached production receiver", err, received)
	}
	path := filepath.Join(t.TempDir(), "bundle.zip")
	result, err := AgentRequest(context.Background(), s.configPath, AgentTarget{}, "GET", "/api/diagnostics/export", nil, "", path)
	if err != nil || result.Path != path {
		t.Fatal(result, err)
	}
	before, err := os.ReadFile(path)
	if err != nil || len(before) < 2 || string(before[:2]) != "PK" {
		t.Fatal("not a real diagnostic ZIP", err)
	}
	_, err = AgentRequest(context.Background(), s.configPath, AgentTarget{}, "GET", "/api/diagnostics/export", nil, "", path)
	if err == nil {
		t.Fatal("existing output replaced")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("original output changed")
	}
}

func TestAgentNativeQueueClaimsOnceAndKeepsReceipts(t *testing.T) {
	s := agentTestController(t)
	s.account.lock() // Native account unlock must be reachable from the lock screen.
	if w := request(s, "POST", "/api/agent/native", s.origin, `{"connection_id":"local","request":{"action":"accountDevice"}}`, true); w.Code != 403 {
		t.Fatal("submission lacked launcher proof", w.Code)
	}
	data, err := AgentCall(context.Background(), s.configPath, "native.submit", json.RawMessage(`{"connection_id":"local","request":{"action":"accountDevice","operation":"status"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var item agentNativeItem
	_ = json.Unmarshal(data, &item)
	if item.Status != "pending" {
		t.Fatal(string(data))
	}
	if w := request(s, "GET", "/api/agent/native?consumer=window", "", "", true); w.Code != 403 {
		t.Fatal("ordinary session could claim native request", w.Code)
	}
	ticket := s.issueTicket()
	registered := request(s, "POST", "/api/agent/native/window", s.origin, `{"ticket":"`+ticket+`"}`, true)
	var grant struct {
		Proof string `json:"window_proof"`
	}
	_ = json.Unmarshal(registered.Body.Bytes(), &grant)
	if registered.Code != 200 || len(grant.Proof) != 64 {
		t.Fatal("native ticket was not exchanged", registered.Code)
	}
	if replay := request(s, "POST", "/api/agent/native/window", s.origin, `{"ticket":"`+ticket+`"}`, true); replay.Code != 403 {
		t.Fatal("native ticket replay accepted")
	}
	claim := func(connection string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", s.origin+"/api/agent/native?consumer=window", nil)
		r.Header.Set(connectionHeader, connection)
		r.Header.Set("X-Cage-Agent-Window", grant.Proof)
		r.AddCookie(&http.Cookie{Name: s.cookieName(), Value: s.session})
		w := httptest.NewRecorder()
		s.handler().ServeHTTP(w, r)
		return w
	}
	if w := claim("old-generation"); strings.Contains(w.Body.String(), item.ID) {
		t.Fatal("wrong window claimed action")
	}
	if w := claim("local"); !strings.Contains(w.Body.String(), item.ID) {
		t.Fatal("correct window cannot claim", w.Code, w.Body.String())
	}
	if w := claim("local"); strings.Contains(w.Body.String(), item.ID) {
		t.Fatal("native operation replayed")
	}
	complete := "/api/agent/native/" + item.ID + "/complete"
	for _, body := range []string{`{"result":{"supported":false}}`, `{"result":{"supported":true}}`} {
		if denied := request(s, "POST", complete, s.origin, body, true); denied.Code != 403 {
			t.Fatal("ordinary session forged native completion")
		}
		r := httptest.NewRequest("POST", s.origin+complete, strings.NewReader(body))
		r.Header.Set("Origin", s.origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Cage-Agent-Window", grant.Proof)
		r.AddCookie(&http.Cookie{Name: s.cookieName(), Value: s.session})
		w := httptest.NewRecorder()
		s.handler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	result, err := AgentRequest(context.Background(), s.configPath, AgentTarget{}, "GET", "/api/agent/native/"+item.ID, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result.Data), `"supported":false`) {
		t.Fatal("repeated completion changed original result", string(result.Data))
	}
	if w := request(s, "GET", "/api/agent/native/"+item.ID, "", "", true); w.Code != 403 {
		t.Fatal("untrusted view read native secret")
	}
}

func TestAgentDesktopConfigurationUsesExplicitSelection(t *testing.T) {
	full := filepath.Join(t.TempDir(), "config.json")
	s := agentTestController(t)
	// Reuse this production listener under the actual desktop-entry location.
	s.desktopEntry = true
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { jsonReply(w, map[string]bool{"ok": true}) }))
	t.Cleanup(endpoint.Close)
	s.remote = newDesktopGateway(s.profile, &gatewayLocalFixture{endpoint: endpoint, status: DesktopLocalStatus{Known: true}})
	t.Cleanup(s.remote.close)
	path := desktopConfigPath(full)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	record, err := os.ReadFile(filepath.Join(s.profile, "control.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(filepath.Join(filepath.Dir(path), "control.json"), json.RawMessage(record)); err != nil {
		t.Fatal(err)
	}
	lock, err := openControllerLock(filepath.Dir(path), os.O_CREATE|os.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	if err := lockController(lock); err != nil {
		t.Fatal(err)
	}
	defer func() { unlockController(lock); lock.Close() }()
	s.configPath = path
	target := AgentTarget{Scope: "desktop"}
	result, err := AgentRequest(context.Background(), full, target, "GET", "/api/client/context", nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var current ClientContext
	_ = json.Unmarshal(result.Data, &current)
	if !current.DesktopGateway || current.ConnectionID == "" {
		t.Fatal(string(result.Data))
	}
	body := `{"name":"test host","host":"127.0.0.1","port":22,"user":"qa","auth":{"kind":"agent"}}`
	result, err = AgentRequest(context.Background(), full, target, "POST", "/api/client/hosts", strings.NewReader(body), "application/json", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.HTTPStatus != 200 || !strings.Contains(string(result.Data), "test host") {
		t.Fatal(string(result.Data))
	}
	_, err = AgentRequest(context.Background(), full, target, "POST", "/api/stop", strings.NewReader(`{}`), "application/json", "")
	var ae *AgentError
	if !errors.As(err, &ae) || ae.Code != "invalid_input" {
		t.Fatal("implicit target accepted", err)
	}
	_, err = AgentRequest(context.Background(), full, AgentTarget{Scope: "selected", Connection: "old"}, "POST", "/api/stop", strings.NewReader(`{}`), "application/json", "")
	if !errors.As(err, &ae) || ae.Code != "state_conflict" {
		t.Fatal("stale target accepted", err)
	}
	// A real candidate creation returns 202. The HTTP adapter must preserve it.
	result, err = AgentRequest(context.Background(), full, target, "POST", "/api/client/candidate", strings.NewReader(`{"mode":"local","connection_id":"`+current.ConnectionID+`"}`), "application/json", "")
	if err != nil || result.HTTPStatus != 202 {
		t.Fatalf("candidate 202 rejected: %+v %v", result, err)
	}
}

func TestAgentSocketUsesTheSameTerminalAndAccountLock(t *testing.T) {
	s := agentTestController(t)
	t.Cleanup(s.terminals.close)
	session, err := s.terminals.start(terminalChildCommand("echo"), "/project", "shell")
	if err != nil {
		t.Fatal(err)
	}
	input, writer := io.Pipe()
	output, reader := io.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		err := AgentSocket(ctx, s.configPath, AgentTarget{}, "/api/terminals/"+session.id+"/stream", input, reader)
		reader.Close()
		done <- err
	}()
	type frame struct {
		Status string          `json:"status"`
		Type   string          `json:"type"`
		Data   json.RawMessage `json:"data"`
	}
	decoder := json.NewDecoder(output)
	var connected frame
	if err := decoder.Decode(&connected); err != nil || connected.Status != "connected" {
		t.Fatal(connected, err)
	}
	payload, _ := json.Marshal(map[string]any{"type": "input", "data": terminalInputLine("agent-socket-parity")})
	if err := json.NewEncoder(writer).Encode(map[string]any{"type": "text", "data": string(payload)}); err != nil {
		t.Fatal(err)
	}
	var captured strings.Builder
	for !strings.Contains(captured.String(), "child:agent-socket-parity") {
		var item frame
		if err := decoder.Decode(&item); err != nil {
			t.Fatal(err, captured.String())
		}
		if item.Type == "binary" {
			var data []byte
			_ = json.Unmarshal(item.Data, &data)
			captured.Write(data)
		}
	}
	cancel()
	writer.Close()
	output.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	s.account.lock()
	if err := AgentSocket(context.Background(), s.configPath, AgentTarget{}, "/api/terminals/"+session.id+"/stream", io.NopCloser(strings.NewReader("")), io.Discard); err == nil {
		t.Fatal("locked account retained terminal access")
	}
}

func TestAgentHostGitUsesSameExplicitConsentAndPreservesConfig(t *testing.T) {
	path := isolatedGitConfig(t)
	s := agentTestController(t)
	if _, err := AgentCall(context.Background(), s.configPath, "host.git.enable", json.RawMessage(`{}`)); err == nil {
		t.Fatal("missing explicit consent accepted")
	}
	_, err := AgentCall(context.Background(), s.configPath, "host.git.enable", json.RawMessage(`{"confirm":true}`))
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(body), "bareRepository = explicit") || !strings.Contains(string(body), "name = Cage Test") {
		t.Fatal("real Git config not preserved", err, string(body))
	}
	ui := request(s, "GET", "/api/host/git", "", "", true)
	if !strings.Contains(ui.Body.String(), `"ready":true`) {
		t.Fatal(ui.Body.String())
	}
}

func TestAgentDoesNotExportNativeDeviceWrappingCredentials(t *testing.T) {
	for _, path := range []string{"/api/agent/native/window", "/api/account/device-enroll", "/api/account/device-unlock", "/api/account/device-revoke", "/api/launch", "/api/session", "/api/desktop-local/trusted-login"} {
		_, err := AgentRequest(context.Background(), filepath.Join(t.TempDir(), "config.json"), AgentTarget{}, "POST", path, strings.NewReader(`{}`), "application/json", "")
		var ae *AgentError
		if !errors.As(err, &ae) || ae.Code != "native_transport_required" {
			t.Fatal(path, err)
		}
	}
}

func TestAgentSocketRejectsMalformedInputWithoutWaitingForRemote(t *testing.T) {
	s := agentTestController(t)
	t.Cleanup(s.terminals.close)
	session, err := s.terminals.start(terminalChildCommand("echo"), "/project", "shell")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := time.Now()
	err = AgentSocket(ctx, s.configPath, AgentTarget{}, "/api/terminals/"+session.id+"/stream", io.NopCloser(strings.NewReader("malformed JSON")), io.Discard)
	var ae *AgentError
	if !errors.As(err, &ae) || ae.Code != "invalid_input" || time.Since(started) > time.Second {
		t.Fatal("malformed input hung or was reported as success", err)
	}
}
