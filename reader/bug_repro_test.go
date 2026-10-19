package reader

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	xunicode "golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// TestUTF8DecoderStrictness 直接验证 x/text UTF-8 decoder 对无效字节是否报错。
func TestUTF8DecoderStrictness(t *testing.T) {
	r := transform.NewReader(bytes.NewReader([]byte{0xC4, 0xE3, 0xBA, 0xC3, 0xFF, 0xFE}),
		xunicode.UTF8.NewDecoder())
	out, err := io.ReadAll(r)
	t.Logf("UTF8 解 GBK 字节(你/好+0xFFFF): out=%q err=%v", out, err)
	if err == nil {
		t.Logf("→ decoder 容错（不报错），翻页不会因此中断")
	} else {
		t.Logf("→ decoder 严格报错！编码误判时翻页会中断（Bug2 根因）")
	}
}

// TestNextErrOnBadBytes 复现：前半合法 UTF-8（Open 判 UTF-8 成功），后半掺无效字节，
// 翻页到含无效字节的页时 Next 是否返回 err（main 层会静默忽略 → 翻不动）。
func TestNextErrOnBadBytes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mixed.txt")
	var buf bytes.Buffer
	// 第一页：合法 UTF-8 中文，确保 Open 成功且倾向判 UTF-8
	buf.WriteString(strings.Repeat("这是合法中文内容。", 1200)) // ~7200 rune
	// 掺入 GBK 常见高位字节（非法 UTF-8），模拟编码误判场景
	buf.Write([]byte{0xC4, 0xE3, 0xBA, 0xC3, 0xFF, 0xFE})
	buf.WriteString(strings.Repeat("后续正文内容。", 1200))
	if err := os.WriteFile(p, buf.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	b, err := Open(p)
	if err != nil {
		t.Fatalf("Open err: %v", err)
	}
	defer b.Close()
	t.Logf("Open 探测编码=%s", b.EncName())
	for i := 0; i < 6; i++ {
		s, err := b.Next()
		if err != nil {
			t.Fatalf("第%d 次 Next 返回 err=%v —— 复现 Bug2：翻页中断", i, err)
		}
		_ = s
	}
	t.Logf("翻页未中断")
}
