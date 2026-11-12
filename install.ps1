# claude-env 面板一键安装（Windows）。在 PowerShell 里执行：
#   irm https://raw.githubusercontent.com/aidever/claude-env/main/install.ps1 | iex
# 安装到 %USERPROFILE%\claude-env，并在桌面创建快捷方式。
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'   # 关闭进度条，否则下载会慢很多

$repo = 'aidever/claude-env'
$dir = if ($env:CLAUDE_ENV_DIR) { $env:CLAUDE_ENV_DIR } else { Join-Path $env:USERPROFILE 'claude-env' }
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$url = "https://github.com/$repo/releases/latest/download/claude-env-panel-windows-$arch.exe"
$exe = Join-Path $dir 'claude-env-panel.exe'

New-Item -ItemType Directory -Force -Path $dir | Out-Null
# 面板正在运行时无法覆盖文件，先关掉旧的
Get-Process -Name 'claude-env-panel' -ErrorAction SilentlyContinue | Stop-Process -Force
Write-Host "正在下载面板到 $dir ..."
Invoke-WebRequest -Uri $url -OutFile "$exe.tmp" -UseBasicParsing
Move-Item -Force "$exe.tmp" $exe
Unblock-File $exe   # 去掉“来自网络”的标记，避免 SmartScreen 拦截

$shell = New-Object -ComObject WScript.Shell
$lnk = $shell.CreateShortcut((Join-Path ([Environment]::GetFolderPath('Desktop')) 'claude-env 面板.lnk'))
$lnk.TargetPath = $exe
$lnk.WorkingDirectory = $dir
$lnk.Save()
Write-Host '已在桌面创建「claude-env 面板」，以后双击它即可打开。'

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
  Write-Host '提示：还没有安装 Docker Desktop，面板里会引导你安装。' -ForegroundColor Yellow
}
Write-Host '安装完成，正在打开面板...'
Start-Process -FilePath $exe -WorkingDirectory $dir
