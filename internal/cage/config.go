// Package cage supplies the configuration and network boundary of Claude Cage.
package cage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"time"
	_ "time/tzdata"
)

const ConfigEnv = "CLAUDE_CAGE_CONFIG"

type Config struct {
	ProxyURL        string     `json:"proxy_url"`
	ProxyRevision   string     `json:"proxy_revision,omitempty"`
	DNSURL          string     `json:"dns_url"`
	Timezone        string     `json:"timezone"`
	Locale          string     `json:"locale"`
	BrowserLanguage string     `json:"browser_language"`
	ExitPolicy      ExitPolicy `json:"exit_policy"`
}

func DefaultConfig() Config {
	return Config{ProxyURL: "socks5://127.0.0.1:17896", DNSURL: "https://1.1.1.1/dns-query", Timezone: "Etc/UTC", Locale: "en_US.UTF-8", BrowserLanguage: "en-US"}
}

func Load(path string) (Config, error) {
	var c Config
	f, err := os.Open(path)
	if err != nil {
		return c, fmt.Errorf("cannot read cage configuration: %w", err)
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 64<<10))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, errors.New("invalid cage JSON configuration")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return c, errors.New("cage configuration must contain one JSON object")
	}
	c.ExitPolicy = c.ExitPolicy.Normalized()
	return c, c.Validate()
}

func (c Config) Validate() error {
	u, err := url.Parse(c.ProxyURL)
	if err != nil || u == nil {
		return errors.New("invalid proxy URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" && u.Scheme != "socks5h" {
		return errors.New("proxy must use http, https or socks5")
	}
	// The proxy endpoint itself must not require the host DNS resolver.
	if net.ParseIP(u.Hostname()) == nil {
		return errors.New("proxy endpoint must be a numeric IP address; DNS bootstrap is not allowed")
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil || p < 1 || p > 65535 {
		return errors.New("proxy requires a valid explicit port")
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("proxy URL must not contain a path, query or fragment")
	}
	d, err := url.Parse(c.DNSURL)
	if err != nil || d.Scheme != "https" || d.Hostname() == "" || d.User != nil || d.Fragment != "" {
		return errors.New("DNS URL must be an HTTPS DNS-over-HTTPS endpoint")
	}
	if c.Timezone == "" || c.Timezone == "Local" {
		return errors.New("timezone must be an explicit IANA name, e.g. Etc/UTC")
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return errors.New("unknown cage timezone")
	}
	if !regexp.MustCompile(`^[A-Za-z]{2,3}_[A-Za-z]{2,3}\.UTF-8$`).MatchString(c.Locale) {
		return errors.New("locale must have the form en_US.UTF-8")
	}
	if !regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`).MatchString(c.BrowserLanguage) {
		return errors.New("invalid browser language tag")
	}
	return c.ExitPolicy.Validate()
}
