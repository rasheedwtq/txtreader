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

# Install rsrc (embeds manifest + toolbar icons into the exe).
if (-not (Get-Command rsrc.exe -ErrorAction SilentlyContinue)) {
    Write-Host "Installing rsrc ..."
    go install github.com/akavel/rsrc@latest
    $gopath = go env GOPATH
    $env:PATH = "$gopath\bin;$env:PATH"
}

Write-Host "Embedding manifest + toolbar icons (group icon IDs: folder=2, font=4, first=6, prev=8, next=10, last=12, refresh=14) ..."
rsrc -manifest app.manifest -ico icons\folder.ico,icons\font.ico,icons\first.ico,icons\prev.ico,icons\next.ico,icons\last.ico,icons\refresh.ico -o rsrc.syso

Write-Host "Building single-file exe ..."
go build -ldflags="-H windowsgui -s -w" -o txtreader.exe

Write-Host "Done: txtreader.exe"
