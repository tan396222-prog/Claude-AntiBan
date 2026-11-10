package workbench

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWindowsMaintenanceFailureRetainsStageForBothJobs(t *testing.T) {
	cause := errors.New("synthetic maintenance process failed")
	for _, action := range []string{"修复 Windows 系统助手", "更新原生客机组件"} {
		for _, test := range []struct {
			phase, reason, next string
		}{
			{"AUTHORIZATION_CANCELED", "已取消 Windows 管理员授权", "系统授权窗口"},
			{"AUTHORIZATION", "授权窗口未能打开", "本机 Cage"},
			{"VALIDATION", "绑定或组件清单", "重新安装完整 Cage"},
			{"HOST_MANAGER", "系统管理助手安装", "修复系统助手"},
			{"GUEST_CONNECTION", "客机连接未完成", "管理员账户"},
			{"GUEST_COMPONENTS", "客机组件安装", "同步环境组件"},
			{"HOST_RELAY", "主机连接组件更新", "占用连接组件"},
			{"PACKAGE_PREPARATION", "组件包准备", "系统盘空间"},
			{"FAILED", "Windows 维护未完成", "导出诊断包"},
		} {
			t.Run(action+"/"+test.phase, func(t *testing.T) {
				output := "CAGE_WINDOWS_MAINTENANCE_" + test.phase + " synthetic stage\naccess is denied token=private-stage-secret"
				err := executionFailure(action, cause, output)
				for _, want := range []string{action, test.reason, test.next} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("lost maintenance stage or next action %q: %v", want, err)
					}
				}
				if !errors.Is(err, cause) || strings.Contains(err.Error(), "private-stage-secret") {
					t.Fatal("stage handling lost the cause or exposed arbitrary process output", err)
				}
			})
		}
	}
}

func TestBrowserFailureExplainsOperationAndKnownCause(t *testing.T) {
	for _, tc := range []struct {
		status     int
		body, want string
	}{
		{400, `{"error":"Tab no longer exists"}`, "标签页已关闭"},
		{401, `{"error":"private token=do-not-display"}`, "授权"},
		{503, `{"error":"private token=do-not-display"}`, "尚未就绪"},
		{500, `{"error":"private token=do-not-display"}`, "HTTP 500"},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer guest.Close()
			client := localBrowserClient(t, context.Background(), guest)
			defer client.close()
			_, err := client.request(context.Background(), "test-token", "POST", "/navigate", []byte(`{}`))
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "do-not-display") {
				t.Fatalf("wrong user failure: %v", err)
			}
		})
	}
}

func TestFileServiceRetainsKnownRejectionAndHidesUnknownBody(t *testing.T) {
	known := fileServiceFailure(400, []byte("上传窗口已关闭，请重新选择文件"))
	if !strings.Contains(known.Error(), "上传窗口已关闭") || !strings.Contains(known.Error(), "重新点击") {
		t.Fatalf("file window reason lost: %v", known)
	}
	unknown := fileServiceFailure(500, []byte("unknown response token=private-file-secret"))
	if !strings.Contains(unknown.Error(), "HTTP 500") || strings.Contains(unknown.Error(), "private-file-secret") {
		t.Fatalf("unsafe unknown response: %v", unknown)
	}
}
