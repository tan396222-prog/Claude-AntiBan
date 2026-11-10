package workbench

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/guestos"
	"golang.org/x/crypto/ssh"
)

func init() {
	if directory := os.Getenv("CAGE_TEST_NATIVE_DESKTOP_DOWNLOAD"); directory != "" {
		if len(os.Args) != 4 || os.Args[1] != "--relay-profile" || os.Args[3] != "desktop" {
			os.Exit(2)
		}
		serveDesktopDownloadFixture(os.Stdin, os.Stdout, directory)
		os.Exit(0)
	}
}

func serveDesktopDownloadFixture(input io.Reader, output io.Writer, directory string) {
	connection := uuid.NewString()
	trace, err := os.OpenFile(filepath.Join(directory, "requests.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer trace.Close()
	reader := bufio.NewScanner(input)
	for reader.Scan() {
		var request struct {
			Action, ID, Transfer string
			IDs                  []string
			Sequence             int
			KeepAlive            bool
		}
		if json.Unmarshal(reader.Bytes(), &request) != nil {
			return
		}
		_ = json.NewEncoder(trace).Encode(map[string]any{"connection": connection, "action": request.Action, "sequence": request.Sequence})
		data, err := os.ReadFile(filepath.Join(directory, "record.json"))
		var record browserDownloadInfo
		if err != nil || json.Unmarshal(data, &record) != nil {
			return
		}
		payload, err := os.ReadFile(filepath.Join(directory, "payload"))
		if err != nil {
			return
		}
		transfer := strings.ReplaceAll(record.ID, "-", "")
		var reply any
		switch request.Action {
		case "files-poll":
			reply = map[string]any{"available": true, "uploads": []any{}, "opens": []any{}, "downloads": []browserDownloadInfo{record}}
			if _, err := os.Stat(filepath.Join(directory, "supports-ack")); err == nil {
				var item map[string]any
				_ = json.Unmarshal(data, &item)
				_, err := os.Stat(filepath.Join(directory, "acknowledged"))
				item["acknowledged"] = err == nil
				reply = map[string]any{"available": true, "downloadAck": true, "downloads": []any{item}}
			}
		case "files-download-ack":
			if len(request.IDs) != 1 || request.IDs[0] != record.ID {
				return
			}
			if _, err := os.Stat(filepath.Join(directory, "fail-ack")); err == nil {
				reply = map[string]string{"error": "desktop_download_manifest_failed"}
			} else {
				if os.WriteFile(filepath.Join(directory, "acknowledged"), nil, 0600) != nil {
					return
				}
				reply = map[string]bool{"ok": true}
			}
		case "files-download-open":
			if request.ID != record.ID {
				return
			}
			reply = map[string]any{"transfer": transfer, "size": len(payload), "name": record.Name}
		case "files-download-read":
			if request.Transfer != transfer {
				return
			}
			start := request.Sequence * (64 << 10)
			if start < 0 || start > len(payload) {
				return
			}
			end := min(start+(64<<10), len(payload))
			reply = map[string]any{"data": base64.StdEncoding.EncodeToString(payload[start:end]), "sequence": request.Sequence + 1, "eof": end == len(payload)}
			if _, err := os.Stat(filepath.Join(directory, "fail-read")); err == nil {
				reply = map[string]string{"error": "desktop_download_changed"}
			}
		case "files-download-close", "files-download-retry":
			reply = map[string]bool{"ok": true}
		case "files-open-claim":
			reply = map[string]any{"id": request.ID, "download_id": record.ID, "action": "open"}
		case "files-open-complete":
			reply = map[string]bool{"ok": true}
		default:
			return
		}
		if json.NewEncoder(output).Encode(reply) != nil || !request.KeepAlive {
			return
		}
	}
}

func TestNativeDesktopDownloadAcknowledgesOnlySavedHostReceipts(t *testing.T) {
	for _, system := range []string{"macos", "windows"} {
		t.Run(system, func(t *testing.T) {
			s, record, _, directory := nativeDesktopDownloadFixture(t, system)
			poll := func() {
				t.Helper()
				response := windowsBrowserRequestForTest(s, http.MethodGet, "/api/input/files/desktop/requests", nil)
				if response.Code != http.StatusOK {
					t.Fatalf("poll failed: %d %s", response.Code, response.Body.String())
				}
			}
			marker := func(name string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(directory, name), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			ackCount := func() int {
				t.Helper()
				trace, err := os.ReadFile(filepath.Join(directory, "requests.jsonl"))
				if err != nil {
					t.Fatal(err)
				}
				return bytes.Count(trace, []byte(`"action":"files-download-ack"`))
			}
			marker("supports-ack")
			poll()
			if ackCount() != 0 {
				t.Fatal("guest-only saved state acknowledged before a durable host copy")
			}
			if _, err := s.downloadInfo(t.Context(), record.ID); err != nil {
				t.Fatal(err)
			}
			marker("fail-ack")
			poll()
			if ackCount() != 1 {
				t.Fatal("saved host receipt was not acknowledged")
			}
			if saved, err := readSavedDownload(s.profile, record.ID); err != nil || saved.Path == "" {
				t.Fatal("failed acknowledgment invalidated the host receipt", err)
			}
			if err := os.Remove(filepath.Join(directory, "fail-ack")); err != nil {
				t.Fatal(err)
			}
			poll()
			if ackCount() != 2 {
				t.Fatal("failed acknowledgment was not retried")
			}
			poll()
			if ackCount() != 2 {
				t.Fatal("acknowledged guest record generated another acknowledgment")
			}
			if err := os.Remove(filepath.Join(directory, "supports-ack")); err != nil {
				t.Fatal(err)
			}
			poll()
			if ackCount() != 2 {
				t.Fatal("legacy helper received an unsupported acknowledgment")
			}
		})
	}
}

func nativeDesktopDownloadFixture(t *testing.T, system string) (*Server, browserDownloadInfo, []byte, string) {
	t.Helper()
	directory := t.TempDir()
	var s *Server
	if system == "macos" {
		guest := newMacWorkspaceGuest(t, func(channel ssh.Channel, command string) (uint32, bool) {
			if !strings.Contains(command, "dev.cage.GuestClaudeWindow/window.sock") {
				return 1, false
			}
			serveDesktopDownloadFixture(channel, channel, directory)
			return 0, true
		})
		s = guest.s
	} else {
		s = testServer(t)
		binding := guestos.WindowsProfile{Version: 1, VMID: uuid.New(), GUIUserSID: "S-1-5-21-1-2-3-1001", GUIUser: "Guest", InstanceDirectory: filepath.Join(s.profile, "unused-vm"), PairingFile: filepath.Join(s.profile, "unused-pair"), RelayExecutable: os.Args[0]}
		if err := guestos.BindWindowsProfile(s.profile, binding); err != nil {
			t.Fatal(err)
		}
		s.windowsGuest = &binding
		t.Setenv("CAGE_TEST_NATIVE_DESKTOP_DOWNLOAD", directory)
	}
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(filepath.Join(s.profile, "environment.json"), map[string]string{"directory": workspace}); err != nil {
		t.Fatal(err)
	}
	// A separate empty browser catalog ensures Desktop lookup reaches its own
	// producer, including before any file-request polling has run.
	browserDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(browserDirectory, "catalog.json"), []byte(`{"downloads":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	client := newWindowsBrowserClient(t.Context(), "desktop-download-browser-fixture")
	client.start = func(ctx context.Context) (*exec.Cmd, error) {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeDownloadRelay$")
		cmd.Env = append(os.Environ(), "CAGE_TEST_NATIVE_DESKTOP_DOWNLOAD=", "CAGE_NATIVE_DOWNLOAD_FIXTURE="+browserDirectory)
		return cmd, nil
	}
	s.windowsBrowserControl, s.ready = client, true
	t.Cleanup(func() { s.beginClose(); client.close(); s.activities.Wait() })
	record := browserDownloadInfo{ID: uuid.NewString(), Name: "生成文件.bin", State: "saved", Path: "private-guest-cache"}
	payload := bytes.Repeat([]byte{0, 255, 17, 42}, 32773)
	if err := writePrivate(filepath.Join(directory, "record.json"), record); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "payload"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	return s, record, payload, directory
}

func TestNativeDesktopDownloadUsesProducerAndOneStreamingConnection(t *testing.T) {
	for _, system := range []string{"macos", "windows"} {
		t.Run(system, func(t *testing.T) {
			s, record, payload, directory := nativeDesktopDownloadFixture(t, system)
			response := windowsBrowserRequestForTest(s, http.MethodGet, "/api/browser/files/downloads/"+record.ID+"/content", nil)
			if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), payload) {
				t.Fatalf("download failed: %d, %d bytes", response.Code, response.Body.Len())
			}
			trace, err := os.ReadFile(filepath.Join(directory, "requests.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			connections := map[string]bool{}
			reads := 0
			for _, line := range bytes.Split(bytes.TrimSpace(trace), []byte("\n")) {
				var request struct{ Connection, Action string }
				if json.Unmarshal(line, &request) != nil {
					t.Fatal("invalid request trace")
				}
				if strings.HasPrefix(request.Action, "files-download-") {
					connections[request.Connection] = true
				}
				if request.Action == "files-download-read" {
					reads++
				}
			}
			if len(connections) != 1 || reads != 3 {
				t.Fatalf("chunk transfer opened %d connections for %d reads", len(connections), reads)
			}
			s.ready = false
			if err := os.Remove(filepath.Join(directory, "payload")); err != nil {
				t.Fatal(err)
			}
			response = windowsBrowserRequestForTest(s, http.MethodGet, "/api/browser/files/downloads/"+record.ID+"/content", nil)
			if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), payload) {
				t.Fatal("saved Desktop file depended on the running guest")
			}
		})
	}
}

func TestNativeDesktopDownloadFailureRetriesThroughFileEntry(t *testing.T) {
	for _, system := range []string{"macos", "windows"} {
		t.Run(system, func(t *testing.T) {
			s, record, payload, directory := nativeDesktopDownloadFixture(t, system)
			failure := filepath.Join(directory, "fail-read")
			if err := os.WriteFile(failure, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.downloadInfo(t.Context(), record.ID); err == nil {
				t.Fatal("failed source read became a successful download")
			}
			if _, err := readSavedDownload(s.profile, record.ID); err == nil {
				t.Fatal("partial Desktop transfer was published")
			}
			if _, err := s.browserStateForHost([]byte(`{"tabs":[],"downloads":[]}`)); err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			retained := s.nativeDownloads[record.ID] != nil
			s.mu.Unlock()
			if !retained {
				t.Fatal("browser polling discarded the Desktop retry record")
			}
			if err := os.Remove(failure); err != nil {
				t.Fatal(err)
			}
			body := []byte(fmt.Sprintf(`{"target":"desktop","id":%q}`, record.ID))
			response := windowsBrowserRequestForTest(s, http.MethodPost, "/api/input/files/download-retry", body)
			if response.Code != http.StatusOK {
				t.Fatalf("retry failed: %d %s", response.Code, response.Body.String())
			}
			saved, err := s.downloadInfo(t.Context(), record.ID)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(saved.Path)
			if err != nil || !bytes.Equal(data, payload) {
				t.Fatal("retried Desktop file differs from producer", err)
			}
		})
	}
}

func TestNativeDesktopOpenDownloadClaimsTheSavedHostFile(t *testing.T) {
	for _, system := range []string{"macos", "windows"} {
		t.Run(system, func(t *testing.T) {
			s, record, payload, _ := nativeDesktopDownloadFixture(t, system)
			openID := uuid.NewString()
			body, _ := json.Marshal(map[string]string{"id": openID, "stage": "claim"})
			response := windowsBrowserRequestForTest(s, http.MethodPost, "/api/input/files/desktop/open", body)
			var opened desktopFileOpen
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &opened) != nil {
				t.Fatalf("open claim failed: %d %s", response.Code, response.Body.String())
			}
			if opened.ID != openID || opened.Action != "open" || !filepath.IsAbs(opened.Path) {
				t.Fatalf("invalid native open response: %+v", opened)
			}
			content, err := os.ReadFile(opened.Path)
			if err != nil || !bytes.Equal(content, payload) {
				t.Fatal("open did not await a complete host copy", err)
			}
			saved, err := readSavedDownload(s.profile, record.ID)
			if err != nil || saved.Path != opened.Path {
				t.Fatal("open did not use the producer's download identity", err)
			}
			body, _ = json.Marshal(map[string]string{"id": openID, "stage": "complete"})
			response = windowsBrowserRequestForTest(s, http.MethodPost, "/api/input/files/desktop/open", body)
			if response.Code != http.StatusOK {
				t.Fatal("open completion failed", response.Body.String())
			}
		})
	}
}
