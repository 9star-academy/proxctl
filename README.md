# proxctl

<p align="center">
  <b>一行命令搞定反向代理、静态站点托管与自动 HTTPS 证书</b><br>
  告别繁琐的 Nginx / Caddy 配置文件，30 秒让本地端口安全暴露到公网域名。
</p>

<p align="center">
  <a href="https://github.com/9star-academy/proxctl/releases"><img src="https://img.shields.io/github/v/release/9star-academy/proxctl?style=flat-square" alt="Release"></a>
  <a href="https://github.com/9star-academy/proxctl/blob/main/LICENSE"><img src="https://img.shields.io/github/license/9star-academy/proxctl?style=flat-square" alt="License"></a>
</p>

---

## 为什么做 proxctl？

在用 [`servctl`](https://github.com/9star-academy/servctl) 把后端程序守护在 `127.0.0.1:8080` 之后，下一步往往是绑定域名并申请 SSL 证书。
传统做法需要安装 Nginx、手写 `server` 块、处理 WebSocket Upgrade 头、再用 `certbot` 申请和续期证书。

**`proxctl` 将这一切简化为一条直觉式的 CLI 命令。**

## 核心特性

- **一行命令反代**：自动完成域名映射、WebSocket 透传、`X-Forwarded-*` 请求头注入。
- **全自动 HTTPS**：内置 ACME 协议支持（Let's Encrypt / ZeroSSL），自动申请、配置并无感续期证书。
- **静态站点秒级托管**：一行命令托管前端构建产物（React / Vue / Flutter Web），默认支持 SPA 路由回退（`try_files`）与 Gzip/Brotli 压缩。
- **开箱即用的安全小开关**：支持一键为站点加上 Basic Auth 密码保护、CORS 跨域头及基础限流。

## 快速开始

### 1. 安装

```bash
curl -fsSL https://raw.githubusercontent.com/9star-academy/proxctl/main/install.sh | sudo bash
```

### 2. 常用命令示例

```bash
# 将 api.example.com 反向代理到本地 8080 端口（自动申请并开启 HTTPS）
sudo proxctl add api.example.com --to 127.0.0.1:8080

# 托管前端静态目录，并开启 SPA 模式（自动 fallback 到 index.html）
sudo proxctl static app.example.com --dir /var/www/app/dist --spa

# 为私有管理后台加上 Basic Auth 账号密码保护
sudo proxctl add admin.example.com --to 9000 --auth admin:secret123

# 查看当前所有托管的域名、目标端口与证书有效期
proxctl list

# 实时查看某个域名的访问与错误日志
proxctl logs api.example.com -f

# 移除某个域名的反代配置
sudo proxctl rm api.example.com
```

## 9star-academy 裸机轻量运维矩阵

| 工具 | 定位 |
| :--- | :--- |
| [**ShellReady**](https://github.com/9star-academy/ShellReady) | 一条命令配好新 Linux 服务器（BBR、trzsz、Starship、Atuin、zoxide） |
| [**secctl**](https://github.com/9star-academy/secctl) | 一行命令搞定裸机 SSH 加固、防火墙管理与 Docker 端口防泄漏 |
| [**servctl**](https://github.com/9star-academy/servctl) | 一行命令将任意可执行程序托管为 `systemd` 系统自启服务 |
| [**proxctl**](https://github.com/9star-academy/proxctl) | 一行命令搞定反向代理、静态站点托管与自动 HTTPS 证书 |
| [**bakctl**](https://github.com/9star-academy/bakctl) | 一行命令将数据库与目录定时加密备份至 S3 / R2 / OSS / WebDAV |

## License

MIT License
