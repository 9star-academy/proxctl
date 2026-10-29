<h1 align="center">proxctl</h1>

<p align="center">
  <strong>一行命令，让服务拥有域名与 HTTPS。</strong><br>
  反向代理 · 静态站点托管 · 自动证书
</p>

<p align="center">
  <a href="https://github.com/9star-academy/proxctl/actions/workflows/ci.yml"><img src="https://github.com/9star-academy/proxctl/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/9star-academy/proxctl/releases"><img src="https://img.shields.io/github/v/release/9star-academy/proxctl?color=0ea5e9" alt="Latest release"></a>
  <a href="go.mod"><img src="https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white" alt="Go 1.26+"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-22c55e" alt="MIT License"></a>
</p>

<p align="center">
  <a href="#安装">安装</a> ·
  <a href="#快速开始">快速开始</a> ·
  <a href="#使用示例">使用示例</a> ·
  <a href="#命令速查">命令速查</a> ·
  <a href="#配置说明">配置说明</a> ·
  <a href="#服务管理">服务管理</a> ·
  <a href="#开发与验证">开发与验证</a>
</p>

---

`proxctl` 是一个轻量的命令行代理工具，适合为 Linux 服务器上已经运行的后端服务绑定域名，也可以托管 Vue、React、Flutter Web 等前端构建产物。**单一 Go 可执行文件，无需另装 Nginx 或 certbot。**

## 功能一览

| 能力 | 说明 |
| --- | --- |
| **反向代理** | 按域名转发到 HTTP/HTTPS 后端，支持 WebSocket 透传 |
| **自动 HTTPS** | 使用 Let's Encrypt ACME 按需签发并自动续期，HTTP 自动跳转 HTTPS |
| **静态站点** | 托管前端构建产物，使用 `--spa` 开启单页应用路由回退 |
| **认证与跨域** | 支持 Basic Auth；反向代理可通过 `--cors` 处理跨域响应与预检 |
| **规则热重载** | 每秒检查配置，新增、修改、删除域名规则无需重启 |
| **服务管理** | 集成 Linux systemd，支持开机自启及启动、停止、重启、状态查询 |

## 安装

| 平台 | 架构 | 运行方式 |
| --- | --- | --- |
| Linux | amd64 / arm64 | 前台运行，或通过 systemd 管理服务 |
| macOS | amd64 / arm64 | 使用 `proxctl serve` 前台运行 |

```bash
curl -fsSL https://raw.githubusercontent.com/9star-academy/proxctl/main/scripts/install.sh | sudo bash
proxctl --version
```

