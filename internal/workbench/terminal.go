package workbench

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

func (s *Server) listTerminals(w http.ResponseWriter, r *http.Request) {
	jsonReply(w, s.terminals.snapshot())
}

func (s *Server) newTerminal(w http.ResponseWriter, r *http.Request) {
	var input terminalRequest
	if !readJSON(w, r, &input) {
		return
	}
	session, err := s.createTerminal(input)
	switch {
	case errors.Is(err, errTerminalsClosed):
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	case errors.Is(err, errTerminalBusy), errors.Is(err, errTerminalNotReady), errors.Is(err, errTerminalLimit):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, errTerminalStart):
		http.Error(w, err.Error(), http.StatusInternalServerError)
	case err != nil:
		requestError(w, err)
	default:
		jsonReply(w, session.summary())
	}
}

func (s *Server) closeTerminal(w http.ResponseWriter, r *http.Request) {
	s.terminals.remove(r.PathValue("id"))
	jsonReply(w, map[string]bool{"ok": true})
}

func (s *Server) terminalStream(w http.ResponseWriter, r *http.Request) {
	session := s.terminals.get(r.PathValue("id"))
	if session == nil {
		http.NotFound(w, r)
		return
	}
	ws, err := s.upgrade(w, r)
	if err != nil {
		return
	}
	defer ws.Close()
	ws.SetReadLimit(1 << 20)
	subscription := session.subscribe()
	defer subscription.Close()
	if len(subscription.History) > 0 {
		_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := ws.WriteMessage(websocket.BinaryMessage, subscription.History); err != nil {
			return
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer ws.Close()
		for chunk := range subscription.Output {
			_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if ws.WriteMessage(websocket.BinaryMessage, chunk) != nil {
				return
			}
		}
	}()
	defer func() {
		subscription.Close()
		_ = ws.Close()
		<-done
	}()
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			return
		}
		var input struct {
			Type string `json:"type"`
			Data string `json:"data"`
			Cols uint16 `json:"cols"`
			Rows uint16 `json:"rows"`
		}
		if json.Unmarshal(data, &input) != nil {
			continue
		}
		switch input.Type {
		case "input":
			if err := session.enqueueInput(r.Context(), input.Data); err != nil {
				return
			}
		case "resize":
			if input.Cols > 1 && input.Cols < 600 && input.Rows > 1 && input.Rows < 400 {
				_ = session.file.Resize(input.Cols, input.Rows)
			}
		}
	}
}
