#!/usr/bin/env bash
set -euo pipefail

RELEASES="https://github.com/9star-academy/proxctl/releases"
BIN_NAME="proxctl"
INSTALL_DIR="/usr/local/bin"
DEST="$INSTALL_DIR/$BIN_NAME"

fail() {
  printf '❌ 错误: %s\n' "$*" >&2
  exit 1
}

version=""
if [[ $# -eq 1 && ( "$1" = "--help" || "$1" = "-h" ) ]]; then
  echo "用法: bash install.sh [--version vX.Y.Z]"
  exit 0
elif [[ $# -eq 2 && "$1" = "--version" ]]; then
  version="$2"
  [[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "版本号无效，请使用 vX.Y.Z"
elif [[ $# -ne 0 ]]; then
  fail "用法: bash install.sh [--version vX.Y.Z]"
fi

echo "==> 检测系统架构与平台..."
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) fail "不支持的架构 $ARCH" ;;
esac
case "$OS" in
  linux|darwin) ;;
  *) fail "不支持的操作系统 $OS" ;;
esac

if command -v sha256sum >/dev/null; then
  hash_command=(sha256sum)
elif command -v shasum >/dev/null; then
  hash_command=(shasum -a 256)
else
  fail "需要 sha256sum 或 shasum 来校验安装包"
fi

sha256() {
  "${hash_command[@]}" | awk '{print $1}'
}

download() {
  curl -fsSL --proto '=https' --proto-redir '=https' "$@"
}

if [[ -z "$version" ]]; then
  release_url="$(download "$RELEASES/latest" -o /dev/null -w '%{url_effective}')" ||
    fail "无法确定最新版本"
  [[ "$release_url" == "$RELEASES/tag/"* ]] || fail "无法确定最新版本"
  version="${release_url#"$RELEASES/tag/"}"
  [[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "无法确定最新版本"
fi

download_dir="$(mktemp -d "${TMPDIR:-/tmp}/proxctl-download.XXXXXX")"
stage_dir=""
installer_uid="$(id -u)"
as_root() {
  if [[ "$installer_uid" -eq 0 ]]; then
    "$@"
  else
    sudo "$@"
  fi
}
cleanup() {
  rm -rf "$download_dir"
  if [[ -n "$stage_dir" ]]; then
    as_root rm -rf "$stage_dir"
  fi
}
trap cleanup EXIT

asset="$BIN_NAME-$OS-$ARCH"
release="$RELEASES/download/$version"
echo "==> 正在从 GitHub 下载 $BIN_NAME $version [$OS/$ARCH]..."
download "$release/SHA256SUMS" -o "$download_dir/SHA256SUMS" ||
  fail "无法下载校验文件 $version/SHA256SUMS，已保留原有程序"
expected="$(awk -v asset="$asset" '
  $2 == asset { matches++; if (NF == 2 && length($1) == 64 && $1 ~ /^[[:xdigit:]]+$/) digest = tolower($1) }
  END { if (matches != 1 || digest == "") exit 1; print digest }
' "$download_dir/SHA256SUMS")" || fail "校验文件缺少唯一有效的 $asset 记录"
download "$release/$asset" -o "$download_dir/$asset" ||
  fail "无法下载 $version/${asset}，已保留原有程序"
actual="$(sha256 < "$download_dir/$asset")"
[[ "$actual" = "$expected" ]] || fail "SHA-256 校验失败，已保留原有程序"

if [[ "$installer_uid" -ne 0 ]]; then
  echo "==> 需要 sudo 权限将 $BIN_NAME 安装至 $INSTALL_DIR..."
fi
[[ ! -d "$DEST" ]] || fail "安装路径 $DEST 是目录"
as_root mkdir -p "$INSTALL_DIR" || fail "无法创建安装目录 $INSTALL_DIR"
stage_dir="$(as_root mktemp -d "$INSTALL_DIR/.proxctl.XXXXXX")" ||
  fail "无法创建临时安装目录"
staged="$stage_dir/$BIN_NAME"
# Root creates the staged file. Read the download with the caller's privileges.
as_root tee "$staged" < "$download_dir/$asset" >/dev/null ||
  fail "无法写入临时安装文件"
actual="$(as_root cat "$staged" | sha256)"
[[ "$actual" = "$expected" ]] || fail "临时安装文件的 SHA-256 校验失败，已保留原有程序"
as_root chmod 0755 "$staged" || fail "无法设置安装文件权限"
as_root mv -f "$staged" "$DEST" || fail "替换失败，已保留原有程序"
echo "✔ $BIN_NAME $version 安装成功！运行 'proxctl --help' 开始使用。"
