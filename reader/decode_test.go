package reader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
)

func writeEncoded(t *testing.T, dir, name string, enc encoding.Encoding, text string) string {
	t.Helper()
	s, err := enc.NewEncoder().String(text)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUTF8CleanJunk(t *testing.T) {
	dir := t.TempDir()
	// BOM + 连续空行 + BEL 控制字符
	raw := "\uFEFF标题\n\n\n\n正文第一行\n第二行\t制表\n\n\n结尾\x07"
	p := writeEncoded(t, dir, "u.txt", unicode.UTF8, raw)
	b, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	want := "标题\n\n正文第一行\n第二行\t制表\n\n结尾"
	if got := b.Current(); got != want {
		t.Fatalf("clean mismatch:\n got=%q\nwant=%q", got, want)
	}
}

func TestGBKDecode(t *testing.T) {
	dir := t.TempDir()
	text := "中文测试内容：你好，世界！123 abc"
	p := writeEncoded(t, dir, "g.txt", simplifiedchinese.GBK, text)
	b, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if got := b.Current(); got != text {
		t.Fatalf("GBK decode mismatch:\n got=%q\nwant=%q", got, text)
	}
}

func TestUTF16LEDecode(t *testing.T) {
	dir := t.TempDir()
	text := "UTF-16 中文测试\n第二行内容"
	enc := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM)
	p := writeEncoded(t, dir, "w.txt", enc, text)
	b, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if got := b.Current(); got != text {
		t.Fatalf("UTF-16LE decode mismatch:\n got=%q\nwant=%q", got, text)
	}
}

func TestPaging(t *testing.T) {
	dir := t.TempDir()
	runes := make([]rune, 100000)
	for i := range runes {
		runes[i] = '中'
	}
	text := string(runes)
	p := writeEncoded(t, dir, "big.txt", unicode.UTF8, text)

	b, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if got := b.Current(); got != string(runes[0:20000]) {
		t.Fatalf("page0 length=%d, want 20000", len(got))
	}
	n, err := b.Next()
	if err != nil {
		t.Fatal(err)
	}
	if n != string(runes[20000:40000]) {
		t.Fatalf("page1 mismatch")
	}
	last, err := b.Last()
	if err != nil {
		t.Fatal(err)
	}
	if last != string(runes[80000:100000]) {
		t.Fatalf("last page mismatch")
	}
}

func TestCleanLogic(t *testing.T) {
	// 直接验证清洗规则
	in := "a\x00b\x01c\r\nd\n\n\n\n\n e \t\nf"
	// 控制字符去，\r\n 归一，连续空行合并，行尾空白去
	out := clean(in)
	if strings.Contains(out, "\x00") || strings.Contains(out, "\x01") {
		t.Fatalf("control chars not removed: %q", out)
	}
	if strings.Contains(out, "\r") {
		t.Fatalf("CR not normalized: %q", out)
	}
	if strings.Contains(out, "\n\n\n") {
		t.Fatalf("blank lines not collapsed: %q", out)
	}
}

// ---- 回归测试：第一轮 bug 分析修复 ----

// Bug1 回归：空文件 / 探测结果不在映射表（ISO-2022-JP）不得 panic。
func TestOpenEmptyFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if got := b.Current(); got != "" {
		t.Fatalf("empty file Current=%q, want \"\"", got)
	}
}

func TestUnknownCharsetFallbackNoPanic(t *testing.T) {
	dir := t.TempDir()
	text := "日本語のテキストです。これは ISO-2022-JP エンコードのテストです。"
	s, err := japanese.ISO2022JP.NewEncoder().String(text)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "jis.txt")
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if !strings.Contains(b.EncName(), "容错") {
		t.Fatalf("EncName=%q, want fallback label", b.EncName())
	}
}

// Bug2 回归：翻到末尾后 Next 原地返回当前页，不产生空白页。
func TestNextStaysAtEOF(t *testing.T) {
	dir := t.TempDir()
	// 恰好一整页（20000 rune），下一页不存在
	page0 := strings.Repeat("中", 20000)
	p := filepath.Join(dir, "exact.txt")
	if err := os.WriteFile(p, []byte(page0), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	s, err := b.Next()
	if err != nil {
		t.Fatal(err)
	}
	if s != page0 {
		t.Fatalf("Next at boundary must stay on page0, got len=%d", len(s))
	}
	s2, _ := b.Next()
	if s2 != page0 {
		t.Fatalf("Next again must stay, got len=%d", len(s2))
	}

	// 尾页半页：先正常翻到尾页，再验证原地
	p2 := filepath.Join(dir, "half.txt")
	full := strings.Repeat("中", 25000)
	if err := os.WriteFile(p2, []byte(full), 0o644); err != nil {
		t.Fatal(err)
	}
	b2, err := Open(p2)
	if err != nil {
		t.Fatal(err)
	}
	defer b2.Close()
	last, err := b2.Next()
	if err != nil {
		t.Fatal(err)
	}
	if last != strings.Repeat("中", 5000) {
		t.Fatalf("last page len=%d, want 5000", len(last))
	}
	again, _ := b2.Next()
	if again != last {
		t.Fatalf("Next past last page must stay, got len=%d", len(again))
	}
}

// Bug3 回归：Last 之后 Next 原地、Prev 回到真实上一页、再 Next 续读正常。
func TestLastThenPrevNext(t *testing.T) {
	dir := t.TempDir()
	text := strings.Repeat("A", 20000) + strings.Repeat("B", 20000) + strings.Repeat("C", 20000)
	p := filepath.Join(dir, "three.txt")
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	last, err := b.Last()
	if err != nil {
		t.Fatal(err)
	}
	if last != strings.Repeat("C", 20000) {
		t.Fatalf("Last mismatch")
	}
	n, err := b.Next()
	if err != nil {
		t.Fatal(err)
	}
	if n != last {
		t.Fatalf("Next after Last must stay on tail, got len=%d", len(n))
	}
	prev, err := b.Prev()
	if err != nil {
		t.Fatal(err)
	}
	if prev != strings.Repeat("B", 20000) {
		t.Fatalf("Prev after Last must be page1(B), got len=%d head=%q", len(prev), prev[:1])
	}
	n2, err := b.Next()
	if err != nil {
		t.Fatal(err)
	}
	if n2 != strings.Repeat("C", 20000) {
		t.Fatalf("Next after Prev must return to page2(C)")
	}
	// 首页 Prev 原地
	f, err := b.First()
	if err != nil {
		t.Fatal(err)
	}
	if f != strings.Repeat("A", 20000) {
		t.Fatalf("First mismatch")
	}
	p0, err := b.Prev()
	if err != nil {
		t.Fatal(err)
	}
	if p0 != f {
		t.Fatalf("Prev at first page must stay")
	}
}
