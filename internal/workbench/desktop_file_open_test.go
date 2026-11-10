package workbench

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sylvinus/agent-vm/internal/guestos"
)

func TestNativeDesktopProjectPathsUseCurrentGuestMount(t *testing.T) {
	for _, system := range []string{"macos", "windows"} {
		t.Run(system, func(t *testing.T) {
			s := testServer(t)
			project := t.TempDir()
			file := filepath.Join(project, "中文 #+%.md")
			if err := os.WriteFile(file, []byte("project document"), 0600); err != nil {
				t.Fatal(err)
			}
			var guest string
			closed := make(chan struct{})
			if system == "macos" {
				s.macosGuest = &guestos.MacOSProfile{User: "cage"}
				guest = macOSProjectPath("cage", project)
			} else {
				s.windowsGuest = &guestos.WindowsProfile{}
				guest = "C:/Cage/Projects/0123456789abcdef"
				s.projectShares = map[string]*projectShare{project: {done: closed, windowsProject: &windowsProjectBinding{Path: guest, ID: "0123456789abcdef", Grant: "test"}}}
			}
			got, err := s.resolveDesktopProjectFile(guest+"/"+filepath.Base(file), []string{project})
			want, _ := filepath.EvalSymlinks(file)
			if err != nil || got != want {
				t.Fatalf("guest mount did not resolve to host original: %q %v", got, err)
			}
			for _, invalid := range []string{guest + "-other/" + filepath.Base(file), guest + "/../outside", guest + "/missing", "relative", "file://" + guest} {
				if _, err := s.resolveDesktopProjectFile(invalid, []string{project}); err == nil {
					t.Fatalf("accepted invalid guest path %q", invalid)
				}
			}
			if system == "windows" {
				close(closed)
				if _, err := s.resolveDesktopProjectFile(guest+"/"+filepath.Base(file), []string{project}); err == nil {
					t.Fatal("revoked Windows share still granted file access")
				}
			}
		})
	}
}

func TestDesktopFilePathsStayInProjectGrants(t *testing.T) {
	project := t.TempDir()
	file := filepath.Join(project, "中文 Word #+%.docx")
	if err := os.WriteFile(file, []byte("document"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, project} {
		got, err := resolveProjectFilePath(guestPath(path), []string{project})
		want, _ := filepath.EvalSymlinks(path)
		if err != nil || got != want {
			t.Fatalf("project file did not map: %q %v", got, err)
		}
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{outside, project + "-sibling/secret.txt", guestPath(project) + "/../secret", "file:///etc/passwd", "relative", guestPath(file) + "\x00", guestPath(project) + "/missing"} {
		if got, err := resolveProjectFilePath(path, []string{project}); err == nil {
			t.Fatalf("ungranted path accepted: %q -> %q", path, got)
		}
	}
	link := filepath.Join(project, "outside-link")
	if err := os.Symlink(filepath.Dir(outside), link); err != nil {
		t.Skip("symlink unavailable: " + err.Error())
	}
	if _, err := resolveProjectFilePath(guestPath(filepath.Join(link, "secret.txt")), []string{project}); err == nil {
		t.Fatal("host-side symlink escaped project")
	}
}

func TestDesktopFileCommandsPreservePathsAndRevealSemantics(t *testing.T) {
	path := "/tmp/项目/Word #+%; $(touch unsafe).docx"
	for _, tc := range []struct {
		platform, action string
		directory        bool
		want             []string
	}{
		{"darwin", "open", false, []string{"/usr/bin/open", "--", path}},
		{"darwin", "reveal", false, []string{"/usr/bin/open", "-R", "--", path}},
		{"windows", "open", false, []string{"rundll32.exe", "url.dll,FileProtocolHandler", path}},
		{"windows", "open", true, []string{"explorer.exe", path}},
		{"windows", "reveal", false, []string{"explorer.exe", "/select," + path}},
		{"linux", "open", false, []string{"gio", "open", path}},
	} {
		got := hostFileCommand(tc.platform, path, tc.action, tc.directory).Args
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s %s: %#v", tc.platform, tc.action, got)
		}
	}
	args := hostFileCommand("linux", path, "reveal", false).Args
	if args[0] != "gdbus" || !strings.Contains(strings.Join(args, " "), "ShowItems") || !strings.Contains(args[len(args)-2], "%23+%25") {
		t.Fatal("Linux reveal lost selection or URI escaping", args)
	}
}

func TestDesktopFileOpenAdmissionRejectsUntrustedRequests(t *testing.T) {
	s := testServer(t)
	s.ready = true
	path := "/api/input/files/desktop/open"
	body := `{"id":"` + testBrowserFileID + `","stage":"claim"}`
	for _, tc := range []struct {
		origin string
		auth   bool
		body   string
		code   int
	}{
		{s.origin, false, body, 401},
		{"https://untrusted.example", true, body, 403},
		{s.origin, true, `{"id":"../host-path","stage":"claim"}`, 400},
		{s.origin, true, `{"id":"` + testBrowserFileID + `","stage":"open","path":"/etc/passwd"}`, 400},
	} {
		w := request(s, "POST", path, tc.origin, tc.body, tc.auth)
		if w.Code != tc.code {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	s.account.lock()
	if w := request(s, "POST", path, s.origin, body, true); w.Code != 423 {
		t.Fatal("locked file operation accepted", w.Code)
	}
}

func TestDesktopFileErrorsReachNativeCaller(t *testing.T) {
	origin := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != origin || r.Header.Get(connectionHeader) != "connection-one" {
			t.Error("missing file operation origin or connection binding")
		}
		http.Error(w, "只能打开 Cage 项目目录中的文件和文件夹", 400)
	}))
	defer server.Close()
	origin = server.URL
	client := &controllerClient{origin: server.URL, client: server.Client()}
	client.client.Transport = connectionTransport{base: http.DefaultTransport, id: "connection-one"}
	err := desktopOpenCall(client, map[string]string{"id": testBrowserFileID, "stage": "claim"}, nil)
	if err == nil || err.Error() != "只能打开 Cage 项目目录中的文件和文件夹" {
		t.Fatal("native caller lost useful error", err)
	}
}

func TestDesktopFileNativeReveal(t *testing.T) {
	if os.Getenv("CAGE_TEST_NATIVE_REVEAL") != "1" {
		t.Skip("explicit desktop integration test")
	}
	project := t.TempDir()
	file := filepath.Join(project, "Cage 生成的 Word 测试.docx")
	if fixture := os.Getenv("CAGE_TEST_NATIVE_REVEAL_PATH"); fixture != "" {
		file, project = fixture, filepath.Dir(fixture)
	} else {
		if err := os.WriteFile(file, []byte("reveal fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	resolved, err := resolveProjectFilePath(guestPath(file), []string{project})
	if err != nil {
		t.Fatal(err)
	}
	if err = openHostFile(resolved, "reveal"); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("CAGE_TEST_NATIVE_OPEN") == "1" {
		if err = openHostFile(resolved, "open"); err != nil {
			t.Fatal(err)
		}
	}
}
