package reader

// H3 / H4（2026-11-25 审查）回归：
// H3 —— \r 也是行边界；\r\n 不得重复冲刷，Windows 文本的条目与偏移必须与旧版一致。
// H4 —— 页内定位优先行首，正文里更早的同一串不能把落点抢走；找不到行首时退回旧口径。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeBook(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "book.txt")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestH3CRLineBoundaries 混合换行 / 纯 CR / CRLF 三组。
func TestH3CRLineBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		content string
		titles  []string
		offsets []int
	}{
		{
			// \n 与 \r、\r\n 混排：三个章节都要在目录里，偏移指向各自行首
			name:    "混合换行",
			content: "第一章 甲\n正文\r第二章 乙\r\n第三章 丙\n",
			titles:  []string{"第一章 甲", "第二章 乙", "第三章 丙"},
			offsets: []int{0, 9, 16},
		},
		{
			// 纯 CR（老 Mac 文本）：不再并成一行
			name:    "纯CR",
			content: "第一章 甲\r第二章 乙\r",
			titles:  []string{"第一章 甲", "第二章 乙"},
			offsets: []int{0, 6},
		},
		{
			// CRLF 回归：偏移必须与旧版逐字节一致（旧版只在 \n 处冲刷）
			name:    "CRLF偏移不变",
			content: "第一章 甲\r\n正文\r\n第二章 乙\r\n",
			titles:  []string{"第一章 甲", "第二章 乙"},
			offsets: []int{0, 11},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := Open(writeBook(t, c.content))
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			toc, err := b.BuildTOC()
			if err != nil {
				t.Fatal(err)
			}
			if len(toc) != len(c.titles) {
				t.Fatalf("条目数=%d want %d：%+v", len(toc), len(c.titles), toc)
			}
			for i := range c.titles {
				if toc[i].Title != c.titles[i] {
					t.Errorf("toc[%d].Title=%q want %q", i, toc[i].Title, c.titles[i])
				}
				if toc[i].Offset != c.offsets[i] {
					t.Errorf("toc[%d].Offset=%d want %d（raw rune 口径）", i, toc[i].Offset, c.offsets[i])
				}
			}
		})
	}
}

// TestH4SeekToPrefersLineHead 正文先出现 "12"，标题行在后面：
// SeekTo 必须落在标题行首，不能落到正文里的 "12"。
func TestH4SeekToPrefersLineHead(t *testing.T) {
	content := "正文：他数到 12 才停。\n12\n这一章的正文。\n"
	b, err := Open(writeBook(t, content))
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	headingOff := len([]rune(content[:strings.LastIndex(content, "12")]))
	if headingOff != 14 {
		t.Fatalf("样本偏移变了：headingOff=%d", headingOff)
	}
	txt, off, err := b.SeekTo(headingOff, "12")
	if err != nil {
		t.Fatal(err)
	}
	rs := []rune(txt)
	if off <= 0 || off >= len(rs) {
		t.Fatalf("页内偏移越界：off=%d len=%d", off, len(rs))
	}
	if got := string(rs[off : off+min(4, len(rs)-off)]); !strings.HasPrefix(got, "12\n") {
		t.Fatalf("应落在标题行首，got %q（落点 %d）", got, off)
	}
	if strings.HasPrefix(string(rs[off:]), "12 才停") {
		t.Fatal("落到了正文里的 12（H4 未修好）")
	}
}

// TestH4NoLineHeadFallsBack 只有行中出现时，保持旧口径（首次出现），
// 防止 "标题被页边界切断 / md 前缀被剥掉" 这类情形反而退化。
func TestH4NoLineHeadFallsBack(t *testing.T) {
	page := "他说 12 才停\n后面还有别的\n"
	if got := IndexLineHead(page, "12", -1); got != 3 {
		t.Fatalf("无行首候选应退回首次出现（3），got %d", got)
	}
}

// TestH4MultipleLineHeadsPickNearest 两个行首候选时取离 hint 最近的，
// 行首空白（半角空格 / 全角空格）不算阻挡。
func TestH4MultipleLineHeadsPickNearest(t *testing.T) {
	page := "12\n" + strings.Repeat("正", 50) + "\n　　12\n后面\n"
	first := 0
	second := len([]rune(page[:strings.LastIndex(page, "12")]))
	if got := IndexLineHead(page, "12", second); got != second {
		t.Fatalf("应取靠近 hint 的第二个行首：want %d got %d（first=%d）", second, got, first)
	}
	if got := IndexLineHead(page, "12", first); got != first {
		t.Fatalf("hint 指向第一个时应取第一个：want %d got %d", first, got)
	}
}
