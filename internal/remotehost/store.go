package remotehost

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/sylvinus/agent-vm/internal/host"
)

var uuidPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var instancePattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var hostnamePattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?$`)
var storeLocks sync.Map

type Store struct {
	profile string
	mu      *sync.Mutex
}
type storedHosts struct {
	Version int           `json:"version"`
	Hosts   []HostProfile `json:"hosts"`
}

func NewStore(profileDir string) *Store {
	absolute, err := filepath.Abs(profileDir)
	if err == nil {
		profileDir = absolute
	}
	lock, _ := storeLocks.LoadOrStore(filepath.Clean(profileDir), &sync.Mutex{})
	return &Store{profile: profileDir, mu: lock.(*sync.Mutex)}
}

func newID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	raw[6] = (raw[6] & 15) | 64
	raw[8] = (raw[8] & 63) | 128
	s := hex.EncodeToString(raw[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 32 || r == 127 })
}

func normalizeProfile(p HostProfile) (HostProfile, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.Host = strings.TrimSpace(p.Host)
	p.User = strings.TrimSpace(p.User)
	if p.Port == 0 {
		p.Port = 22
	}
	if p.Executable == "" {
		p.Executable = "cage"
	}
	if p.ID != "" && !uuidPattern.MatchString(p.ID) {
		return p, failure("invalid_profile", "主机标识无效")
	}
	if p.Name == "" || len(p.Name) > 200 || hasControl(p.Name) {
		return p, failure("invalid_profile", "请填写有效的主机名称")
	}
	if p.Host == "" || len(p.Host) > 253 || (net.ParseIP(p.Host) == nil && !hostnamePattern.MatchString(p.Host)) || strings.Contains(p.Host, "..") {
		return p, failure("invalid_profile", "请填写主机名或 IP 地址")
	}
	if p.Port < 1 || p.Port > 65535 || p.User == "" || len(p.User) > 128 || hasControl(p.User) {
		return p, failure("invalid_profile", "SSH 用户名或端口无效")
	}
	if p.Auth.Kind != "key_file" && p.Auth.Kind != "agent" {
		return p, failure("invalid_profile", "请选择 SSH 密钥文件或本机 SSH agent")
	}
	if p.Auth.Kind == "key_file" && (!filepath.IsAbs(p.Auth.IdentityFile) || hasControl(p.Auth.IdentityFile)) {
		return p, failure("invalid_profile", "SSH 密钥文件需要使用本机绝对路径")
	}
	if p.Auth.Kind == "agent" {
		p.Auth.IdentityFile = ""
	}
	if (p.Executable != "cage" && !path.IsAbs(p.Executable)) || hasControl(p.Executable) || len(p.Executable) > 4096 {
		return p, failure("invalid_profile", "云端 Cage 程序需要使用绝对路径或 cage")
	}
	if (p.ConfigPath != "" && !path.IsAbs(p.ConfigPath)) || hasControl(p.ConfigPath) || len(p.ConfigPath) > 4096 {
		return p, failure("invalid_profile", "云端配置文件需要使用绝对路径")
	}
	if p.HostKey != nil {
		key := *p.HostKey
		raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(key.Fingerprint, "SHA256:"))
		if key.Algorithm == "" || len(key.Algorithm) > 128 || hasControl(key.Algorithm) || !strings.HasPrefix(key.Fingerprint, "SHA256:") || err != nil || len(raw) != 32 {
			return p, failure("invalid_profile", "SSH 主机密钥指纹无效")
		}
		p.HostKey = &key
	}
	return p, nil
}

func (s *Store) read() ([]HostProfile, error) {
	filename := filepath.Join(s.profile, "remote-hosts.json")
	if err := host.CheckPrivateFile(filename); err != nil {
		if os.IsNotExist(err) {
			return []HostProfile{}, nil
		}
		return nil, failure("profile_permissions", "远程主机配置权限不安全")
	}
	f, err := os.Open(filename)
	if err != nil {
		return nil, failure("profile_read", "无法读取远程主机配置")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > 1<<20 {
		return nil, failure("profile_invalid", "远程主机配置过大或无法读取")
	}
	var data storedHosts
	decoder := json.NewDecoder(io.LimitReader(f, 1<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&data) != nil || data.Version != 1 || len(data.Hosts) > 100 {
		return nil, failure("profile_invalid", "远程主机配置损坏，现有文件未修改")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, failure("profile_invalid", "远程主机配置内容无效")
	}
	seen := map[string]bool{}
	for i, p := range data.Hosts {
		normalized, err := normalizeProfile(p)
		if err != nil || p.ID == "" || seen[p.ID] {
			return nil, failure("profile_invalid", "远程主机配置内容无效")
		}
		data.Hosts[i] = normalized
		seen[p.ID] = true
	}
	if data.Hosts == nil {
		data.Hosts = []HostProfile{}
	}
	return data.Hosts, nil
}

func (s *Store) write(profiles []HostProfile) error {
	if err := host.EnsurePrivateDir(s.profile); err != nil {
		return failure("profile_permissions", "无法创建私有远程主机配置目录")
	}
	file, err := os.CreateTemp(s.profile, ".remote-hosts-*")
	if err != nil {
		return failure("profile_write", "无法保存远程主机配置")
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if err = host.ProtectFile(tmp); err == nil {
		err = json.NewEncoder(file).Encode(storedHosts{1, profiles})
	}
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err == nil {
		err = os.Rename(tmp, filepath.Join(s.profile, "remote-hosts.json"))
	}
	if err != nil {
		return failure("profile_write", "远程主机配置未能保存，现有文件保持不变")
	}
	return nil
}

func (s *Store) List() ([]HostProfile, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.read() }
func (s *Store) Get(id string) (HostProfile, error) {
	profiles, err := s.List()
	if err != nil {
		return HostProfile{}, err
	}
	for _, p := range profiles {
		if p.ID == id {
			return p, nil
		}
	}
	return HostProfile{}, failure("host_not_found", "远程主机配置已不存在")
}
func (s *Store) Upsert(profile HostProfile) (HostProfile, error) {
	profile, err := normalizeProfile(profile)
	if err != nil {
		return profile, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	profiles, err := s.read()
	if err != nil {
		return profile, err
	}
	if profile.ID == "" {
		if len(profiles) >= 100 {
			return profile, failure("profile_limit", "最多保存 100 台远程主机")
		}
		profile.ID = newID()
		profiles = append(profiles, profile)
	} else {
		found := false
		for i, p := range profiles {
			if p.ID == profile.ID {
				profiles[i] = profile
				found = true
				break
			}
		}
		if !found {
			return profile, failure("host_not_found", "远程主机配置已不存在")
		}
	}
	return profile, s.write(profiles)
}
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	profiles, err := s.read()
	if err != nil {
		return err
	}
	for i, p := range profiles {
		if p.ID == id {
			return s.write(append(profiles[:i], profiles[i+1:]...))
		}
	}
	return failure("host_not_found", "远程主机配置已不存在")
}
