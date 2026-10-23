package reader

// EPUB 支持：解包 → 提纯文本 → 复用 TXT 管线（方案《EPUB支持方案_2026-10-23.md》）。
// 路线拍板：A1 pirmd/epub 容器解析 + B1 内存流 + C1 [图] 占位 + D1 规则库回落。
//
// 职责边界：
//   - 本文件只做「EPUB → 纯文本 + TOC(offset)」的纯函数转换，不碰 Book/UI；
//   - 容器/OPF/spine/元数据用 pirmd/epub；XHTML→文本用 x/net/html 自写；
//   - zip 条目读取自实现（path 包 POSIX 口径）——pirmd 的 OpenItem 在 Windows
//     上用 filepath 拼路径（反斜杠），与 zip 条目名的正斜杠对不上，有坑不用。

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/pirmd/epub"
	xhtml "golang.org/x/net/html"
)

// errEncrypted EPUB 带 DRM 加密（META-INF/encryption.xml），明确不支持。
var errEncrypted = errors.New("该 EPUB 已加密（DRM 保护），暂不支持阅读")

// EpubContent 解包后的 EPUB 纯文本内容。
type EpubContent struct {
	Title  string     // OPF 元数据书名（读不到为空）
	Author string     // OPF 元数据作者（读不到为空）
	Text   string     // 全文纯文本（spine 阅读顺序，UTF-8）
	Toc    []TocEntry // Offset = Text 内 rune 偏移；Page 由调用方按 pageSize 填
}

// tocEntryRaw 解析出的原始目录条目（href 未映射到正文偏移）。
type tocEntryRaw struct {
	title string
	href  string
	level int
}

// ParseEpub 解包 EPUB：spine 顺序提取纯文本 + 解析 TOC 并映射到正文偏移。
// imgPlaceholder 非空时，图片位置插入该占位符（C1 拍板："[图]"）。
func ParseEpub(epubPath string, imgPlaceholder string) (*EpubContent, error) {
	e, err := epub.Open(epubPath)
	if err != nil {
		return nil, fmt.Errorf("无法打开 EPUB（zip 结构损坏？）：%w", err)
	}
	defer e.Close()

	// 加密检测：pirmd 不做，自己扫（有 encryption.xml 一律视为 DRM/加密）
	for _, f := range e.File {
		if strings.EqualFold(f.Name, "META-INF/encryption.xml") {
			return nil, errEncrypted
		}
	}

	opf, err := e.Package()
	if err != nil {
		return nil, fmt.Errorf("EPUB 结构损坏（OPF 解析失败）：%w", err)
	}
	if opf.Spine == nil || len(opf.Spine.Itemrefs) == 0 {
		return nil, errors.New("EPUB 缺少 spine（无阅读顺序），无法打开")
	}
	opfDir, err := findOPFDir(e)
	if err != nil {
		return nil, err
	}

	// manifest：id → 条目；顺便定位 NCX（EPUB2）与 nav（EPUB3）
	type mItem struct{ href, mediaType, properties string }
	manifest := make(map[string]mItem, len(opf.Manifest.Items))
	ncxHref, navHref := "", ""
	for _, it := range opf.Manifest.Items {
		manifest[it.ID] = mItem{href: it.Href, mediaType: it.MediaType, properties: it.Properties}
		if it.MediaType == "application/x-dtbncx+xml" {
			ncxHref = it.Href
		}
		if strings.Contains(it.Properties, "nav") && strings.EqualFold(it.MediaType, "application/xhtml+xml") {
			navHref = it.Href
		}
	}
	if opf.Spine.Toc != "" { // EPUB2 spine toc 属性优先
		if it, ok := manifest[opf.Spine.Toc]; ok {
			ncxHref = it.href
		}
	}

	// ---- spine 顺序提取纯文本，记录每个条目的起始 rune 偏移 ----
	ec := &EpubContent{}
	if info, err := e.Information(); err == nil && info != nil {
		if len(info.Title) > 0 {
			ec.Title = strings.TrimSpace(info.Title[0])
		}
		if len(info.Creator) > 0 {
			ec.Author = strings.TrimSpace(info.Creator[0].FullName)
		}
	}

	var sb strings.Builder
	runes := 0
	itemStart := make(map[string]int, len(opf.Spine.Itemrefs))
	n := 0
	for _, ref := range opf.Spine.Itemrefs {
		it, ok := manifest[ref.IDref]
		if !ok {
			continue // spine 引用了 manifest 外的条目：跳过（野格式宽容）
		}
		mt := strings.ToLower(it.mediaType)
		if mt != "application/xhtml+xml" && mt != "text/html" {
			continue
		}
		raw, err := readZipItem(e, opfDir, it.href)
		if err != nil {
			continue // 单章读取失败：跳过，宁缺勿崩
		}
		txt := strings.TrimSpace(htmlToText(strings.NewReader(raw), imgPlaceholder))
		if txt == "" {
			continue // 空章节（封面页等）不入正文流
		}
		if n > 0 {
			sb.WriteString("\n\n")
			runes += 2
		}
		itemStart[itemKey(opfDir, it.href)] = runes
		sb.WriteString(txt)
		runes += utf8.RuneCountInString(txt)
		n++
	}
	if n == 0 {
		return nil, errors.New("EPUB 内没有可读的正文内容")
	}
	ec.Text = sb.String()

	// ---- TOC：EPUB3 nav 优先，EPUB2 NCX 兜底 ----
	// 目录 href 相对于其所属文件（nav.xhtml / toc.ncx）所在目录，归一到与
	// spine 条目相同的键口径后映射到正文偏移。
	var tocRaw []tocEntryRaw
	baseDir := ""
	if navHref != "" {
		tocRaw = parseNav(e, opfDir, navHref)
		baseDir = path.Dir(normHref(navHref))
	}
	if len(tocRaw) == 0 && ncxHref != "" {
		tocRaw = parseNCX(e, opfDir, ncxHref)
		baseDir = path.Dir(normHref(ncxHref))
	}
	for _, t := range tocRaw {
		// E1（2026-10-23 审查）：baseDir 已是 TOC 文件所在目录（根化），直接拼
		// href 即可——此前多套一层 path.Dir 把 TOC 自身目录剥掉，TOC 文件在
		// OPF 子目录时条目静默全丢（同目录形态恰好抵消，自测没暴露）。
		key := itemKey(opfDir, path.Join(baseDir, t.href))
		if off, ok := itemStart[key]; ok {
			ec.Toc = append(ec.Toc, TocEntry{
				Title: strings.TrimSpace(t.title), Offset: off, Level: t.level,
			})
		}
	}
	return ec, nil
}

