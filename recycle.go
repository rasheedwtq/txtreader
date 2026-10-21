package main

import (
	"fmt"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	foDelete     = 0x0003
	fofAllowundo = 0x0040 // 进回收站（可逆）
	fofNoconfirm = 0x0010
	fofSilent    = 0x0004
	fofNoerrorui = 0x0400
)

// shfileopstructW 对应 Windows SHFileOperationW 的 SHFILEOPSTRUCTW（64 位布局）。
type shfileopstructW struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted uint32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

// recycleBin 把文件移入系统回收站（等价“删除走隔离区”，可逆可恢复）。
func recycleBin(path string) error {
	shell32 := windows.NewLazySystemDLL("shell32.dll")
	proc := shell32.NewProc("SHFileOperationW")

	// pFrom 需要以双 NULL 结尾
	runes := utf16.Encode([]rune(path + "\x00"))
	runes = append(runes, 0)
	p := &runes[0]

	op := shfileopstructW{
		wFunc:  foDelete,
		pFrom:  p,
		fFlags: fofAllowundo | fofNoconfirm | fofSilent | fofNoerrorui,
	}
	r, _, _ := proc.Call(uintptr(unsafe.Pointer(&op)))
	if r != 0 {
		return fmt.Errorf("移入回收站失败 (code %d): %s", r, path)
	}
	return nil
}
