package remotelink

import (
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const writeMessageSize = 32 << 10

// webSocketConn treats binary messages as one byte stream. After an I/O timeout
// the WebSocket must be replaced, as required by gorilla/websocket. In
// particular a write deadline closes the socket to interrupt a blocked write;
// this avoids concurrently changing gorilla's writer-owned deadline field.
type webSocketConn struct {
	ws                        *websocket.Conn
	readMu, writeMu           sync.Mutex
	reader                    io.Reader
	closeOnce                 sync.Once
	closeErr                  error
	deadlineMu                sync.Mutex
	writeDeadline             time.Time
	writeTimer                *time.Timer
	writeGeneration           uint64
	writeActive, writeExpired bool
}

var _ net.Conn = (*webSocketConn)(nil)

func newWebSocketConn(ws *websocket.Conn) *webSocketConn {
	ws.SetReadLimit(MaxMessageSize)
	_ = ws.SetWriteDeadline(time.Time{})
	return &webSocketConn{ws: ws}
}

func (c *webSocketConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for {
		if c.reader == nil {
			kind, reader, err := c.ws.NextReader()
			if err != nil {
				return 0, err
			}
			if kind != websocket.BinaryMessage {
				_ = c.Close()
				return 0, errors.New("remote transport accepts only binary WebSocket messages")
			}
			c.reader = reader
		}
		n, err := c.reader.Read(p)
		if err == io.EOF {
			c.reader = nil
			if n == 0 {
				continue
			}
			err = nil
		}
		return n, err
	}
}

func (c *webSocketConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.deadlineMu.Lock()
	if !c.writeDeadline.IsZero() && !time.Now().Before(c.writeDeadline) {
		c.deadlineMu.Unlock()
		return 0, os.ErrDeadlineExceeded
	}
	c.writeActive, c.writeExpired = true, false
	c.scheduleWriteDeadline()
	c.deadlineMu.Unlock()
	n, err := 0, error(nil)
	for n < len(p) {
		end := min(n+writeMessageSize, len(p))
		if err = c.ws.WriteMessage(websocket.BinaryMessage, p[n:end]); err != nil {
			break
		}
		n = end
	}
	c.deadlineMu.Lock()
	c.writeActive = false
	c.writeGeneration++
	if c.writeTimer != nil {
		c.writeTimer.Stop()
		c.writeTimer = nil
	}
	if c.writeExpired {
		err = os.ErrDeadlineExceeded
	}
	c.deadlineMu.Unlock()
	return n, err
}

func (c *webSocketConn) Close() error {
	c.closeOnce.Do(func() { c.closeErr = c.ws.Close() })
	return c.closeErr
}

func (c *webSocketConn) LocalAddr() net.Addr  { return c.ws.UnderlyingConn().LocalAddr() }
func (c *webSocketConn) RemoteAddr() net.Addr { return c.ws.UnderlyingConn().RemoteAddr() }

func (c *webSocketConn) SetDeadline(deadline time.Time) error {
	if err := c.SetReadDeadline(deadline); err != nil {
		return err
	}
	return c.SetWriteDeadline(deadline)
}

func (c *webSocketConn) SetReadDeadline(deadline time.Time) error {
	return c.ws.UnderlyingConn().SetReadDeadline(deadline)
}

func (c *webSocketConn) SetWriteDeadline(deadline time.Time) error {
	c.deadlineMu.Lock()
	defer c.deadlineMu.Unlock()
	c.writeDeadline = deadline
	c.scheduleWriteDeadline()
	return nil
}

// scheduleWriteDeadline is called with deadlineMu held. Each generation fences
// a timer canceled by a successful write or a changed deadline.
func (c *webSocketConn) scheduleWriteDeadline() {
	c.writeGeneration++
	if c.writeTimer != nil {
		c.writeTimer.Stop()
		c.writeTimer = nil
	}
	if !c.writeActive || c.writeDeadline.IsZero() {
		return
	}
	generation := c.writeGeneration
	c.writeTimer = time.AfterFunc(time.Until(c.writeDeadline), func() {
		c.deadlineMu.Lock()
		if !c.writeActive || c.writeGeneration != generation {
			c.deadlineMu.Unlock()
			return
		}
		c.writeExpired = true
		c.deadlineMu.Unlock()
		_ = c.Close()
	})
}
