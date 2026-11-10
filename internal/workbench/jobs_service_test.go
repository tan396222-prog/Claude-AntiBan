package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLogoutJoinsRunningStopAndLocksOnlyAfterSuccess(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			s := testServer(t)
			started, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			finish := func() { once.Do(func() { close(release) }) }
			t.Cleanup(func() { finish(); s.jobs.close() })
			job, err := s.submitJob("stop", func() error {
				close(started)
				<-release
				if fail {
					return errors.New("could not stop environment")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			awaitJobSignal(t, started)
			response := request(s, "POST", "/api/account/logout", s.origin, "{}", true)
			if response.Code != http.StatusOK {
				t.Fatalf("logout failed: %d %s", response.Code, response.Body.String())
			}
			var joined Job
			if err := json.Unmarshal(response.Body.Bytes(), &joined); err != nil {
				t.Fatal(err)
			}
			if joined.ID != job.ID || joined.Kind != "stop" || !joined.Running {
				t.Fatalf("logout did not join existing stop: %+v, original %+v", joined, job)
			}
			if !s.account.view().Unlocked {
				t.Fatal("logout locked account before stop completed")
			}
			finish()
			state := awaitJobIdle(t, &s.jobs)
			view := s.account.view()
			if fail {
				if !view.Unlocked || view.RequireAuthentication || !strings.Contains(state.Error, "停止环境失败") {
					t.Fatalf("failed stop changed account access: %+v, job %+v", view, state)
				}
				return
			}
			if view.Unlocked || !view.RequireAuthentication || state.Error != "" {
				t.Fatalf("successful stop did not lock account: %+v, job %+v", view, state)
			}
			data, err := os.ReadFile(s.account.path)
			if err != nil {
				t.Fatal(err)
			}
			var persisted accountRecord
			if err := json.Unmarshal(data, &persisted); err != nil {
				t.Fatal(err)
			}
			if !persisted.RequireAuthentication || len(persisted.Devices) != 0 {
				t.Fatal("successful logout did not persist account lock")
			}
		})
	}
}

func TestAcceptedJobOutlivesSubmittingHTTPRequest(t *testing.T) {
	s := testServer(t)
	requestContext := make(chan context.Context, 1)
	jobContext := make(chan context.Context, 1)
	release := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	t.Cleanup(func() { finish(); s.jobs.close() })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestContext <- r.Context()
		s.launch(w, "settings", func() error {
			ctx := s.operationContext()
			jobContext <- ctx
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-release:
				return nil
			}
		})
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("job submission failed: %d", response.StatusCode)
	}
	cancel()
	awaitContext := func(contexts <-chan context.Context) context.Context {
		t.Helper()
		select {
		case value := <-contexts:
			return value
		case <-time.After(3 * time.Second):
			t.Fatal("request or accepted job did not start")
			return nil
		}
	}
	acceptedRequest := awaitContext(requestContext)
	awaitJobSignal(t, acceptedRequest.Done())
	acceptedJob := awaitContext(jobContext)
	if err := acceptedJob.Err(); err != nil {
		t.Fatalf("request completion cancelled accepted job: %v", err)
	}
	if !s.jobs.snapshot().Running {
		t.Fatal("accepted job ended with request")
	}
	finish()
	state := awaitJobIdle(t, &s.jobs)
	if state.Error != "" || state.Message != "已完成" {
		t.Fatalf("independent job failed: %+v", state)
	}
}
