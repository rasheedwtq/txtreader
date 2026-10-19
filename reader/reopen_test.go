package reader

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// TestReopenWith 验证编码兜底接口：重解码后编码名更新、游标回到首页、当前页非空。
func TestReopenWith(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	content := "第一章 你好 world\n第二行\n第三行\n" // 含非 ASCII，验证跨编码不崩溃
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	b, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.EncName() == "" {
		t.Fatal("enc name empty after open")
	}

	// 强制用 GBK 重解码（模拟自动探测出错后的兜底路径）
	if err := b.ReopenWith(simplifiedchinese.GBK, "GBK"); err != nil {
		t.Fatal(err)
	}
	if b.EncName() != "GBK" {
		t.Fatalf("enc name not updated, got %q", b.EncName())
	}
	if b.Page() != 0 {
		t.Fatalf("expected page 0 after reopen, got %d", b.Page())
	}
	if b.Current() == "" {
		t.Fatal("current empty after reopen")
	}
}
