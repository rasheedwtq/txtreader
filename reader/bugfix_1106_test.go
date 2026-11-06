package reader

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pirmd/epub"
	xhtml "golang.org/x/net/html"
)

// 2026-11-06 回归：G4（EPUB 目录 fragment 精确定位）/ G5（加密检测按
// OPF manifest 的 media-type 判内容文档）。

func fix1106Pack(t *testing.T, files map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fix1106.epub")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, body := range files {
		wr, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := wr.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

const fix1106Container = `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`

const fix1106EncTmpl = `<?xml version="1.0"?>
<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container" xmlns:enc="http://www.w3.org/2001/04/xmlenc#">
  <enc:EncryptedData>
    <enc:EncryptionMethod Algorithm="%s"/>
    <enc:CipherData><enc:CipherReference URI="%s"/></enc:CipherData>
  </enc:EncryptedData>
</encryption>`

const (
	fix1106AES      = "http://www.w3.org/2001/04/xmlenc#aes128-cbc"
	fix1106FontAlgo = "http://www.idpf.org/2008/embedding"
)

// fix1106EncBook 构造一本最小 EPUB：一个正文条目（文件名 / media-type 可参数化）
// + 若干非正文资源 + 可选的 encryption.xml。
func fix1106EncBook(t *testing.T, contentHref, contentMT, encURI, encAlgo string) string {
	t.Helper()
	files := map[string]string{
		"mimetype":               "application/epub+zip",
		"META-INF/container.xml": fix1106Container,
		"OEBPS/content.opf": fmt.Sprintf(`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>加密矩阵</dc:title></metadata><manifest>
<item id="c1" href="%s" media-type="%s"/>
<item id="f1" href="fonts/a.ttf" media-type="application/vnd.ms-opentype"/>
<item id="r1" href="rights.xml" media-type="application/xml"/>
<item id="t1" href="toc.xml" media-type="application/xml"/>
</manifest><spine><itemref idref="c1"/></spine></package>`, contentHref, contentMT),
		"OEBPS/" + contentHref: `<html><body><p>正文可读内容。</p></body></html>`,
		"OEBPS/fonts/a.ttf":    "fake-font-bytes",
		"OEBPS/rights.xml":     "<rights/>",
		"OEBPS/toc.xml":        "<toc/>",
		"META-INF/rights.xml":  "<rights/>",
	}
	if encURI != "" {
		if encAlgo == "" {
			encAlgo = fix1106AES
		}
		files["META-INF/encryption.xml"] = fmt.Sprintf(fix1106EncTmpl, encAlgo, encURI)
	}
	return fix1106Pack(t, files)
}

// ---- G4：fragment ----

// fix1106FragmentBook 两章正文 + NCX（三条，含两个指向 ch2 不同 fragment 的条目）。
func fix1106FragmentBook(t *testing.T, ch2Body, ncx string) string {
	t.Helper()
	files := map[string]string{
		"mimetype":               "application/epub+zip",
		"META-INF/container.xml": fix1106Container,
		"OEBPS/content.opf": `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>fragment 测试</dc:title></metadata><manifest>
<item id="c1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
<item id="c2" href="ch2.xhtml" media-type="application/xhtml+xml"/>
<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
</manifest><spine toc="ncx"><itemref idref="c1"/><itemref idref="c2"/></spine></package>`,
		"OEBPS/ch1.xhtml": `<html><body><h1>第一章 初入江湖</h1><p>甲甲甲。</p></body></html>`,
		"OEBPS/ch2.xhtml": ch2Body,
		"OEBPS/toc.ncx":   ncx,
	}
	return fix1106Pack(t, files)
}

const fix1106Ch2 = `<html><body>
<h2 id="intro">第二章 短兵相接</h2>
<p>开头很长的一段正文，用来把小节和章首拉开距离。</p>
<h3 id="sub">2.1 前哨</h3>
<p>小节正文。</p>
<h3 id="later">2.2 接战</h3>
<p>另一段小节正文。</p>
</body></html>`

func fix1106NCX(refs ...string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0"?><ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1"><navMap>`)
	for i, ref := range refs {
		fmt.Fprintf(&sb, `<navPoint id="n%d" playOrder="%d"><navLabel><text>条目%d</text></navLabel><content src="%s"/></navPoint>`, i, i+1, i+1, ref)
	}
	sb.WriteString(`</navMap></ncx>`)
	return sb.String()
}

func fix1106RuneAt(text string, off int) string {
	rs := []rune(text)
	if off < 0 || off >= len(rs) {
		return "<越界>"
	}
	end := off + 24
	if end > len(rs) {
		end = len(rs)
	}
	return string(rs[off:end])
}

// TestG4FragmentOffset 同一章的多个 #fragment 条目必须落到各自的 id 上，
// 而不是全部挤到章首（修复前 offset 差为 0）。
func TestG4FragmentOffset(t *testing.T) {
	// 标题写成正文里真实存在的文字，端到端 SeekTo 才能按标题定位到页内。
	ncx := `<?xml version="1.0"?><ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1"><navMap>
<navPoint id="n1"><navLabel><text>第一章 初入江湖</text></navLabel><content src="ch1.xhtml"/></navPoint>
<navPoint id="n2"><navLabel><text>第二章 短兵相接</text></navLabel><content src="ch2.xhtml"/></navPoint>
<navPoint id="n3"><navLabel><text>2.1 前哨</text></navLabel><content src="ch2.xhtml#sub"/></navPoint>
<navPoint id="n4"><navLabel><text>2.2 接战</text></navLabel><content src="ch2.xhtml#later"/></navPoint>
</navMap></ncx>`
	p := fix1106FragmentBook(t, fix1106Ch2, ncx)
	ec, err := ParseEpub(p, "[图]")
	if err != nil {
		t.Fatal(err)
	}
	if len(ec.Toc) != 4 {
		t.Fatalf("TOC 应 4 条，got %d：%+v", len(ec.Toc), ec.Toc)
	}
	for i, e := range ec.Toc {
		t.Logf("TOC[%d] %q offset=%d 落点=%q", i, e.Title, e.Offset, fix1106RuneAt(ec.Text, e.Offset))
	}
	if ec.Toc[2].Offset <= ec.Toc[1].Offset || ec.Toc[3].Offset <= ec.Toc[2].Offset {
		t.Fatalf("G4 回归：fragment 条目仍挤在章首：%d / %d / %d",
			ec.Toc[1].Offset, ec.Toc[2].Offset, ec.Toc[3].Offset)
	}
	if !strings.HasPrefix(fix1106RuneAt(ec.Text, ec.Toc[2].Offset), "2.1 前哨") {
		t.Fatalf("G4 回归：#sub 未落在小节标题：%q", fix1106RuneAt(ec.Text, ec.Toc[2].Offset))
	}
	if !strings.HasPrefix(fix1106RuneAt(ec.Text, ec.Toc[3].Offset), "2.2 接战") {
		t.Fatalf("G4 回归：#later 未落在小节标题：%q", fix1106RuneAt(ec.Text, ec.Toc[3].Offset))
	}
	// 端到端：经 OpenEpub 的 pageSize 换算 + SeekTo 仍能定位到小节
	b, err := OpenEpub(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	e := ec.Toc[2]
	txt, inPage, err := b.SeekTo(e.Offset, e.Title)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string([]rune(txt)[inPage:]), "2.1 前哨") {
		t.Fatalf("G4 回归：SeekTo 未落在小节：page=%d inPage=%d", b.Page(), inPage)
	}
}

// TestG4FragmentFallback 找不到 id 时退回章首（旧行为，保持容错）。
func TestG4FragmentFallback(t *testing.T) {
	p := fix1106FragmentBook(t, fix1106Ch2,
		fix1106NCX("ch2.xhtml", "ch2.xhtml#不存在的锚点"))
	ec, err := ParseEpub(p, "[图]")
	if err != nil {
		t.Fatal(err)
	}
	if len(ec.Toc) != 2 {
		t.Fatalf("TOC 应 2 条，got %d", len(ec.Toc))
	}
	if ec.Toc[0].Offset != ec.Toc[1].Offset {
		t.Fatalf("G4 回归：未知 fragment 应退回章首：%d vs %d", ec.Toc[0].Offset, ec.Toc[1].Offset)
	}
}

// TestG4FragmentURLEncoded fragment 里的百分号编码必须解码后再查 id。
func TestG4FragmentURLEncoded(t *testing.T) {
	ch2 := `<html><body><h2>第二章</h2><p>间隔。</p><h3 id="前哨">2.1 前哨</h3><p>正文。</p></body></html>`
	p := fix1106FragmentBook(t, ch2,
		fix1106NCX("ch2.xhtml", "ch2.xhtml#%E5%89%8D%E5%93%A8"))
	ec, err := ParseEpub(p, "[图]")
	if err != nil {
		t.Fatal(err)
	}
	if len(ec.Toc) != 2 || ec.Toc[1].Offset <= ec.Toc[0].Offset {
		t.Fatalf("G4 回归：URL 编码 fragment 未定位：%+v", ec.Toc)
	}
	if !strings.HasPrefix(fix1106RuneAt(ec.Text, ec.Toc[1].Offset), "2.1 前哨") {
		t.Fatalf("G4 回归：编码 fragment 落点错：%q", fix1106RuneAt(ec.Text, ec.Toc[1].Offset))
	}
}

// TestG4HTMLNameAnchor EPUB2 常用 <a name="...">，也要认。
func TestG4HTMLNameAnchor(t *testing.T) {
	ch2 := `<html><body><h2>第二章</h2><p>间隔。</p><a name="sec2"></a><h3>2.1 前哨</h3><p>正文。</p></body></html>`
	p := fix1106FragmentBook(t, ch2, fix1106NCX("ch2.xhtml", "ch2.xhtml#sec2"))
	ec, err := ParseEpub(p, "[图]")
	if err != nil {
		t.Fatal(err)
	}
	if len(ec.Toc) != 2 || ec.Toc[1].Offset <= ec.Toc[0].Offset {
		t.Fatalf("G4 回归：name 锚点未识别：%+v", ec.Toc)
	}
}

// TestG4FragmentNoE1Regression TOC 在 OPF 子目录 + 带 fragment，
// 确认拆 fragment 没有动 normHref/itemKey 口径（E1 回归）。
func TestG4FragmentNoE1Regression(t *testing.T) {
	files := map[string]string{
		"mimetype":               "application/epub+zip",
		"META-INF/container.xml": fix1106Container,
		"OEBPS/content.opf": `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>子目录</dc:title></metadata><manifest>
<item id="c1" href="text/ch1.xhtml" media-type="application/xhtml+xml"/>
<item id="nav" href="meta/nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
</manifest><spine><itemref idref="c1"/></spine></package>`,
		"OEBPS/text/ch1.xhtml": `<html><body><h1>第一章</h1><p>甲甲甲。</p><h2 id="part2">1.1 小节</h2><p>乙乙乙。</p></body></html>`,
		"OEBPS/meta/nav.xhtml": `<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><body><nav epub:type="toc"><ol>
<li><a href="../text/ch1.xhtml">第一章</a></li>
<li><a href="../text/ch1.xhtml#part2">1.1 小节</a></li>
</ol></nav></body></html>`,
	}
	p := fix1106Pack(t, files)
	ec, err := ParseEpub(p, "[图]")
	if err != nil {
		t.Fatal(err)
	}
	if len(ec.Toc) != 2 {
		t.Fatalf("G4/E1 回归：子目录 nav 条目丢失，TOC=%d 条：%+v", len(ec.Toc), ec.Toc)
	}
	if ec.Toc[1].Offset <= ec.Toc[0].Offset {
		t.Fatalf("G4/E1 回归：子目录 fragment 未定位：%+v", ec.Toc)
	}
	if !strings.HasPrefix(fix1106RuneAt(ec.Text, ec.Toc[1].Offset), "1.1 小节") {
		t.Fatalf("G4/E1 回归：落点错：%q", fix1106RuneAt(ec.Text, ec.Toc[1].Offset))
	}
}

// refHTMLToText 是 G4 改动前的 htmlToText 原文，仅用于锁死「提纯输出零改变」。
// htmlToText 的输出一旦变化，所有 EPUB 的页码/锚点/目录偏移都会跟着漂。
func refHTMLToText(r io.Reader, imgPlaceholder string) string {
	doc, err := xhtml.Parse(r)
	if err != nil {
		return ""
	}
	var sb strings.Builder
	var line []rune
	flush := func() {
		t := strings.Join(strings.Fields(string(line)), " ")
		if t != "" {
			sb.WriteString(t)
			sb.WriteByte('\n')
		}
		line = line[:0]
	}
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode {
			switch n.Data {
			case "script", "style", "head", "title":
				return
			case "br":
				flush()
				return
			case "img", "image":
				if imgPlaceholder != "" {
					line = append(line, []rune(imgPlaceholder)...)
				}
				return
			case "svg":
				if imgPlaceholder != "" {
					line = append(line, []rune(imgPlaceholder)...)
				}
				return
			}
			isBlock := blockTags[n.Data]
			if isBlock {
				flush()
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
			if isBlock {
				flush()
			}
			return
		}
		if n.Type == xhtml.TextNode {
			line = append(line, []rune(n.Data)...)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	flush()
	return sb.String()
}

// TestG4HTMLToTextUnchanged G4 只是加锚点记录，正文提纯结果必须与旧实现
// 逐字节一致（含 script 剔除、实体解码、图片占位、空白折叠、表格分行）。
func TestG4HTMLToTextUnchanged(t *testing.T) {
	samples := []string{
		`<html><body><h1>第一章</h1><p>少年背起行囊，走向&lt;江湖&gt;。此行凶险，&amp;前路未卜。<br/>夜宿荒村。</p><img src="a.png" alt=""/></body></html>`,
		`<html><head><title>不应出现</title><script>var evil="不该出现";</script><style>p{color:red}</style></head><body><p>正文。</p><script>console.log("也不该出现");</script></body></html>`,
		`<html><body><div>　　全角缩进  与   多空格</div><section><article>嵌套块</article></section></body></html>`,
		`<html><body><table><tr><td>甲</td><td>乙</td></tr><tr><td>丙</td><td>丁</td></tr></table></body></html>`,
		`<html><body><p>前</p><svg><path d="M0 0"/></svg><p>后</p></body></html>`,
		`<html><body><blockquote><p>引用一</p><p>引用二</p></blockquote><pre>代码  行</pre></body></html>`,
	}
	for i, s := range samples {
		want := refHTMLToText(strings.NewReader(s), "[图]")
		got := htmlToText(strings.NewReader(s), "[图]")
		if got != want {
			t.Fatalf("样本 %d 提纯输出变了：\nwant=%q\ngot =%q", i, want, got)
		}
		if _, anchors := htmlToTextWithAnchors(strings.NewReader(s), "[图]"); anchors == nil {
			t.Fatalf("样本 %d 锚点表不应为 nil", i)
		}
	}
}

// ---- G6：尾部空章节 ----

// fix1106EmptyBook 按给定顺序拼 spine；文件名含 ch1/ch2 的是正文，其余为空页。
func fix1106EmptyBook(t *testing.T, spine []string, ncx string) string {
	t.Helper()
	var items, refs strings.Builder
	files := map[string]string{
		"mimetype":               "application/epub+zip",
		"META-INF/container.xml": fix1106Container,
		"OEBPS/toc.ncx":          ncx,
	}
	for i, name := range spine {
		fmt.Fprintf(&items, `<item id="s%d" href="%s" media-type="application/xhtml+xml"/>`, i, name)
		fmt.Fprintf(&refs, `<itemref idref="s%d"/>`, i)
		body := `<html><body><div></div></body></html>`
		switch {
		case strings.Contains(name, "ch1"):
			body = `<html><body><h1>第一章 甲</h1><p>甲甲甲甲。</p></body></html>`
		case strings.Contains(name, "ch2"):
			body = `<html><body><h1>第二章 乙</h1><p>乙乙乙乙。</p></body></html>`
		}
		files["OEBPS/"+name] = body
	}
	items.WriteString(`<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>`)
	files["OEBPS/content.opf"] = fmt.Sprintf(`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>空章节</dc:title></metadata><manifest>%s</manifest><spine toc="ncx">%s</spine></package>`, items.String(), refs.String())
	return fix1106Pack(t, files)
}

// TestG6TrailingEmptyChapterDropped 文末空章节的 TOC 条目必须消失：
// 修复前它 offset=len(Text)，SeekTo 钳回 page=0 页内 0，与首章完全重合。
func TestG6TrailingEmptyChapterDropped(t *testing.T) {
	p := fix1106EmptyBook(t, []string{"ch1.xhtml", "cover.xhtml"},
		fix1106NCX("ch1.xhtml", "cover.xhtml"))
	ec, err := ParseEpub(p, "[图]")
	if err != nil {
		t.Fatal(err)
	}
	if len(ec.Toc) != 1 {
		t.Fatalf("G6 回归：尾部空章节应被丢弃，TOC got %d 条：%+v", len(ec.Toc), ec.Toc)
	}
	if ec.Toc[0].Offset != 0 || !strings.HasPrefix(fix1106RuneAt(ec.Text, ec.Toc[0].Offset), "第一章") {
		t.Fatalf("G6 回归：保留的条目应指向正文开头：%+v", ec.Toc[0])
	}
	b, err := OpenEpub(p)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if len(b.epubToc) != 1 {
		t.Fatalf("G6 回归：OpenEpub 的目录也应只有 1 条，got %d", len(b.epubToc))
	}
}

// TestG6LeadingEmptyChapterKept 首部空章节（F4 原场景）必须保留，不能误删。
func TestG6LeadingEmptyChapterKept(t *testing.T) {
	p := fix1106EmptyBook(t, []string{"cover.xhtml", "ch1.xhtml"},
		fix1106NCX("cover.xhtml", "ch1.xhtml"))
	ec, err := ParseEpub(p, "[图]")
	if err != nil {
		t.Fatal(err)
	}
	if len(ec.Toc) != 2 {
		t.Fatalf("G6 回归：首部空章节被误删，TOC got %d 条：%+v", len(ec.Toc), ec.Toc)
	}
	for _, e := range ec.Toc {
		if e.Offset < 0 || e.Offset > len([]rune(ec.Text)) {
			t.Fatalf("首部空章 offset 越界：%+v", e)
		}
	}
}

// TestG6MiddleEmptyChapterKept 中间空章节仍按 F4 登记，落点在后续内容附近，
// 三个条目 offset 必须严格递增（不能被挤成相同值）。
func TestG6MiddleEmptyChapterKept(t *testing.T) {
	p := fix1106EmptyBook(t, []string{"ch1.xhtml", "blank.xhtml", "ch2.xhtml"},
		fix1106NCX("ch1.xhtml", "blank.xhtml", "ch2.xhtml"))
	ec, err := ParseEpub(p, "[图]")
	if err != nil {
		t.Fatal(err)
	}
	if len(ec.Toc) != 3 {
		t.Fatalf("G6 回归：中间空章节被误删，TOC got %d 条：%+v", len(ec.Toc), ec.Toc)
	}
	if !(ec.Toc[0].Offset < ec.Toc[1].Offset && ec.Toc[1].Offset < ec.Toc[2].Offset) {
		t.Fatalf("G6 回归：中间空章条目落点应递增：%d / %d / %d",
			ec.Toc[0].Offset, ec.Toc[1].Offset, ec.Toc[2].Offset)
	}
}

// ---- 野格式 OPF 宽容兜底（2026-11-06 现场：整批 epubBuilder 书打不开）----

// fix1106LooseOPFBook 复刻现场样本：<package> 把 xmlns 拼成 mlns（根元素
// 无命名空间），但 manifest / spine / dc 元数据都完好。
func fix1106LooseOPFBook(t *testing.T, container string, encURI string) string {
	t.Helper()
	files := map[string]string{
		"mimetype": "application/epub+zip",
		"OPS/fb.opf": `<?xml version="1.0" encoding="UTF-8" ?>
<package version="2.0" unique-identifier="PrimaryID" mlns="http://www.idpf.org/2007/opf">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:opf="http://www.idpf.org/2007/opf">
<dc:title>野格式测试书名</dc:title>
<dc:creator>测试作者</dc:creator>
</metadata>
<manifest>
<item id="coverpage" href="coverpage.html" media-type="application/xhtml+xml"/>
<item id="chapter1" href="chapter1.html" media-type="application/xhtml+xml"/>
<item id="ncx" href="fb.ncx" media-type="application/x-dtbncx+xml"/>
</manifest>
<spine toc="ncx"><itemref idref="coverpage"/><itemref idref="chapter1"/></spine>
</package>`,
		"OPS/coverpage.html": `<html><body><div></div></body></html>`,
		"OPS/chapter1.html":  `<html><body><h1>第一章 正文</h1><p>这是野格式 EPUB 的正文内容。</p><h2 id="sec">1.1 小节</h2><p>小节内容。</p></body></html>`,
		"OPS/fb.ncx": `<?xml version="1.0"?><ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1"><navMap>
<navPoint id="n1"><navLabel><text>第一章 正文</text></navLabel><content src="chapter1.html"/></navPoint>
<navPoint id="n2"><navLabel><text>1.1 小节</text></navLabel><content src="chapter1.html#sec"/></navPoint>
</navMap></ncx>`,
	}
	switch container {
	case "ok":
		files["META-INF/container.xml"] = `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OPS/fb.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`
	case "broken":
		// 文件在、但没有 rootfile；pirmd 的 Open 不解析内容，可走到扫描兜底
		files["META-INF/container.xml"] = `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles></rootfiles></container>`
	}
	if encURI != "" {
		files["META-INF/encryption.xml"] = fmt.Sprintf(fix1106EncTmpl, fix1106AES, encURI)
	}
	return fix1106Pack(t, files)
}

// TestLooseOPFmlns 无命名空间 OPF 必须照常打开，元数据/正文/TOC/fragment
// 全链路可用（G4 的 fragment 定位在兜底路径上同样生效）。
func TestLooseOPFmlns(t *testing.T) {
	p := fix1106LooseOPFBook(t, "ok", "")
	ec, err := ParseEpub(p, "[图]")
	if err != nil {
		t.Fatalf("野格式 OPF 应可打开：%v", err)
	}
	if ec.Title != "野格式测试书名" || ec.Author != "测试作者" {
		t.Fatalf("兜底元数据丢失：title=%q author=%q", ec.Title, ec.Author)
	}
	if !strings.Contains(ec.Text, "这是野格式 EPUB 的正文内容") {
		t.Fatalf("正文提取失败：%q", ec.Text)
	}
	if len(ec.Toc) != 2 || ec.Toc[1].Offset <= ec.Toc[0].Offset {
		t.Fatalf("兜底路径的 TOC/fragment 异常：%+v", ec.Toc)
	}
	if !strings.HasPrefix(fix1106RuneAt(ec.Text, ec.Toc[1].Offset), "1.1 小节") {
		t.Fatalf("fragment 落点错：%q", fix1106RuneAt(ec.Text, ec.Toc[1].Offset))
	}
	if _, err := OpenEpub(p); err != nil {
		t.Fatalf("OpenEpub 应成功：%v", err)
	}
}

// TestLooseOPFBrokenContainer container.xml 在但缺 rootfile 时，
// 靠扫描 .opf 条目兜底。（container.xml 整个缺失时是 pirmd 的 epub.Open()
// 先拒绝，不在本层兜底范围。）
func TestLooseOPFBrokenContainer(t *testing.T) {
	p := fix1106LooseOPFBook(t, "broken", "")
	ec, err := ParseEpub(p, "[图]")
	if err != nil {
		t.Fatalf("container 缺 rootfile 时应靠扫描 .opf 兜底：%v", err)
	}
	if !strings.Contains(ec.Text, "这是野格式 EPUB 的正文内容") {
		t.Fatalf("正文提取失败：%q", ec.Text)
	}
}

// TestLooseOPFEncryptionStillRejected 兜底 opf 必须照样喂给 checkEncryption，
// 不能因为走了宽容解析就放松 DRM 红线（G5）。
func TestLooseOPFEncryptionStillRejected(t *testing.T) {
	p := fix1106LooseOPFBook(t, "ok", "OPS/chapter1.html")
	if _, err := ParseEpub(p, "[图]"); !errors.Is(err, errEncrypted) {
		t.Fatalf("野格式 + 真 DRM 仍应拒绝：%v", err)
	}
}

// TestStandardOPFUnchanged 标准命名空间的书仍走 pirmd 原路径，行为不变。
func TestStandardOPFUnchanged(t *testing.T) {
	p := buildTestEpub(t, true, false)
	e, err := epub.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if _, err := e.Package(); err != nil {
		t.Fatalf("标准 OPF 不应触发兜底路径：%v", err)
	}
	ec, err := ParseEpub(p, "[图]")
	if err != nil || len(ec.Toc) != 3 {
		t.Fatalf("标准 EPUB 行为变化：err=%v toc=%d", err, len(ec.Toc))
	}
}

// ---- G5：media-type ----

// TestG5MediaTypeMatrix 完整 ParseEpub 路径：判定只看 manifest 的 media-type，
// 与扩展名无关；算法字段不再能替正文条目开绿灯。
func TestG5MediaTypeMatrix(t *testing.T) {
	cases := []struct {
		name        string
		contentHref string
		contentMT   string
		encURI      string
		encAlgo     string
		wantDRM     bool
	}{
		{"标准 xhtml 被加密", "ch1.xhtml", "application/xhtml+xml", "OEBPS/ch1.xhtml", fix1106AES, true},
		{"非标准扩展名 .xht", "ch1.xht", "application/xhtml+xml", "OEBPS/ch1.xht", fix1106AES, true},
		{"无扩展名", "ch1", "application/xhtml+xml", "OEBPS/ch1", fix1106AES, true},
		{"扩展名 .xml 但 media-type 是正文", "ch1.xml", "application/xhtml+xml", "OEBPS/ch1.xml", fix1106AES, true},
		{"text/html 正文", "ch1.html", "text/html", "OEBPS/ch1.html", fix1106AES, true},
		{"media-type 带参数", "ch1.xhtml", "application/xhtml+xml; charset=utf-8", "OEBPS/ch1.xhtml", fix1106AES, true},
		{"URI 相对 OPF 目录", "ch1.xhtml", "application/xhtml+xml", "ch1.xhtml", fix1106AES, true},
		{"字体混淆算法却锁正文（异常样本必须拒）", "ch1.xhtml", "application/xhtml+xml", "OEBPS/ch1.xhtml", fix1106FontAlgo, true},
		{"正版字体混淆", "ch1.xhtml", "application/xhtml+xml", "OEBPS/fonts/a.ttf", fix1106FontAlgo, false},
		{"META-INF/rights.xml 被加密", "ch1.xhtml", "application/xhtml+xml", "META-INF/rights.xml", fix1106AES, false},
		{"OEBPS/toc.xml 被加密", "ch1.xhtml", "application/xhtml+xml", "OEBPS/toc.xml", fix1106AES, false},
		{"OEBPS/rights.xml 被加密", "ch1.xhtml", "application/xhtml+xml", "OEBPS/rights.xml", fix1106AES, false},
		{"encryption.xml 只列字体", "ch1.xhtml", "application/xhtml+xml", "OEBPS/fonts/a.ttf", fix1106AES, false},
		{"无 encryption.xml", "ch1.xhtml", "application/xhtml+xml", "", fix1106AES, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := fix1106EncBook(t, c.contentHref, c.contentMT, c.encURI, c.encAlgo)
			ec, err := ParseEpub(p, "[图]")
			isDRM := errors.Is(err, errEncrypted)
			if isDRM != c.wantDRM {
				t.Fatalf("判定不符：want DRM=%v got DRM=%v（err=%v）", c.wantDRM, isDRM, err)
			}
			if !c.wantDRM && (err != nil || ec == nil || !strings.Contains(ec.Text, "正文可读内容")) {
				t.Fatalf("放行的书正文应完整可读：err=%v ec=%+v", err, ec)
			}
		})
	}
}

// TestG5FallbackNoManifest manifest 没有内容文档条目时退回扩展名兜底，
// 且 .xml 不再被误判为正文。
func TestG5FallbackNoManifest(t *testing.T) {
	docs := map[string]bool{} // 模拟 manifest 里没有 xhtml 条目
	cases := []struct {
		uri  string
		want bool
	}{
		{"OEBPS/ch1.xhtml", true},
		{"OEBPS/ch1.html", true},
		{"OEBPS/ch1.xht", true},
		{"META-INF/rights.xml", false}, // G5 修复点：旧口径误判 true
		{"OEBPS/toc.xml", false},
		{"OEBPS/fonts/a.ttf", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isEncryptedContentDoc(c.uri, docs, "/OEBPS"); got != c.want {
			t.Fatalf("兜底判定 %q：want %v got %v", c.uri, c.want, got)
		}
	}
	if isContentDocURI("META-INF/rights.xml") {
		t.Fatal("G5 回归：.xml 仍被当内容文档（非正文 XML 误杀）")
	}
}

// TestG5RealDRMAndFontObfuscation 沿用仓库既有样本，确认 F1 修复未被 G5 改坏。
func TestG5RealDRMAndFontObfuscation(t *testing.T) {
	pFont := buildEncEpub(t, false)
	if _, err := ParseEpub(pFont, "[图]"); err != nil {
		t.Fatalf("字体混淆书应放行：%v", err)
	}
	pDRM := buildEncEpub(t, true)
	if _, err := ParseEpub(pDRM, "[图]"); !errors.Is(err, errEncrypted) {
		t.Fatalf("真 DRM 应拒绝：%v", err)
	}
}
