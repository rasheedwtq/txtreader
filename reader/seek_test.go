package reader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSeekToSamePage 验证同页多章节可分别精确定位：
// 两章落在同一页，SeekTo 应把各自标题对准到页内正确 rune 偏移（offset 不同）。
func TestSeekToSamePage(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "multi.txt")
	content := "第一章 起始\n" + strings.Repeat("正文甲。", 100) + "\n\n" +
		"第二章 转折\n" + strings.Repeat("正文乙。", 100) + "\n"
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
	if len(toc) != 2 {
		t.Fatalf("应识别 2 章，got %d：%v", len(toc), toc)
	}
	if toc[0].Page != toc[1].Page {
		t.Fatalf("两章应同页（间隔<pageSize）：%d vs %d", toc[0].Page, toc[1].Page)
	}

	txt0, off0, err := b.SeekTo(toc[0].Offset, toc[0].Title)
	if err != nil {
		t.Fatal(err)
	}
	rs0 := []rune(txt0)
	if !strings.HasPrefix(string(rs0[off0:]), toc[0].Title) {
		t.Fatalf("第一章 offset 未对准：%q", string(rs0[off0:off0+12]))
	}

	txt1, off1, err := b.SeekTo(toc[1].Offset, toc[1].Title)
	if err != nil {
		t.Fatal(err)
	}
	rs1 := []rune(txt1)
	if !strings.HasPrefix(string(rs1[off1:]), toc[1].Title) {
		t.Fatalf("第二章 offset 未对准：%q", string(rs1[off1:off1+12]))
	}
	if off0 == off1 {
		t.Fatal("同页两章 offset 应不同（否则页内导航失效）")
	}
}
