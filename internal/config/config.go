package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
	HTTPPort  int              `json:"http_port"`
	HTTPSPort int              `json:"https_port"`
	Email     string           `json:"email"` // For ACME registration
	CertDir   string           `json:"cert_dir"`
	Rules     map[string]*Rule `json:"rules"`
	mu        sync.RWMutex
	filePath  string
	pending   map[string]*Rule // nil values are deletions waiting to be saved.
	base      settings
}

// DefaultConfigPath prefers an existing system configuration over a personal one.
func DefaultConfigPath() string {
	return defaultConfigPath(os.Geteuid(), "/etc/proxctl/config.json", os.Getenv("HOME"))
}

func defaultConfigPath(uid int, systemPath, home string) string {
	if uid == 0 {
		return systemPath
	}
	if _, err := os.Stat(systemPath); !os.IsNotExist(err) {
		return systemPath // Never hide a permission error by selecting another file.
	}
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
	return load(path, true)
}

func load(path string, initialize bool) (*Config, error) {
	if path == "" {
		path = DefaultConfigPath()
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	path = absPath

	cfg := &Config{
		HTTPPort:  80,
		HTTPSPort: 443,
		CertDir:   DefaultCertDir(),
		Rules:     make(map[string]*Rule),
		filePath:  path,
		pending:   make(map[string]*Rule),
	}
	cfg.base = configSettings(cfg)

	file, err := os.Open(path)
	if os.IsNotExist(err) && initialize {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("配置路径必须为普通文件: %s", path)
	}
	if info.Mode().Perm() != 0600 {
		if err := file.Chmod(0600); err != nil {
			return nil, fmt.Errorf("保护配置文件权限失败: %w", err)
		}
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("配置必须为 JSON 对象")
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
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("配置无效: %w", err)
	}
	cfg.base = configSettings(cfg)

	return cfg, nil
}

// Path identifies the configuration used by this process and its daemon unit.
func (c *Config) Path() string { return c.filePath }

// AddRule adds or updates a domain rule.
func (c *Config) AddRule(rule *Rule) error {
	if err := validateRule(rule); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	copy := *rule
	copy.CreatedAt = time.Now()
	if c.Rules == nil {
		c.Rules = make(map[string]*Rule)
	}
	if c.pending == nil {
		c.pending = make(map[string]*Rule)
	}
	c.Rules[copy.Domain] = &copy
	c.pending[copy.Domain] = &copy
	return nil
}

// RemoveRule removes a domain rule.
func (c *Config) RemoveRule(domain string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.Rules[domain]; exists {
		delete(c.Rules, domain)
		if c.pending == nil {
			c.pending = make(map[string]*Rule)
		}
		c.pending[domain] = nil
		return true
	}
	return false
}

// GetRule returns a rule by domain.
func (c *Config) GetRule(domain string) (*Rule, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	r, exists := c.Rules[domain]
	if !exists {
		return nil, false
	}
	copy := *r
	return &copy, true
}

// ListRules returns all configured rules.
func (c *Config) ListRules() []*Rule {
	c.mu.RLock()
	defer c.mu.RUnlock()

	list := make([]*Rule, 0, len(c.Rules))
	for _, r := range c.Rules {
		copy := *r
		list = append(list, &copy)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Domain < list[j].Domain })
	return list
}

// ReloadRules replaces only routing rules. Listener and ACME settings need restart.
// A missing, malformed or invalid file leaves the last valid snapshot untouched.
func (c *Config) ReloadRules() (bool, error) {
	next, err := load(c.filePath, false)
	if err != nil {
		return false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if reflect.DeepEqual(c.Rules, next.Rules) {
		return false, nil
	}
	c.Rules = next.Rules
	return true, nil
}
