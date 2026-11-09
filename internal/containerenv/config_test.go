package containerenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureConfig() Config {
	return Config{Engine: "docker", Executable: filepath.Join(os.TempDir(), "cage-test-bin", "docker"), Socket: "unix:///tmp/cage-test.sock", Image: "sha256:" + strings.Repeat("a", 64), CPUs: 2, MemoryBytes: 4 << 30}
}

func TestConfigRequiresPinnedLocalRootlessEngine(t *testing.T) {
	for _, change := range []func(*Config){
		func(c *Config) { c.Socket = "tcp://127.0.0.1:2375" },
		func(c *Config) { c.Socket = "unix:///tmp/cage.sock\n--privileged" },
		func(c *Config) { c.Image = "debian:13" },
		func(c *Config) { c.Executable = "docker" },
	} {
		config := fixtureConfig()
		change(&config)
		if config.Validate() == nil {
			t.Fatalf("accepted unsafe config %+v", config)
		}
	}
	profile := t.TempDir()
	current, err := Configure(profile, fixtureConfig())
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(profile)
	if err != nil {
		t.Fatal(err)
	}
	if current.Name() != reloaded.Name() || current.Config() != reloaded.Config() {
		t.Fatal("configuration identity did not persist")
	}
	changed := fixtureConfig()
	changed.Image = "sha256:" + strings.Repeat("b", 64)
	if _, err := Configure(profile, changed); err == nil {
		t.Fatal("replacement would strand original writable root filesystem")
	}
	if err := os.WriteFile(filepath.Join(profile, "container.json"), []byte(`{"engine":"docker","privileged":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(profile); err == nil {
		t.Fatal("unknown configuration field accepted")
	}
}

func TestRejectContainerEscapeConfiguration(t *testing.T) {
	valid := &engineContainer{}
	valid.HostConfig.NetworkMode = "none"
	valid.HostConfig.CgroupnsMode = "private"
	valid.HostConfig.IpcMode = "private"
	if err := checkContainerConfiguration(valid); err != nil {
		t.Fatal(err)
	}
	valid.HostConfig.ExtraHosts = []string{"cage:127.0.1.1"}
	if err := checkContainerConfiguration(valid); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*engineContainer){
		func(c *engineContainer) { c.HostConfig.Privileged = true },
		func(c *engineContainer) { c.HostConfig.NetworkMode = "bridge" },
		func(c *engineContainer) { c.HostConfig.NetworkMode = "host" },
		func(c *engineContainer) { c.HostConfig.PidMode = "host" },
		func(c *engineContainer) { c.HostConfig.PidMode = "container:other" },
		func(c *engineContainer) { c.HostConfig.CgroupnsMode = "host" },
		func(c *engineContainer) { c.HostConfig.Binds = []string{"/:/host"} },
		func(c *engineContainer) { c.HostConfig.ExtraHosts = []string{"cage:host-gateway"} },
		func(c *engineContainer) { c.HostConfig.ExtraHosts = []string{"cage:192.168.64.1"} },
		func(c *engineContainer) { c.HostConfig.CapAdd = []string{"ALL"} },
		func(c *engineContainer) {
			c.Mounts = append(c.Mounts, struct{ Type, Source, Destination string }{"bind", "/var/run/docker.sock", "/run/docker.sock"})
		},
	} {
		c := &engineContainer{}
		c.HostConfig.NetworkMode = "none"
		c.HostConfig.CgroupnsMode = "private"
		c.HostConfig.IpcMode = "private"
		mutate(c)
		if checkContainerConfiguration(c) == nil {
			t.Fatal("unsafe container configuration accepted")
		}
	}
}

func TestCreateResolvesHostnameWithinContainerLoopback(t *testing.T) {
	for _, engine := range []string{"docker", "podman"} {
		config := fixtureConfig()
		config.Engine = engine
		runtime := newRuntime(t.TempDir(), config)
		args := runtime.createArguments()
		found := false
		for i, arg := range args {
			if arg == "--add-host" && i+1 < len(args) {
				if args[i+1] != "cage:127.0.1.1" {
					t.Fatalf("%s resolves cage outside its loopback: %q", engine, args[i+1])
				}
				found = true
			}
		}
		if !found {
			t.Fatalf("%s does not resolve the container hostname", engine)
		}
	}
}

func TestSSHKeyIsStableAndHostKeyPathsAreQuoted(t *testing.T) {
	r := newRuntime(filepath.Join(t.TempDir(), "profile with spaces"), fixtureConfig())
	first, err := r.ensureSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.ensureSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("SSH key changed on reconnect")
	}
	if err := r.writeSSHConfig(17770); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(r.SSHConfig())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "StrictHostKeyChecking yes") || !strings.Contains(string(data), `IdentityFile "`) {
		t.Fatal("SSH trust or paths were not preserved")
	}
}
