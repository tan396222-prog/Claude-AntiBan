package cliconversation

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type approvalCapture struct{ bytes.Buffer }

func (*approvalCapture) Close() error { return nil }

func TestDecisionsFollowTheOriginalRequestThroughSupervisor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wire := new(approvalCapture)
	release := make(chan struct{})
	p := &approvalStream{
		ctx: ctx, cancel: cancel, input: wire, supervised: true, turn: Turn{SessionID: testSessionID, Prompt: "fixture"},
		pending: make(map[string]context.CancelFunc), seen: make(map[string]bool),
		approve: func(ctx context.Context, r PermissionRequest) bool {
			var args struct{ Command string }
			if err := json.Unmarshal(r.Input, &args); err != nil {
				t.Error(err)
				return false
			}
			switch args.Command {
			case "first":
				<-release // A second decision may arrive first.
				clear(r.Input)
				return true
			case "second":
				return false
			case "revoked":
				<-ctx.Done()
				return true // A late allow after CLI cancellation must have no effect.
			default:
				t.Error("oversized input reached the approval UI")
				return false
			}
		},
	}
	if err := p.begin(); err != nil {
		t.Fatal(err)
	}
	if err := p.control("control_response", []byte(`{"response":{"subtype":"success","request_id":"wrong-init"}}`)); err == nil {
		t.Fatal("accepted an unrelated initialization reply")
	}
	if err := p.control("control_response", []byte(`{"response":{"subtype":"success","request_id":"cage-cli-initialize"}}`)); err != nil {
		t.Fatal(err)
	}
	request := func(id, command string) error {
		line, _ := json.Marshal(map[string]any{"request_id": id, "request": map[string]any{
			"subtype": "can_use_tool", "tool_name": "Bash", "input": map[string]string{"command": command},
		}})
		return p.control("control_request", line)
	}
	for id, command := range map[string]string{"one": "first", "two": "second", "old": "revoked", "large": strings.Repeat("x", maxApprovalBytes)} {
		if err := request(id, command); err != nil {
			t.Fatal(err)
		}
	}
	if err := request("one", "first"); err == nil {
		t.Fatal("accepted a duplicate approval ID")
	}
	if err := p.control("control_cancel_request", []byte(`{"request_id":"old"}`)); err != nil {
		t.Fatal(err)
	}
	close(release)
	p.workers.Wait()
	if err := p.finish(); err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(wire.Bytes()), []byte("\n"))
	if string(lines[0]) != `{"stream":true}` || string(lines[len(lines)-1]) != `{"finish":true}` {
		t.Fatal("supervisor did not get explicit stream start and finish")
	}
	decisions := map[string]string{}
	for _, line := range lines[1 : len(lines)-1] {
		var packet struct {
			Input struct {
				Type     string `json:"type"`
				Response struct {
					ID     string `json:"request_id"`
					Answer struct {
						Behavior string                   `json:"behavior"`
						Updated  struct{ Command string } `json:"updatedInput"`
					} `json:"response"`
				} `json:"response"`
			} `json:"input"`
		}
		if err := json.Unmarshal(line, &packet); err != nil || packet.Input.Type == "" {
			t.Fatalf("invalid supervisor input: %s", line)
		}
		if packet.Input.Type == "control_response" {
			response := packet.Input.Response
			if _, duplicate := decisions[response.ID]; duplicate {
				t.Fatal("sent two answers for one request")
			}
			decisions[response.ID] = response.Answer.Behavior + ":" + response.Answer.Updated.Command
		}
	}
	if len(decisions) != 3 || decisions["one"] != "allow:first" || decisions["two"] != "deny:" || decisions["large"] != "deny:" {
		t.Errorf("approval crossed requests, accepted a late decision, or changed input: %#v", decisions)
	}
}
