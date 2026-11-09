// SPDX-FileCopyrightText: Copyright The Lima Authors
// SPDX-License-Identifier: Apache-2.0

package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lima-vm/lima/v2/pkg/progressbar"
	"github.com/opencontainers/go-digest"
	"github.com/sirupsen/logrus"
)

// downloadHTTPResumable is called only while holding the cache directory lock
// and with a pinned digest. A partial response is never published as cache data.
func downloadHTTPResumable(ctx context.Context, local, modified, contentType, remote, description string, expected digest.Digest) error {
	part := local + ".part"
	identity := part + ".digest"
	previous, err := os.ReadFile(identity)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if string(previous) != expected.String() {
		if err := os.Remove(part); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.WriteFile(identity, []byte(expected.String()), 0600); err != nil {
			return err
		}
	}
	// Cancellation can arrive after all bytes were received but before rename.
	if validateLocalFileDigest(part, expected) == nil {
		return publishPartial(part, local, identity)
	}
	for attempt := 0; attempt < 4; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var retry bool
		retry, err = resumeHTTPOnce(ctx, local, modified, contentType, remote, description, expected, time.Minute)
		if err == nil {
			return publishPartial(part, local, identity)
		}
		if !retry {
			return err
		}
		if attempt < 3 {
			logrus.Infof("Image download interrupted; retry %d/3 will reuse saved partial data", attempt+1)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt+1) * time.Second):
			}
		}
	}
	return fmt.Errorf("image download interrupted; partial data retained for setup retry: %w", err)
}

func publishPartial(part, local, identity string) error {
	if err := os.Rename(part, local); err != nil {
		return err
	}
	return os.Remove(identity)
}

func resumeHTTPOnce(ctx context.Context, local, modified, contentType, remote, description string, expected digest.Digest, idleTimeout time.Duration) (bool, error) {
	part := local + ".part"
	var offset int64
	if st, err := os.Stat(part); err == nil {
		offset = st.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Only received body bytes renew the timeout. Headers and progress messages
	// cannot keep a stalled transfer alive; a progressing transfer has no cap.
	idle := newDownloadIdleWatchdog(idleTimeout, cancel)
	defer idle.stop()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, remote, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return true, err
	}
	defer resp.Body.Close()
	var total int64
	expectedBody := resp.ContentLength
	switch resp.StatusCode {
	case http.StatusOK:
		// Servers may ignore Range. Replace the partial file instead of appending.
		offset, total = 0, resp.ContentLength
	case http.StatusPartialContent:
		start, end, size, ok := parseDownloadRange(resp.Header.Get("Content-Range"))
		if !ok || start != offset || (resp.ContentLength >= 0 && resp.ContentLength != end-start+1) {
			return false, errors.New("invalid image Content-Range; partial data left unchanged")
		}
		total = size
		expectedBody = end - start + 1
	case http.StatusRequestedRangeNotSatisfiable:
		// The digest above already ruled out a completed file. Reset only this
		// incomplete artifact, for a server whose representation has changed.
		if err := os.Remove(part); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		return true, errors.New("image range rejected; restarting this artifact")
	default:
		return resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500, fmt.Errorf("image download returned HTTP %d", resp.StatusCode)
	}
	for name, value := range map[string]string{modified: resp.Header.Get("Last-Modified"), contentType: resp.Header.Get("Content-Type")} {
		if name != "" {
			if err := os.WriteFile(name, []byte(value), 0600); err != nil {
				return false, err
			}
		}
	}
	flags := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flags |= os.O_TRUNC
	}
	out, err := os.OpenFile(part, flags, 0600)
	if err != nil {
		return false, err
	}
	defer out.Close()
	if _, err := out.Seek(offset, io.SeekStart); err != nil {
		return false, err
	}
	bar, err := progressbar.New(total)
	if err != nil {
		return false, err
	}
	if HideProgress {
		hideBar(bar)
	}
	bar.SetCurrent(offset)
	if description == "" {
		description = "pinned image"
	}
	logrus.Infof("Downloading %s; continuing at %d bytes", description, offset)
	bar.Start()
	defer bar.Finish()
	reader := &downloadActivityReader{reader: resp.Body, activity: idle.touch}
	// The desktop workbench consumes complete lines when stderr is a pipe.
	// Range responses count the bytes already present in the partial file.
	if os.Getenv("CAGE_SETUP_PROGRESS") == "1" {
		reader.received = offset
		reader.total = total
		reader.report = func(received, total int64) {
			fmt.Fprintf(os.Stderr, "CAGE_IMAGE_BYTES %d %d\n", received, total)
		}
		reader.report(offset, total)
		reader.lastReport = time.Now()
	}
	n, err := io.Copy(out, bar.NewProxyReader(reader))
	idle.stop() // Local syncing and checksum validation are not network waits.
	if err != nil {
		return true, err
	}
	if expectedBody >= 0 && n != expectedBody {
		if n > expectedBody {
			_ = out.Close()
			_ = os.Remove(part)
			return false, errors.New("image response exceeded its declared range; partial data discarded")
		}
		return true, io.ErrUnexpectedEOF
	}
	if total >= 0 && n+offset != total {
		return true, io.ErrUnexpectedEOF
	}
	if reader.report != nil {
		reader.report(reader.received, total)
		fmt.Fprintln(os.Stderr, "CAGE_IMAGE_VERIFY")
	}
	if err := out.Sync(); err != nil {
		return false, err
	}
	if err := out.Close(); err != nil {
		return false, err
	}
	if err := validateLocalFileDigest(part, expected); err != nil {
		_ = os.Remove(part)
		return false, fmt.Errorf("image checksum mismatch; partial data discarded: %w", err)
	}
	return false, nil
}

