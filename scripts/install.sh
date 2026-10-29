#!/usr/bin/env bash
set -euo pipefail

REPO="9star-academy/proxctl"
BIN_NAME="proxctl"
INSTALL_DIR="/usr/local/bin"

echo "==> 检测系统架构与平台..."
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "错误: 不支持的架构 $ARCH"; exit 1 ;;
esac

if [ "$OS" != "linux" ] && [ "$OS" != "darwin" ]; then
  echo "错误: 不支持的操作系统 $OS"
  exit 1
fi

FILE="${BIN_NAME}-${OS}-${ARCH}"
URL="https://github.com/${REPO}/releases/latest/download/${FILE}"

echo "==> 正在从 GitHub 下载 ${BIN_NAME} 最新发布版 [${OS}/${ARCH}]..."
TMP_FILE="$(mktemp)"
trap 'rm -f "$TMP_FILE"' EXIT
if curl -fsSL "$URL" -o "$TMP_FILE"; then
  chmod 0755 "$TMP_FILE"
  if [ "$(id -u)" -eq 0 ]; then
    mv "$TMP_FILE" "${INSTALL_DIR}/${BIN_NAME}"
  else
    echo "==> 需要 sudo 权限将 ${BIN_NAME} 安装至 ${INSTALL_DIR}..."
    sudo mv "$TMP_FILE" "${INSTALL_DIR}/${BIN_NAME}"
  fi
  echo "✔ ${BIN_NAME} 安装成功！运行 'proxctl --help' 开始使用。"
else
  echo "❌ 下载失败: 无法下载 $URL"
  exit 1
fi
