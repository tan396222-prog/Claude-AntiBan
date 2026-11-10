// Package hostimage prepares clean installation media matching the host OS.
// It reads OS identity and installation media without copying personal data.
package hostimage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
	"golang.org/x/mod/semver"
)

type Identity struct {
	System   string `json:"system"`
	Version  string `json:"version"`
	Build    string `json:"build,omitempty"`
	Edition  string `json:"edition,omitempty"`
	Language string `json:"language,omitempty"`
	Arch     string `json:"arch"`
}

func IsLinux(system string) bool {
	return system == "debian-12" || system == "debian-13" || system == "ubuntu-22.04" || system == "ubuntu-24.04"
}

// Plan is immutable after the user chooses it. Path refers only to source
// media. Download destinations are derived independently from its digest.
type Plan struct {
	Host     Identity `json:"host"`
	Version  string   `json:"version"`
	Build    string   `json:"build,omitempty"`
	Format   string   `json:"format"`
	URL      string   `json:"url,omitempty"`
	Digest   string   `json:"digest,omitempty"`
	Bytes    int64    `json:"bytes"`
	Path     string   `json:"path,omitempty"`
	Modified int64    `json:"modified,omitempty"`
}

func (p Plan) Key() string {
	if p.URL != "" && p.Digest != "" {
		p.Path, p.Modified = "", 0
	}
	data, _ := json.Marshal(p)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func (p Plan) Validate() error {
	if p.Host.Arch != "arm64" && p.Host.Arch != "amd64" {
		return errors.New("本机系统架构暂不支持创建客机")
	}
	if p.Version == "" || p.Version != p.Host.Version || p.Bytes < 0 || p.Bytes > 64<<30 {
		return errors.New("系统镜像与本机版本不匹配")
	}
	switch p.Host.System {
	case "macos":
		if p.Format != "ipsw" || p.Build != p.Host.Build || p.Host.Arch != "arm64" {
			return errors.New("macOS 恢复镜像与本机版本不匹配")
		}
		if !semver.IsValid("v"+p.Version) || semver.Compare("v"+p.Version, "v26.4") < 0 {
			return errors.New("自动匹配 macOS 镜像需要本机运行 macOS 26.4 或更新版本，以支持当前工作台使用的 FSKit 只读挂载")
		}
	case "windows-11":
		if p.Format != "esd" || p.Host.Arch != "amd64" || p.Host.Edition == "" || p.Host.Language == "" || p.Build == "" {
			return errors.New("Windows 安装镜像信息不完整")
		}
	case "debian-12", "debian-13", "ubuntu-22.04", "ubuntu-24.04":
		if p.Format != "qcow2" {
			return errors.New("Linux 安装镜像与本机不匹配")
		}
	default:
		return errors.New("当前支持的本机系统为 macOS、Windows 11、Debian 12/13 和 Ubuntu 22.04/24.04")
	}
	if p.Path != "" && (!filepath.IsAbs(p.Path) || filepath.Clean(p.Path) != p.Path || strings.ContainsAny(p.Path, "\r\n\x00")) {
		return errors.New("本机系统镜像路径无效")
	}
	if p.URL != "" {
		if err := trustedURL(p.URL, p.Host.System); err != nil {
			return err
		}
		if _, err := digestHash(p.Digest); err != nil {
			return err
		}
	}
	if p.Path == "" && p.URL == "" {
		return errors.New("缺少系统安装材料")
	}
	return nil
}

func trustedURL(raw, system string) error {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return errors.New("系统材料下载地址无效")
	}
	// Microsoft's HTTPS catalog publishes ESDs on this HTTP-only CDN route.
	// Its pinned SHA-256 is mandatory and verified before any image is applied.
	microsoftESD := system == "windows-11" && u.Scheme == "http" && u.Hostname() == "dl.delivery.mp.microsoft.com"
	if u.Scheme != "https" && !microsoftESD {
		return errors.New("系统材料下载协议无效")
	}
	allowed := map[string][]string{
		"macos":      {"updates.cdn-apple.com", "appldnld.apple.com"},
		"windows-11": {"dl.delivery.mp.microsoft.com", "download.microsoft.com", "software.download.prss.microsoft.com"},
		"debian-12":  {"cloud.debian.org"}, "debian-13": {"cloud.debian.org"},
		"ubuntu-22.04": {"cloud-images.ubuntu.com"},
		"ubuntu-24.04": {"cloud-images.ubuntu.com"},
	}
	for _, domain := range allowed[system] {
		if u.Hostname() == domain {
			return nil
		}
	}
	return errors.New("系统材料下载地址不属于受支持的系统厂商")
}

