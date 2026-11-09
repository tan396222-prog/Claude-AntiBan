package cage

import (
	"errors"
	"net/netip"
	"regexp"
	"strings"
)

// ExitPolicy is user configuration. Its anchor survives checks and restarts;
// observations must never silently replace a configured address or region.
type ExitPolicy struct {
	Mode        string `json:"mode"`
	Scope       string `json:"scope"`
	IP          string `json:"ip,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
	Region      string `json:"region,omitempty"`
	City        string `json:"city,omitempty"`
}

type ExitLocation struct{ IP, CountryCode, Region, City string }

func (p ExitPolicy) Normalized() ExitPolicy {
	if p.Mode == "" {
		p.Mode = "dynamic"
	}
	if p.Scope == "" {
		p.Scope = "country"
	}
	p.IP = strings.TrimSpace(p.IP)
	p.CountryCode = strings.ToUpper(strings.TrimSpace(p.CountryCode))
	p.Region = strings.Join(strings.Fields(p.Region), " ")
	p.City = strings.Join(strings.Fields(p.City), " ")
	return p
}

func (p ExitPolicy) Validate() error {
	p = p.Normalized()
	if p.Mode != "static" && p.Mode != "dynamic" {
		return errors.New("请选择静态或动态 IP")
	}
	if p.Scope != "any" && p.Scope != "country" && p.Scope != "city" {
		return errors.New("请选择动态 IP 的允许范围")
	}
	if p.IP != "" {
		ip, err := netip.ParseAddr(p.IP)
		if err != nil || !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() {
			return errors.New("固定出口应填写公网 IPv4 地址")
		}
	}
	if p.CountryCode != "" && !regexp.MustCompile(`^[A-Z]{2}$`).MatchString(p.CountryCode) {
		return errors.New("国家或地区代码应为两个字母，例如 US")
	}
	if len(p.Region) > 120 || len(p.City) > 120 {
		return errors.New("地区名称过长")
	}
	return nil
}

func (p ExitPolicy) NeedsBinding() bool {
	p = p.Normalized()
	if p.Mode == "static" {
		return p.IP == ""
	}
	return p.Scope != "any" && (p.CountryCode == "" || p.Scope == "city" && (p.Region == "" || p.City == ""))
}

// BindMissing is used only for a previously unconfigured anchor. It leaves
// existing values intact, including when the new observation violates them.
func (p ExitPolicy) BindMissing(location ExitLocation) ExitPolicy {
	p = p.Normalized()
	if p.Mode == "static" && p.IP == "" {
		p.IP = location.IP
	}
	if p.Mode == "dynamic" && p.Scope != "any" {
		if p.CountryCode == "" {
			p.CountryCode = location.CountryCode
		}
		if p.Scope == "city" && strings.EqualFold(p.CountryCode, location.CountryCode) {
			if p.Region == "" {
				p.Region = location.Region
			}
			if p.City == "" {
				p.City = location.City
			}
		}
	}
	return p.Normalized()
}

func (p ExitPolicy) Evaluate(ip string, location *ExitLocation) (CheckState, string) {
	p = p.Normalized()
	if err := p.Validate(); err != nil {
		return CheckUnknown, err.Error()
	}
	observed, err := netip.ParseAddr(ip)
	if err != nil || !observed.Is4() || !observed.IsGlobalUnicast() || observed.IsPrivate() {
		return CheckUnknown, "尚未取得有效的检测出口"
	}
	if p.NeedsBinding() {
		return CheckUnknown, "尚未绑定出口规则，请在设置中确认检测到的 IP 或地区"
	}
	if p.Mode == "static" {
		if p.IP != observed.String() {
			return CheckFailed, "检测出口已偏离绑定的静态 IP"
		}
		return CheckPassed, "检测出口与绑定的静态 IP 一致"
	}
	if p.Scope == "any" {
		return CheckPassed, "动态 IP 不限制地区，流量仍须经指定代理"
	}
	if location == nil || location.IP != observed.String() || location.CountryCode == "" {
		return CheckUnknown, "无法取得当前出口的国家或地区，不能确认允许范围"
	}
	if !strings.EqualFold(p.CountryCode, location.CountryCode) {
		return CheckFailed, "检测出口超出允许的国家或地区"
	}
	if p.Scope == "country" {
		return CheckPassed, "检测出口位于允许的国家或地区"
	}
	if location.Region == "" || location.City == "" {
		return CheckUnknown, "当前位置缺少州、省或城市，不能确认城市范围"
	}
	if !strings.EqualFold(p.Region, strings.Join(strings.Fields(location.Region), " ")) || !strings.EqualFold(p.City, strings.Join(strings.Fields(location.City), " ")) {
		return CheckFailed, "检测出口超出允许的城市范围"
	}
	return CheckPassed, "检测出口位于允许的城市及其所属州、省"
}
