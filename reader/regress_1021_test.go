package reader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPumpAfterLastFirst 回归（2026-10-21 审查 N-1 的实证）：
// 末页(Last) → 首页(First) → 累积式加载泵必须还能追加下一页。
// 关键机制：First() 首行清 tail、readUpTo 会 resetReader(eof=false)，
// 所以"tail 污染导致 eof 恒真、泵永久失效"不成立——此测试锁死该结论。
func TestPumpAfterLastFirst(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ten.txt")
	if err := os.WriteFile(p, []byte(strings.Repeat("甲", defaultPageRunes*10)), 0644); err != nil {
		t.Fatal(err)
	}
	b, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if _, err := b.Last(); err != nil {
		t.Fatal(err)
	}
	if b.Page() != 9 {
		t.Fatalf("末页应为第 9 页，got %d", b.Page())
	}
	if _, err := b.First(); err != nil {
		t.Fatal(err)
	}
	if b.tail != "" {
		t.Fatalf("First() 应清空 tail，实际 %d 字节", len(b.tail))
	}
	if b.eof {
		t.Fatal("First() 后 eof 应为 false（readUpTo→resetReader）")
	}
	s, more, err := b.LoadNextPage()
	if err != nil {
		t.Fatal(err)
	}
	if !more {
		t.Fatal("首页后应能追加第 1 页（more=true）")
	}
	if b.Page() != 1 {
		t.Fatalf("追加后应停在第 1 页，got %d", b.Page())
	}
	if len(s) == 0 {
		t.Fatal("追加页内容为空")
	}
}

// TestSeekToSplitTitle 回归（2026-10-21 审查 N-2）：章节标题行横跨页边界时，
// 整串标题不在任何单页里——SeekTo 必须退化到"标题开头"的位置，
// 而不是退化为页首（否则目录跳转落在整页前文上，标题还被切成两半）。
func TestSeekToSplitTitle(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "straddle.txt")
	// 标题行起点落在 pageSize-5 → 标题被切成两半（前 5 rune 在第 0 页）
	content := strings.Repeat("甲", defaultPageRunes-6) + "\n第一章 开始\n后续内容\n"
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	b, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	toc, err := b.BuildTOC()
	if err != nil {
		t.Fatal(err)
	}
	if len(toc) == 0 {
		t.Fatal("应识别出章节标题")
	}
	e := toc[0]
	if e.Title != "第一章 开始" {
		t.Fatalf("标题应完整识别为 %q，got %q", "第一章 开始", e.Title)
	}
	txt, off, err := b.SeekTo(e.Offset, e.Title)
	if err != nil {
		t.Fatal(err)
	}
	if off <= 0 {
		t.Fatalf("标题跨页时应定位到标题开头附近，页内偏移=%d（退化成页首）", off)
	}
	rs := []rune(txt)
	if got := string(rs[off:min(len(rs), off+5)]); got != "第一章 开" {
		t.Fatalf("落点应是标题开头 %q，got %q", "第一章 开", got)
	}
}
