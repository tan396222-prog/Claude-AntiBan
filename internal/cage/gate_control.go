package cage

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sylvinus/agent-vm/internal/host"
)

const gateRecordName = "cage-gate.json"

type gateRecord struct {
	Socket string `json:"socket"`
	Token  string `json:"token"`
	RunID  string `json:"run_id"`
}

type gateCommand struct {
	Action   string        `json:"action"`
	Fault    bool          `json:"fault,omitempty"`
	Reason   string        `json:"reason,omitempty"`
	Report   *SafetyReport `json:"report,omitempty"`
	ReportID string        `json:"report_id,omitempty"`
}

// StartGateControl publishes an authenticated Unix endpoint in host-private
// storage. It creates no TCP listener or guest-visible control credential.
func StartGateControl(configPath, instanceDir string, gate *EgressGate) (func(), error) {
	dir, err := os.MkdirTemp(gateTempDir(), "cage-gate-")
	if err != nil {
		return nil, err
	}
	if err = host.EnsurePrivateDir(dir); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	socket := filepath.Join(dir, "control.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	if err = host.ProtectFile(socket); err != nil {
		listener.Close()
		os.RemoveAll(dir)
		return nil, err
	}
	var token [32]byte
	if _, err = rand.Read(token[:]); err != nil {
		listener.Close()
		os.RemoveAll(dir)
		return nil, err
	}
	record := gateRecord{Socket: socket, Token: hex.EncodeToString(token[:]), RunID: gate.Status().RunID}
	data, _ := json.Marshal(record)
	temporary, err := os.CreateTemp(instanceDir, ".gate-*")
	if err != nil {
		listener.Close()
		os.RemoveAll(dir)
		return nil, err
	}
	defer os.Remove(temporary.Name())
	if err = host.ProtectFile(temporary.Name()); err != nil {
		temporary.Close()
		listener.Close()
		os.RemoveAll(dir)
		return nil, err
	}
	_, err = temporary.Write(data)
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temporary.Name(), filepath.Join(instanceDir, gateRecordName))
	}
	if err != nil {
		listener.Close()
		os.RemoveAll(dir)
		return nil, err
	}
	server := &http.Server{ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 8192}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		if r.Host != "cage" || r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+record.Token)) != 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		var operationError error
		if r.Method == "POST" && r.URL.Path == "/control" {
			var command gateCommand
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&command) != nil || len(command.Reason) > 200 {
				w.WriteHeader(400)
				return
			}
			if command.Action == "check" || command.Action == "grant" || command.Action == "renew" || command.Action == "proxy-revision" {
				cfg, e := Load(configPath)
				if e == nil {
					e = cfg.RequireManagedProxy(filepath.Dir(configPath))
				}
				if e != nil {
					gate.Close("代理节点未应用，外网保持关闭")
					w.WriteHeader(409)
					_ = json.NewEncoder(w).Encode(map[string]string{"error": e.Error()})
					return
				}
			}
			switch command.Action {
			case "proxy-revision":
				cfg, e := Load(configPath)
				if e != nil {
					operationError = e
				} else {
					operationError = gate.SyncProxyRevision(cfg)
					if operationError == nil {
						operationError = cfg.RecordRunningAt(instanceDir)
					}
				}
			case "check":
				operationError = gate.BeginCheck()
			case "fault":
				operationError = gate.SetFault(command.Fault)
			case "grant":
				if command.Report == nil {
					operationError = errors.New("检测报告缺失")
				} else {
					operationError = gate.Grant(*command.Report)
				}
			case "renew":
				operationError = gate.Renew(command.ReportID)
			case "close":
				gate.Close(command.Reason)
			default:
				w.WriteHeader(400)
				return
			}
		} else if r.Method != "GET" || r.URL.Path != "/status" {
			w.WriteHeader(404)
			return
		}
		if operationError != nil {
			w.WriteHeader(409)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": operationError.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(gate.Status())
	})
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	stop := func() {
		once.Do(func() { cancel(); gate.Close("网络控制服务已停止"); _ = server.Close(); _ = os.RemoveAll(dir) })
	}
	go func() { _ = server.Serve(listener); stop() }()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cfg, err := Load(configPath)
				if err == nil {
					err = cfg.RequireManagedProxy(filepath.Dir(configPath))
				}
				if err != nil || cfg.Fingerprint() != gate.Status().Configuration {
					gate.Close("配置已变更或无法读取，请重启环境并重新检测")
				}
			}
		}
	}()
	return stop, nil
}

type GateClient struct{ record gateRecord }

func OpenGateClient(instanceDir string) (*GateClient, error) {
	path := filepath.Join(instanceDir, gateRecordName)
	if err := host.CheckPrivateFile(path); err != nil {
		return nil, errors.New("网络门槛控制记录不可用，请停止并重启环境")
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 4096 {
		return nil, errors.New("网络门槛控制记录不可读")
	}
	var record gateRecord
	if json.Unmarshal(data, &record) != nil || len(record.Token) != 64 || record.RunID == "" || !filepath.IsAbs(record.Socket) {
		return nil, errors.New("网络门槛控制记录无效")
	}
	return &GateClient{record: record}, nil
}

func (c *GateClient) request(ctx context.Context, command *gateCommand) (GateStatus, error) {
	var state GateStatus
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", c.record.Socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("gate redirect refused") }}
	method, path := "GET", "/status"
	var body io.Reader
	if command != nil {
		method, path = "POST", "/control"
		data, err := json.Marshal(command)
		if err != nil {
			return state, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://cage"+path, body)
	if err != nil {
		return state, err
	}
	req.Header.Set("Authorization", "Bearer "+c.record.Token)
	resp, err := client.Do(req)
	if err != nil {
		return state, errors.New("无法连接 VM 外网络门槛控制器")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		var message struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&message)
		if message.Error == "" {
			message.Error = "网络门槛操作被拒绝"
		}
		return state, errors.New(message.Error)
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 128<<10)).Decode(&state) != nil || state.RunID != c.record.RunID || state.Policy != SafetyPolicyVersion {
		return GateStatus{}, errors.New("网络门槛状态不属于当前运行实例")
	}
	return state, nil
}

func (c *GateClient) Status(ctx context.Context) (GateStatus, error) { return c.request(ctx, nil) }
func (c *GateClient) BeginCheck(ctx context.Context) (GateStatus, error) {
	return c.request(ctx, &gateCommand{Action: "check"})
}
func (c *GateClient) SetFault(ctx context.Context, fault bool) (GateStatus, error) {
	return c.request(ctx, &gateCommand{Action: "fault", Fault: fault})
}
func (c *GateClient) Grant(ctx context.Context, report SafetyReport) (GateStatus, error) {
	return c.request(ctx, &gateCommand{Action: "grant", Report: &report})
}
func (c *GateClient) Renew(ctx context.Context, reportID string) (GateStatus, error) {
	return c.request(ctx, &gateCommand{Action: "renew", ReportID: reportID})
}
func (c *GateClient) Close(ctx context.Context, reason string) (GateStatus, error) {
	return c.request(ctx, &gateCommand{Action: "close", Reason: reason})
}
func (c *GateClient) SyncProxyRevision(ctx context.Context) (GateStatus, error) {
	return c.request(ctx, &gateCommand{Action: "proxy-revision"})
}
