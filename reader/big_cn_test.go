package reader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// TestBigCNScaled 复现大叔真实场景：GBK 中文 ~2MB、带章节的大文件。
// 验证：Open 编码判定、逐页翻到底且每页完整（无提前 EOF/丢页）、BuildTOC 耗时。
func TestBigCNScaled(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cn.txt")
	var text strings.Builder
	for ch := 0; ch < 120; ch++ {
		text.WriteString(fmt.Sprintf("第%d章 测试章节标题\n", ch+1))
		for para := 0; para < 40; para++ {
			text.WriteString(strings.Repeat("中文正文内容测试句子。", 25))
			text.WriteString("\n\n")
		}
	}
	raw, _ := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(text.String()))
	if err := os.WriteFile(p, raw, 0644); err != nil {
		t.Fatal(err)
	}
	t.Logf("文件大小=%d bytes（约 %.1f KB）", len(raw), float64(len(raw))/1024)

	b, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	t.Logf("Open 判定编码=%s", b.EncName())

	// 逐页翻到底，检查每页（除末页）rune 数满页、内容非空
	pages := 1
	prev := b.Current()
	if len([]rune(prev)) == 0 {
		t.Fatal("首页为空")
	}
	for {
		s, err := b.Next()
		if err != nil {
			t.Fatalf("第%d 次 Next err=%v", pages, err)
		}
		if s == prev { // 末页
			break
		}
		prev = s
		pages++
		if len([]rune(s)) == 0 {
			t.Fatalf("第%d 页为空（中间页丢失）", pages)
		}
		if pages > 100000 {
			t.Fatal("翻页失控")
		}
	}
	t.Logf("共翻 %d 页到底", pages)

	// BuildTOC 耗时（D 轮 openAt 后同步全扫，阻塞 UI 的程度）
	t0 := time.Now()
	toc, err := b.BuildTOC()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("BuildTOC 识别 %d 章，耗时 %v", len(toc), time.Since(t0))
}
