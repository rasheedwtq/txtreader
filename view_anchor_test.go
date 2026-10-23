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
)

// TestViewAnchorRestore 行级书签（视口锚点）端到端回归：
// 15000 字短篇整本只有 1 页，页码永远是 0——"下次启动直接接着看"必须靠
// 关窗时"屏幕第一行"的文字锚点。本测锁全链路：滚动到页中某行 → persist →
// 模拟重启 → CLI 双击重开 → 第一可见行应回到锚点行。
func TestViewAnchorRestore(t *testing.T) {
	runtime.LockOSThread()
	var mw *walk.MainWindow
	if err := (MainWindow{
		AssignTo: &mw,
		Visible:  false,
		Size:     Size{Width: 500, Height: 400},
		Layout:   VBox{},
		Children: []Widget{
			Label{AssignTo: &folderLbl, Text: ""},
			ListBox{AssignTo: &lb, Model: []string{}},
			TextEdit{AssignTo: &te, VScroll: true, ReadOnly: true, MinSize: Size{Width: 460, Height: 360}},
		},
	}).Create(); err != nil {
		t.Skipf("无法创建窗口：%v", err)
	}
	defer mw.Dispose()
	stateDirOverride = t.TempDir()
	defer func() { stateDirOverride = "" }()
	t.Cleanup(func() {
		if curBook != nil {
			curBook.Close()
			curBook = nil
		}
	})

	const anchorLine = "乙页锚点行：灯下看剑，梦里吹角。"
	dir := t.TempDir()
	p := filepath.Join(dir, "book.txt")
	// 锚点故意放在第 1 页【中部】而非首行——首行锚点会掩盖"lastState 被
	// showCurrent→persist 覆盖"的时序 bug（覆盖前后恰好是同一行，假绿）。
	content := strings.Repeat("甲", 20000) + // 第 0 页
		strings.Repeat("乙", 10000) +
		anchorLine + "\n" + // 第 1 页中部（锚点）
		strings.Repeat("乙", 9900) +
		strings.Repeat("丙", 5000)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// 打开 + 追加第 1 页进缓冲
	loadFolderOpen(dir, p)
	if curBook == nil {
		t.Fatal("打开失败")
	}
	if s, more, err := curBook.LoadNextPage(); err != nil || !more {
		t.Fatalf("应还有第 1 页：more=%v err=%v", more, err)
	} else {
		appendPage(s)
	}

	// 滚到第 1 页锚点行（模拟用户已看到那里）
	d := displayText(strings.Join(accumBuf, pageJoin()))
	idx := strings.Index(d, anchorLine)
	if idx < 0 {
		t.Fatal("锚点行不在显示文本里")
	}
	scrollCaretToTop(len([]rune(d[:idx])))

	// 落盘：应记下 ViewPage=1 + 锚点
	persist()
	saved, _ := loadState()
	if saved.ViewPage != 1 {
		t.Fatalf("ViewPage 应为 1，got %d", saved.ViewPage)
	}
	if saved.ViewAnchor != anchorLine {
		t.Fatalf("锚点应完整保存，got %q", saved.ViewAnchor)
	}

	// 模拟重启 + CLI 双击重开
	curBook.Close()
	curBook = nil
	lastState, _ = loadState()
	oldArgs := os.Args
	os.Args = []string{"txtreader.exe", p}
	defer func() { os.Args = oldArgs }()
	if !openCLIPath() {
		t.Fatal("CLI 应接管启动")
	}
	if curBook == nil || curBook.Page() != 1 {
		t.Fatalf("应恢复到第 1 页，got page=%d", curBook.Page())
	}

	// 断言第一可见行就是锚点行
	first := int(te.SendMessage(win.EM_GETFIRSTVISIBLELINE, 0, 0))
	ci := int(te.SendMessage(win.EM_LINEINDEX, uintptr(first), 0))
	rs := []rune(te.Text())
	if ci < 0 || ci >= len(rs) {
		ci = 0
	}
	line := rs[ci:]
	for i, r := range line {
		if r == '\n' {
			line = line[:i]
			break
		}
	}
	got := strings.TrimSpace(string(line))
	if !strings.Contains(got, "乙页锚点行") {
		t.Fatalf("重开后第一可见行应回到锚点行，got %q（first=%d ci=%d）", got, first, ci)
	}
	t.Logf("重开恢复到锚点行：%q", got)
}

