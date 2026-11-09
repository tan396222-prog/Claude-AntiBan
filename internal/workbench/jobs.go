package workbench

import (
	"context"
	"errors"
	"fmt"
	"github.com/sylvinus/agent-vm/internal/diagnostics"
	"runtime/debug"
	"strings"
	"sync"
)

type Job struct {
	SetupProgress *SetupProgress `json:"setup_progress,omitempty"`
	ID            string         `json:"id"`
	Kind          string         `json:"kind"`
	Running       bool           `json:"running"`
	Message       string         `json:"message"`
	Error         string         `json:"error,omitempty"`
	Log           string         `json:"log,omitempty"`
}

var (
	errJobBusy    = errors.New("另一个操作仍在进行，请稍候")
	errJobsClosed = errors.New("后台正在更新，请重新打开 Cage。")
)

// jobManager owns accepted work independently of HTTP requests. The operation
// lock is shared with synchronous workbench operations and monitoring.
type jobManager struct {
	mu           sync.Mutex
	state        Job
	cancel       context.CancelFunc
	active       *jobExecution
	afterSuccess func() error
	closed       bool
	workers      sync.WaitGroup
}

// prepare runs synchronously only for a new admission. Neither prepare nor
// afterSuccess may call this manager. afterSuccess must not acquire Server.mu:
// submission can hold Server.mu while waiting for the manager's lock.
func (m *jobManager) submit(operation sync.Locker, kind string, pauseVersion uint64, prepare func() error, afterSuccess func() error, run func() error) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Job{}, errJobsClosed
	}
	if m.state.Running {
		if kind == "stop" && m.state.Kind == "stop" {
			// Logout can join a pending stop and request account locking once
			// that same stop succeeds. A plain repeated stop cannot undo it.
			if afterSuccess != nil {
				m.afterSuccess = afterSuccess
			}
			return m.state, nil
		}
		if kind != "stop" {
			return Job{}, errJobBusy
		}
	}
	if prepare != nil {
		if err := prepare(); err != nil {
			return Job{}, err
		}
	}
	if m.cancel != nil {
		m.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.afterSuccess = afterSuccess
	m.state = Job{ID: randomToken()[:12], Kind: kind, Running: true, Message: "正在准备…"}
	execution := &jobExecution{owner: m, id: m.state.ID, ctx: ctx, pauseVersion: pauseVersion}
	execution.log = diagnostics.NewStream(diagnostics.Default(), "task/"+kind, "info", func(line string) {
		if m.state.ID == execution.id {
			if m.state.SetupProgress != nil {
				if p := parseSetupProgress(line); p != nil {
					m.state.SetupProgress = p
					if strings.HasPrefix(line, "CAGE_") {
						return
					}
				}
			}
			m.state.Log += line
			if len(m.state.Log) > 24000 {
				m.state.Log = m.state.Log[len(m.state.Log)-24000:]
			}
		}
	})
	diagnostics.Log("info", "task", kind+" started")
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		defer cancel()
		operation.Lock()
		defer operation.Unlock()
		m.mu.Lock()
		if m.state.ID != execution.id {
			m.mu.Unlock()
			return
		}
		if ctx.Err() != nil {
			m.finishLocked(execution, ctx.Err())
			m.mu.Unlock()
			return
		}
		m.active = execution
		m.mu.Unlock()
		err := func() (err error) {
			defer func() {
				if failure := recover(); failure != nil {
					diagnostics.Log("error", "task", fmt.Sprintf("panic: %v\n%s", failure, debug.Stack()))
					panic(failure)
				}
			}()
			return run()
		}()
		m.mu.Lock()
		defer m.mu.Unlock()
		m.finishLocked(execution, err)
	}()
	return m.state, nil
}

func (m *jobManager) finishLocked(execution *jobExecution, err error) {
	if execution.log != nil {
		_ = execution.log.Close()
	}
	if m.active == execution {
		m.active = nil
	}
	if m.state.ID != execution.id {
		return
	}
	if err == nil {
		err = execution.ctx.Err()
	}
	if err == nil && m.afterSuccess != nil {
		err = m.afterSuccess()
	}
	m.cancel, m.afterSuccess = nil, nil
	m.state.Running = false
	switch {
	case errors.Is(err, context.Canceled):
		m.state.Message = "已取消"
		diagnostics.Log("info", "task", m.state.Kind+" canceled")
	case err != nil:
		m.state.Error = err.Error()
		m.state.Message = "操作未完成"
		diagnostics.Log("error", "task", m.state.Kind+": "+err.Error())
	default:
		m.state.Message = "已完成"
		diagnostics.Log("info", "task", m.state.Kind+" completed")
	}
}

func (m *jobManager) snapshot() Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// execution returns the operation that is actually running, including an old
// operation that has been cancelled by a stop but has not returned yet.
func (m *jobManager) execution() *jobExecution {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active != nil {
		return m.active
	}
	return &jobExecution{owner: m, ctx: context.Background()}
}

func (m *jobManager) cancelRunning() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Kind != "stop" && m.cancel != nil {
		m.cancel()
	}
	if m.active != nil {
		return m.active.id
	}
	return ""
}

// close seals admission before waiting, so no worker can be added concurrently
// with Wait. Superseded workers are included even while waiting for operation.
func (m *jobManager) close() {
	m.mu.Lock()
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	m.workers.Wait()
}

type jobExecution struct {
	owner        *jobManager
	id           string
	ctx          context.Context
	pauseVersion uint64
	log          *diagnostics.Stream
}

func (e *jobExecution) message(message string) {
	e.owner.mu.Lock()
	defer e.owner.mu.Unlock()
	if e.id != "" && e.owner.state.ID == e.id && e.owner.state.Running {
		e.owner.state.Message = message
	}
}

func (e *jobExecution) Write(data []byte) (int, error) {
	e.owner.mu.Lock()
	defer e.owner.mu.Unlock()
	if e.id != "" && e.owner.state.ID == e.id && e.owner.state.Running {
		if e.log != nil {
			_, _ = e.log.Write(data)
		}
	}
	return len(data), nil
}
