package agentcli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentCLIProtocolAndInit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile", "config.json")
	run := func(args ...string) (int, agentReply) {
		t.Helper()
		var output bytes.Buffer
		code := Main(append(args, "--config", path), strings.NewReader(""), &output)
		var reply agentReply
		if json.Unmarshal(output.Bytes(), &reply) != nil || reply.Protocol != 1 {
			t.Fatalf("not one protocol reply: %s", output.String())
		}
		return code, reply
	}
	if code, _ := run("capabilities"); code != 0 {
		t.Fatal(code)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("discovery created profile")
	}
	if code, _ := run("init"); code != 0 {
		t.Fatal(code)
	}
	before, _ := os.ReadFile(path)
	if code, _ := run("init"); code != 0 {
		t.Fatal("not idempotent")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("repeat init overwrote config")
	}
	for _, args := range [][]string{{"call", "status", "--input", "-"}, {"wat"}, {"doctor", "--wait", "1s"}, {"capabilities", "--wait", "-1s"}, {"call"}} {
		if code, _ := run(args...); code != 2 {
			t.Fatalf("usage accepted %v: %d", args, code)
		}
	}
	if err := os.WriteFile(path, []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if code, reply := run("init"); code != 1 || reply.Error.Code != "config_invalid" {
		t.Fatal("damaged config silently replaced")
	}
}

func TestAgentCLIRejectsSecretArgumentsAndMalformedJSON(t *testing.T) {
	for _, input := range []string{`null`, `[]`, `{} {}`, `{"password":"secret"} trailing`} {
		var output bytes.Buffer
		code := Main([]string{"call", "account.unlock", "--input", "-", "--config", filepath.Join(t.TempDir(), "config.json")}, strings.NewReader(input), &output)
		if code != 2 || strings.Contains(output.String(), "secret") {
			t.Fatal(code, output.String())
		}
	}
	var output bytes.Buffer
	code := Main([]string{"call", "account.unlock", "--password", "do-not-echo"}, strings.NewReader(""), &output)
	if code != 2 || strings.Contains(output.String(), "do-not-echo") {
		t.Fatal(output.String())
	}
}

func TestAgentWaitTimeoutKeepsOriginalJobID(t *testing.T) {
	data, err := agentWait(context.Background(), filepath.Join(t.TempDir(), "config.json"), "012345abcdef", time.Millisecond)
	if err == nil || data != nil || !strings.Contains(err.Error(), "job may still be running") {
		t.Fatal(string(data), err)
	}
}