// TestCaptureViewSinglePage 短篇（<1 页）也能记锚点：ViewPage=0、锚点为行首文字。
func TestCaptureViewSinglePage(t *testing.T) {
	runtime.LockOSThread()
	var mw *walk.MainWindow
	if err := (MainWindow{
		AssignTo: &mw,
		Visible:  false,
		Size:     Size{Width: 500, Height: 400},
		Layout:   VBox{},
		Children: []Widget{
			TextEdit{AssignTo: &te, VScroll: true, ReadOnly: true},
		},
	}).Create(); err != nil {
		t.Skipf("无法创建窗口：%v", err)
	}
	defer mw.Dispose()
	stateDirOverride = t.TempDir()
	defer func() { stateDirOverride = "" }()
	t.Cleanup(func() {
		if curBook != nil {
			curBook.Close()
			curBook = nil
		}
	})

	dir := t.TempDir()
	p := filepath.Join(dir, "short.txt")
	var sb strings.Builder
	sb.WriteString("短篇开头。\n")
	for i := 0; i < 200; i++ {
		sb.WriteString(fmt.Sprintf("第%d段：短篇正文内容，整本不足一页。\n", i))
	}
	if err := os.WriteFile(p, []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}
	loadFolderOpen(dir, p)
	if curBook == nil {
		t.Fatal("打开失败")
	}
	// 滚到中部某行
	target := "第100段：短篇正文内容，整本不足一页。"
	d := te.Text()
	idx := strings.Index(d, target)
	if idx < 0 {
		t.Fatal("目标行不存在")
	}
	scrollCaretToTop(len([]rune(d[:idx])))
	vp, anchor := captureView()
	if vp != 0 {
		t.Fatalf("短篇 ViewPage 应为 0，got %d", vp)
	}
	if !strings.Contains(anchor, "第100段") {
		t.Fatalf("锚点应为目标行文字，got %q", anchor)
	}

	// 完整恢复链路（大叔现场场景：17263 字符的 md = 1 页，锚点在文档中部）：
	// persist → 模拟重启 → CLI 双击重开 → 第一可见行应回到第 100 段。
	persist()
	curBook.Close()
	curBook = nil
	lastState, _ = loadState()
	oldArgs := os.Args
	os.Args = []string{"txtreader.exe", p}
	defer func() { os.Args = oldArgs }()
	if !openCLIPath() {
		t.Fatal("CLI 应接管启动")
	}
	first := int(te.SendMessage(win.EM_GETFIRSTVISIBLELINE, 0, 0))
	ci := int(te.SendMessage(win.EM_LINEINDEX, uintptr(first), 0))
	rs := []rune(te.Text())
	if ci < 0 || ci >= len(rs) {
		ci = 0
	}
	line := rs[ci:]
	for i, r := range line {
		if r == '\n' {
			line = line[:i]
			break
		}
	}
	if got := strings.TrimSpace(string(line)); !strings.Contains(got, "第100段") {
		t.Fatalf("短篇重开后应回到第 100 段，got %q（时序覆盖 bug 回归？）", got)
	}
}

