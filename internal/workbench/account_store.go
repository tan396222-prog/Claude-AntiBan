package workbench

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/sylvinus/agent-vm/internal/host"
	"golang.org/x/crypto/argon2"
)

type accountRecord struct {
	TrustedDevice         string                   `json:"trusted_device,omitempty"`
	TrustedUnlock         *accountDevice           `json:"trusted_unlock,omitempty"`
	Version               int                      `json:"version"`
	Username              string                   `json:"username"`
	Avatar                string                   `json:"avatar,omitempty"`
	Salt                  []byte                   `json:"salt"`
	Nonce                 []byte                   `json:"nonce"`
	SealedKey             []byte                   `json:"sealed_key"`
	Devices               map[string]accountDevice `json:"devices,omitempty"`
	RequireAuthentication bool                     `json:"require_authentication,omitempty"`
}

type localAccount struct {
	mu             sync.Mutex
	path           string
	record         *accountRecord
	trustedSession bool
	key            []byte
	vaultLease     string
	epoch          context.Context
	cancel         context.CancelFunc
	nextAttempt    time.Time
}

type accountView struct {
	TrustedLocal          bool              `json:"trusted_local,omitempty"`
	TrustedLogin          bool              `json:"trusted_login,omitempty"`
	Exists                bool              `json:"exists"`
	Unlocked              bool              `json:"unlocked"`
	Username              string            `json:"username"`
	Avatar                string            `json:"avatar,omitempty"`
	BackgroundRunning     bool              `json:"background_running"`
	DeviceBinding         string            `json:"device_binding"`
	Devices               map[string]string `json:"devices,omitempty"`
	RequireAuthentication bool              `json:"require_authentication"`
}

func loadLocalAccount(profile string) (*localAccount, error) {
	a := &localAccount{path: filepath.Join(profile, "account.json")}
	b, err := os.ReadFile(a.path)
	if os.IsNotExist(err) {
		return a, nil
	}
	if err != nil {
		return nil, errors.New("无法读取本地账户，请检查配置目录")
	}
	if err = host.CheckPrivateFile(a.path); err != nil {
		return nil, errors.New("本地账户文件权限不安全，请恢复私有文件权限")
	}
	var record accountRecord
	if len(b) > 256<<10 || json.Unmarshal(b, &record) != nil || record.Version != 1 || !validTrustedID(record.TrustedDevice) || len(record.Salt) != 16 || len(record.Nonce) != 12 || len(record.SealedKey) != 48 || validAccountName(record.Username) != nil {
		return nil, errors.New("本地账户文件损坏，请从备份恢复；已有数据未修改")
	}
	if device := record.TrustedUnlock; device != nil && (len(device.ID) != 64 || !validTrustedID(device.ID) || len(device.Nonce) != 12 || len(device.SealedKey) != 48 || record.TrustedDevice != "") {
		return nil, errors.New("本机信任记录损坏，请从备份恢复")
	}
	a.record = &record
	for kind, device := range record.Devices {
		if !validDeviceKind(kind) || len(device.ID) != 32 || len(device.Nonce) != 12 || len(device.SealedKey) != 48 {
			return nil, errors.New("本地账户快捷登录记录损坏，请从备份恢复")
		}
	}
	return a, nil
}

func validAccountName(name string) error {
	if strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 40 || strings.ContainsAny(name, "\x00\r\n") {
		return errors.New("用户名需要 1–40 个字符，不能包含换行")
	}
	return nil
}

func validAccountPassword(password string) error {
	if utf8.RuneCountInString(password) < 8 || len(password) > 1024 || strings.ContainsRune(password, 0) {
		return errors.New("密码至少 8 个字符，最多 1024 字节")
	}
	return nil
}

func sealAccountKey(record *accountRecord, password string, key []byte) error {
	record.Salt = make([]byte, 16)
	if _, err := rand.Read(record.Salt); err != nil {
		return err
	}
	derived := argon2.IDKey([]byte(password), record.Salt, 3, 64*1024, 2, 32)
	defer clear(derived)
	block, err := aes.NewCipher(derived)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	record.Nonce = make([]byte, aead.NonceSize())
	if _, err = rand.Read(record.Nonce); err != nil {
		return err
	}
	record.SealedKey = aead.Seal(nil, record.Nonce, key, []byte("Cage local account v1"))
	return nil
}

func openAccountKey(record *accountRecord, password string) ([]byte, error) {
	if len(password) > 1024 {
		return nil, errors.New("密码不正确")
	}
	derived := argon2.IDKey([]byte(password), record.Salt, 3, 64*1024, 2, 32)
	defer clear(derived)
	block, _ := aes.NewCipher(derived)
	aead, _ := cipher.NewGCM(block)
	key, err := aead.Open(nil, record.Nonce, record.SealedKey, []byte("Cage local account v1"))
	if err != nil {
		return nil, errors.New("密码不正确")
	}
	return key, nil
}

func (a *localAccount) view() accountView { return a.viewFor(true) }

