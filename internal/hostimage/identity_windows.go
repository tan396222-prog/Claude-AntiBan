package hostimage

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func Host(ctx context.Context) (Identity, error) {
	if err := ctx.Err(); err != nil {
		return Identity{}, err
	}
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return Identity{}, err
	}
	defer key.Close()
	version, _, err := key.GetStringValue("DisplayVersion")
	if err != nil {
		return Identity{}, err
	}
	build, _, err := key.GetStringValue("CurrentBuildNumber")
	if err != nil {
		return Identity{}, err
	}
	major, err := strconv.Atoi(build)
	if err != nil || major < 22000 {
		return Identity{}, fmt.Errorf("本机系统创建需要 Windows 11")
	}
	revision, _, err := key.GetIntegerValue("UBR")
	if err != nil {
		return Identity{}, err
	}
	edition, _, err := key.GetStringValue("EditionID")
	if err != nil {
		return Identity{}, err
	}
	languageKey, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\MUI\UILanguages`, registry.READ)
	if err != nil {
		return Identity{}, err
	}
	defer languageKey.Close()
	languages, err := languageKey.ReadSubKeyNames(-1)
	if err != nil || len(languages) == 0 {
		return Identity{}, fmt.Errorf("无法读取 Windows 安装语言")
	}
	// InstallLanguage is the base UI language. On multilingual installations,
	// prefer the installed-language key matching the system locale.
	locale, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Nls\Language`, registry.QUERY_VALUE)
	if err == nil {
		defer locale.Close()
		code, _, _ := locale.GetStringValue("InstallLanguage")
		for _, language := range languages {
			candidate, openErr := registry.OpenKey(languageKey, language, registry.QUERY_VALUE)
			if openErr != nil {
				continue
			}
			lcid, _, _ := candidate.GetIntegerValue("LCID")
			candidate.Close()
			if strings.EqualFold(fmt.Sprintf("%04x", lcid), code) {
				languages[0] = language
				break
			}
		}
	}
	return Identity{System: "windows-11", Version: version, Build: fmt.Sprintf("%s.%d", build, revision), Edition: edition, Language: languages[0], Arch: runtime.GOARCH}, nil
}
