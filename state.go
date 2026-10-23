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
	// 清洗偏好三态指针：nil=旧配置文件无此字段→保持默认（A1/B1/E1 全开），
	// 非 nil=用户明确改过。Plain bool 会把"没配过"误当成"用户关了"。
	CleanIndent   *bool `json:"clean_indent,omitempty"`
	CleanAd       *bool `json:"clean_ad,omitempty"`
	CleanBlankAll *bool `json:"clean_blank_all,omitempty"`
	// 窗口状态：RcNormalPosition 口径（最大化时存的也是还原尺寸）。
	// 零值（旧文件缺字段）= 未记录，不恢复。恢复前须过显示器边界检查。
	WinX   int  `json:"win_x,omitempty"`
	WinY   int  `json:"win_y,omitempty"`
	WinW   int  `json:"win_w,omitempty"`
	WinH   int  `json:"win_h,omitempty"`
	WinMax bool `json:"win_max,omitempty"`
	// 行级书签（视口锚点）：关窗时屏幕第一可见行的行首文字 + 其所在页码。
	// 页粒度（20000 字符/页）对短篇无效——15000 字的小说整本只有 1 页，
	// 页码永远是 0，页内位置必须靠锚点记。恢复时在显示文本里搜锚点行滚回去，
	// 找不到（正文/设置变了）退化为页首。
	ViewPage   int    `json:"view_page,omitempty"`
	ViewAnchor string `json:"view_anchor,omitempty"`
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
