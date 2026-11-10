// cage-relay forwards an opaque stream between two outbound connections.
// Device authentication and application authorization belong to the peers.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/sylvinus/agent-vm/internal/relay"
)

const maxTokenBytes = 4096

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "cage-relay:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("cage-relay", flag.ContinueOnError)
	flags.SetOutput(output)
	listen := flags.String("listen", "127.0.0.1:8787", "HTTP listen address; place public deployments behind TLS")
	tokenFile := flags.String("token-file", "", "required file containing the relay administration token")
	publicRooms := flags.Bool("public-rooms", false, "allow room creation without a token; enforce quotas at the public ingress")
	maxRooms := flags.Int("max-rooms", 128, "maximum pending and connected rooms")
	pairTimeout := flags.Duration("pair-timeout", 2*time.Minute, "time allowed for both peers to connect")
	idleTimeout := flags.Duration("idle-timeout", 5*time.Minute, "maximum connection idle time")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("positional arguments are not supported")
	}
	if *maxRooms < 1 || *pairTimeout <= 0 || *idleTimeout <= 0 {
		return errors.New("max-rooms, pair-timeout and idle-timeout must be positive")
	}
	token, err := readTokenFile(*tokenFile)
	if err != nil {
		return err
	}
	server, err := relay.New(relay.Config{
		AdminToken: token, PublicRooms: *publicRooms, MaxRooms: *maxRooms,
		PairTimeout: *pairTimeout, IdleTimeout: *idleTimeout,
	})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		_ = server.Close()
		return fmt.Errorf("listen: %w", err)
	}
	logger := log.New(output, "cage-relay: ", log.LstdFlags)
	logger.Printf("listening on %s", listener.Addr())
	return serve(ctx, listener, server, logger)
}

func readTokenFile(name string) (string, error) {
	if name == "" {
		return "", errors.New("--token-file is required")
	}
	file, err := os.Open(name)
	if err != nil {
		return "", fmt.Errorf("open token file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect token file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("token file must be a regular file")
	}
	// Windows permissions are ACL based and cannot be checked with Unix mode
	// bits. Deployment instructions require an owner-restricted ACL there.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return "", errors.New("token file must not grant permissions to group or other users; use mode 0600")
	}
	// One trailing LF or CRLF is accepted, as produced by command-line secret
	// generators. Other whitespace is rejected instead of silently changing it.
	raw, err := io.ReadAll(io.LimitReader(file, maxTokenBytes+3))
	if err != nil {
		return "", fmt.Errorf("read token file: %w", err)
	}
	defer clear(raw)
	value := raw
	if bytes.HasSuffix(value, []byte("\n")) {
		value = bytes.TrimSuffix(value[:len(value)-1], []byte("\r"))
	}
	if len(value) < 32 || len(value) > maxTokenBytes {
		return "", errors.New("token file must contain 32 to 4096 bytes, excluding its trailing newline")
	}
	for _, ch := range value {
		if ch < 0x21 || ch > 0x7e {
			return "", errors.New("token must contain visible ASCII characters without whitespace")
		}
	}
	return string(value), nil
}

func serve(ctx context.Context, listener net.Listener, relayServer *relay.Server, logger *log.Logger) error {
	httpServer := &http.Server{
		Handler: relayServer, ErrorLog: logger,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.Serve(listener) }()
	select {
	case err := <-serveErr:
		closeErr := relayServer.Close()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		return errors.Join(err, closeErr)
	case <-ctx.Done():
		// http.Server.Shutdown does not close upgraded WebSocket connections.
		// Stop relay rooms first so peers and pending joins are released too.
		closeErr := relayServer.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		shutdownErr := httpServer.Shutdown(shutdownCtx)
		if shutdownErr != nil {
			_ = httpServer.Close()
		}
		<-serveErr
		return errors.Join(closeErr, shutdownErr)
	}
}
