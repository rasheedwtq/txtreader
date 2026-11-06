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
	"unicode"
	"unicode/utf8"

	"github.com/pirmd/epub"
	xhtml "golang.org/x/net/html"
)

// errEncrypted EPUB 正文被 DRM 加密，明确不支持。
// 注：META-INF/encryption.xml 还有合法的字体混淆用途（见 checkEncryption），
// 不能见文件就拒（F1，2026-10-26 审查）。
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

	// G5（2026-11-06）：加密检测要按 OPF manifest 的 media-type 判「内容文档」
	// （EPUB 规范里扩展名可以任意），所以 Package / findOPFDir 必须先跑完。
	// 顺序变化只影响「OPF 本身损坏且带 encryption.xml」的野书报错措辞。
	opf, err := e.Package()
	looseTitle, looseAuthor := "", ""
	if err != nil {
		// 宽容兜底（2026-11-06 现场）：epubBuilder 2.5.x 等老式生成器把
		// xmlns 拼成 mlns，<package> 没有命名空间；pirmd 的 Package() 严格
		// 校验命名空间会直接失败。这类书的 manifest/spine 本身是好的，只是
		// 根元素命名空间不合规（书库抽样 600 本里 598 本是这个形态），
		// 用不约束命名空间的自解析补上，让整批书照常可读。
		opfRel, pErr := findOPFPath(e)
		if pErr == nil {
			var t, a string
			opf, t, a, err = parseOPFLoose(e, opfRel)
			if err == nil {
				looseTitle, looseAuthor = t, a
			}
		}
		if err != nil {
			return nil, fmt.Errorf("EPUB 结构损坏（OPF 解析失败）：%w", err)
		}
	}
	if opf.Spine == nil || len(opf.Spine.Itemrefs) == 0 {
		return nil, errors.New("EPUB 缺少 spine（无阅读顺序），无法打开")
	}
	opfDir, err := findOPFDir(e)
	if err != nil {
		return nil, err
	}
	// 只有「内容文档被加密」才报 DRM；字体混淆 / 图片 / NCX 等资源被加密
	// 不影响正文阅读，一律放行。
	if err := checkEncryption(e, opf, opfDir); err != nil {
		return nil, err
	}

	// manifest：id → 条目；顺便定位 NCX（EPUB2）与 nav（EPUB3）
	type mItem struct{ href, mediaType, properties string }
	manifest := make(map[string]mItem, len(opf.Manifest.Items))
	ncxHref, navHref := "", ""
	for _, it := range opf.Manifest.Items {
		// media-type 允许带 "; charset=utf-8" 之类参数，取分号前的主类型。
		// 否则 spine 提取会把这类合法正文条目整个跳过（G5 顺带修复）。
		mt := strings.ToLower(strings.TrimSpace(strings.SplitN(it.MediaType, ";", 2)[0]))
		manifest[it.ID] = mItem{href: it.Href, mediaType: mt, properties: it.Properties}
		if mt == "application/x-dtbncx+xml" {
			ncxHref = it.Href
		}
		if strings.Contains(it.Properties, "nav") && mt == "application/xhtml+xml" {
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
	// 兜底路径下 pirmd 的 Information() 也会因命名空间失败，书名/作者用
	// 自解析 OPF 的 dc:title / dc:creator 补上。
	if ec.Title == "" {
		ec.Title = looseTitle
	}
	if ec.Author == "" {
		ec.Author = looseAuthor
	}

	var sb strings.Builder
	runes := 0
	itemStart := make(map[string]int, len(opf.Spine.Itemrefs))
	itemAnchors := make(map[string]map[string]int, len(opf.Spine.Itemrefs)) // G4：文件键 → id → 章节内 rune 偏移
	// G6：空章节先暂存，spine 扫完再决定登记与否（单遍扫描时不知道后面
	// 还有没有正文）。文末空章节若登记，offset 会等于 len(Text)，SeekTo
	// 钳回首页 → 目录里留个点了没反应、与首章重合的假条目。
	pendingEmpty := make(map[string]int)
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
		key := itemKey(opfDir, it.href)
		rawText, anchors := htmlToTextWithAnchors(strings.NewReader(raw), imgPlaceholder)
		txt := strings.TrimSpace(rawText)
		// G4：锚点偏移按 htmlToText 的原始输出记录，TrimSpace 削掉的前导空白
		// 必须同步扣掉，否则 fragment 落点整体前移。
		if lead := utf8.RuneCountInString(rawText) - utf8.RuneCountInString(strings.TrimLeftFunc(rawText, unicode.IsSpace)); lead > 0 {
			for id, off := range anchors {
				if off > lead {
					anchors[id] = off - lead
				} else {
					anchors[id] = 0
				}
			}
		}
		if txt == "" {
			// F4（2026-10-26）：空章节（封面/扉页）也要让 TOC 条目有落点。
			// G6：但登记要推迟到扫描结束——只有「其后还有正文写入」才有效。
			pendingEmpty[key] = runes
			continue // 空章节不入正文流
		}
		if n > 0 {
			sb.WriteString("\n\n")
			runes += 2
		}
		itemStart[key] = runes
		itemAnchors[key] = anchors
		sb.WriteString(txt)
		runes += utf8.RuneCountInString(txt)
		n++
	}
	if n == 0 {
		return nil, errors.New("EPUB 内没有可读的正文内容")
	}
	ec.Text = sb.String()
	// G6：首部/中间空章节（off < runes）按 F4 保留，落在下一段实际内容；
	// 尾部空章节（off == runes）丢弃条目，避免出现指向文末之后的假目录项。
	for key, off := range pendingEmpty {
		if off < runes {
			itemStart[key] = off
		}
	}

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
		// G4（2026-11-06）：fragment 必须在这一层单独拆。normHref 被 itemKey
		// 与 readZipItem 共用，改成保留 # 会让 E1 口径回潮；这里拆完再按
		// 「章节起点 + id 在章节内的偏移」精确定位，找不到 id 时退回章首。
		hrefPath, frag := splitHref(t.href)
		key := itemKey(opfDir, path.Join(baseDir, hrefPath))
		off, ok := itemStart[key]
		if !ok {
			continue
		}
		if frag != "" {
			if a, ok := itemAnchors[key][frag]; ok {
				off += a
			}
		}
		ec.Toc = append(ec.Toc, TocEntry{
			Title: strings.TrimSpace(t.title), Offset: off, Level: t.level,
		})
	}
	return ec, nil
}

