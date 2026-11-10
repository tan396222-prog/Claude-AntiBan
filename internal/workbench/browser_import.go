package workbench

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type localBrowserRoot struct{ Name, Path string }
type localBrowserExtension struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Version           string   `json:"version"`
	Enabled           bool     `json:"enabled"`
	StateKnown        bool     `json:"state_known"`
	Compatible        bool     `json:"compatible"`
	IdentityPreserved bool     `json:"identity_preserved"`
	Permissions       []string `json:"permissions"`
	HostPermissions   []string `json:"hostPermissions"`
	directory         string
	key               string
}
type localBrowserProfile struct {
	ID         string                  `json:"id"`
	Browser    string                  `json:"browser"`
	Name       string                  `json:"name"`
	Extensions []localBrowserExtension `json:"extensions"`
}

func localBrowserRoots() []localBrowserRoot {
	home, _ := os.UserHomeDir()
	var base string
	var locations []string
	switch runtime.GOOS {
	case "darwin":
		base = filepath.Join(home, "Library", "Application Support")
		locations = []string{"Microsoft Edge", "Google/Chrome", "BraveSoftware/Brave-Browser", "Chromium"}
	case "windows":
		base = os.Getenv("LOCALAPPDATA")
		locations = []string{"Microsoft/Edge/User Data", "Google/Chrome/User Data", "BraveSoftware/Brave-Browser/User Data", "Chromium/User Data"}
	default:
		base, _ = os.UserConfigDir()
		locations = []string{"microsoft-edge", "google-chrome", "BraveSoftware/Brave-Browser", "chromium"}
	}
	if base == "" {
		return nil
	}
	var roots []localBrowserRoot
	for i, name := range []string{"Microsoft Edge", "Google Chrome", "Brave", "Chromium"} {
		roots = append(roots, localBrowserRoot{name, filepath.Join(base, filepath.FromSlash(locations[i]))})
	}
	return roots
}
func readBrowserJSON(path string, value any) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	return json.NewDecoder(io.LimitReader(file, 16<<20)).Decode(value) == nil
}
func browserDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

func scanLocalBrowserProfiles(roots []localBrowserRoot) []localBrowserProfile {
	result := []localBrowserProfile{}
	for _, root := range roots {
		if !browserDirectory(root.Path) {
			continue
		}
		entries, err := os.ReadDir(root.Path)
		if err != nil {
			continue
		}
		var local struct {
			Profile struct {
				Info map[string]struct {
					Name string `json:"name"`
				} `json:"info_cache"`
			} `json:"profile"`
		}
		readBrowserJSON(filepath.Join(root.Path, "Local State"), &local)
		for _, entry := range entries {
			name := entry.Name()
			if name != "Default" && !strings.HasPrefix(name, "Profile ") {
				continue
			}
			profilePath := filepath.Join(root.Path, name)
			if !browserDirectory(profilePath) {
				continue
			}
			digest := sha256.Sum256([]byte(root.Path + "\x00" + name))
			profile := localBrowserProfile{ID: hex.EncodeToString(digest[:16]), Browser: root.Name, Name: local.Profile.Info[name].Name, Extensions: []localBrowserExtension{}}
			if profile.Name == "" {
				profile.Name = name
			}
			var prefs struct {
				Extensions struct {
					Settings map[string]struct {
						State          *int            `json:"state"`
						DisableReasons json.RawMessage `json:"disable_reasons"`
						Path           string          `json:"path"`
						Manifest       struct {
							Key string `json:"key"`
						} `json:"manifest"`
					} `json:"settings"`
				} `json:"extensions"`
			}
			readBrowserJSON(filepath.Join(profilePath, "Preferences"), &prefs)
			// Secure Preferences owns protected extension state on desktop Chromium.
			var secure = prefs
			if readBrowserJSON(filepath.Join(profilePath, "Secure Preferences"), &secure) {
				for id, value := range secure.Extensions.Settings {
					if prefs.Extensions.Settings == nil {
						prefs.Extensions.Settings = secure.Extensions.Settings
						break
					}
					prefs.Extensions.Settings[id] = value
				}
			}
			extRoot := filepath.Join(profilePath, "Extensions")
			if !browserDirectory(extRoot) {
				continue
			}
			extensionDirs, _ := os.ReadDir(extRoot)
			for _, extensionDir := range extensionDirs {
				id := extensionDir.Name()
				if len(id) != 32 || strings.Trim(id, "abcdefghijklmnop") != "" {
					continue
				}
				directory := filepath.Join(extRoot, id)
				if !browserDirectory(directory) {
					continue
				}
				versions, _ := os.ReadDir(directory)
				sort.Slice(versions, func(i, j int) bool {
					a, _ := versions[i].Info()
					b, _ := versions[j].Info()
					return a != nil && (b == nil || a.ModTime().After(b.ModTime()))
				})
				state := prefs.Extensions.Settings[id]
				if _, registered := prefs.Extensions.Settings[id]; prefs.Extensions.Settings != nil && !registered {
					continue
				}
				if state.State != nil && *state.State == 2 {
					continue
				}
				if state.Path != "" {
					desired := filepath.Base(filepath.Clean(state.Path))
					sort.SliceStable(versions, func(i, j int) bool { return versions[i].Name() == desired && versions[j].Name() != desired })
				}
				for _, version := range versions {
					packagePath := filepath.Join(directory, version.Name())
					if !browserDirectory(packagePath) {
						continue
					}
					var manifest struct {
						Name            string   `json:"name"`
						Version         string   `json:"version"`
						ManifestVersion int      `json:"manifest_version"`
						Key             string   `json:"key"`
						Permissions     []string `json:"permissions"`
						Hosts           []string `json:"host_permissions"`
						Content         []struct {
							Matches []string `json:"matches"`
						} `json:"content_scripts"`
						Locale string `json:"default_locale"`
					}
					if !readBrowserJSON(filepath.Join(packagePath, "manifest.json"), &manifest) || manifest.Name == "" || manifest.Version == "" {
						continue
					}
					display := manifest.Name
					if strings.HasPrefix(display, "__MSG_") && strings.HasSuffix(display, "__") && filepath.Base(manifest.Locale) == manifest.Locale {
						var messages map[string]struct {
							Message string `json:"message"`
						}
						if readBrowserJSON(filepath.Join(packagePath, "_locales", manifest.Locale, "messages.json"), &messages) {
							if text := messages[strings.TrimSuffix(strings.TrimPrefix(display, "__MSG_"), "__")].Message; text != "" {
								display = text
							}
						}
					}
					publicKey := manifest.Key
					if publicKey == "" {
						publicKey = state.Manifest.Key
					}
					if publicKey != "" {
						data, decodeErr := base64.StdEncoding.DecodeString(publicKey)
						digest := sha256.Sum256(data)
						actual := ""
						for _, char := range hex.EncodeToString(digest[:16]) {
							var value int
							if char >= 'a' {
								value = int(char-'a') + 10
							} else {
								value = int(char - '0')
							}
							actual += string(rune('a' + value))
						}
						if decodeErr != nil || actual != id {
							publicKey = ""
						}
					}
					enabled, known := importedExtensionState(state.State, state.DisableReasons)
					hosts := append([]string{}, manifest.Hosts...)
					for _, content := range manifest.Content {
						hosts = append(hosts, content.Matches...)
					}
					profile.Extensions = append(profile.Extensions, localBrowserExtension{ID: id, Name: display, Version: manifest.Version, Enabled: enabled, StateKnown: known, Compatible: manifest.ManifestVersion == 3, IdentityPreserved: publicKey != "", Permissions: manifest.Permissions, HostPermissions: hosts, directory: packagePath, key: publicKey})
					break
				}
			}
			sort.Slice(profile.Extensions, func(i, j int) bool { return profile.Extensions[i].Name < profile.Extensions[j].Name })
			result = append(result, profile)
		}
	}
	return result
}

