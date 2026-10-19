package reader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBigFilePaging 验证大文件（多页）逐页内容正确、能翻到末页、Last 定位正确。
// 每页用唯一页码前缀填充到 pageSize，避免连续同字符导致 s==prev 误判末页。
func TestBigFilePaging(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.txt")
	const pages = 30
	var sb strings.Builder
	for i := 0; i < pages; i++ {
		sb.WriteString(fmt.Sprintf("%03d", i))
		sb.WriteString(strings.Repeat("x", defaultPageRunes-3))
	}
	if err := os.WriteFile(p, []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}
	b, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	for i := 0; i < pages; i++ {
		cur := b.Current()
		if !strings.HasPrefix(cur, fmt.Sprintf("%03d", i)) {
			t.Fatalf("第%d页前缀=%q，want %03d", i, cur[:3], i)
		}
		if i < pages-1 {
			if _, err := b.Next(); err != nil {
				t.Fatal(err)
			}
		}
	}
	// 末页后再 Next 应原地不变
	last := b.Current()
	if s, _ := b.Next(); s != last {
		t.Fatal("末页后 Next 前进了（分页越界）")
	}
	// Last 应定位到真实末页
	l, err := b.Last()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(l, fmt.Sprintf("%03d", pages-1)) {
		t.Fatalf("Last 前缀=%q，want %03d", l[:3], pages-1)
	}
}