func metadata(ctx context.Context, client *http.Client, address string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Cage-SystemInstaller/1.0)")
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("系统目录返回 HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 8<<20 {
		return errors.New("系统目录超过大小限制")
	}
	if strings.HasSuffix(address, ".xml") {
		return xml.Unmarshal(data, out)
	}
	return json.Unmarshal(data, out)
}

// Resolve first reuses previously verified media metadata. Template is the
// application's bundled, pinned Linux image list, supplied by its owner.
func Resolve(ctx context.Context, identity Identity, cache, downloads string, template []byte, transport http.RoundTripper) (Plan, error) {
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	if plan, err := Load(cache); err == nil && plan.Host == identity {
		if plan.Path != "" && usableFile(plan.Path, plan.Bytes) {
			info, _ := os.Stat(plan.Path)
			if info.ModTime().UnixNano() == plan.Modified {
				return plan, nil
			}
		}
		if path := plan.CachedPath(cache); usableFile(path, plan.Bytes) {
			plan.Path = path
			info, _ := os.Stat(path)
			plan.Modified = info.ModTime().UnixNano()
			return plan, plan.Validate()
		}
	}
	var plan Plan
	var err error
	switch identity.System {
	case "macos":
		if plan, ok := localMacImage(identity, cache, downloads); ok {
			return plan, plan.Validate()
		}
		plan, err = macCatalog(ctx, client, identity)
	case "windows-11":
		plan, err = windowsCatalog(ctx, client, identity)
	default:
		plan, err = linuxCatalog(identity, template)
	}
	if err != nil {
		return Plan{}, err
	}
	for _, candidate := range []string{plan.CachedPath(cache), filepath.Join(downloads, filepath.Base(plan.URL))} {
		if usableFile(candidate, plan.Bytes) {
			info, _ := os.Stat(candidate)
			plan.Path, plan.Modified = candidate, info.ModTime().UnixNano()
			if plan.Bytes == 0 {
				plan.Bytes = info.Size()
			}
			break
		}
	}
	return plan, plan.Validate()
}

func usableFile(path string, size int64) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0 && (size == 0 || info.Size() == size)
}

func macCatalog(ctx context.Context, client *http.Client, host Identity) (Plan, error) {
	// IPSW.me indexes historical Apple URLs. The archive is fetched only from
	// Apple and independently verified by Apple's installer before restoration.
	var catalog struct {
		Firmware []struct {
			Version string `json:"version"`
			Build   string `json:"buildid"`
			URL     string `json:"url"`
			Bytes   int64  `json:"filesize"`
			SHA256  string `json:"sha256sum"`
		} `json:"firmwares"`
	}
	if err := metadata(ctx, client, "https://api.ipsw.me/v4/device/VirtualMac2,1?type=ipsw", &catalog); err != nil {
		return Plan{}, fmt.Errorf("查找与本机一致的 Apple 恢复镜像失败，可选择已有 IPSW：%w", err)
	}
	for _, media := range catalog.Firmware {
		if media.Version == host.Version && media.Build == host.Build {
			return Plan{Host: host, Version: media.Version, Build: media.Build, Format: "ipsw", URL: media.URL, Digest: "sha256:" + media.SHA256, Bytes: media.Bytes}, nil
		}
	}
	return Plan{}, fmt.Errorf("未找到 macOS %s（%s）的恢复镜像，请选择已有的对应 IPSW", host.Version, host.Build)
}

const windowsCatalogURL = "https://download.microsoft.com/download/eb1cc454-1c9a-4c94-adf8-b30c7f3d03d1/products.xml"

