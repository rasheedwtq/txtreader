# txtreader

一款 Windows 原生 TXT/Markdown 阅读器，基于 Go + walk 框架开发。

## 特性

- 支持 `.txt` / `.md` 文件阅读
- Markdown 美化渲染（标题、列表、代码块等）
- 目录跳转（TOC 自动提取）
- 拼音首字母快速定位（中文按拼音，英文按字母，书名号等前导标点不参与排序）
- 行间距调节（紧凑 / 标准 / 宽松 / 超宽）
- 字体大小调节
- 无限滚动加载
- 自动回收站（删除文件移入回收站而非直接删除）
- 单文件 exe 分发，零外部依赖

## 构建

需要 Go 1.23+，Windows 平台。

```powershell
# 使用本地 Go（如 PATH 无 go）
$env:PATH = "D:\project\txt\go_pkg\go\bin;$env:PATH"

# 安装 rsrc（嵌入图标）
go install github.com/akavel/rsrc@latest

# 构建
go build -ldflags="-H windowsgui -s -w" -o txtreader.exe
```

或直接运行 `build.ps1`。

## 依赖

| 包 | 用途 |
|----|------|
| `github.com/lxn/walk` | UI 框架 |
| `github.com/lxn/win` | Windows API |
| `github.com/mozillazg/go-pinyin` | 拼音检索 |
| `github.com/saintfish/chardet` | 编码检测 |
| `golang.org/x/text` | 文本编码 |

## 运行

双击 `txtreader.exe`，或命令行：

```cmd
txtreader.exe [路径]
```

传入路径参数可打开指定文件或目录。

## 项目结构

```
txtreader/
├── main.go          # 主窗口与 UI 逻辑
├── state.go         # 状态持久化（字体/行距等）
├── md.go            # Markdown 美化渲染
├── folderpick.go    # 文件夹选择对话框
├── recycle.go       # 回收站操作
├── reader/          # 核心阅读引擎
│   ├── decode.go    # 编码检测与解码
│   ├── toc.go       # 目录提取
│   └── *.go         # 分页/缓冲/定位等
├── icons/           # 图标资源
├── app.manifest     # UAC manifest
└── build.ps1        # 构建脚本
```

## 开发

```powershell
# 构建
go build -ldflags="-H windowsgui -s -w" -o txtreader.exe

# 测试（需设置 GOPATH）
set GOROOT=D:\project\txt\go_pkg\go
set GOPATH=D:\project\txt\gopath
set GOMODCACHE=%GOPATH%\pkg\mod
go test ./...
```

## License

MIT