// itemKey 归一条目键：href 相对 opfDir，统一为根化 POSIX 路径。
// rel 可以是 opf 相对路径，也可以是已根化的路径（path.Join 幂等处理）。
func itemKey(opfDir, rel string) string {
	return path.Clean(path.Join(opfDir, normHref(rel)))
}

// splitHref 拆出 fragment（G4）。只在这里拆：路径部分仍交给 normHref 做
// 解码 + 根化，fragment 单独解码；normHref / itemKey / readZipItem 的口径
// 一个字都不动，避免 E1 的 OPF 子目录回归。
func splitHref(h string) (string, string) {
	if i := strings.IndexByte(h, '#'); i >= 0 {
		frag := h[i+1:]
		if d, err := url.PathUnescape(frag); err == nil {
			frag = d
		}
		return h[:i], frag
	}
	return h, ""
}

// checkEncryption 分析 META-INF/encryption.xml：只对「内容文档被加密」报
// errEncrypted。G5（2026-11-06）：内容文档按 OPF manifest 的 media-type 判定，
// 不再看扩展名——规范允许正文用 .xht / 无扩展名，也允许非正文资源叫 .xml
// （旧口径把 .xml 一律当正文，rights.xml / toc.xml 被加密时正文可读却整本
// 拒绝）。算法字段不再参与判定：字体混淆的合法性由「目标不是内容文档」保证，
// 把字体混淆算法写在正文条目上的异常样本必须拒绝。
// 无 encryption.xml / 解析失败（野格式）/ manifest 无内容文档条目 → 放行或
// 退回扩展名兜底，交给正文提取自然报错。
func checkEncryption(e *epub.Epub, opf *epub.PackageDocument, opfDir string) error {
	for _, f := range e.File {
		if normZipName(f.Name) != "/META-INF/encryption.xml" {
			continue
		}
		r, err := f.Open()
		if err != nil {
			return nil // 读不出就当没有，别把可读书误杀
		}
		b, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			return nil
		}
		var doc struct {
			EncryptedData []struct {
				CipherData struct {
					CipherReference struct {
						URI string `xml:"URI,attr"`
					} `xml:"CipherReference"`
				} `xml:"CipherData"`
			} `xml:"EncryptedData"`
		}
		if err := xml.Unmarshal(b, &doc); err != nil {
			return nil // 野格式解析失败：放行
		}
		contentDocs := manifestContentDocs(opf, opfDir)
		for _, ed := range doc.EncryptedData {
			if isEncryptedContentDoc(ed.CipherData.CipherReference.URI, contentDocs, opfDir) {
				return errEncrypted
			}
		}
		return nil // 有 encryption.xml 但没锁正文：放行
	}
	return nil
}

