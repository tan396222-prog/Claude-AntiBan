package hostimage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The Microsoft service publishes a SHA-256-authenticated CAB containing the
// current MCT catalog. Discovery sends only the OS build and architecture.
func currentWindowsCatalog(ctx context.Context, client *http.Client, host Identity) (windowsMediaCatalog, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	var catalog windowsMediaCatalog
	body, _ := json.Marshal(map[string]string{
		"Products":         "PN=Windows.Products.Cab.amd64&V=0.0.0.0",
		"DeviceAttributes": "DUScan=1;OSVersion=10.0." + host.Build,
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://fe3.delivery.mp.microsoft.com/UpdateMetadataService/updates/search/v1/bydeviceinfo", bytes.NewReader(body))
	if err != nil {
		return catalog, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "*/*")
	request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Cage-SystemInstaller/1.0)")
	data, err := catalogResponse(client, request)
	if err != nil {
		return catalog, err
	}
	var records []json.RawMessage
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err = json.Unmarshal(trimmed, &records); err != nil {
			return catalog, err
		}
	} else {
		records = []json.RawMessage{trimmed}
	}
	if len(records) != 1 {
		return catalog, errors.New("微软目录更新返回多个结果")
	}
	var update struct {
		Files []struct {
			Name   string      `json:"FileName"`
			URL    string      `json:"Url"`
			Digest string      `json:"Digest"`
			Size   json.Number `json:"Size"`
		} `json:"FileLocations"`
	}
	if err = json.Unmarshal(records[0], &update); err != nil {
		return catalog, err
	}
	for _, file := range update.Files {
		if file.Name != "products.cab" {
			continue
		}
		address, err := url.Parse(file.URL)
		if err != nil || address.User != nil || address.Port() != "" || address.Fragment != "" ||
			(address.Scheme != "https" && address.Scheme != "http") ||
			!(strings.HasSuffix(address.Hostname(), ".delivery.mp.microsoft.com") || address.Hostname() == "download.microsoft.com" || address.Hostname() == "download.windowsupdate.com") {
			return catalog, errors.New("微软目录更新地址无法核验")
		}
		digest, err := base64.StdEncoding.DecodeString(file.Digest)
		size, sizeErr := file.Size.Int64()
		if err != nil || sizeErr != nil || len(digest) != sha256.Size || size <= 0 || size > 8<<20 {
			return catalog, errors.New("微软目录更新摘要或大小无效")
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, file.URL, nil)
		if err != nil {
			return catalog, err
		}
		request.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Cage-SystemInstaller/1.0)")
		compressed, err := catalogResponse(client, request)
		if err != nil {
			return catalog, err
		}
		actual := sha256.Sum256(compressed)
		if int64(len(compressed)) != size || !bytes.Equal(actual[:], digest) {
			return catalog, errors.New("微软目录更新校验失败")
		}
		data, err := expandWindowsCatalog(ctx, compressed)
		if err != nil {
			return catalog, err
		}
		err = xml.Unmarshal(data, &catalog)
		return catalog, err
	}
	return catalog, errors.New("微软尚未提供当前系统的安装目录")
}

func catalogResponse(client *http.Client, request *http.Request) ([]byte, error) {
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("微软目录返回 HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (8<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 8<<20 {
		return nil, errors.New("微软目录超过大小限制")
	}
	return data, nil
}
