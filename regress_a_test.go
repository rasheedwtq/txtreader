package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"txtreader/reader"
)

// makeTestWindow 建一个最小隐藏窗口（只挂 te），供 appendPage/渲染类测试复用。
//
// 必须 LockOSThread：Go 协程会在 OS 线程间迁移，窗口建在线程 A、之后
// SendMessage 发自线程 B 时，跨线程 SendMessage 要等属主线程泵消息，
// 而测试没有消息循环 → 永久阻塞（TestLoadFolderOpenTarget 全量跑曾挂死）。
// 钉死线程后同线程 SendMessage 直达 wndproc，行为与有消息循环时一致。
func makeTestWindow(t *testing.T) *walk.MainWindow {
	t.Helper()
	runtime.LockOSThread()
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
	t.Cleanup(func() { mw.Dispose() })
	return mw
}

// TestAppendPageIncremental P1 回归：增量追加（EM_REPLACESEL）与旧全量 SetText
// 逐字符等价——四档行距 × 页内含段间空行/段内换行的组合全覆盖。
// clean() 保证页面首尾无空白（TrimSpace），页间 "\n\n" 不与页内换行粘连，
// applySpacing/disp 因此可分块计算；本测试锁死该前提，前提被破坏会立刻红。
func TestAppendPageIncremental(t *testing.T) {
	makeTestWindow(t)

	pages := []string{
		"第一页正文。\n段内的第二行。\n\n段间空行后的第三段。",
		"第二页，开头无空白。结尾也无空白。",
		"第三页有连续两空行\n\n\n夹一个被 clean 压掉的三连？不对，这里直接给两空行：\n\n收尾段。",
	}

	for sp := 0; sp <= 3; sp++ {
		curSpacing = sp
		curIsMD = false

		// 路径 A（旧）：全量 SetText
		want := displayText(strings.Join(pages, "\n\n"))

		// 路径 B（新）：首屏 SetText + 逐页增量
		accumBuf = []string{pages[0]}
		render(pages[0])
		for _, p := range pages[1:] {
			appendPage(p)
		}
		got := te.Text()

		if got != want {
			t.Fatalf("行距 %d 不等价：\n增量=%q\n全量=%q", sp, got, want)
		}
		t.Logf("行距 %d 等价（%d 页，%d 字符）", sp, len(pages), len(got))
	}

	// 真实书页量级：3×2 万字符页，累计 6 万——远超 EDIT 默认 30000 上限。
	// 不提 EM_SETLIMITTEXT 时 EM_REPLACESEL 会被截断/拒绝（曾真实咬过
	// TestAccumScroll：首刀截断到 30000、后续全拒），此段专锁该 bug。
	bigPages := make([]string, 3)
	for i := range bigPages {
		bigPages[i] = fmt.Sprintf("第%d页", i) + strings.Repeat("字", 20000-2)
	}
	curSpacing = 1
	curIsMD = false
	wantBig := displayText(strings.Join(bigPages, "\n\n"))
	accumBuf = []string{bigPages[0]}
	render(bigPages[0])
	for _, p := range bigPages[1:] {
		appendPage(p)
	}
	gotBig := te.Text()
	if gotBig != wantBig {
		t.Fatalf("大页（%d 字符）不等价：增量 len=%d、全量 len=%d（上限截断回归？）",
			len([]rune(wantBig)), len(gotBig), len(wantBig))
	}
	t.Logf("大页等价（累计 %d 字符，越过 30000 默认上限）", len([]rune(gotBig)))
}

// TestIndexOfPath 路径定位：命中返回下标，未命中/空串回落 0（第一本）。
func TestIndexOfPath(t *testing.T) {
	curEntries = []entry{
		{name: "a.txt", path: `X:\b\a.txt`},
		{name: "c.txt", path: `X:\b\c.txt`},
	}
	defer func() { curEntries = nil }()

	if got := indexOfPath(`X:\b\c.txt`); got != 1 {
		t.Fatalf("命中应返回 1，got %d", got)
	}
	if got := indexOfPath(`X:\b\zz.txt`); got != 0 {
		t.Fatalf("未命中应回落 0，got %d", got)
	}
	if got := indexOfPath(""); got != 0 {
		t.Fatalf("空串应回落 0，got %d", got)
	}
}

