package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	"txtreader/reader"
)

// 2026-11-06 回归：G1（恢复后落盘锚点）/ G2（两次复核真正生效）/
// G3（控件级滚动作废 + 代次隔离）。
//
// 关键：walk 的 Synchronize 回调只在 w.Run() 的消息循环里执行，隐藏窗口
// 且无消息循环的测试一律假绿（10-30 教训）。需要观察延时回调的用例都跑
// 真实消息循环，用 EM_GETSEL 的 caret 作"是否被复位"的可观测代理。

const fix1106Anchor = "锚点行：灯下看剑"

func fix1106Caret() uint32 {
	var s, e uint32
	te.SendMessage(win.EM_GETSEL, uintptr(unsafe.Pointer(&s)), uintptr(unsafe.Pointer(&e)))
	return s
}

// fix1106Fixture 建隐藏窗口 + 真实 Book。返回 (窗口, 书路径, cleanup)。
// 不调 openAt：避免 showTOC 的后台协程在 mw != nil 时排队干扰。
func fix1106Fixture(t *testing.T, withWindow bool) (*walk.MainWindow, string, func()) {
	t.Helper()
	runtime.LockOSThread()
	oldMW, oldTE, oldLB, oldLbl, oldBook := mw, te, lb, folderLbl, curBook
	oldTocLB, oldEntries, oldDir := tocLB, curEntries, curDir
	oldState, oldPending, oldPath, oldGen := lastState, pendingAnchor, pendingAnchorPath, reanchorGen
	oldHook, oldCB, oldSuppress := scrollHookOn, scrollSubclassCB, suppressScrollInvalidate
	oldFamily, oldSize, oldSpacing := curFamily, curSize, curSpacing

	mw, tocLB = nil, nil
	pendingAnchor, pendingAnchorPath, suppressScrollInvalidate = "", "", false
	lastState = appState{}

	var w *walk.MainWindow
	if withWindow {
		if err := (MainWindow{
			AssignTo: &w,
			Visible:  false,
			Size:     Size{Width: 500, Height: 400},
			Layout:   VBox{},
			Children: []Widget{
				Label{AssignTo: &folderLbl, Text: ""},
				ListBox{AssignTo: &lb, Model: []string{}},
				TextEdit{AssignTo: &te, VScroll: true, ReadOnly: true, MinSize: Size{Width: 460, Height: 360}},
			},
		}).Create(); err != nil {
			t.Skipf("无法创建窗口：%v", err)
		}
		mw = w
	}
	stateDirOverride = t.TempDir()
	dir := t.TempDir()
	p := filepath.Join(dir, "book.txt")
	content := strings.Repeat("甲", 20000) +
		strings.Repeat("乙", 10000) + fix1106Anchor + "\n" +
		strings.Repeat("乙", 9900) + strings.Repeat("丙", 5000)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	b, err := reader.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	curBook = b
	curEntries = []entry{{name: "book.txt", path: p}}
	curDir = dir

	return w, p, func() {
		if curBook != nil {
			curBook.Close()
		}
		uninstallScrollHook()
		scrollHookOn, scrollSubclassCB = oldHook, oldCB
		if w != nil && w.Handle() != 0 {
			w.Dispose()
		}
		mw, te, lb, folderLbl, curBook = oldMW, oldTE, oldLB, oldLbl, oldBook
		tocLB, curEntries, curDir = oldTocLB, oldEntries, oldDir
		lastState, pendingAnchor, pendingAnchorPath, reanchorGen = oldState, oldPending, oldPath, oldGen
		suppressScrollInvalidate = oldSuppress
		curFamily, curSize, curSpacing = oldFamily, oldSize, oldSpacing
		stateDirOverride = ""
	}
}

// fix1106SetAnchorText 把阅读区换成一段锚点不在开头的固定文本，返回锚点 UTF-16 偏移。
func fix1106SetAnchorText(t *testing.T) uint32 {
	t.Helper()
	prefix := strings.Repeat("甲", 300) + "\r\n"
	d := prefix + fix1106Anchor + "\r\n" + strings.Repeat("乙", 300)
	te.SetText(d)
	te.SendMessage(win.EM_SETSEL, 7, 7)
	return uint32(runeToUTF16Off(d, len([]rune(prefix))))
}

// fix1106RunLoop 跑真实消息循环：按绝对时间点入队若干主线程采样/操作，最后关窗。
func fix1106RunLoop(w *walk.MainWindow, closeAt time.Duration, steps map[time.Duration]func()) {
	for at, fn := range steps {
		at, fn := at, fn
		go func() {
			time.Sleep(at)
			w.Synchronize(fn)
		}()
	}
	go func() {
		time.Sleep(closeAt)
		win.PostMessage(w.Handle(), win.WM_CLOSE, 0, 0)
	}()
	w.Run()
}

