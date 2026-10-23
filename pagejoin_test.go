package main

import "testing"

// TestPageJoinMDExempt B5 回归：空行全收全局开启时，.md 的页间分隔符仍须是
// "\n\n"——md 被 effectiveClean 强制 BlankAll=false（空行是块语义），
// pageJoin 若只看全局开关，md 页界会丢空行、行距 2/3 档还会把页界当段内行处理。
func TestPageJoinMDExempt(t *testing.T) {
	saved := curClean
	defer func() { curClean = saved }()

	curClean.BlankAll = true

	curIsMD = true
	if got := pageJoin(); got != "\n\n" {
		t.Fatalf("md 页间分隔符应为 \\\\n\\\\n，got %q", got)
	}
	curIsMD = false
	if got := pageJoin(); got != "\n" {
		t.Fatalf("txt 空行全收下页间分隔符应为 \\\\n，got %q", got)
	}

	curClean.BlankAll = false
	if got := pageJoin(); got != "\n\n" {
		t.Fatalf("txt 非全收下页间分隔符应为 \\\\n\\\\n，got %q", got)
	}
}
