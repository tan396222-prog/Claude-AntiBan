//go:build !windows

package workbench

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"

	"al.essio.dev/pkg/shellescape"
	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/mobilelink"
	"github.com/sylvinus/agent-vm/internal/relay"
	"github.com/sylvinus/agent-vm/internal/remotelink"
)

func TestCLIChannelProtocol(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	cmd := exec.Command(python, "-I", "cli_channel_test.py")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("channel protocol: %v\n%s", err, output)
	}
}

func TestCLIChannelCommandFitsWindows(t *testing.T) {
	// Leave room in CreateProcessW's 32767 UTF-16 limit for SSH paths and the
	// login-shell quoting around this command.
	command := cliChannelCommand("launch", `{"id":"`+uuid.NewString()+`","prefix":["claude"]}`)
	t.Logf("embedded command: %d UTF-16 units", len(utf16.Encode([]rune(command))))
	if size := len(utf16.Encode([]rune(command))); size > 16000 {
		t.Fatalf("channel command uses %d UTF-16 units", size)
	}
}

func TestSharedSendReceiptSurvivesReadFailure(t *testing.T) {
	s, id, project := metadataFixture(t)
	home := t.TempDir()
	mobileTestSSH(t, s, home, []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/local/bin"})
	record, err := s.mobileCLI.record(s.profile, id)
	if err != nil {
		t.Fatal(err)
	}
	record.Terminal = true
	record.Connection = uuid.NewString()
	record.GuestWorkspace = guestPath(project)
	if err = writePrivate(mobileRecordPath(s.profile, id), record); err != nil {
		t.Fatal(err)
	}
	// Keep the real host SSH/bootstrap/RPC path; replace only the guest
	// operation boundary to lose the read immediately after an admitted send.
	helper := filepath.Join(home, ".local/share/cage/cli-channel-code", fmt.Sprintf("%x.py", sha256.Sum256([]byte(cliChannelHelper+cliResumePrompt+claudeAccountResumePrompt))))
	if err = os.MkdirAll(filepath.Dir(helper), 0700); err != nil {
		t.Fatal(err)
	}
	code := "import json,sys\nrequest=json.load(sys.stdin)\nif sys.argv[1]=='rpc': print(json.dumps({'ok':True}))\nelse: sys.exit(3)\n"
	if err = os.WriteFile(helper, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	epoch, ok := s.account.access()
	if !ok {
		t.Fatal("missing unlock epoch")
	}
	ctx := context.WithValue(context.Background(), mobileEpochKey{}, epoch)
	requestID := uuid.NewString()
	view, err := s.terminalConversationAction(ctx, id, map[string]any{"action": "send", "request_id": requestID, "text": "accepted message"})
	if err != nil || view.AcknowledgedRequestID != requestID {
		t.Fatalf("admitted send lost its receipt: %#v %v", view, err)
	}
	if len(view.Turns) != len(record.Turns)+1 || view.Turns[len(view.Turns)-1].Text != "" || view.Turns[len(view.Turns)-1].Status != "queued" {
		t.Fatal("receipt did not preserve the accepted message without inventing a reply")
	}
}

func TestSessionCatalogActivity(t *testing.T) {
	s, id, project := metadataFixture(t)
	record, err := s.mobileCLI.record(s.profile, id)
	if err != nil {
		t.Fatal(err)
	}
	record.Terminal = true
	record.Active = true
	if err := writePrivate(mobileRecordPath(s.profile, id), record); err != nil {
		t.Fatal(err)
	}
	s.mobileCLI.catalog = nil
	list, err := s.mobileCLI.summaries(s.profile, map[string]bool{project: true}, false)
	if err != nil || len(list) != 1 || !list[0].Active {
		t.Fatal("catalog lost task activity", list, err)
	}
	record.Terminal = false
	record.Active = false
	record.Turns = []mobileTurn{{Status: "running"}}
	s.mobileCLI.active = map[string]*mobileRun{id: {record: record}}
	list, err = s.mobileCLI.summaries(s.profile, map[string]bool{project: true}, false)
	if err != nil || !list[0].Active {
		t.Fatal("mobile turn activity missing", list, err)
	}
	record.Turns[0].Status = "completed"
	list, err = s.mobileCLI.summaries(s.profile, map[string]bool{project: true}, false)
	if err != nil || list[0].Active {
		t.Fatal("completed turn stayed active", list, err)
	}
}

func TestImportedCLIUUIDIsIndependentOfPublicSessionID(t *testing.T) {
	profile, id := t.TempDir(), uuid.NewString()
	record := &mobileRecord{mobileSession: mobileSession{mobileSummary: mobileSummary{ID: id, Workspace: "/project"}, Turns: []mobileTurn{}}, CLIID: "c4cbf880-92e8-11f1-8596-0242ac120002", Terminal: true}
	if err := writePrivate(mobileRecordPath(profile, id), record); err != nil {
		t.Fatal(err)
	}
	var store mobileCLIStore
	if _, err := store.record(profile, id); err != nil {
		t.Fatal(err)
	}
	if mobileID(record.CLIID) {
		t.Fatal("CLI identity widened the client creation contract")
	}
}

func TestArchivedCLIHistoryIsNotRediscovered(t *testing.T) {
	s, id, project := metadataFixture(t)
	home, _ := filepath.EvalSymlinks(t.TempDir())
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	// This archived history has no live CLI process. Keep the real discovery
	// helper and SSH path independent of a developer's installed Claude CLI.
	cli := "#!/bin/sh\n[ \"$#\" -eq 2 ] && [ \"$1\" = agents ] && [ \"$2\" = --json ] || exit 64\nprintf '[]\\n'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(cli), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".bash_profile"), []byte("export PATH="+shellescape.Quote(bin)+":/usr/bin:/bin\n"), 0600); err != nil {
		t.Fatal(err)
	}
	mobileTestSSH(t, s, home, []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin", "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude")})
	record, err := s.mobileCLI.record(s.profile, id)
	if err != nil {
		t.Fatal(err)
	}
	history := filepath.Join(home, ".claude", "projects", "fixture", record.CLIID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(history), 0700); err != nil {
		t.Fatal(err)
	}
	row, _ := json.Marshal(map[string]any{"cwd": guestPath(project), "type": "user", "uuid": uuid.NewString(), "message": map[string]string{"content": "guest title"}})
	if err := os.WriteFile(history, append(row, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.mobileCLI.summaries(s.profile, map[string]bool{project: true}, false); err != nil {
		t.Fatal(err)
	}
	s.ready = true
	if err := s.discoverTerminalConversations(context.Background(), []string{project}); err != nil {
		t.Fatal(err)
	}
	if w := request(s, "POST", "/api/mobile/cli/sessions/"+id+"/metadata", s.origin, `{"title":"保留用户命名","pinned":true}`, true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := s.discoverTerminalConversations(context.Background(), []string{project}); err != nil {
		t.Fatal(err)
	}
	list, _ := s.mobileCLI.summaries(s.profile, map[string]bool{project: true}, false)
	if len(list) != 1 || list[0].Title != "保留用户命名" || !list[0].Pinned {
		t.Fatal("discovery overwrote metadata", list)
	}
	if err := s.setSessionArchived(id, true, nil); err != nil {
		t.Fatal(err)
	}
	s.mobileCLI = mobileCLIStore{}
	if _, err := s.mobileCLI.summaries(s.profile, map[string]bool{project: true}, false); err != nil {
		t.Fatal(err)
	}
	if err := s.discoverTerminalConversations(context.Background(), []string{project}); err != nil {
		t.Fatal(err)
	}
	list, _ = s.mobileCLI.summaries(s.profile, map[string]bool{project: true}, false)
	if len(list) != 0 {
		t.Fatal("guest history resurrected deleted entry", list)
	}
	if _, err := os.Stat(history); err != nil {
		t.Fatal("CLI history was removed", err)
	}
}

func TestDiscoveryRunningStateUpdatesCatalogAndClearsMissingHistory(t *testing.T) {
	s, id, project := metadataFixture(t)
	home, _ := filepath.EvalSymlinks(t.TempDir())
	mobileTestSSH(t, s, home, []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/local/bin"})
	record, err := s.mobileCLI.record(s.profile, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.mobileCLI.summaries(s.profile, map[string]bool{project: true}, false); err != nil {
		t.Fatal(err)
	}
	// Keep the host bootstrap and SSH path; substitute the guest's observation.
	observation := filepath.Join(home, "observation.json")
	helper := filepath.Join(home, ".local/share/cage/cli-channel-code", fmt.Sprintf("%x.py", sha256.Sum256([]byte(cliChannelHelper+cliResumePrompt+claudeAccountResumePrompt))))
	if err := os.MkdirAll(filepath.Dir(helper), 0700); err != nil {
		t.Fatal(err)
	}
	code := fmt.Sprintf("import json,sys\njson.load(sys.stdin)\nprint(open(%q).read())\n", observation)
	if err := os.WriteFile(helper, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	s.ready = true
	for _, phase := range []struct{ present, running bool }{{true, true}, {true, false}, {true, true}, {false, false}} {
		found := []terminalConversation{}
		if phase.present {
			found = append(found, terminalConversation{CLIID: record.CLIID, Workspace: guestPath(project), Config: filepath.Join(home, ".claude"), Running: phase.running})
		}
		if err := writePrivate(observation, found); err != nil {
			t.Fatal(err)
		}
		if err := s.discoverTerminalConversations(context.Background(), []string{project}); err != nil {
			t.Fatal(err)
		}
		list, err := s.mobileCLI.summaries(s.profile, map[string]bool{project: true}, false)
		if err != nil || len(list) != 1 || list[0].Running != phase.running {
			t.Fatalf("phase %+v: summaries = %+v, error = %v", phase, list, err)
		}
	}
}

func sharedCLIWorkbench(t *testing.T) (*Server, string, *atomic.Bool) {
	t.Helper()
	binary, flags := os.Getenv("CAGE_TEST_CLAUDE_CLI"), os.Getenv("CAGE_TEST_CHANNEL_FEATURES")
	if runtime.GOOS != "darwin" || !filepath.IsAbs(binary) || !filepath.IsAbs(flags) {
		t.Skip("set CAGE_TEST_CLAUDE_CLI and CAGE_TEST_CHANNEL_FEATURES on macOS for the isolated interactive CLI test")
	}
	features, err := os.ReadFile(flags)
	if err != nil {
		t.Fatal(err)
	}
	var booleans map[string]bool
	if err := json.Unmarshal(features, &booleans); err != nil {
		t.Fatal(err)
	}
	s := testServer(t)
	home, _ := filepath.EvalSymlinks(t.TempDir())
	project, _ := filepath.EvalSymlinks(t.TempDir())
	s.ready = true
	s.preferences.Workspace = project
	if err := writePrivate(filepath.Join(s.profile, "environment.json"), map[string]string{"directory": project}); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	actual, _ := filepath.EvalSymlinks(binary)
	personal, _ := os.UserHomeDir()
	quote := func(value string) string { b, _ := json.Marshal(value); return string(b) }
	policy := "(version 1)(allow default)(deny mach-lookup)(deny file-read-data (subpath " + quote(personal) + ") (subpath \"/Library/Keychains\"))(allow file-read* (literal " + quote(actual) + "))(deny network-outbound)(allow network-outbound (remote ip \"localhost:*\"))"
	debug := filepath.Join(home, "cli-debug.log")
	t.Cleanup(func() {
		if t.Failed() {
			if data, err := os.ReadFile(debug); err == nil {
				if len(data) > 16000 {
					data = data[len(data)-16000:]
				}
				t.Log(string(data))
			}
		}
	})
	shim := "#!/bin/sh\nexec /usr/bin/sandbox-exec -p " + shellescape.Quote(policy) + " " + shellescape.Quote(actual) + " --debug-file " + shellescape.Quote(debug) + " --model sonnet --permission-mode manual \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(shim), 0700); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(t.Name(), "CCS") {
		// Exercise the profile dispatch boundary with a deterministic dispatcher;
		// the interactive process and Channels remain the genuine CLI.
		if err := os.WriteFile(filepath.Join(bin, "ccs"), []byte("#!/bin/sh\n[ \"$1\" = cage-fixture ] || exit 1\nshift\nexec claude \"$@\"\n"), 0700); err != nil {
			t.Fatal(err)
		}
		s.preferences.ClaudeTerminalCommand = "ccs cage-fixture"
	}
	if err := os.WriteFile(filepath.Join(home, ".bash_profile"), []byte("export PATH="+shellescape.Quote(bin)+":/usr/bin:/bin\n"), 0600); err != nil {
		t.Fatal(err)
	}
	key := "sk-ant-local-channel-test"
	config := map[string]any{"hasCompletedOnboarding": true, "theme": "dark", "cachedGrowthBookFeatures": booleans,
		"cachedGrowthBookFeaturesAt": time.Now().UnixMilli(), "customApiKeyResponses": map[string]any{"approved": []string{key[len(key)-20:]}, "rejected": []string{}},
		"projects": map[string]any{project: map[string]bool{"hasTrustDialogAccepted": true}}}
	if t.Name() == "TestSharedTerminalUntrustedProject" {
		delete(config, "projects")
	}
	for _, path := range []string{filepath.Join(home, ".claude.json"), filepath.Join(home, ".claude", ".claude.json")} {
		if err := writePrivate(path, config); err != nil {
			t.Fatal(err)
		}
	}
	var resumed atomic.Bool
	model := mobileTestModel(t, &resumed)
	t.Cleanup(model.Close)
	mobileTestSSH(t, s, home, []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin", "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"),
		"ANTHROPIC_API_KEY=" + key, "ANTHROPIC_BASE_URL=" + model.URL, "DISABLE_AUTOUPDATER=1", "TERM=xterm-256color",
		"HTTPS_PROXY=http://127.0.0.1:1", "HTTP_PROXY=http://127.0.0.1:1", "NO_PROXY=127.0.0.1,localhost"})
	// The local SSH stand-in must reap its CLI and hook children before the
	// isolated home is removed. Closing the host PTY only waits for local SSH.
	t.Cleanup(func() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			output, _ := exec.Command("pgrep", "-f", regexp.QuoteMeta(home)).Output()
			if len(strings.TrimSpace(string(output))) == 0 {
				return
			}
			for _, value := range strings.Fields(string(output)) {
				pid, err := strconv.Atoi(value)
				if err == nil && pid > 0 {
					child, _ := os.FindProcess(pid)
					_ = child.Kill()
					_ = child.Release()
				}
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Error("isolated CLI children did not exit")
	})
	t.Cleanup(s.terminals.closeAll)
	return s, project, &resumed
}

func TestSharedTerminalConversation(t *testing.T) {
	testSharedTerminalConversation(t)
}

func TestSharedCCSTerminalConversation(t *testing.T) {
	testSharedTerminalConversation(t)
}

func testSharedTerminalConversation(t *testing.T) {
	s, project, resumed := sharedCLIWorkbench(t)
	originalLauncher := claudeTerminalCommand(s.preferences)
	if os.Getenv("CAGE_TEST_CHANNEL_BROWSER") != "" {
		runSharedConversationBrowser(t, s, project)
		return
	}
	api := func(method, path string, body any) mobileSession {
		t.Helper()
		data, _ := json.Marshal(body)
		if body == nil {
			data = nil
		}
		response := request(s, method, "/api/mobile/cli/"+path, s.origin, string(data), true)
		if response.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, response.Code, response.Body.String())
		}
		var result mobileSession
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	id := uuid.NewString()
	// A phone built before shared-session negotiation omits the flag.
	api("POST", "sessions", map[string]any{"id": id, "workspace": project})
	// A view polled before the guest starts can mark the cached running flag
	// false. The already published host PTY must remain its sole owner.
	s.mobileCLI.mu.Lock()
	starting, err := s.mobileCLI.record(s.profile, id)
	if err == nil {
		starting.TerminalRunning = false
		err = writePrivate(mobileRecordPath(s.profile, id), starting)
	}
	s.mobileCLI.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if view := api("POST", "sessions", map[string]any{"id": id, "workspace": project, "shared": false}); !view.TerminalAvailable {
		t.Fatal("a client flag created a phone-only session")
	}
	terminals := s.terminals.snapshot()
	if len(terminals) != 1 || terminals[0]["cli_session_id"] != id {
		t.Fatalf("mobile creation did not expose its desktop terminal: %#v", terminals)
	}
	terminal := s.terminals.get(terminals[0]["id"].(string))
	deadline := time.Now().Add(30 * time.Second)
	var current mobileSession
	wait := func(predicate func(mobileSession) bool) {
		t.Helper()
		for time.Now().Before(deadline) {
			current = api("GET", "sessions/"+id, nil)
			if predicate(current) {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		terminal.mu.Lock()
		defer terminal.mu.Unlock()
		t.Fatalf("shared conversation did not reach state: %#v\n%s", current, terminal.history)
	}
	wait(func(view mobileSession) bool { return view.CanSend != nil && *view.CanSend })
	turn := uuid.NewString()
	message := map[string]any{"request_id": turn, "text": "initial mobile prompt"}
	api("POST", "sessions/"+id+"/messages", message)
	api("POST", "sessions/"+id+"/messages", message)
	wait(func(view mobileSession) bool { return len(view.Turns) == 1 && view.Turns[0].Approval != nil })
	if _, err := os.Stat(filepath.Join(project, "mobile-approved.txt")); !os.IsNotExist(err) {
		t.Fatal("tool ran before permission")
	}
	approval := map[string]any{"request_id": turn, "approval_id": current.Turns[0].Approval.ID, "allow": true}
	api("POST", "sessions/"+id+"/approval", approval)
	wait(func(view mobileSession) bool {
		return len(view.Turns) == 1 && view.Turns[0].Text == "isolated tool completed" && view.Turns[0].Status == "completed"
	})
	if err := terminal.enqueueInput(context.Background(), "follow-up\r"); err != nil {
		t.Fatal(err)
	}
	wait(func(view mobileSession) bool {
		return len(view.Turns) == 2 && view.Turns[1].Prompt == "follow-up" && view.Turns[1].Text == "isolated continuation"
	})
	if !resumed.Load() || current.Turns[0].ID != turn {
		t.Fatal("desktop and mobile diverged or duplicated a request")
	}
	listing := request(s, "GET", "/api/mobile/cli/sessions", s.origin, "", true)
	var catalog struct {
		Sessions []mobileSummary `json:"sessions"`
	}
	if err := json.Unmarshal(listing.Body.Bytes(), &catalog); err != nil || len(catalog.Sessions) != 1 || catalog.Sessions[0].ID != id {
		t.Fatalf("catalog duplicated the terminal: %s", listing.Body.String())
	}
	mutate := func(path string, body string) {
		t.Helper()
		response := request(s, "POST", path, s.origin, body, true)
		if response.Code != 200 {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
	}
	s.mobileCLI.mu.Lock()
	record, err := s.mobileCLI.record(s.profile, id)
	s.mobileCLI.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	cliID, config := record.CLIID, record.GuestConfig
	mutate("/api/cli/sessions/"+id+"/archive", `{}`)
	if len(s.terminals.snapshot()) != 0 {
		t.Fatal("archive kept its terminal open")
	}
	mutate("/api/mobile/cli/sessions/"+id+"/restore", `{}`)
	s.mu.Lock()
	s.preferences.ClaudeTerminalCommand = "ccs changed"
	s.mu.Unlock()
	if response := request(s, "POST", "/api/mobile/cli/sessions/"+id+"/open", s.origin, `{"automatic":true}`, true); response.Code != 422 || len(s.terminals.snapshot()) != 0 {
		t.Fatal("changed launcher created a terminal during automatic resume", response.Code)
	}
	s.mu.Lock()
	s.preferences.ClaudeTerminalCommand = originalLauncher
	s.mu.Unlock()
	mutate("/api/cli/sessions/"+id+"/open", `{}`)
	terminals = s.terminals.snapshot()
	if len(terminals) != 1 {
		t.Fatal("restore did not open a single terminal")
	}
	terminal = s.terminals.get(terminals[0]["id"].(string))
	deadline = time.Now().Add(30 * time.Second)
	wait(func(view mobileSession) bool { return view.CanSend != nil && *view.CanSend && len(view.Turns) == 2 })
	// An acknowledgement lost before archival must not replay a turn after
	// the same CLI history resumes through its new channel connection.
	api("POST", "sessions/"+id+"/messages", message)
	if current = api("GET", "sessions/"+id, nil); len(current.Turns) != 2 {
		t.Fatal("resume lost message idempotency")
	}
	mutate("/api/mobile/cli/sessions/"+id+"/archive", `{}`)
	purged := false
	for attempt := 0; attempt < 20; attempt++ {
		response := request(s, "POST", "/api/cli/sessions/"+id+"/purge", s.origin, `{"confirm":true}`, true)
		if response.Code == 200 {
			purged = true
			break
		}
		if attempt == 19 {
			t.Fatal(response.Code, response.Body.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !purged {
		t.Fatal("purge did not complete")
	}
	mutate("/api/cli/sessions/"+id+"/purge", `{"confirm":true}`)
	var removed terminalConversation
	if err := s.runCLIChannel(context.Background(), "view", map[string]string{"cli_id": cliID, "config": config}, &removed); err != nil || removed.HasHistory || len(removed.Turns) != 0 {
		t.Fatal("purge retained the CLI transcript", err)
	}
	probe := s.sshCommand(context.Background(), "-T", s.sshTarget(), "--", "claude --resume "+cliID+" --print")
	output, err := probe.CombinedOutput()
	if err == nil || !strings.Contains(strings.ToLower(string(output)), "no conversation found") {
		t.Fatalf("Claude still resumed the deleted transcript: %v %s", err, output)
	}
	if response := request(s, "POST", "/api/cli/sessions/"+id+"/restore", s.origin, `{}`, true); response.Code != 404 {
		t.Fatal("purged history was restored", response.Code)
	}
	s.account.lock()
	if response := request(s, "GET", "/api/mobile/cli/sessions/"+id, s.origin, "", true); response.Code != 423 {
		t.Fatal("lock did not revoke mobile access", response.Code)
	}
}

func TestLegacyMobileSessionOpensOnDesktop(t *testing.T) {
	s, project, _ := sharedCLIWorkbench(t)
	id, requestID := uuid.NewString(), uuid.NewString()
	if _, err := s.mobileCLI.create(s.profile, project, id); err != nil {
		t.Fatal(err)
	}
	path := "/api/mobile/cli/sessions/" + id
	body, _ := json.Marshal(map[string]string{"request_id": requestID, "text": "follow-up"})
	if w := request(s, "POST", path+"/messages", s.origin, string(body), true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	deadline := time.Now().Add(30 * time.Second)
	read := func() mobileSession {
		t.Helper()
		w := request(s, "GET", path, s.origin, "", true)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var view mobileSession
		if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		return view
	}
	for {
		view := read()
		if len(view.Turns) == 1 && view.Turns[0].Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("legacy turn did not finish: %#v", view)
		}
		time.Sleep(100 * time.Millisecond)
	}
	w := request(s, "POST", "/api/cli/sessions/"+id+"/open", s.origin, "{}", true)
	if w.Code != 200 {
		t.Fatal("desktop open", w.Code, w.Body.String())
	}
	var opened mobileSession
	if err := json.Unmarshal(w.Body.Bytes(), &opened); err != nil {
		t.Fatal(err)
	}
	terminal := s.terminals.get(opened.DesktopTerminalID)
	if terminal == nil || terminal.sharedID != id {
		t.Fatal("desktop did not attach the original session")
	}
	for {
		view := read()
		if view.CanSend != nil && *view.CanSend {
			if len(view.Turns) != 1 || view.Turns[0].ID != requestID {
				t.Fatalf("history changed: %#v", view)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("adopted terminal not ready: %#v", view)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Retrying a pre-upgrade request acknowledges it without sending it again.
	if w := request(s, "POST", path+"/messages", s.origin, string(body), true); w.Code != 200 || !strings.Contains(w.Body.String(), requestID) {
		t.Fatal(w.Code, w.Body.String())
	}
	if view := read(); len(view.Turns) != 1 {
		t.Fatal("old request was replayed", view)
	}
	newBody, _ := json.Marshal(map[string]string{"request_id": uuid.NewString(), "text": "follow-up"})
	if w := request(s, "POST", path+"/messages", s.origin, string(newBody), true); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for {
		view := read()
		if len(view.Turns) == 2 && view.Turns[1].Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("shared continuation failed: %#v", view)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestFailedLegacyMobileSessionOpensOnDesktop(t *testing.T) {
	s, project, _ := sharedCLIWorkbench(t)
	id := uuid.NewString()
	if _, err := s.mobileCLI.create(s.profile, project, id); err != nil {
		t.Fatal(err)
	}
	record, err := s.mobileCLI.record(s.profile, id)
	if err != nil {
		t.Fatal(err)
	}
	record.Launcher = "claude"
	record.Turns = []mobileTurn{{ID: uuid.NewString(), Prompt: "original failed message", Status: "failed", Error: "preflight failed"}}
	if err := writePrivate(mobileRecordPath(s.profile, id), record); err != nil {
		t.Fatal(err)
	}
	w := request(s, "POST", "/api/cli/sessions/"+id+"/open", s.origin, "{}", true)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	deadline := time.Now().Add(25 * time.Second)
	for {
		w = request(s, "GET", "/api/mobile/cli/sessions/"+id, s.origin, "", true)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var view mobileSession
		if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		if view.CanSend != nil && *view.CanSend {
			if len(view.Turns) != 1 || view.Turns[0].Error != "preflight failed" {
				t.Fatalf("cached failure lost: %#v", view)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("failed legacy session never became usable: %#v", view)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestImportAndResumeDesktopConversation(t *testing.T) {
	s, project, resumed := sharedCLIWorkbench(t)
	cmd := s.sshCommand(s.lifetimeContext(), "-tt", s.sshTarget(), "--", terminalScript(guestPath(project), "claude", "claude"))
	original, err := s.terminals.startSession(cmd, project, "claude", claudeSelection{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	strip := regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\s+`)
	waitScreen := func(terminal *terminalSession, phrase string) {
		t.Helper()
		var text string
		for time.Now().Before(deadline) {
			terminal.mu.Lock()
			text = strip.ReplaceAllString(string(terminal.history), "")
			terminal.mu.Unlock()
			if strings.Contains(text, phrase) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		if len(text) > 6000 {
			text = text[len(text)-6000:]
		}
		t.Fatalf("terminal did not show %q: %s", phrase, text)
	}
	waitScreen(original, "manualmodeon")
	_ = original.enqueueInput(context.Background(), "initial mobile prompt\r")
	waitScreen(original, "Doyouwanttoproceed?")
	waitScreen(original, "Tabtoamend")
	// The interactive permission dialog ignores keys during its opening guard.
	time.Sleep(700 * time.Millisecond)
	_ = original.enqueueInput(context.Background(), "1\r")
	waitScreen(original, "isolatedtoolcompleted")
	var catalog struct {
		Sessions []mobileSummary `json:"sessions"`
	}
	for time.Now().Before(deadline) {
		response := request(s, "GET", "/api/mobile/cli/sessions", s.origin, "", true)
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), &catalog); err != nil {
			t.Fatal(err)
		}
		if len(catalog.Sessions) == 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(catalog.Sessions) != 1 {
		t.Fatal("desktop history was not discovered")
	}
	id := catalog.Sessions[0].ID
	path := "/api/mobile/cli/sessions/" + id
	before := request(s, "GET", path, s.origin, "", true)
	var view mobileSession
	if before.Code != 200 || json.Unmarshal(before.Body.Bytes(), &view) != nil || len(view.Turns) != 1 || view.CanSend == nil || *view.CanSend {
		t.Fatalf("unexpected imported history: %s", before.Body.String())
	}
	if response := request(s, "POST", path+"/open", s.origin, "{}", true); response.Code != 409 {
		t.Fatal("opened a second live process for the same CLI history", response.Code)
	}
	var opened bool
	for time.Now().Before(deadline) {
		response := request(s, "POST", path+"/open", s.origin, `{"automatic":true}`, true)
		if response.Code == 200 {
			opened = true
			break
		}
		if response.Code != 409 {
			t.Fatal(response.Code, response.Body.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !opened {
		original.mu.Lock()
		output := strip.ReplaceAllString(string(original.history), "")
		original.mu.Unlock()
		t.Fatalf("original CLI did not exit: %s", output)
	}
	s.mobileCLI.mu.Lock()
	record, err := s.mobileCLI.record(s.profile, id)
	s.mobileCLI.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	terminal := s.terminals.get(record.TerminalID)
	if terminal == nil || terminal == original {
		t.Fatal("history did not open in a shared terminal")
	}
	for time.Now().Before(deadline) {
		response := request(s, "GET", path, s.origin, "", true)
		if response.Code == 200 && json.Unmarshal(response.Body.Bytes(), &view) == nil && view.CanSend != nil && *view.CanSend {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	message, _ := json.Marshal(map[string]string{"request_id": uuid.NewString(), "text": "follow-up"})
	if response := request(s, "POST", path+"/messages", s.origin, string(message), true); response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	for time.Now().Before(deadline) {
		response := request(s, "GET", path, s.origin, "", true)
		if response.Code == 200 && json.Unmarshal(response.Body.Bytes(), &view) == nil && len(view.Turns) == 2 && view.Turns[1].Text == "isolated continuation" {
			if !resumed.Load() {
				t.Fatal("resumed CLI lost the desktop context")
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("resumed history did not finish: %#v", view)
}

func TestSharedTerminalUntrustedProject(t *testing.T) {
	s, project, _ := sharedCLIWorkbench(t)
	// Start without a CLI trust entry; Cage's project grant is sufficient for
	// its own startup, without granting any tools or external MCP servers.
	id := uuid.NewString()
	body, _ := json.Marshal(map[string]any{"id": id, "workspace": project, "shared": true})
	response := request(s, "POST", "/api/mobile/cli/sessions", s.origin, string(body), true)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	deadline := time.Now().Add(35 * time.Second)
	for time.Now().Before(deadline) {
		response = request(s, "GET", "/api/mobile/cli/sessions/"+id, s.origin, "", true)
		var view mobileSession
		if response.Code == 200 && json.Unmarshal(response.Body.Bytes(), &view) == nil && view.CanSend != nil && *view.CanSend {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, summary := range s.terminals.snapshot() {
		terminal := s.terminals.get(summary["id"].(string))
		terminal.mu.Lock()
		t.Log(cliStartupText(terminal.history))
		terminal.mu.Unlock()
	}
	t.Fatal("untrusted project startup required desktop input")
}

func TestSharedTerminalStartupBrowser(t *testing.T) {
	if os.Getenv("CAGE_TEST_CHANNEL_BROWSER") == "" {
		t.Skip("set browser and isolated CLI fixture inputs")
	}
	s, project, _ := sharedCLIWorkbench(t)
	server := httptest.NewServer(s.handler())
	defer server.Close()
	s.origin = server.URL
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	input, _ := json.Marshal(map[string]string{"origin": s.origin, "cookie": s.cookieName(), "session": s.session, "project": project})
	driver := exec.CommandContext(ctx, "node", "../../ui/scripts/terminal-startup-live-smoke.mjs")
	driver.Stdin = strings.NewReader(string(input))
	if output, err := driver.CombinedOutput(); err != nil {
		t.Fatalf("startup browser: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}

func TestWaitingDesktopApprovalMovesToPhone(t *testing.T) {
	s, project, _ := sharedCLIWorkbench(t)
	cmd := s.sshCommand(s.lifetimeContext(), "-tt", s.sshTarget(), "--", terminalScript(guestPath(project), "claude", "claude"))
	original, err := s.terminals.startSession(cmd, project, "claude", claudeSelection{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(45 * time.Second)
	waitScreen := func(phrase string) {
		t.Helper()
		for time.Now().Before(deadline) {
			original.mu.Lock()
			text := cliStartupText(original.history)
			original.mu.Unlock()
			if strings.Contains(text, phrase) {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("legacy CLI did not show ", phrase)
	}
	waitScreen("manualmodeon")
	if err := original.enqueueInput(context.Background(), "initial mobile prompt\r"); err != nil {
		t.Fatal(err)
	}
	waitScreen("Tabtoamend")
	// Give the CLI's supported agent registry time to publish the dialog state.
	for time.Now().Before(deadline) {
		probe := s.sshCommand(context.Background(), "-T", s.sshTarget(), "--", "claude agents --json")
		data, err := probe.Output()
		if err != nil {
			t.Fatal(err)
		}
		var rows []struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(data, &rows) == nil && len(rows) == 1 && rows[0].Status == "waiting" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	listing := request(s, "GET", "/api/mobile/cli/sessions", s.origin, "", true)
	var catalog struct {
		Sessions []mobileSummary `json:"sessions"`
	}
	if listing.Code != 200 || json.Unmarshal(listing.Body.Bytes(), &catalog) != nil || len(catalog.Sessions) != 1 {
		t.Fatal("missing legacy history", listing.Body.String())
	}
	path := "/api/mobile/cli/sessions/" + catalog.Sessions[0].ID
	opened := request(s, "POST", path+"/open", s.origin, `{"automatic":true}`, true)
	if opened.Code != 200 {
		t.Fatal(opened.Code, opened.Body.String())
	}
	var view mobileSession
	for time.Now().Before(deadline) {
		response := request(s, "GET", path, s.origin, "", true)
		if response.Code == 200 && json.Unmarshal(response.Body.Bytes(), &view) == nil && len(view.Turns) == 1 && view.Turns[0].Approval != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(view.Turns) != 1 || view.Turns[0].Approval == nil {
		t.Fatalf("phone did not receive fresh approval: %#v", view)
	}
	if view.Turns[0].Prompt != "initial mobile prompt" {
		t.Fatal("internal reconnect notice leaked into the conversation")
	}
	if strings.Contains(view.Turns[0].Text, "No response requested.") {
		t.Fatal("synthetic restart message leaked into the conversation")
	}
	if _, err := os.Stat(filepath.Join(project, "mobile-approved.txt")); !os.IsNotExist(err) {
		t.Fatal("handoff approved the tool")
	}
	body, _ := json.Marshal(map[string]any{"request_id": view.Turns[0].ID, "approval_id": view.Turns[0].Approval.ID, "allow": true})
	response := request(s, "POST", path+"/approval", s.origin, string(body), true)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	for time.Now().Before(deadline) {
		response = request(s, "GET", path, s.origin, "", true)
		if response.Code == 200 && json.Unmarshal(response.Body.Bytes(), &view) == nil && len(view.Turns) == 1 && view.Turns[0].Status == "completed" {
			if data, err := os.ReadFile(filepath.Join(project, "mobile-approved.txt")); err != nil || string(data) != "approved" {
				t.Fatal("approved task did not complete", err)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("resumed task did not complete")
}

func runSharedConversationBrowser(t *testing.T, s *Server, project string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	server := httptest.NewServer(s.handler())
	defer server.Close()
	s.origin = server.URL
	workbench, err := mobilelink.OpenWorkbench(ctx, s.origin, s.issueTicket())
	if err != nil {
		t.Fatal(err)
	}
	defer workbench.Close()
	admin := strings.Repeat("c", 48)
	service, err := relay.New(relay.Config{AdminToken: admin})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	relayServer := httptest.NewServer(service)
	defer relayServer.Close()
	hostIdentity, _ := remotelink.NewIdentity()
	phoneIdentity, _ := remotelink.NewIdentity()
	room, err := mobilelink.CreateRoom(ctx, relayServer.URL, admin)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = mobilelink.ServeHost(ctx, relayServer.URL, room.ID, room.HostToken, hostIdentity, phoneIdentity.Pin, workbench)
	}()
	client, err := mobilelink.ConnectClient(ctx, relayServer.URL, room.ID, room.ClientToken, phoneIdentity, hostIdentity.Pin)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	web := httptest.NewUnstartedServer(nil)
	assets, _ := filepath.Abs("web")
	handler, entry, err := mobilelink.NewBrowser("http://"+web.Listener.Addr().String(), assets, client)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	web.Config.Handler = handler
	web.Start()
	defer web.Close()
	// The browser must complete startup itself. This goroutine only verifies
	// that a later desktop keyboard message still reaches the phone.
	desktopCtx, stopDesktop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		replied := false
		strip := regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\s+`)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-desktopCtx.Done():
				return
			case <-ticker.C:
				for _, row := range s.terminals.snapshot() {
					terminal := s.terminals.get(row["id"].(string))
					terminal.mu.Lock()
					output := strip.ReplaceAllString(string(terminal.history), "")
					terminal.mu.Unlock()
					if !replied && strings.Contains(output, "isolatedtoolcompleted") {
						_ = terminal.enqueueInput(desktopCtx, "follow-up\r")
						replied = true
					}
				}
			}
		}
	}()
	defer func() { stopDesktop(); <-done }()
	driver := exec.CommandContext(ctx, "node", "../../ui/scripts/shared-cli-live-smoke.mjs")
	driver.Stdin = strings.NewReader(entry)
	driver.Env = append(os.Environ(), "CAGE_TEST_MOBILE_MARKER="+filepath.Join(project, "mobile-approved.txt"))
	if output, err := driver.CombinedOutput(); err != nil {
		for _, row := range s.terminals.snapshot() {
			terminal := s.terminals.get(row["id"].(string))
			terminal.mu.Lock()
			t.Log(cliStartupText(terminal.history))
			terminal.mu.Unlock()
		}
		t.Fatalf("shared browser: %v\n%s", err, output)
	}
}
