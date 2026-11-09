package workbench

import (
	"context"
	"net/http"
	"time"
)

// Cancelling a context alone cannot release a slow request-body read or a
// response write. HTTP deadlines interrupt both without closing a reused
// connection after its handler has returned. A genuine upgrade also owns its
// WebSocket cancellation; an Upgrade header alone must not bypass body cleanup.
func interruptHTTPOnCancel(w http.ResponseWriter, r *http.Request) func() {
	done := make(chan struct{})
	body := r.Body
	stop := context.AfterFunc(r.Context(), func() {
		defer close(done)
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(time.Now())
		_ = controller.SetWriteDeadline(time.Now())
		_ = body.Close()
	})
	return func() {
		if !stop() {
			<-done
		}
	}
}
