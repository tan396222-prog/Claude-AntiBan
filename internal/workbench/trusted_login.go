package workbench

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/sylvinus/agent-vm/internal/host"
)

// Only the local launcher possesses the controller token. Its independent
// cookie jar carries this proof; the browser and SSH tickets never receive it.
func trustedViewProof(token string) string {
	digest := sha256.Sum256([]byte("trusted desktop view\x00" + token))
	return hex.EncodeToString(digest[:])
}
func (s *Server) hasTrustedView(r *http.Request) bool {
	c, err := r.Cookie(s.cookieName() + "-desktop")
	return err == nil && equalSecret(c.Value, trustedViewProof(s.token))
}

func validTrustedID(id string) bool {
	if id == "" {
		return true
	}
	_, err := hex.DecodeString(id)
	return len(id) == 64 && err == nil
}

type trustedLoginRequest struct {
	Action       string `json:"action"`
	ConnectionID string `json:"connection_id,omitempty"`
	Token        string `json:"token,omitempty"`
	InstanceID   string `json:"instance_id,omitempty"`
	Password     string `json:"password,omitempty"`
}

// Domain errors travel as data through the authenticated launcher client, which
// deliberately discards HTTP error bodies. Neither credential reaches the web UI.
type trustedLoginReply struct {
	Error string `json:"error,omitempty"`
}

func (d *desktopGateway) trustedLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "需要 POST 请求", 405)
		return
	}
	var in trustedLoginRequest
	if !readJSON(w, r, &in) {
		return
	}
	d.change.Lock()
	defer d.change.Unlock()
	session, remote, err := d.acquire(in.ConnectionID)
	local, ok := d.local.(*desktopLocal)
	if err != nil || remote || !ok {
		http.Error(w, "请返回当前本机工作台", 409)
		return
	}
	c, err := openController(local.profile)
	if err != nil {
		http.Error(w, "本机后台无法连接", 503)
		return
	}
	defer c.close()
	if c.origin != session.Origin {
		http.Error(w, "本机后台已切换", 409)
		return
	}
	in.Token, in.InstanceID = c.record.Token, session.Manifest.InstanceID
	var reply trustedLoginReply
	status, err := c.callContext(r.Context(), "POST", "/api/desktop-local/trusted-login", in, &reply)
	if err != nil || status != http.StatusOK {
		http.Error(w, "本机后台未接受登录请求，请确认已更新 Cage", 409)
		return
	}
	if reply.Error != "" {
		http.Error(w, reply.Error, 409)
		return
	}
	jsonReply(w, map[string]bool{"ok": true})
}

func (s *Server) localTrustedLogin(w http.ResponseWriter, r *http.Request) {
	var in trustedLoginRequest
	if !readJSON(w, r, &in) {
		return
	}
	if s.account == nil || !equalSecret(in.Token, s.token) || in.InstanceID != s.instanceID {
		http.Error(w, "Launcher refused", http.StatusUnauthorized)
		return
	}
	_, finish, err := s.beginActivity(r.Context())
	if err != nil {
		http.Error(w, "后台正在更新", 503)
		return
	}
	defer finish()
	base, err := os.UserConfigDir()
	if runtime.GOOS == "windows" {
		base = os.Getenv("LOCALAPPDATA")
	}
	if !filepath.IsAbs(base) {
		err = errors.New("无法确定本机系统用户配置目录")
	}
	if err == nil {
		err = s.account.trustedLogin(in.Action, in.Password, filepath.Join(base, "Cage", "trusted-local"))
	}
	reply := trustedLoginReply{}
	if err != nil {
		reply.Error = err.Error()
	}
	jsonReply(w, reply)
}

