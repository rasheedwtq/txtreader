package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestCollectTxtMany 验证当前层收集正确性（txt + md 混合）。
func TestCollectTxtMany(t *testing.T) {
	dir := t.TempDir()
	const n = 500
	for i := 0; i < n; i++ {
		ext := ".txt"
		if i%10 == 0 {
			ext = ".md"
		}
		name := filepath.Join(dir, fmt.Sprintf("book_%05d%s", i, ext))
		if err := os.WriteFile(name, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	ents, err := collectTxt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != n {
		t.Fatalf("collectTxt 返回 %d 个，want %d（txt+md 混合）", len(ents), n)
	}
}

// TestCollectTxtDirError 回归（2026-10-21 卫生项 2）：目录读失败必须报错，
// 不能与"目录里真没书"一样返回空列表（否则 UI 会显示"无文档"，用户以为书没了）。
func TestCollectTxtDirError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "不存在的目录")
	ents, err := collectTxt(missing)
	if err == nil {
		t.Fatal("目录不存在时应返回 error")
	}
	if ents != nil {
		t.Errorf("失败时应返回 nil 列表，got %v", ents)
	}
}

// TestCollectNoRecurse 验证只读当前层：不递归子目录、收 .txt/.md、不收其他格式。
func TestCollectNoRecurse(t *testing.T) {
	dir := t.TempDir()
	for _, nm := range []string{"a.txt", "b.txt", "c.txt", "g.md", "d.pdf", "e.epub", "f.mobi"} {
		if err := os.WriteFile(filepath.Join(dir, nm), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	sub := filepath.Join(dir, "子目录")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(filepath.Join(sub, fmt.Sprintf("s%d.txt", i)), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	ents, err := collectTxt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 5 {
		t.Fatalf("应收当前层 5 篇（3 txt + 1 md + 1 epub；不递归子目录、不收 pdf/mobi），got %d：%v", len(ents), ents)
	}
}

// TestPinyinSortOrder 验证中文按拼音序（而非 Unicode 码点序）：
// 龙(long) < 组(zu)，"组织部长"应排"龙城"之后；旧的码点序会把"龙"U+9F99 排到最后。
func TestPinyinSortOrder(t *testing.T) {
	names := []string{"龙城 杨川庆", "组织部长 许", "公安局长 张", "丑事 萧寒"}
	sort.Slice(names, func(i, j int) bool {
		return pinyinKey(names[i]) < pinyinKey(names[j])
	})
	if names[0] != "丑事 萧寒" {
		t.Fatalf("拼音序第一个应为『丑事』，got %q；序=%v", names[0], names)
	}
	if names[len(names)-1] != "组织部长 许" {
		t.Fatalf("拼音序最后一个应为『组织部长』，got %q；序=%v", names[len(names)-1], names)
	}
	if names[len(names)-2] != "龙城 杨川庆" {
		t.Fatalf("龙城应紧邻组织部长之前，got %q；序=%v", names[len(names)-2], names)
	}
}

// TestRealGuanchangPinyinSort 用真实书库目录验证拼音排序生效（仅在目录存在时跑）。
func TestRealGuanchangPinyinSort(t *testing.T) {
	dir := `E:\book\中国\小说\官场`
	if _, err := os.Stat(dir); err != nil {
		t.Skip("官场目录不存在，跳过")
	}
	ents, err := collectTxt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) == 0 {
		t.Fatal("collectTxt 返回空")
	}
	last := ents[len(ents)-1].name
	t.Logf("共 %d 个 txt，拼音序最后 5 个：", len(ents))
	for i := len(ents) - 5; i < len(ents); i++ {
		t.Logf("  [%d] %s", i+1, ents[i].name)
	}
	if strings.Contains(last, "龙城") {
		t.Fatalf("龙城仍是最后一个（拼音排序未生效），last=%q", last)
	}
}

// TestPinyinKeyKeepsNonChinese B3 回归：自定义 Fallback 后非汉字原样保留，
// 英文/数字前缀不再被丢弃导致同键排序退化。
func TestPinyinKeyKeepsNonChinese(t *testing.T) {
	if got := pinyinKey("hello.txt"); got != "hello.txt" {
		t.Errorf("pinyinKey(hello.txt)=%q, want \"hello.txt\"", got)
	}
	if pinyinKey("A文件.txt") == pinyinKey("Z文件.txt") {
		t.Error("英文前缀应参与排序键，A文件/Z文件 不应同键")
	}
	if got := pinyinKey("2008年.txt"); got != "2008nian.txt" {
		t.Errorf("pinyinKey(2008年.txt)=%q, want \"2008nian.txt\"", got)
	}
	if got := pinyinKey("龙城.txt"); got != "longcheng.txt" {
		t.Errorf("pinyinKey(龙城.txt)=%q, want \"longcheng.txt\"", got)
	}
}

// TestPinyinBracketSort N-A 回归：前导标点（书名号《等）不参与排序键，
// 《三国演义》按"三国演义"归位、与同拼音普通书名相邻，而不是沉到全列表底部。
func TestPinyinBracketSort(t *testing.T) {
	if !strings.HasPrefix(pinyinSortKey("《三国演义》.txt"), "sang") {
		t.Errorf("书名号应被剥离再转拼音，key=%q", pinyinSortKey("《三国演义》.txt"))
	}
	names := []string{"zoo.txt", "《安魂曲》.txt", "曹操传.txt", "《曹操传》.txt"}
	sort.Slice(names, func(i, j int) bool {
		return pinyinSortKey(names[i]) < pinyinSortKey(names[j])
	})
	want := []string{"《安魂曲》.txt", "曹操传.txt", "《曹操传》.txt", "zoo.txt"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("排序结果 %v，want %v（an < cao < zoo；书名号版紧邻普通版之后）", names, want)
		}
	}
}
