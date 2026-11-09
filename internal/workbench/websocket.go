package workbench

import (
	"context"
	"github.com/gorilla/websocket"
	"net/http"
)

func (s *Server) upgrade(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == s.origin }, ReadBufferSize: 16384, WriteBufferSize: 16384}
	ws, err := up.Upgrade(w, r, nil)
	if err == nil {
		context.AfterFunc(r.Context(), func() { _ = ws.Close() })
	}
	return ws, err
}