// TestCaptureViewBlankFirstLine 没有"标签"的文件：首可见行是空行/纯空白时，
// 锚点向下取最近的非空行——空行分隔的纯文本也要能记位置，不能存出空锚点。
func TestCaptureViewBlankFirstLine(t *testing.T) {
	runtime.LockOSThread()
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
	stateDirOverride = t.TempDir()
	defer func() { stateDirOverride = "" }()
	t.Cleanup(func() {
		if curBook != nil {
			curBook.Close()
			curBook = nil
		}
	})

	dir := t.TempDir()
	p := filepath.Join(dir, "blank.txt")
	content := "第一段。\n\n\n停在空行往下是这段。\n\n\n结尾段。"
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	loadFolderOpen(dir, p)
	if curBook == nil {
		t.Fatal("打开失败")
	}
	// 滚到第一个空行（第一段之后；\r\n 是 2 字符，off 要越过整对换行）
	d := te.Text()
	idx := strings.Index(d, "第一段。")
	if idx < 0 {
		t.Fatal("目标不存在")
	}
	scrollCaretToTop(len([]rune(d[:idx])) + len([]rune("第一段。")) + 2)
	vp, anchor := captureView()
	if vp != 0 {
		t.Fatalf("ViewPage 应为 0，got %d", vp)
	}
	if !strings.Contains(anchor, "停在空行往下是这段") {
		t.Fatalf("空行首可见应向下取最近非空行，got %q", anchor)
	}
}

// TestCaptureViewMDTableRow md 表格行锚点（2026-10-22 现场实锤）：
// mdBeautify 的 alignTables 给表格行加对齐空格，美化后的行【不是】原始页文本
// 的子串——旧的 Contains 匹配直接把锚点丢成空，进度丢失。显示偏移映射后必须存住。
func TestCaptureViewMDTableRow(t *testing.T) {
	runtime.LockOSThread()
	var mw *walk.MainWindow
	if err := (MainWindow{
		AssignTo: &mw,
		Visible:  false,
		Size:     Size{Width: 900, Height: 700},
		Layout:   VBox{},
		Children: []Widget{
			Label{AssignTo: &folderLbl, Text: ""},
			ListBox{AssignTo: &lb, Model: []string{}},
			TextEdit{AssignTo: &te, VScroll: true, ReadOnly: true, MinSize: Size{Width: 860, Height: 660}},
		},
	}).Create(); err != nil {
		t.Skipf("无法创建窗口：%v", err)
	}
	defer mw.Dispose()
	stateDirOverride = t.TempDir()
	defer func() { stateDirOverride = "" }()
	t.Cleanup(func() {
		if curBook != nil {
			curBook.Close()
			curBook = nil
		}
	})

	dir := t.TempDir()
	p := filepath.Join(dir, "table.md")
	content := "# 研究报告\n\n" +
		"| 键 | 值 |\n" +
		"| --- | --- |\n" +
		"| SECI 模型 | Socialization-Externalization-Combination 是很长的内容让这一列明显更宽产生对齐差异 |\n" +
		"| 短键 | 短值 |\n\n" +
		"正文段落收尾。\n"
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	loadFolderOpen(dir, p)
	if curBook == nil {
		t.Fatal("打开失败")
	}
	// 滚到 SECI 表格行（美化后的显示文本里找）
	d := te.Text()
	idx := strings.Index(d, "SECI 模型")
	if idx < 0 {
		t.Fatalf("SECI 行不在显示文本里（md 美化路径异常），d 前 200 字=%q", []rune(d[:200]))
	}
	scrollCaretToTop(len([]rune(d[:idx])))
	vp, anchor := captureView()
	if anchor == "" {
		t.Fatal("表格行锚点被丢成空（Contains 匹配回归？）")
	}
	if !strings.Contains(anchor, "SECI") {
		t.Fatalf("锚点应是 SECI 表格行，got %q", anchor)
	}
	if vp != 0 {
		t.Fatalf("ViewPage 应为 0，got %d", vp)
	}
	// 反向验证：锚点确实【不是】原始页文本的子串（确认测的是修复点）
	if strings.Contains(accumBuf[0], anchor) {
		t.Log("注：该行恰好未被对齐改写，测试强度不足")
	}
}
