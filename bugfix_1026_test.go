package main

import (
	"strings"
	"testing"
	"unicode/utf16"
)

// TestRuneUTF16Roundtrip F3 回归（2026-10-26 探针实锤）：rune ↔ UTF-16 偏移
// 转换工具——EM_SETSEL/EM_LINEINDEX 是 UTF-16 口径，emoji 等非 BMP 字符每字
// 差 1 个 code unit，直接混用会让跳章/锚点恢复错位。
func TestRuneUTF16Roundtrip(t *testing.T) {
	cases := []struct {
		name string
		s    string
	}{
		{"纯中文", "第一章 起点\n正文内容"},
		{"emoji 前缀", "😀😀第一章 起点\n正文内容"},
		{"emoji 混排", "甲😀乙😀丙第一章"},
		{"增补平面汉字", "𠀀𠀁第一章"}, // CJK Ext-B
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rs := []rune(c.s)
			for _, runeOff := range []int{0, 1, 2, 3, len(rs)} {
				if runeOff > len(rs) {
					continue
				}
				u16 := runeToUTF16Off(c.s, runeOff)
				// 与直接编码对拍
				want := len(utf16.Encode(rs[:runeOff]))
				if u16 != want {
					t.Fatalf("runeToUTF16Off(%q, %d)=%d want %d", c.s, runeOff, u16, want)
				}
				// 往返：rune→UTF-16→rune（落在字符边界时必须还原）
				back := utf16ToRuneOff(c.s, u16)
				if back != runeOff {
					t.Fatalf("roundtrip(%q, %d): utf16=%d back=%d", c.s, runeOff, u16, back)
				}
			}
		})
	}
}

// TestUtf16ToRuneOffTruncation 转换撞上代理对中间时必须截到字符边界。
func TestUtf16ToRuneOffTruncation(t *testing.T) {
	s := "😀A" // 😀 = 2 UTF-16 units
	// u16=1 落在代理对中间 → 截到 0 个 rune
	if got := utf16ToRuneOff(s, 1); got != 0 {
		t.Fatalf("u16=1 应截到 0 rune，got %d", got)
	}
	if got := utf16ToRuneOff(s, 2); got != 1 {
		t.Fatalf("u16=2 应是 1 rune（完整 emoji），got %d", got)
	}
	if got := utf16ToRuneOff(s, 3); got != 2 {
		t.Fatalf("u16=3 应是 2 rune，got %d", got)
	}
}

// TestScrollCaretToTopEmoji F3 契约：scrollCaretToTop 调用方传 rune 口径，
// 内部转 UTF-16 后 EM_SETSEL 落点必须是标题首字（旧实现直接塞 rune 偏移，
// emoji 前缀下会落在 emoji 上）。窗口级行为由 TestScrollCaretToTop 覆盖。
func TestScrollCaretToTopEmoji(t *testing.T) {
	d := "😀😀第一章 起点\n正文"
	runeOff := len([]rune(d[:strings.Index(d, "第一章")]))
	u16 := runeToUTF16Off(d, runeOff)
	if u16 == runeOff {
		t.Fatal("用例无效：emoji 前缀下 rune/UTF-16 不应相等")
	}
	units := utf16.Encode([]rune(d))
	if string(utf16.Decode(units[u16:u16+2])) != "第一" {
		t.Fatalf("转换后落点应是'第一'，got %q", string(utf16.Decode(units[u16:u16+2])))
	}
	// captureView 反向：EM_LINEINDEX(u16) → rune，必须还原到「第一章」
	if got := utf16ToRuneOff(d, u16); got != runeOff {
		t.Fatalf("utf16ToRuneOff 反向应还原 runeOff=%d，got %d", runeOff, got)
	}
}
