package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// TestCLIReopenKeepsProgress 现场复现（2026-10-22 大叔报告：json 里书在、页码归 0）：
// 上次读到第 2 页的书，双击文件（CLI 路径）重新打开——openCLIPath 不走会话恢复，
// openAt 从第 0 页开 + showCurrent 立即 persist，把上次进度覆盖销毁。
// 修复：openAt 统一按启动快照 lastState 恢复单本进度（CLI/会话两路共用）。
func TestCLIReopenKeepsProgress(t *testing.T) {
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
	stateDirOverride = t.TempDir()
	defer func() { stateDirOverride = "" }()
	t.Cleanup(func() { // 先于 TempDir 清理执行（LIFO），否则书句柄占文件删不掉
		if curBook != nil {
			curBook.Close()
			curBook = nil
		}
	})

	dir := t.TempDir()
	p := filepath.Join(dir, "book.txt")
	content := strings.Repeat("甲", 20000) +
		strings.Repeat("乙", 20000) +
		strings.Repeat("丙", 5000)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// 第一次打开：读到第 2 页并落盘
	loadFolderOpen(dir, p)
	if curBook == nil {
		t.Fatal("首次打开失败")
	}
	if err := curBook.Goto(2); err != nil {
		t.Fatal(err)
	}
	showCurrent() // persist：page=2
	saved, _ := loadState()
	if saved.Page != 2 {
		t.Fatalf("落盘页码应为 2，got %d", saved.Page)
	}

	// 模拟重启：关书、清恢复快照
	curBook.Close()
	curBook = nil
	lastState, _ = loadState()

	// 模拟 CLI 双击启动（os.Args 带路径参数）
	oldArgs := os.Args
	os.Args = []string{"txtreader.exe", p}
	defer func() { os.Args = oldArgs }()
	if !openCLIPath() {
		t.Fatal("CLI 路径应接管启动")
	}
	if curBook == nil || curBook.Path() != p {
		t.Fatalf("CLI 应打开目标书，got %v", curBook)
	}
	if curBook.Page() != 2 {
		t.Fatalf("CLI 重开应恢复到第 2 页，got %d（进度被 page=0 覆盖？）", curBook.Page())
	}
	if cur := curBook.Current(); !strings.Contains(cur, "丙") {
		t.Fatalf("第 2 页应含『丙』，got 前 10 字=%q", []rune(cur[:10]))
	}
}
