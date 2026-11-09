package containerenv

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sylvinus/agent-vm/internal/host"
)

const profileLabel = "cn.cage.profile"

type Config struct {
	Engine      string `json:"engine"`
	Executable  string `json:"executable"`
	Socket      string `json:"socket,omitempty"`
	Image       string `json:"image"`
	CPUs        int    `json:"cpus"`
	MemoryBytes int64  `json:"memory_bytes"`
}

type Runtime struct {
	profile  string
	config   Config
	identity string
}

var imageIDPattern = regexp.MustCompile(`^(?:sha256:|[a-zA-Z0-9][a-zA-Z0-9./:_-]*@sha256:)[a-f0-9]{64}$`)

func (c Config) Validate() error {
	if c.Engine != "docker" && c.Engine != "podman" {
		return errors.New("容器引擎必须是 rootless Docker 或 Podman")
	}
	if !filepath.IsAbs(c.Executable) || strings.ContainsAny(c.Executable, "\x00\r\n") {
		return errors.New("容器引擎必须使用绝对路径")
	}
	if c.Engine == "docker" && (!strings.HasPrefix(c.Socket, "unix:///") || strings.ContainsAny(c.Socket, "\x00\r\n?#")) {
		return errors.New("Docker 必须使用独立的本机 rootless Unix socket")
	}
	if c.Engine == "podman" && c.Socket != "" {
		return errors.New("Podman 运行环境不使用远程服务 socket")
	}
	if !imageIDPattern.MatchString(c.Image) {
		return errors.New("容器镜像必须固定为 SHA-256 image ID 或 registry digest")
	}
	if c.CPUs < 1 || c.CPUs > 256 || c.MemoryBytes < 512<<20 {
		return errors.New("容器 CPU 或内存限制无效")
	}
	return nil
}

func Configured(profile string) bool {
	_, err := os.Lstat(filepath.Join(profile, "container.json"))
	return !os.IsNotExist(err)
}

func Load(profile string) (*Runtime, error) {
	profile, err := filepath.Abs(profile)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(profile, "container.json")
	if err := host.CheckPrivateFile(path); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var config Config
	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, errors.New("容器配置格式无效")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, errors.New("容器配置必须只有一个 JSON 对象")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return newRuntime(profile, config), nil
}

func Configure(profile string, config Config) (*Runtime, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	profile, err := filepath.Abs(profile)
	if err != nil {
		return nil, err
	}
	if err := host.EnsurePrivateDir(profile); err != nil {
		return nil, err
	}
	// Changing a runtime would strand the full persistent filesystem. Require
	// an explicit migration path rather than silently creating another one.
	if Configured(profile) {
		existing, err := Load(profile)
		if err != nil {
			return nil, err
		}
		if existing.config != config {
			return nil, errors.New("已有容器配置不能直接替换；请保留现有环境并执行迁移")
		}
		return existing, nil
	}
	if err := writePrivateJSON(filepath.Join(profile, "container.json"), config); err != nil {
		return nil, err
	}
	return newRuntime(profile, config), nil
}

func newRuntime(profile string, config Config) *Runtime {
	digest := sha256.Sum256([]byte(profile))
	return &Runtime{profile: profile, config: config, identity: hex.EncodeToString(digest[:16])}
}

func (r *Runtime) Config() Config { return r.config }
func (r *Runtime) InstanceDir() string {
	return filepath.Join(r.profile, "state", "container", "environment")
}
func (r *Runtime) SSHConfig() string { return filepath.Join(r.InstanceDir(), "ssh.config") }
func (r *Runtime) SSHTarget() string { return "cage-container" }
func (r *Runtime) Name() string      { return "cage-" + r.identity }

func writePrivateJSON(path string, value any) error {
	if err := host.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".container-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err = host.ProtectFile(file.Name()); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
