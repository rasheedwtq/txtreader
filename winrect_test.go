package main

import (
	"testing"

	"github.com/lxn/win"
)

// TestWinRectVisible 窗口恢复边界检查（方案 §2 防丢）：
// 与虚拟桌面交集 ≥20% 才恢复。副屏拔掉/分辨率缩小后旧坐标不把窗口弄丢。
func TestWinRectVisible(t *testing.T) {
	vx := int(win.GetSystemMetrics(win.SM_XVIRTUALSCREEN))
	vy := int(win.GetSystemMetrics(win.SM_YVIRTUALSCREEN))
	vw := int(win.GetSystemMetrics(win.SM_CXVIRTUALSCREEN))
	vh := int(win.GetSystemMetrics(win.SM_CYVIRTUALSCREEN))
	if vw <= 0 || vh <= 0 {
		t.Skip("取不到虚拟桌面尺寸")
	}

	cases := []struct {
		name    string
		x, y    int
		w, h    int
		visible bool
	}{
		{"主屏内", vx + 100, vy + 100, 800, 600, true},
		{"跨主副屏", vx + vw - 400, vy + 100, 800, 600, true},
		{"完全屏幕外(右)", vx + vw + 2000, vy + 100, 800, 600, false},
		{"完全屏幕外(下)", vx + 100, vy + vh + 2000, 800, 600, false},
		{"负半区", vx - 600, vy - 600, 500, 400, false}, // 交集 0
	}
	for _, c := range cases {
		if got := winRectVisible(c.x, c.y, c.w, c.h); got != c.visible {
			t.Errorf("[%s] winRectVisible(%d,%d,%d,%d)=%v, want %v（虚拟屏 %dx%d@%d,%d）",
				c.name, c.x, c.y, c.w, c.h, got, c.visible, vw, vh, vx, vy)
		}
	}
}
