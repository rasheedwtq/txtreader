package main

import (
	"os"
	"testing"
)

// TestStateRoundTrip 验证状态保存→读取闭环：目录、书路径、页码都原样往返。
func TestStateRoundTrip(t *testing.T) {
	stateDirOverride = t.TempDir()
	defer func() { stateDirOverride = "" }()

	want := appState{
		LastDir: `D:\book\小说`, CurrentPath: `D:\book\小说\foo.txt`, Page: 7,
		FontFamily: "宋体", FontSize: 18, LineSpacing: 2,
		CleanIndent: boolPtr(false), CleanAd: boolPtr(true), CleanBlankAll: boolPtr(true),
		WinX: 120, WinY: 80, WinW: 1280, WinH: 860, WinMax: true,
	}
	saveState(want)

	got, ok := loadState()
	if !ok {
		t.Fatal("loadState 应成功，但返回 false")
	}
	// 含指针字段不能整结构体比较（JSON 往返后指针地址不同），逐字段解引用比较
	if got.LastDir != want.LastDir || got.CurrentPath != want.CurrentPath || got.Page != want.Page ||
		got.FontFamily != want.FontFamily || got.FontSize != want.FontSize || got.LineSpacing != want.LineSpacing {
		t.Fatalf("标量字段往返不一致: want=%+v got=%+v", want, got)
	}
	if got.WinX != want.WinX || got.WinY != want.WinY || got.WinW != want.WinW ||
		got.WinH != want.WinH || got.WinMax != want.WinMax {
		t.Fatalf("窗口字段往返不一致: want=%+v got=%+v", want, got)
	}
	if !eqBoolPtr(got.CleanIndent, want.CleanIndent) || !eqBoolPtr(got.CleanAd, want.CleanAd) || !eqBoolPtr(got.CleanBlankAll, want.CleanBlankAll) {
		t.Fatalf("清洗偏好字段往返不一致: want=%v/%v/%v got=%v/%v/%v",
			*want.CleanIndent, *want.CleanAd, *want.CleanBlankAll,
			deref(got.CleanIndent), deref(got.CleanAd), deref(got.CleanBlankAll))
	}
	if got.CleanIndent == nil || *got.CleanIndent != false ||
		got.CleanAd == nil || *got.CleanAd != true ||
		got.CleanBlankAll == nil || *got.CleanBlankAll != true {
		t.Fatal("清洗偏好指针字段值不一致")
	}
}

func eqBoolPtr(a, b *bool) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a == nil || *a == *b
}

func deref(b *bool) string {
	if b == nil {
		return "<nil>"
	}
	if *b {
		return "true"
	}
	return "false"
}

// TestLoadStateLegacyNoCleanFields 旧版状态文件（无清洗字段）→ 三个指针为 nil，
// restoreState 据此保持默认 A1/B1/E1 全开，不误判为"用户关了"。
func TestLoadStateLegacyNoCleanFields(t *testing.T) {
	stateDirOverride = t.TempDir()
	defer func() { stateDirOverride = "" }()

	legacy := `{"last_dir":"D:\\book","current_path":"","page":0,"font_family":"微软雅黑","font_size":14,"line_spacing":1}`
	if err := os.WriteFile(statePathMust(), []byte(legacy), 0644); err != nil {
		t.Fatal(err)
	}
	got, ok := loadState()
	if !ok {
		t.Fatal("旧版状态文件应可读")
	}
	if got.CleanIndent != nil || got.CleanAd != nil || got.CleanBlankAll != nil {
		t.Fatalf("旧文件缺字段应为 nil，got %+v", got)
	}
}

func statePathMust() string {
	p, err := statePath()
	if err != nil {
		panic(err)
	}
	return p
}

// TestLoadStateMissing 验证文件不存在时返回 (零值, false)，不报错。
func TestLoadStateMissing(t *testing.T) {
	stateDirOverride = t.TempDir()
	defer func() { stateDirOverride = "" }()

	if _, ok := loadState(); ok {
		t.Fatal("缺文件时应返回 false")
	}
}
