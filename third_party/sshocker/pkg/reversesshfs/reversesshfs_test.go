package reversesshfs

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// TestHelperProcess is a child that waits to be killed, for TestCloseReaps.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("REVERSESSHFS_TEST_SLEEP") == "" {
		return
	}
	time.Sleep(time.Minute)
}

// Close kills the processes and reaps them: a killed child left unwaited stays a zombie, with its pipes.
func TestCloseReaps(t *testing.T) {
	start := func() *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
		cmd.Env = append(os.Environ(), "REVERSESSHFS_TEST_SLEEP=1")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	rsf := &ReverseSSHFS{sshCmd: start(), opensshSftpServerCmd: start()}
	if err := rsf.Close(); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []*exec.Cmd{rsf.sshCmd, rsf.opensshSftpServerCmd} {
		if cmd.ProcessState == nil {
			t.Error("a killed process was not waited for")
		}
	}
}

func TestAddQuotes(t *testing.T) {
	type testCase struct {
		input    string
		expected string
	}
	var testCases []testCase
	if runtime.GOOS != "windows" {
		testCases = []testCase{
			{
				input:    "/user/test/path",
				expected: "\"/user/test/path\"",
			},
		}
	} else {
		testCases = []testCase{
			{
				input:    "/user/test/path",
				expected: "'/user/test/path'",
			},
		}
	}
	for i, tc := range testCases {
		got := addQuotes(tc.input)
		if got != tc.expected {
			t.Errorf("#%d: expected %q, got %q", i, tc.expected, got)
		}
	}
}

func TestConvertMSYS2Path(t *testing.T) {
	inputPath := "/c/Users/lts"
	expectedPath := "C:\\Users\\lts"

	actualPath := convertMSYS2Path(inputPath)

	if actualPath != expectedPath {
		t.Errorf("Conversion failed: expected %q, got %q", expectedPath, actualPath)
	} else {
		t.Logf("Success! Converted path for native Windows OpenSSH: %q", actualPath)
	}
}
