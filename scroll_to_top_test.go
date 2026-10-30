package main

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

// TestScrollCaretToTop 验证 scrollCaretToTop 把目标行滚到阅读区第一行。
func TestScrollCaretToTop(t *testing.T) {
	runtime.LockOSThread() // 线程钉死：见 makeTestWindow 注释
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

	var sb strings.Builder
	for i := 0; i < 300; i++ {
		sb.WriteString(fmt.Sprintf("第%d行内容测试。\n", i))
	}
	te.SetText(sb.String())

	target := "第180行内容测试。"
	idx := strings.Index(sb.String(), target)
	if idx < 0 {
		t.Fatal("目标行不存在")
	}
	d := sb.String()
	off := len([]rune(d[:idx]))
	scrollCaretToTop(d, off)

	// F3：校验也用 UTF-16 口径（EM_LINEFROMCHAR 同源），否则含 emoji 时
	// 测试自己会把口径混用传下去
	line := int(te.SendMessage(win.EM_LINEFROMCHAR, uintptr(runeToUTF16Off(d, off)), 0))
	first := int(te.SendMessage(win.EM_GETFIRSTVISIBLELINE, 0, 0))
	if first != line {
		t.Fatalf("目标行应在第一行：first=%d line=%d", first, line)
	}
	t.Logf("目标行 %d 已滚到第一行", line)
}