// manifestContentDocs 收集 OPF manifest 里 media-type 为内容文档的条目键。
// media-type 可能带 "; charset=utf-8" 之类参数，取分号前的主类型。
func manifestContentDocs(opf *epub.PackageDocument, opfDir string) map[string]bool {
	keys := make(map[string]bool)
	if opf == nil {
		return keys
	}
	for _, it := range opf.Manifest.Items {
		mt := strings.ToLower(strings.TrimSpace(strings.SplitN(it.MediaType, ";", 2)[0]))
		if mt != "application/xhtml+xml" && mt != "text/html" {
			continue
		}
		keys[itemKey(opfDir, it.Href)] = true
	}
	return keys
}

// isEncryptedContentDoc 判断 CipherReference 是否指向内容文档。
// 规范上 URI 相对包根；少数生成器写成相对 OPF 目录，两种口径都试。
// manifest 里没有内容文档条目时退回扩展名兜底（野格式容错）。
func isEncryptedContentDoc(uri string, contentDocs map[string]bool, opfDir string) bool {
	if uri == "" {
		return false
	}
	if len(contentDocs) > 0 {
		if contentDocs[normHref(uri)] {
			return true
		}
		return contentDocs[itemKey(opfDir, uri)]
	}
	return isContentDocURI(uri)
}

// isContentDocURI 扩展名兜底（仅在 manifest 无内容文档条目时使用）。
// 不含 .xml：NCX / rights.xml 等非正文 XML 不能仅凭扩展名误判为正文。
func isContentDocURI(uri string) bool {
	if uri == "" {
		return false
	}
	ext := strings.ToLower(path.Ext(normHref(uri)))
	return ext == ".xhtml" || ext == ".html" || ext == ".htm" || ext == ".xht"
}

// findOPFPath 定位 OPF（返回包根相对路径，如 "OPS/fb.opf"）。
// 优先 container.xml 的 rootfile；缺失/损坏时退而扫描 zip 里的 .opf 条目
// （老式包常没有 container.xml，或路径写法不规范）。
func findOPFPath(e *epub.Epub) (string, error) {
	for _, f := range e.File {
		if normZipName(f.Name) != "/META-INF/container.xml" {
			continue
		}
		r, err := f.Open()
		if err != nil {
			break
		}
		b, readErr := io.ReadAll(r)
		r.Close()
		if readErr == nil {
			var c struct {
				Rootfiles struct {
					Rootfile []struct {
						FullPath string `xml:"full-path,attr"`
					} `xml:"rootfile"`
				} `xml:"rootfiles"`
			}
			if xml.Unmarshal(b, &c) == nil && len(c.Rootfiles.Rootfile) > 0 {
				if fp := strings.TrimSpace(c.Rootfiles.Rootfile[0].FullPath); fp != "" {
					return fp, nil
				}
			}
		}
		break
	}
	for _, f := range e.File {
		n := normZipName(f.Name)
		if strings.HasPrefix(strings.ToLower(n), "/meta-inf/") {
			continue
		}
		if strings.EqualFold(path.Ext(n), ".opf") {
			return strings.TrimPrefix(n, "/"), nil
		}
	}
	return "", errors.New("EPUB 缺少可识别的 OPF（container.xml 与 .opf 条目都找不到）")
}

// findOPFDir 从 OPF 路径解出所在目录（根化，如 /OPS）。
func findOPFDir(e *epub.Epub) (string, error) {
	full, err := findOPFPath(e)
	if err != nil {
		return "", err
	}
	return path.Clean("/" + path.Dir(full)), nil
}

