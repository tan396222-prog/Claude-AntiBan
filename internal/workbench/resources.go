package workbench

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/docker/go-units"
	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/containerenv"
	"go.yaml.in/yaml/v3"
)

func (s *Server) instanceDir() string {
	if containerenv.Configured(s.profile) {
		return filepath.Join(s.profile, "state", "container", "environment")
	}
	return filepath.Join(s.profile, "state", "lima", cage.InstanceName(s.profile, s.environmentDir()))
}

func (s *Server) resourceStatus(w http.ResponseWriter, r *http.Request) {
	if containerenv.Configured(s.profile) {
		environment, err := containerenv.Load(s.profile)
		if err != nil {
			requestError(w, err)
			return
		}
		state, err := environment.State(r.Context())
		if err != nil {
			requestError(w, err)
			return
		}
		config := environment.Config()
		jsonReply(w, map[string]any{"running": state.Running, "automatic": false, "runtime": "container",
			"recommended_memory_bytes": config.MemoryBytes, "recommended_cpus": config.CPUs,
			"configured_memory_bytes": state.MemoryBytes, "configured_cpus": state.CPUs,
			"resources_verified": state.ResourcesVerified, "resource_error": state.ResourceError, "pids_limit": state.PIDs,
			"restart_required": false, "observation": nil})
		return
	}
	memory, cpus := cage.ResourceLimits()
	running := s.vmRunning()
	var configured struct {
		Memory string `yaml:"memory"`
		CPUs   int    `yaml:"cpus"`
	}
	if b, err := os.ReadFile(filepath.Join(s.instanceDir(), "lima.yaml")); err == nil {
		_ = yaml.Unmarshal(b, &configured)
	}
	maximum, _ := units.RAMInBytes(configured.Memory)
	var observation *cage.ResourceStatus
	status, err := cage.ReadResourceStatus(s.instanceDir())
	if running && err == nil {
		observation = &status
	}
	jsonReply(w, map[string]any{
		"running": running, "automatic": true,
		"recommended_memory_bytes": uint64(memory) << 30, "recommended_cpus": cpus,
		"configured_memory_bytes": maximum, "configured_cpus": configured.CPUs,
		"restart_required": running && (maximum != int64(memory)<<30 || configured.CPUs != cpus || observation == nil),
		"observation":      observation,
	})
}

func (s *Server) applyResources(w http.ResponseWriter, r *http.Request) {
	s.launch(w, "resources", func() error {
		if s.vmRunning() {
			if err := s.stopEnvironment(); err != nil {
				return err
			}
		}
		if err := s.operationContext().Err(); err != nil {
			return err
		}
		return s.startEnvironment(false)
	})
}

func (s *Server) checkNetworkIfWanted() error {
	s.mu.Lock()
	paused := s.networkPaused
	s.mu.Unlock()
	if paused {
		return nil
	}
	return s.runSafetyCheck()
}
