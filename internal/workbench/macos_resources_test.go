package workbench

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/guestos"
	"github.com/sylvinus/agent-vm/internal/host"
)

func TestMacOSResourceSaveUsesGuestMinimumAndPreservesOtherData(t *testing.T) {
	oldMemory, oldCPUs := host.MemGiB, host.CPUs
	t.Cleanup(func() { host.MemGiB, host.CPUs = oldMemory, oldCPUs })
	host.MemGiB = func() int { return 24 }
	host.CPUs = func() int { return 10 }
	t.Setenv("AGENT_VM_HOST_SHARE", "2")
	s := testServer(t)
	s.macosGuest = &guestos.MacOSProfile{Instance: "mac", User: "cage"}
	if err := os.MkdirAll(s.instanceDir(), 0700); err != nil {
		t.Fatal(err)
	}
	configuration := []byte("os: Darwin\nmemory: 4GiB\ncpus: 2\n")
	if err := os.WriteFile(filepath.Join(s.instanceDir(), "lima.yaml"), configuration, 0600); err != nil {
		t.Fatal(err)
	}
	network, _ := os.ReadFile(s.configPath)
	for _, body := range []string{`{"allocation":{"memory_gib":2,"cpus":2}}`, `{"allocation":{"memory_gib":4,"cpus":1}}`, `{"allocation":{"memory_gib":13,"cpus":2}}`} {
		response := windowsBrowserRequestForTest(s, http.MethodPost, "/api/environment/resources", []byte(body))
		if response.Code == 200 {
			t.Fatalf("accepted invalid macOS allocation %s", body)
		}
	}
	response := windowsBrowserRequestForTest(s, http.MethodPost, "/api/environment/resources", []byte(`{"allocation":{"memory_gib":6,"cpus":3}}`))
	if response.Code != 200 {
		t.Fatalf("save failed %d %s", response.Code, response.Body.String())
	}
	allocation, err := cage.LoadResourceAllocation(s.configPath)
	if err != nil || allocation.MemoryGiB != 6 || allocation.CPUs != 3 {
		t.Fatalf("saved allocation %+v %v", allocation, err)
	}
	for filename, want := range map[string][]byte{s.configPath: network, filepath.Join(s.instanceDir(), "lima.yaml"): configuration} {
		got, err := os.ReadFile(filename)
		if err != nil || string(got) != string(want) {
			t.Fatalf("save changed unrelated file %s", filename)
		}
	}
	response = windowsBrowserRequestForTest(s, http.MethodGet, "/api/environment/resources", nil)
	var status struct {
		Runtime     string
		Policy      string `json:"memory_policy"`
		Minimum     int    `json:"min_memory_gib"`
		Memory      int    `json:"memory_gib"`
		Observation any
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &status) != nil || status.Runtime != "vz-macos" || status.Policy != "fixed" || status.Minimum != 4 || status.Memory != 6 || status.Observation != nil {
		t.Fatalf("status mismatch %s", response.Body.String())
	}
	s.op.Lock()
	response = windowsBrowserRequestForTest(s, http.MethodPost, "/api/environment/resources", []byte(`{"allocation":{"automatic":true}}`))
	s.op.Unlock()
	if response.Code != 409 {
		t.Fatal("resource save raced lifecycle operation")
	}
	if err = writePrivate(cage.ResourceAllocationPath(s.configPath), cage.ResourceAllocation{MemoryGiB: 2, CPUs: 1}); err != nil {
		t.Fatal(err)
	}
	s.networkPaused = true
	response = windowsBrowserRequestForTest(s, http.MethodPost, "/api/environment/resources", []byte(`{}`))
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	job := awaitJobIdle(t, &s.jobs)
	if !strings.Contains(job.Error, "至少需要") || !s.networkPaused {
		t.Fatalf("invalid saved limit passed restart validation: %+v", job)
	}
}

func TestMacOSResourceSamplesKeepUnknownCPUAndResetAcrossBoots(t *testing.T) {
	sample := macOSResourceSample{Version: 1, Total: 4 << 30, Available: 2 << 30, Free: 1 << 30, Inactive: 1 << 30, CPUs: 2, CPUTicks: [4]uint32{10, 10, 80, 0}, Boot: 10}
	data, _ := json.Marshal(sample)
	if _, err := parseMacOSResourceSample(data, 4<<30, 2); err != nil {
		t.Fatal(err)
	}
	observer := macOSResourceObserver{}
	now := time.Now()
	first := observer.observation(sample, now)
	if first["cpu_percent"].(*float64) != nil {
		t.Fatal("first CPU sample was fabricated")
	}
	if _, ok := first["pressure"]; ok {
		t.Fatal("unavailable pressure was reported as observed")
	}
	sample.CPUTicks = [4]uint32{20, 20, 100, 0}
	second := observer.observation(sample, now.Add(4*time.Second))
	if value := second["cpu_percent"].(*float64); value == nil || *value != 50 {
		t.Fatalf("CPU delta=%v", value)
	}
	sample.Boot++
	if observer.observation(sample, now.Add(8*time.Second))["cpu_percent"].(*float64) != nil {
		t.Fatal("CPU carried across guest restart")
	}
	sample.Boot--
	if observer.observation(sample, now.Add(30*time.Second))["cpu_percent"].(*float64) != nil {
		t.Fatal("CPU carried across stale observation")
	}
	for _, change := range []func(*macOSResourceSample){func(s *macOSResourceSample) { s.Total = 8 << 30 }, func(s *macOSResourceSample) { s.CPUs = 4 }, func(s *macOSResourceSample) { s.Available = 5 << 30 }, func(s *macOSResourceSample) { s.Inactive = 3 << 30 }, func(s *macOSResourceSample) { s.Boot = 0 }} {
		invalid := sample
		change(&invalid)
		data, _ = json.Marshal(invalid)
		if _, err := parseMacOSResourceSample(data, 4<<30, 2); err == nil {
			t.Fatal("inconsistent guest metrics accepted")
		}
	}
}

func TestMacOSResourcesReadThroughBoundSSHAndDiscardFailures(t *testing.T) {
	guest := newMacWorkspaceGuest(t)
	s := guest.s
	if err := os.WriteFile(filepath.Join(s.instanceDir(), "lima.yaml"), []byte("os: Darwin\nmemory: 4GiB\ncpus: 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	read := func() map[string]any {
		t.Helper()
		response := windowsBrowserRequestForTest(s, http.MethodGet, "/api/environment/resources", nil)
		var status map[string]any
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &status) != nil {
			t.Fatalf("resource HTTP response %d %s", response.Code, response.Body.String())
		}
		return status
	}
	first := read()
	if first["observation"].(map[string]any)["cpu_percent"] != nil {
		t.Fatal("first SSH sample fabricated CPU usage")
	}
	second := read()
	if second["observation"].(map[string]any)["cpu_percent"] != float64(50) {
		t.Fatal("bound SSH samples lost CPU delta")
	}
	guest.resourceFailure.Store(true)
	failed := read()
	if failed["observation"] != nil || failed["resource_error"] == "" {
		t.Fatal("failed observation exposed stale metrics or hid the error")
	}
}
