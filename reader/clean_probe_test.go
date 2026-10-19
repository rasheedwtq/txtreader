package reader

import "testing"

// TestCleanKeepsSingleBlankLine 验证 clean 把各种“连续空行”都压到至多 1 个空行，
// 并确认换页符 \f 被当作控制字符丢弃（不再残留成怪异空行）。
func TestCleanKeepsSingleBlankLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"3blank", "A\n\n\n\nB", "A\n\nB"},
		{"5blank", "A\n\n\n\n\nB", "A\n\nB"},
		{"spaces", "A\n \n \nB", "A\n\nB"},
		{"tabs", "A\n\t\n\t\nB", "A\n\nB"},
		{"mixed", "A\r\n\r\n\r\nB", "A\n\nB"},
		{"formfeed", "A\n\f\nB", "A\n\nB"}, // \f 修复：丢弃后段间归一为 1 空行
		{"para2", "A\n\n\nB", "A\n\nB"},
	}
	for _, c := range cases {
		if got := clean(c.in); got != c.want {
			t.Errorf("[%s]\n in  =%q\n want=%q\n got =%q", c.name, c.in, c.want, got)
		}
	}
}
