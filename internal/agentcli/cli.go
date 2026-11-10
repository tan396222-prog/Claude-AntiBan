package agentcli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/guestos"
	"github.com/sylvinus/agent-vm/internal/host"
	"github.com/sylvinus/agent-vm/internal/version"
	"github.com/sylvinus/agent-vm/internal/workbench"
)

type agentReply struct {
	Protocol int                   `json:"protocol"`
	Version  string                `json:"version"`
	Profile  string                `json:"config_path,omitempty"`
	Status   string                `json:"status"`
	Data     any                   `json:"data,omitempty"`
	Error    *workbench.AgentError `json:"error,omitempty"`
}

func Main(args []string, stdin io.Reader, stdout io.Writer) int {
	return runAgent(args, stdin, stdout, false)
}
func ClientMain(args []string, stdin io.Reader, stdout io.Writer) int {
	return runAgent(args, stdin, stdout, true)
}
func runAgent(args []string, stdin io.Reader, stdout io.Writer, clientOnly bool) int {
	reply := agentReply{Protocol: workbench.AgentProtocol, Version: version.Version, Status: "completed"}
	finish := func(data any, err error) int {
		reply.Data = data
		code := 0
		if err != nil {
			reply.Status = "error"
			code = 1
			var ae *workbench.AgentError
			if errors.As(err, &ae) {
				reply.Error = ae
			} else {
				reply.Error = &workbench.AgentError{Code: "operation_failed", Message: "Cage could not complete the operation. Inspect diagnostics.", NextAction: "doctor"}
			}
			if reply.Error.Code == "invalid_input" || reply.Error.Code == "unknown_operation" {
				code = 2
			}
			if reply.Error.Code == "account_locked" || reply.Error.Code == "user_action_required" {
				code = 3
			}
			if reply.Error.Code == "wait_timeout" {
				code = 4
			}
		}
		if json.NewEncoder(stdout).Encode(reply) != nil {
			return 1
		}
		return code
	}
	bad := func(message string) int {
		return finish(nil, &workbench.AgentError{Code: "invalid_input", Message: message, NextAction: "capabilities"})
	}
	command := "capabilities"
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}
	operand := ""
	if command == "call" || command == "job" || command == "request" || command == "socket" {
		if len(args) == 0 {
			return bad("call needs NAME; job needs ID; request needs METHOD /api/PATH; socket needs /api/PATH.")
		}
		operand, args = args[0], args[1:]
	}
	requestPath := ""
	if command == "request" {
		if len(args) == 0 {
			return bad("request needs METHOD /api/PATH")
		}
		requestPath, args = args[0], args[1:]
	}
	flags := flag.NewFlagSet("cage agent", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	config := flags.String("config", os.Getenv(cage.ConfigEnv), "")
	input := flags.String("input", "", "")
	output := flags.String("output", "", "")
	wait := flags.Duration("wait", 0, "")
	defaultScope := "local"
	if clientOnly {
		defaultScope = "client"
	}
	scope := flags.String("scope", defaultScope, "")
	connection := flags.String("connection", "", "")
	contentType := flags.String("content-type", "application/json", "")
	_ = flags.Bool("json", true, "")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return bad("Usage: cage agent capabilities|doctor|init|service.start|host.inspect|call NAME|request METHOD /api/PATH|socket /api/PATH|routes|job ID|diagnostics [--config PATH] [--input -|FILE] [--wait 5m] [--output PATH]")
	}
	if *wait < 0 || *wait > 24*time.Hour {
		return bad("--wait must be between 0 and 24h.")
	}
	if *scope != "local" && *scope != "client" && *scope != "desktop" && *scope != "selected" {
		return bad("scope must be local, client, desktop or selected")
	}
	if command != "call" && command != "request" && command != "socket" && command != "job" && command != "routes" && command != "doctor" && command != "diagnostics" && (*scope != defaultScope || *connection != "") {
		return bad("scope and connection apply to call, request, socket, job, routes, doctor and diagnostics")
	}
	if *input != "" && command != "call" && command != "request" {
		return bad("--input is valid only for call and request.")
	}
	if *output != "" && command != "diagnostics" && command != "request" {
		return bad("--output is valid only for diagnostics and request.")
	}
	if *wait != 0 && command != "call" && command != "job" {
		return bad("--wait is valid only for call and job.")
	}
	if *config == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return finish(nil, err)
		}
		*config = filepath.Join(home, ".claude-cage", "config.json")
		if clientOnly {
			*config = filepath.Join(home, ".claude-cage", "client", "config.json")
		}
	}
	path, err := filepath.Abs(*config)
	if err != nil {
		return finish(nil, err)
	}
	reply.Profile = path
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	switch command {
	case "routes":
		response, err := workbench.AgentRequest(ctx, path, workbench.AgentTarget{Scope: *scope, Connection: *connection}, "GET", "/api/agent/routes", nil, "", "")
		return finish(response.Data, err)
	case "help", "--help", "capabilities":
		return finish(map[string]any{"platform": runtime.GOOS, "arch": runtime.GOARCH, "commands": []string{"capabilities", "doctor", "init", "service.start", "host.inspect", "call NAME", "request METHOD /api/PATH", "socket /api/PATH", "routes", "job ID", "diagnostics"}, "operations": workbench.AgentOperations(), "routes": "agent routes reads the actual registered routes from the chosen live listener", "exit_codes": map[string]int{"completed_or_accepted": 0, "operation_failed": 1, "invalid_input": 2, "user_action_required": 3, "wait_timeout": 4}, "guide": "https://lee-xeo.github.io/Claude-Cage/docs/agent/", "scope": "local: owning workload; desktop: client configuration; selected: explicit desktop connection generation; client: lightweight client listener", "request": map[string]any{"methods": []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"}, "path": "relative /api/ endpoint from the UI", "binary": "--input FILE --content-type application/octet-stream; --output ABSOLUTE_NEW_FILE", "native": "call native.submit --scope desktop; request GET /api/agent/native/ID --scope desktop; use the same native action and connection_id as the open window"}, "job_retention": "only the latest job; query promptly and never resubmit an unknown mutation automatically"}, nil)
	case "init":
		if _, err := os.Stat(path); err == nil {
			if _, err := cage.Load(path); err != nil {
				return finish(nil, &workbench.AgentError{Code: "config_invalid", Message: "Existing configuration is invalid. Preserve it and export diagnostics.", NextAction: "diagnostics"})
			}
			return finish(map[string]bool{"created": false}, nil)
		} else if !os.IsNotExist(err) {
			return finish(nil, err)
		}
		if err := host.EnsurePrivateDir(filepath.Dir(path)); err != nil {
			return finish(nil, err)
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return finish(nil, err)
		}
		err = host.ProtectFile(path)
		if err == nil {
			err = json.NewEncoder(file).Encode(cage.DefaultConfig())
		}
		if e := file.Close(); err == nil {
			err = e
		}
		return finish(map[string]bool{"created": err == nil}, err)
	case "service.start":
		if clientOnly {
			return finish(nil, &workbench.AgentError{Code: "user_action_required", Message: "Open the installed Cage client to start its connection service", NextAction: "open_desktop"})
		}
		if err := host.ConfigureWindowsRuntime(); err != nil {
			return finish(nil, err)
		}
		startup, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := workbench.AgentStartService(startup, path); err != nil {
			return finish(nil, err)
		}
		return finish(map[string]bool{"service_ready": true}, nil)
	case "host.inspect":
		probe, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		return finish(guestos.Inspect(probe), nil)
	case "doctor":
		if *scope != "local" {
			endpoint := "/api/client/context"
			if *scope == "selected" {
				endpoint = "/api/status"
			}
			response, err := workbench.AgentRequest(ctx, path, workbench.AgentTarget{Scope: *scope, Connection: *connection}, "GET", endpoint, nil, "", "")
			return finish(map[string]any{"scope": *scope, "snapshot": response.Data, "note": "Read-only snapshot of the explicit target. No repairs were applied."}, err)
		}
		return finish(agentDoctor(ctx, path), nil)
	case "diagnostics":
		if !filepath.IsAbs(*output) {
			return bad("diagnostics requires --output with an absolute local ZIP path.")
		}
		if _, err := os.Lstat(*output); !os.IsNotExist(err) {
			return bad("Choose a new output path; existing files are preserved.")
		}
		if *scope != "local" {
			result, err := workbench.AgentRequest(ctx, path, workbench.AgentTarget{Scope: *scope, Connection: *connection}, "GET", "/api/diagnostics/export", nil, "", *output)
			return finish(result, err)
		}
		if err := workbench.ExportAgentDiagnostics(path, *output); err != nil {
			return finish(nil, err)
		}
		return finish(map[string]string{"path": *output}, nil)
	case "socket":
		if *input != "" || *output != "" || *wait != 0 {
			return bad("socket uses NDJSON stdin/stdout and no input/output/wait flags")
		}
		inputStream, ok := stdin.(io.ReadCloser)
		if !ok {
			inputStream = io.NopCloser(stdin)
		}
		err := workbench.AgentSocket(ctx, path, workbench.AgentTarget{Scope: *scope, Connection: *connection}, operand, inputStream, stdout)
		return finish(nil, err)
	case "request":
		var reader io.Reader
		if *input != "" {
			reader = stdin
			if *input != "-" {
				f, err := os.Open(*input)
				if err != nil {
					return finish(nil, err)
				}
				defer f.Close()
				reader = f
			}
		}
		if reader == nil && operand != "GET" && operand != "HEAD" {
			reader = strings.NewReader("{}")
		}
		if *output != "" {
			if !filepath.IsAbs(*output) {
				return bad("output requires an absolute new file path")
			}
			if _, err := os.Lstat(*output); !os.IsNotExist(err) {
				return bad("existing output files are preserved")
			}
		}
		result, err := workbench.AgentRequest(ctx, path, workbench.AgentTarget{Scope: *scope, Connection: *connection}, operand, requestPath, reader, *contentType, *output)
		var shape struct {
			ID      string `json:"id"`
			Running bool   `json:"running"`
		}
		_ = json.Unmarshal(result.Data, &shape)
		if result.HTTPStatus == 202 || (shape.ID != "" && shape.Running) {
			reply.Status = "accepted"
		}
		return finish(result, err)
	case "call", "job":
		var data json.RawMessage
		if command == "call" {
			body := []byte("{}")
			if *input != "" {
				reader := stdin
				if *input != "-" {
					f, e := os.Open(*input)
					if e != nil {
						return finish(nil, e)
					}
					defer f.Close()
					reader = f
				}
				body, err = io.ReadAll(io.LimitReader(reader, (12<<20)+1))
				if err != nil {
					return finish(nil, err)
				}
			}
			var object map[string]json.RawMessage
			if len(body) > 12<<20 || json.Unmarshal(body, &object) != nil || object == nil {
				return bad("Input must be one JSON object of at most 12 MiB.")
			}
			for _, op := range workbench.AgentOperations() {
				if op.Name == operand && !op.Mutates && len(object) > 0 {
					return bad("Read operations accept no input fields.")
				}
			}
			data, err = workbench.AgentCallTarget(ctx, path, workbench.AgentTarget{Scope: *scope, Connection: *connection}, operand, body)
		} else {
			result, e := workbench.AgentRequest(ctx, path, workbench.AgentTarget{Scope: *scope, Connection: *connection}, "GET", "/api/agent/jobs/"+operand, nil, "", "")
			data, err = result.Data, e
		}
		if err != nil {
			return finish(nil, err)
		}

		var job workbench.Job
		_ = json.Unmarshal(data, &job)
		var shape map[string]json.RawMessage
		_ = json.Unmarshal(data, &shape)
		if _, isJob := shape["running"]; job.ID == "" || !isJob {
			var native struct {
				Status string `json:"status"`
				Error  string `json:"error"`
			}
			_ = json.Unmarshal(data, &native)
			if native.Status == "pending" || native.Status == "running" {
				reply.Status = "accepted"
			}
			if native.Status == "failed" {
				return finish(data, &workbench.AgentError{Code: "native_failed", Message: native.Error, NextAction: "status"})
			}
			return finish(data, nil)
		}
		if *wait > 0 && job.Running {
			data, err = agentWaitTarget(ctx, path, workbench.AgentTarget{Scope: *scope, Connection: *connection}, job.ID, *wait)
			if err != nil {
				return finish(data, err)
			}
			_ = json.Unmarshal(data, &job)
		}
		if job.Running {
			reply.Status = "accepted"
			return finish(data, nil)
		}
		if job.Canceled {
			return finish(data, &workbench.AgentError{Code: "job_canceled", Message: "The original job was canceled. Inspect status before retrying.", NextAction: "status"})
		}
		if job.Error != "" {
			return finish(data, &workbench.AgentError{Code: "job_failed", Message: job.Error, NextAction: "doctor"})
		}
		return finish(data, nil)
	default:
		return bad("Unknown agent command. Run cage agent capabilities.")
	}
}

func agentWait(parent context.Context, path, id string, duration time.Duration) (json.RawMessage, error) {
	return agentWaitTarget(parent, path, workbench.AgentTarget{}, id, duration)
}

func agentWaitTarget(parent context.Context, path string, target workbench.AgentTarget, id string, duration time.Duration) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(parent, duration)
	defer cancel()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	var latest json.RawMessage
	for {
		select {
		case <-ctx.Done():
			return latest, &workbench.AgentError{Code: "wait_timeout", Message: "Waiting ended; the job may still be running. Query the same job id.", NextAction: "job " + id, Retryable: true}
		case <-tick.C:
			response, err := workbench.AgentRequest(ctx, path, target, "GET", "/api/agent/jobs/"+id, nil, "", "")
			data := response.Data
			if ctx.Err() != nil {
				continue
			}
			if err != nil {
				return latest, err
			}
			latest = data
			var job workbench.Job
			if json.Unmarshal(data, &job) != nil || job.ID != id {
				return data, &workbench.AgentError{Code: "job_not_found", Message: "Original job state is unavailable. Inspect status before retrying.", NextAction: "status"}
			}
			if !job.Running {
				return data, nil
			}
		}
	}
}

func agentDoctor(ctx context.Context, path string) map[string]any {
	result := map[string]any{"note": "Read-only snapshot. No repairs were applied; missing services were not started."}
	checks := []map[string]any{}
	add := func(code, status, next string) {
		checks = append(checks, map[string]any{"code": code, "status": status, "next_action": next})
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		add("config_missing", "blocked", "init")
	} else if _, err := cage.Load(path); err != nil {
		add("config_invalid", "blocked", "diagnostics")
	} else {
		add("config_valid", "ok", "")
	}
	for _, name := range []string{"account.json", "proxy/sources.json"} {
		if err := host.CheckPrivateFile(filepath.Join(filepath.Dir(path), filepath.FromSlash(name))); err != nil && !os.IsNotExist(err) {
			add("private_file_permissions", "blocked", "repair_profile_permissions")
		}
	}
	data, err := workbench.AgentCall(ctx, path, "account.status", nil)
	if err != nil {
		var ae *workbench.AgentError
		if errors.As(err, &ae) {
			add(ae.Code, "blocked", ae.NextAction)
		} else {
			add("service_unavailable", "blocked", "service.start")
		}
	} else {
		var account struct {
			Exists   bool `json:"exists"`
			Unlocked bool `json:"unlocked"`
		}
		_ = json.Unmarshal(data, &account)
		if !account.Exists {
			add("account_missing", "blocked", "account.create")
		} else if !account.Unlocked {
			add("account_locked", "blocked", "account.unlock")
		} else {
			add("account_unlocked", "ok", "")
			if proxy, e := workbench.AgentCall(ctx, path, "proxy.status", nil); e == nil {
				result["proxy"] = proxy
				var view struct {
					Active  bool  `json:"active"`
					Running bool  `json:"running"`
					Sources []any `json:"sources"`
				}
				_ = json.Unmarshal(proxy, &view)
				switch {
				case len(view.Sources) == 0:
					add("proxy_missing", "blocked", "proxy.import")
				case !view.Active:
					add("proxy_unapplied", "blocked", "proxy.select")
				case !view.Running:
					add("proxy_stopped", "blocked", "proxy.restart")
				default:
					add("proxy_running", "ok", "")
				}
			} else {
				add("proxy_status_unavailable", "blocked", "diagnostics")
			}
			if state, e := workbench.AgentCall(ctx, path, "status", nil); e == nil {
				result["environment"] = state
				var view struct {
					Ready        bool          `json:"ready"`
					Running      bool          `json:"vm_running"`
					Update       bool          `json:"environment_update_required"`
					Job          workbench.Job `json:"job"`
					NetworkError string        `json:"network_error"`
				}
				_ = json.Unmarshal(state, &view)
				switch {
				case view.Job.Running:
					add("job_running", "waiting", "job "+view.Job.ID)
				case view.Job.Error != "":
					add("job_failed", "blocked", "diagnostics")
				case view.Update:
					add("environment_update_required", "blocked", "environment.update")
				case !view.Running || !view.Ready:
					add("environment_not_ready", "blocked", "environment.start")
				case view.NetworkError != "":
					add("network_not_ready", "blocked", "inspect_network_policy")
				default:
					add("environment_ready", "ok", "")
				}
			} else {
				add("environment_status_unavailable", "blocked", "diagnostics")
			}
		}
	}
	result["checks"] = checks
	return result
}
