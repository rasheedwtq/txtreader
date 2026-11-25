package main

// H2（2026-11-25 审查）：打开失败后关窗，不能把"上次在读哪本、第几页"抹掉。
// 同时锁住正常路径：curBook 非 nil 时照旧写当前书的进度。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"txtreader/reader"
)

// snapshotGlobals 保存/恢复 persist 涉及的全局状态，避免污染同包其它测试。
func snapshotGlobals(t *testing.T) {
	t.Helper()
	oldState, oldDir, oldBook := lastState, curDir, curBook
	oldFamily, oldSize, oldSpacing, oldClean := curFamily, curSize, curSpacing, curClean
	oldOverride := stateDirOverride
	t.Cleanup(func() {
		lastState, curDir, curBook = oldState, oldDir, oldBook
		curFamily, curSize, curSpacing, curClean = oldFamily, oldSize, oldSpacing, oldClean
		stateDirOverride = oldOverride
	})
}

// TestH2PersistKeepsProgressWithoutBook 模拟「选中打不开的文件 → 关窗」：
// 进度四字段沿用上一份快照，目录/字体/清洗偏好仍按当前值落盘。
func TestH2PersistKeepsProgressWithoutBook(t *testing.T) {
	snapshotGlobals(t)
	stateDirOverride = t.TempDir()

	prev := appState{
		LastDir:     `D:\book\旧目录`,
		CurrentPath: `D:\book\旧目录\a.txt`,
		Page:        3,
		ViewPage:    2,
		ViewAnchor:  "他说完这句话就走了",
		FontFamily:  "宋体",
		FontSize:    18,
	}
	saveState(prev)
	lastState = prev

	// 打开失败后的状态：书没了，但用户改了目录和字体
	curBook = nil
	curDir = `D:\book\新目录`
	curFamily = "黑体"
	curSize = 20
	curSpacing = 1
	curClean = reader.CleanOptions{Indent: false, AdFilter: true, BlankAll: false}

	persist()

	got, ok := loadState()
	if !ok {
		t.Fatal("loadState 失败")
	}
	if got.CurrentPath != prev.CurrentPath || got.Page != prev.Page ||
		got.ViewPage != prev.ViewPage || got.ViewAnchor != prev.ViewAnchor {
		t.Fatalf("打开失败后进度被抹：want path=%q page=%d viewPage=%d anchor=%q, got %+v",
			prev.CurrentPath, prev.Page, prev.ViewPage, prev.ViewAnchor, got)
	}
	if got.LastDir != curDir || got.FontFamily != curFamily || got.FontSize != curSize || got.LineSpacing != curSpacing {
		t.Fatalf("目录/字体偏好应按当前值落盘：got %+v", got)
	}
	if got.CleanIndent == nil || *got.CleanIndent != false || got.CleanAd == nil || *got.CleanAd != true {
		t.Fatalf("清洗偏好应按当前值落盘：got indent=%v ad=%v", deref(got.CleanIndent), deref(got.CleanAd))
	}
}

// TestH2PersistNormalBookStillWrites 锁正常路径：有书时进度来自当前书，
// 不能被上面的兜底分支覆盖。
func TestH2PersistNormalBookStillWrites(t *testing.T) {
	snapshotGlobals(t)
	stateDirOverride = t.TempDir()

	p := filepath.Join(t.TempDir(), "book.txt")
	if err := os.WriteFile(p, []byte("第一页正文\n"), 0644); err != nil {
		t.Fatal(err)
	}
	b, err := reader.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	curBook = b
	t.Cleanup(func() { b.Close() })

	// 上一份快照故意指向别的书，确保走的是当前书而不是兜底
	lastState = appState{CurrentPath: `D:\book\other.txt`, Page: 9, ViewPage: 9, ViewAnchor: "别的书"}

	persist()

	got, ok := loadState()
	if !ok {
		t.Fatal("loadState 失败")
	}
	if got.CurrentPath != p {
		t.Fatalf("有书时应写当前书路径：want %q got %q", p, got.CurrentPath)
	}
	if got.Page != 0 || got.ViewPage != 0 || got.ViewAnchor != "" {
		t.Fatalf("窗口隐藏时 anchor 应为空、页码为 0：got page=%d viewPage=%d anchor=%q",
			got.Page, got.ViewPage, got.ViewAnchor)
	}
}

// TestH4DisplayOffsetPrefersHeadingLine 显示层也要优先行首：
// 正文里的 "12" 先出现，跳章落点必须对齐到标题行。
func TestH4DisplayOffsetPrefersHeadingLine(t *testing.T) {
	oldMD := curIsMD
	curIsMD = false
	t.Cleanup(func() { curIsMD = oldMD })

	page := "正文：他数到 12 才停。\n12\n这一章的正文。"
	d := displayText(page)
	dr := []rune(d)
	bodyPos := len([]rune(d[:strings.Index(d, "12")]))

	got := displayRuneOffset(page, "12", 14)
	if got <= 0 || got+2 > len(dr) || string(dr[got:got+2]) != "12" {
		t.Fatalf("落点不是 12：got=%d", got)
	}
	if got == bodyPos {
		t.Fatal("落到了正文里的 12（displayRuneOffset 未按行首优先）")
	}

	// 找不到标题时仍退化为 0（旧口径），不能 panic
	if off := displayRuneOffset(page, "不存在的标题", 0); off != 0 {
		t.Fatalf("找不到应返回 0，got %d", off)
	}
}
