package reader

import (
	"bufio"
	"io"
	"os"
	"regexp"
	"strings"

	"golang.org/x/text/encoding"
	"golang.org/x/text/transform"
)

// TocEntry 目录项：章节标题、所在页码（0 起）、标题首字符的全局 rune 偏移。
// Offset 用于页内精确定位，支持同页多章节分别跳转。
// Level 层级占位：0=章 1=卷（本次不做分层 UI，字段先行）。
type TocEntry struct {
	Title  string
	Page   int
	Offset int
	Level  int
}

// ---- 章节规则库（数据驱动，规则切片顺序即优先级）----
// 来源：原有 8 条规则 + legado txtTocRule / TRNovel toc-rules 定向移植（方案
// 《章节识别规则库与TXT清洗方案_2026-10-22.md》§1）。原则：
//   - 规则内嵌编译，不引外部 JSON（单文件便携优先于用户可配置）；
//   - 宁多勿漏：排除类只堵实锤误判（计数词/句号结尾），不追求识别准确率；
//   - 首字符分流：trigger 集之外的行直接短路，绝大多数正文行碰不到正则。

// tocRule 单条识别规则；level 0=章 1=卷。
type tocRule struct {
	name  string
	re    *regexp.Regexp
	level int
}

// 中/英文数字字符类（含财务大写：壹贰叁…仟萬，legado 支持，书库网文出现过）。
const cnNumClass = `0-9零〇一二三四五六七八九十百千两壹贰叁肆伍陆柒捌玖拾佰仟萬`

var (
	// ---- 排除类：命中即否决（抗误报，先于识别规则）----
	// 计数词歧义：『第三部分/第三节课/第五部队』不是章节（TRNovel）。
	reExcludeCount = regexp.MustCompile(`^第?[` + cnNumClass + `]{1,8}(?:部分|部门|部队|节课|季度|集合|步骤)`)
	// 句号结尾：『第二章写完了。』是正文（TRNovel：忽略以句号结尾的行）。
	// 只排全角句号；半角 '.' 会误杀『Chapter 3.』类标题。
	reExcludePeriod = regexp.MustCompile(`[。．]$`)

	// ---- 强规则：不受行首全角缩进影响（『　　第十二章 xxx』仍是章节）----
	reMD = regexp.MustCompile(`^#{1,6}\s+\S`)
	// 英文：chapter/ch./section/part/vol./book + 数字/罗马数字
	reEN = regexp.MustCompile(`(?i)^(?:chapter|ch\.?|section|part|vol\.?|book)\s+[0-9ivxlc]+`)
	// 中文数字章节：第X章/回/节/篇/集
	reCNNum = regexp.MustCompile(`^第\s*[` + cnNumClass + `]+\s*[章回节篇集]`)
	// 分卷：第X卷/部（level=1）
	reVolume = regexp.MustCompile(`^第\s*[` + cnNumClass + `]+\s*[卷部]`)
	// 中文关键词（前缀匹配，配合 40 字上限防『前言往事如烟…』灌目录）
	reCNKeyword = regexp.MustCompile(`^(?:序章|序言|序|前言|引言|引子|楔子|后记|附录|番外|外传|终章|尾声|跋)`)
	// 上/中/下卷
	reVolUpDown = regexp.MustCompile(`^(?:上|中|下)(?:卷|部)$`)

	// ---- 弱规则：原始行首全角缩进时冻结（D1 拍板）----
	// 『　　12 年前，他还是个孩子』是正文段，不是章节。
	reMDStrip   = regexp.MustCompile(`^#{1,6}\s+`)
	reNumOnly   = regexp.MustCompile(`^[0-9]{1,4}$`)                          // 1 / 12 / 007
	reCNNumOnly = regexp.MustCompile(`^[零〇一二三四五六七八九十百千两壹贰叁肆伍陆柒捌玖拾佰仟萬]{1,6}$`) // 一 / 十二 / 壹佰
	reNumDot    = regexp.MustCompile(`^[0-9]{1,3}\s*[、.．]\s*\S`)              // 1. 起点 / 12、风云
	reCNNumDot  = regexp.MustCompile(`^[零〇一二三四五六七八九十百千两壹贰叁肆伍陆柒捌玖拾佰仟萬]{1,3}\s*[、.．]\s*\S`)
	reParen     = regexp.MustCompile(`^[（(]\s*[0-9零〇一二三四五六七八九十百千两壹贰叁肆伍陆柒捌玖拾佰仟萬]{1,3}\s*[)）]$`) // （一） / (2)
	reNumSpace  = regexp.MustCompile(`^[0-9]{1,3}\s+\S`)                                       // 12 风波（另限行长≤20）
)

var tocRules = []tocRule{
	{"markdown", reMD, 0},
	{"english", reEN, 0},
	{"cn-volume", reVolume, 1},
	{"cn-chapter", reCNNum, 0},
	{"cn-keyword", reCNKeyword, 0},
	{"vol-updown", reVolUpDown, 1},
	{"num-only", reNumOnly, 0},
	{"cn-num-only", reCNNumOnly, 0},
	{"paren-num", reParen, 0},
	{"num-dot", reNumDot, 0},
	{"cn-num-dot", reCNNumDot, 0},
	{"num-space", reNumSpace, 0},
}

