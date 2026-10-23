package reader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	xunicode "golang.org/x/text/encoding/unicode"
)

// TestBuildTOCPerfRealScale 真实书页量级性能探针（方法论：小样本会假绿）。
// 200 页 × 2 万字符 ≈ 8MB 网文量级，规则库从 8 条扩到 12 条 + 首字符分流，
// 劣化相对基线 >20% 视为不过。-short 跳过。
func TestBuildTOCPerfRealScale(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过真实量级探针")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "perf.txt")

	// 正文行模拟：绝大多数行走 trigger 短路，每 20 行掺 1 条章节行。
	// 按 rune 计数凑满 4M rune（≈200 页 × 2 万 rune，真实书页量级；
	// 不能用 sb.Len()——那是 UTF-8 字节数，中文 1 字 3 字节，书会缩水 3 倍）。
	const totalRunes = 4_000_000
	var sb strings.Builder
	fill := "这是一段用于填充性能探针的正文内容，长度适中，不含任何章节特征。"
	nRunes := 0
	for line := 0; nRunes < totalRunes; line++ {
		var s string
		if line%20 == 0 {
			s = fmt.Sprintf("第%d章 风云变幻%d", line, line)
		} else {
			s = fill
		}
		sb.WriteString(s)
		sb.WriteByte('\n')
		nRunes += len([]rune(s)) + 1
	}
	if err := os.WriteFile(p, []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	toc, err := BuildTOCFor(p, xunicode.UTF8, defaultPageRunes, CleanOptions{AdFilter: true})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	// 命中数 sanity：总行数 ~13 万、每 20 行一条章节 → 约 6500 章（±15%）
	if len(toc) < 5500 || len(toc) > 7500 {
		t.Fatalf("章节数异常：%d", len(toc))
	}
	// 经验基线：旧 8 条规则同量级 ~300ms 量级；给 2s 硬上限防灾难性回归
	if elapsed > 2*time.Second {
		t.Fatalf("BuildTOC 劣化：%v（%d 章）", elapsed, len(toc))
	}
	t.Logf("真实量级 BuildTOC：%v（%d 章）", elapsed, len(toc))
}
