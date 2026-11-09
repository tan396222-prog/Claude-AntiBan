// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package downloader

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
)

func TestPinnedDownloadResumesInterruptedBody(t *testing.T) {
	data := "0123456789abcdef"
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Length", "16")
			fmt.Fprint(w, data[:5])
			return
		}
		if r.Header.Get("Range") != "bytes=5-" {
			t.Errorf("did not resume: %q", r.Header.Get("Range"))
		}
		w.Header().Set("Content-Range", "bytes 5-15/16")
		w.WriteHeader(http.StatusPartialContent)
		fmt.Fprint(w, data[5:])
	}))
	defer s.Close()
	cache := t.TempDir()
	t.Setenv("CAGE_LIMA_CACHE_DIR", cache)
	r, err := Download(t.Context(), "", s.URL, WithCache(), WithExpectedDigest(digest.FromString(data)))
	if err != nil {
		t.Fatal(err)
	}
	if rel, err := filepath.Rel(cache, r.CachePath); err != nil || !filepath.IsLocal(rel) {
		t.Fatalf("download landed outside chosen cache: %q", r.CachePath)
	}
	got, err := os.ReadFile(r.CachePath)
	if err != nil || string(got) != data || calls.Load() != 2 {
		t.Fatalf("result %q, calls %d, error %v", got, calls.Load(), err)
	}
}

func TestPinnedDownloadAcrossSetupRetries(t *testing.T) {
	for _, mode := range []string{"range", "ignore-range", "bad-range", "wrong-hash", "changed-digest", "complete", "range-rejected"} {
		t.Run(mode, func(t *testing.T) {
			data := "0123456789abcdef"
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch mode {
				case "complete":
					t.Error("complete partial was downloaded again")
				case "changed-digest", "ignore-range":
					if mode == "changed-digest" && r.Header.Get("Range") != "" {
						t.Error("resumed a different digest")
					}
					fmt.Fprint(w, data)
				case "range-rejected":
					if calls.Load() == 1 {
						w.WriteHeader(416)
					} else {
						if r.Header.Get("Range") != "" {
							t.Error("rejected range was reused")
						}
						fmt.Fprint(w, data)
					}
				default:
					if r.Header.Get("Range") != "bytes=5-" {
						t.Error("partial not reused")
					}
					span := "bytes 5-15/16"
					if mode == "bad-range" {
						span = "bytes 4-14/16"
					}
					w.Header().Set("Content-Range", span)
					w.WriteHeader(206)
					if mode == "wrong-hash" {
						fmt.Fprint(w, "xxxxxxxxxxx")
					} else {
						fmt.Fprint(w, data[5:])
					}
				}
			}))
			defer s.Close()
			cache := t.TempDir()
			dir := cacheDirectoryPath(cache, s.URL)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			part := filepath.Join(dir, "data.part")
			body, expected := data[:5], digest.FromString(data)
			identity := expected.String()
			if mode == "changed-digest" {
				identity = digest.FromString("old").String()
			}
			if mode == "complete" {
				body = data
			}
			if err := os.WriteFile(part, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(part+".digest", []byte(identity), 0600); err != nil {
				t.Fatal(err)
			}
			r, err := Download(t.Context(), "", s.URL, WithCacheDir(cache), WithExpectedDigest(expected))
			if mode == "bad-range" || mode == "wrong-hash" {
				if err == nil {
					t.Fatal("invalid data accepted")
				}
				if _, err := os.Stat(filepath.Join(dir, "data")); !os.IsNotExist(err) {
					t.Fatal("invalid cache published")
				}
				if mode == "bad-range" {
					got, _ := os.ReadFile(part)
					if string(got) != body {
						t.Fatal("bad range altered partial")
					}
				} else if _, err := os.Stat(part); !os.IsNotExist(err) {
					t.Fatal("corrupt partial retained")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(r.CachePath)
			if err != nil || string(got) != data {
				t.Fatalf("got %q: %v", got, err)
			}
		})
	}
}

func TestPinnedDownloadCancellationRetainsPartial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		fmt.Fprint(w, "partial")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer s.Close()
	dir := t.TempDir()
	local := filepath.Join(dir, "data")
	done := make(chan error, 1)
	go func() {
		done <- downloadHTTPResumable(ctx, local, "", "", s.URL, "test", digest.FromString(strings.Repeat("x", 1000)))
	}()
	// Wait for the client to persist data, not just for the server to send it.
	for i := 0; i < 1000; i++ {
		if st, err := os.Stat(local + ".part"); err == nil && st.Size() > 0 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("ended early: %v", err)
		default:
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	got, err := os.ReadFile(local + ".part")
	if err != nil || string(got) != "partial" {
		t.Fatalf("partial lost: %q %v", got, err)
	}
	if _, err := os.Stat(local); !os.IsNotExist(err) {
		t.Fatal("cancelled data published")
	}
}

func TestPinnedDownloadUsesLatestBodyActivity(t *testing.T) {
	for _, mode := range []string{"slow", "stalled", "headers-only", "no-response"} {
		t.Run(mode, func(t *testing.T) {
			const data = "abcdef"
			const idle = 200 * time.Millisecond
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode != "no-response" {
					w.Header().Set("Content-Length", fmt.Sprint(len(data)))
					w.(http.Flusher).Flush()
				}
				if mode == "headers-only" || mode == "no-response" {
					<-r.Context().Done()
					return
				}
				for i := range len(data) {
					fmt.Fprint(w, data[i:i+1])
					w.(http.Flusher).Flush()
					if mode == "stalled" {
						<-r.Context().Done()
						return
					}
					select {
					case <-r.Context().Done():
						return
					case <-time.After(idle / 3):
					}
				}
			}))
			defer s.Close()
			local := filepath.Join(t.TempDir(), "data")
			start := time.Now()
			retry, err := resumeHTTPOnce(t.Context(), local, "", "", s.URL, "test", digest.FromString(data), idle)
			if mode == "slow" {
				if err != nil || retry || time.Since(start) <= idle {
					t.Fatalf("progressing transfer interrupted: retry=%v err=%v", retry, err)
				}
				got, err := os.ReadFile(local + ".part")
				if err != nil || string(got) != data {
					t.Fatalf("received %q: %v", got, err)
				}
			} else if !retry || err == nil {
				t.Fatalf("stalled transfer not retried: retry=%v err=%v", retry, err)
			}
			if mode == "stalled" {
				got, err := os.ReadFile(local + ".part")
				if err != nil || string(got) != data[:1] {
					t.Fatalf("partial data lost: %q %v", got, err)
				}
			}
		})
	}
}
