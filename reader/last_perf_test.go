package reader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLastPerf 测 Last()（全文件扫描定位末页）对大文件的耗时，判断是否需页偏移索引。
func TestLastPerf(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big20.txt")
	var sb strings.Builder
	for sb.Len() < 20<<20 { // ~20MB
		sb.WriteString(strings.Repeat("中文正文内容测试句子。", 200))
		sb.WriteString("\n")
	}
	if err := os.WriteFile(p, []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}
	b, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	t0 := time.Now()
	if _, err := b.Last(); err != nil {
		t.Fatal(err)
	}
	t.Logf("Last() ~20MB 耗时 %v", time.Since(t0))
}
