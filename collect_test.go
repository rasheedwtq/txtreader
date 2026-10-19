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
	ents := collectTxt(dir)
	if len(ents) != n {
		t.Fatalf("collectTxt 返回 %d 个，want %d（txt+md 混合）", len(ents), n)
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
	ents := collectTxt(dir)
	if len(ents) != 4 {
		t.Fatalf("应收当前层 4 篇（3 txt + 1 md；不递归子目录、不收 pdf/epub/mobi），got %d：%v", len(ents), ents)
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
	ents := collectTxt(dir)
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
