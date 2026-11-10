package appupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestUpdateSourceRetryInstructions(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name    string
		status  int
		headers http.Header
		wait    time.Duration
	}{
		{"seconds", 429, http.Header{"Retry-After": {"3600"}}, time.Hour},
		{"date", 429, http.Header{"Retry-After": {now.Add(2 * time.Hour).Format(http.TimeFormat)}}, 2 * time.Hour},
		{"primary", 403, http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {strconv.FormatInt(now.Add(time.Hour).Unix(), 10)}}, time.Hour},
		{"both", 429, http.Header{"Retry-After": {"60"}, "X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {strconv.FormatInt(now.Add(time.Hour).Unix(), 10)}}, time.Hour},
		{"malformed", 429, http.Header{"Retry-After": {"9999999999999999999999"}}, 5 * time.Minute},
		{"past", 429, http.Header{"Retry-After": {now.Add(-time.Hour).Format(http.TimeFormat)}}, 5 * time.Minute},
		{"forbidden", 403, nil, 0},
		{"remaining", 403, http.Header{"X-Ratelimit-Remaining": {"10"}, "X-Ratelimit-Reset": {strconv.FormatInt(now.Add(time.Hour).Unix(), 10)}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: tc.headers, Body: io.NopCloser(strings.NewReader("private response must stay out of errors")), Request: r}, nil
			})}
			_, err := findRelease(context.Background(), client, "0.1.0")
			if err == nil || strings.Contains(err.Error(), "private response") {
				t.Fatalf("unsafe error: %v", err)
			}
			var delayed interface{ RetryAt() time.Time }
			if tc.wait == 0 {
				if errors.As(err, &delayed) || !strings.Contains(err.Error(), "403") {
					t.Fatalf("ordinary forbidden misclassified: %v", err)
				}
				return
			}
			if !errors.As(err, &delayed) {
				t.Fatalf("retry deadline lost: %v", err)
			}
			if delta := delayed.RetryAt().Sub(now.Add(tc.wait)); delta < -time.Second || delta > 5*time.Second {
				t.Fatalf("wrong retry deadline: %s", delayed.RetryAt())
			}
			state := State{}
			state.fail(fmt.Errorf("wrapped: %w", err))
			data, _ := json.Marshal(state)
			var wire map[string]any
			_ = json.Unmarshal(data, &wire)
			if wire["retryAt"] != delayed.RetryAt().UTC().Format(time.RFC3339) {
				t.Fatalf("native protocol lost deadline: %s", data)
			}
			state.fail(errors.New("unrelated failure"))
			data, _ = json.Marshal(state)
			if !strings.Contains(string(data), `"retryAt":""`) {
				t.Fatalf("stale deadline retained by merging native bridge: %s", data)
			}
		})
	}
}
