package workbench

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sylvinus/agent-vm/internal/guestos"
)

func TestWindowsGuestTerminalRPC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWindowsTerminalRelayHelper$")
	cmd.Env = append(os.Environ(), "CAGE_WINDOWS_TERMINAL_RPC_TEST=echo")
	p, err := startWindowsGuestTerminal(cmd, 100, 30)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	expected := []byte("中文🙂\x03")
	if _, err = p.Write(expected); err != nil {
		t.Fatal(err)
	}
	actual := make([]byte, len(expected))
	if _, err = io.ReadFull(p, actual); err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(expected) {
		t.Fatalf("output changed: %q", actual)
	}
	if err = p.Resize(160, 50); err != nil {
		t.Fatal(err)
	}
	marker := make([]byte, len("160x50"))
	if _, err = io.ReadFull(p, marker); err != nil {
		t.Fatal(err)
	}
	if string(marker) != "160x50" {
		t.Fatalf("resize did not reach guest: %q", marker)
	}
	if err = p.Resize(0, 50); err == nil {
		t.Fatal("invalid resize admitted")
	}
	if _, err = p.Write([]byte("exit")); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("clean remote exit got %v", err)
	}
	if err = p.Wait(); err != nil {
		t.Fatal(err)
	}
}
func TestWindowsGuestTerminalRejectsMissingReady(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWindowsTerminalRelayHelper$")
	cmd.Env = append(os.Environ(), "CAGE_WINDOWS_TERMINAL_RPC_TEST=unavailable")
	if p, err := startWindowsGuestTerminal(cmd, 100, 30); err == nil {
		p.Close()
		t.Fatal("unavailable GUI became terminal")
	}
}

func TestWindowsGuestTerminalLargePasteThroughWorkbench(t *testing.T) {
	s := testServer(t)
	s.windowsGuest = &guestos.WindowsProfile{}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWindowsTerminalRelayHelper$")
	command.Env = append(os.Environ(), "CAGE_WINDOWS_TERMINAL_RPC_TEST=echo")
	session, err := s.terminals.startSessionWithPTY(command, t.TempDir(), "shell", claudeSelection{}, nil, func() (terminalPTY, error) {
		return startWindowsGuestTerminal(command, 100, 30)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.terminals.close()
	server := httptest.NewServer(s.handler())
	defer server.Close()
	s.origin = server.URL
	headers := http.Header{
		"Origin": {s.origin},
		"Cookie": {s.cookieName() + "=" + s.session + "; " + s.cookieName() + "-desktop=" + trustedViewProof(s.token)},
	}
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.origin, "http")+"/api/terminals/"+session.id+"/stream", headers)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	read := func(expected string) {
		t.Helper()
		if err := ws.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		var actual []byte
		for len(actual) < len(expected) {
			_, data, err := ws.ReadMessage()
			if err != nil {
				t.Fatalf("terminal disconnected after %d of %d bytes: %v", len(actual), len(expected), err)
			}
			actual = append(actual, data...)
		}
		if string(actual) != expected {
			t.Fatalf("terminal changed input bytes: received=%d expected=%d", len(actual), len(expected))
		}
	}
	for _, payload := range []string{"small", strings.Repeat("x", 64<<10), strings.Repeat("x", (64<<10)+1), strings.Repeat("中文🙂\n", 20000), strings.Repeat("x", (1<<20)-64)} {
		if err := ws.WriteJSON(map[string]string{"type": "input", "data": payload}); err != nil {
			t.Fatal(err)
		}
		read(payload)
	}
	if err := ws.WriteJSON(map[string]any{"type": "resize", "cols": 599, "rows": 399}); err != nil {
		t.Fatal(err)
	}
	read("599x399")
	if session.summary()["closed"] != false {
		t.Fatal("large paste closed its terminal")
	}
}

func TestWindowsTerminalRelayHelper(t *testing.T) {
	mode := os.Getenv("CAGE_WINDOWS_TERMINAL_RPC_TEST")
	if mode == "" {
		return
	}
	reader := bufio.NewScanner(os.Stdin)
	reader.Buffer(make([]byte, 4096), 100<<10)
	if !reader.Scan() {
		os.Exit(2)
	}
	var open map[string]any
	if json.Unmarshal(reader.Bytes(), &open) != nil || open["action"] != "open" || open["workspace"] != "home" || open["shell"] != "powershell" {
		os.Exit(2)
	}
	write := func(value any) {
		if json.NewEncoder(os.Stdout).Encode(value) != nil {
			os.Exit(2)
		}
	}
	if mode == "unavailable" {
		write(map[string]any{"error": "requires_gui_login"})
		os.Exit(1)
	}
	write(map[string]any{"ready": true})
	for reader.Scan() {
		var value map[string]any
		if json.Unmarshal(reader.Bytes(), &value) != nil {
			os.Exit(2)
		}
		switch value["action"] {
		case "input":
			data, err := base64.StdEncoding.DecodeString(value["data"].(string))
			if err != nil || len(data) > 64<<10 {
				os.Exit(2)
			}
			if string(data) == "exit" {
				write(map[string]any{"exit": 0})
				os.Exit(0)
			}
			write(map[string]any{"data": base64.StdEncoding.EncodeToString(data)})
		case "resize":
			cols, rows := value["cols"].(float64), value["rows"].(float64)
			if cols < 1 || cols >= 600 || rows < 1 || rows > 500 {
				os.Exit(2)
			}
			write(map[string]any{"data": base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "%dx%d", int(cols), int(rows)))})
		default:
			os.Exit(2)
		}
	}
	os.Exit(0)
}