// itemKey 归一条目键：href 相对 opfDir，统一为根化 POSIX 路径。
// rel 可以是 opf 相对路径，也可以是已根化的路径（path.Join 幂等处理）。
func itemKey(opfDir, rel string) string {
	return path.Clean(path.Join(opfDir, normHref(rel)))
}

// findOPFDir 从 META-INF/container.xml 解出 OPF 所在目录（根化，如 /OEBPS）。
func findOPFDir(e *epub.Epub) (string, error) {
	for _, f := range e.File {
		if normZipName(f.Name) != "/META-INF/container.xml" {
			continue
		}
		r, err := f.Open()
		if err != nil {
			return "", err
		}
		defer r.Close()
		b, err := io.ReadAll(r)
		if err != nil {
			return "", err
		}
		var c struct {
			Rootfiles struct {
				Rootfile []struct {
					FullPath string `xml:"full-path,attr"`
				} `xml:"rootfile"`
			} `xml:"rootfiles"`
		}
		if err := xml.Unmarshal(b, &c); err != nil || len(c.Rootfiles.Rootfile) == 0 {
			return "", errors.New("container.xml 缺少 rootfile")
		}
		return path.Clean("/" + path.Dir(c.Rootfiles.Rootfile[0].FullPath)), nil
	}
	return "", errors.New("EPUB 缺少 META-INF/container.xml")
}

// ---- XHTML → 纯文本 ----

// blockTags 块级元素：进出都冲刷当前行，保证块与块换行分隔。
var blockTags = map[string]bool{
	"p": true, "div": true, "h1": true, "h2": true, "h3": true, "h4": true,
	"h5": true, "h6": true, "li": true, "ul": true, "ol": true, "blockquote": true,
	"table": true, "tr": true, "td": true, "th": true, "section": true,
	"article": true, "header": true, "footer": true, "figure": true,
	"figcaption": true, "pre": true, "hr": true, "dd": true, "dt": true,
	"dl": true, "main": true, "aside": true, "center": true, "body": true,
}

