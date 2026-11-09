package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"
)

type vaultBrowserInput struct {
	Action    string   `json:"action"`
	Tab       string   `json:"tab"`
	ID        string   `json:"id"`
	Candidate string   `json:"candidate"`
	Document  string   `json:"document"`
	Origin    string   `json:"origin"`
	Lease     string   `json:"lease,omitempty"`
	Username  string   `json:"username,omitempty"`
	Password  string   `json:"password,omitempty"`
	Blocked   []string `json:"blocked,omitempty"`
}
type vaultCandidate struct {
	ID       string `json:"id"`
	Tab      string `json:"tab"`
	Origin   string `json:"origin"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	Update   bool   `json:"update"`
}
type vaultBrowserState struct {
	Available  bool             `json:"available"`
	Origin     string           `json:"origin"`
	Document   string           `json:"document"`
	Candidates []vaultCandidate `json:"candidates"`
	Entries    []vaultEntry     `json:"entries"`
}

func (s *Server) vaultList(w http.ResponseWriter, r *http.Request) {
	var out vaultData
	err := s.account.withVault(r.Context(), false, func(db *vaultData) error {
		out = *db
		for i := range out.Entries {
			out.Entries[i].Password = ""
		}
		return nil
	})
	if err != nil {
		requestError(w, err)
		return
	}
	jsonReply(w, out)
}
func (s *Server) vaultMutation(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Action          string     `json:"action"`
		Entry           vaultEntry `json:"entry"`
		ID              string     `json:"id"`
		Origin          string     `json:"origin"`
		AccountPassword string     `json:"account_password"`
	}
	if !readJSON(w, r, &input, 32<<10) {
		return
	}
	if input.Action == "reveal" {
		if err := s.account.verifyVaultPassword(input.AccountPassword); err != nil {
			requestError(w, err)
			return
		}
	}
	result := map[string]any{"ok": true}
	err := s.account.withVault(r.Context(), input.Action != "reveal", func(db *vaultData) error {
		switch input.Action {
		case "save":
			return db.save(input.Entry)
		case "delete":
			db.Entries = slices.DeleteFunc(db.Entries, func(e vaultEntry) bool { return e.ID == input.ID })
		case "unblock":
			db.Blocked = slices.DeleteFunc(db.Blocked, func(origin string) bool { return origin == input.Origin })
		case "reveal":
			for _, entry := range db.Entries {
				if entry.ID == input.ID {
					result["password"] = entry.Password
					return nil
				}
			}
			return errors.New("密码记录不存在")
		default:
			return errors.New("密码保险箱操作无效")
		}
		return nil
	})
	if err != nil {
		requestError(w, err)
		return
	}
	jsonReply(w, result)
}

func (s *Server) guestVault(ctx context.Context, input vaultBrowserInput, out any) error {
	s.mu.Lock()
	ready, token := s.ready, s.credentials.Token
	s.mu.Unlock()
	if !ready {
		return errBrowserNotReady
	}
	body, _ := json.Marshal(input)
	defer clear(body)
	data, err := s.guestAPI(ctx, token, http.MethodPost, "/vault", body)
	defer clear(data)
	if err != nil {
		return errors.New("浏览器密码功能暂不可用，请更新环境组件后重试")
	}
	var failure struct {
		Error string `json:"vaultError"`
	}
	if json.Unmarshal(data, &failure) == nil && failure.Error != "" {
		switch failure.Error {
		case "登录记录已过期，请重新登录", "当前网页已切换", "未找到可填写的登录框，或页面已变化", "保险箱已锁定，请重试", "登录页面已变化，请稍后重试":
			return errors.New(failure.Error)
		default:
			return errors.New("网页未能处理密码操作，请刷新后重试")
		}
	}
	if json.Unmarshal(data, out) != nil {
		return errors.New("浏览器密码响应无效")
	}
	return nil
}

func (s *Server) vaultBrowser(w http.ResponseWriter, r *http.Request) {
	var input vaultBrowserInput
	if !readJSON(w, r, &input, 16<<10) {
		return
	}
	// Passwords and origins used for fill always come from the host database.
	input.Password, input.Username, input.Origin, input.Lease, input.Blocked = "", "", "", "", nil
	s.account.mu.Lock()
	lease := s.account.vaultLease
	s.account.mu.Unlock()
	input.Lease = lease
	var result any = map[string]bool{"ok": true}
	err := s.account.withVault(r.Context(), input.Action == "save" || input.Action == "never", func(db *vaultData) error {
		if lease == "" || lease != s.account.vaultLease {
			return errors.New("请重新解锁后重试")
		}
		switch input.Action {
		case "status":
			input.Blocked = db.Blocked
			state := vaultBrowserState{Candidates: []vaultCandidate{}, Entries: []vaultEntry{}}
			if err := s.guestVault(r.Context(), input, &state); err != nil {
				result = map[string]any{"available": false, "message": err.Error(), "candidates": []vaultCandidate{}, "entries": []vaultEntry{}}
				return nil
			}
			pending := []vaultCandidate{}
			for _, candidate := range state.Candidates {
				if slices.Contains(db.Blocked, candidate.Origin) {
					continue
				}
				same := false
				for _, entry := range db.Entries {
					if entry.Origin == candidate.Origin && entry.Username == candidate.Username {
						candidate.Update = true
						same = entry.Password == candidate.Password
					}
				}
				candidate.Password = ""
				if !same {
					pending = append(pending, candidate)
				}
			}
			state.Candidates = pending
			state.Entries = []vaultEntry{}
			for _, entry := range db.Entries {
				if entry.Origin == state.Origin {
					entry.Password = ""
					state.Entries = append(state.Entries, entry)
				}
			}
			result = state
		case "fill":
			for _, entry := range db.Entries {
				if entry.ID == input.ID {
					input.Origin, input.Username, input.Password = entry.Origin, entry.Username, entry.Password
					var reply map[string]any
					return s.guestVault(r.Context(), input, &reply)
				}
			}
			return errors.New("密码记录不存在")
		case "save", "never":
			request := input
			request.Action = "candidate"
			var candidate vaultCandidate
			if err := s.guestVault(r.Context(), request, &candidate); err != nil {
				return err
			}
			origin, err := vaultOrigin(candidate.Origin)
			if err != nil || origin != candidate.Origin || candidate.ID != input.Candidate {
				return errors.New("登录记录已失效，请重新登录")
			}
			if input.Action == "never" {
				if !slices.Contains(db.Blocked, origin) {
					db.Blocked = append(db.Blocked, origin)
				}
				return nil
			}
			entry := vaultEntry{Origin: origin, Username: candidate.Username, Password: candidate.Password}
			for _, old := range db.Entries {
				if old.Origin == origin && old.Username == entry.Username {
					entry.ID = old.ID
				}
			}
			return db.save(entry)
		case "dismiss":
			var reply map[string]any
			return s.guestVault(r.Context(), input, &reply)
		default:
			return errors.New("密码保险箱操作无效")
		}
		return nil
	})
	if err != nil {
		requestError(w, err)
		return
	}
	if input.Action == "save" || input.Action == "never" {
		input.Action = "dismiss"
		var ignored map[string]any
		_ = s.guestVault(r.Context(), input, &ignored)
	}
	jsonReply(w, result)
}

func (s *Server) clearBrowserVault(lease string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var out map[string]any
	_ = s.guestVault(ctx, vaultBrowserInput{Action: "lock", Lease: lease}, &out)
}
