package workbench

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"math"
	"net/http"
	"time"

	"golang.org/x/image/draw"
)

// The authenticated desktop relay pins the GUI user and official Claude window.
// One phone lease owns one relay connection, including paired pointer/key events.
type windowsPhoneDesktopSession struct {
	connection     *nativeWindowConnection
	id, token      string
	width, height  int
	ratio, density float64
	frame          windowsWindowFrame
	jpeg           string
	focus          string
	idle           *time.Timer
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func (p *windowsPhoneDesktopSession) close() {
	if p.idle != nil {
		p.idle.Stop()
	}
	p.connection.Close()
}

func (s *Server) windowsPhoneDesktop(ctx context.Context, token, op string, input phoneSurfaceInput) ([]byte, int, error) {
	state := &s.phones.desktop
	p := state.nativeDesktop
	if op == "close" {
		if p != nil && p.id == input.ID && equalSecret(p.token, token) {
			state.nativeDesktop = nil
			p.close()
		}
		out, err := json.Marshal(map[string]string{"id": input.ID})
		return out, http.StatusOK, err
	}
	lease := state.session
	if token == "" || lease == nil || lease.id != input.ID || !equalSecret(lease.token, token) || lease.ctx.Err() != nil {
		return nil, http.StatusGone, nil
	}
	if p == nil {
		if op != "open" {
			return nil, http.StatusGone, nil
		}
		connection, err := s.windowsWindowConnection(lease.ctx)
		if err != nil {
			return nil, http.StatusBadGateway, err
		}
		p = &windowsPhoneDesktopSession{connection: connection, id: input.ID, token: token}
		p.idle = time.AfterFunc(2*time.Minute, lease.cancel)
		state.nativeDesktop = p
	}
	if p.id != input.ID || !equalSecret(p.token, token) {
		return nil, http.StatusGone, nil
	}
	p.idle.Reset(2 * time.Minute)
	operation, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	stop := context.AfterFunc(operation, p.connection.Close)
	defer stop()
	var out []byte
	var err error
	switch op {
	case "open":
		var status struct {
			Installed, Running bool
			PhoneSupported     bool    `json:"phone_supported"`
			Density            float64 `json:"display_scale"`
		}
		if err = p.connection.request(map[string]any{"action": "status"}, &status); err != nil {
			break
		}
		if !status.PhoneSupported {
			return nil, http.StatusUpgradeRequired, nil
		}
		if !status.Installed {
			return nil, http.StatusNotImplemented, nil
		}
		if !status.Running {
			return nil, http.StatusPreconditionFailed, nil
		}
		p.density = status.Density
		if !finite(p.density) || p.density < 1 || p.density > 8 {
			return nil, http.StatusBadGateway, nil
		}
		err = p.resize(input)
	case "frame":
		out, err = p.capture()
	case "action":
		if input.Type == "resize" {
			err = p.resize(input)
		} else {
			err = p.action(input)
		}
	default:
		return nil, http.StatusBadRequest, nil
	}
	if err != nil {
		status := windowsPhoneDesktopStatus(err)
		var failure windowsWindowFailure
		if errors.As(err, &failure) && failure.code == "requires_gui_login" {
			return nil, status, err
		}
		return nil, status, nil
	}
	if operation.Err() != nil {
		return nil, http.StatusGone, nil
	}
	if out == nil {
		out, err = json.Marshal(map[string]string{"id": input.ID})
	}
	return out, http.StatusOK, err
}

func windowsPhoneDesktopStatus(err error) int {
	var failure windowsWindowFailure
	if !errors.As(err, &failure) {
		return http.StatusGone
	}
	switch failure.code {
	case "desktop_not_installed":
		return http.StatusNotImplemented
	case "desktop_not_running", "desktop_capture_busy":
		return http.StatusServiceUnavailable
	case "desktop_target_changed", "desktop_phone_focus_changed":
		return http.StatusConflict
	case "requires_gui_login":
		return http.StatusGone
	case "desktop_size_invalid", "desktop_pointer_invalid", "desktop_key_invalid", "desktop_text_limit", "desktop_request_invalid":
		return http.StatusBadRequest
	default:
		return http.StatusBadGateway
	}
}

func (p *windowsPhoneDesktopSession) resize(input phoneSurfaceInput) error {
	if !input.valid(phoneDesktop, "open") {
		return windowsWindowFailure{code: "desktop_size_invalid"}
	}
	ratio := input.PixelRatio
	if ratio == 0 {
		ratio = 1
	}
	width := int(math.Round(float64(max(320, input.Width)) * p.density))
	height := int(math.Round(float64(max(240, input.Height)) * p.density))
	if err := p.connection.request(map[string]any{"action": "resize", "width": width, "height": height}, nil); err != nil {
		return err
	}
	p.width, p.height, p.ratio = input.Width, input.Height, ratio
	p.frame, p.jpeg, p.focus = windowsWindowFrame{}, "", ""
	return nil
}

func (p *windowsPhoneDesktopSession) capture() ([]byte, error) {
	var frame windowsWindowFrame
	if err := p.connection.request(map[string]any{"action": "frame", "after": p.frame.Sequence}, &frame); err != nil {
		return nil, err
	}
	if !frame.Unchanged {
		if !finite(frame.Density) || frame.Density < 1 || frame.Density > 8 {
			return nil, windowsWindowFailure{code: "desktop_frame_invalid"}
		}
		encoded, err := windowsPhoneJPEG(frame, p.width, p.height, p.ratio)
		if err != nil {
			return nil, err
		}
		p.frame, p.jpeg, p.density = frame, encoded, frame.Density
	} else if frame.Sequence == "" || frame.Sequence != p.frame.Sequence {
		return nil, windowsWindowFailure{code: "desktop_frame_invalid"}
	}
	if p.jpeg == "" || p.frame.Target == "" || len(p.frame.Target) > 128 {
		return nil, windowsWindowFailure{code: "desktop_frame_invalid"}
	}
	var focus struct {
		Available bool   `json:"available"`
		Context   string `json:"context"`
		Password  bool   `json:"password"`
	}
	err := p.connection.request(map[string]any{"action": "ime", "mode": "status"}, &focus)
	if err != nil && windowsPhoneDesktopStatus(err) != http.StatusConflict {
		return nil, err
	}
	p.focus = ""
	if err == nil && focus.Available && !focus.Password && len(focus.Context) <= 128 {
		p.focus = focus.Context
	}
	return json.Marshal(map[string]any{
		"id": p.id, "document": p.frame.Target, "image": p.jpeg,
		"width": p.width, "height": p.height, "content_width": float64(p.frame.Width) / p.density, "content_height": float64(p.frame.Height) / p.density,
		"focus": p.focus, "password": focus.Password, "window": "desktop", "shared": true,
		"login_supported": false, "pixel_ratio_control": true, "requested_pixel_ratio": p.ratio,
	})
}

func (p *windowsPhoneDesktopSession) action(input phoneSurfaceInput) error {
	if p.frame.Target == "" || input.Document != p.frame.Target {
		return windowsWindowFailure{code: "desktop_target_changed"}
	}
	request := map[string]any{"target": p.frame.Target}
	send := func() error {
		err := p.connection.request(request, nil)
		if err != nil {
			// A down event may have succeeded before a later request failed.
			// Closing the native lease releases every held button and key.
			p.connection.Close()
		}
		return err
	}
	typing := input.Type == "text" || input.Type == "key" && input.Key != "Tab" && input.Key != "Escape" && input.Key != "ArrowUp" && input.Key != "ArrowDown" && input.Key != "ArrowLeft" && input.Key != "ArrowRight"
	if typing {
		if p.focus == "" || input.Focus != p.focus {
			return windowsWindowFailure{code: "desktop_phone_focus_changed"}
		}
		request["phone_focus"] = p.focus
	}
	switch input.Type {
	case "tap", "scroll":
		if input.X == nil || input.Y == nil || !finite(*input.X) || !finite(*input.Y) || *input.X < 0 || *input.Y < 0 || *input.X >= float64(p.frame.Width)/p.density || *input.Y >= float64(p.frame.Height)/p.density {
			return windowsWindowFailure{code: "desktop_pointer_invalid"}
		}
		request["action"], request["x"], request["y"] = "pointer", int(*input.X*p.density), int(*input.Y*p.density)
		masks := []int{1}
		if input.Type == "scroll" {
			if input.DX == nil || input.DY == nil || !finite(*input.DX) || !finite(*input.DY) || math.Abs(*input.DX) > 3200 || math.Abs(*input.DY) > 3200 {
				return windowsWindowFailure{code: "desktop_pointer_invalid"}
			}
			masks = nil
			for _, axis := range []struct {
				delta              float64
				negative, positive int
			}{{*input.DY, 8, 16}, {*input.DX, 32, 64}} {
				mask := axis.positive
				if axis.delta < 0 {
					mask = axis.negative
				}
				for steps := int(math.Ceil(math.Abs(axis.delta) / 120)); steps > 0; steps-- {
					masks = append(masks, mask)
				}
			}
		}
		for _, mask := range masks {
			request["mask"] = mask
			if err := send(); err != nil {
				return err
			}
			request["mask"] = 0
			if err := send(); err != nil {
				return err
			}
		}
		return nil
	case "text":
		request["action"], request["text"] = "text", input.Text
	case "key":
		if len(input.Key) == 1 && input.Key[0] >= 0x20 && input.Key[0] <= 0x7e {
			request["action"], request["text"] = "text", input.Key
			break
		}
		keys := map[string]uint32{"Backspace": 0xff08, "Tab": 0xff09, "Enter": 0xff0d, "Escape": 0xff1b, "Delete": 0xffff, "ArrowLeft": 0xff51, "ArrowUp": 0xff52, "ArrowRight": 0xff53, "ArrowDown": 0xff54}
		key := keys[input.Key]
		if key == 0 {
			return windowsWindowFailure{code: "desktop_key_invalid"}
		}
		request["action"], request["keysym"], request["down"] = "key", key, true
		if err := send(); err != nil {
			return err
		}
		request["down"] = false
		delete(request, "phone_focus")
	default:
		return windowsWindowFailure{code: "desktop_request_invalid"}
	}
	return send()
}

func windowsPhoneJPEG(frame windowsWindowFrame, width, height int, ratio float64) (string, error) {
	img, err := nativeFrameImage(frame)
	if err != nil {
		return "", windowsWindowFailure{code: "desktop_frame_invalid"}
	}
	scale := min(1, float64(width)*min(2, ratio)/float64(frame.Width), float64(height)*min(2, ratio)/float64(frame.Height))
	if scale < 1 {
		resized := image.NewRGBA(image.Rect(0, 0, max(1, int(float64(frame.Width)*scale)), max(1, int(float64(frame.Height)*scale))))
		draw.CatmullRom.Scale(resized, resized.Bounds(), img, img.Bounds(), draw.Src, nil)
		img = resized
	}
	for _, quality := range []int{90, 78, 62, 45} {
		var output bytes.Buffer
		if err := jpeg.Encode(&output, img, &jpeg.Options{Quality: quality}); err != nil {
			return "", windowsWindowFailure{code: "desktop_frame_invalid"}
		}
		if output.Len() <= 2<<20 {
			return base64.StdEncoding.EncodeToString(output.Bytes()), nil
		}
	}
	return "", windowsWindowFailure{code: "desktop_frame_limit"}
}
