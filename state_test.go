package main

import "testing"

// TestStateRoundTrip 验证状态保存→读取闭环：目录、书路径、页码都原样往返。
func TestStateRoundTrip(t *testing.T) {
	stateDirOverride = t.TempDir()
	defer func() { stateDirOverride = "" }()

	want := appState{LastDir: `D:\book\小说`, CurrentPath: `D:\book\小说\foo.txt`, Page: 7, FontFamily: "宋体", FontSize: 18, LineSpacing: 2}
	saveState(want)

	got, ok := loadState()
	if !ok {
		t.Fatal("loadState 应成功，但返回 false")
	}
	if got != want {
		t.Fatalf("往返不一致: want=%+v got=%+v", want, got)
	}
}

// TestLoadStateMissing 验证文件不存在时返回 (零值, false)，不报错。
func TestLoadStateMissing(t *testing.T) {
	stateDirOverride = t.TempDir()
	defer func() { stateDirOverride = "" }()

	if _, ok := loadState(); ok {
		t.Fatal("缺文件时应返回 false")
	}
}
