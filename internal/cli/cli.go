package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/9star-academy/proxctl/internal/config"
	"github.com/9star-academy/proxctl/internal/proxy"
)

const helpText = `proxctl — 一行命令搞定反向代理、静态站点托管与自动 HTTPS 证书

用法 (全局选项 --config <路径> 必须放在子命令之前):
  proxctl add <域名> --to <目标地址> [选项]       添加反向代理（自动申请 HTTPS）
  proxctl static <域名> --dir <静态目录> [选项]   添加前端静态站点托管
  proxctl list                                   查看所有已配置的域名与状态
  proxctl rm <域名>                              删除域名配置
  proxctl serve                                 前台启动代理服务引擎
  proxctl daemon <install|start|stop|restart|status>  Linux systemd 管理
  proxctl --config /path/config.json list        使用指定配置文件
  proxctl --version                              查看版本信息

选项示例:
  sudo proxctl add api.example.com --to 127.0.0.1:8080 --cors
  sudo proxctl add admin.example.com --to 9000 --auth admin:secret123
  sudo proxctl static app.example.com --dir /var/www/dist --spa
  sudo proxctl list
  sudo proxctl daemon install                    (一键安装并启动为 systemd 自启服务)

特性:
  1. 单一 Go 可执行文件，内置 ACME 自动申请与续期 Let's Encrypt 证书。
  2. 自动 HTTP -> HTTPS 301 重定向与 WebSocket 协议透传。
  3. 支持 SPA 单页应用路由回退 (fallback 到 index.html) 与基础鉴权。
  4. 运行期间每秒重载域名规则；监听端口、邮箱和证书目录变更需重启。
`

type App struct {
	Version string
	Out     io.Writer
	Err     io.Writer
	Config  *config.Config
	daemon  *daemonManager
}

func New(version string, out, errOut io.Writer) (*App, error) {
	if out == nil {
		out = os.Stdout
	}
	if errOut == nil {
		errOut = os.Stderr
	}

	return &App{
		Version: version,
		Out:     out,
		Err:     errOut,
	}, nil
}

func (a *App) Run(ctx context.Context, args []string) int {
	configPath := ""
	if len(args) > 0 && (args[0] == "--config" || strings.HasPrefix(args[0], "--config=")) {
		if args[0] == "--config" {
			if len(args) < 2 {
				fmt.Fprintln(a.Err, "--config 需要指定配置文件路径")
				return 1
			}
			configPath, args = args[1], args[2:]
		} else {
			configPath, args = strings.TrimPrefix(args[0], "--config="), args[1:]
		}
		if configPath == "" {
			fmt.Fprintln(a.Err, "--config 路径不能为空")
			return 1
		}
	}
	if len(args) == 0 {
		fmt.Fprint(a.Out, helpText)
		return 0
	}

	cmd := args[0]
	subArgs := args[1:]
	switch cmd {
	case "-v", "--version", "version":
		fmt.Fprintf(a.Out, "proxctl version %s\n", a.Version)
		return 0
	case "-h", "--help", "help":
		fmt.Fprint(a.Out, helpText)
		return 0
	case "add", "static", "rm", "delete", "list", "ls", "serve", "daemon":
	default:
		fmt.Fprintf(a.Err, "未知命令: %s\n运行 'proxctl --help' 查看帮助\n", cmd)
		return 1
	}
	if a.Config == nil || configPath != "" {
		cfg, err := config.Load(configPath)
		if err != nil {
			fmt.Fprintf(a.Err, "加载配置失败: %v\n", err)
			if errors.Is(err, os.ErrPermission) {
				fmt.Fprintln(a.Err, "系统配置需要 sudo 权限；也可用 --config 明确选择自己的配置文件。")
			}
			return 1
		}
		a.Config = cfg
	}

	switch cmd {
	case "add":
		return a.runAdd(ctx, subArgs)
	case "static":
		return a.runStatic(ctx, subArgs)
	case "rm", "delete":
		return a.runRemove(ctx, subArgs)
	case "list", "ls":
		if len(subArgs) != 0 {
			fmt.Fprintln(a.Err, "list 不接受额外参数")
			return 1
		}
		return a.runList(ctx)
	case "serve":
		return a.runServe(ctx, subArgs)
	case "daemon":
		return a.runDaemon(ctx, subArgs)
	default:
		return 1
	}
}

