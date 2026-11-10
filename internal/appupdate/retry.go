package appupdate

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type retryError struct{ at time.Time }

func (e *retryError) Error() string      { return "更新源请求受限，请稍后重试" }
func (e *retryError) RetryAt() time.Time { return e.at }

func (s *State) fail(err error) {
	s.Phase, s.Message, s.Retryable, s.RetryAt = "failed", err.Error(), true, ""
	var delayed interface{ RetryAt() time.Time }
	if errors.As(err, &delayed) {
		s.RetryAt = delayed.RetryAt().UTC().Format(time.RFC3339)
	}
}

func updateSourceError(resp *http.Response, now time.Time) error {
	after := strings.TrimSpace(resp.Header.Get("Retry-After"))
	exhausted := strings.TrimSpace(resp.Header.Get("X-RateLimit-Remaining")) == "0"
	if resp.StatusCode == http.StatusForbidden && after == "" && !exhausted {
		return errors.New("更新源拒绝访问（HTTP 403），请检查所选代理或稍后重试")
	}
	var at time.Time
	if seconds, err := strconv.ParseInt(after, 10, 64); err == nil && seconds >= 0 && seconds <= int64((1<<63-1)/time.Second) {
		at = now.Add(time.Duration(seconds) * time.Second)
	} else if date, err := http.ParseTime(after); err == nil {
		at = date
	}
	if exhausted {
		// Unix seconds through 9999-12-31 fit the RFC3339 native bridge field.
		if seconds, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil && seconds > 0 && seconds <= 253402300799 {
			if reset := time.Unix(seconds, 0); reset.After(at) {
				at = reset
			}
		}
	}
	if !at.After(now) {
		at = now.Add(5 * time.Minute)
	}
	return &retryError{at: at}
}
