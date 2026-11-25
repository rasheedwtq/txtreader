package reader

// H1（2026-11-25 审查）：畸形 OPF 的空指针崩溃回归。
// 约定：缺元数据 → 正文照常可读（书名留空）；缺 manifest → 返回错误而不是 panic。

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildMalformedEpub 造一本只缺一块的最小 EPUB（其余部分合规，排除命名空间干扰）。
func buildMalformedEpub(t *testing.T, dropMeta, dropManifest bool) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "malformed.epub")
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

	meta := `<metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>缺块测试</dc:title><dc:creator>长工</dc:creator>
  </metadata>`
	if dropMeta {
		meta = ""
	}
	manifest := `<manifest><item id="c1" href="ch1.xhtml" media-type="application/xhtml+xml"/></manifest>`
	if dropManifest {
		manifest = ""
	}
	add("OEBPS/content.opf", `<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">`+
		meta+manifest+`<spine><itemref idref="c1"/></spine></package>`)
	add("OEBPS/ch1.xhtml", `<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body><h1>第一章</h1><p>正文可读内容</p></body></html>`)

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestH1MissingMetadataStillReadable 缺 <metadata>：旧版在 Information()→getMeta(nil)
// 处空指针崩溃；修复后应正常出正文，书名留空（不影响阅读）。
func TestH1MissingMetadataStillReadable(t *testing.T) {
	p := buildMalformedEpub(t, true, false)
	ec, err := ParseEpub(p, "[图]")
	if err != nil {
		t.Fatalf("缺 metadata 不应打不开：%v", err)
	}
	if !strings.Contains(ec.Text, "正文可读内容") {
		t.Fatalf("正文缺失：%q", ec.Text)
	}
	if ec.Title != "" {
		t.Fatalf("缺 metadata 时书名应为空，got %q", ec.Title)
	}
}

// TestH1MissingManifestReturnsError 缺 <manifest>：旧版在 opf.Manifest.Items 处 panic；
// 修复后必须是可读错误，不能崩进程、不能返回半成品。
func TestH1MissingManifestReturnsError(t *testing.T) {
	p := buildMalformedEpub(t, false, true)
	ec, err := ParseEpub(p, "[图]")
	if err == nil {
		t.Fatalf("缺 manifest 应报错，却返回了内容：%+v", ec)
	}
	if !strings.Contains(err.Error(), "manifest") {
		t.Fatalf("错误信息应指明 manifest，got %v", err)
	}
	if ec != nil {
		t.Fatalf("报错时不应返回内容对象，got %+v", ec)
	}
}