// htmlToText XHTML → 纯文本：块级元素换行分隔、<br> 换行、script/style/head
// 整块跳过、img 插占位符、行内空白折叠、实体由解析器解码。
func htmlToText(r io.Reader, imgPlaceholder string) string {
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
				return // void 元素，无子节点
			case "svg":
				if imgPlaceholder != "" {
					line = append(line, []rune(imgPlaceholder)...)
				}
				return // 内嵌矢量图整块跳过
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

// ---- TOC 解析 ----

// parseNCX 解析 EPUB2 toc.ncx（标准 XML，stdlib 即可），递归展平 navPoint。
func parseNCX(e *epub.Epub, opfDir, ncxHref string) []tocEntryRaw {
	raw, err := readZipItem(e, opfDir, ncxHref)
	if err != nil {
		return nil
	}
	var doc struct {
		NavMap struct {
			Points []ncxPoint `xml:"navPoint"`
		} `xml:"navMap"`
	}
	if err := xml.Unmarshal([]byte(raw), &doc); err != nil {
		return nil
	}
	var flat []tocEntryRaw
	var walk func(points []ncxPoint, level int)
	walk = func(points []ncxPoint, level int) {
		for _, p := range points {
			title := strings.TrimSpace(p.Text)
			if title != "" && p.Content.Src != "" {
				flat = append(flat, tocEntryRaw{title: title, href: p.Content.Src, level: level})
			}
			if len(p.Points) > 0 {
				walk(p.Points, level+1)
			}
		}
	}
	walk(doc.NavMap.Points, 0)
	return flat
}

type ncxPoint struct {
	Text    string     `xml:"navLabel>text"`
	Content ncxContent `xml:"content"`
	Points  []ncxPoint `xml:"navPoint"`
}

// ncxContent 单独一层取属性——encoding/xml 不支持「元素路径+属性」链式标签
// （content[src],attr 静默返回空，2026-10-23 真实样本探针实锤），必须拆两层。
type ncxContent struct {
	Src string `xml:"src,attr"`
}

// parseNav 解析 EPUB3 nav.xhtml（properties="nav"）：取第一个 <nav>，
// 递归展平 ol>li>a 嵌套。
func parseNav(e *epub.Epub, opfDir, navHref string) []tocEntryRaw {
	raw, err := readZipItem(e, opfDir, navHref)
	if err != nil {
		return nil
	}
	doc, err := xhtml.Parse(strings.NewReader(raw))
	if err != nil {
		return nil
	}
	var nav *xhtml.Node
	var findNav func(n *xhtml.Node)
	findNav = func(n *xhtml.Node) {
		if nav != nil {
			return
		}
		if n.Type == xhtml.ElementNode && n.Data == "nav" {
			nav = n
			return
		}
		for c := n.FirstChild; c != nil && nav == nil; c = c.NextSibling {
			findNav(c)
		}
	}
	findNav(doc)
	if nav == nil {
		return nil
	}
	var flat []tocEntryRaw
	var walkList func(ol *xhtml.Node, level int)
	walkList = func(ol *xhtml.Node, level int) {
		for c := ol.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != xhtml.ElementNode || c.Data != "li" {
				continue
			}
			var a, nested *xhtml.Node
			for ch := c.FirstChild; ch != nil; ch = ch.NextSibling {
				if ch.Type != xhtml.ElementNode {
					continue
				}
				if ch.Data == "a" && a == nil {
					a = ch
				}
				if ch.Data == "ol" && nested == nil {
					nested = ch
				}
			}
			if a != nil {
				title := strings.TrimSpace(textOf(a))
				href := attrOf(a, "href")
				if title != "" && href != "" {
					flat = append(flat, tocEntryRaw{title: title, href: href, level: level})
				}
			}
			if nested != nil {
				walkList(nested, level+1)
			}
		}
	}
	// nav 下可能直接是 ol，也可能隔一层 div
	var findOL func(n *xhtml.Node) *xhtml.Node
	findOL = func(n *xhtml.Node) *xhtml.Node {
		if n.Type == xhtml.ElementNode && n.Data == "ol" {
			return n
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if got := findOL(c); got != nil {
				return got
			}
		}
		return nil
	}
	if ol := findOL(nav); ol != nil {
		walkList(ol, 0)
	}
	return flat
}

func textOf(n *xhtml.Node) string {
	var sb strings.Builder
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.TextNode {
			sb.WriteString(n.Data)
			return
		}
		if n.Type == xhtml.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(sb.String()), " ")
}

func attrOf(n *xhtml.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// ---- zip 条目读取（POSIX 路径口径，绕开 pirmd OpenItem 的 filepath 坑）----

// readZipItem 按 href 读取 zip 内条目：unescape → 剥 fragment → 以 OPF 目录
// 为基准用 path（正斜杠）拼绝对条目名，再在 e.File 里精确匹配。
func readZipItem(e *epub.Epub, opfDir, href string) (string, error) {
	if href == "" {
		return "", errors.New("empty href")
	}
	name := itemKey(opfDir, href) // 形如 /OEBPS/ch1.xhtml
	var found *zip.File
	for i := range e.File {
		if normZipName(e.File[i].Name) == name {
			found = e.File[i]
			break
		}
	}
	if found == nil {
		return "", fmt.Errorf("zip 内找不到条目 %s", name)
	}
	r, err := found.Open()
	if err != nil {
		return "", err
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// normHref 归一 href：剥 fragment → URL 解码 → 以根开头的 POSIX 路径。
func normHref(h string) string {
	if i := strings.IndexByte(h, '#'); i >= 0 {
		h = h[:i]
	}
	if d, err := url.PathUnescape(h); err == nil {
		h = d
	}
	return path.Clean("/" + h)
}

// normZipName zip 条目名归一到与 normHref 同一口径。
func normZipName(name string) string {
	if d, err := url.PathUnescape(name); err == nil {
		name = d
	}
	return path.Clean("/" + name)
}
