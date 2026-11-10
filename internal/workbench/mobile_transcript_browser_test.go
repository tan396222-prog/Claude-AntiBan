package workbench

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sylvinus/agent-vm/internal/cliconversation"
	"github.com/sylvinus/agent-vm/internal/mobilelink"
	"github.com/sylvinus/agent-vm/internal/relay"
	"github.com/sylvinus/agent-vm/internal/remotelink"
)

func TestMobileTranscriptInBrowser(t *testing.T) {
	if os.Getenv("CAGE_TEST_MOBILE_BROWSER") == "" {
		t.Skip("set CAGE_TEST_MOBILE_BROWSER after building the phone UI; needs ffmpeg")
	}
	s := testServer(t)
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivate(filepath.Join(s.profile, "environment.json"), map[string]string{"directory": project}); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc2=s=320x240:r=24", "-t", "2", "-c:v", "libx264", "-qp", "0", "-movflags", "+faststart", filepath.Join(project, "demo.mp4")).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	logo, err := os.ReadFile("../../ui/src/logo.png")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(project, "cover.png"), logo, 0600); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err := s.mobileCLI.create(s.profile, project, id); err != nil {
		t.Fatal(err)
	}
	rec, err := s.mobileCLI.record(s.profile, id)
	if err != nil {
		t.Fatal(err)
	}
	rec.Title = "宣传片制作"
	rec.Turns = []mobileTurn{
		{ID: uuid.NewString(), Prompt: "检查宣传片并预览成片", Status: "completed", Text: "## 交付文件\n\n**以后维护**：运行 `npm run plates`。\n\n- [x] 横版与竖版均已导出\n\n| 版本 | 比例 |\n| --- | --- |\n| 竖版 | 9:16 |\n\n![封面](cover.png)\n\n[成片](demo.mp4)\n\n![缺失](missing.png)\n\n[查看网页](https://example.org/demo)\n\n```xml\n<task-notification>示例</task-notification>\n```", Tools: []cliconversation.ToolActivity{{ID: "a", Name: "Bash", Detail: "检查帧率", Status: "completed"}, {ID: "b", Name: "Read", Status: "failed"}}},
		{ID: uuid.NewString(), Prompt: "<task-notification>\n<task-id>fixture-id</task-id>\n<status>completed</status>\n<summary>竖版视频已导出</summary>\n</task-notification>", Status: "completed"},
	}
	if err = writePrivate(mobileRecordPath(s.profile, id), rec); err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(s.handler())
	defer host.Close()
	s.origin = host.URL
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	backend, err := mobilelink.OpenWorkbench(ctx, host.URL, s.issueTicket())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	admin := strings.Repeat("q", 48)
	service, err := relay.New(relay.Config{AdminToken: admin})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	relayHTTP := httptest.NewServer(service)
	defer relayHTTP.Close()
	room, err := mobilelink.CreateRoom(ctx, relayHTTP.URL, admin)
	if err != nil {
		t.Fatal(err)
	}
	hostID, err := remotelink.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	phoneID, err := remotelink.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	go mobilelink.ServeHost(ctx, relayHTTP.URL, room.ID, room.HostToken, hostID, phoneID.Pin, backend)
	phone, err := mobilelink.ConnectClient(ctx, relayHTTP.URL, room.ID, room.ClientToken, phoneID, hostID.Pin)
	if err != nil {
		t.Fatal(err)
	}
	defer phone.Close()
	web := httptest.NewUnstartedServer(nil)
	defer web.Close()
	assets, err := filepath.Abs("../../mobile/.build/resources/mobile-ui")
	if err != nil {
		t.Fatal(err)
	}
	handler, entry, err := mobilelink.NewBrowser("http://"+web.Listener.Addr().String(), assets, phone)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	web.Config.Handler = handler
	web.Start()
	driver := exec.CommandContext(ctx, "node", "../../ui/scripts/mobile-transcript-smoke.mjs")
	driver.Stdin = strings.NewReader(entry)
	if output, err := driver.CombinedOutput(); err != nil {
		t.Fatalf("browser: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}
