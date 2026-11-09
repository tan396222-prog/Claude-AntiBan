package workbench

import (
	"net/http"
)

func (s *Server) accountStatus(w http.ResponseWriter, r *http.Request) {
	v := s.account.viewFor(s.hasTrustedView(r))
	v.BackgroundRunning = s.vmRunning()
	jsonReply(w, v)
}

func (s *Server) accountAction(w http.ResponseWriter, r *http.Request) {
	if s.account == nil {
		http.Error(w, "本地账户尚未初始化", 503)
		return
	}
	action := r.PathValue("action")
	if action != "create" && action != "unlock" && action != "profile" && action != "password" && action != "lock" && action != "logout" && action != "suspend" && action != "device-enroll" && action != "device-revoke" && action != "device-unlock" {
		http.NotFound(w, r)
		return
	}
	if action != "create" && action != "unlock" && action != "device-unlock" {
		if _, ok := s.account.accessFor(s.hasTrustedView(r)); !ok {
			http.Error(w, "请先解锁 Cage", http.StatusLocked)
			return
		}
	}
	if (action == "lock" || action == "logout" || action == "device-enroll") && s.account.view().TrustedLocal {
		http.Error(w, "请先在本机的「设置 → 账户」设置密码", http.StatusConflict)
		return
	}
	var input struct {
		Username        string           `json:"username"`
		Password        string           `json:"password"`
		ConfirmPassword string           `json:"confirm_password"`
		OldPassword     string           `json:"old_password"`
		Avatar          string           `json:"avatar"`
		Kind            string           `json:"kind"`
		Credential      deviceCredential `json:"credential"`
	}
	if !readJSON(w, r, &input, 3<<20) {
		return
	}
	s.account.mu.Lock()
	vaultLease := s.account.vaultLease
	s.account.mu.Unlock()
	var err error
	switch action {
	case "create":
		if input.Password != input.ConfirmPassword {
			http.Error(w, "两次密码不一致", 400)
			return
		}
		err = s.account.create(input.Username, input.Password, input.Avatar)
	case "unlock":
		err = s.account.unlock(input.Password)
	case "profile":
		err = s.account.updateProfile(input.Username, input.Avatar)
	case "password":
		if input.Password != input.ConfirmPassword {
			http.Error(w, "两次密码不一致", 400)
			return
		}
		err = s.account.changePassword(input.OldPassword, input.Password)
	case "lock":
		err = s.account.lockAccess(false)
	case "suspend":
		s.account.lock()
	case "device-enroll":
		var credential deviceCredential
		credential, err = s.account.enrollDevice(input.Kind)
		if err == nil {
			jsonReply(w, credential)
			return
		}
	case "device-revoke":
		err = s.account.revokeDevice(input.Kind)
	case "device-unlock":
		err = s.account.unlockDevice(input.Kind, input.Credential)
	case "logout":
		s.requestStop(w, true)
		return
	}
	if err != nil {
		requestError(w, err)
		return
	}
	if action == "lock" || action == "suspend" {
		s.clearBrowserVault(vaultLease)
	}
	s.accountStatus(w, r)
}
