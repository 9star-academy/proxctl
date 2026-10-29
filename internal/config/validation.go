package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

func (c *Config) validate() error {
	if c.HTTPPort < 1 || c.HTTPPort > 65535 || c.HTTPSPort < 1 || c.HTTPSPort > 65535 {
		return fmt.Errorf("监听端口必须在 1-65535 之间")
	}
	for domain, rule := range c.Rules {
		if err := validateRule(rule); err != nil {
			return err
		}
		if domain != rule.Domain {
			return fmt.Errorf("规则键与域名不一致: %s", domain)
		}
	}
	return nil
}

func validateRule(rule *Rule) error {
	if rule == nil {
		return fmt.Errorf("规则不能为空")
	}
	if rule.Domain == "" || len(rule.Domain) > 253 {
		return fmt.Errorf("域名无效")
	}
	for _, label := range strings.Split(rule.Domain, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("域名无效: %s", rule.Domain)
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return fmt.Errorf("域名无效: %s", rule.Domain)
			}
		}
	}
	if rule.Auth != "" {
		parts := strings.SplitN(rule.Auth, ":", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("认证格式应为 user:password，且均不能为空")
		}
	}
	switch rule.Type {
	case TypeProxy:
		_, err := TargetURL(rule.Target)
		return err
	case TypeStatic:
		if !filepath.IsAbs(rule.StaticDir) {
			return fmt.Errorf("静态目录必须为绝对路径")
		}
	default:
		return fmt.Errorf("不支持的规则类型: %s", rule.Type)
	}
	return nil
}

// TargetURL accepts HTTP(S) URLs or host:port targets already normalized by CLI.
func TargetURL(target string) (*url.URL, error) {
	if !strings.Contains(target, "://") {
		target = "http://" + target
	}
	u, err := url.Parse(target)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, fmt.Errorf("反向代理目标无效，需使用 host:port 或 HTTP(S) URL")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("目标端口必须在 1-65535 之间")
		}
	}
	return u, nil
}
