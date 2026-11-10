package workbench

import _ "embed"

//go:embed mobile_cli_identity.py
var mobileCLIIdentity string

// Runs after the guest login shell has established the CLI's environment.
func mobileIdentityCommand(prefix, args []string) []string {
	if len(prefix) == 5 && prefix[0] == "env" && prefix[2] == "/usr/local/bin/cage-claude-account" && prefix[3] == "run" {
		command := []string{prefix[0], prefix[1], "python3", "-I", "-c", mobileCLIIdentity, "official", prefix[2], prefix[4]}
		return append(command, args...)
	}
	if len(prefix) == 1 && prefix[0] == "claude" || len(prefix) == 2 && prefix[0] == "ccs" {
		command := []string{"python3", "-I", "-c", mobileCLIIdentity, prefix[0]}
		if prefix[0] == "ccs" {
			command = append(command, prefix[1])
		}
		return append(command, args...)
	}
	return append(append([]string(nil), prefix...), args...)
}
