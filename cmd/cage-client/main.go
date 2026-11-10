// cage-client contains the desktop connection service without Lima, virtual
// machine assets or a local workload launcher. Native shells use the same
// desktop/file-command contract as the full application.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/sylvinus/agent-vm/internal/agentcli"
	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/diagnostics"
	"github.com/sylvinus/agent-vm/internal/host"
	"github.com/sylvinus/agent-vm/internal/version"
	"github.com/sylvinus/agent-vm/internal/workbench"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "agent" {
		os.Exit(agentcli.ClientMain(os.Args[2:], os.Stdin, os.Stdout))
	}
	if err := run(os.Args[1:]); err != nil {
		diagnostics.Log("error", "client", err.Error())
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "error", "message": err.Error()})
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Println("Cage remote client " + version.Version + "\nCommands: desktop --json, serve, prepare-update. Workloads run on the selected remote host.")
		return nil
	}
	if args[0] == "verify-web-assets" {
		if len(args) != 1 {
			return errors.New("verify-web-assets does not accept arguments")
		}
		return workbench.VerifyWebAssets()
	}
	config := os.Getenv(cage.ConfigEnv)
	if config == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		config = filepath.Join(home, ".claude-cage", "client", "config.json")
	}
	config, err := filepath.Abs(config)
	if err != nil {
		return err
	}
	diagnostics.SetDefault(diagnostics.New(filepath.Dir(config)))
	printJSON := func(value any, err error) error {
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(value)
	}
	switch args[0] {
	case "serve":
		return workbench.ServeClient(config)
	case "log-relay":
		if len(args) != 2 || args[1] != "controller" {
			return errors.New("unsupported client service")
		}
		return relay(config)
	case "desktop":
		options := workbench.DesktopOptions{}
		for _, argument := range args[1:] {
			switch argument {
			case "--json":
			case "--existing":
				options.UseExisting = true
			case "--complete-update":
				options.CompleteUpdate = true
			default:
				return errors.New("unsupported desktop option")
			}
		}
		return printJSON(workbench.Desktop(config, options))
	case "prepare-update":
		stop := false
		for _, argument := range args[1:] {
			switch argument {
			case "--json":
			case "--stop":
				stop = true
			default:
				return errors.New("unsupported update option")
			}
		}
		return printJSON(workbench.PrepareUpdate(config, stop))
	case "diagnostic-event":
		var event struct {
			Source  string `json:"source"`
			Message string `json:"message"`
		}
		if json.NewDecoder(io.LimitReader(os.Stdin, 32<<10)).Decode(&event) != nil || event.Source != "native" {
			return errors.New("invalid diagnostic event")
		}
		return diagnostics.New(filepath.Dir(config)).Record("error", event.Source, event.Message)
	case "diagnostics":
		connection := ""
		if len(args) == 5 && args[1] == "--json" && args[2] == "--connection" {
			connection = args[3]
			args = []string{args[0], args[1], args[4]}
		}
		if len(args) != 3 || args[1] != "--json" {
			return errors.New("diagnostics --json /absolute/path.zip")
		}
		if err := workbench.ExportDiagnosticsForConnection(config, connection, args[2]); err != nil {
			return err
		}
		return printJSON(map[string]string{"status": "ok"}, nil)
	case "share-project":
		if len(args) != 6 || args[1] != "--json" || args[2] != "--connection" || args[3] == "" || (args[4] != "read" && args[4] != "write") {
			return errors.New("share-project --json --connection ID read|write DIRECTORY")
		}
		return printJSON(workbench.ShareNativeCloudProject(config, args[3], args[5], args[4] == "read"))
	case "desktop-file":
		if len(args) != 5 || args[1] != "--json" || args[2] != "--connection" || args[3] == "" {
			return errors.New("desktop-file --json --connection ID REQUEST")
		}
		return printJSON(workbench.DesktopFileForConnection(config, args[3], args[4]))
	case "clipboard-files", "upload-files", "browser-file":
		if len(args) < 4 || args[1] != "--json" {
			return errors.New("invalid file command")
		}
		connection := ""
		if args[2] == "--connection" {
			if len(args) < 6 || args[3] == "" {
				return errors.New("missing connection identity")
			}
			connection = args[3]
			args = append(append([]string{}, args[:2]...), args[4:]...)
		}
		if args[0] == "clipboard-files" {
			if len(args) < 4 {
				return errors.New("clipboard-files --json --connection ID send|receive PATH...")
			}
			return printJSON(workbench.ClipboardFilesForConnection(config, connection, args[2], args[3:]))
		}
		if args[0] == "upload-files" {
			if len(args) < 5 {
				return errors.New("no selected files")
			}
			return printJSON(workbench.UploadHostFilesForConnection(config, connection, args[2], args[3], args[4:]))
		}
		if len(args) < 4 || len(args) > 5 {
			return errors.New("invalid download command")
		}
		destination := ""
		if len(args) == 5 {
			destination = args[4]
		}
		return printJSON(workbench.BrowserFileForConnection(config, connection, args[2], args[3], destination))
	case "app-update":
		return printJSON(map[string]any{"phase": "manual", "currentVersion": version.Version, "message": "轻量客户端使用独立安装包更新；连接配置会保留，云端任务不受影响。"}, nil)
	default:
		return errors.New("轻量客户端只负责连接；请在远程工作台管理运行环境")
	}
}

func relay(config string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(executable, "serve")
	cmd.Env = append(os.Environ(), cage.ConfigEnv+"="+config)
	store := diagnostics.New(filepath.Dir(config))
	out, stderr := store.Stream("client", "info"), store.Stream("client", "error")
	defer out.Close()
	defer stderr.Close()
	cmd.Stdout, cmd.Stderr = out, stderr
	host.HideCommandWindow(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case sig := <-signals:
			_ = cmd.Process.Signal(sig)
		case <-done:
		}
	}()
	return cmd.Wait()
}
