package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// appState 是会话持久化的最小状态：上次目录 + 正在读的书 + 页码。
// 这是“深模块”的 seam：main 只认 Load/Save 两个接口，JSON 读写细节全藏在此文件。
type appState struct {
	LastDir     string `json:"last_dir"`
	CurrentPath string `json:"current_path"`
	Page        int    `json:"page"`
	FontFamily  string `json:"font_family"`
	FontSize    int    `json:"font_size"`
	LineSpacing int    `json:"line_spacing"`
}

const stateFileName = "txtreader.state.json"

// stateDirOverride 仅供测试重定向状态文件路径；生产环境为空，走 exe 同目录。
var stateDirOverride string

// statePath 返回状态文件路径：exe 同目录下的 txtreader.state.json（便携、零依赖）。
func statePath() (string, error) {
	dir := stateDirOverride
	if dir == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		dir = filepath.Dir(exe)
	}
	return filepath.Join(dir, stateFileName), nil
}

// loadState 读取状态；文件不存在或解析失败返回 (零值, false)。
func loadState() (appState, bool) {
	var st appState
	p, err := statePath()
	if err != nil {
		return st, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return st, false
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, false
	}
	return st, true
}

// saveState 写入状态；任何错误静默忽略（状态丢失不应影响阅读）。
func saveState(st appState) {
	p, err := statePath()
	if err != nil {
		return
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(p, b, 0644)
}
