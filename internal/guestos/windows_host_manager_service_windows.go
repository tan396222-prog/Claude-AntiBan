//go:build windows

package guestos

import (
	"context"
	"errors"
	"golang.org/x/sys/windows/svc"
)

type windowsHostManagerHandler struct{ bindingPath string }

func (handler windowsHostManagerHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending, WaitHint: 30000}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	ready := make(chan struct{})
	go func() { done <- serveWindowsHostManager(ctx, handler.bindingPath, ready) }()
	select {
	case <-ready:
	case <-done:
		return true, 1
	}
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			if err != nil {
				return true, 1
			}
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				err := <-done
				if err != nil {
					return true, 1
				}
				return false, 0
			}
		}
	}
}

func RunWindowsHostManagerService(path string) error {
	service, err := svc.IsWindowsService()
	if err != nil || !service {
		return errors.New("manager_requires_scm")
	}
	binding, err := LoadWindowsHostManagerBinding(path)
	if err != nil {
		return err
	}
	return svc.Run(WindowsHostManagerServiceName(binding.VMID), windowsHostManagerHandler{bindingPath: path})
}
