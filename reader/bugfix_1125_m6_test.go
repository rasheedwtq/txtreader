package reader

// M6（2026-11-25 审查）：Open 在 openBook 失败路径必须关闭文件句柄。
// Windows 上 os.Open(目录) 成功、Read 失败，正好拿来构造稳定失败样本。

import (
	"os"
	"testing"
)

func TestM6FailedOpenClosesHandle(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Open(dir)
	if err != nil {
		t.Skipf("本平台目录不可打开，构造不出失败样本：%v", err)
	}
	// 先确认这个平台的目录读会失败；能读的话本测没有意义，直接跳过。
	if n, rerr := f.Read(make([]byte, 8)); rerr == nil || n > 0 {
		f.Close()
		t.Skip("本平台允许读目录，构造不出失败样本")
	}

	if _, err := openWithSource(f, dir); err == nil {
		f.Close()
		t.Fatal("读取失败的目录不应打开成功")
	}
	// 失败路径已关闭句柄：再 Stat 必然报 closed。
	if _, err := f.Stat(); err == nil {
		f.Close()
		t.Fatal("openBook 失败后句柄未关闭（M6 回归）")
	}
}
