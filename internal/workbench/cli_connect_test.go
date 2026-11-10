package workbench

import "testing"

func TestCLIStartupDialogs(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"\x1b[31mWARNING: Loading development channels\x1b[0m\r\nChannels: server:cage\r\nI am using this for local development", "channel"},
		{"Quick safety check: is this a project you created or one you trust?\r\n/work/project\r\nYes, I trust this folder", "project"},
		{"Quick safety check: is this a project you created or one you trust? /other/project Yes, I trust this folder", ""},
		{"WARNING: Loading development channels Channels: server:other I am using this for local development", ""},
		{"WARNING: Loading development channels Channels: server:cage-extra 1. I am using this for local development", ""},
		{"WARNING: Loading development channels Channels: server:cage,server:other 1. I am using this for local development", ""},
		{"WARNING: Loading development channels Channels: server:cage ❯ 1. I am using this for local development", "channel"},
		{"Do you want to proceed? 1. Yes 2. No", ""},
		{"New MCP server found in this project: other Use this MCP server", ""},
		{"Do you want to use this API key?", ""},
	} {
		if got := cliStartupDialog(cliStartupText([]byte(tc.text)), "/work/project"); got != tc.want {
			t.Errorf("dialog %q: %q, want %q", tc.text, got, tc.want)
		}
	}
}