// Current Chromium records disable_reasons; older profiles also have state.
func importedExtensionState(state *int, reasons json.RawMessage) (bool, bool) {
	if len(reasons) > 0 && string(reasons) != "null" {
		var list []json.RawMessage
		if json.Unmarshal(reasons, &list) == nil {
			return len(list) == 0 && (state == nil || *state == 1), true
		}
		var mask int
		if json.Unmarshal(reasons, &mask) == nil {
			return mask == 0 && (state == nil || *state == 1), true
		}
	}
	if state != nil {
		return *state == 1, true
	}
	return false, false
}

func packageLocalExtension(extension localBrowserExtension) ([]byte, error) {
	if !extension.Compatible || !browserDirectory(extension.directory) {
		return nil, errors.New("此扩展不支持导入")
	}
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	count := 0
	var total int64
	err := filepath.WalkDir(extension.directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, e := filepath.Rel(extension.directory, path)
		if e != nil {
			return e
		}
		if relative == "." {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == "_metadata" || entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("扩展包含无法导入的特殊文件")
		}
		count++
		total += info.Size()
		if count > 3000 || total > 128<<20 || info.Size() > 32<<20 {
			return errors.New("扩展文件超出导入限制")
		}
		target, e := archive.Create(filepath.ToSlash(relative))
		if e != nil {
			return e
		}
		source, e := os.Open(path)
		if e != nil {
			return e
		}
		defer source.Close()
		if relative == "manifest.json" && extension.key != "" {
			var manifest map[string]any
			if json.NewDecoder(io.LimitReader(source, 1<<20)).Decode(&manifest) != nil {
				return errors.New("扩展配置已变化，请刷新清单")
			}
			manifest["key"] = extension.key
			return json.NewEncoder(target).Encode(manifest)
		}
		_, e = io.CopyN(target, source, info.Size())
		if output.Len() > 32<<20 {
			return errors.New("扩展压缩包不能超过 32 MB")
		}
		return e
	})
	closeErr := archive.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if output.Len() > 32<<20 {
		return nil, errors.New("扩展压缩包不能超过 32 MB")
	}
	return output.Bytes(), nil
}

func (s *Server) localBrowserExtensions(w http.ResponseWriter, r *http.Request) {
	profiles := scanLocalBrowserProfiles(localBrowserRoots())
	if r.Method == http.MethodGet {
		jsonReply(w, map[string]any{"profiles": profiles})
		return
	}
	var input struct {
		Profile string `json:"profile"`
		ID      string `json:"id"`
		Version string `json:"version"`
	}
	if !readJSON(w, r, &input) {
		return
	}
	for _, profile := range profiles {
		if profile.ID != input.Profile {
			continue
		}
		for _, extension := range profile.Extensions {
			if extension.ID != input.ID {
				continue
			}
			if extension.Version != input.Version {
				requestError(w, errors.New("本机扩展已更新，请刷新清单"))
				return
			}
			data, err := packageLocalExtension(extension)
			if err != nil {
				requestError(w, err)
				return
			}
			w.Header().Set("Content-Type", "application/zip")
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			_, _ = w.Write(data)
			return
		}
	}
	http.Error(w, "所选本机扩展已不可用，请刷新清单", http.StatusNotFound)
}
