# 在 Windows 上交叉编译 Linux/amd64 静态二进制。
# 用法： powershell -ExecutionPolicy Bypass -File .\build.ps1
#        可选参数： -Version v1.0.0  -SkipTests  -AlsoWindows

param(
    [string]$Version = "",
    [switch]$SkipTests,
    [switch]$AlsoWindows
)

$ErrorActionPreference = "Stop"

$AppName = "crontabui"
$GoBin = "C:\Program Files\Go\bin"
if (Test-Path $GoBin) {
    $env:Path = "$GoBin;$env:Path"
}

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw "找不到 go 命令，请先安装 Go 或把 Go 的 bin 目录加入 PATH。"
}

if ([string]::IsNullOrWhiteSpace($Version)) {
    try {
        $Version = (git describe --tags --always --dirty 2>$null)
    }
    catch {
        $Version = $null
    }
    if ([string]::IsNullOrWhiteSpace($Version)) { $Version = "dev" }
}

$env:CGO_ENABLED = "0"

Push-Location $PSScriptRoot
try {
    if (-not $SkipTests) {
        Write-Host "==> 运行测试" -ForegroundColor Cyan
        go test -count=1 ./...
        if ($LASTEXITCODE -ne 0) { throw "测试未通过，已中止构建。" }
    }

    $dist = Join-Path $PSScriptRoot "dist"
    New-Item -ItemType Directory -Force -Path $dist | Out-Null

    $targets = @(
        @{ OS = "linux"; Arch = "amd64"; Out = "$AppName-linux-amd64" }
    )
    if ($AlsoWindows) {
        $targets += @{ OS = "windows"; Arch = "amd64"; Out = "$AppName-windows-amd64.exe" }
    }

    foreach ($t in $targets) {
        $outPath = Join-Path $dist $t.Out
        Write-Host "==> 构建 $($t.OS)/$($t.Arch) -> dist\$($t.Out)  (version=$Version)" -ForegroundColor Cyan
        $env:GOOS = $t.OS
        $env:GOARCH = $t.Arch
        go build -trimpath -ldflags "-s -w -X main.version=$Version" -o $outPath "./cmd/$AppName"
        if ($LASTEXITCODE -ne 0) { throw "构建 $($t.Out) 失败。" }

        $size = "{0:N2} MB" -f ((Get-Item $outPath).Length / 1MB)
        Write-Host "    完成：$outPath ($size)" -ForegroundColor Green
    }

    # 把 Debian 安装脚本一起放进 dist/，方便直接 scp 到服务器
    $installer = Join-Path $PSScriptRoot "deploy\install-corntab.sh"
    if (Test-Path $installer) {
        Copy-Item $installer (Join-Path $dist "install-corntab.sh") -Force
        Write-Host "    完成：$(Join-Path $dist 'install-corntab.sh')" -ForegroundColor Green
    }
}
finally {
    Pop-Location
}
