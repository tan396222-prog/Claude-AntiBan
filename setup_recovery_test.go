package agentvm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func setupFunction(t *testing.T, name string, end string) string {
	t.Helper()
	start := strings.Index(SetupScript, name+"() ")
	if start < 0 {
		t.Fatal("missing function", name)
	}
	body := SetupScript[start:]
	last := strings.Index(body, "\n"+end+"\n")
	if last < 0 {
		t.Fatal("missing function end", name)
	}
	return body[:last+len(end)+2]
}

func TestRepositoryKeyFailurePreservesTrustedFile(t *testing.T) {
	for _, mode := range []string{"eof", "empty", "invalid", "success"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			key := filepath.Join(dir, "nodesource.asc")
			if err := os.WriteFile(key, []byte("trusted"), 0644); err != nil {
				t.Fatal(err)
			}
			commands := map[string]string{
				"curl": `#!/bin/sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = -o ]; then shift; target="$1"; fi
  shift
done
case "$KEY_MODE" in
  eof) printf partial > "$target"; exit 56;;
  empty) : > "$target";;
  invalid) printf '<html>bad gateway</html>' > "$target";;
  success) printf '%s\n' '-----BEGIN PGP PUBLIC KEY BLOCK-----' new '-----END PGP PUBLIC KEY BLOCK-----' > "$target";;
esac
`,
				// Exercise actual local filesystem operations without root.
				"sudo": `#!/bin/sh
if [ "$1" = install ]; then
  shift
  while [ "$#" -gt 2 ]; do shift; done
  cp "$1" "$2"
else
  exec "$@"
fi
`,
			}
			for name, body := range commands {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0755); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("bash", "-c", setupFunction(t, "install_repository_key", ")")+`install_repository_key https://example.invalid/key "$KEY_TARGET"`)
			cmd.Env = append(os.Environ(), "HOME="+dir, "PATH="+dir+":"+os.Getenv("PATH"), "KEY_MODE="+mode, "KEY_TARGET="+key)
			out, err := cmd.CombinedOutput()
			data, readErr := os.ReadFile(key)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if mode == "success" {
				if err != nil || !strings.Contains(string(data), "\nnew\n") {
					t.Fatalf("%v %s %q", err, out, data)
				}
			} else if err == nil || string(data) != "trusted" {
				t.Fatalf("failed download replaced key: %v %q", err, data)
			}
			left, _ := filepath.Glob(key + ".*")
			if len(left) != 0 {
				t.Fatal("staging key leaked", left)
			}
		})
	}
}

func TestAPTFailureRecordsOOMWithoutChangingExitCode(t *testing.T) {
	dir := t.TempDir()
	commands := map[string]string{
		"sudo": `#!/bin/sh
if [ "$1" = env ]; then echo apt-failed; exit 137; fi
if [ "$1" = timeout ]; then echo 'Out of memory: Killed process 2926 (apt-get)'; exit 0; fi
exit 99
`,
		"awk": "#!/bin/sh\necho 3\n",
		"ps":  "#!/bin/sh\necho 'PID RSS COMMAND'\n",
	}
	for name, body := range commands {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("bash", "-c", "set -euo pipefail\n"+setupFunction(t, "apt_get", "}")+"apt_get install package")
	cmd.Env = append(os.Environ(), "HOME="+dir, "PATH="+dir+":"+os.Getenv("PATH"), "CLAUDE_CAGE=1")
	out, err := cmd.CombinedOutput()
	if err == nil || cmd.ProcessState.ExitCode() != 137 || !strings.Contains(string(out), "oom_kill before=3") || !strings.Contains(string(out), "Killed process") {
		t.Fatalf("lost failure evidence: %v %s", err, out)
	}
}
