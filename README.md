# proxctl

一行命令配置反向代理、静态站点托管与自动 HTTPS。

[Release](https://github.com/9star-academy/proxctl/releases) · [MIT License](LICENSE)

`proxctl` 适合在已经运行后端服务的 Linux 服务器上绑定域名，也可以托管 Vue、React、Flutter Web 等前端构建产物。提供单一 Go 可执行文件，无需另装 Nginx 或 certbot。

## 已支持的功能

- 按域名反向代理 HTTP/HTTPS 后端，透传 WebSocket。
- 从当前请求生成 `X-Forwarded-For`、`X-Forwarded-Host`、`X-Forwarded-Proto` 和 `X-Real-IP`，覆盖客户端伪造的同名转发头。部署在其他代理之后时，这些 IP 表示直接连接 proxctl 的上一跳。
- 使用 Let's Encrypt ACME 按需申请并自动续期证书，HTTP 入口负责域名验证和跳转到 HTTPS。
- 静态文件托管，可通过 `--spa` 显式开启回退到根目录 `index.html`。
- Basic Auth，以及反向代理规则的 `--cors` 跨域响应与预检处理。
- 每秒检查并重载域名规则；新增、修改、删除规则无需重启。
- Linux systemd 服务安装、启动、停止、重启和状态查询。

当前没有 `proxctl logs`、证书有效期展示、Gzip/Brotli 压缩、请求限流或 ZeroSSL 配置入口。`list` 中的 `Auto` 代表自动证书模式，不代表证书已签发或服务正在运行。

## 安装

支持 Linux、macOS 的 amd64 和 arm64；`daemon` 命令仅支持使用 systemd 的 Linux，服务单元使用 [`Type=exec`](https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html#Type=)，需要 systemd 支持该类型。

```bash
curl -fsSL https://raw.githubusercontent.com/9star-academy/proxctl/main/scripts/install.sh | sudo bash
proxctl --version
```

安装脚本下载 GitHub **最新 Release** 的二进制到 `/usr/local/bin/proxctl`；源码中的新改动需要发布后才会包含在该安装包里。脚本不会自动启动服务。也可直接从 [Releases](https://github.com/9star-academy/proxctl/releases) 下载对应系统的文件。

## Linux 快速开始

先确认：

- 域名的 A/AAAA 记录指向本机；设置了 AAAA 时，也要保证 IPv6 可达。
- 公网 TCP 80 和 443 已放行，且未被其他服务占用。
- 后端服务已启动，例如监听 `127.0.0.1:8080`。

```bash
# 添加域名，目标可以是端口、host:port 或完整 HTTP(S) URL
sudo proxctl add api.example.com --to 127.0.0.1:8080

# 安装、启动服务并设为开机自启（首次执行）
sudo proxctl daemon install
sudo proxctl daemon status

# 添加前端站点；目录必须存在
sudo proxctl static app.example.com --dir /var/www/app/dist --spa

# 密码中有特殊字符时使用 shell 引号
sudo proxctl add admin.example.com --to 9000 --auth 'admin:change-this-password'

# 为 API 开启 CORS
sudo proxctl add api.example.com --to 8080 --cors

# 查看配置；这不等于服务或证书健康检查
sudo proxctl list

# 删除域名，运行中的服务约 1 秒内停止路由该域名的新请求
sudo proxctl rm admin.example.com
```

再次对同一域名执行 `add` 或 `static` 会替换整条规则，未提供的 `--auth`、`--cors`、`--spa` 会关闭。更新目标地址后，后续请求会使用新后端。已经建立的请求或 WebSocket 连接不会因规则重载而被强制断开。

删除域名后，新建 HTTPS 连接会被域名策略拒绝；沿用已有 HTTPS 连接的新请求返回 404。

首次 HTTPS 访问可能等待证书签发。命令提示“配置成功”表示规则已保存；实际签发仍取决于 DNS、网络和 Let's Encrypt 的验证结果。

`--cors` 使用 `Access-Control-Allow-Origin: *`，适合允许任意来源调用的 API。与 Basic Auth 一起使用时，预检无需认证，实际请求仍需 `Authorization`；此模式不支持浏览器 `credentials: 'include'`，也不会设置 `Access-Control-Allow-Credentials`。

## 配置路径、权限与重载

默认选择规则：

1. root 使用 `/etc/proxctl/config.json`。
2. 非 root 如果发现系统配置，也选择该文件；无权限时明确提示使用 sudo，不会悄悄切换到个人配置。
3. 没有系统配置时，非 root 使用 `~/.config/proxctl/config.json`。

可以在**子命令之前**指定配置文件，所有操作必须指向同一个文件：

```bash
sudo proxctl --config /etc/proxctl/custom.json add api.example.com --to 8080
sudo proxctl --config /etc/proxctl/custom.json daemon install
sudo proxctl --config /etc/proxctl/custom.json list
```

systemd 单元会保存安装时选定的配置路径。一个 `proxctl.service` 对应一个配置文件；对其他文件执行 `daemon install` 会替换该服务的配置并重启服务。

新建配置目录权限为 `0700`，配置文件为 `0600`；读取旧配置时也会将文件权限收紧到 `0600`。Basic Auth 密码仍以明文存于配置文件中，只允许文件所有者或 root 读取。配置变更使用文件锁和原子替换，并发 CLI 修改不同域名不会互相覆盖；同时修改同一域名时，以后取得写入锁的操作为准。不要在服务运行期间手动删除 `.lock` 文件。

运行中的服务每秒加载有效规则。文件丢失、JSON 损坏或规则无效时，服务保留上一次有效规则并记录错误；修复文件后会继续重载。因此，若要停止某个站点，应使用 `rm`，不要通过破坏或删除配置文件来停站。

下面的全局字段需要手动编辑配置文件，修改后重启服务：

| 字段 | 默认值 | 用途 |
| --- | --- | --- |
| `http_port` | `80` | HTTP 验证与 HTTPS 跳转端口 |
| `https_port` | `443` | HTTPS 服务端口 |
| `email` | 空 | ACME 账户联系邮箱 |
| `cert_dir` | root 为 `/etc/proxctl/certs`，普通用户为 `~/.config/proxctl/certs` | 证书及账户缓存 |

使用自定义本地端口不会改变 Let's Encrypt 对公网验证入口的要求；公网部署仍需将 80/443 正确转发到服务。证书目录需要可写，并应在升级、重启时保留。

macOS 或本地调试可使用个人配置，将监听端口改为 `8080`、`8443`，然后前台运行：

```bash
proxctl --config /absolute/path/config.json serve
```

## 服务管理、排障与升级

```bash
sudo proxctl daemon start
sudo proxctl daemon stop
sudo proxctl daemon restart
sudo proxctl daemon status

# systemd 标准输出、引擎错误和规则重载日志；当前没有逐请求访问日志
sudo journalctl -u proxctl -f

# 从源码构建并安装新二进制后，必须重启旧进程才能运行新代码
sudo proxctl daemon restart
```

`daemon install`、配置写入等操作失败时会返回非零退出码并显示原因。查看 `systemctl status proxctl` 和上述日志，确认服务运行；再用 `curl -I https://api.example.com` 确认 HTTPS。这比仅查看 `list` 更能判断站点是否可用。

升级前备份配置和证书目录。回滚时恢复旧二进制并重启服务；本次配置格式与 V1 保持兼容。原先运行的旧版进程需要先重启，才能启用规则自动重载。

## 开发与验证

需要 Go 1.26 或更高版本，不需要新增运行时组件。

```bash
go test -race -timeout 90s -coverprofile=/tmp/proxctl-coverage.out ./...
go tool cover -func=/tmp/proxctl-coverage.out
go vet ./...
bash -n scripts/install.sh
go build -o /tmp/proxctl ./cmd/proxctl

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/proxctl-linux-amd64 ./cmd/proxctl
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /tmp/proxctl-linux-arm64 ./cmd/proxctl
```

回归测试使用临时配置、本机 HTTP/HTTPS 服务、测试证书和模拟安装命令，覆盖规则重载/删除、配置损坏恢复、并发写入、权限迁移、转发头、认证 CORS、WebSocket 双向传输及失败退出码；不会申请公网证书或修改系统服务。CI 在 Linux 和 macOS 上执行测试、race、vet 与跨平台构建。

CI 另在临时 runner 上验证候选程序；发布工作流则从上面公开的一键安装入口下载实际 Release 包，再验证前台服务、HTTPS、静态站点、反向代理、认证跨域和规则重载。Linux 验收还会安装并实际启动、停止、重启 systemd 服务，结束后清理测试配置。验收脚本为 `scripts/verify-install.py`，其中 systemd 模式仅允许在全新的 Linux CI 环境执行。

上线前仍需在实际 Linux 服务器验证 systemd 开机启动、真实域名签发、证书续期和回滚。TLS 测试使用本地证书，交叉编译也不等同于 Linux 实机验收。

## 9star-academy 裸机轻量运维矩阵

| 工具 | 定位 |
| --- | --- |
| [ShellReady](https://github.com/9star-academy/ShellReady) | Linux 终端与 Shell 环境配置 |
| [secctl](https://github.com/9star-academy/secctl) | SSH 加固与防火墙管理 |
| [servctl](https://github.com/9star-academy/servctl) | systemd 服务托管 |
| [proxctl](https://github.com/9star-academy/proxctl) | 反向代理、静态托管与自动 HTTPS |
| [bakctl](https://github.com/9star-academy/bakctl) | 数据备份 |

## License

[MIT](LICENSE)
