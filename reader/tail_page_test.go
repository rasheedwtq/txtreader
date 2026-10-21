package reader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNextNoEmptyTailPage N-B 回归：文件末尾恰以空白结尾（页边界落在 \r\n 上）时，
// Next 的"已到末页"判断必须在 clean 之后——否则会缓存一个空页，
// 累积阅读里多出一段无意义空白。defaultPageRunes=20000，构造整页 + 纯空白尾巴。
func TestNextNoEmptyTailPage(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tail.txt")
	content := strings.Repeat("甲", defaultPageRunes) + "\r\n\r\n"
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	b, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if b.Page() != 0 {
		t.Fatalf("打开后应在第 0 页，got %d", b.Page())
	}
	s, more, err := b.LoadNextPage()
	if err != nil {
		t.Fatal(err)
	}
	if more {
		t.Fatalf("末尾空白不应产生新页（more=true, page=%q）", s)
	}
	if b.Page() != 0 {
		t.Fatalf("游标不应前进，got page=%d", b.Page())
	}
}
