package cli

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/9star-academy/proxctl/internal/config"
)

const doctorTimeout = 4 * time.Second

func (a *App) runDoctor(ctx context.Context, args []string, configPath string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	expected := fs.String("expect-ip", "", "DNS 记录中应包含的服务器 IP")
	port := fs.Int("https-port", 443, "要检查的对外 HTTPS 端口")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "用法: proxctl doctor <域名> [--expect-ip <IP>] [--https-port <端口>]")
		fs.PrintDefaults()
	}
	if len(args) == 0 {
		fs.Usage()
		return 1
	}
	if args[0] == "--help" || args[0] == "-h" {
		fs.SetOutput(a.Out)
		fs.Usage()
		return 0
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return 1
	}
	domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(args[0])), ".")
	if err := config.ValidateDomain(domain); err != nil {
		fmt.Fprintln(a.Err, err)
		return 1
	}
	var expectedIP netip.Addr
	if *expected != "" {
		ip, err := netip.ParseAddr(*expected)
		if err != nil || ip.Zone() != "" {
			fmt.Fprintln(a.Err, "--expect-ip 必须是 IPv4 或 IPv6 地址")
			return 1
		}
		expectedIP = ip.Unmap()
	}
	if *port < 1 || *port > 65535 {
		fmt.Fprintln(a.Err, "--https-port 必须在 1-65535 之间")
		return 1
	}

	fmt.Fprintf(a.Out, "排障 %s\n", domain)
	rule := a.doctorRule(configPath, domain)
	resolved, dnsOK := a.checkDoctorDNS(ctx, domain, expectedIP)
	dialer := &net.Dialer{Timeout: doctorTimeout}
	backendOK := a.checkDoctorBackend(ctx, dialer, rule)

	httpsOK := false
	if resolved {
		tlsDialer := &tls.Dialer{NetDialer: dialer, Config: &tls.Config{MinVersion: tls.VersionTLS12}}
		httpsOK = a.checkDoctorHTTPS(ctx, tlsDialer, net.JoinHostPort(domain, strconv.Itoa(*port)))
	} else {
		fmt.Fprintln(a.Out, "[跳过] HTTPS 需要先解析域名")
	}
	if rule == nil || !dnsOK || !backendOK || !httpsOK {
		return 1
	}
	return 0
}

func (a *App) doctorRule(path, domain string) *config.Rule {
	cfg, err := config.LoadReadOnly(path)
	if err != nil {
		fmt.Fprintf(a.Out, "[失败] 配置 %v\n", err)
		if errors.Is(err, os.ErrPermission) {
			fmt.Fprintln(a.Out, "请使用 sudo 或通过 --config 选择正确的配置文件。")
		}
		return nil
	}
	rule, ok := cfg.GetRule(domain)
	if !ok {
		fmt.Fprintf(a.Out, "[失败] 配置 %s 中没有该域名的规则\n", cfg.Path())
		return nil
	}
	fmt.Fprintf(a.Out, "[正常] 配置 %s 中存在该域名的规则\n", cfg.Path())
	return rule
}

