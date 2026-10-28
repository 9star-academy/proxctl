package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// RuleType defines whether the entry is a reverse proxy or static directory.
type RuleType string

const (
	TypeProxy  RuleType = "proxy"
	TypeStatic RuleType = "static"
)

// Rule represents a hosted domain's configuration.
type Rule struct {
	Domain    string    `json:"domain"`
	Type      RuleType  `json:"type"`
	Target    string    `json:"target,omitempty"`     // For proxy (e.g. 127.0.0.1:8080)
	StaticDir string    `json:"static_dir,omitempty"` // For static site
	SPA       bool      `json:"spa,omitempty"`        // Fallback to index.html for 404
	Auth      string    `json:"auth,omitempty"`       // "user:password"
	CORS      bool      `json:"cors,omitempty"`       // Enable CORS headers
	CreatedAt time.Time `json:"created_at"`
}

// Config holds all rules and global settings.
type Config struct {
	HTTPPort   int               `json:"http_port"`
	HTTPSPort  int               `json:"https_port"`
	Email      string            `json:"email"` // For ACME registration
	CertDir    string            `json:"cert_dir"`
	Rules      map[string]*Rule  `json:"rules"`
	mu         sync.RWMutex
	filePath   string
}

// DefaultConfigPath returns /etc/proxctl/config.json if root, otherwise ~/.config/proxctl/config.json
func DefaultConfigPath() string {
	if os.Geteuid() == 0 {
		return "/etc/proxctl/config.json"
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = "."
	}
	return filepath.Join(home, ".config", "proxctl", "config.json")
}

// DefaultCertDir returns the directory for storing SSL certificates.
func DefaultCertDir() string {
	if os.Geteuid() == 0 {
		return "/etc/proxctl/certs"
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = "."
	}
	return filepath.Join(home, ".config", "proxctl", "certs")
}

// Load reads or initializes the config file.
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultConfigPath()
	}

	cfg := &Config{
		HTTPPort:  80,
		HTTPSPort: 443,
		CertDir:   DefaultCertDir(),
		Rules:     make(map[string]*Rule),
		filePath:  path,
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}
	cfg.filePath = path
	if cfg.Rules == nil {
		cfg.Rules = make(map[string]*Rule)
	}
	if cfg.CertDir == "" {
		cfg.CertDir = DefaultCertDir()
	}
	if cfg.HTTPPort == 0 {
		cfg.HTTPPort = 80
	}
	if cfg.HTTPSPort == 0 {
		cfg.HTTPSPort = 443
	}

	return cfg, nil
}

// Save writes the config to disk atomically.
func (c *Config) Save() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	dir := filepath.Dir(c.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建配置目录失败 %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	tmpFile := fmt.Sprintf("%s.tmp.%d", c.filePath, time.Now().UnixNano())
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("写入临时配置文件失败: %w", err)
	}

	return os.Rename(tmpFile, c.filePath)
}

// AddRule adds or updates a domain rule.
func (c *Config) AddRule(rule *Rule) error {
	if rule.Domain == "" {
		return fmt.Errorf("域名不能为空")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	rule.CreatedAt = time.Now()
	c.Rules[rule.Domain] = rule
	return nil
}

// RemoveRule removes a domain rule.
func (c *Config) RemoveRule(domain string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.Rules[domain]; exists {
		delete(c.Rules, domain)
		return true
	}
	return false
}

// GetRule returns a rule by domain.
func (c *Config) GetRule(domain string) (*Rule, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	r, exists := c.Rules[domain]
	return r, exists
}

// ListRules returns all configured rules.
func (c *Config) ListRules() []*Rule {
	c.mu.RLock()
	defer c.mu.RUnlock()

	list := make([]*Rule, 0, len(c.Rules))
	for _, r := range c.Rules {
		list = append(list, r)
	}
	return list
}
