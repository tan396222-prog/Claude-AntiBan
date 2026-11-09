package workbench

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type networkIntent struct {
	Version      int    `json:"version"`
	Paused       bool   `json:"paused"`
	PauseVersion uint64 `json:"pause_version"`
}

// An absent record can belong to an older profile whose pause was memory-only.
// Keep it offline until an explicit user operation establishes a durable intent.
func (s *Server) restoreNetworkIntent() error {
	paused, generation := true, uint64(0)
	data, err := os.ReadFile(filepath.Join(s.profile, "network-intent.json"))
	if err == nil {
		var record struct {
			Version      int     `json:"version"`
			Paused       *bool   `json:"paused"`
			PauseVersion *uint64 `json:"pause_version"`
		}
		if json.Unmarshal(data, &record) != nil || record.Version != 1 || record.Paused == nil || record.PauseVersion == nil {
			err = errors.New("保存的网络暂停状态无效")
		} else {
			paused, generation = *record.Paused, *record.PauseVersion
		}
	}
	s.mu.Lock()
	s.networkPaused, s.safety.pauseVersion = paused, generation
	s.mu.Unlock()
	if err != nil && !os.IsNotExist(err) {
		return errors.New("无法读取网络暂停状态，外网保持关闭；请确认后手动恢复。")
	}
	return nil
}

// Caller holds mu so a later pause cannot be overwritten by a pending resume.
func (s *Server) saveNetworkIntentLocked(paused bool) error {
	if err := writePrivate(filepath.Join(s.profile, "network-intent.json"), networkIntent{Version: 1, Paused: paused, PauseVersion: s.safety.pauseVersion}); err != nil {
		s.networkPaused = true
		return fmt.Errorf("无法保存网络暂停状态，当前外网保持关闭；请解决配置写入问题后重试：%w", err)
	}
	s.networkPaused = paused
	return nil
}

func (s *Server) resumeNetworkIntentAt(generation uint64) error {
	s.mu.Lock()
	if generation != s.safety.pauseVersion {
		s.mu.Unlock()
		return nil
	}
	err := s.saveNetworkIntentLocked(false)
	s.mu.Unlock()
	if err != nil {
		_ = s.closeProxyGate("网络恢复意图无法保存，外网保持关闭")
	}
	return err
}
