package workbench

import (
	"net/http"
	"runtime"

	"github.com/sylvinus/agent-vm/internal/guestos"
)

func (s *Server) macAccountHost() bool {
	return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" && s.localVMStorage()
}

func (s *Server) accountStatus(w http.ResponseWriter, r *http.Request) {
	v := s.account.viewFor(s.hasTrustedView(r))
	v.MacOSAccountHost = s.macAccountHost()
	v.BackgroundRunning = s.vmRunning()
	if s.macosGuest != nil {
		v.GuestSystem = "macos"
		v.InitialMacSetup = s.macSetupPending()
		v.MacOSGuestUser = s.macosGuest.User
	}
	if s.windowsGuest != nil {
		v.GuestSystem = "windows-11"
	}
	if v.MacOSAccountHost && v.Unlocked && !v.MacOSCredentialsReady && v.MacOSGuestUser == "" {
		if install, err := guestos.LoadInstallation(s.profile); err == nil && install != nil {
			if previous, err := s.account.legacyMacGuestPassword(r.Context(), install.ID); err == nil && previous != "" {
				v.MacOSGuestUser = "cage"
			}
		}
	}
	jsonReply(w, v)
}

func (s *Server) accountAction(w http.ResponseWriter, r *http.Request) {
	if s.account == nil {
		http.Error(w, "本地账户尚未初始化", 503)
		return
	}
	action := r.PathValue("action")
	if action != "prepare-macos" && action != "create" && action != "unlock" && action != "profile" && action != "password" && action != "lock" && action != "logout" && action != "suspend" && action != "device-enroll" && action != "device-revoke" && action != "device-unlock" {
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
	if action == "password" || action == "prepare-macos" {
		if !s.op.TryLock() {
			http.Error(w, "环境操作仍在进行，请等待完成后设置密码", http.StatusConflict)
			return
		}
		defer s.op.Unlock()
		if s.busy() {
			http.Error(w, "环境操作仍在进行，请等待完成后设置密码", http.StatusConflict)
			return
		}
	}
	switch action {
	case "create":
		if input.Password != input.ConfirmPassword {
			http.Error(w, "两次密码不一致", 400)
			return
		}
		err = s.account.createAccount(input.Username, input.Password, input.Avatar, s.macAccountHost())
	case "prepare-macos":
		if !s.macAccountHost() || !s.hasTrustedView(r) {
			http.Error(w, "请在本机 Cage 窗口设置共用账密", http.StatusForbidden)
			return
		}
		if input.Password != input.ConfirmPassword {
			http.Error(w, "两次密码不一致", http.StatusBadRequest)
			return
		}
		guests := map[string]macGuestCredential{}
		install, loadErr := guestos.LoadInstallation(s.profile)
		if loadErr != nil {
			requestError(w, loadErr)
			return
		}
		if s.macosGuest != nil && install == nil {
			http.Error(w, "原客机安装记录缺失，请恢复备份后继续", http.StatusConflict)
			return
		}
		if install != nil {
			previous, loadErr := s.account.savedMacGuestPassword(r.Context(), install.ID)
			if loadErr != nil {
				requestError(w, loadErr)
				return
			}
			if s.macosGuest != nil && previous == "" {
				http.Error(w, "原客机凭据缺失，请恢复加密保险箱后继续", http.StatusConflict)
				return
			}
			if previous != "" {
				user := "cage"
				if s.macosGuest != nil {
					user = s.macosGuest.User
				}
				if input.Username != user {
					http.Error(w, "请使用现有客机用户名；安装记录和原文件已保留", http.StatusConflict)
					return
				}
				guests[install.ID.String()] = macGuestCredential{Username: user, Password: previous}
			}
		}
		err = s.account.prepareMacAccount(input.Username, input.Password, guests)
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
	if (action == "password" || action == "prepare-macos") && s.macosGuest != nil && s.vmRunning() {
		// The account write already succeeded. A failed guest update remains a
		// durable pending state, rather than reporting that the old Cage password works.
		_ = s.syncMacGuestPassword(r.Context())
	}
	s.accountStatus(w, r)
}
