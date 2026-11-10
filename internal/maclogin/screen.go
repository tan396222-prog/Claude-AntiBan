package maclogin

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// The trusted guest probe supplies the console owner and bound account UID
// before Apple's unmodified IOConsoleUsers array. Accept exactly one active,
// completed, explicitly locked session for that same user.
func permittedLoginScreen(data []byte, user string) string {
	if bytes.Equal(data, []byte("system-login")) {
		return "system-login"
	}
	header, payload, ok := bytes.Cut(data, []byte("\n"))
	fields := strings.Fields(string(header))
	if !ok || len(fields) != 3 || fields[1] != user || fields[0] != fields[2] {
		return ""
	}
	uid, err := strconv.ParseUint(fields[0], 10, 32)
	if err != nil || uid < 500 || len(payload) > 32<<10 {
		return ""
	}
	type item struct {
		XMLName xml.Name
		Value   string `xml:",chardata"`
	}
	var doc struct {
		XMLName xml.Name `xml:"plist"`
		Values  []struct {
			XMLName xml.Name
			Records []struct {
				XMLName xml.Name
				Items   []item `xml:",any"`
			} `xml:",any"`
		} `xml:",any"`
	}
	decoder := xml.NewDecoder(bytes.NewReader(bytes.TrimSpace(payload)))
	if decoder.Decode(&doc) != nil || len(doc.Values) != 1 || doc.Values[0].XMLName.Local != "array" {
		return ""
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ""
	}
	key := ""
	for _, record := range doc.Values[0].Records {
		if record.XMLName.Local != "dict" || len(record.Items)%2 != 0 {
			return ""
		}
		values := map[string]item{}
		for i := 0; i < len(record.Items); i += 2 {
			k, v := record.Items[i], record.Items[i+1]
			if k.XMLName.Local != "key" || k.Value == "" || values[k.Value].XMLName.Local != "" {
				return ""
			}
			values[k.Value] = v
		}
		on := values["kCGSSessionOnConsoleKey"]
		if on.XMLName.Local == "false" && on.Value == "" {
			continue
		}
		boolTrue := func(k string) bool { v := values[k]; return v.XMLName.Local == "true" && v.Value == "" }
		id, name := values["kCGSSessionUserIDKey"], values["kCGSSessionUserNameKey"]
		session := values["CGSSessionUniqueSessionUUID"]
		sessionID, err := uuid.Parse(session.Value)
		if key != "" || !boolTrue("kCGSSessionOnConsoleKey") || !boolTrue("kCGSessionLoginDoneKey") ||
			!boolTrue("CGSSessionScreenIsLocked") || id.XMLName.Local != "integer" || id.Value != fields[0] ||
			name.XMLName.Local != "string" || name.Value != user || session.XMLName.Local != "string" || err != nil || sessionID == uuid.Nil {
			return ""
		}
		key = "locked-" + sessionID.String()
	}
	return key
}

// Lima can shorten a Unix socket path with a symlink on macOS. Bind the view
// to the same existing instance directory, including /tmp and /private/tmp.
func sameGuestDirectory(actual, expected string) bool {
	a, err := os.Stat(actual)
	if err != nil || !a.IsDir() {
		return false
	}
	b, err := os.Stat(expected)
	return err == nil && b.IsDir() && os.SameFile(a, b)
}
