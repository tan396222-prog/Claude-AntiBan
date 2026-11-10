package workbench

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/managedproxy"
)

func TestProxyImportAPIReportsPartialSuccessAndProtectsRefresh(t *testing.T) {
	s := testServer(t)
	cfg, _ := loadConfig(s.configPath)
	cfg.ProxyRevision = ""
	if err := writePrivate(s.configPath, cfg); err != nil {
		t.Fatal(err)
	}
	oldValidate, oldFetch := validateProxyCore, fetchProxySource
	t.Cleanup(func() { validateProxyCore, fetchProxySource = oldValidate, oldFetch })
	validateProxyCore = func(_ context.Context, profile string, p managedproxy.Store) error {
		_, err := p.Runtime(profile)
		return err
	}
	pin := strings.Repeat("ab", 32)
	content := `proxies:
 - {name: hopping, type: hysteria2, server: example.com, port: 35000, password: private-node-password, mport: 35000-39000, pinSHA256: "` + pin + `"}
 - {name: unsupported, type: socks5, server: example.com, port: 1080, INTERFACE_NAME: private-field-value}
`
	mutate := func(action string, input proxyInput) Job {
		t.Helper()
		body, _ := json.Marshal(input)
		w := request(s, "POST", "/api/proxy/"+action, s.origin, string(body), true)
		if w.Code != http.StatusOK {
			t.Fatal(w.Code, w.Body.String())
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			job := s.jobs.snapshot()
			if !job.Running {
				return job
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("proxy import job did not finish")
		return Job{}
	}
	if job := mutate("import", proxyInput{Name: "feedback", Content: content, Interval: 24}); job.Error != "" {
		t.Fatal(job.Error)
	}
	p, _ := managedproxy.Load(s.profile)
	src := p.Sources[1]
	if len(src.Nodes) != 1 || len(src.Skipped) != 1 || src.Nodes[0].Config["fingerprint"] != pin {
		t.Fatal("API import lost the valid node, certificate pin or skipped-node report")
	}
	w := request(s, "GET", "/api/proxy", s.origin, "", true)
	if w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
	var view struct {
		Sources []struct {
			Skipped []managedproxy.NodeIssue `json:"skipped"`
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || len(view.Sources[1].Skipped) != 1 || view.Sources[1].Skipped[0].Index != 2 {
		t.Fatal("status lost node import diagnostics", err, w.Body.String())
	}
	for _, secret := range []string{"private-node-password", "private-field-value", pin, p.Auth} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("status leaked subscription credentials or field values")
		}
	}
	// A later successful refresh replaces the report. An unsuccessful refresh
	// must keep that last successful source and its metadata.
	p.Sources[1].URL = "https://example.com/sub?token=private-subscription"
	p.Selected = src.Nodes[0].ID
	p.Chain = []string{p.Selected}
	if err := p.Save(s.profile); err != nil {
		t.Fatal(err)
	}
	response := strings.Split(content, " - {name: unsupported")[0]
	fetchProxySource = func(context.Context, *http.Client, string) ([]byte, string, error) {
		return []byte(response), "", nil
	}
	if job := mutate("refresh", proxyInput{ID: src.ID, HostDownload: true}); job.Error != "" {
		t.Fatal(job.Error)
	}
	before, _ := managedproxy.Load(s.profile)
	if len(before.Sources[1].Skipped) != 0 {
		t.Fatal("successful refresh retained stale skipped-node report")
	}
	response = strings.Replace(content, "mport: 35000-39000", "mport: invalid", 1) + " - {name: spare, type: http, server: example.com, port: 8080}\n"
	if job := mutate("refresh", proxyInput{ID: src.ID, HostDownload: true}); !strings.Contains(job.Error, "hopping") || !strings.Contains(job.Error, "mport") || strings.Contains(job.Error, "private-") {
		t.Fatal("refresh did not identify the skipped active exit safely", job.Error)
	}
	after, _ := managedproxy.Load(s.profile)
	if !reflect.DeepEqual(after.Sources[1].Nodes, before.Sources[1].Nodes) || after.Selected != before.Selected || !reflect.DeepEqual(after.Chain, before.Chain) || !after.Sources[1].Updated.Equal(before.Sources[1].Updated) || after.Sources[1].Error == "" {
		t.Fatal("failed refresh changed the last working nodes or route")
	}
}

func TestProxyImportAndRefreshUseBundledCoreValidation(t *testing.T) {
	if os.Getenv("CAGE_PROXY_INTEGRATION") != "1" {
		t.Skip("requires a test binary beside the bundled Mihomo")
	}
	s := testServer(t)
	cfg, _ := loadConfig(s.configPath)
	cfg.ProxyRevision = ""
	if err := writePrivate(s.configPath, cfg); err != nil {
		t.Fatal(err)
	}
	content := `proxies: [{name: feedback, type: vless, server: example.com, port: 443, uuid: 00000000-0000-4000-8000-000000000000, xudp: true, future-option: private-field-value}]`
	if err := s.changeProxy("import", proxyInput{Name: "feedback", Content: content}, nil); err != nil {
		t.Fatal(err)
	}
	before, err := managedproxy.Load(s.profile)
	if err != nil || len(before.Sources) != 2 || before.Sources[1].Nodes[0].Config["xudp"] != true {
		t.Fatal("core-compatible node was not saved", err)
	}
	src := &before.Sources[1]
	src.URL = "https://example.com/sub?token=private-subscription"
	before.Selected = src.Nodes[0].ID
	before.Chain = []string{before.Selected}
	if err := before.Save(s.profile); err != nil {
		t.Fatal(err)
	}
	oldFetch := fetchProxySource
	t.Cleanup(func() { fetchProxySource = oldFetch })
	fetchProxySource = func(context.Context, *http.Client, string) ([]byte, string, error) {
		return []byte(strings.Replace(content, "xudp: true", "xudp: {invalid: private-value}", 1)), "", nil
	}
	err = s.changeProxy("refresh", proxyInput{ID: src.ID, HostDownload: true}, nil)
	if err == nil || !strings.Contains(err.Error(), "Mihomo") || strings.Contains(err.Error(), "private-") {
		t.Fatal("core did not safely reject the invalid refresh", err)
	}
	after, err := managedproxy.Load(s.profile)
	if err != nil || !reflect.DeepEqual(after.Sources[1].Nodes, src.Nodes) || after.Selected != before.Selected || !reflect.DeepEqual(after.Chain, before.Chain) || !after.Sources[1].Updated.Equal(src.Updated) || after.Sources[1].Error == "" {
		t.Fatal("core rejection changed the last good source or active route", err)
	}
}
