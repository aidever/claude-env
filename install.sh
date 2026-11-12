#!/bin/sh
# claude-env 面板一键安装（macOS / Linux）：
#   curl -fsSL https://raw.githubusercontent.com/aidever/claude-env/main/install.sh | sh
# 安装到 ~/claude-env，并在桌面放一个双击即可打开的快捷方式。
set -eu

REPO="aidever/claude-env"
DIR="${CLAUDE_ENV_DIR:-$HOME/claude-env}"

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) echo "不支持的系统：$(uname -s)。Windows 请用 PowerShell 安装命令。" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "不支持的 CPU：$(uname -m)" >&2; exit 1 ;;
esac

url="https://github.com/$REPO/releases/latest/download/claude-env-panel-$os-$arch"
mkdir -p "$DIR"
echo "正在下载面板到 $DIR …"
curl -fL --progress-bar "$url" -o "$DIR/claude-env-panel.tmp"
chmod +x "$DIR/claude-env-panel.tmp"
mv "$DIR/claude-env-panel.tmp" "$DIR/claude-env-panel"
# 命令行下载的文件一般不带隔离标记；保险起见清掉，避免 macOS 提示“无法验证开发者”。
[ "$os" = darwin ] && xattr -d com.apple.quarantine "$DIR/claude-env-panel" 2>/dev/null || true

# 桌面快捷方式
desktop="$HOME/Desktop"
if [ "$os" = darwin ] && [ -d "$desktop" ]; then
  launcher="$desktop/claude-env 面板.command"
  printf '#!/bin/sh\ncd "%s" && exec ./claude-env-panel\n' "$DIR" > "$launcher"
  chmod +x "$launcher"
  echo "已在桌面创建「claude-env 面板」，以后双击它即可打开。"
elif [ "$os" = linux ] && [ -d "$desktop" ]; then
  launcher="$desktop/claude-env-panel.desktop"
  cat > "$launcher" <<EOF
[Desktop Entry]
Type=Application
Name=claude-env 面板
Exec=sh -c 'cd "$DIR" && ./claude-env-panel'
Terminal=true
EOF
  chmod +x "$launcher"
  echo "已在桌面创建「claude-env 面板」快捷方式。"
fi

echo "安装完成，正在打开面板…（关闭这个窗口即可退出面板，不会停止容器）"
cd "$DIR"
exec ./claude-env-panel
