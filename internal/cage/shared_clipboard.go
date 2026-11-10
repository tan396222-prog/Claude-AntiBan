package cage

import _ "embed"

//go:embed shared-clipboard.py
var SharedClipboardHelper string

func sharedClipboardScript() string {
	return "sudo tee /usr/local/bin/cage-shared-clipboard >/dev/null <<'CAGE_SHARED_CLIPBOARD'\n" + SharedClipboardHelper + "\nCAGE_SHARED_CLIPBOARD\nsudo chmod 0755 /usr/local/bin/cage-shared-clipboard\n"
}
