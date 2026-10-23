package reader

import (
	"os"
	"strings"
	"testing"
)

// ---- 清洗选项专项（方案 §2：C0 修复 / A1 缩进 / B1 广告 / E1 空行全收）----

// TestCleanC0FullWidthBlank C0 回归（2026-10-22 探针实锤的 bug）：
// 全角空格空行必须与半角/Tab 同等对待——旧行为原样漏网。
func TestCleanC0FullWidthBlank(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"全角空行x2", "A\n\u3000\n\u3000\nB", "A\n\nB"},
		{"全角空行x4", "A\n\u3000\n\u3000\n\u3000\n\u3000\nB", "A\n\nB"},
		{"全角行尾", "A\u3000\nB", "A\nB"},
		{"混用空白行", "A\n \u3000\t \n\nB", "A\n\nB"},
		{"半角不回归", "A\n \n \nB", "A\n\nB"},
		{"Tab不回归", "A\n\t\n\t\nB", "A\n\nB"},
	}
	for _, c := range cases {
		if got := clean(c.in); got != c.want {
			t.Errorf("[C0 %s] got=%q want=%q", c.name, got, c.want)
		}
	}
}

// TestCleanIndent A1：非空行行首空白统一为 2 全角；空行不动；关=旧行为。
func TestCleanIndent(t *testing.T) {
	in := "　　已缩进段落\n  半角四格\n\tTab行\n无缩进\n\n下一段"
	want := "　　已缩进段落\n　　半角四格\n　　Tab行\n　　无缩进\n\n　　下一段"
	if got := cleanWith(in, CleanOptions{Indent: true}); got != want {
		t.Fatalf("Indent on:\n got=%q\nwant=%q", got, want)
	}
	// 关闭 = 旧行为：行首空白原样保留
	if got := cleanWith(in, CleanOptions{}); !strings.HasPrefix(got, "　　已缩进段落") {
		t.Fatalf("Indent off 应保留原缩进，got=%q", got)
	}
}

// TestCleanAdFilter B1：整行广告剔除；含广告词的正文行必须保留。
func TestCleanAdFilter(t *testing.T) {
	ads := []string{
		"天才一秒记住本站地址：www.abc.com",
		"最新章节请到 www.abc.com",
		"https://example.com/book/123",
		"www.abc.com",
		"本书来自互联网，仅供交流",
		"求收藏！",
		"求推荐票，谢谢",
		"（本章完）",
		"(本章完)",
		"未完待续。",
		"笔趣阁 www.biquge.com 提供",
		"起点中文网",
		"本章未完，点击下一页继续阅读",
	}
	for _, a := range ads {
		got := cleanWith("前文。\n"+a+"\n后文。", CleanOptions{AdFilter: true})
		if strings.Contains(got, a) {
			t.Errorf("[B1] 广告行未剔除：%q → %q", a, got)
		}
	}

	keep := []string{
		"他在纸上写下：求收藏，求推荐票。", // 对话里含广告词——锚定整行匹配不误杀
		"这本书讲的是笔趣阁的兴衰史。",
		"中文网文学的未来在哪里？",
		"点击下一页的按钮坏了。", // 句中出现，非整行
		"未完待续的故事还有很多。",
	}
	for _, k := range keep {
		got := cleanWith("前文。\n"+k+"\n后文。", CleanOptions{AdFilter: true})
		if !strings.Contains(got, k) {
			t.Errorf("[B1] 正文行被误杀：%q → %q", k, got)
		}
	}
}

// TestCleanBlankAll E1：空行全收；配合缩进段落仍可辨（纸书排版）。
func TestCleanBlankAll(t *testing.T) {
	in := "第一段。\n\n\n第二段。\n\n第三段。"
	if got := cleanWith(in, CleanOptions{BlankAll: true}); strings.Contains(got, "\n\n") {
		t.Fatalf("空行全收后不应残留空行：%q", got)
	}
	// 关闭 = 现状：最多保留 1 空行
	if got := cleanWith(in, CleanOptions{}); got != "第一段。\n\n第二段。\n\n第三段。" {
		t.Fatalf("BlankAll off 行为变化：%q", got)
	}
}

// TestCleanOptsSwitch 切档口径：SetCleanOpts 后页内容按新口径重读，页码保持。
func TestCleanOptsSwitch(t *testing.T) {
	dir := t.TempDir()
	p := dir + "\\opts.txt"
	content := strings.Repeat("段落文字。\n\n", 5000) // ~3 万 rune，>1 页
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	b, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := b.Next(); err != nil {
		t.Fatal(err)
	}
	pageBefore := b.Page()
	b.SetCleanOpts(CleanOptions{BlankAll: true})
	if b.Page() != pageBefore {
		t.Fatalf("切档应保持页码：got %d want %d", b.Page(), pageBefore)
	}
	if strings.Contains(b.Current(), "\n\n") {
		t.Fatal("切到空行全收后，页内不应残留空行")
	}
}
