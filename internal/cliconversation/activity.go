package cliconversation

import (
	"encoding/json"
	"strings"
)

// ToolActivity is a display-only summary. Command bodies, file contents and
// tool outputs stay in the CLI transcript. Approval has a separate protocol.
type ToolActivity struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	Status string `json:"status"`
}

func updateTools(tools []ToolActivity, role string, line []byte) ([]ToolActivity, bool) {
	var msg struct {
		Message struct {
			Content []struct {
				Type      string                     `json:"type"`
				ID        string                     `json:"id"`
				Name      string                     `json:"name"`
				Input     map[string]json.RawMessage `json:"input"`
				ToolUseID string                     `json:"tool_use_id"`
				IsError   bool                       `json:"is_error"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &msg) != nil {
		return tools, false
	}
	changed := false
	for _, b := range msg.Message.Content {
		if role == "user" && b.Type == "tool_result" {
			for i := range tools {
				if tools[i].ID == b.ToolUseID && tools[i].Status == "called" {
					tools[i].Status = "completed"
					if b.IsError {
						tools[i].Status = "failed"
					}
					changed = true
					break
				}
			}
		}
		if role != "assistant" || (b.Type != "tool_use" && b.Type != "server_tool_use") || !toolNamePattern.MatchString(b.Name) || b.ID == "" || len(b.ID) > 128 || len(tools) >= 64 {
			continue
		}
		duplicate := false
		for _, item := range tools {
			if item.ID == b.ID {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		detail := ""
		for _, key := range []string{"description", "subject", "file_path", "pattern", "query", "skill", "subagent_type"} {
			var value string
			if json.Unmarshal(b.Input[key], &value) == nil && strings.TrimSpace(value) != "" {
				runes := []rune(strings.TrimSpace(value))
				detail = string(runes[:min(len(runes), 160)])
				break
			}
		}
		tools = append(tools, ToolActivity{ID: b.ID, Name: b.Name, Detail: detail, Status: "called"})
		changed = true
	}
	return tools, changed
}
