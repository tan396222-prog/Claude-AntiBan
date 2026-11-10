package main

import (
	"github.com/sylvinus/agent-vm/internal/agentcli"
	"io"
)

func runAgent(args []string, stdin io.Reader, stdout io.Writer) int {
	return agentcli.Main(args, stdin, stdout)
}
