package workbench

import (
	"errors"
	"net/http"
	"time"
)

func (s *Server) browser(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if r.Method == http.MethodGet {
		action = "tabs"
	}
	switch action {
	case "tabs", "split", "new", "activate", "close", "navigate", "back", "forward", "reload", "editor", "editor-restart", "download-retry", "upload-finish", "upload-cancel", "resize", "insert-text", "copy-context":
	default:
		http.NotFound(w, r)
		return
	}
	var input *browserInput
	if r.Method != http.MethodGet {
		input = &browserInput{}
		if !readJSON(w, r, input) {
			return
		}
	}
	data, err := s.browserAction(r.Context(), action, input)
	if err != nil {
		status := http.StatusBadGateway
		var inputError browserInputError
		switch {
		case errors.Is(err, errJobsClosed):
			status = http.StatusServiceUnavailable
		case errors.Is(err, errBrowserNotReady), errors.Is(err, errBrowserBusy):
			status = http.StatusConflict
		case errors.As(err, &inputError):
			status = http.StatusBadRequest
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func (s *Server) remoteInput(w http.ResponseWriter, r *http.Request) {
	// The authenticated workbench endpoint shares browser transport with both
	// graphical sessions; it does not create another guest listener.
	var input browserInput
	if !readJSON(w, r, &input) {
		return
	}
	data, err := s.browserAction(r.Context(), "ime", &input)
	if err != nil {
		status := http.StatusBadGateway
		var inputError browserInputError
		if errors.As(err, &inputError) {
			status = http.StatusBadRequest
		}
		if errors.Is(err, errBrowserNotReady) {
			status = http.StatusConflict
		}
		http.Error(w, err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func (s *Server) browserEvents(w http.ResponseWriter, r *http.Request) {
	body, err := s.subscribeBrowser(r.Context())
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, errBrowserNotReady) {
			status = http.StatusConflict
		}
		http.Error(w, "Browser unavailable", status)
		return
	}
	defer body.Close()
	controller := http.NewResponseController(w)
	// The HTTP handler cancels blocked reads and writes with the service. A
	// write deadline also bounds delivery while a connected viewer is too slow.
	defer func() {
		if r.Context().Err() == nil {
			_ = controller.SetWriteDeadline(time.Time{})
		}
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Connection", "keep-alive")
	_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if r.Context().Err() != nil {
		return
	}
	if err := controller.Flush(); err != nil {
		return
	}
	_ = s.writeBrowserEvents(r.Context(), w, body)
}
