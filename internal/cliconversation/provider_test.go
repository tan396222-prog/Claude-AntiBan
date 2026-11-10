package cliconversation

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testSessionID = "de3ac0b3-1bad-43da-88ab-d86b7af32210"

func TestResultRequiresMatchingCompletedSession(t *testing.T) {
	init := `{"type":"system","subtype":"init","session_id":"` + testSessionID + `"}` + "\n"
	final := `{"type":"result","subtype":"success","session_id":"` + testSessionID + `","result":"done"}`
	for _, events := range []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"partial"}]}}`,
		strings.Replace(final, testSessionID, "1d2f57e6-64f9-44df-b0b4-43381e33a317", 1),
		init + final + "\n" + final,
		`{"type":"result","session_id":"` + testSessionID + `","is_error":true,"result":"private upstream diagnostic"}`,
	} {
		_, err := readResult(strings.NewReader(events), testSessionID, nil, nil, nil)
		if err == nil || strings.Contains(err.Error(), "private") {
			t.Fatalf("expected a sanitized failure, got %v", err)
		}
	}
}

func TestIdentityFailureExplainsFirstLaunch(t *testing.T) {
	result, err := readResult(strings.NewReader(`{"type":"system","subtype":"cage_identity_unverified_start"}`), testSessionID, nil, nil, nil)
	if result.Started || err == nil || !strings.Contains(err.Error(), "当前 Claude 账号") || strings.Contains(err.Error(), "新建会话") {
		t.Fatalf("unexpected first-launch failure: %#v %v", result, err)
	}
}

func TestSupervisedInputKeepsConnectionOpenAfterPrompt(t *testing.T) {
	const prompt = "a quoted \"argument\"\non another line"
	if os.Getenv("CAGE_CLI_SUPERVISOR_FIXTURE") == "1" {
		input := bufio.NewReader(os.Stdin)
		line, err := input.ReadBytes('\n')
		var received string
		if err != nil || json.Unmarshal(line, &received) != nil || received != prompt {
			os.Exit(2)
		}
		closed := make(chan struct{})
		go func() { _, _ = input.ReadByte(); close(closed) }()
		select {
		case <-closed:
			os.Exit(3)
		case <-time.After(70 * time.Millisecond):
		}
		fmt.Printf("{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":%q}\n", testSessionID)
		fmt.Printf("{\"type\":\"result\",\"subtype\":\"success\",\"session_id\":%q,\"result\":\"ok\"}\n", testSessionID)
		os.Exit(0)
	}
	runner := Runner{SupervisedStdin: true, Command: func(ctx context.Context, _ string, _ []string) (*exec.Cmd, error) {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSupervisedInputKeepsConnectionOpenAfterPrompt$")
		cmd.Env = append(os.Environ(), "CAGE_CLI_SUPERVISOR_FIXTURE=1")
		return cmd, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	result, err := runner.Run(ctx, Turn{Workspace: t.TempDir(), SessionID: testSessionID, Prompt: prompt}, nil)
	if err != nil || !result.Started || result.Text != "ok" {
		t.Fatalf("supervised turn: %#v, %v", result, err)
	}
}

func TestCancelStopsRunningProcess(t *testing.T) {
	if os.Getenv("CAGE_CLI_CANCEL_FIXTURE") == "1" {
		fmt.Printf("{\"type\":\"stream_event\",\"session_id\":%q,\"event\":{\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"started\"}}}\n", testSessionID)
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	runner := Runner{Command: func(ctx context.Context, _ string, _ []string) (*exec.Cmd, error) {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCancelStopsRunningProcess$")
		cmd.Env = append(os.Environ(), "CAGE_CLI_CANCEL_FIXTURE=1")
		return cmd, nil
	}}
	_, err := runner.Run(ctx, Turn{Workspace: t.TempDir(), SessionID: testSessionID, Prompt: "synthetic"}, func(string) { cancel() })
	if err != context.Canceled {
		t.Fatalf("cancelling a streaming turn returned %v", err)
	}
}

// CAGE_TEST_CLAUDE_CLI points to an installed official executable. This test
// drives that actual process and its real session files; only its model is a
// fixture. Bare mode and an isolated environment keep personal credentials out.
func TestRealClaudeCodeWithLocalUpstream(t *testing.T) {
	binary := os.Getenv("CAGE_TEST_CLAUDE_CLI")
	if binary == "" {
		t.Skip("set CAGE_TEST_CLAUDE_CLI to an absolute official Claude Code executable")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("use an absolute executable path")
	}
	home := t.TempDir()
	project := t.TempDir()
	forbidden := filepath.Join(project, "would-write.txt")
	var resumed, denied atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/v1/messages/count_tokens" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"input_tokens":64}`)
			return
		}
		if req.URL.Path != "/v1/messages" {
			http.NotFound(w, req)
			return
		}
		var payload struct {
			Model    string          `json:"model"`
			Messages json.RawMessage `json:"messages"`
			Stream   bool            `json:"stream"`
		}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil || !payload.Stream {
			http.Error(w, "expected a streaming Messages API request", http.StatusBadRequest)
			return
		}
		history := string(payload.Messages)
		w.Header().Set("Content-Type", "text/event-stream")
		event := func(kind string, data any) {
			body, err := json.Marshal(data)
			if err != nil {
				t.Errorf("fixture event: %v", err)
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, body)
			w.(http.Flusher).Flush()
		}
		event("message_start", map[string]any{"type": "message_start", "message": map[string]any{
			"id": "msg_synthetic_cage", "type": "message", "role": "assistant", "model": payload.Model,
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]int{"input_tokens": 64, "output_tokens": 1},
		}})
		stop := "end_turn"
		if strings.Contains(history, "write-fixture") && !strings.Contains(history, `"tool_result"`) {
			stop = "tool_use"
			event("content_block_start", map[string]any{"type": "content_block_start", "index": 0,
				"content_block": map[string]any{"type": "tool_use", "id": "toolu_cage", "name": "Bash", "input": map[string]any{}}})
			input, _ := json.Marshal(map[string]string{"command": "printf synthetic > would-write.txt", "description": "Create a synthetic test file"})
			event("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
				"delta": map[string]string{"type": "input_json_delta", "partial_json": string(input)}})
		} else {
			reply := "first simulated reply"
			if strings.Contains(history, "resume-fixture") {
				resumed.Store(strings.Contains(history, "first-fixture") && strings.Contains(history, "first simulated reply"))
				reply = "second simulated reply"
			}
			if strings.Contains(history, "write-fixture") {
				denied.Store(strings.Contains(history, `"is_error":true`))
				if denied.Load() {
					reply = "simulated denial received"
				} else {
					reply = "simulated write completed"
				}
			}
			event("content_block_start", map[string]any{"type": "content_block_start", "index": 0,
				"content_block": map[string]string{"type": "text", "text": ""}})
			event("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
				"delta": map[string]string{"type": "text_delta", "text": reply}})
		}
		event("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
		event("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil},
			"usage": map[string]int{"output_tokens": 5}})
		event("message_stop", map[string]string{"type": "message_stop"})
	}))
	defer upstream.Close()
	supervisor := os.Getenv("CAGE_TEST_CLI_SUPERVISOR")
	runner := Runner{SupervisedStdin: supervisor != "", Command: func(ctx context.Context, workspace string, args []string) (*exec.Cmd, error) {
		command := append([]string{binary, "--bare"}, args...)
		if supervisor != "" {
			python, err := exec.LookPath("python3")
			if err != nil {
				return nil, err
			}
			command = append([]string{python, "-I", supervisor, workspace}, command...)
		}
		cmd := exec.CommandContext(ctx, command[0], command[1:]...)
		if supervisor != "" {
			// Simulate SSH disconnect locally: EOF reaches the real supervisor.
			// WaitDelay remains a final bound if the supervisor cannot exit.
			cmd.Cancel = func() error { return nil }
		}
		cmd.Dir = workspace
		cmd.Env = []string{
			"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, "config"),
			"PATH=" + filepath.Dir(binary) + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin",
			"ANTHROPIC_BASE_URL=" + upstream.URL, "ANTHROPIC_API_KEY=sk-ant-fixture-only",
			"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1",
			"HTTPS_PROXY=http://127.0.0.1:1", "HTTP_PROXY=http://127.0.0.1:1", "NO_PROXY=127.0.0.1,localhost",
		}
		if systemRoot := os.Getenv("SystemRoot"); systemRoot != "" {
			cmd.Env = append(cmd.Env, "SystemRoot="+systemRoot)
		}
		return cmd, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	run := func(id, prompt, want string, resume bool) Result {
		t.Helper()
		var live strings.Builder
		result, err := runner.Run(ctx, Turn{project, id, prompt, resume}, func(delta string) { live.WriteString(delta) })
		if err != nil || !result.Started || result.Text != want || live.String() != want {
			t.Fatalf("turn %s: result=%q live=%q err=%v", prompt, result.Text, live.String(), err)
		}
		return result
	}
	run(testSessionID, "first-fixture", "first simulated reply", false)
	run(testSessionID, "resume-fixture", "second simulated reply", true)
	if !resumed.Load() {
		t.Fatal("resumed CLI did not send the first exchange to the local provider")
	}
	result := run("93b18ece-8eab-4c7e-b970-c3652252b7c4", "write-fixture", "simulated denial received", false)
	if !denied.Load() || !slices.Contains(result.DeniedTools, "Bash") {
		t.Fatalf("missing permission denial: upstream observed denial=%t, result=%#v", denied.Load(), result)
	}
	if _, err := os.Stat(forbidden); !os.IsNotExist(err) {
		t.Fatalf("denied tool changed the workspace: %v", err)
	}
	var approvals atomic.Int32
	answer := func(allow bool) func(context.Context, PermissionRequest) bool {
		return func(_ context.Context, request PermissionRequest) bool {
			approvals.Add(1)
			var proposed struct{ Command string }
			if request.Tool != "Bash" || json.Unmarshal(request.Input, &proposed) != nil || proposed.Command != "printf synthetic > would-write.txt" {
				t.Errorf("unexpected approval request: %s, %s", request.Tool, request.Input)
				return false
			}
			if _, err := os.Stat(forbidden); !os.IsNotExist(err) {
				t.Errorf("command ran before the decision: %v", err)
				return false
			}
			// The approval UI cannot substitute different arguments, even if it
			// accidentally mutates its raw JSON while preparing a preview.
			clear(request.Input)
			return allow
		}
	}
	runner.Approve = answer(false)
	run("24816c82-cf57-4ec3-b8c0-05dc70614440", "write-fixture", "simulated denial received", false)
	pending, stop := context.WithCancel(ctx)
	defer stop()
	runner.Approve = func(approvalCtx context.Context, _ PermissionRequest) bool {
		stop()
		<-approvalCtx.Done()
		return true
	}
	result, err := runner.Run(pending, Turn{project, "0ca69e7b-2661-4b81-b966-f2589547933c", "write-fixture", false}, nil)
	if err != context.Canceled || !result.Started {
		t.Fatalf("cancel during pending tool approval: %#v, %v", result, err)
	}
	runner.Approve = answer(true)
	run("8c0d77d3-cb3e-43b2-9114-62cb05c5ebc4", "write-fixture", "simulated write completed", false)
	bytes, err := os.ReadFile(forbidden)
	if err != nil || string(bytes) != "synthetic" || approvals.Load() != 2 {
		t.Fatalf("expected one approved write after exactly two decisions: %q, %v, decisions=%d", bytes, err, approvals.Load())
	}
}