安装脚本将 GitHub **最新 Release** 的二进制下载到 `/usr/local/bin/proxctl`，不会自动启动服务。也可从 [Releases](https://github.com/9star-academy/proxctl/releases) 手动下载对应平台的文件。

> **版本与环境**：源码中的改动需要发布后才会包含在安装包中。`daemon` 仅适用于使用 systemd 的 Linux，服务单元使用 [`Type=exec`](https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html#Type=)，需要 systemd 支持该类型。

## 快速开始

以下以 **Linux + systemd** 为例，将 `api.example.com` 指向本机的后端服务。

### 1. 准备域名与服务

- 域名的 A/AAAA 记录指向本机；设置了 AAAA 时，也要保证 IPv6 可达。
- 公网 TCP 80 和 443 已放行，且未被其他服务占用。
- 后端服务已启动，例如监听 `127.0.0.1:8080`。

### 2. 添加域名并启动

```bash
# 添加反向代理规则
sudo proxctl add api.example.com --to 127.0.0.1:8080

# 安装、启动服务并设为开机自启（首次执行）
sudo proxctl daemon install
```

### 3. 验证访问

```bash
sudo proxctl daemon status
curl -I https://api.example.com
```

确认服务处于运行状态，并能通过 HTTPS 收到后端响应。首次 HTTPS 访问可能等待证书签发。

> **配置保存 ≠ 站点已就绪**：命令提示“配置成功”表示规则已保存；实际签发仍取决于 DNS、网络和 Let's Encrypt 的验证结果。`list` 中的 `Auto` 仅代表自动证书模式，不代表证书已签发或服务正在运行。

## 使用示例

### 反向代理后端

目标支持端口、`host:port` 或完整 HTTP(S) URL；只填写端口时，默认使用 `127.0.0.1`。

```bash
sudo proxctl add api.example.com --to 8080
```

WebSocket 自动透传。从当前请求生成 `X-Forwarded-For`、`X-Forwarded-Host`、`X-Forwarded-Proto` 和 `X-Real-IP`，覆盖客户端伪造的同名转发头；部署在其他代理之后时，这些 IP 表示直接连接 proxctl 的上一跳。

### 托管前端站点

静态目录必须已存在。Vue、React、Flutter Web 等单页应用可用 `--spa` 开启回退到根目录 `index.html`；普通静态站点可省略该选项。

```bash
sudo proxctl static app.example.com --dir /var/www/app/dist --spa
```

### 添加访问认证

```bash
# 密码中有特殊字符时使用 shell 引号
sudo proxctl add admin.example.com --to 9000 --auth 'admin:change-this-password'
```

反向代理和静态站点均支持 `--auth '用户名:密码'`。密码以明文保存在配置文件中，文件权限说明见[配置说明](#配置说明)。

### 开启 API 跨域

```bash
sudo proxctl add api.example.com --to 8080 --cors
```

`--cors` 使用 `Access-Control-Allow-Origin: *`，适合允许任意来源调用的 API。与 Basic Auth 一起使用时，预检无需认证，实际请求仍需 `Authorization`；此模式不支持浏览器 `credentials: 'include'`，也不会设置 `Access-Control-Allow-Credentials`。

### 查看与删除规则

```bash
# 查看配置；这不等于服务或证书健康检查
sudo proxctl list

# 删除域名，运行中的服务约 1 秒内停止路由该域名的新请求
sudo proxctl rm admin.example.com
```

> **更新会替换整条规则**：再次对同一域名执行 `add` 或 `static` 时，未提供的 `--auth`、`--cors`、`--spa` 会关闭。修改规则时，请同时传入需要保留的选项。

更新目标地址后，后续请求会使用新后端；已经建立的请求或 WebSocket 连接不会因规则重载而被强制断开。删除域名后，新建 HTTPS 连接会被域名策略拒绝，沿用已有 HTTPS 连接的新请求返回 404。

## 命令速查

| 命令 | 用途 | 常用选项 |
| --- | --- | --- |
| `proxctl add <域名> --to <目标>` | 添加或替换反向代理规则 | `--auth`、`--cors` |
| `proxctl static <域名> --dir <目录>` | 添加或替换静态站点规则 | `--auth`、`--spa` |
| `proxctl list` | 查看已配置的域名 | — |
| `proxctl rm <域名>` | 删除域名规则 | — |
| `proxctl serve` | 前台运行代理服务 | — |
| `proxctl daemon <操作>` | 管理 systemd 服务 | `install` / `start` / `stop` / `restart` / `status` |
| `proxctl --version` | 查看版本 | — |
| `proxctl --help` | 查看帮助 | — |

全局选项 `--config <路径>` 必须放在**子命令之前**。读写系统配置或管理 systemd 服务时，请使用 `sudo`。

## 配置说明

### 配置文件位置

| 运行身份 / 环境 | 默认配置路径 |
| --- | --- |
| root | `/etc/proxctl/config.json` |
| 非 root，已有系统配置 | `/etc/proxctl/config.json`；无权限时提示使用 sudo |
| 非 root，没有系统配置 | `~/.config/proxctl/config.json` |

已有系统配置时，不会因权限不足而自动切换到个人配置。也可以显式指定路径，**所有操作必须指向同一个文件**：

```bash
sudo proxctl --config /etc/proxctl/custom.json add api.example.com --to 8080
sudo proxctl --config /etc/proxctl/custom.json daemon install
sudo proxctl --config /etc/proxctl/custom.json list
```

systemd 单元会保存安装时选定的配置路径。一个 `proxctl.service` 对应一个配置文件；对其他文件执行 `daemon install` 会替换该服务的配置并重启服务。

### 权限与规则重载

- **文件权限**：新建配置目录为 `0700`，配置文件为 `0600`；读取旧配置时也会将文件权限收紧到 `0600`。Basic Auth 密码以明文存储，只允许文件所有者或 root 读取。
- **并发写入**：配置变更使用文件锁和原子替换。并发 CLI 修改不同域名不会互相覆盖；修改同一域名时，以后取得写入锁的操作为准。不要在服务运行期间手动删除 `.lock` 文件。
- **自动重载**：运行中的服务每秒加载有效规则。文件丢失、JSON 损坏或规则无效时，保留上一次有效规则并记录错误，修复文件后继续重载。

若要停止某个站点，请使用 `proxctl rm <域名>`，不要通过破坏或删除配置文件来停站。

### 全局设置

下面的全局字段需要手动编辑配置文件，修改后重启服务：

| 字段 | 默认值 | 用途 |
| --- | --- | --- |
| `http_port` | `80` | HTTP 验证与 HTTPS 跳转端口 |
| `https_port` | `443` | HTTPS 服务端口 |
| `email` | 空 | ACME 账户联系邮箱 |
| `cert_dir` | root 为 `/etc/proxctl/certs`，普通用户为 `~/.config/proxctl/certs` | 证书及账户缓存 |

使用自定义本地端口不会改变 Let's Encrypt 对公网验证入口的要求；公网部署仍需将 80/443 正确转发到服务。证书目录需要可写，并应在升级、重启时保留。

### macOS 与本地调试

可使用个人配置，将监听端口改为 `8080`、`8443`，然后前台运行：

```bash
proxctl --config /absolute/path/config.json serve
```

## 服务管理

### 启停与日志

```bash
sudo proxctl daemon start
sudo proxctl daemon stop
sudo proxctl daemon restart
sudo proxctl daemon status

# systemd 标准输出、引擎错误和规则重载日志；当前没有逐请求访问日志
sudo journalctl -u proxctl -f
```

### 排障检查

| 检查项 | 方法 |
| --- | --- |
| 服务是否运行 | `sudo proxctl daemon status` 或 `sudo systemctl status proxctl` |
| 启动与重载错误 | `sudo journalctl -u proxctl -f` |
| HTTPS 是否可访问 | `curl -I https://api.example.com` |
| 规则是否保存 | `sudo proxctl list`；仅查看配置，不代表服务健康 |

`daemon install`、配置写入等操作失败时会返回非零退出码并显示原因。

### 升级与回滚

1. 升级前备份配置文件和证书目录。
2. 安装新二进制后，执行 `sudo proxctl daemon restart`，使进程运行新代码。
3. 检查服务状态、日志和 HTTPS 响应。
4. 如需回滚，恢复旧二进制并重启服务。

当前配置格式与 V1 保持兼容。原先运行的旧版进程需要先重启，才能启用规则自动重载。

### 当前功能边界

暂不提供 `proxctl logs`、逐请求访问日志、证书有效期展示、Gzip/Brotli 压缩、请求限流或 ZeroSSL 配置入口。

## 开发与验证

需要 **Go 1.26 或更高版本**。

```bash
go test -race -timeout 90s -coverprofile=/tmp/proxctl-coverage.out ./...
go tool cover -func=/tmp/proxctl-coverage.out
go vet ./...
bash -n scripts/install.sh
go build -o /tmp/proxctl ./cmd/proxctl
```

交叉编译 Linux 二进制：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/proxctl-linux-amd64 ./cmd/proxctl
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /tmp/proxctl-linux-arm64 ./cmd/proxctl
```

<details>
<summary><strong>测试覆盖与 CI 验收说明</strong></summary>

回归测试使用临时配置、本机 HTTP/HTTPS 服务、测试证书和模拟安装命令，覆盖规则重载/删除、配置损坏恢复、并发写入、权限迁移、转发头、认证 CORS、WebSocket 双向传输及失败退出码；不会申请公网证书或修改系统服务。[CI](.github/workflows/ci.yml) 在 Linux 和 macOS 上执行测试、race、vet 与跨平台构建。

CI 另在临时 runner 上验证候选程序；[发布工作流](.github/workflows/release.yml) 则从公开的一键安装入口下载实际 Release 包，再验证前台服务、HTTPS、静态站点、反向代理、认证跨域和规则重载。Linux 验收还会安装并实际启动、停止、重启 systemd 服务，结束后清理测试配置。验收脚本为 [`scripts/verify-install.py`](scripts/verify-install.py)，其中 systemd 模式仅允许在全新的 Linux CI 环境执行。

</details>

上线前仍需在实际 Linux 服务器验证 systemd 开机启动、真实域名签发、证书续期和回滚。TLS 测试使用本地证书，交叉编译也不等同于 Linux 实机验收。

## 相关工具

来自 **9star-academy** 的裸机轻量运维工具：

| 工具 | 定位 |
| --- | --- |
| [ShellReady](https://github.com/9star-academy/ShellReady) | Linux 终端与 Shell 环境配置 |
| [secctl](https://github.com/9star-academy/secctl) | SSH 加固与防火墙管理 |
| [servctl](https://github.com/9star-academy/servctl) | systemd 服务托管 |
| **[proxctl](https://github.com/9star-academy/proxctl)** | **反向代理、静态托管与自动 HTTPS** |
| [bakctl](https://github.com/9star-academy/bakctl) | 数据备份 |

## License

[MIT License](LICENSE) · Copyright © 2026 9star-academy