func (a *App) checkDoctorDNS(ctx context.Context, domain string, expected netip.Addr) (resolved, ok bool) {
	lookupCtx, cancel := context.WithTimeout(ctx, doctorTimeout)
	defer cancel()
	addresses, err := net.DefaultResolver.LookupNetIP(lookupCtx, "ip", domain)
	if err != nil || len(addresses) == 0 {
		fmt.Fprintf(a.Out, "[失败] DNS 无法解析 %s，请检查 A / AAAA 记录和 DNS 服务\n", domain)
		return false, false
	}
	if expected.IsValid() {
		if !slices.ContainsFunc(addresses, func(ip netip.Addr) bool { return ip.Unmap() == expected }) {
			fmt.Fprintf(a.Out, "[失败] DNS 解析为 %v，缺少预期 IP %s\n", addresses, expected)
			return true, false
		}
		fmt.Fprintf(a.Out, "[正常] DNS 解析为 %v，包含预期 IP %s\n", addresses, expected)
		return true, true
	}
	local, _ := net.InterfaceAddrs()
	for _, addr := range local {
		prefix, err := netip.ParsePrefix(addr.String())
		if err != nil {
			continue
		}
		if slices.ContainsFunc(addresses, func(ip netip.Addr) bool { return ip.Unmap() == prefix.Addr().Unmap() }) {
			fmt.Fprintf(a.Out, "[正常] DNS 解析为 %v，包含本机 IP %s\n", addresses, prefix.Addr())
			return true, true
		}
	}
	fmt.Fprintf(a.Out, "[待确认] DNS 解析为 %v，请用 --expect-ip 核对服务器公网或 CDN 地址\n", addresses)
	return true, true
}

func (a *App) checkDoctorBackend(ctx context.Context, dialer *net.Dialer, rule *config.Rule) bool {
	if rule == nil {
		fmt.Fprintln(a.Out, "[跳过] 后端缺少配置规则")
		return false
	}
	if rule.Type == config.TypeStatic {
		root, err := os.OpenRoot(rule.StaticDir)
		if err != nil {
			fmt.Fprintf(a.Out, "[失败] 静态目录 %v\n", err)
			return false
		}
		defer root.Close()
		if rule.SPA {
			info, err := root.Stat("index.html")
			if err != nil || !info.Mode().IsRegular() {
				fmt.Fprintln(a.Out, "[失败] 静态目录缺少可用的 SPA 入口 index.html")
				return false
			}
		}
		fmt.Fprintf(a.Out, "[正常] 静态目录 %s 可打开\n", rule.StaticDir)
		return true
	}
	target, err := config.TargetURL(rule.Target)
	if err != nil {
		fmt.Fprintf(a.Out, "[失败] 后端 %v\n", err)
		return false
	}
	port := target.Port()
	if port == "" {
		port = "80"
		if target.Scheme == "https" {
			port = "443"
		}
	}
	address := net.JoinHostPort(target.Hostname(), port)
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		fmt.Fprintf(a.Out, "[失败] 后端 %s %s，请检查服务和监听端口\n", address, doctorConnectionError(err))
		return false
	}
	conn.Close()
	fmt.Fprintf(a.Out, "[正常] 后端 %s 的 TCP 端口可连接\n", address)
	return true
}

func (a *App) checkDoctorHTTPS(ctx context.Context, dialer *tls.Dialer, address string) bool {
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		fmt.Fprintf(a.Out, "[失败] HTTPS %s %s\n", address, doctorTLSError(err))
		return false
	}
	defer conn.Close()
	cert := conn.(*tls.Conn).ConnectionState().PeerCertificates[0]
	fmt.Fprintf(a.Out, "[正常] HTTPS %s（连接到 %s）证书有效，到期 %s\n", address, conn.RemoteAddr(), cert.NotAfter.UTC().Format(time.RFC3339))
	return true
}

func doctorTLSError(err error) string {
	var hostname x509.HostnameError
	var authority x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var verification *tls.CertificateVerificationError
	switch {
	case errors.As(err, &hostname):
		return "证书域名不匹配"
	case errors.As(err, &authority):
		return "证书不受信任，请检查签发机构和证书链"
	case errors.As(err, &invalid) && invalid.Reason == x509.Expired:
		return "证书已过期或尚未生效"
	case errors.As(err, &verification):
		return "证书校验失败"
	default:
		return doctorConnectionError(err) + "，请检查 HTTPS 监听、端口转发和证书签发日志"
	}
}

func doctorConnectionError(err error) string {
	var netErr net.Error
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "拒绝连接"
	case errors.As(err, &netErr) && netErr.Timeout():
		return "连接或握手超时"
	default:
		return "连接或握手失败"
	}
}
