package workbench

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sylvinus/agent-vm/internal/guestos"
)

func (s *Server) windowsAudioStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	cmd, err := guestos.WindowsRelayCommand(ctx, s.profile, "audio")
	if err != nil {
		http.Error(w, "Windows 客机声音组件尚未连接", 502)
		return
	}
	cmd.Stdin = strings.NewReader("{\"action\":\"status\"}\n")
	cmd.Stderr = io.Discard
	var data bytes.Buffer
	cmd.Stdout = &boundedWindowsStatusWriter{buffer: &data, remaining: 4096}
	var status map[string]any
	if cmd.Run() != nil || json.Unmarshal(data.Bytes(), &status) != nil || status["supported"] != true {
		http.Error(w, "Windows 客机声音组件尚未就绪", 502)
		return
	}
	jsonReply(w, status)
}

func (s *Server) windowsAudioCapture(w http.ResponseWriter, r *http.Request, ctx context.Context, cancel context.CancelFunc) {
	cmd, err := guestos.WindowsRelayCommand(ctx, s.profile, "audio")
	if err != nil {
		http.Error(w, "Windows 客机声音组件尚未连接", 502)
		return
	}
	cmd.Stdin = strings.NewReader("{\"action\":\"capture\"}\n")
	cmd.Stderr = io.Discard
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		http.Error(w, "声音连接未能建立", 502)
		return
	}
	if err = cmd.Start(); err != nil {
		pipe.Close()
		http.Error(w, "声音连接未能建立", 502)
		return
	}
	defer func() { cancel(); pipe.Close(); _ = cmd.Wait() }()
	deadlineDone := make(chan struct{})
	deadline := time.AfterFunc(15*time.Second, func() { defer close(deadlineDone); cancel(); pipe.Close() })
	reader := bufio.NewReaderSize(pipe, 4096)
	err = readAudioReady(reader)
	stopped := cancelWindowsAdmissionDeadline(deadline, deadlineDone)
	if err != nil || !stopped {
		http.Error(w, "Windows 声音输出未能启动，请确认客机已解锁并有可用的声音输出", 502)
		return
	}
	s.streamAudio(w, r.WithContext(ctx), bufferedAudio{Reader: reader, Closer: pipe})
}
