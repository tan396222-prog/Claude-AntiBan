//go:build darwin && arm64 && cgo && !no_vz

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sylvinus/agent-vm/internal/hostproxy"
)

func prepareMacRestoreMedia(ctx context.Context, media, url, digest string, total int64, out io.Writer) error {
	transport := hostproxy.NewTransport()
	defer transport.CloseIdleConnections()
	return prepareMacRestoreMediaWithTransport(ctx, media, url, digest, total, out, transport)
}

func prepareMacRestoreMediaWithTransport(ctx context.Context, media, url, digest string, total int64, out io.Writer, transport http.RoundTripper) error {
	if _, err := os.Stat(media); err == nil {
		return verifyMacRestoreMedia(ctx, media, digest, out)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	partial := media + ".partial"
	client := &http.Client{Transport: transport, Timeout: 2 * time.Hour, CheckRedirect: func(req *http.Request, previous []*http.Request) error {
		if req.URL.Scheme != "https" || len(previous) >= 5 {
			return errors.New("Apple 镜像重定向无效")
		}
		return nil
	}}
	defer client.CloseIdleConnections()
	report := func() {
		var received int64
		if info, err := os.Stat(partial); err == nil {
			received = info.Size()
		}
		fmt.Fprintf(out, "CAGE_GUEST_BYTES macos download %d %d\n", received, total)
	}
	report()
	done := make(chan error, 1)
	go func() {
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			err = downloadMacRestore(ctx, client, url, partial, total)
			if err == nil || ctx.Err() != nil {
				break
			}
		}
		done <- err
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			report()
		case err := <-done:
			report()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return err
			}
			if err = verifyMacRestoreMedia(ctx, partial, digest, out); err != nil {
				// Cancellation retains a complete archive for the next attempt.
				if ctx.Err() == nil {
					_ = os.Remove(partial)
				}
				return err
			}
			return os.Rename(partial, media)
		}
	}
}

func downloadMacRestore(ctx context.Context, client *http.Client, address, partial string, total int64) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if !strings.HasPrefix(address, "https://") {
		return errors.New("Apple 镜像必须通过 HTTPS 下载")
	}
	file, err := os.OpenFile(partial, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	offset := info.Size()
	if total > 0 && offset > total {
		if err := file.Truncate(0); err != nil {
			return err
		}
		offset = 0
	}
	if total > 0 && offset == total {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept-Encoding", "identity")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		offset = 0
		if err := file.Truncate(0); err != nil {
			return err
		}
	case http.StatusPartialContent:
		var start, end, size int64
		if _, err := fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &size); err != nil || start != offset || end < start || total > 0 && size != total {
			return errors.New("Apple 镜像续传范围不匹配")
		}
	default:
		return fmt.Errorf("Apple 镜像下载返回 HTTP %d", resp.StatusCode)
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	// Retain the old curl path's protection against a stalled image stream.
	watchdog := time.AfterFunc(90*time.Second, cancel)
	defer watchdog.Stop()
	limit := total
	if limit <= 0 {
		limit = 64 << 30
	}
	count, err := io.Copy(file, io.LimitReader(restoreDownloadReader{reader: resp.Body, watchdog: watchdog}, limit-offset+1))
	if err != nil {
		return err
	}
	if count+offset > limit || total > 0 && count+offset != total {
		return errors.New("Apple 镜像下载大小不匹配")
	}
	return nil
}

type restoreDownloadReader struct {
	reader   io.Reader
	watchdog *time.Timer
}

func (r restoreDownloadReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	if n > 0 {
		r.watchdog.Reset(90 * time.Second)
	}
	return n, err
}

func verifyMacRestoreMedia(ctx context.Context, media, digest string, out io.Writer) error {
	file, err := os.Open(media)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	var received int64
	report := func() { fmt.Fprintf(out, "CAGE_GUEST_BYTES macos verify %d %d\n", received, info.Size()) }
	report()
	hash := sha256.New()
	buffer := make([]byte, 4<<20)
	last := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := file.Read(buffer)
		_, _ = hash.Write(buffer[:n])
		received += int64(n)
		if time.Since(last) >= time.Second || readErr == io.EOF {
			report()
			last = time.Now()
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if hex.EncodeToString(hash.Sum(nil)) != digest {
		return errors.New("Apple 下载校验失败，请重试。")
	}
	return nil
}