// ---- G1 ----

// TestG1RestoreKeepsAnchorOnDisk 恢复完成后 state.json 的 ViewAnchor 必须
// 仍是原锚点（此前 showCurrent 的 persist 在视口顶部把它写成页首 24 字）。
func TestG1RestoreKeepsAnchorOnDisk(t *testing.T) {
	_, p, cleanup := fix1106Fixture(t, true)
	defer cleanup()
	mw = nil // 不启动延时回调 goroutine，本例只验落盘链路
	loadFolderOpen(filepath.Dir(p), p)
	if curBook == nil {
		t.Fatal("打开失败")
	}
	s, more, err := curBook.LoadNextPage()
	if err != nil || !more {
		t.Fatalf("应还有第 1 页：more=%v err=%v", more, err)
	}
	appendPage(s)
	d := displayText(strings.Join(accumBuf, pageJoin()))
	bi := strings.Index(d, fix1106Anchor)
	if bi < 0 {
		t.Fatal("锚点不在显示文本里")
	}
	scrollCaretToTop(d, len([]rune(d[:bi])))
	curFamily, curSize, curSpacing = "宋体", 18, 2
	persist()
	before, _ := loadState()
	if before.ViewAnchor != fix1106Anchor {
		t.Fatalf("前置条件不成立：锚点没存上，got %q", before.ViewAnchor)
	}

	// 模拟重启 + CLI 双击同一本
	curBook.Close()
	curBook = nil
	lastState, _ = loadState()
	oldArgs := os.Args
	os.Args = []string{"txtreader.exe", p}
	defer func() { os.Args = oldArgs }()
	if !openCLIPath() {
		t.Fatal("CLI 应接管启动")
	}
	after, _ := loadState()
	if after.ViewAnchor != fix1106Anchor {
		t.Fatalf("G1 回归：恢复动作把落盘锚点写坏了，want %q got %q", fix1106Anchor, after.ViewAnchor)
	}
	if after.ViewPage != before.ViewPage || after.FontFamily != before.FontFamily ||
		after.FontSize != before.FontSize || after.LineSpacing != before.LineSpacing {
		t.Fatalf("G1 回归：补落盘改动了其它状态字段：before=%+v after=%+v", before, after)
	}
}

// ---- G2 ----

// TestG2SecondReanchorAlive 第一次复核后 pendingAnchor 必须仍在，且 1200ms
// 的第二次复核真的会再次对齐（此前第一次回调末尾清空 → 第二次必然 no-op）。
func TestG2SecondReanchorAlive(t *testing.T) {
	w, _, cleanup := fix1106Fixture(t, true)
	defer cleanup()
	want := fix1106SetAnchorText(t)
	installScrollHook()

	var mu sync.Mutex
	var midPending string
	var midGen uint64
	var endCaret uint32
	var endPending string
	var endGen uint64

	scheduleViewReanchor(fix1106Anchor)
	fix1106RunLoop(w, 1900*time.Millisecond, map[time.Duration]func(){
		600 * time.Millisecond: func() {
			mu.Lock()
			midPending, midGen = pendingAnchor, reanchorGen
			mu.Unlock()
			// 模拟首次显示后的第二次布局冲掉视口
			te.SendMessage(win.EM_SETSEL, 42, 42)
		},
		1400 * time.Millisecond: func() {
			mu.Lock()
			endCaret, endPending, endGen = fix1106Caret(), pendingAnchor, reanchorGen
			mu.Unlock()
		},
		1800 * time.Millisecond: func() { uninstallScrollHook() },
	})

	mu.Lock()
	mp, mg, ec, ep, eg := midPending, midGen, endCaret, endPending, endGen
	mu.Unlock()
	t.Logf("t=600ms pendingAnchor=%q gen=%d；t=1400ms caret=%d(want=%d) pendingAnchor=%q gen=%d",
		mp, mg, ec, want, ep, eg)
	if mp != fix1106Anchor {
		t.Fatalf("G2 回归：第一次复核后锚点被清空，第二次复核必成 no-op：%q", mp)
	}
	if ec != want {
		t.Fatalf("G2 回归：1200ms 的第二次复核没有再次对齐：caret=%d want=%d", ec, want)
	}
	if ep != "" || eg == mg {
		t.Fatalf("G2 回归：复核周期结束后未释放锚点/未推进代次：pending=%q gen %d→%d", ep, mg, eg)
	}
}

// ---- G3 ----

