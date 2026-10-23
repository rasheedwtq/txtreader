package reader

import (
	"os"
	"path/filepath"
	"strings"
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
		"第12章 终局":            true,
		"第百回 大战":             true,
		"序":                  true,
		"后记":                 true,
		"楔子":                 true,
		"Chapter 3 The End":  true,
		"SECTION 2 overview": true,
		"# 标题":               true,
		"## 二级标题":            true,
		// 数字/中文数字类章节分割
		"一":       true,
		"1":       true,
		"十二":      true,
		"007":     true,
		"一、风波暗涌":  true,
		"1. 起点":   true,
		"12、风云突变": true,
		"（一）":     true,
		"(2)":     true,
		"12 风波":   true,
		// 规则库新增（legado/TRNovel 移植）
		"第壹佰贰拾叁节 试炼": true, // 财务大写数字
		"第12卷 风云再起":  true, // 分卷
		"上卷":         true, // 上/中/下卷
		"引子":         true, // 新增关键词
		"外传":         true, // 新增关键词
		// 抗误报排除
		"第三部分":      false, // 计数词歧义
		"第三节课":      false, // 计数词歧义
		"第五部队":      false, // 计数词歧义
		"第十二章 写完了。": false, // 句号结尾
		"第二部结束。":    false, // 句号结尾
		// D1：行首全角缩进冻结弱规则（源文件真实缩进的正文段）
		"\u3000\u300012 年前，他还是个孩子": false,
		"\u3000\u3000（三）":          false,
		"\u3000\u3000十二":           false,
		// D1 反向：缩进不冻结强规则——缩进的真章节仍识别
		"\u3000\u3000第十二章 归来":   true,
		"\u3000\u3000Chapter 4": true,
		// 反例保护
		"2008年大事记": false, // 4 位数字且无分隔符
		"3个人走在路上":  false, // 数字后无空格/分隔符
		"a":        false, // 太短且非关键词/数字
		"正文内容继续写下去没有章节特征": false, // 无模式
		"这是一段普通正文不应被识别为章节因为太长太长太长太长太长太长太长太长太长太长太长太长太长太长太长太长太长太长": false, // 超长
	}
	for in, want := range cases {
		if got := isChapterTitle(in); got != want {
			t.Errorf("isChapterTitle(%q)=%v, want %v", in, got, want)
		}
	}
}

// TestChapterTitleKeywordCap B4 回归：关键词前缀匹配必须有 40 字上限，
// 否则正文段落（以"前言/附录"起头的长句）会被灌进目录。
func TestChapterTitleKeywordCap(t *testing.T) {
	long := "前言往事如烟，这一段的正文非常长，" + strings.Repeat("内容", 60)
	if isChapterTitle(long) {
		t.Errorf("%d 字正文段不应被判为章节标题", len([]rune(long)))
	}
	long2 := "附录里他还提到，" + strings.Repeat("字", 100)
	if isChapterTitle(long2) {
		t.Errorf("100+ 字正文不应因「附录」前缀成目录项")
	}
	if !isChapterTitle("前言") {
		t.Error("短关键词行仍应识别")
	}
	if !isChapterTitle("楔子") {
		t.Error("短关键词行仍应识别")
	}
}