// isVisibleStyle 读 WS_VISIBLE 样式位。不能用 walk 的 Visible()——它走
// IsWindowVisible，父窗隐藏（测试窗口 Visible:false）时永远 false，
// 无法反映 SetVisible 的真实意图。
func isVisibleStyle(w *walk.ListBox) bool {
	return win.GetWindowLong(w.Handle(), win.GWL_STYLE)&win.WS_VISIBLE != 0
}

// TestApplyTOCStaleGen P2' 回归：过期代次的目录结果必须丢弃（换书/换编码场景），
// 不灌进新书的目录栏；当代次的结果正常落地。
func TestApplyTOCStaleGen(t *testing.T) {
	runtime.LockOSThread() // 线程钉死：见 makeTestWindow 注释
	var mw *walk.MainWindow
	if err := (MainWindow{
		AssignTo: &mw,
		Visible:  false,
		Size:     Size{Width: 500, Height: 400},
		Layout:   VBox{},
		Children: []Widget{
			ListBox{AssignTo: &tocLB, Visible: false, Model: []string{}},
		},
	}).Create(); err != nil {
		t.Skipf("无法创建窗口：%v", err)
	}
	defer mw.Dispose()
	tocLB.SetVisible(false)

	toc := []reader.TocEntry{{Title: "第一章", Page: 0, Offset: 0}}

	// 过期代次（构建开始时 5，落地时已 6）→ 丢弃
	tocGen = 6
	applyTOCIf(5, toc, nil)
	if curTOC != nil {
		t.Fatal("过期结果不应落地")
	}

	// 当代次正常落地
	applyTOCIf(6, toc, nil)
	if len(curTOC) != 1 || curTOC[0].Title != "第一章" {
		t.Fatalf("当代次应落地，got %+v", curTOC)
	}
	if !isVisibleStyle(tocLB) {
		t.Fatal("有目录应显示目录栏")
	}

	// 空目录 → 隐藏
	applyTOC(nil, nil)
	if isVisibleStyle(tocLB) || curTOC != nil {
		t.Fatal("无目录应隐藏栏并清 curTOC")
	}
}

// TestLoadFolderOpenTarget 启动双开修复回归：loadFolderOpen 直接打开目标书，
// 不先开第一本。用真实临时目录 + 真实 Book 验证最终打开的就是目标文件。
func TestLoadFolderOpenTarget(t *testing.T) {
	runtime.LockOSThread() // 线程钉死：见 makeTestWindow 注释
	var mw *walk.MainWindow
	if err := (MainWindow{
		AssignTo: &mw,
		Visible:  false,
		Size:     Size{Width: 500, Height: 400},
		Layout:   VBox{},
		Children: []Widget{
			Label{AssignTo: &folderLbl, Text: ""},
			ListBox{AssignTo: &lb, Model: []string{}},
			TextEdit{AssignTo: &te, VScroll: true, ReadOnly: true},
		},
	}).Create(); err != nil {
		t.Skipf("无法创建窗口：%v", err)
	}
	defer mw.Dispose()
	stateDirOverride = t.TempDir() // 状态文件不落真实 exe 目录
	defer func() { stateDirOverride = "" }()

	dir := t.TempDir()
	names := []string{"a-第一本.txt", "b-第二本.txt", "c-目标书.txt"}
	for i, n := range names {
		content := fmt.Sprintf("这是%s的正文。\n第二行。\n", n)
		if err := os.WriteFile(filepath.Join(dir, n), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		_ = i
	}
	target := filepath.Join(dir, "c-目标书.txt")

	loadFolderOpen(dir, target)
	defer func() {
		if curBook != nil {
			curBook.Close()
			curBook = nil
		}
	}()

	if curBook == nil {
		t.Fatal("应打开目标书")
	}
	if curBook.Path() != target {
		t.Fatalf("打开的应是目标书：got %s want %s", curBook.Path(), target)
	}
	if got := lb.CurrentIndex(); curEntries[got].path != target {
		t.Fatalf("列表选中应是目标书：idx=%d got %s", got, curEntries[got].path)
	}

	// 未命中路径 → 回落第一本
	loadFolderOpen(dir, filepath.Join(dir, "zz-不存在.txt"))
	if curBook == nil || curBook.Path() != curEntries[0].path {
		t.Fatalf("未命中应回落第一本，got %v", curBook)
	}
}
