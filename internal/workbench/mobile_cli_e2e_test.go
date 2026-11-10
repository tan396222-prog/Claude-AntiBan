//go:build !windows

package workbench

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/mobilelink"
	"github.com/sylvinus/agent-vm/internal/relay"
	"github.com/sylvinus/agent-vm/internal/remotelink"
	"golang.org/x/crypto/ssh"
)

// A temporary, public-key-authenticated SSH endpoint stands in for the guest.
// It runs the exact command issued by Cage in an empty home. Only the synthetic
// Messages service is reachable through the test CLI's provider configuration.
func mobileTestSSH(t *testing.T, s *Server, home string, env []string) {
	mobileTestSSHRewrite(t, s, home, env, nil)
}

func mobileTestSSHRewrite(t *testing.T, s *Server, home string, env []string, rewrite func(string) string) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostPublic, hostKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatal(err)
	}
	clientKey, _ := ssh.NewPublicKey(public)
	config := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if !bytes.Equal(key.Marshal(), clientKey.Marshal()) {
			return nil, errors.New("unknown test client")
		}
		return nil, nil
	}}
	config.AddHostKey(hostSigner)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	dir := s.instanceDir()
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(dir, "fixture-key")
	block, err := ssh.MarshalPrivateKey(private, "cage-test")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(keyFile, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	pub, _ := ssh.NewPublicKey(hostPublic)
	known := filepath.Join(dir, "fixture-known-hosts")
	if err = os.WriteFile(known, append([]byte(fmt.Sprintf("[127.0.0.1]:%d ", port)), ssh.MarshalAuthorizedKey(pub)...), 0600); err != nil {
		t.Fatal(err)
	}
	settings := fmt.Sprintf("Host *\n HostName 127.0.0.1\n Port %d\n User cage\n StrictHostKeyChecking yes\n UserKnownHostsFile %q\n IdentityFile %q\n", port, known, keyFile)
	if err = os.WriteFile(filepath.Join(dir, "ssh.config"), []byte(settings), 0600); err != nil {
		t.Fatal(err)
	}
	if err = writePrivate(filepath.Join(dir, "cage-running.json"), map[string]int{"pid": os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				server, channels, requests, err := ssh.NewServerConn(conn, config)
				if err != nil {
					conn.Close()
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for target := range channels {
					if target.ChannelType() != "session" {
						_ = target.Reject(ssh.UnknownChannelType, "session required")
						continue
					}
					ch, reqs, err := target.Accept()
					if err != nil {
						return
					}
					func() {
						defer ch.Close()
						withPTY := false
						for req := range reqs {
							if req.Type == "pty-req" {
								withPTY = true
								_ = req.Reply(true, nil)
								continue
							}
							if req.Type != "exec" {
								_ = req.Reply(false, nil)
								continue
							}
							var in struct{ Command string }
							if ssh.Unmarshal(req.Payload, &in) != nil {
								return
							}
							_ = req.Reply(true, nil)
							if rewrite != nil {
								in.Command = rewrite(in.Command)
							}
							cmd := exec.Command("/bin/sh", "-c", in.Command)
							cmd.Dir, cmd.Env = home, env
							if withPTY {
								terminal, err := startTerminal(cmd, 100, 36)
								if err != nil {
									return
								}
								defer terminal.Close()
								go func() { _, _ = io.Copy(ch, terminal) }()
								go func() { _, _ = io.Copy(terminal, ch); _ = terminal.Kill(); _ = terminal.Close() }()
								_ = terminal.Wait()
								_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
								return
							}
							cmd.Stdout, cmd.Stderr = ch, ch.Stderr()
							input, err := cmd.StdinPipe()
							if err != nil {
								return
							}
							go func() { _, _ = io.Copy(input, ch); _ = input.Close() }()
							status := uint32(0)
							if err := cmd.Run(); err != nil {
								status = 1
							}
							_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
							return
						}
					}()
				}
			}()
		}
	}()
}