type downloadActivityReader struct {
	reader          io.Reader
	activity        func()
	received, total int64
	report          func(int64, int64)
	lastReport      time.Time
}

func (r *downloadActivityReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.activity()
		if r.report != nil {
			r.received += int64(n)
			if time.Since(r.lastReport) >= time.Second {
				r.report(r.received, r.total)
				r.lastReport = time.Now()
			}
		}
	}
	return n, err
}

// Check the latest receive time under the same lock as touch. An expired
// callback that was already scheduled must not cancel a renewed download.
type downloadIdleWatchdog struct {
	mu      sync.Mutex
	timer   *time.Timer
	last    time.Time
	timeout time.Duration
	cancel  context.CancelFunc
	stopped bool
}

func newDownloadIdleWatchdog(timeout time.Duration, cancel context.CancelFunc) *downloadIdleWatchdog {
	w := &downloadIdleWatchdog{last: time.Now(), timeout: timeout, cancel: cancel}
	w.timer = time.AfterFunc(timeout, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.stopped {
			return
		}
		if remaining := w.timeout - time.Since(w.last); remaining > 0 {
			w.timer.Reset(remaining)
		} else {
			w.stopped = true
			w.cancel()
		}
	})
	return w
}

func (w *downloadIdleWatchdog) touch() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.last = time.Now()
}

func (w *downloadIdleWatchdog) stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stopped = true
	w.timer.Stop()
}

func parseDownloadRange(value string) (start, end, total int64, ok bool) {
	span, size, found := strings.Cut(strings.TrimPrefix(value, "bytes "), "/")
	first, last, dash := strings.Cut(span, "-")
	if !strings.HasPrefix(value, "bytes ") || !found || !dash {
		return
	}
	var a, b, c error
	start, a = strconv.ParseInt(first, 10, 64)
	end, b = strconv.ParseInt(last, 10, 64)
	total, c = strconv.ParseInt(size, 10, 64)
	ok = a == nil && b == nil && c == nil && start >= 0 && end >= start && total > end
	return
}