func (a *App) runAdd(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.Err, "用法: proxctl add <域名> --to <目标地址> [--cors] [--auth user:pass]")
		return 1
	}

	domain := strings.ToLower(strings.TrimSpace(args[0]))
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(a.Err)

	to := fs.String("to", "", "反向代理目标地址 (如 127.0.0.1:8080 或 8080)")
	cors := fs.Bool("cors", false, "自动添加跨域 CORS 响应头")
	auth := fs.String("auth", "", "开启 Basic Auth 鉴权 (如 admin:password)")

	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.Err, "add 包含无法识别的参数")
		return 1
	}

	if *to == "" {
		fmt.Fprintln(a.Err, "❌ 错误: 必须指定反向代理目标地址 (--to)")
		return 1
	}

	target := *to
	if !strings.Contains(target, ":") {
		// Just port like "8080"
		target = "127.0.0.1:" + target
	}

	rule := &config.Rule{
		Domain: domain,
		Type:   config.TypeProxy,
		Target: target,
		CORS:   *cors,
		Auth:   *auth,
	}

	if err := a.Config.AddRule(rule); err != nil {
		fmt.Fprintf(a.Err, "添加规则失败: %v\n", err)
		return 1
	}

	if err := a.Config.Save(); err != nil {
		fmt.Fprintf(a.Err, "保存配置失败: %v\n", err)
		return 1
	}

	fmt.Fprintf(a.Out, "✔ 反向代理配置成功: %s -> %s\n", domain, target)
	fmt.Fprintf(a.Out, "  - 自动证书: 将由运行中的引擎按需申请 (Let's Encrypt / ACME)\n")
	if *cors {
		fmt.Fprintln(a.Out, "  - 跨域支持: 已开启 (Access-Control-Allow-Origin: *)")
	}
	if *auth != "" {
		fmt.Fprintf(a.Out, "  - 访问认证: 已启用 (Basic Auth: %s)\n", strings.Split(*auth, ":")[0])
	}
	fmt.Fprintln(a.Out, "\n规则已保存，运行中的引擎将在约 1 秒内加载。请确保 DNS 已指向本机且 TCP 80/443 可达。")
	fmt.Fprintln(a.Out, "尚未启动时，请运行 'sudo proxctl daemon install' 或使用相同配置运行 'proxctl serve'。")

	return 0
}

func (a *App) runStatic(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.Err, "用法: proxctl static <域名> --dir <静态目录> [--spa] [--auth user:pass]")
		return 1
	}

	domain := strings.ToLower(strings.TrimSpace(args[0]))
	fs := flag.NewFlagSet("static", flag.ContinueOnError)
	fs.SetOutput(a.Err)

	dir := fs.String("dir", "", "静态文件根目录路径")
	spa := fs.Bool("spa", false, "开启单页应用 (SPA) 路由回退 (404 自动回退至 index.html)")
	auth := fs.String("auth", "", "开启 Basic Auth 鉴权 (如 admin:password)")

	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(a.Err, "static 包含无法识别的参数")
		return 1
	}

	if *dir == "" {
		fmt.Fprintln(a.Err, "❌ 错误: 必须指定静态文件根目录 (--dir)")
		return 1
	}

	absDir, err := filepath.Abs(*dir)
	if err != nil {
		fmt.Fprintf(a.Err, "解析目录路径失败: %v\n", err)
		return 1
	}
	info, err := os.Stat(absDir)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(a.Err, "静态目录不存在或不是目录: %s\n", absDir)
		return 1
	}

	rule := &config.Rule{
		Domain:    domain,
		Type:      config.TypeStatic,
		StaticDir: absDir,
		SPA:       *spa,
		Auth:      *auth,
	}

	if err := a.Config.AddRule(rule); err != nil {
		fmt.Fprintf(a.Err, "添加规则失败: %v\n", err)
		return 1
	}

	if err := a.Config.Save(); err != nil {
		fmt.Fprintf(a.Err, "保存配置失败: %v\n", err)
		return 1
	}

	fmt.Fprintf(a.Out, "✔ 静态站点配置成功: %s -> %s\n", domain, absDir)
	fmt.Fprintln(a.Out, "规则已保存，运行中的引擎将在约 1 秒内加载。")
	if *spa {
		fmt.Fprintln(a.Out, "  - SPA 路由: 已开启 (自动回退至 index.html)")
	}

	return 0
}