// TestG3ScrollHookInvalidates 控件级滚轮/滚动条消息必须作废在途复位
// （真实消息循环探针 S3 实锤过的干扰路径）。
func TestG3ScrollHookInvalidates(t *testing.T) {
	w, _, cleanup := fix1106Fixture(t, true)
	defer cleanup()
	want := fix1106SetAnchorText(t)
	installScrollHook()
	if !scrollHookOn {
		t.Skip("comctl32 SetWindowSubclass 不可用（降级环境），跳过控件级断言")
	}

	var mu sync.Mutex
	var endCaret uint32
	var endPending string
	scheduleViewReanchor(fix1106Anchor)
	fix1106RunLoop(w, 1500*time.Millisecond, map[time.Duration]func(){
		200 * time.Millisecond: func() {
			// 用户滚轮/拖滚动条：消息直达 EDIT，不经过任何 Go 函数入口
			te.SendMessage(win.WM_VSCROLL, uintptr(win.SB_LINEDOWN), 0)
			te.SendMessage(win.EM_SETSEL, 42, 42)
		},
		900 * time.Millisecond: func() {
			mu.Lock()
			endCaret, endPending = fix1106Caret(), pendingAnchor
			mu.Unlock()
		},
		1400 * time.Millisecond: func() { uninstallScrollHook() },
	})
	mu.Lock()
	ec, ep := endCaret, endPending
	mu.Unlock()
	t.Logf("用户滚动后 t=900ms caret=%d（用户位置 42，锚点 %d）pendingAnchor=%q", ec, want, ep)
	if ec != 42 {
		t.Fatalf("G3 回归：用户滚动后仍被延时复位拽回锚点：caret=%d want=42", ec)
	}
	if ep != "" {
		t.Fatalf("G3 回归：控件钩子没有作废在途复位：pendingAnchor=%q", ep)
	}
}

// TestG3FunctionEntriesInvalidate 所有显式跳转/重渲染入口统一走 invalidateReanchor。
func TestG3FunctionEntriesInvalidate(t *testing.T) {
	_, _, cleanup := fix1106Fixture(t, true)
	defer cleanup()
	for name, fn := range map[string]func(){
		"pageFirst":   pageFirst,
		"pageLast":    pageLast,
		"pageNext":    pageNext,
		"pagePrev":    pagePrev,
		"showCurrent": showCurrent,
	} {
		pendingAnchor, pendingAnchorPath, reanchorGen = "占位锚点", "p", 10
		fn()
		if pendingAnchor != "" || pendingAnchorPath != "" || reanchorGen == 10 {
			t.Fatalf("G3 回归：%s 没有作废在途复位：pending=%q path=%q gen=%d",
				name, pendingAnchor, pendingAnchorPath, reanchorGen)
		}
	}
}

// TestG3GenerationIsolatesOldCallback 换书/作废后，旧 goroutine 的待执行
// 回调必须被代次校验挡掉，不能干扰用户后续操作。
func TestG3GenerationIsolatesOldCallback(t *testing.T) {
	w, _, cleanup := fix1106Fixture(t, true)
	defer cleanup()
	want := fix1106SetAnchorText(t)

	var mu sync.Mutex
	var endCaret uint32
	scheduleViewReanchor(fix1106Anchor)
	fix1106RunLoop(w, 1500*time.Millisecond, map[time.Duration]func(){
		100 * time.Millisecond: func() { invalidateReanchor() }, // 模拟 openAt 换书
		200 * time.Millisecond: func() {
			te.SendMessage(win.EM_SETSEL, 42, 42)
		},
		900 * time.Millisecond: func() {
			mu.Lock()
			endCaret = fix1106Caret()
			mu.Unlock()
		},
	})
	mu.Lock()
	ec := endCaret
	mu.Unlock()
	if ec != 42 {
		t.Fatalf("G3 回归：作废后旧回调仍复位了视口：caret=%d want=42（锚点 %d）", ec, want)
	}
}

// TestG3ProgrammaticAlignDoesNotSelfInvalidate scrollCaretToTop 发的
// WM_VSCROLL 不能被控件钩子当成用户滚动，否则第一次复核会掐断第二次。
func TestG3ProgrammaticAlignDoesNotSelfInvalidate(t *testing.T) {
	_, _, cleanup := fix1106Fixture(t, true)
	defer cleanup()
	want := fix1106SetAnchorText(t)
	installScrollHook()
	if !scrollHookOn {
		t.Skip("comctl32 SetWindowSubclass 不可用（降级环境），跳过")
	}
	defer uninstallScrollHook()
	pendingAnchor, pendingAnchorPath, reanchorGen = fix1106Anchor, "p", 1
	d := te.Text()
	bi := strings.Index(d, fix1106Anchor)
	if bi < 0 {
		t.Fatal("锚点不在测试文本里")
	}
	scrollCaretToTop(d, len([]rune(d[:bi])))
	if pendingAnchor != fix1106Anchor {
		t.Fatal("G3 回归：程序化对齐把自己作废了")
	}
	if got := fix1106Caret(); got != want {
		t.Fatalf("程序化对齐未落到锚点：caret=%d want=%d", got, want)
	}
}
