package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

type daemonManager struct {
	unitPath string
	binary   string
	run      func(context.Context, ...string) error
}

func systemdQuote(value string) string {
	value = strings.ReplaceAll(value, "%", "%%")
	value = strings.ReplaceAll(value, "$", "$$")
	return strconv.Quote(value)
}

func (a *App) runDaemon(ctx context.Context, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.Err, "用法: proxctl daemon <install|start|stop|restart|status>")
		return 1
	}
	sub := args[0]
	switch sub {
	case "install", "start", "stop", "restart", "status":
	default:
		fmt.Fprintf(a.Err, "未知操作: %s\n", sub)
		return 1
	}
	m := a.daemon
	if m == nil {
		if runtime.GOOS != "linux" {
			fmt.Fprintln(a.Err, "daemon 仅支持使用 systemd 的 Linux；本机请使用 proxctl serve。")
			return 1
		}
		binary, err := os.Executable()
		if err != nil {
			fmt.Fprintf(a.Err, "获取可执行文件路径失败: %v\n", err)
			return 1
		}
		m = &daemonManager{unitPath: "/etc/systemd/system/proxctl.service", binary: binary,
			run: func(ctx context.Context, args ...string) error {
				cmd := exec.CommandContext(ctx, "systemctl", args...)
				cmd.Stdout, cmd.Stderr = a.Out, a.Err
				return cmd.Run()
			},
		}
	}
	if sub != "install" {
		if err := m.run(ctx, sub, "proxctl"); err != nil {
			fmt.Fprintf(a.Err, "systemctl %s 失败: %v\n", sub, err)
			return 1
		}
		return 0
	}
	unit := fmt.Sprintf(`[Unit]
Description=proxctl - Reverse proxy and automatic HTTPS
After=network.target

[Service]
Type=exec
ExecStart=%s --config %s serve
Restart=always
RestartSec=3
AmbientCapabilities=CAP_NET_BIND_SERVICE
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
`, systemdQuote(m.binary), systemdQuote(a.Config.Path()))
	if err := os.WriteFile(m.unitPath, []byte(unit), 0644); err != nil {
		fmt.Fprintf(a.Err, "写入 %s 失败 (需要 sudo 权限): %v\n", m.unitPath, err)
		return 1
	}
	// Restart also applies a changed --config path when the service is already active.
	for _, command := range [][]string{{"daemon-reload"}, {"enable", "proxctl"}, {"restart", "proxctl"}} {
		if err := m.run(ctx, command...); err != nil {
			fmt.Fprintf(a.Err, "systemctl %s 失败: %v\n", strings.Join(command, " "), err)
			return 1
		}
	}
	fmt.Fprintln(a.Out, "✔ proxctl systemd 守护进程已成功安装并启动！开机将自动运行。")
	return 0
}
