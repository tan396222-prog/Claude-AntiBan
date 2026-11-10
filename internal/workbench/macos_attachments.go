package workbench

import (
	"path"
	"strings"

	"github.com/google/uuid"
)

func (s *Server) validMacOSAttachmentPath(value, name string) bool {
	if s.macosGuest == nil || strings.ContainsAny(value, "\\\x00\r\n") || path.Clean(value) != value {
		return false
	}
	root := path.Join("/Users", s.macosGuest.User, ".local/share/cage/attachments/files") + "/"
	relative, ok := strings.CutPrefix(value, root)
	if !ok {
		return false
	}
	parts := strings.Split(relative, "/")
	if len(parts) != 3 || parts[2] != name || !browserFileName(name) {
		return false
	}
	for _, part := range parts[:2] {
		id, err := uuid.Parse(part)
		if err != nil || id == uuid.Nil || id.String() != part {
			return false
		}
	}
	return true
}
