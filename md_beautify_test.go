package main

import (
	"strings"
	"testing"
)

// TestMdBeautify 验证 md → 美化纯文本：标题/分隔线/列表/引用/粗体/代码/表格对齐，行数不变。
func TestMdBeautify(t *testing.T) {
	in := "# 书名\n" +
		"\n" +
		"## 章节标题\n" +
		"\n" +
		"### 小节\n" +
		"\n" +
		"- 项目甲\n" +
		"- 项目乙\n" +
		"\n" +
		"> 引用句子\n" +
		"\n" +
		"---\n" +
		"\n" +
		"这是**加粗**和`代码`混排。\n" +
		"\n" +
		"| 列A | 列B |\n" +
		"|---|---|\n" +
		"| 1 | 中文值 |\n" +
		"| 22 | x |\n"

	out := mdBeautify(in)

	inLines := strings.Count(in, "\n")
	outLines := strings.Count(out, "\n")
	if inLines != outLines {
		t.Fatalf("行数必须 1:1（TOC 定位依赖）：in=%d out=%d", inLines, outLines)
	}
	want := []string{
		"书名",           // H1 顶格
		"  章节标题",        // H2 缩 2
		"    小节",         // H3 缩 4
		"- 项目甲",          // 列表原样保留
		"- 项目乙",
		"    引用句子",       // 引用按层缩进
		"这是加粗和代码混排。", // 粗体/代码去标记
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("缺少转换结果 %q；输出：\n%s", w, out)
		}
	}
	// 表格保持 |/- 制表符风格、按列宽对齐（列A 宽3+2、列B 宽6+2）
	if !strings.Contains(out, "|-----|--------|") || !strings.Contains(out, "中文值 |") {
		t.Errorf("表格应保持 | 风格并对齐；输出：\n%s", out)
	}
	// 不应再出现发明出来的符号/框线字符
	for _, bad := range []string{"■", "◆", "▏", "•", "├", "┼", "┤", "【书名】"} {
		if strings.Contains(out, bad) {
			t.Errorf("不应出现符号 %q；输出：\n%s", bad, out)
		}
	}
	// 普通正文不受影响
	if !strings.Contains(out, "这是加粗和代码混排。") {
		t.Error("普通正文应保留")
	}
}

// TestMdTableCap 验证超长单元格触发列宽上限：截断加省略号、分隔行不爆宽。
func TestMdTableCap(t *testing.T) {
	long := strings.Repeat("长", 60) // 显示宽 120，远超上限 28
	in := "| A | B |\n|---|---|\n| 1 | " + long + " |\n"
	out := mdBeautify(in)
	if !strings.Contains(out, "…") {
		t.Errorf("超长单元格应截断加省略号；输出：\n%s", out)
	}
	if strings.Contains(out, strings.Repeat("-", 31)) {
		t.Error("分隔行段宽不应超过上限（28+2）")
	}
	if strings.Contains(out, strings.Repeat("长", 14)) {
		t.Error("截断后不应保留超长内容")
	}
	if strings.Count(out, "\n") != strings.Count(in, "\n") {
		t.Fatal("行数必须 1:1")
	}
}

// TestMdBeautifyPlainText txt 文件不走美化（curIsMD=false 时 displayText 直通）。
func TestMdBeautifyPlainText(t *testing.T) {
	old := curIsMD
	curIsMD = false
	defer func() { curIsMD = old }()

	in := "# 不是md\n- 甲\n"
	out := displayText(in)
	if !strings.Contains(out, "# 不是md") {
		t.Error("txt 模式下 # 应原样保留")
	}
}
