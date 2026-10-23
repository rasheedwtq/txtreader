package reader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---- 2026-10-22 bug 审查报告修复回归（B2/B3/B4）----

// writeRuneBook 写一本 page0=fill1、其余页=fill2 的书，字符可区分便于断言。
func writeRuneBook(t *testing.T, total int) (*Book, string, string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "book.txt")
	page0 := strings.Repeat("甲", defaultPageRunes)
	rest := strings.Repeat("乙", total-defaultPageRunes)
	if err := os.WriteFile(p, []byte(page0+rest), 0644); err != nil {
		t.Fatal(err)
	}
	b, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b, page0, rest
}

// TestSeekToClampedOffset B2 回归：offset 越过文件末尾时 readUpTo 钳制到末页，
// 缓存键必须用钳制后的页号——旧行为用钳制前页号查 cache 必 miss，返回整屏空白。
func TestSeekToClampedOffset(t *testing.T) {
	b, _, rest := writeRuneBook(t, defaultPageRunes+100) // 一页多一点
	defer b.Close()
	txt, off, err := b.SeekTo(5*defaultPageRunes, "不存在")
	if err != nil {
		t.Fatal(err)
	}
	if txt == "" {
		t.Fatal("SeekTo 越界偏移应落到实际末页文本，而不是返回空白页")
	}
	if !strings.Contains(txt, rest[:50]) {
		t.Fatalf("落点应是末页（含『乙』），got 前 20 字=%q", []rune(txt[:20]))
	}
	if off != 0 {
		t.Fatalf("标题未命中应退化为页首 off=0，got %d", off)
	}
}

// TestGotoClearsTail B3 回归：Last() 置 tail 后 Goto 不清 tail，
// Current() 会返回陈旧文本（API 埋雷，UI 未来任何 Goto 后读 Current 必踩）。
func TestGotoClearsTail(t *testing.T) {
	b, page0, _ := writeRuneBook(t, defaultPageRunes+100)
	defer b.Close()
	if _, err := b.Last(); err != nil {
		t.Fatal(err)
	}
	if err := b.Goto(0); err != nil {
		t.Fatal(err)
	}
	got := b.Current()
	if got == "" || !strings.HasPrefix(got, page0[:20]) {
		t.Fatalf("Goto(0) 后 Current() 应是第 0 页文本（甲…），got 前 20 字=%q", []rune(got[:minInt(20, len([]rune(got)))]))
	}
}

// TestLastPageExact B4 回归：total 不整页时 Last() 只含末页部分，
// 且 cache[Page()] ≡ Goto(Page()) 的所见（persist 页码恢复后内容一致）。
func TestLastPageExact(t *testing.T) {
	const extra = 100
	b, _, _ := writeRuneBook(t, defaultPageRunes+extra)
	defer b.Close()
	s, err := b.Last()
	if err != nil {
		t.Fatal(err)
	}
	rs := []rune(s)
	if len(rs) != extra {
		t.Fatalf("Last() 应只含末页 %d rune，got %d", extra, len(rs))
	}
	if strings.ContainsRune(s, '甲') {
		t.Fatal("末页视图不应混入第 0 页文本（乙书混进甲）")
	}
	if b.Page() != 1 {
		t.Fatalf("Page() 应为 1，got %d", b.Page())
	}
	// 口径闭环：Goto(Page()) 重建后的末页与 Last() 所见一致
	if err := b.Goto(1); err != nil {
		t.Fatal(err)
	}
	if b.Current() != s {
		t.Fatalf("Goto(1) 与 Last() 所见不一致：\nlast=%q\ngoto=%q", []rune(s[:20]), []rune(b.Current()[:20]))
	}
}

// TestLastExactMultiple 整页对齐时 Last() 不受影响（20000 整页 → Page()=0）。
func TestLastExactMultiple(t *testing.T) {
	b, page0, _ := writeRuneBook(t, defaultPageRunes)
	defer b.Close()
	s, err := b.Last()
	if err != nil {
		t.Fatal(err)
	}
	if b.Page() != 0 {
		t.Fatalf("整页书末页应为第 0 页，got %d", b.Page())
	}
	if len([]rune(s)) != defaultPageRunes {
		t.Fatalf("Last() 应含全部 %d rune，got %d", defaultPageRunes, len([]rune(s)))
	}
	_ = page0
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
