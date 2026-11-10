//go:build !windows

package main

import "os/exec"

func configureRelayCommand(cmd *exec.Cmd) {}
