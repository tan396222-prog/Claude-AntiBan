package hostimage

import (
	"archive/zip"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func localMacImage(host Identity, directories ...string) (Plan, bool) {
	for _, directory := range directories {
		entries, err := os.ReadDir(directory)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !strings.EqualFold(filepath.Ext(entry.Name()), ".ipsw") || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 64<<30 {
				continue
			}
			version, build, err := macImageIdentity(path)
			if err != nil || version != host.Version || build != host.Build {
				continue
			}
			return Plan{Host: host, Version: version, Build: build, Format: "ipsw", Bytes: info.Size(), Path: path, Modified: info.ModTime().UnixNano()}, true
		}
	}
	return Plan{}, false
}

func macImageIdentity(path string) (string, string, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return "", "", err
	}
	defer archive.Close()
	manifest, err := archive.Open("BuildManifest.plist")
	if err != nil {
		return "", "", err
	}
	defer manifest.Close()
	// Apple's universal archives list many hardware variants before the root
	// version fields. The macOS 26.5 manifest alone is over 25 MiB.
	decoder := xml.NewDecoder(io.LimitReader(manifest, 128<<20))
	depth := 0
	var key, version, build string
	for {
		token, err := decoder.Token()
		if err != nil {
			return version, build, err
		}
		switch element := token.(type) {
		case xml.StartElement:
			depth++
			if depth == 3 && element.Name.Local == "key" {
				if err = decoder.DecodeElement(&key, &element); err != nil {
					return "", "", err
				}
				depth--
			} else if depth == 3 && element.Name.Local == "string" {
				var value string
				if err = decoder.DecodeElement(&value, &element); err != nil {
					return "", "", err
				}
				depth--
				switch key {
				case "ProductVersion":
					version = value
				case "ProductBuildVersion":
					build = value
				}
				if version != "" && build != "" {
					return version, build, nil
				}
			}
		case xml.EndElement:
			depth--
		}
	}
}
