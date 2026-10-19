package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
)

// TestInfiniteScrollBottom 验证 teAtBottom：短页(一屏放得下)不判到底、
// 长页初始(顶部)不判到底、长页滚到底后判到底。防止无限滚动误翻/漏翻。
func TestInfiniteScrollBottom(t *testing.T) {
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

	// 短文本：一屏放得下，不应判到底
	te.SetText("line1\nline2\nline3")
	if teAtBottom() {
		t.Error("短文本误判到底（会导致自动狂奔翻页）")
	}

	// 长文本：超一屏
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		sb.WriteString(fmt.Sprintf("第%d行内容测试。\n", i))
	}
	te.SetText(sb.String())
	if teAtBottom() {
		t.Error("长文本初始(顶部)误判到底")
	}

	// 滚到底
	te.SendMessage(win.WM_VSCROLL, uintptr(win.SB_BOTTOM), 0)
	if !teAtBottom() {
		t.Error("长文本滚到底未被检测到（无限滚动会失效）")
	}
	t.Log("teAtBottom 逻辑正确")
}