// tocTriggers 首字符分流表：行首 rune 不在表内则不可能是任何规则的命中，
// 直接短路（绝大多数正文行零正则成本）。维护新规则时同步补表。
var tocTriggers = func() map[rune]bool {
	m := make(map[rune]bool, 64)
	for _, s := range []string{
		"第0123456789#(（",
		"ccCssppvvbbCCSSPPVVBB", // chapter/section/part/vol./book 首字母
		"序前言引楔后附番外终尾跋上中下",
		"零〇一二三四五六七八九十百千两壹贰叁肆伍陆柒捌玖拾佰仟萬",
	} {
		for _, r := range s {
			m[r] = true
		}
	}
	return m
}()

// matchChapter 综合识别章节标题；返回命中的规则（含层级）。
// s 为 clean 后的行（TOC 清洗管线 Indent=false，行首 \u3000 是源文件真实缩进，
// 用于 D1 弱规则冻结；行尾空白不保证剥离——单行无 \n 时 reTrailingWS 不触发，
// 下面的 TrimSpace 兜底）。
func matchChapter(s string) (*tocRule, bool) {
	t := strings.TrimSpace(s)
	rs := []rune(t)
	if len(rs) == 0 || len(rs) > 40 { // 1~40 字窗口：短至 1 字认关键词/纯数字，40 上限防长正文
		return nil, false
	}
	if !tocTriggers[rs[0]] {
		return nil, false
	}
	if reExcludePeriod.MatchString(t) || reExcludeCount.MatchString(t) {
		return nil, false
	}
	// D1：源文件行首全角缩进 → 冻结弱规则（正文缩进数字行不是章节）
	indented := strings.HasPrefix(s, "\u3000")
	for i := range tocRules {
		r := &tocRules[i]
		if indented && isWeakRule(r.re) {
			continue
		}
		// num-space（12 风波）：正文数字后一般不跟空格，但仍限行长 ≤20 压误判
		if r.re == reNumSpace && len(rs) > 20 {
			continue
		}
		if r.re.MatchString(t) {
			return r, true
		}
	}
	return nil, false
}

// isWeakRule 弱规则集合（行首全角缩进时冻结）。弱 = 纯数字/括号/顿号类，
// 它们对『缩进的正文段』误判率最高；第X章/Chapter/MD 等强规则不冻结。
var weakRules = []*regexp.Regexp{reNumOnly, reCNNumOnly, reParen, reNumDot, reCNNumDot, reNumSpace}

func isWeakRule(re *regexp.Regexp) bool {
	for _, w := range weakRules {
		if w == re {
			return true
		}
	}
	return false
}

// isChapterTitle 综合识别章节标题（兼容入口）。
func isChapterTitle(s string) bool {
	_, ok := matchChapter(s)
	return ok
}

// BuildTOC 全扫全书识别章节标题，返回（标题, 页码）列表。
// 用独立文件句柄扫描，不触碰 Book 当前的分页游标/缓存（不影响阅读位置）。
// 页码按「行首原始 rune 偏移 / pageSize」计算，与 Goto 定位一致。
// EPUB：优先用解析好的自带 TOC；无 TOC 时对内存全文回落章节规则库（D1 拍板）。
func (b *Book) BuildTOC() ([]TocEntry, error) {
	if b.epub {
		if len(b.epubToc) > 0 {
			return b.epubToc, nil
		}
		return buildTOCFromText(b.text, b.pageSize, b.tocCleanOpts()), nil
	}
	return BuildTOCFor(b.path, b.enc, b.pageSize, b.tocCleanOpts())
}

// TOCFunc 返回异步建目录的执行函数（UI 线程取快照，后台执行）。
// EPUB 走内存（无需再碰文件）；txt 保持独立的文件句柄扫描。
// E3（2026-10-23 审查）：两个分支都必须在 UI 线程完成快照——后台闭包只读
// 局部变量，否则后台建目录与 UI 的 SetCleanOpts 在 b.cleanOpts 上竞争。
func (b *Book) TOCFunc() func() ([]TocEntry, error) {
	if b.epub {
		pre, hasTOC := b.epubToc, len(b.epubToc) > 0
		text, pageSize, co := b.text, b.pageSize, b.tocCleanOpts()
		return func() ([]TocEntry, error) {
			if hasTOC {
				return pre, nil
			}
			return buildTOCFromText(text, pageSize, co), nil // D1 规则库回落（快照口径）
		}
	}
	path, enc, pageSize, co := b.path, b.enc, b.pageSize, b.tocCleanOpts()
	return func() ([]TocEntry, error) { return BuildTOCFor(path, enc, pageSize, co) }
}

