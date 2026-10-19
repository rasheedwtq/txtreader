package reader

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBuildTOC 验证综合规则识别 + 页码映射（全书 < 一页，页码应全 0）。
func TestBuildTOC(t *testing.T) {
	content := "前言\n" +
		"一些正文内容。\n\n" +
		"第一章 风云初起\n" +
		"正文第一段。\n" +
		"正文第二段，较长的内容用于填充，确保跨页或至少若干行。\n\n" +
		"第二章 江湖再见\n" +
		"更多正文。\n\n" +
		"# Markdown 标题也能识别\n" +
		"md 正文。\n\n" +
		"Chapter 3 The End\n" +
		"english body\n\n" +
		"这是一段普通正文，不应被识别为章节，因为没有序号也没有关键词特征。\n" +
		"后记\n"

	dir := t.TempDir()
	p := filepath.Join(dir, "book.txt")
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
	want := []string{
		"前言", "第一章 风云初起", "第二章 江湖再见",
		"Markdown 标题也能识别", "Chapter 3 The End", "后记",
	}
	if len(toc) != len(want) {
		t.Fatalf("章节数=%d, want %d; got=%v", len(toc), len(want), toc)
	}
	for i, w := range want {
		if toc[i].Title != w {
			t.Errorf("toc[%d].Title=%q, want %q", i, toc[i].Title, w)
		}
		if toc[i].Page != 0 {
			t.Errorf("toc[%d].Page=%d, want 0 (全书 < 一页)", i, toc[i].Page)
		}
	}
}

// TestIsChapterTitle 覆盖综合规则与误判排除。
func TestIsChapterTitle(t *testing.T) {
	cases := map[string]bool{
		"第一章 风云初起":           true,
		"第12章 终局":              true,
		"第百回 大战":              true,
		"序":                     true,
		"后记":                    true,
		"楔子":                    true,
		"Chapter 3 The End":      true,
		"SECTION 2 overview":     true,
		"# 标题":                  true,
		"## 二级标题":              true,
		// 数字/中文数字类章节分割
		"一":                     true,
		"1":                     true,
		"十二":                   true,
		"007":                   true,
		"一、风波暗涌":             true,
		"1. 起点":                true,
		"12、风云突变":            true,
		"（一）":                  true,
		"(2)":                   true,
		"12 风波":                true,
		// 反例保护
		"2008年大事记":            false, // 4 位数字且无分隔符
		"3个人走在路上":             false, // 数字后无空格/分隔符
		"a":                     false, // 太短且非关键词/数字
		"正文内容继续写下去没有章节特征":     false, // 无模式
		"这是一段普通正文不应被识别为章节因为太长太长太长太长太长太长太长太长太长太长太长太长太长太长太长太长太长太长": false, // 超长
	}
	for in, want := range cases {
		if got := isChapterTitle(in); got != want {
			t.Errorf("isChapterTitle(%q)=%v, want %v", in, got, want)
		}
	}
}
