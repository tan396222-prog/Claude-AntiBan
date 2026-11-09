package workbench

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"maps"
)

type accountDevice struct {
	ID        string `json:"id"`
	Nonce     []byte `json:"nonce"`
	SealedKey []byte `json:"sealed_key"`
}
type deviceCredential struct {
	ID     string `json:"id"`
	Secret string `json:"secret"`
}

func validDeviceKind(kind string) bool { return kind == "automatic" || kind == "biometric" }
func fmtAccountBinding(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])
}
func deviceAEAD(secret []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(secret)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func (a *localAccount) enrollDevice(kind string) (deviceCredential, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !validDeviceKind(kind) || len(a.key) != 32 {
		return deviceCredential{}, errors.New("请先解锁 Cage，再设置快捷登录")
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return deviceCredential{}, err
	}
	defer clear(secret)
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return deviceCredential{}, err
	}
	device := accountDevice{ID: hex.EncodeToString(id), Nonce: make([]byte, 12)}
	if _, err := rand.Read(device.Nonce); err != nil {
		return deviceCredential{}, err
	}
	aead, _ := deviceAEAD(secret)
	device.SealedKey = aead.Seal(nil, device.Nonce, a.key, []byte(kind+":"+device.ID))
	record := *a.record
	record.Devices = maps.Clone(record.Devices)
	if record.Devices == nil {
		record.Devices = map[string]accountDevice{}
	}
	record.Devices[kind] = device
	if err := writePrivate(a.path, record); err != nil {
		return deviceCredential{}, errors.New("快捷登录未能保存，请重试")
	}
	a.record = &record
	return deviceCredential{ID: device.ID, Secret: base64.RawStdEncoding.EncodeToString(secret)}, nil
}
func (a *localAccount) revokeDevice(kind string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !validDeviceKind(kind) || len(a.key) != 32 {
		return errors.New("请先解锁 Cage")
	}
	record := *a.record
	record.Devices = maps.Clone(record.Devices)
	delete(record.Devices, kind)
	if err := writePrivate(a.path, record); err != nil {
		return errors.New("快捷登录未能关闭，请重试")
	}
	a.record = &record
	return nil
}

// Called with a.mu held. The disk flag prevents reopening the app or restarting
// the controller from turning an explicit lock into automatic login.
func (a *localAccount) clearAuthenticationRequirement() error {
	if !a.record.RequireAuthentication {
		return nil
	}
	record := *a.record
	record.RequireAuthentication = false
	if err := writePrivate(a.path, record); err != nil {
		return errors.New("无法保存解锁状态，请重试")
	}
	a.record = &record
	return nil
}
func (a *localAccount) unlockDevice(kind string, credential deviceCredential) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.record == nil || !validDeviceKind(kind) {
		return errors.New("快捷登录不可用，请使用密码")
	}
	if kind == "automatic" && a.record.RequireAuthentication {
		return errors.New("Cage 已手动锁定，请使用指纹或密码")
	}
	device, ok := a.record.Devices[kind]
	if !ok || device.ID != credential.ID {
		return errors.New("快捷登录授权已失效，请使用密码")
	}
	secret, err := base64.RawStdEncoding.DecodeString(credential.Secret)
	if err != nil || len(secret) != 32 {
		return errors.New("快捷登录凭据无效")
	}
	defer clear(secret)
	aead, _ := deviceAEAD(secret)
	key, err := aead.Open(nil, device.Nonce, device.SealedKey, []byte(kind+":"+device.ID))
	if err != nil {
		return errors.New("快捷登录凭据无效")
	}
	if err = a.clearAuthenticationRequirement(); err != nil {
		clear(key)
		return err
	}
	a.setKey(key)
	return nil
}
func (a *localAccount) lockAccess(logout bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.record == nil {
		return nil
	}
	record := *a.record
	record.RequireAuthentication = true
	if logout {
		record.Devices = nil
		record.TrustedUnlock = nil
	}
	if err := writePrivate(a.path, record); err != nil {
		return errors.New("无法保存账户锁定状态，请重试")
	}
	a.record = &record
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	clear(a.key)
	a.key = nil
	return nil
}
