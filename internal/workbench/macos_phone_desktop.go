package workbench

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/jpeg"
	"net/http"
)

// The phone uses its own host-generated secret on the private signed GUI agent.
// The ordinary desktop window endpoint cannot submit these actions.
func (s *Server) macOSPhoneDesktop(ctx context.Context, token, op string, input phoneSurfaceInput) ([]byte, int, error) {
	if token == "" {
		return nil, http.StatusGone, nil
	}
	out, err := s.macOSWindowCall(ctx, macOSWindowRequest{Action: "phone-" + op, Phone: &input, PhoneToken: token})
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	return macOSPhoneDesktopReply(out, op, input.ID)
}

func macOSPhoneDesktopReply(out []byte, op, id string) ([]byte, int, error) {
	limit := 4096
	if op == "frame" {
		limit = 3 << 20
	}
	var reply struct {
		Error         string  `json:"error"`
		ID            string  `json:"id"`
		Document      string  `json:"document"`
		Image         string  `json:"image"`
		Width         int     `json:"width"`
		Height        int     `json:"height"`
		ContentWidth  float64 `json:"content_width"`
		ContentHeight float64 `json:"content_height"`
	}
	if len(out) > limit || json.Unmarshal(out, &reply) != nil {
		return nil, http.StatusBadGateway, nil
	}
	if reply.Error != "" {
		status := http.StatusBadGateway
		switch reply.Error {
		case "phone_session_ended", "gui_session_changed", "requires_gui_login":
			status = http.StatusGone
		case "window_changed", "phone_document_changed", "phone_focus_changed", "not_running", "no_visible_window":
			status = http.StatusConflict
		case "screen_recording_permission", "accessibility_permission":
			status = http.StatusForbidden
		case "invalid_input":
			status = http.StatusBadRequest
		case "not_installed":
			status = http.StatusNotImplemented
		}
		return nil, status, nil
	}
	if reply.ID != id {
		return nil, http.StatusBadGateway, nil
	}
	if op == "frame" {
		if reply.Document == "" || len(reply.Document) > 128 || reply.Width < 240 || reply.Width > 1440 || reply.Height < 160 || reply.Height > 1600 {
			return nil, http.StatusBadGateway, nil
		}
		if reply.ContentWidth <= 0 || reply.ContentWidth > 2560 || reply.ContentHeight <= 0 || reply.ContentHeight > 1600 {
			return nil, http.StatusBadGateway, nil
		}
		image, err := base64.StdEncoding.DecodeString(reply.Image)
		if err != nil {
			return nil, http.StatusBadGateway, nil
		}
		config, err := jpeg.DecodeConfig(bytes.NewReader(image))
		if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 5120 || config.Height > 3200 {
			return nil, http.StatusBadGateway, nil
		}
	}
	return out, http.StatusOK, nil
}
