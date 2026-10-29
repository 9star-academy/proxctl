package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

type settings struct {
	httpPort, httpsPort int
	email, certDir      string
}

func configSettings(c *Config) settings {
	return settings{c.HTTPPort, c.HTTPSPort, c.Email, c.CertDir}
}

// Save merges this instance's edits into the latest disk snapshot under an OS
// lock. Atomic rename prevents readers from observing a partially written file.
func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.filePath == "" {
		return fmt.Errorf("配置文件路径为空")
	}
	dir := filepath.Dir(c.filePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}
	lock, err := os.OpenFile(c.filePath+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("打开配置锁失败: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("锁定配置失败: %w", err)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	_, statErr := os.Stat(c.filePath)
	next, err := Load(c.filePath)
	if err != nil {
		return err
	}
	if os.IsNotExist(statErr) {
		for domain, rule := range c.Rules {
			next.Rules[domain] = rule
		}
	}
	for domain, rule := range c.pending {
		if rule == nil {
			delete(next.Rules, domain)
		} else {
			next.Rules[domain] = rule
		}
	}
	// Preserve independently updated settings unless this instance changed them.
	if c.HTTPPort != c.base.httpPort {
		next.HTTPPort = c.HTTPPort
	}
	if c.HTTPSPort != c.base.httpsPort {
		next.HTTPSPort = c.HTTPSPort
	}
	if c.Email != c.base.email {
		next.Email = c.Email
	}
	if c.CertDir != c.base.certDir {
		next.CertDir = c.CertDir
	}
	if err := next.validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".proxctl-*.tmp") // CreateTemp uses mode 0600.
	if err != nil {
		return fmt.Errorf("创建临时配置失败: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), c.filePath); err != nil {
		return fmt.Errorf("替换配置文件失败: %w", err)
	}
	c.HTTPPort, c.HTTPSPort, c.Email, c.CertDir = next.HTTPPort, next.HTTPSPort, next.Email, next.CertDir
	c.Rules = next.Rules
	c.base = configSettings(c)
	clear(c.pending)
	return nil
}
