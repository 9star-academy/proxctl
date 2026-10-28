package proxy

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/9star-academy/proxctl/internal/config"
	"golang.org/x/crypto/acme/autocert"
)

// Server handles reverse proxying and automated HTTPS certificate provisioning.
type Server struct {
	Config      *config.Config
	CertManager *autocert.Manager
	httpServer  *http.Server
	httpsServer *http.Server
	proxies     sync.Map // domain -> *httputil.ReverseProxy
	Logger      *log.Logger
}

func NewServer(cfg *config.Config, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}

	certDir := cfg.CertDir
	if err := os.MkdirAll(certDir, 0700); err != nil {
		logger.Printf("警告: 创建证书目录失败: %v", err)
	}

	s := &Server{
		Config: cfg,
		Logger: logger,
	}

	s.CertManager = &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(certDir),
		HostPolicy: s.hostPolicy,
	}
	if cfg.Email != "" {
		s.CertManager.Email = cfg.Email
	}

	return s
}

// hostPolicy ensures ACME certificates are only requested for domains in config.
func (s *Server) hostPolicy(ctx context.Context, host string) error {
	host = strings.ToLower(host)
	if _, ok := s.Config.GetRule(host); ok {
		return nil
	}
	return fmt.Errorf("domain %s is not configured in proxctl", host)
}

// Start launches the HTTP and HTTPS listeners.
func (s *Server) Start(ctx context.Context) error {
	httpMux := http.NewServeMux()

	// 1. HTTP Handler: Handles ACME HTTP-01 challenge, redirects all other requests to HTTPS
	httpMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}

		targetURL := "https://" + host + r.RequestURI
		http.Redirect(w, r, targetURL, http.StatusMovedPermanently)
	})

	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.Config.HTTPPort),
		Handler:      s.CertManager.HTTPHandler(httpMux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// 2. HTTPS Handler: Main router
	s.httpsServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", s.Config.HTTPSPort),
		Handler:      http.HandlerFunc(s.ServeHTTP),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
		TLSConfig: &tls.Config{
			GetCertificate: s.CertManager.GetCertificate,
			MinVersion:     tls.VersionTLS12,
		},
	}

	errChan := make(chan error, 2)

	go func() {
		s.Logger.Printf("HTTP 服务监听于 :%d (ACME 验证与 HTTPS 重定向)", s.Config.HTTPPort)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- fmt.Errorf("HTTP 监听失败: %w", err)
		}
	}()

	go func() {
		s.Logger.Printf("HTTPS 服务监听于 :%d (自动证书与业务代理)", s.Config.HTTPSPort)
		if err := s.httpsServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			errChan <- fmt.Errorf("HTTPS 监听失败: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		s.Shutdown()
		return nil
	case err := <-errChan:
		s.Shutdown()
		return err
	}
}

func (s *Server) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if s.httpServer != nil {
		_ = s.httpServer.Shutdown(ctx)
	}
	if s.httpsServer != nil {
		_ = s.httpsServer.Shutdown(ctx)
	}
}

// ServeHTTP handles incoming HTTP/HTTPS routing.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if strings.Contains(host, ":") {
		h, _, err := net.SplitHostPort(host)
		if err == nil {
			host = h
		}
	}
	host = strings.ToLower(host)

	rule, ok := s.Config.GetRule(host)
	if !ok {
		http.Error(w, fmt.Sprintf("proxctl: 未配置域名 %s 的反代或静态规则", host), http.StatusNotFound)
		return
	}

	// 1. Basic Auth check
	if rule.Auth != "" {
		if !checkBasicAuth(r, rule.Auth) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted by proxctl"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// 2. CORS headers
	if rule.CORS {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}

	// 3. Static Site
	if rule.Type == config.TypeStatic {
		s.serveStatic(w, r, rule)
		return
	}

	// 4. Reverse Proxy
	s.serveProxy(w, r, rule)
}

func (s *Server) serveProxy(w http.ResponseWriter, r *http.Request, rule *config.Rule) {
	val, ok := s.proxies.Load(rule.Domain)
	var proxy *httputil.ReverseProxy

	if ok {
		proxy = val.(*httputil.ReverseProxy)
	} else {
		target := rule.Target
		if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
			target = "http://" + target
		}
		targetURL, err := url.Parse(target)
		if err != nil {
			http.Error(w, "Target URL invalid", http.StatusInternalServerError)
			return
		}

		proxy = httputil.NewSingleHostReverseProxy(targetURL)
		origDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			origDirector(req)
			req.Host = targetURL.Host
			req.Header.Set("X-Forwarded-Host", r.Host)
			req.Header.Set("X-Forwarded-Proto", "https")
			if clientIP, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
				req.Header.Set("X-Real-IP", clientIP)
			}
		}

		proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			s.Logger.Printf("代理请求失败 [%s -> %s]: %v", rule.Domain, rule.Target, err)
			http.Error(w, fmt.Sprintf("proxctl: 无法连接目标后端 %s: %v", rule.Target, err), http.StatusBadGateway)
		}

		s.proxies.Store(rule.Domain, proxy)
	}

	proxy.ServeHTTP(w, r)
}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request, rule *config.Rule) {
	dir := rule.StaticDir
	fs := http.Dir(dir)

	cleanPath := filepath.Clean(r.URL.Path)
	localPath := filepath.Join(dir, cleanPath)

	fi, err := os.Stat(localPath)
	if err == nil && !fi.IsDir() {
		http.FileServer(fs).ServeHTTP(w, r)
		return
	}

	// Check if directory has index.html
	if err == nil && fi.IsDir() {
		indexPath := filepath.Join(localPath, "index.html")
		if _, err := os.Stat(indexPath); err == nil {
			http.FileServer(fs).ServeHTTP(w, r)
			return
		}
	}

	// If SPA mode is enabled and file not found, serve index.html
	if rule.SPA {
		rootIndex := filepath.Join(dir, "index.html")
		if _, err := os.Stat(rootIndex); err == nil {
			http.ServeFile(w, r, rootIndex)
			return
		}
	}

	http.FileServer(fs).ServeHTTP(w, r)
}

func checkBasicAuth(r *http.Request, expected string) bool {
	auth := r.Header.Get("Authorization")
	if auth == "" || !strings.HasPrefix(auth, "Basic ") {
		return false
	}

	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(auth, "Basic "))
	if err != nil {
		return false
	}

	return string(payload) == expected
}
