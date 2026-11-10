package main

import (
	"context"
	"encoding/json"
	"github.com/sylvinus/agent-vm/internal/cage"
	"github.com/sylvinus/agent-vm/internal/guestos"
	"github.com/sylvinus/agent-vm/internal/managedproxy"
	"os"
	"path/filepath"
)

func proxyCommand(command, profile string, cfg cage.Config) error {
	if command == "proxy-stop" {
		if err := closeManagedGates(profile); err != nil {
			return err
		}
		return managedproxy.Stop(profile)
	}
	p, err := managedproxy.Applied(profile, cfg.ProxyURL, cfg.ProxyRevision)
	if err != nil {
		return err
	}
	name, err := managedproxy.PhysicalInterface(context.Background())
	if err != nil {
		_ = closeManagedGates(profile)
		return err
	}
	bound, _ := managedproxy.BoundInterface(filepath.Join(managedproxy.Dir(profile), "config.yaml"))
	if bound == name && managedproxy.Running(context.Background(), profile, p) {
		return nil
	}
	if err = closeManagedGates(profile); err != nil {
		return err
	}
	p.Revision = managedproxy.Token()
	cfg.ProxyRevision = p.Revision
	if err = p.Save(profile); err != nil {
		return err
	}
	path := os.Getenv(cage.ConfigEnv)
	if path == "" {
		path = filepath.Join(profile, "config.json")
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err = managedproxy.Write(path, data); err != nil {
		return err
	}
	files, err := managedGateFiles(profile)
	if err != nil {
		return err
	}
	for _, file := range files {
		if client, e := cage.OpenGateClient(filepath.Dir(file)); e == nil {
			if _, e = client.Status(context.Background()); e == nil {
				if _, e = client.SyncProxyRevision(context.Background()); e != nil {
					return e
				}
			}
		}
	}
	return managedproxy.Start(context.Background(), profile, p)
}

func closeManagedGates(profile string) error {
	files, err := managedGateFiles(profile)
	if err != nil {
		return err
	}
	for _, file := range files {
		if client, e := cage.OpenGateClient(filepath.Dir(file)); e == nil {
			if _, e = client.Status(context.Background()); e == nil {
				if _, e = client.Close(context.Background(), "代理核心停止或重启，等待重新检测"); e != nil {
					return e
				}
			}
		}
	}
	return nil
}

func managedGateFiles(profile string) ([]string, error) {
	files, _ := filepath.Glob(filepath.Join(profile, "state", "lima", "*", "cage-gate.json"))
	containers, _ := filepath.Glob(filepath.Join(profile, "state", "container", "*", "cage-gate.json"))
	files = append(files, containers...)
	binding, err := guestos.LoadWindowsProfile(profile)
	if err != nil {
		return nil, err
	}
	if binding != nil {
		files = append(files, filepath.Join(binding.InstanceDirectory, "cage-gate.json"))
	}
	return files, nil
}
