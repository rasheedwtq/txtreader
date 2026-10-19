package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// browseinfoW 对应 Windows SHBrowseForFolderW 的 BROWSEINFO 结构（64 位布局）。
type browseinfoW struct {
	hwndOwner      uintptr
	pidlRoot       uintptr
	pszDisplayName *uint16
	lpszTitle      *uint16
	ulFlags        uint32
	lpfn           uintptr
	lParam         uintptr
	iImage         int32
}

const bifReturnonlyfsdirs = 0x0001

// chooseFolder 弹出系统文件夹选择对话框，返回所选目录；取消则返回空串。
func chooseFolder() string {
	title, _ := windows.UTF16PtrFromString("选择要阅读的 txt 文件夹")
	bi := browseinfoW{
		ulFlags:   bifReturnonlyfsdirs,
		lpszTitle: title,
	}
	shell32 := windows.NewLazySystemDLL("shell32.dll")
	procB := shell32.NewProc("SHBrowseForFolderW")
	procG := shell32.NewProc("SHGetPathFromIDListW")

	ret, _, _ := procB.Call(uintptr(unsafe.Pointer(&bi)))
	if ret == 0 {
		return ""
	}
	buf := make([]uint16, 260)
	r, _, _ := procG.Call(ret, uintptr(unsafe.Pointer(&buf[0])))
	// 释放 SHBrowseForFolder 分配的 PIDL
	windows.NewLazySystemDLL("ole32.dll").NewProc("CoTaskMemFree").Call(ret)
	if r == 0 {
		return ""
	}
	return windows.UTF16ToString(buf)
}