func (a *localAccount) trustedLogin(action, password, devices string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch action {
	case "create":
		if a.record != nil {
			return errors.New("已有账户，请使用原登录方式")
		}
		record := &accountRecord{Version: 1, Username: "本机用户", TrustedDevice: randomToken()}
		secret := randomToken()
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return err
		}
		defer clear(key)
		if err := sealAccountKey(record, secret, key); err != nil {
			return err
		}
		path := filepath.Join(devices, record.TrustedDevice+".json")
		// Store the wrapping credential first. A crash cannot publish a record
		// whose credential has not been persisted.
		if err := writePrivate(path, secret); err != nil {
			return errors.New("无法保存当前系统用户的信任凭据")
		}
		if err := writePrivate(a.path, record); err != nil {
			_ = os.Remove(path)
			return errors.New("无法保存本机工作区")
		}
		a.record = record
		a.setKey(append([]byte(nil), key...))
		a.trustedSession = true
	case "enable":
		if a.record == nil || a.record.TrustedDevice != "" || len(a.key) != 32 {
			return errors.New("请先用本机账户登录")
		}
		if time.Now().Before(a.nextAttempt) {
			return errors.New("请稍后再试")
		}
		key, err := openAccountKey(a.record, password)
		if err != nil {
			a.nextAttempt = time.Now().Add(time.Second)
			return err
		}
		defer clear(key)
		a.nextAttempt = time.Time{}
		if a.record.TrustedUnlock != nil {
			return nil
		}
		secret := randomToken()
		raw, err := hex.DecodeString(secret)
		if err != nil {
			return err
		}
		defer clear(raw)
		aead, err := deviceAEAD(raw)
		if err != nil {
			return err
		}
		device := accountDevice{ID: randomToken(), Nonce: make([]byte, aead.NonceSize())}
		if _, err = rand.Read(device.Nonce); err != nil {
			return err
		}
		device.SealedKey = aead.Seal(nil, device.Nonce, key, []byte("trusted:"+device.ID))
		record := *a.record
		record.TrustedUnlock = &device
		path := filepath.Join(devices, device.ID+".json")
		if err = writePrivate(path, secret); err != nil {
			return errors.New("无法保存本机信任凭据")
		}
		if err = writePrivate(a.path, &record); err != nil {
			_ = os.Remove(path)
			return errors.New("本机信任未能保存")
		}
		a.record = &record
	case "disable":
		if a.record == nil || len(a.key) != 32 {
			return errors.New("请先解锁 Cage")
		}
		if a.record.TrustedUnlock == nil {
			return nil
		}
		record := *a.record
		record.TrustedUnlock = nil
		if err := writePrivate(a.path, &record); err != nil {
			return errors.New("本机信任未能关闭")
		}
		a.record = &record
	case "resume":
		if a.record == nil || (a.record.TrustedDevice == "" && a.record.TrustedUnlock == nil) || a.record.RequireAuthentication {
			return errors.New("请使用原有账户验证方式")
		}
		if len(a.key) == 32 {
			return nil
		}
		id := a.record.TrustedDevice
		if a.record.TrustedUnlock != nil {
			id = a.record.TrustedUnlock.ID
		}
		path := filepath.Join(devices, id+".json")
		if err := host.CheckPrivateFile(path); err != nil {
			return errors.New("找不到当前系统用户的私有信任凭据，请恢复备份")
		}
		data, err := os.ReadFile(path)
		var secret string
		if err != nil || len(data) > 128 || json.Unmarshal(data, &secret) != nil || len(secret) != 64 {
			return errors.New("本机信任凭据无法读取，请恢复备份")
		}
		var key []byte
		if device := a.record.TrustedUnlock; device != nil {
			raw, decodeErr := hex.DecodeString(secret)
			if decodeErr != nil {
				return errors.New("本机信任凭据无效")
			}
			defer clear(raw)
			aead, cipherErr := deviceAEAD(raw)
			if cipherErr != nil {
				return cipherErr
			}
			key, err = aead.Open(nil, device.Nonce, device.SealedKey, []byte("trusted:"+device.ID))
		} else {
			key, err = openAccountKey(a.record, secret)
		}
		if err != nil {
			return errors.New("信任凭据与工作区不匹配，请恢复备份")
		}
		a.setKey(key)
		a.trustedSession = true
	case "protect":
		if a.record == nil || a.record.TrustedDevice == "" || len(a.key) != 32 {
			return errors.New("请先在受信任的本机进入工作区")
		}
		if err := validAccountPassword(password); err != nil {
			return err
		}
		record := *a.record
		record.TrustedDevice = ""
		if err := sealAccountKey(&record, password, a.key); err != nil {
			return err
		}
		if err := writePrivate(a.path, &record); err != nil {
			return errors.New("密码未能保存，仍可使用本机登录")
		}
		a.record = &record
	default:
		return errors.New("未知的信任登录操作")
	}
	return nil
}