// looseOPF 不约束命名空间地解析 OPF。Go 的 encoding/xml 对未指定命名空间
// 的字段标签会匹配任意命名空间的同名元素，所以 dc:title 与裸 title、
// 带前缀/不带前缀的 manifest 都能解析。
type looseOPF struct {
	Metadata struct {
		Title   []string `xml:"title"`
		Creator []string `xml:"creator"`
	} `xml:"metadata"`
	Manifest struct {
		Items []struct {
			ID         string `xml:"id,attr"`
			Href       string `xml:"href,attr"`
			MediaType  string `xml:"media-type,attr"`
			Properties string `xml:"properties,attr"`
		} `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		Toc      string `xml:"toc,attr"`
		Itemrefs []struct {
			IDref string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

// parseOPFLoose 构造与 pirmd 等价的 PackageDocument，并顺带返回
// dc:title / dc:creator（失败路径下 e.Information() 同样不可用）。
func parseOPFLoose(e *epub.Epub, opfPath string) (*epub.PackageDocument, string, string, error) {
	raw, err := readZipItem(e, "/", opfPath)
	if err != nil {
		return nil, "", "", err
	}
	var doc looseOPF
	if err := xml.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, "", "", err
	}
	if len(doc.Manifest.Items) == 0 || len(doc.Spine.Itemrefs) == 0 {
		return nil, "", "", errors.New("OPF 缺少 manifest 或 spine")
	}
	opf := &epub.PackageDocument{
		Manifest: &epub.Manifest{},
		Spine:    &epub.Spine{},
	}
	for _, it := range doc.Manifest.Items {
		opf.Manifest.Items = append(opf.Manifest.Items, epub.Item{
			ID: it.ID, Href: it.Href, MediaType: it.MediaType, Properties: it.Properties,
		})
	}
	opf.Spine.Toc = doc.Spine.Toc
	for _, r := range doc.Spine.Itemrefs {
		opf.Spine.Itemrefs = append(opf.Spine.Itemrefs, epub.Itemref{IDref: r.IDref})
	}
	title, author := "", ""
	if len(doc.Metadata.Title) > 0 {
		title = strings.TrimSpace(doc.Metadata.Title[0])
	}
	if len(doc.Metadata.Creator) > 0 {
		author = strings.TrimSpace(doc.Metadata.Creator[0])
	}
	return opf, title, author, nil
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
	text, _ := htmlToTextWithAnchors(r, imgPlaceholder)
	return text
}

// htmlToTextWithAnchors 提纯文本的同时记录 id / name → 该文档内的 rune 偏移。
// G4（2026-11-06）：EPUB3 nav / EPUB2 NCX 的目录条目常带 #fragment，只按
// 文件键映射会把同一章的小节条目全挤到章首。锚点记在「元素文本开始处」，
// 口径与最终输出一致（当前行按 flush 时的折叠规则折算），调用方再加上章节
// 在全文中的起点即可。重复 id 取第一次出现。
func htmlToTextWithAnchors(r io.Reader, imgPlaceholder string) (string, map[string]int) {
	doc, err := xhtml.Parse(r)
	if err != nil {
		return "", nil
	}
	anchors := make(map[string]int)
	var sb strings.Builder
	var line []rune
	written := 0 // 已 flush 进 sb 的 rune 数
	// curOff：若此刻把 line 冲刷出去，其在最终输出文本里的 rune 偏移。
	curOff := func() int {
		return written + utf8.RuneCountInString(strings.Join(strings.Fields(string(line)), " "))
	}
	record := func(n *xhtml.Node) {
		for _, key := range []string{"id", "name"} {
			id := strings.TrimSpace(attrOf(n, key))
			if id == "" {
				continue
			}
			if _, exists := anchors[id]; !exists {
				anchors[id] = curOff()
			}
		}
	}
	flush := func() {
		t := strings.Join(strings.Fields(string(line)), " ")
		if t != "" {
			sb.WriteString(t)
			sb.WriteByte('\n')
			written += utf8.RuneCountInString(t) + 1
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
				record(n) // 图片锚点指向 [图] 占位符之前
				if imgPlaceholder != "" {
					line = append(line, []rune(imgPlaceholder)...)
				}
				return // void 元素，无子节点
			case "svg":
				record(n)
				if imgPlaceholder != "" {
					line = append(line, []rune(imgPlaceholder)...)
				}
				return // 内嵌矢量图整块跳过
			}
			isBlock := blockTags[n.Data]
			if isBlock {
				flush()
			}
			record(n)
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
	return sb.String(), anchors
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

// parseNav 解析 EPUB3 nav.xhtml（properties="nav"）：优先取 <nav epub:type="toc">，
// 全不中再退回第一个 <nav>（F2，2026-10-26 探针实锤：landmarks/page-list 排在
// toc 之前时旧逻辑取错 nav，目录栏变成地标条目）。取到后递归展平 ol>li>a 嵌套。
func parseNav(e *epub.Epub, opfDir, navHref string) []tocEntryRaw {
	raw, err := readZipItem(e, opfDir, navHref)
	if err != nil {
		return nil
	}
	doc, err := xhtml.Parse(strings.NewReader(raw))
	if err != nil {
		return nil
	}
	var tocNav, firstNav *xhtml.Node
	var findNav func(n *xhtml.Node)
	findNav = func(n *xhtml.Node) {
		if tocNav != nil {
			return
		}
		if n.Type == xhtml.ElementNode && n.Data == "nav" {
			if firstNav == nil {
				firstNav = n
			}
			// epub:type 可能是 "toc" 或 "toc xxx"（空格分隔多值）；HTML 解析器
			// 保留字面前缀属性名（epub:type），两种写法都认。
			if v := strings.ToLower(attrOf(n, "epub:type")); v != "" {
				for _, f := range strings.Fields(v) {
					if f == "toc" {
						tocNav = n
						return
					}
				}
			}
		}
		for c := n.FirstChild; c != nil && tocNav == nil; c = c.NextSibling {
			findNav(c)
		}
	}
	findNav(doc)
	nav := tocNav
	if nav == nil {
		nav = firstNav // 无 epub:type="toc"：退回第一个 nav（旧格式容错）
	}
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