func (a *localAccount) viewFor(desktop bool) accountView {
	if a == nil {
		return accountView{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	v := accountView{Exists: a.record != nil, Unlocked: len(a.key) == 32 && (!a.trustedSession || desktop)}
	v.DeviceBinding = fmtAccountBinding(a.path)
	if a.record != nil {
		v.TrustedLocal = a.record.TrustedDevice != ""
		v.TrustedLogin = a.record.TrustedUnlock != nil
		v.Username = a.record.Username
		v.Avatar = a.record.Avatar
		v.RequireAuthentication = a.record.RequireAuthentication
		v.Devices = make(map[string]string)
		for kind, device := range a.record.Devices {
			v.Devices[kind] = device.ID
		}
	}
	return v
}

func (a *localAccount) access() (context.Context, bool) { return a.accessFor(true) }

func (a *localAccount) accessFor(desktop bool) (context.Context, bool) {
	if a == nil {
		return nil, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.epoch, len(a.key) == 32 && (!a.trustedSession || desktop)
}

func (a *localAccount) setKey(key []byte) {
	if a.cancel != nil {
		a.cancel()
	}
	clear(a.key)
	a.key = key
	a.trustedSession = false
	a.vaultLease = randomToken()
	a.epoch, a.cancel = context.WithCancel(context.Background())
}

func (a *localAccount) lock() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	clear(a.key)
	a.key = nil
}

func (a *localAccount) create(name, password, avatar string) error {
	name = strings.TrimSpace(name)
	if err := validAccountName(name); err != nil {
		return err
	}
	if err := validAccountPassword(password); err != nil {
		return err
	}
	normalized, err := normalizeAccountAvatar(avatar)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.record != nil {
		return errors.New("此配置已创建本地账户，请登录")
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return err
	}
	defer clear(key)
	record := &accountRecord{Version: 1, Username: name, Avatar: normalized}
	if err = sealAccountKey(record, password, key); err != nil {
		return err
	}
	if err = writePrivate(a.path, record); err != nil {
		return errors.New("账户未能保存，请检查磁盘空间与目录权限")
	}
	a.record = record
	a.setKey(append([]byte(nil), key...))
	return nil
}

func (a *localAccount) unlock(password string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.record == nil {
		return errors.New("请先创建本地账户")
	}
	if time.Now().Before(a.nextAttempt) {
		return errors.New("请稍后再试")
	}
	key, err := openAccountKey(a.record, password)
	if err != nil {
		a.nextAttempt = time.Now().Add(time.Second)
		return err
	}
	a.nextAttempt = time.Time{}
	if err = a.clearAuthenticationRequirement(); err != nil {
		clear(key)
		return err
	}
	a.setKey(key)
	return nil
}

func (a *localAccount) updateProfile(name, avatar string) error {
	name = strings.TrimSpace(name)
	if err := validAccountName(name); err != nil {
		return err
	}
	normalized, err := normalizeAccountAvatar(avatar)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.key) != 32 {
		return errors.New("请先解锁 Cage")
	}
	record := *a.record
	record.Username = name
	record.Avatar = normalized
	if err = writePrivate(a.path, record); err != nil {
		return errors.New("资料未能保存，请重试")
	}
	a.record = &record
	return nil
}

func (a *localAccount) changePassword(oldPassword, newPassword string) error {
	if err := validAccountPassword(newPassword); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.key) != 32 {
		return errors.New("请先解锁 Cage")
	}
	if time.Now().Before(a.nextAttempt) {
		return errors.New("请稍后再试")
	}
	key, err := openAccountKey(a.record, oldPassword)
	if err != nil {
		a.nextAttempt = time.Now().Add(time.Second)
		return err
	}
	defer clear(key)
	record := *a.record
	if err = sealAccountKey(&record, newPassword, key); err != nil {
		return err
	}
	if err = writePrivate(a.path, record); err != nil {
		return errors.New("新密码未能保存，原密码仍然有效")
	}
	a.record = &record
	a.nextAttempt = time.Time{}
	return nil
}

func (a *localAccount) desktopKey() ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.key) != 32 {
		return nil, errors.New("请先登录 Cage，再打开 Claude Desktop")
	}
	// Base64 is accepted by the keyring without newline/encoding ambiguity.
	encoded := make([]byte, base64.RawStdEncoding.EncodedLen(len(a.key)))
	base64.RawStdEncoding.Encode(encoded, a.key)
	return encoded, nil
}

func normalizeAccountAvatar(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	const maxBytes = 2 << 20
	if len(value) > maxBytes*4/3+100 {
		return "", errors.New("头像图片不能超过 2 MB")
	}
	_, payload, ok := strings.Cut(value, ",")
	if !ok || (!strings.HasPrefix(value, "data:image/png;base64,") && !strings.HasPrefix(value, "data:image/jpeg;base64,")) {
		return "", errors.New("请选择 PNG 或 JPEG 图片")
	}
	b, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(b) > maxBytes {
		return "", errors.New("头像图片无效")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 || cfg.Width > 4096 || cfg.Height > 4096 || cfg.Width*cfg.Height > 8_000_000 {
		return "", errors.New("头像图片尺寸无效或过大")
	}
	src, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return "", errors.New("无法读取头像图片")
	}
	size := min(cfg.Width, cfg.Height)
	x0, y0 := (cfg.Width-size)/2, (cfg.Height-size)/2
	dst := image.NewRGBA(image.Rect(0, 0, 128, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			dst.Set(x, y, src.At(x0+x*size/128, y0+y*size/128))
		}
	}
	var out bytes.Buffer
	if err = png.Encode(&out, dst); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(out.Bytes()), nil
}
