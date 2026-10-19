package main

import "testing"

// TestApplySpacing 断言行距对“段间空行”和“段内行间”的处理，
// 且对中文（段内无单 \n）零影响、对英文硬换行（段间无 \n\n）也生效。
func TestApplySpacing(t *testing.T) {
	cases := []struct {
		name string
		sp   int
		in   string
		want string
	}{
		// 标准(1)：完全保持（中文观感不变的前提）
		{"std_cn", 1, "甲\n\n乙\n\n丙", "甲\n\n乙\n\n丙"},
		{"std_en_hard", 1, "a\nb\nc", "a\nb\nc"},
		// 紧凑(0)：段间空行压成单换行，段内不变
		{"compact_cn", 0, "甲\n\n乙", "甲\n乙"},
		// 宽松(2)：段间 2 空行、段内行后 1 空行
		{"loose_cn", 2, "甲\n\n乙", "甲\n\n\n乙"},
		{"loose_en_hard", 2, "a\nb\nc", "a\n\nb\n\nc"},
		{"loose_en_para", 2, "a\nb\n\nc\nd", "a\n\nb\n\n\nc\n\nd"},
		// 超宽(3)：段间 3 空行、段内行后 2 空行
		{"wide_cn", 3, "甲\n\n乙", "甲\n\n\n\n乙"},
		{"wide_en_hard", 3, "a\nb", "a\n\n\nb"},
	}
	for _, c := range cases {
		curSpacing = c.sp
		if got := applySpacing(c.in); got != c.want {
			t.Errorf("[%s] sp=%d\n in  =%q\n want=%q\n got =%q", c.name, c.sp, c.in, c.want, got)
		}
	}
}
