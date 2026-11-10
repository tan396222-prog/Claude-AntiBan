//go:build darwin && arm64 && cgo && !no_vz

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMacRestoreDownloadResumesAndReportsVerification(t *testing.T) {
	body := bytes.Repeat([]byte("restore-image"), 10000)
	offset := len(body) / 3
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != fmt.Sprintf("bytes=%d-", offset) {
			t.Errorf("missing resume offset: %s", r.Header.Get("Range"))
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, len(body)-1, len(body)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body[offset:])
	}))
	defer server.Close()
	media := filepath.Join(t.TempDir(), "MacOS.ipsw")
	if err := os.WriteFile(media+".partial", body[:offset], 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(body)
	var log bytes.Buffer
	if err := prepareMacRestoreMediaWithTransport(t.Context(), media, server.URL, hex.EncodeToString(hash[:]), int64(len(body)), &log, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(media)
	if err != nil || !bytes.Equal(actual, body) {
		t.Fatalf("published media differs: %v", err)
	}
	if _, err := os.Stat(media + ".partial"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial file was not promoted: %v", err)
	}
	for _, event := range []string{fmt.Sprintf("CAGE_GUEST_BYTES macos download %d %d", offset, len(body)), fmt.Sprintf("CAGE_GUEST_BYTES macos verify 0 %d", len(body)), fmt.Sprintf("CAGE_GUEST_BYTES macos verify %d %d", len(body), len(body))} {
		if !strings.Contains(log.String(), event) {
			t.Fatalf("missing %q in %s", event, log.String())
		}
	}
	log.Reset()
	// A verified cached archive must not make another request to the server.
	if err := prepareMacRestoreMediaWithTransport(t.Context(), media, "https://invalid.invalid", hex.EncodeToString(hash[:]), int64(len(body)), &log, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log.String(), "macos download") {
		t.Fatal("cached archive was downloaded again")
	}
}

func TestMacRestoreCancelledDownloadKeepsPartial(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), 128<<10))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	media := filepath.Join(t.TempDir(), "MacOS.ipsw")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- prepareMacRestoreMediaWithTransport(ctx, media, server.URL, strings.Repeat("0", 64), 1<<20, &bytes.Buffer{}, server.Client().Transport)
	}()
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			t.Fatalf("download ended before cancellation: %v", err)
		case <-deadline:
			t.Fatal("download did not write partial bytes")
		case <-ticker.C:
			if info, err := os.Stat(media + ".partial"); err == nil && info.Size() > 0 {
				cancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Fatalf("wrong cancellation: %v", err)
				}
				if info, err := os.Stat(media + ".partial"); err != nil || info.Size() == 0 {
					t.Fatalf("partial download lost: %v", err)
				}
				if _, err := os.Stat(media); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("unverified media published: %v", err)
				}
				return
			}
		}
	}
}

func TestMacRestoreDigestFailureDoesNotPublishMedia(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("invalid restore")) }))
	defer server.Close()
	media := filepath.Join(t.TempDir(), "MacOS.ipsw")
	if err := prepareMacRestoreMediaWithTransport(t.Context(), media, server.URL, strings.Repeat("0", 64), 15, &bytes.Buffer{}, server.Client().Transport); err == nil {
		t.Fatal("invalid digest accepted")
	}
	for _, p := range []string{media, media + ".partial"} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("invalid media retained at %s: %v", p, err)
		}
	}
}
