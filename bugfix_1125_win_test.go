package main

// H6（2026-11-25 审查）：窗口几何存的是物理像素，恢复必须用 *Pixels 一套。
// 本测直接用像素口径摆矩形 → saveWindowRect → restoreWindowRect → 比较像素，
// 非 100% 缩放的机器上旧实现（DIP setter）会放大，测试立刻红。

import (
	"testing"
	"time"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

// pumpMessages 把 UI 线程的排队消息泵空。walk 的布局协程把结果 synchronize 回
// UI 线程；不泵，Dispose 会与仍在跑的 layoutTree 撞上 walk 的 send-on-closed
// 竞态（layout.go:276）。测试里手动泵到静默即可。
func pumpMessages(d time.Duration) {
	deadline := time.Now().Add(d)
	var msg win.MSG
	for time.Now().Before(deadline) {
		busy := false
		for win.PeekMessage(&msg, 0, 0, 0, win.PM_REMOVE) {
			busy = true
			win.TranslateMessage(&msg)
			win.DispatchMessage(&msg)
		}
		if !busy {
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func TestH6WindowRectPixelRoundtrip(t *testing.T) {
	w := makeTestWindow(t)
	oldMW := mw
	mw = w
	t.Cleanup(func() { mw = oldMW })

	if err := w.SetBoundsPixels(walk.Rectangle{X: 100, Y: 100, Width: 640, Height: 480}); err != nil {
		t.Fatalf("SetBoundsPixels: %v", err)
	}
	var st appState
	saveWindowRect(&st)
	if st.WinX != 100 || st.WinY != 100 || st.WinW != 640 || st.WinH != 480 {
		t.Fatalf("保存应为物理像素：%+v", st)
	}

	// 打乱现场，再按 state 恢复
	if err := w.SetBoundsPixels(walk.Rectangle{X: 20, Y: 20, Width: 320, Height: 240}); err != nil {
		t.Fatalf("SetBoundsPixels: %v", err)
	}
	restoreWindowRect(st)

	got := w.BoundsPixels()
	if got.X != 100 || got.Y != 100 || got.Width != 640 || got.Height != 480 {
		t.Fatalf("恢复后像素矩形不符：got=%+v want={100 100 640 480}（DPI=%d）",
			got, w.DPI())
	}
	pumpMessages(300 * time.Millisecond) // 让布局落定，避免 Dispose 与 layoutTree 竞态
	t.Logf("窗口缩放 DPI=%d（96=100%%；非 96 时本测能直接抓到旧的 DIP setter 放大问题）", w.DPI())
}
