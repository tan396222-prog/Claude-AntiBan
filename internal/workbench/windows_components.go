package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/sylvinus/agent-vm/internal/guestos"
)

// Component polling uses only the paired GUI status operation. It neither
// queries Hyper-V nor starts a guest program, and one slow reply owns one slot.
func (s *Server) refreshWindowsComponents() {
	s.mu.Lock()
	binding := s.windowsGuest
	state := s.windowsState
	if binding == nil || s.closing || !s.ready || state.Version != 1 || state.OS != "windows" ||
		state.VMID != binding.VMID.String() || state.SID != binding.GUIUserSID ||
		state.ComponentsRefreshing || time.Since(state.ComponentsChecked) < 5*time.Second {
		s.mu.Unlock()
		return
	}
	s.windowsState.ComponentsRefreshing = true
	ctx, cancel := context.WithTimeout(s.lifetimeContextLocked(), 5*time.Second)
	s.mu.Unlock()
	go func() {
		defer cancel()
		report, err := readWindowsComponentStatus(ctx, s.profile)
		if err == nil && (report.Version != 1 || report.OS != "windows" || report.VMID != binding.VMID.String() ||
			report.SID != binding.GUIUserSID || report.Session == "" || len(report.Session) > 256) {
			err = errors.New("Windows component identity mismatch")
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		// A newer attach/reconnect owns its own status. An older reply cannot
		// overwrite its session or release a newer refresh slot.
		if s.closing || s.windowsGuest != binding || s.windowsState.Session != state.Session {
			return
		}
		s.windowsState.ComponentsRefreshing = false
		s.windowsState.ComponentsChecked = time.Now()
		if !s.ready {
			return
		}
		if err != nil {
			for name, component := range s.windowsState.Components {
				component.Ready, component.Reason = false, "agent_status_unavailable"
				s.windowsState.Components[name] = component
			}
			return
		}
		s.windowsState.Components = report.Components
		s.windowsState.Capabilities = report.Capabilities
		s.windowsState.Browser = report.Browser
		s.windowsState.Available, s.windowsState.Reason, s.windowsState.Session = report.Available, report.Reason, report.Session
	}()
}

func readWindowsComponentStatus(ctx context.Context, profile string) (windowsGuestState, error) {
	cmd, err := guestos.WindowsRelayCommand(ctx, profile, "status")
	if err != nil {
		return windowsGuestState{}, err
	}
	var output bytes.Buffer
	cmd.Stdout = &boundedWindowsStatusWriter{buffer: &output, remaining: 64 << 10}
	cmd.Stderr = io.Discard
	if err = cmd.Run(); err != nil {
		return windowsGuestState{}, err
	}
	var state windowsGuestState
	decoder := json.NewDecoder(&output)
	if decoder.Decode(&state) != nil || decoder.Decode(new(any)) != io.EOF {
		return windowsGuestState{}, errors.New("Windows component status invalid")
	}
	state.ComponentsChecked = time.Now()
	return state, nil
}
