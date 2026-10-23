# Build txtreader as a single Windows exe (zero external deps).
# Requires Go 1.23+ in PATH.
$ErrorActionPreference = 'Stop'

# Locate go (local portable GOROOT first, then PATH).
if (-not (Get-Command go.exe -ErrorAction SilentlyContinue)) {
    if (Test-Path "D:\project\txt\go_pkg\go\bin\go.exe") {
        $env:PATH = "D:\project\txt\go_pkg\go\bin;$env:PATH"
    } else {
        throw "go.exe not found"
    }
}

# Build env fixed (same as dev/test): local Go + module cache + CN proxy.
# 2026-10-23 lesson: without GOPROXY, `go build` silently fails to fetch new
# deps (proxy.golang.org blocked) and the script still prints Done — the exe
# stays STALE. Always check $LASTEXITCODE after native commands.
$env:GOPROXY = 'https://goproxy.cn,direct'
$env:GOROOT = 'D:\project\txt\go_pkg\go'
$env:GOPATH = 'D:\project\txt\gopath'
$env:GOMODCACHE = 'D:\project\txt\gopath\pkg\mod'

# Install rsrc (embeds manifest + toolbar icons into the exe).
if (-not (Get-Command rsrc.exe -ErrorAction SilentlyContinue)) {
    Write-Host "Installing rsrc ..."
    go install github.com/akavel/rsrc@latest
    $gopath = go env GOPATH
    $env:PATH = "$gopath\bin;$env:PATH"
}

Write-Host "Embedding manifest + toolbar icons (group icon IDs: folder=2, font=4, first=6, prev=8, next=10, last=12, refresh=14, encode=16) ..."
rsrc -manifest app.manifest -ico icons\folder.ico,icons\font.ico,icons\first.ico,icons\prev.ico,icons\next.ico,icons\last.ico,icons\refresh.ico,icons\encode.ico -o rsrc.syso

Write-Host "Building single-file exe ..."
# 显式固定 GOARCH=amd64：默认取平台值，机器上若装了 32 位 Go 会静默退化成 32 位 exe
# （32 位 EDIT 控件超大文本有失败风险，见 bug 审查报告 P1/P5）。
$env:GOARCH = 'amd64'
$env:GOOS = 'windows'
go build -ldflags="-H windowsgui -s -w" -o txtreader.exe
if ($LASTEXITCODE -ne 0) { throw "go build FAILED (exit $LASTEXITCODE), exe NOT updated" }

Write-Host "Done: txtreader.exe"
