package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// TestTocJumpAccumBase E2 回归：tocJump 重置累积缓冲后必须同步 accumBase，
// 否则跳章后关窗，captureView 用旧坐标系定页 → 存下错误 ViewPage → 重开错位。
func TestTocJumpAccumBase(t *testing.T) {
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
			ListBox{AssignTo: &tocLB, Model: []string{}},
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
	p := filepath.Join(dir, "book.txt")
	var sb strings.Builder
	sb.WriteString("第一章 开篇\n")
	for i := 0; i < 2600; i++ { // ~2 万字符/章，正文行不带章头前缀（否则规则库全误判）
		sb.WriteString(fmt.Sprintf("甲甲甲 %04d\n", i))
	}
	sb.WriteString("第二章 转折\n")
	for i := 0; i < 2600; i++ {
		sb.WriteString(fmt.Sprintf("乙乙乙 %04d\n", i))
	}
	sb.WriteString("第三章 收尾\n")
	for i := 0; i < 2600; i++ {
		sb.WriteString(fmt.Sprintf("丙丙丙 %04d\n", i))
	}
	content := sb.String()
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	loadFolderOpen(dir, p)
	if curBook == nil {
		t.Fatal("打开失败")
	}
	var err error
	curTOC, err = curBook.BuildTOC() // 必须赋值全局（短声明会遮蔽，tocJump 读到 nil）
	if err != nil {
		t.Fatal(err)
	}
	if len(curTOC) < 3 {
		t.Fatalf("应识别 3 章，got %d", len(curTOC))
	}
	titles := make([]string, len(curTOC))
	for i, e := range curTOC {
		titles[i] = e.Title
	}
	tocLB.SetModel(titles)

	// 开书时 showTOC 的后台建目录与 tocLB 操作存在竞态（applyTOC 落地会
	// SetModel 重建列表并清选中态，tocJump 会读到 -1 直接返回）——重试直至
	// 跳转成功；后台落地后必成功。
	tocLoading = false
	for i := 0; i < 30; i++ {
		tocLB.SetCurrentIndex(1)
		tocJump()
		if curBook.Page() != 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if curBook.Page() == 0 {
		t.Fatal("重试后仍未跳离第 0 页")
	}
	if accumBase != curBook.Page() {
		t.Fatalf("E2 回归：tocJump 后 accumBase=%d 应等于 Page=%d", accumBase, curBook.Page())
	}
	persist()
	saved, _ := loadState()
	if saved.ViewPage != curBook.Page() {
		t.Fatalf("ViewPage=%d 应等于 Page=%d", saved.ViewPage, curBook.Page())
	}
}

// TestLegacyStatePageMigration E4 回归：旧口径 state.json（只有 page 没有
func TestLegacyStatePageMigration(t *testing.T) {
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
			TextEdit{AssignTo: &te, VScroll: true, ReadOnly: true},
		},
	}).Create(); err != nil {
		t.Skipf("无法创建窗口：%v", err)
	}
	defer mw.Dispose()
	stateDir := t.TempDir()
	stateDirOverride = stateDir
	defer func() { stateDirOverride = "" }()
	t.Cleanup(func() {
		if curBook != nil {
			curBook.Close()
			curBook = nil
		}
	})

	dir := t.TempDir()
	p := filepath.Join(dir, "book.txt")
	content := strings.Repeat("甲", 20000) + strings.Repeat("乙", 20000) + strings.Repeat("丙", 5000)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// 预置旧口径 state：page=2，无 view_* 字段
	legacy := `{"last_dir":"` + strings.ReplaceAll(dir, `\`, `\\`) +
		`","current_path":"` + strings.ReplaceAll(p, `\`, `\\`) +
		`","page":2,"font_family":"宋体","font_size":14,"line_spacing":1}`
	if err := os.WriteFile(filepath.Join(stateDir, "txtreader.state.json"), []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}

	lastState, _ = loadState()
	oldArgs := os.Args
	os.Args = []string{"txtreader.exe", p}
	defer func() { os.Args = oldArgs }()
	if !openCLIPath() {
		t.Fatal("CLI 应接管启动")
	}
	if curBook == nil || curBook.Page() != 2 {
		t.Fatalf("E4 回归：旧口径 page=2 应迁移恢复，got page=%d", curBook.Page())
	}
	if !strings.Contains(curBook.Current(), "丙") {
		t.Fatal("第 2 页应是丙段内容")
	}
}

// 对应行文本包含目标标题。