// tocCleanOpts 建目录专用的清洗口径：Indent 强制关（行首 \u3000 必须是源文件
// 真实缩进，否则 D1 弱规则冻结会误伤所有行）、BlankAll 无意义（逐行 clean）。
// AdFilter 跟随 Book：广告行整行剔除后，目录扫描自然不再见到它们。
func (b *Book) tocCleanOpts() CleanOptions {
	return CleanOptions{AdFilter: b.cleanOpts.AdFilter}
}

// BuildTOCFor 用给定路径/编码/页大小/清洗口径独立扫描建目录；自带文件句柄，
// 不触碰任何 Book 状态，可后台并发调用（页码口径与 Book.BuildTOC 一致）。
func BuildTOCFor(path string, enc encoding.Encoding, pageSize int, co CleanOptions) ([]TocEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := transform.NewReader(f, enc.NewDecoder())
	br := bufio.NewReader(r)

	var toc []TocEntry
	var line strings.Builder
	lineStart := 0 // 行首原始 rune 偏移
	raw := 0
	flush := func() {
		txt := cleanWith(line.String(), co)
		if rule, ok := matchChapter(txt); ok {
			title := strings.TrimSpace(txt)
			if reMD.MatchString(title) {
				title = strings.TrimSpace(reMDStrip.ReplaceAllString(title, "")) // TOC 显示去掉 # 前缀
			}
			toc = append(toc, TocEntry{Title: title, Page: lineStart / pageSize, Offset: lineStart, Level: rule.level})
		}
		line.Reset()
	}
	for {
		ru, _, err := br.ReadRune()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		raw++
		if ru == '\n' {
			flush()
			lineStart = raw // 下一行首原始 rune 偏移
			continue
		}
		if ru == '\r' {
			continue
		}
		line.WriteRune(ru)
	}
	flush()
	return toc, nil
}

// buildTOCFromText 对（已解码的）纯文本做章节规则扫描，口径与 BuildTOCFor
// 完全一致（行首原始 rune 偏移 / pageSize）。EPUB 无自带 TOC 时走这里。
func buildTOCFromText(text string, pageSize int, co CleanOptions) []TocEntry {
	var toc []TocEntry
	lineStart := 0 // 行首原始 rune 偏移
	raw := 0
	var line strings.Builder
	flush := func() {
		txt := cleanWith(line.String(), co)
		if rule, ok := matchChapter(txt); ok {
			title := strings.TrimSpace(txt)
			if reMD.MatchString(title) {
				title = strings.TrimSpace(reMDStrip.ReplaceAllString(title, "")) // TOC 显示去掉 # 前缀
			}
			toc = append(toc, TocEntry{Title: title, Page: lineStart / pageSize, Offset: lineStart, Level: rule.level})
		}
		line.Reset()
	}
	for _, ru := range text {
		raw++
		if ru == '\n' {
			flush()
			lineStart = raw
			continue
		}
		if ru == '\r' {
			continue
		}
		line.WriteRune(ru)
	}
	flush()
	return toc
}

// SeekTo 跳到全局 rune 偏移 offset 所在页，并在该页（clean 后）文本中定位 title，
// 返回页文本与 title 的页内 rune 偏移。用于章级精确定位（含同页多章节分别定位）。
//
// 边界情形：标题行可能横跨页边界（标题行本身有十几字，页边界落在行中间），
// 此时整串标题不在任何单页里，strings.Index 必然 -1。退化策略：
// 取标题的最长前缀在页尾附近找落点（被切断时前半段必在页尾），
// 至少保证"跳到标题开头"而不是退化为页首。
func (b *Book) SeekTo(offset int, title string) (string, int, error) {
	if offset < 0 {
		offset = 0
	}
	page := offset / b.pageSize
	if err := b.readUpTo(page); err != nil {
		return "", 0, err
	}
	// B2：readUpTo 会把越界 page 钳制到实际末页（b.cur），缓存键必须用钳制后
	// 的页号——用钳制前的 page 查 cache 必然 miss，返回整屏空白（书被外部
	// 编辑变小 / Offset 与当前 pageSize 不符时可达）。
	txt := b.cache[b.cur]
	bi := strings.Index(txt, title)
	if bi >= 0 {
		return txt, len([]rune(txt[:bi])), nil
	}
	if off, ok := findSplitTitle(txt, title); ok {
		return txt, off, nil
	}
	return txt, 0, nil // 标题未在页内找到（极端），退化为页首
}

// findSplitTitle 标题被页边界切断时的落点定位：在页尾 window 个 rune 内，
// 找标题最长前缀的最后一次出现，返回其在页内的 rune 偏移。
func findSplitTitle(pageText, title string) (int, bool) {
	const window = 400
	rs := []rune(pageText)
	ts := []rune(title)
	if len(ts) < 2 || len(rs) < 2 {
		return 0, false
	}
	start := len(rs) - window
	if start < 0 {
		start = 0
	}
	tail := string(rs[start:])
	for n := len(ts); n >= 2; n-- {
		i := strings.LastIndex(tail, string(ts[:n]))
		if i >= 0 {
			return start + len([]rune(tail[:i])), true
		}
	}
	return 0, false
}
