package main

import (
	"encoding/json"
	"os"
	"testing"
)

// 2026-11-06 卫生项回归：saveState 的「临时文件 + rename」原子写。

func readStateRaw(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(statePathMust())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestSaveStateAtomicOverwrite 覆盖写必须整份替换、临时文件无残留、
// 每次落盘后 state.json 都是完整合法 JSON（不出现半截文件）。
func TestSaveStateAtomicOverwrite(t *testing.T) {
	stateDirOverride = t.TempDir()
	defer func() { stateDirOverride = "" }()
	p := statePathMust()

	for _, page := range []int{1, 7, 42, 999} {
		saveState(appState{CurrentPath: "book.txt", Page: page, FontFamily: "宋体"})
		got, ok := loadState()
		if !ok {
			t.Fatalf("page=%d 时 loadState 解析失败，说明留下了半截 JSON", page)
		}
		if got.Page != page || got.FontFamily != "宋体" {
			t.Fatalf("page=%d 覆盖写不完整：%+v", page, got)
		}
		var probe map[string]interface{}
		if err := json.Unmarshal(readStateRaw(t), &probe); err != nil {
			t.Fatalf("page=%d 落盘 JSON 不合法：%v", page, err)
		}
		if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
			t.Fatalf("page=%d 后临时文件 .tmp 残留", page)
		}
	}
}

// TestSaveStateRepairsStaleTmp 上次写一半被杀留下的损坏 .tmp 不能污染本次落盘。
func TestSaveStateRepairsStaleTmp(t *testing.T) {
	stateDirOverride = t.TempDir()
	defer func() { stateDirOverride = "" }()
	p := statePathMust()
	if err := os.WriteFile(p+".tmp", []byte(`{"page":`+"半截"), 0644); err != nil {
		t.Fatal(err)
	}
	// 旧的正本也应被本次写入完整替换
	if err := os.WriteFile(p, []byte(`{"page":3}`), 0644); err != nil {
		t.Fatal(err)
	}

	saveState(appState{CurrentPath: "x.txt", Page: 88, ViewAnchor: "锚点行"})
	got, ok := loadState()
	if !ok || got.Page != 88 || got.ViewAnchor != "锚点行" {
		t.Fatalf("原子写未修复陈旧 .tmp：ok=%v got=%+v", ok, got)
	}
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("陈旧 .tmp 未被清理")
	}
	var probe map[string]interface{}
	if err := json.Unmarshal(readStateRaw(t), &probe); err != nil {
		t.Fatalf("正本 JSON 不合法：%v", err)
	}
}

// TestSaveStateMissingDirFailsSilently 目录不存在时不 panic、不留垃圾文件。
func TestSaveStateMissingDirFailsSilently(t *testing.T) {
	stateDirOverride = t.TempDir() + "/不存在的子目录"
	defer func() { stateDirOverride = "" }()
	saveState(appState{Page: 5}) // 必须静默返回
	if _, ok := loadState(); ok {
		t.Fatal("目录不存在时不应读到状态")
	}
}
