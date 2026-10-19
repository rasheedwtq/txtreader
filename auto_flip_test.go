package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"txtreader/reader"
)

// TestAccumScroll 验证累积式无限滚动：
// 到底追加下一页、连续追加到末页、向上滚前文仍在（回看自由）、eof 后不再追加。
func TestAccumScroll(t *testing.T) {
	var mw *walk.MainWindow
	if err := (MainWindow{
		AssignTo: &mw,
		Visible:  false,
		Size:     Size{Width: 500, Height: 400},
		Layout:   VBox{},
		Children: []Widget{
			TextEdit{AssignTo: &te, VScroll: true, ReadOnly: true, MinSize: Size{Width: 460, Height: 360}},
		},
	}).Create(); err != nil {
		t.Skipf("无法创建窗口：%v", err)
	}
	defer mw.Dispose()

	dir := t.TempDir()
	p := filepath.Join(dir, "m.txt")
	var sb strings.Builder
	for i := 0; i < 4; i++ {
		sb.WriteString(fmt.Sprintf("%03d", i))
		sb.WriteString(strings.Repeat("字", 20000-3))
	}
	if err := os.WriteFile(p, []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}
	b, err := reader.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	curBook = b
	defer func() { curBook = nil }()

	// 首屏 = 页 0（showCurrent 同款）
	if err := b.Goto(0); err != nil {
		t.Fatal(err)
	}
	accumBase = 0
	accumBuf = []string{b.Current()}
	render(b.Current())
	if !strings.Contains(te.Text(), "000") {
		t.Fatal("首屏应含页 0")
	}

	// 滚到底 → 追加页 1
	te.SendMessage(win.WM_VSCROLL, uintptr(win.SB_BOTTOM), 0)
	appendTick()
	if len(accumBuf) != 2 || !strings.Contains(te.Text(), "001") {
		t.Fatalf("到底应追加页 1：accumBuf=%d", len(accumBuf))
	}

	// 连续滚到底 → 一直追加到末页
	for i := 0; i < 10; i++ {
		te.SendMessage(win.WM_VSCROLL, uintptr(win.SB_BOTTOM), 0)
		appendTick()
	}
	if !strings.Contains(te.Text(), "003") {
		t.Fatal("应累积到末页（页 3）")
	}

	// 向上滚回看：前文仍在，一个字都不少
	te.SendMessage(win.WM_VSCROLL, uintptr(win.SB_TOP), 0)
	if !strings.Contains(te.Text(), "000") {
		t.Fatal("回滚到顶后前文（页 0）应仍在缓冲里")
	}

	// eof 后继续到底不再追加
	n := len(accumBuf)
	te.SendMessage(win.WM_VSCROLL, uintptr(win.SB_BOTTOM), 0)
	appendTick()
	if len(accumBuf) != n {
		t.Fatal("eof 后不应继续追加")
	}
}
