package reader

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildBigTestEpub 两章各 ~1.5 万字符（>1 页），用于翻页/切档/Seek 测试。
func buildBigTestEpub(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "big.epub")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	add := func(name, body string) {
		wr, _ := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		wr.Write([]byte(body))
	}
	add("mimetype", "application/epub+zip")
	add("META-INF/container.xml", `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`)
	add("OEBPS/content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>大部头</dc:title><dc:creator>作者甲</dc:creator></metadata><manifest><item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/><item id="c2" href="c2.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c1"/><itemref idref="c2"/></spine></package>`)
	fill := strings.Repeat("这是填充正文内容，用于把章节撑过一页的长度限制。", 800) // ~1.8 万字符/章
	add("OEBPS/c1.xhtml", "<html><body><h1>第一章 开篇</h1><p>"+fill+"</p></body></html>")
	add("OEBPS/c2.xhtml", "<html><body><h1>第二章 收尾</h1><p>"+fill+"</p></body></html>")
	w.Close()
	f.Close()
	return p
}

// ---- 批次2：OpenEpub 内存流 Book（分页/TOC/Seek/规则库回落）----

// TestOpenEpubPaging 解包文本走与 txt 相同的分页：首页/翻页/末页/页码口径。
func TestOpenEpubPaging(t *testing.T) {
	p := buildTestEpub(t, true, false)
	b, err := OpenEpub(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if b.EncName() != "UTF-8" {
		t.Fatalf("EPUB 编码应恒为 UTF-8，got %s", b.EncName())
	}
	cur := b.Current()
	if !strings.Contains(cur, "第一章 初入江湖") {
		t.Fatalf("首页应含第一章，got 前 100 字=%q", []rune(cur[:minStr(100, len([]rune(cur)))]))
	}
	// 短书（<1 页）：翻页应原地不动且不报错
	if _, more, err := b.LoadNextPage(); err != nil || more {
		t.Fatalf("单页书不应有下一页：more=%v err=%v", more, err)
	}
	// Last 定位末页且游标一致
	last, err := b.Last()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(last, "第二章") && !strings.Contains(last, "刀光") {
		t.Fatalf("末页应含第二章内容，got 前 50 字=%q", []rune(last[:minStr(50, len([]rune(last)))]))
	}
}

// TestOpenEpubTOC 自带 TOC 直接可用，SeekTo 能落到对应章节。
func TestOpenEpubTOC(t *testing.T) {
	p := buildTestEpub(t, true, false)
	b, err := OpenEpub(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	toc, err := b.BuildTOC()
	if err != nil {
		t.Fatal(err)
	}
	if len(toc) != 3 {
		t.Fatalf("TOC 应 3 条，got %d", len(toc))
	}
	if toc[0].Title != "第一章 初入江湖" || toc[2].Level != 1 {
		t.Fatalf("TOC 内容/层级不符：%+v", toc)
	}
	// SeekTo 落到第二章标题
	txt, off, err := b.SeekTo(toc[1].Offset, toc[1].Title)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(txt, "第二章 短兵相接") || off < 0 {
		t.Fatalf("SeekTo 未落第二章：off=%d txt 前 50=%q", off, []rune(txt[:minStr(50, len([]rune(txt)))]))
	}
}

// TestOpenEpubNoTOCRuleFallback 无自带 TOC 时回落章节规则库（D1 拍板）。
func TestOpenEpubNoTOCRuleFallback(t *testing.T) {
	p := buildTestEpub(t, false, false)
	b, err := OpenEpub(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	toc, err := b.BuildTOC()
	if err != nil {
		t.Fatal(err)
	}
	if len(toc) < 2 {
		t.Fatalf("规则库应识别出至少 2 章，got %d：%+v", len(toc), toc)
	}
	found1, found2 := false, false
	for _, e := range toc {
		if strings.Contains(e.Title, "第一章") {
			found1 = true
		}
		if strings.Contains(e.Title, "第二章") {
			found2 = true
		}
	}
	if !found1 || !found2 {
		t.Fatalf("规则库应识别两章：%+v", toc)
	}
}

// TestOpenEpubEncrypted 加密 EPUB 在 Book 层同样明确报错。
func TestOpenEpubEncrypted(t *testing.T) {
	p := buildTestEpub(t, true, true)
	if _, err := OpenEpub(p); err == nil {
		t.Fatal("加密 EPUB 应报错")
	}
}

// TestOpenEpubProgressAndAnchor 进度（raw 页码）与清洗切档在 EPUB 上口径不变。
func TestOpenEpubProgressAndAnchor(t *testing.T) {
	// 构造一本 >1 页的 epub：把两章各塞 1.5 万字符
	p := buildBigTestEpub(t)
	b, err := OpenEpub(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := b.Next(); err != nil {
		t.Fatal(err)
	}
	pageBefore := b.Page()
	if pageBefore != 1 {
		t.Fatalf("应已到第 1 页，got %d", pageBefore)
	}
	// 切清洗档：页码保持（raw 口径）
	b.SetCleanOpts(CleanOptions{Indent: true, AdFilter: true, BlankAll: true})
	if b.Page() != pageBefore {
		t.Fatalf("切档应保持页码，got %d want %d", b.Page(), pageBefore)
	}
	// 锚点定位：SeekTo 任意偏移不越界
	txt, _, err := b.SeekTo(5*defaultPageRunes, "无")
	if err != nil {
		t.Fatal(err)
	}
	if txt == "" {
		t.Fatal("SeekTo 越界应钳制到末页文本，而非空白")
	}
}