func mobileTestModel(t *testing.T, resumed *atomic.Bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/messages/count_tokens" {
			_, _ = io.WriteString(w, `{"input_tokens":7}`)
			return
		}
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		var input struct {
			Messages json.RawMessage `json:"messages"`
			Model    string          `json:"model"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			http.Error(w, "bad fixture request", 400)
			return
		}
		history := string(input.Messages)
		needsTool := !strings.Contains(history, `"tool_result"`)
		if strings.Contains(history, "The user has reopened this same conversation") {
			var messages []struct {
				Content json.RawMessage `json:"content"`
			}
			if json.Unmarshal(input.Messages, &messages) == nil {
				needsTool = true
				for _, message := range messages {
					var blocks []struct {
						Type    string `json:"type"`
						IsError bool   `json:"is_error"`
					}
					if json.Unmarshal(message.Content, &blocks) != nil {
						continue
					}
					for _, block := range blocks {
						if block.Type == "tool_result" && !block.IsError {
							needsTool = false
						}
					}
				}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		event := func(kind string, fields map[string]any) {
			fields["type"] = kind
			b, _ := json.Marshal(fields)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, b)
			w.(http.Flusher).Flush()
		}
		event("message_start", map[string]any{"message": map[string]any{"id": "msg_" + uuid.NewString(), "type": "message", "role": "assistant", "model": input.Model, "content": []string{}, "stop_reason": nil, "usage": map[string]int{"input_tokens": 8, "output_tokens": 1}}})
		stop := "end_turn"
		if needsTool && !strings.Contains(history, "follow-up") {
			stop = "tool_use"
			event("content_block_start", map[string]any{"index": 0, "content_block": map[string]any{"type": "tool_use", "id": "toolu_" + uuid.NewString(), "name": "Bash", "input": map[string]any{}}})
			event("content_block_delta", map[string]any{"index": 0, "delta": map[string]string{"type": "input_json_delta", "partial_json": `{"command":"printf approved > mobile-approved.txt","description":"Create the isolated project marker"}`}})
		} else {
			text := "isolated tool completed"
			if strings.Contains(history, "follow-up") {
				resumed.Store(strings.Contains(history, "initial mobile prompt") && strings.Contains(history, "isolated tool completed"))
				text = "isolated continuation"
			}
			event("content_block_start", map[string]any{"index": 0, "content_block": map[string]string{"type": "text", "text": ""}})
			event("content_block_delta", map[string]any{"index": 0, "delta": map[string]string{"type": "text_delta", "text": text}})
		}
		event("content_block_stop", map[string]any{"index": 0})
		event("message_delta", map[string]any{"delta": map[string]string{"stop_reason": stop}, "usage": map[string]int{"output_tokens": 5}})
		event("message_stop", map[string]any{})
	}))
}

// newMobileCLIWorkbench runs the shipped workbench routes and CLI against a
// loopback SSH test endpoint and a synthetic Messages provider in a fresh home.
func newMobileCLIWorkbench(t *testing.T) (*Server, *mobilelink.Workbench, string, string, *atomic.Bool) {
	t.Helper()
	binary := os.Getenv("CAGE_TEST_CLAUDE_CLI")
	if !filepath.IsAbs(binary) {
		t.Skip("set CAGE_TEST_CLAUDE_CLI to an isolated official executable")
	}
	s := testServer(t)
	home := t.TempDir()
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	desktopProject := t.TempDir()
	s.preferences.Workspace = desktopProject
	s.ready = true
	if err = writePrivate(filepath.Join(s.profile, "environment.json"), map[string]string{"directory": project}); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, "bin")
	if err = os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexec "+shellescape.Quote(binary)+" --bare \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, ".bash_profile"), []byte("export PATH="+shellescape.Quote(bin)+":/usr/bin:/bin\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var resumed atomic.Bool
	model := mobileTestModel(t, &resumed)
	t.Cleanup(model.Close)
	mobileTestSSH(t, s, home, []string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin", "CLAUDE_CONFIG_DIR=" + filepath.Join(home, "config"),
		"ANTHROPIC_API_KEY=sk-ant-local-fixture", "ANTHROPIC_BASE_URL=" + model.URL, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1",
		"HTTPS_PROXY=http://127.0.0.1:1", "HTTP_PROXY=http://127.0.0.1:1", "NO_PROXY=127.0.0.1,localhost", "LANG=en_US.UTF-8"})
	server := httptest.NewServer(s.handler())
	t.Cleanup(server.Close)
	s.origin = server.URL
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, err := mobilelink.OpenWorkbench(ctx, s.origin, s.issueTicket())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	t.Cleanup(s.mobileCLI.stopAll)
	return s, b, project, desktopProject, &resumed
}

// Uses a genuine CLI and the production relay, TLS, workbench router, project
// checks, OpenSSH and guest supervisor. No live model or personal profile.
func TestMobileCLIThroughRelayAndSSH(t *testing.T) {
	s, b, project, desktopProject, resumed := newMobileCLIWorkbench(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin := strings.Repeat("q", 48)
	service, err := relay.New(relay.Config{AdminToken: admin})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewServer(service)
	defer r.Close()
	defer service.Close()
	hostID, err := remotelink.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	phoneID, err := remotelink.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	connect := func() *mobilelink.Client {
		room, err := mobilelink.CreateRoom(ctx, r.URL, admin)
		if err != nil {
			t.Fatal(err)
		}
		go func() { _ = mobilelink.ServeHost(ctx, r.URL, room.ID, room.HostToken, hostID, phoneID.Pin, b) }()
		client, err := mobilelink.ConnectClient(ctx, r.URL, room.ID, room.ClientToken, phoneID, hostID.Pin)
		if err != nil {
			t.Fatal(err)
		}
		return client
	}
	client := connect()
	defer func() { client.Close() }()
	if engine := os.Getenv("CAGE_TEST_MOBILE_BROWSER"); engine != "" {
		legacy, err := s.mobileCLI.create(s.profile, project, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		legacyJSON, _ := json.Marshal(legacy)
		web := httptest.NewUnstartedServer(nil)
		origin := "http://" + web.Listener.Addr().String()
		assets, err := filepath.Abs("web")
		if err != nil {
			t.Fatal(err)
		}
		handler, entry, err := mobilelink.NewBrowser(origin, assets, client)
		if err != nil {
			web.Listener.Close()
			t.Fatal(err)
		}
		defer handler.Close()
		web.Config.Handler = handler
		web.Start()
		defer web.Close()
		driver := exec.CommandContext(ctx, "node", "../../ui/scripts/mobile-cli-live-smoke.mjs")
		driver.Stdin = strings.NewReader(entry)
		driver.Env = append(os.Environ(), "CAGE_TEST_MOBILE_MARKER="+filepath.Join(project, "mobile-approved.txt"), "CAGE_TEST_MOBILE_LEGACY_SESSION="+string(legacyJSON))
		if output, err := driver.CombinedOutput(); err != nil {
			t.Fatalf("browser: %v\n%s", err, output)
		}
		if !resumed.Load() || s.workspace() != desktopProject {
			t.Fatal("browser flow lost history or changed desktop project")
		}
		return
	}
	api := func(method, path string, input any, status int) mobileSession {
		t.Helper()
		var body []byte
		if input != nil {
			body, _ = json.Marshal(input)
		}
		rep, err := client.Do(ctx, method, "/api/mobile/cli/"+path, body)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Status != status {
			t.Fatalf("%s %s: %d %s", method, path, rep.Status, rep.Body)
		}
		var view mobileSession
		if status == 200 {
			if err = json.Unmarshal(rep.Body, &view); err != nil {
				t.Fatal(err)
			}
		}
		return view
	}
	id := uuid.NewString()
	path := "sessions/" + id
	api("POST", "sessions", map[string]string{"id": id, "workspace": s.profile}, 403)
	// Seed a historical headless record; new requests always create shared
	// terminals, covered by TestSharedTerminalConversation.
	if _, err := s.mobileCLI.create(s.profile, project, id); err != nil {
		t.Fatal(err)
	}
	turnID := uuid.NewString()
	send := map[string]string{"request_id": turnID, "text": "initial mobile prompt"}
	api("POST", path+"/messages", send, 200)
	wait := func(condition func(mobileTurn) bool) mobileTurn {
		t.Helper()
		for ctx.Err() == nil {
			view := api("GET", path, nil, 200)
			turn := view.Turns[len(view.Turns)-1]
			if condition(turn) {
				return turn
			}
			if turn.Status != "running" {
				t.Fatalf("unexpected turn %+v", turn)
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal(ctx.Err())
		return mobileTurn{}
	}
	pending := wait(func(t mobileTurn) bool { return t.Approval != nil })
	if pending.Approval.Tool != "Bash" || !strings.Contains(string(pending.Approval.Input), "mobile-approved.txt") {
		t.Fatal("incorrect approval details")
	}
	marker := filepath.Join(project, "mobile-approved.txt")
	if _, err = os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("tool executed before approval")
	}
	client.Close()
	client = connect()
	again := api("POST", path+"/messages", send, 200)
	if len(again.Turns) != 1 || again.Turns[0].Approval.ID != pending.Approval.ID {
		t.Fatal("lost pending action on reconnect")
	}
	action := map[string]any{"request_id": turnID, "approval_id": pending.Approval.ID, "allow": true}
	api("POST", path+"/approval", action, 200)
	api("POST", path+"/approval", action, 409)
	wait(func(t mobileTurn) bool { return t.Status == "completed" && t.Text == "isolated tool completed" })
	if data, err := os.ReadFile(marker); err != nil || string(data) != "approved" {
		t.Fatal("approved tool did not perform requested operation")
	}
	api("POST", path+"/messages", map[string]string{"request_id": uuid.NewString(), "text": "follow-up"}, 200)
	wait(func(t mobileTurn) bool { return t.Status == "completed" && t.Text == "isolated continuation" })
	if !resumed.Load() || s.workspace() != desktopProject {
		t.Fatal("history or desktop project was not preserved")
	}
	s.account.lock()
	api("GET", path, nil, 423)
}
