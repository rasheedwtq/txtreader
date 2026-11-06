package main

import (
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

// 控件级滚动作废（G3，2026-11-06）。
//
// 背景：用户滚轮 / 拖滚动条 / 方向键翻页产生的 WM_MOUSEWHEEL / WM_VSCROLL /
// WM_KEYDOWN 是直达 EDIT 控件的，不经过 pageNext / pagePrev / tocJump 等 Go
// 函数入口。只在函数入口清 pendingAnchor 会漏掉这些最高频操作，延时复核就会
// 把用户刚滚到的位置拽回锚点（真实消息循环探针 S3 实锤）。
//
// 实现用 comctl32 的 SetWindowSubclass 而不是直接改 GWLP_WNDPROC：前者由系统
// 维护子类链，窗口销毁时自动移除，不会因忘记还原而崩溃；DefSubclassProc 负责
// 把消息交回 walk 的窗口过程。回调由 EDIT 的窗口过程在 UI 线程同步调用，与
// invalidateReanchor 的其它调用点同线程，无需加锁。
//
// 降级：老系统找不到 subclass API 时静默不装，函数入口的作废仍然生效，
// 不会因钩子缺失影响启动或阅读。

const scrollSubclassID = 0x54525458 // "TRTX"，任意非零 ID

var (
	comctl32DLL        = windows.NewLazySystemDLL("comctl32.dll")
	procSetSubclass    = comctl32DLL.NewProc("SetWindowSubclass")
	procRemoveSubclass = comctl32DLL.NewProc("RemoveWindowSubclass")
	procDefSubclass    = comctl32DLL.NewProc("DefSubclassProc")

	// scrollSubclassCB 必须全局保活：NewCallback 返回的是 Go 闭包蹦床地址，
	// 一旦被 GC 回收，控件收到消息就会跳到无效地址。
	scrollSubclassCB uintptr
	scrollHookOn     bool

	// suppressScrollInvalidate：scrollCaretToTop 等程序化对齐会发 WM_VSCROLL，
	// 不能让钩子把它当成用户滚动而作废掉 reanchor 自己（否则第二次复核会被
	// 自己发出的消息掐断，G2 白修）。只在对齐期间置位，UI 线程同步。
	suppressScrollInvalidate bool
)

// installScrollHook 在 te 创建完成后调用（main 里 builder.Create() 之后）。
func installScrollHook() {
	if te == nil || scrollHookOn {
		return
	}
	if err := procSetSubclass.Find(); err != nil {
		return
	}
	if err := procDefSubclass.Find(); err != nil {
		return
	}
	scrollSubclassCB = windows.NewCallback(scrollSubclassProc)
	r, _, _ := procSetSubclass.Call(uintptr(te.Handle()), scrollSubclassCB, scrollSubclassID, 0)
	scrollHookOn = r != 0
}

// uninstallScrollHook 关窗时显式摘钩子（comctl32 在窗口销毁时也会自动清理，
// 这里是显式收口，避免窗口复用场景下重复安装）。
func uninstallScrollHook() {
	if !scrollHookOn || te == nil {
		return
	}
	if err := procRemoveSubclass.Find(); err == nil {
		procRemoveSubclass.Call(uintptr(te.Handle()), scrollSubclassCB, scrollSubclassID)
	}
	scrollHookOn = false
}

// scrollSubclassProc 匹配 SUBCLASSPROC：
// (HWND, UINT, WPARAM, LPARAM, UINT_PTR, DWORD_PTR) -> LRESULT。
// 参数一律 uintptr，交给 NewCallback 生成蹦床。
func scrollSubclassProc(hwnd, msg, wParam, lParam, uIDSubclass, dwRefData uintptr) uintptr {
	if !suppressScrollInvalidate {
		switch uint32(msg) {
		case win.WM_VSCROLL, win.WM_MOUSEWHEEL, win.WM_KEYDOWN:
			invalidateReanchor()
		}
	}
	r, _, _ := procDefSubclass.Call(hwnd, msg, wParam, lParam)
	return r
}
