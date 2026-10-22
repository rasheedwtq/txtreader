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
type TocEntry struct {
	Title  string
	Page   int
	Offset int
}

// 综合章节识别（中 / 英 / Markdown），ADR 级规则集：
//   - 中文序号：第[零一二…X]章/回/节/卷/部/篇/集
//   - 中文关键词：序/序言/前言/引言/引子/楔子/后记/附录/番外/终章/尾声/跋
//   - 英文：chapter|ch.|section|part|vol.|book + 数字/罗马数字
//   - Markdown：#..###### 标题
var (
	reCNKeyword = regexp.MustCompile(`^(序|序言|前言|引言|引子|楔子|后记|附录|番外|终章|尾声|跋)`)
	reCNNum     = regexp.MustCompile(`^第\s*[0-9零一二三四五六七八九十百千两]+\s*[章回节卷部篇集]`)
	reEN        = regexp.MustCompile(`(?i)^(?:chapter|ch\.?|section|part|vol\.?|book)\s+[0-9ivxlc]+`)
	reMD        = regexp.MustCompile(`^#{1,6}\s+\S`)
	reMDStrip   = regexp.MustCompile(`^#{1,6}\s+`)

	// 数字/中文数字类章节分割（大叔经验：正文一般不会以数字开头，
	// 以数字开头的短行大概率是章节标记；少量误判可容忍——宁多勿漏）。
	reNumOnly   = regexp.MustCompile(`^[0-9]{1,4}$`)                              // 1 / 12 / 007
	reCNNumOnly = regexp.MustCompile(`^[零〇一二三四五六七八九十百千两]{1,6}$`)                  // 一 / 十二
	reNumDot    = regexp.MustCompile(`^[0-9]{1,3}\s*[、.．]\s*\S`)                  // 1. 起点 / 12、风云
	reCNNumDot  = regexp.MustCompile(`^[零〇一二三四五六七八九十百千两]{1,3}\s*[、.．]\s*\S`)      // 一、风波
	reParen     = regexp.MustCompile(`^[（(]\s*[0-9零〇一二三四五六七八九十百千两]{1,3}\s*[)）]$`) // （一） / (2)
	reNumSpace  = regexp.MustCompile(`^[0-9]{1,3}\s+\S`)                          // 12 风波（另限行长≤20）
)

// isChapterTitle 综合识别章节标题。
// 容忍度：标题需 2~40 个字符（避免误判长正文或单字噪声），并匹配任一模式。
func isChapterTitle(s string) bool {
	t := strings.TrimSpace(s)
	r := []rune(t)
	if len(r) == 0 {
		return false
	}
	// 短至 1 字也认：关键词类（序/后记/楔子…）+ 纯数字/纯中文数字/括号数字行。
	// 关键词是前缀匹配，必须限 40 字上限：否则正文段落"前言往事如烟…"会被灌进目录。
	if len(r) <= 40 && (reCNKeyword.MatchString(t) || reNumOnly.MatchString(t) || reCNNumOnly.MatchString(t) || reParen.MatchString(t)) {
		return true
	}
	if len(r) < 2 || len(r) > 40 {
		return false
	}
	if reCNNum.MatchString(t) || reEN.MatchString(t) || reMD.MatchString(t) ||
		reNumDot.MatchString(t) || reCNNumDot.MatchString(t) {
		return true
	}
	// "12 风波"（数字+空格+内容）：正文数字后一般不跟空格；再限行长 ≤20 压误判
	if reNumSpace.MatchString(t) && len(r) <= 20 {
		return true
	}
	return false
}

// BuildTOC 全扫全书识别章节标题，返回（标题, 页码）列表。
// 用独立文件句柄扫描，不触碰 Book 当前的分页游标/缓存（不影响阅读位置）。
// 页码按「行首原始 rune 偏移 / pageSize」计算，与 Goto 定位一致。
func (b *Book) BuildTOC() ([]TocEntry, error) {
	return BuildTOCFor(b.path, b.enc, b.pageSize)
}

// TOCParams 返回建目录所需的只读参数快照（path/enc/pageSize）。
// UI 线程先快照、再交给后台 goroutine 调 BuildTOCFor，
// 避免后台扫描与 ReopenWith（换编码）并发读写 Book 字段的数据竞争。
func (b *Book) TOCParams() (string, encoding.Encoding, int) {
	return b.path, b.enc, b.pageSize
}

// BuildTOCFor 用给定路径/编码/页大小独立扫描建目录；自带文件句柄，
// 不触碰任何 Book 状态，可后台并发调用（页码口径与 Book.BuildTOC 一致）。
func BuildTOCFor(path string, enc encoding.Encoding, pageSize int) ([]TocEntry, error) {
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
		txt := clean(line.String())
		if isChapterTitle(txt) {
			title := strings.TrimSpace(txt)
			if reMD.MatchString(title) {
				title = strings.TrimSpace(reMDStrip.ReplaceAllString(title, "")) // TOC 显示去掉 # 前缀
			}
			toc = append(toc, TocEntry{Title: title, Page: lineStart / pageSize, Offset: lineStart})
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
	txt := b.cache[page]
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
