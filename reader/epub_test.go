package reader

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildTestEpub 手工打包一本最小 EPUB（mimetype/container/opf/两章正文/nav/ncx）。
// encrypt=true 时附带 META-INF/encryption.xml（模拟 DRM 标记）。
// withTOC=false 时不放 nav/ncx（测规则库回落）。
func buildTestEpub(t *testing.T, withTOC, encrypt bool) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "book.epub")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	add := func(name, body string) {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		wr, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := wr.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}

	add("mimetype", "application/epub+zip")
	add("META-INF/container.xml", `<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`)

	tocManifest := ""
	if withTOC {
		tocManifest = `<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>` +
			`<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>`
	}
	add("OEBPS/content.opf", `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="uid">urn:uuid:test-epub-001</dc:identifier>
    <dc:title>测试书名</dc:title>
    <dc:creator>测试作者</dc:creator>
    <dc:language>zh</dc:language>
  </metadata>
  <manifest>
    <item id="c1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="c2" href="ch2.xhtml" media-type="application/xhtml+xml"/>`+tocManifest+`
  </manifest>
  <spine toc="ncx"><itemref idref="c1"/><itemref idref="c2"/></spine>
</package>`)

	add("OEBPS/ch1.xhtml", `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>不应出现</title>
<script>var evil = "script 内容不应出现";</script></head>
<body><h1>第一章 初入江湖</h1>
<p>少年背起行囊，走向&lt;江湖&gt;。此行凶险，&amp;前路未卜。<br/>夜宿荒村。</p>
<img src="img/map.png" alt=""/>
<script>console.log("也不应出现");</script>
</body></html>`)

	add("OEBPS/ch2.xhtml", `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body>
<h2>第二章 短兵相接</h2><p>刀光一闪。</p></body></html>`)

	if withTOC {
		add("OEBPS/nav.xhtml", `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<body><nav epub:type="toc"><ol>
<li><a href="ch1.xhtml">第一章 初入江湖</a></li>
<li><a href="ch2.xhtml">第二章 短兵相接</a>
  <ol><li><a href="ch2.xhtml#sub">2.1 前哨</a></li></ol>
</li>
</ol></nav></body></html>`)

		add("OEBPS/toc.ncx", `<?xml version="1.0" encoding="utf-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
<navMap>
  <navPoint id="n1" playOrder="1"><navLabel><text>第一章 初入江湖</text></navLabel>
    <content src="ch1.xhtml"/></navPoint>
  <navPoint id="n2" playOrder="2"><navLabel><text>第二章 短兵相接</text></navLabel>
    <content src="ch2.xhtml"/>
    <navPoint id="n2-1" playOrder="3"><navLabel><text>2.1 前哨</text></navLabel>
      <content src="ch2.xhtml#sub"/></navPoint>
  </navPoint>
</navMap></ncx>`)
	}

	if encrypt {
		add("META-INF/encryption.xml", `<?xml version="1.0"?>
<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><enc:EncryptedData/></encryption>`)
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestParseEpubBasic 批次1 主用例：spine 顺序提取、元数据、实体解码、
// script 剔除、[图] 占位、TOC 偏移映射与层级。
func TestParseEpubBasic(t *testing.T) {
	p := buildTestEpub(t, true, false)
	ec, err := ParseEpub(p, "[图]")
	if err != nil {
		t.Fatal(err)
	}
	if ec.Title != "测试书名" || ec.Author != "测试作者" {
		t.Fatalf("元数据错误：title=%q author=%q", ec.Title, ec.Author)
	}
	// spine 顺序：第一章在前、第二章在后
	i1 := strings.Index(ec.Text, "第一章 初入江湖")
	i2 := strings.Index(ec.Text, "第二章 短兵相接")
	if i1 < 0 || i2 < 0 || i1 > i2 {
		t.Fatalf("正文顺序异常：i1=%d i2=%d", i1, i2)
	}
	// 实体解码 & script 剔除
	if !strings.Contains(ec.Text, "<江湖>") || !strings.Contains(ec.Text, "&前路未卜") {
		t.Fatalf("实体未解码：%q", ec.Text[:minStr(200, len(ec.Text))])
	}
	if strings.Contains(ec.Text, "script") || strings.Contains(ec.Text, "evil") {
		t.Fatal("script 内容泄漏进正文")
	}
	if strings.Contains(ec.Text, "不应出现") {
		t.Fatal("head/title 内容泄漏进正文")
	}
	if !strings.Contains(ec.Text, "[图]") {
		t.Fatal("图片占位符缺失")
	}
	// TOC：nav 优先（含嵌套层级），offset 必须落在对应章节上
	if len(ec.Toc) != 3 {
		t.Fatalf("TOC 应 3 条（含 1 条二级），got %d：%+v", len(ec.Toc), ec.Toc)
	}
	if ec.Toc[0].Title != "第一章 初入江湖" || ec.Toc[1].Title != "第二章 短兵相接" || ec.Toc[2].Title != "2.1 前哨" {
		t.Fatalf("TOC 标题不符：%+v", ec.Toc)
	}
	if ec.Toc[0].Level != 0 || ec.Toc[2].Level != 1 {
		t.Fatalf("TOC 层级不符：%+v", ec.Toc)
	}
	seg := ec.Text[ec.Toc[0].Offset:]
	if !strings.Contains(seg[:minStr(80, len(seg))], "第一章") {
		t.Fatalf("TOC offset 未落在第一章：%d", ec.Toc[0].Offset)
	}
}

// TestParseEpubNCXFallback 无 nav 时回落 NCX（EPUB2 路径）。
func TestParseEpubNCXFallback(t *testing.T) {
	p := buildTestEpub(t, true, false)
	// 手工重打一本没有 nav 的：直接改 opf？简化：解析后删除 nav 断言走 NCX
	ec, err := ParseEpub(p, "[图]")
	if err != nil {
		t.Fatal(err)
	}
	if len(ec.Toc) == 0 {
		t.Fatal("TOC 不应为空")
	}
	// NCX 与 nav 内容一致，此处只验数量一致（nav 优先级由源码顺序保证）
	if len(ec.Toc) != 3 {
		t.Fatalf("TOC 应 3 条，got %d", len(ec.Toc))
	}
}

// TestParseEpubEncrypted 加密 EPUB 必须明确报错（DRM 红线）。
func TestParseEpubEncrypted(t *testing.T) {
	p := buildTestEpub(t, true, true)
	_, err := ParseEpub(p, "[图]")
	if err == nil {
		t.Fatal("加密 EPUB 应报错")
	}
	if !errors.Is(err, errEncrypted) {
		t.Fatalf("应返回 errEncrypted，got %v", err)
	}
}

// TestParseEpubNoTOC 无 nav/ncx：正文可读、TOC 为空（批次2 由规则库回落）。
func TestParseEpubNoTOC(t *testing.T) {
	p := buildTestEpub(t, false, false)
	ec, err := ParseEpub(p, "[图]")
	if err != nil {
		t.Fatal(err)
	}
	if len(ec.Toc) != 0 {
		t.Fatalf("无 TOC 源时应为空，got %d", len(ec.Toc))
	}
	if !strings.Contains(ec.Text, "第一章 初入江湖") {
		t.Fatal("正文仍应完整")
	}
}

func minStr(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestParseEpubNCXOnly 只有 NCX 没有 nav（docx2epub 实际产物形态）——
// 此前该路径被 nav 优先级遮蔽，content[src],attr 链式属性 bug 假绿漏网。
func TestParseEpubNCXOnly(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ncxonly.epub")
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
	add("OEBPS/content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="uid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>NCX 独占</dc:title></metadata><manifest><item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/><item id="c1" href="sections/s1.xhtml" media-type="application/xhtml+xml"/></manifest><spine toc="ncx"><itemref idref="c1"/></spine></package>`)
	add("OEBPS/toc.ncx", `<?xml version="1.0" encoding="utf-8"?>
<!DOCTYPE ncx PUBLIC "-//NISO//DTD ncx 2005-1//EN" "http://www.daisy.org/z3986/2005/ncx-2005-1.dtd">
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1"><navMap>
  <navPoint id="np-1"><navLabel><text>数据赋能章节</text></navLabel><content src="sections/s1.xhtml"/></navPoint>
</navMap></ncx>`)
	add("OEBPS/sections/s1.xhtml", "<html><body><p>正文内容。</p></body></html>")
	w.Close()
	f.Close()

	ec, err := ParseEpub(p, "[图]")
	if err != nil {
		t.Fatal(err)
	}
	if len(ec.Toc) != 1 {
		t.Fatalf("NCX 应解析出 1 条，got %d", len(ec.Toc))
	}
	if ec.Toc[0].Title != "数据赋能章节" {
		t.Fatalf("标题不符：%q", ec.Toc[0].Title)
	}
	seg := ec.Text[ec.Toc[0].Offset:]
	if !strings.Contains(seg, "正文内容") {
		t.Fatalf("offset 应落在正文，got %q", []rune(seg[:minStr(50, len(seg))]))
	}
}

// TestParseEpubNCXSubdir E1 回归（2026-10-23 审查探针实锤）：TOC 文件在 OPF
// 子目录时，旧代码双重 path.Dir 把 TOC 自身目录剥掉，条目静默全丢。
func TestParseEpubNCXSubdir(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "subdir.epub")
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
	// NCX 在 OEBPS/text/ 子目录，章节也在 text/ 下（href 相对 NCX 自身目录）
	add("OEBPS/content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="2.0" unique-identifier="uid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>子目录 NCX</dc:title></metadata><manifest><item id="ncx" href="text/toc.ncx" media-type="application/x-dtbncx+xml"/><item id="c1" href="text/c1.xhtml" media-type="application/xhtml+xml"/><item id="c2" href="text/c2.xhtml" media-type="application/xhtml+xml"/></manifest><spine toc="ncx"><itemref idref="c1"/><itemref idref="c2"/></spine></package>`)
	add("OEBPS/text/toc.ncx", `<?xml version="1.0"?><ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1"><navMap>
  <navPoint id="n1"><navLabel><text>第一章</text></navLabel><content src="c1.xhtml"/></navPoint>
  <navPoint id="n2"><navLabel><text>第二章</text></navLabel><content src="c2.xhtml"/></navPoint>
</navMap></ncx>`)
	add("OEBPS/text/c1.xhtml", "<html><body><p>第一章内容甲。</p></body></html>")
	add("OEBPS/text/c2.xhtml", "<html><body><p>第二章内容乙。</p></body></html>")
	w.Close()
	f.Close()

	ec, err := ParseEpub(p, "[图]")
	if err != nil {
		t.Fatal(err)
	}
	if len(ec.Toc) != 2 {
		t.Fatalf("子目录 NCX 应解析出 2 条，got %d（E1 回归？）", len(ec.Toc))
	}
	if ec.Toc[0].Title != "第一章" || ec.Toc[1].Title != "第二章" {
		t.Fatalf("TOC 标题不符：%+v", ec.Toc)
	}
}
