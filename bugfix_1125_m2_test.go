package main

// M2（2026-11-25 审查）：同会话 A→B→A 不能丢 A 的进度。
// 本文件两条：restorePoint 的取值优先级（纯逻辑）+ 真实开两本书切回来的集成。

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

func resetBookMarks() (map[string]bookMark, []string) {
	oldMarks, oldSeq := bookMarks, bookMarkSeq
	bookMarks, bookMarkSeq = map[string]bookMark{}, nil
	return oldMarks, oldSeq
}

// TestM2RestorePoint restorePoint 的优先级：lastState 优先，其次会话内书签；
// 旧口径只有 page 时用 page 顶上 viewPage（E4 迁移）。
func TestM2RestorePoint(t *testing.T) {
	oldMarks, oldSeq := resetBookMarks()
	oldLast := lastState
	t.Cleanup(func() { bookMarks, bookMarkSeq, lastState = oldMarks, oldSeq, oldLast })

	lastState = appState{CurrentPath: "a.txt", Page: 5, ViewPage: 4, ViewAnchor: "锚A"}
	bookMarks["a.txt"] = bookMark{page: 1, viewPage: 1, anchor: "旧"}
	bookMarks["b.txt"] = bookMark{page: 3, viewPage: 2, anchor: "锚B"}
	bookMarks["c.txt"] = bookMark{page: 7} // 只有 page，模拟旧口径

	if ok, page, vp, a := restorePoint("a.txt"); !ok || page != 5 || vp != 4 || a != "锚A" {
		t.Fatalf("同路径应以 lastState 为准：ok=%v page=%d vp=%d a=%q", ok, page, vp, a)
	}
	if ok, page, vp, a := restorePoint("b.txt"); !ok || page != 3 || vp != 2 || a != "锚B" {
		t.Fatalf("lastState 不匹配时应取书签：ok=%v page=%d vp=%d a=%q", ok, page, vp, a)
	}
	if ok, _, vp, _ := restorePoint("c.txt"); !ok || vp != 7 {
		t.Fatalf("旧口径应把 page 顶上 viewPage：ok=%v vp=%d", ok, vp)
	}
	if ok, _, _, _ := restorePoint("z.txt"); ok {
		t.Fatal("没有记录的书不应返回 remembered")
	}
}

// TestM2SwitchBackKeepsPage A 读到第 2 页 → 切 B → 切回 A，应回第 2 页。
func TestM2SwitchBackKeepsPage(t *testing.T) {
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
	oldMarks, oldSeq := resetBookMarks()
	oldLast := lastState
	t.Cleanup(func() {
		stateDirOverride = ""
		bookMarks, bookMarkSeq, lastState = oldMarks, oldSeq, oldLast
		if curBook != nil {
			curBook.Close()
			curBook = nil
		}
	})

	dir := t.TempDir()
	pA := filepath.Join(dir, "a.txt")
	contentA := strings.Repeat("甲", 20000) + strings.Repeat("乙", 20000) +
		"丙页起点，这一段用来确认恢复落在第 3 页。" + strings.Repeat("丙", 5000)
	if err := os.WriteFile(pA, []byte(contentA), 0644); err != nil {
		t.Fatal(err)
	}
	pB := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(pB, []byte("丁书很短，只有一页。"), 0644); err != nil {
		t.Fatal(err)
	}

	loadFolderOpen(dir, pA)
	if curBook == nil || curBook.Path() != pA {
		t.Fatalf("首次应打开 a.txt，got %v", curBook)
	}
	if err := curBook.Goto(2); err != nil {
		t.Fatal(err)
	}
	showCurrent() // 落盘 A 的第 2 页 + 记录锚点

	idx := func(path string) int {
		for i, e := range curEntries {
			if e.path == path {
				return i
			}
		}
		t.Fatalf("列表里找不到 %s", path)
		return -1
	}

	openAt(idx(pB)) // A→B：此时应记下 A 的现场
	if curBook == nil || curBook.Path() != pB {
		t.Fatalf("应切到 b.txt，got %v", curBook)
	}

	openAt(idx(pA)) // B→A：应回第 2 页
	if curBook == nil || curBook.Path() != pA {
		t.Fatalf("应切回 a.txt，got %v", curBook)
	}
	if curBook.Page() != 2 {
		t.Fatalf("切回应回第 2 页，got %d（M2 未生效：进度被 B 覆盖）", curBook.Page())
	}
	if cur := curBook.Current(); !strings.Contains(cur, "丙页起点") {
		t.Fatalf("第 2 页内容不对：%.20q", cur)
	}
}
