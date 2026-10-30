package reader

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildEncEpub 构造带 META-INF/encryption.xml 的最小 EPUB。
// encryptedContent=true：内容文档被加密（真 DRM）；false：只做字体混淆。
func buildEncEpub(t *testing.T, encryptContent bool) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "enc.epub")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	add := func(name, body string) {
		wr, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := wr.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	add("mimetype", "application/epub+zip")
	add("META-INF/container.xml", `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`)

	if encryptContent {
		// 真 DRM：AES 加密指向内容 xhtml
		add("META-INF/encryption.xml", `<?xml version="1.0"?>
<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container" xmlns:enc="http://www.w3.org/2001/04/xmlenc#">
  <enc:EncryptedData>
    <enc:EncryptionMethod Algorithm="http://www.w3.org/2001/04/xmlenc#aes128-cbc"/>
    <enc:CipherData><enc:CipherReference URI="OEBPS/ch1.xhtml"/></enc:CipherData>
  </enc:EncryptedData>
</encryption>`)
	} else {
		// 字体混淆：IDPF embedding 算法指向 ttf，正文不受影响
		add("META-INF/encryption.xml", `<?xml version="1.0"?>
<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container" xmlns:enc="http://www.w3.org/2001/04/xmlenc#">
  <enc:EncryptedData>
    <enc:EncryptionMethod Algorithm="http://www.idpf.org/2008/embedding"/>
    <enc:CipherData><enc:CipherReference URI="OEBPS/fonts/simsum.ttf"/></enc:CipherData>
  </enc:EncryptedData>
</encryption>`)
	}
	add("OEBPS/content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>加密测试</dc:title></metadata><manifest><item id="c1" href="ch1.xhtml" media-type="application/xhtml+xml"/><item id="f1" href="fonts/simsum.ttf" media-type="application/vnd.ms-opentype"/></manifest><spine><itemref idref="c1"/></spine></package>`)
	add("OEBPS/ch1.xhtml", `<html><body><p>正文可读内容。</p></body></html>`)
	add("OEBPS/fonts/simsum.ttf", "fake-font-bytes")
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestFontObfuscationNotDRM F1 回归（2026-10-26 探针实锤）：encryption.xml 只做
// 字体混淆时必须放行——此前见文件即拒，Calibre 嵌入字体的正版书整本打不开。
func TestFontObfuscationNotDRM(t *testing.T) {
	p := buildEncEpub(t, false)
	ec, err := ParseEpub(p, "[图]")
	if err != nil {
		t.Fatalf("字体混淆 EPUB 被误拒：%v", err)
	}
	if !strings.Contains(ec.Text, "正文可读") {
		t.Fatalf("正文提取失败：%q", ec.Text)
	}
}

// TestContentEncryptedStillDRM F1 对照：内容文档被真加密仍必须报 errEncrypted。
func TestContentEncryptedStillDRM(t *testing.T) {
	p := buildEncEpub(t, true)
	_, err := ParseEpub(p, "[图]")
	if err == nil {
		t.Fatal("内容加密 EPUB 应报错")
	}
	if !errors.Is(err, errEncrypted) {
		t.Fatalf("应返回 errEncrypted，got %v", err)
	}
}

// TestParseNavPrefersToc F2 回归（2026-10-26 探针实锤）：landmarks nav 排在
// toc 之前时，必须取 epub:type="toc" 的 nav，不能取第一个 <nav>。
func TestParseNavPrefersToc(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "navtype.epub")
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
	add("META-INF/container.xml", `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`)
	add("OEBPS/content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>landmarks 在前</dc:title></metadata><manifest><item id="c1" href="ch1.xhtml" media-type="application/xhtml+xml"/><item id="c2" href="ch2.xhtml" media-type="application/xhtml+xml"/><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/></manifest><spine><itemref idref="c1"/><itemref idref="c2"/></spine></package>`)
	add("OEBPS/nav.xhtml", `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops">
<body>
<nav epub:type="landmarks" hidden=""><ol>
  <li><a epub:type="bodymatter" href="ch1.xhtml">正文</a></li>
</ol></nav>
<nav epub:type="toc"><ol>
  <li><a href="ch1.xhtml">第一章 甲</a></li>
  <li><a href="ch2.xhtml">第二章 乙</a></li>
</ol></nav>
</body></html>`)
	add("OEBPS/ch1.xhtml", `<html><body><h1>第一章 甲</h1><p>甲甲甲。</p></body></html>`)
	add("OEBPS/ch2.xhtml", `<html><body><h1>第二章 乙</h1><p>乙乙乙。</p></body></html>`)
	w.Close()
	f.Close()

	ec, err := ParseEpub(p, "[图]")
	if err != nil {
		t.Fatal(err)
	}
	if len(ec.Toc) != 2 {
		t.Fatalf("应取 toc nav 得 2 条，got %d：%+v", len(ec.Toc), ec.Toc)
	}
	if ec.Toc[0].Title != "第一章 甲" || ec.Toc[1].Title != "第二章 乙" {
		t.Fatalf("TOC 标题不符（误取 landmarks？）：%+v", ec.Toc)
	}
}

// TestParseNavFallbackFirst F2 对照：无 epub:type 时退回第一个 nav（旧格式容错）。
func TestParseNavFallbackFirst(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "nonavtype.epub")
	f, _ := os.Create(p)
	w := zip.NewWriter(f)
	add := func(name, body string) {
		wr, _ := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		wr.Write([]byte(body))
	}
	add("mimetype", "application/epub+zip")
	add("META-INF/container.xml", `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`)
	add("OEBPS/content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>无 epub:type</dc:title></metadata><manifest><item id="c1" href="ch1.xhtml" media-type="application/xhtml+xml"/><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/></manifest><spine><itemref idref="c1"/></spine></package>`)
	add("OEBPS/nav.xhtml", `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body>
<nav><ol><li><a href="ch1.xhtml">旧式目录</a></li></ol></nav>
</body></html>`)
	add("OEBPS/ch1.xhtml", `<html><body><p>正文。</p></body></html>`)
	w.Close()
	f.Close()

	ec, err := ParseEpub(p, "[图]")
	if err != nil {
		t.Fatal(err)
	}
	if len(ec.Toc) != 1 || ec.Toc[0].Title != "旧式目录" {
		t.Fatalf("应回退第一个 nav，got %+v", ec.Toc)
	}
}

// TestTOCEntryEmptyChapter F4 回归（2026-10-26 探针实锤）：TOC 指向空章节
// （封面/扉页）时条目不再静默丢弃。
func TestTOCEntryEmptyChapter(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cover.epub")
	f, _ := os.Create(p)
	w := zip.NewWriter(f)
	add := func(name, body string) {
		wr, _ := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		wr.Write([]byte(body))
	}
	add("mimetype", "application/epub+zip")
	add("META-INF/container.xml", `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`)
	add("OEBPS/content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>含封面</dc:title></metadata><manifest><item id="c0" href="cover.xhtml" media-type="application/xhtml+xml"/><item id="c1" href="ch1.xhtml" media-type="application/xhtml+xml"/><item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/></manifest><spine toc="ncx"><itemref idref="c0"/><itemref idref="c1"/></spine></package>`)
	add("OEBPS/cover.xhtml", `<html><body><div></div></body></html>`)
	add("OEBPS/ch1.xhtml", `<html><body><h1>第一章</h1><p>正文。</p></body></html>`)
	add("OEBPS/toc.ncx", `<?xml version="1.0"?><ncx xmlns="http://www.daisy.org/z3986/2005/ncx/"><navMap>
  <navPoint id="n0"><navLabel><text>封面</text></navLabel><content src="cover.xhtml"/></navPoint>
  <navPoint id="n1"><navLabel><text>第一章</text></navLabel><content src="ch1.xhtml"/></navPoint>
</navMap></ncx>`)
	w.Close()
	f.Close()

	ec, err := ParseEpub(p, "[图]")
	if err != nil {
		t.Fatal(err)
	}
	if len(ec.Toc) != 2 {
		t.Fatalf("指向空章节的 TOC 条目被静默丢弃：应 2 条 got %d：%+v", len(ec.Toc), ec.Toc)
	}
	if ec.Toc[0].Title != "封面" || ec.Toc[1].Title != "第一章" {
		t.Fatalf("TOC 标题不符：%+v", ec.Toc)
	}
	// 封面偏移应落在合法范围（0 或后续内容起点）
	if ec.Toc[0].Offset < 0 || ec.Toc[0].Offset > len([]rune(ec.Text)) {
		t.Fatalf("封面 offset 越界：%d", ec.Toc[0].Offset)
	}
}
