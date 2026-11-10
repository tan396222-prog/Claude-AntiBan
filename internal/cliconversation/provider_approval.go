package cliconversation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

// Input is the complete proposed tool input. Approve receives a copy so its
// decision cannot change the arguments which are sent back to Claude Code.
type PermissionRequest struct {
	Tool  string
	Input json.RawMessage
}

const maxApprovalBytes = 8 << 10
const initRequestID = "cage-cli-initialize"

// Claude Code uses the same bidirectional JSONL control protocol as its Agent
// SDK. In supervised mode a second framing layer keeps the guest process lease
// open after the CLI's input is closed at the end of a successful turn.
type approvalStream struct {
	ctx        context.Context
	cancel     context.CancelFunc
	input      io.WriteCloser
	supervised bool
	approve    func(context.Context, PermissionRequest) bool
	turn       Turn

	mu       sync.Mutex // Serializes control frames, answers, and end-of-input.
	ready    bool
	ended    bool
	pending  map[string]context.CancelFunc
	seen     map[string]bool
	workers  sync.WaitGroup
	writeErr error
}

func (p *approvalStream) expireInitialization() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.ready && p.ctx.Err() == nil {
		p.writeErr = errors.New("客机 Claude Code 未在一分钟内确认逐次审批，请检查 CLI 版本")
		p.cancel()
	}
}

func (p *approvalStream) failure() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.writeErr
}

func (p *approvalStream) begin() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.supervised {
		if err := json.NewEncoder(p.input).Encode(map[string]bool{"stream": true}); err != nil {
			return errors.New("无法开始客机审批通道")
		}
	}
	return p.sendLocked(map[string]any{"type": "control_request", "request_id": initRequestID,
		"request": map[string]any{"subtype": "initialize", "hooks": map[string]any{}}})
}

func (p *approvalStream) sendLocked(frame any) error {
	if p.supervised {
		frame = map[string]any{"input": frame}
	}
	if err := json.NewEncoder(p.input).Encode(frame); err != nil {
		p.writeErr = errors.New("Claude Code 审批通道已断开")
		p.cancel()
		return p.writeErr
	}
	return nil
}

type controlFrame struct {
	RequestID string `json:"request_id"`
	Request   struct {
		Subtype string          `json:"subtype"`
		Tool    string          `json:"tool_name"`
		Input   json.RawMessage `json:"input"`
	} `json:"request"`
	Response struct {
		Subtype   string `json:"subtype"`
		RequestID string `json:"request_id"`
	} `json:"response"`
}

func (p *approvalStream) control(kind string, line []byte) error {
	var frame controlFrame
	if err := json.Unmarshal(line, &frame); err != nil {
		return errors.New("Claude Code 审批消息格式无效")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ended {
		return nil
	}
	if kind == "control_response" {
		if p.ready || frame.Response.RequestID != initRequestID || frame.Response.Subtype != "success" {
			return errors.New("客机 Claude Code 无法初始化逐次审批，请检查 CLI 版本")
		}
		p.ready = true
		return p.sendLocked(map[string]any{"type": "user", "session_id": p.turn.SessionID, "parent_tool_use_id": nil,
			"message": map[string]string{"role": "user", "content": p.turn.Prompt}})
	}
	id := frame.RequestID
	if id == "" || len(id) > 256 {
		return errors.New("Claude Code 审批请求标识无效")
	}
	if kind == "control_cancel_request" {
		if cancel := p.pending[id]; cancel != nil {
			delete(p.pending, id)
			cancel()
		}
		return nil
	}
	if !p.ready || p.seen[id] {
		return errors.New("Claude Code 审批请求重复或未初始化")
	}
	if len(p.seen) >= 1024 || len(p.pending) >= 16 {
		return errors.New("本轮工具请求超出容量，已停止执行")
	}
	p.seen[id] = true
	if frame.Request.Subtype != "can_use_tool" {
		return p.sendLocked(map[string]any{"type": "control_response", "response": map[string]string{
			"subtype": "error", "request_id": id, "error": "Unsupported control request"}})
	}
	request := PermissionRequest{Tool: frame.Request.Tool, Input: frame.Request.Input}
	// json.Unmarshal validated the complete frame; individual tool inputs must
	// be objects. An oversized input must never become a truncated approval.
	if !toolNamePattern.MatchString(request.Tool) || len(request.Input) == 0 || len(request.Input) > maxApprovalBytes || request.Input[0] != '{' {
		return p.answerLocked(id, false, nil)
	}
	ctx, cancel := context.WithCancel(p.ctx)
	p.pending[id] = cancel
	p.workers.Add(1)
	go func() {
		defer p.workers.Done()
		defer cancel()
		allowed := p.approve(ctx, PermissionRequest{Tool: request.Tool, Input: append(json.RawMessage(nil), request.Input...)})
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.pending[id] == nil || ctx.Err() != nil || p.ended {
			return
		}
		delete(p.pending, id)
		_ = p.answerLocked(id, allowed, request.Input)
	}()
	return nil
}

func (p *approvalStream) answerLocked(id string, allowed bool, input json.RawMessage) error {
	answer := map[string]any{"behavior": "deny", "message": "Cage did not approve this tool call."}
	if allowed {
		answer = map[string]any{"behavior": "allow", "updatedInput": input}
	}
	return p.sendLocked(map[string]any{"type": "control_response", "response": map[string]any{
		"subtype": "success", "request_id": id, "response": answer}})
}

func (p *approvalStream) finish() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ended = true
	for _, cancel := range p.pending {
		cancel()
	}
	if !p.ready {
		return errors.New("客机 Claude Code 未确认逐次审批协议")
	}
	if p.writeErr != nil {
		return p.writeErr
	}
	if p.supervised {
		if err := json.NewEncoder(p.input).Encode(map[string]bool{"finish": true}); err != nil {
			return errors.New("无法结束 Claude Code 客机输入")
		}
		return nil
	}
	if err := p.input.Close(); err != nil {
		return errors.New("无法结束 Claude Code 输入")
	}
	return nil
}
