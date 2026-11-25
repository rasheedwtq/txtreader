package reader

// H5（2026-11-25 审查）：无 BOM UTF-16 误判回归 + 正常编码不受影响的护栏。

import (
	"strings"
	"testing"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/simplifiedchinese"
	xunicode "golang.org/x/text/encoding/unicode"
)

// TestH5UTF16NoBOMDetected 无 BOM 的 UTF-16 中文必须判对字节序并正确解码。
func TestH5UTF16NoBOMDetected(t *testing.T) {
	text := "第一章 风起\n他走进屋子，看见桌上放着一本书，封面上写着三个字。\n“你终于来了。”老人说道。"
	cases := []struct {
		name string
		enc  encoding.Encoding
		want string
	}{
		{"UTF16LE 无BOM", xunicode.UTF16(xunicode.LittleEndian, xunicode.IgnoreBOM), "UTF-16LE"},
		{"UTF16BE 无BOM", xunicode.UTF16(xunicode.BigEndian, xunicode.IgnoreBOM), "UTF-16BE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := writeEncoded(t, t.TempDir(), "u16.txt", c.enc, text)
			b, err := Open(p)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			if b.EncName() != c.want {
				t.Fatalf("EncName=%q want %q（状态栏/编码对话框要靠它回显）", b.EncName(), c.want)
			}
			if got := b.Current(); got != text {
				t.Fatalf("解码不对：\n got=%q\nwant=%q", got, text)
			}
		})
	}
}

// TestH5NormalEncodingsUnchanged 护栏：UTF-8 / GBK / 拉丁文 / ASCII 的判定与文本
// 必须与修前一致（这些样本都不含 NUL，不会进 UTF-16 嗅探分支）。
func TestH5NormalEncodingsUnchanged(t *testing.T) {
	zh := "中文测试内容：你好，世界！123 abc"
	en := "The quick brown fox — café naïve résumé."
	cases := []struct {
		name     string
		text     string
		enc      encoding.Encoding
		wantName string // 空串 = 只验文本，不锁具体标签（GBK/GB18030 都可能）
	}{
		{"UTF-8", zh, xunicode.UTF8, "UTF-8"},
		{"GBK", zh, simplifiedchinese.GBK, ""},
		{"Windows-1252", en, charmap.Windows1252, "windows-1252"},
		{"ASCII", "plain ascii text only, no high bytes.", xunicode.UTF8, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := writeEncoded(t, t.TempDir(), "book.txt", c.enc, c.text)
			b, err := Open(p)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			if got := b.Current(); got != c.text {
				t.Fatalf("解码变了：\n got=%q\nwant=%q", got, c.text)
			}
			if strings.Contains(b.EncName(), "UTF-16") {
				t.Fatalf("%s 被误判成 UTF-16：EncName=%q", c.name, b.EncName())
			}
			if c.wantName != "" && b.EncName() != c.wantName {
				t.Fatalf("%s EncName=%q want %q", c.name, b.EncName(), c.wantName)
			}
		})
	}
}