func (a *App) runRemove(ctx context.Context, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.Err, "用法: proxctl rm <域名>")
		return 1
	}

	domain := strings.ToLower(strings.TrimSpace(args[0]))
	if a.Config.RemoveRule(domain) {
		if err := a.Config.Save(); err != nil {
			fmt.Fprintf(a.Err, "保存配置失败: %v\n", err)
			return 1
		}
		fmt.Fprintf(a.Out, "✔ 域名 %s 的配置已删除\n", domain)
		fmt.Fprintln(a.Out, "运行中的引擎将在约 1 秒内加载删除结果。")
		return 0
	}

	fmt.Fprintf(a.Err, "未找到域名 %s 的配置规则\n", domain)
	return 1
}

func (a *App) runList(ctx context.Context) int {
	rules := a.Config.ListRules()
	if len(rules) == 0 {
		fmt.Fprintln(a.Out, "当前暂无配置的域名。使用 'proxctl add' 或 'proxctl static' 开始添加。")
		return 0
	}

	fmt.Fprintf(a.Out, "%-24s %-10s %-30s %-8s %s\n", "域名", "类型", "后端目标 / 静态目录", "HTTPS", "特性")
	fmt.Fprintln(a.Out, "------------------------------------------------------------------------------------------------")

	for _, r := range rules {
		target := r.Target
		if r.Type == config.TypeStatic {
			target = r.StaticDir
		}

		features := make([]string, 0)
		if r.CORS {
			features = append(features, "CORS")
		}
		if r.SPA {
			features = append(features, "SPA")
		}
		if r.Auth != "" {
			features = append(features, "Auth")
		}
		featureStr := strings.Join(features, ", ")
		if featureStr == "" {
			featureStr = "-"
		}

		fmt.Fprintf(a.Out, "%-24s %-10s %-30s %-8s %s\n",
			r.Domain,
			strings.ToUpper(string(r.Type)),
			target,
			"Auto",
			featureStr,
		)
	}
	return 0
}

func (a *App) runServe(ctx context.Context, args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(a.Err, "serve 不接受额外参数；请通过配置文件设置端口")
		return 1
	}
	if _, err := os.Stat(a.Config.Path()); os.IsNotExist(err) {
		if err := a.Config.Save(); err != nil {
			fmt.Fprintf(a.Err, "初始化配置失败: %v\n", err)
			return 1
		}
	}
	logger := log.New(a.Out, "[proxctl] ", log.LstdFlags)
	server := proxy.NewServer(a.Config, logger)

	fmt.Fprintf(a.Out, "🚀 proxctl 引擎正在启动 (HTTP :%d / HTTPS :%d)，配置: %s\n", a.Config.HTTPPort, a.Config.HTTPSPort, a.Config.Path())
	if err := server.Start(ctx); err != nil {
		fmt.Fprintf(a.Err, "❌ 服务异常停止: %v\n", err)
		return 1
	}
	return 0
}