type windowsMediaCatalog struct {
	Files []struct {
		Name     string `xml:"FileName"`
		Language string `xml:"LanguageCode"`
		Edition  string `xml:"Edition"`
		Arch     string `xml:"Architecture"`
		Bytes    int64  `xml:"Size"`
		SHA256   string `xml:"Sha256"`
		URL      string `xml:"FilePath"`
	} `xml:"Files>File"`
}

func windowsCatalog(ctx context.Context, client *http.Client, host Identity) (Plan, error) {
	current, currentErr := currentWindowsCatalog(ctx, client, host)
	if currentErr == nil {
		if plan, ok := selectWindowsMedia(current, host); ok {
			return plan, nil
		}
	}
	// Microsoft's current catalog can omit an older feature release. Keep the
	// published 25H2 catalog as a fallback, without changing the selected release.
	var catalog windowsMediaCatalog
	if err := metadata(ctx, client, windowsCatalogURL, &catalog); err != nil {
		return Plan{}, fmt.Errorf("读取微软安装目录失败，可选择已有 ISO：%w", err)
	}
	if plan, ok := selectWindowsMedia(catalog, host); ok {
		return plan, nil
	}
	if currentErr != nil {
		return Plan{}, fmt.Errorf("无法读取匹配本机版本的微软目录，请检查所选代理后重试：%w", currentErr)
	}
	return Plan{}, fmt.Errorf("微软当前目录没有 Windows 11 %s %s %s 的对应介质，请选择已有 ISO", host.Version, host.Edition, host.Language)
}

func selectWindowsMedia(catalog windowsMediaCatalog, host Identity) (Plan, bool) {
	major := strings.SplitN(host.Build, ".", 2)[0]
	for _, file := range catalog.Files {
		if file.Arch != "x64" || file.Edition != host.Edition || !strings.EqualFold(file.Language, host.Language) ||
			major == "" || !strings.HasPrefix(file.Name, major+".") {
			continue
		}
		build := regexp.MustCompile(`^\d+\.\d+`).FindString(file.Name)
		return Plan{Host: host, Version: host.Version, Build: build, Format: "esd", Bytes: file.Bytes,
			Digest: "sha256:" + strings.ToLower(file.SHA256), URL: file.URL}, true
	}
	return Plan{}, false
}

func linuxCatalog(host Identity, template []byte) (Plan, error) {
	var config struct {
		Images []struct {
			Location string `yaml:"location"`
			Arch     string `yaml:"arch"`
			Digest   string `yaml:"digest"`
			Bytes    int64  `yaml:"bytes"`
		} `yaml:"images"`
	}
	if err := yaml.Unmarshal(template, &config); err != nil {
		return Plan{}, err
	}
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[host.Arch]
	for _, image := range config.Images {
		if arch != "" && image.Arch == arch && image.Digest != "" {
			return Plan{Host: host, Version: host.Version, Format: "qcow2", URL: image.Location, Digest: image.Digest, Bytes: image.Bytes}, nil
		}
	}
	return Plan{}, errors.New("此安装包缺少与本机发行版一致的已校验 Linux 镜像")
}

func (p Plan) CachedPath(cache string) string {
	key := strings.ReplaceAll(p.Digest, ":", "-")
	if key == "" {
		key = p.Key()
	}
	return filepath.Join(cache, key+"."+p.Format)
}

func (p Plan) RemainingBytes(cache string) int64 {
	if p.Path != "" {
		return 0
	}
	if usableFile(p.CachedPath(cache), p.Bytes) {
		return 0
	}
	info, err := os.Lstat(p.CachedPath(cache) + ".partial")
	if err == nil && info.Mode().IsRegular() && info.Size() <= p.Bytes {
		return p.Bytes - info.Size()
	}
	return p.Bytes
}

const sourceRecord = "host-system-source.json"

func Save(cache string, plan Plan) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(cache, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(cache, ".host-system-source-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	err = json.NewEncoder(file).Encode(plan)
	closeErr := file.Close()
	if err = errors.Join(err, closeErr); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(cache, sourceRecord))
}

func Load(cache string) (Plan, error) {
	var plan Plan
	file, err := os.Open(filepath.Join(cache, sourceRecord))
	if err != nil {
		return plan, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 32<<10))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&plan); err != nil {
		return plan, err
	}
	return plan, plan.Validate()
}
