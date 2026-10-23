package main

import (
	"regexp"
	"strings"
)

// ---- Markdown 美化（md → 易读纯文本）----
// 阅读区是普通 EDIT（纯文本、单字体、无颜色/粗体混排），无法真渲染；
// 这里做"美化纯文本"：层级用缩进表达、不发明符号；表格保持 |/- 制表符风格、按列宽对齐。
// 硬约束：逐行 1:1 转换、总行数不变 —— 否则 TOC 的行级定位（SeekTo/scrollCaretToTop）会错位。

var (
	reMDHead = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	// 闭合式 ATX 尾缀：`## 标题 ##` 的尾部 ` ##`（CommonMark 要求 # 前有空白，
	// "C#" 这类正文不会被误剥）。
	reMDATXClose = regexp.MustCompile(`\s+#+\s*$`)
	reMDHr       = regexp.MustCompile(`^\s*(-{3,}|\*{3,}|_{3,})\s*$`)
	reMDBold     = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	reMDCode     = regexp.MustCompile("`([^`\n]+)`")
)

// mdFenceState 围栏代码块状态机的跨页状态。预留：MD 路径当前走全量重灌
// （appendPage 对 md 用 SetText），一次 mdBeautify 传零值状态即可；
// 若未来做 md 增量美化，用此结构在页间传递围栏状态。
type mdFenceState struct {
	in     bool
	marker rune
	length int
}

// mdBeautifyState 逐行转换 + 表格块对齐重排（行数不变），并返回行末围栏状态。
// 围栏代码块（``` / ~~~）内逐字保留：块里的 #、>、** 是代码不是排版，
// 误美化会把注释行缩进成假标题。围栏开/闭行本身也原样保留。
func mdBeautifyState(s string, st mdFenceState) (string, mdFenceState) {
	lines := strings.Split(s, "\n")
	fenced := make([]bool, len(lines)) // 该行是否处于围栏内（含围栏行本身）
	inFence := st.in
	fenceMarker := st.marker
	fenceLen := st.length
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if m, n, ok := mdFence(t); ok {
			if !inFence {
				inFence, fenceMarker, fenceLen = true, m, n
				fenced[i] = true
				continue
			}
			// 闭栏：同一字符、不短于开栏、行内只有该字符（闭栏不允许 info string）
			if m == fenceMarker && n >= fenceLen && strings.Trim(t, string(m)) == "" {
				inFence = false
				fenced[i] = true
				continue
			}
		}
		if inFence {
			fenced[i] = true
			continue
		}
		lines[i] = mdLine(ln)
	}
	alignTables(lines, fenced)
	return strings.Join(lines, "\n"), mdFenceState{in: inFence, marker: fenceMarker, length: fenceLen}
}

// mdBeautify 整段一次美化（兼容入口）：围栏状态从零起算。
func mdBeautify(s string) string {
	out, _ := mdBeautifyState(s, mdFenceState{})
	return out
}

// mdFence 识别围栏行：0~3 空格缩进 + 至少 3 个 ` 或 ~（可带 info string）。
func mdFence(t string) (marker rune, length int, ok bool) {
	t = strings.TrimLeft(t, " ")
	if len(t) < 3 {
		return 0, 0, false
	}
	c := t[0]
	if c != '`' && c != '~' {
		return 0, 0, false
	}
	n := 0
	for n < len(t) && t[n] == c {
		n++
	}
	if n < 3 {
		return 0, 0, false
	}
	rest := strings.TrimSpace(t[n:])
	if c == '`' && strings.ContainsRune(rest, '`') {
		return 0, 0, false // ` 的 info string 内不允许反引号
	}
	return rune(c), n, true
}

// mdLine 单行转换：层级用【缩进】表达，不发明符号（大叔审美：改格式，不替换字符）。
// 列表（-/*+）与分隔线（---）原样保留——它们本身就是通用排版符；表格行交给块对齐。
func mdLine(ln string) string {
	t := strings.TrimSpace(ln)
	if t == "" {
		return ln
	}
	// 表格行交给块对齐
	if strings.HasPrefix(t, "|") {
		return ln
	}
	// ATX 标题：去 # 前缀，按层级缩进（H1 顶格、H2 缩 2 格、H3 缩 4 格…）；
	// 标题内的粗体/行内代码标记同样剥离
	if m := reMDHead.FindStringSubmatch(t); m != nil {
		lvl := len(m[1])
		body := strings.TrimSpace(m[2])
		body = reMDATXClose.ReplaceAllString(body, "") // 闭合式 ATX 尾部 ## 剥离
		body = reMDBold.ReplaceAllString(body, "$1")
		body = reMDCode.ReplaceAllString(body, "$1")
		return strings.Repeat("  ", lvl-1) + body
	}
	// 分隔线 --- *** ___ 原样保留
	// 引用 >：去掉符号，按嵌套层数缩进（每层 4 格）。
	// 层数 = 前导「> 与空格」混合序列里 > 的个数，"> > x" 与 ">> x" 都算两层。
	if strings.HasPrefix(t, ">") {
		body := strings.TrimLeft(t, "> ")
		n := strings.Count(t[:len(t)-len(body)], ">")
		return strings.Repeat("    ", n) + strings.TrimSpace(body)
	}
	// 无序列表：原样保留（- / * / + 是通用列表符）
	// 行内：粗体/行内代码去标记（删字符而非换字符）
	out := reMDBold.ReplaceAllString(ln, "$1")
	out = reMDCode.ReplaceAllString(out, "$1")
	return out
}

// dispWidth 近似显示宽度：非 ASCII（中文/全角）记 2，ASCII 记 1。
func dispWidth(s string) int {
	w := 0
	for _, r := range s {
		if r > 0x7F {
			w += 2
		} else {
			w++
		}
	}
	return w
}

func padRight(s string, w int) string {
	if d := w - dispWidth(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func isTableLine(ln string) bool {
	t := strings.TrimSpace(ln)
	return strings.HasPrefix(t, "|") && strings.HasSuffix(t, "|")
}

func parseCells(ln string) []string {
	t := strings.TrimSpace(ln)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	parts := strings.Split(t, "|")
	cells := make([]string, len(parts))
	for i, c := range parts {
		c = strings.TrimSpace(c)
		c = reMDBold.ReplaceAllString(c, "$1") // 单元格内粗体/代码标记同样剥离
		c = reMDCode.ReplaceAllString(c, "$1")
		cells[i] = c
	}
	return cells
}

var reMDSepCell = regexp.MustCompile(`^:?-{3,}:?$`)

func isSepRow(cells []string) bool {
	for _, c := range cells {
		if !reMDSepCell.MatchString(c) {
			return false
		}
	}
	return len(cells) > 0
}

// alignTables 扫描连续表格块（| 开头 | 结尾的行），按列宽对齐重排（行数不变）。
// fenced 标记围栏内的行（含围栏行），不参与表格识别——代码块里的 |...| 不是表格。
func alignTables(lines []string, fenced []bool) {
	for i := 0; i < len(lines); {
		if fenced[i] || !isTableLine(lines[i]) {
			i++
			continue
		}
		j := i
		for j < len(lines) && !fenced[j] && isTableLine(lines[j]) {
			j++
		}
		rows := lines[i:j]
		// 至少 2 行且含分隔行才算表格
		hasSep := false
		var parsed [][]string
		for _, r := range rows {
			cells := parseCells(r)
			if isSepRow(cells) {
				hasSep = true
			}
			parsed = append(parsed, cells)
		}
		if hasSep && j-i >= 2 {
			realignTable(lines[i:j], parsed)
		}
		i = j
	}
}

// mdTableColCap 表格单列最大显示宽度：超长单元格截断加省略号，
// 防止个别长内容把整列（含分隔行）撑爆屏幕（表格是概览，完整内容在正文）。
const mdTableColCap = 28

// truncCell 超过显示宽 w 的单元格截断到 w-2 宽并加省略号。
func truncCell(s string, w int) string {
	if w < 4 || dispWidth(s) <= w {
		return s
	}
	limit := w - 2
	cur := 0
	for i, r := range s {
		rw := 1
		if r > 0x7F {
			rw = 2
		}
		if cur+rw > limit {
			return s[:i] + "…"
		}
		cur += rw
	}
	return s
}

func realignTable(dest []string, parsed [][]string) {
	// 列数取分隔行（权威）；无分隔行取最大列数
	cols := 0
	for _, cells := range parsed {
		if isSepRow(cells) {
			cols = len(cells)
			break
		}
	}
	if cols == 0 {
		for _, cells := range parsed {
			if len(cells) > cols {
				cols = len(cells)
			}
		}
	}
	if cols == 0 {
		return
	}
	widths := make([]int, cols)
	for _, cells := range parsed {
		if isSepRow(cells) {
			continue
		}
		for c := 0; c < cols && c < len(cells); c++ {
			w := dispWidth(cells[c])
			if w > mdTableColCap {
				w = mdTableColCap
			}
			if w > widths[c] {
				widths[c] = w
			}
		}
	}
	for i, cells := range parsed {
		if isSepRow(cells) {
			// 分隔行保持 md 制表符风格：| --- | --- |
			segs := make([]string, cols)
			for c := 0; c < cols; c++ {
				segs[c] = strings.Repeat("-", widths[c]+2)
			}
			dest[i] = "|" + strings.Join(segs, "|") + "|"
			continue
		}
		// 数据行：对齐填充，保持 | 分隔；超长单元格截断加省略号
		segs := make([]string, cols)
		for c := 0; c < cols; c++ {
			cell := ""
			if c < len(cells) {
				cell = truncCell(cells[c], widths[c])
			}
			segs[c] = " " + padRight(cell, widths[c]) + " "
		}
		dest[i] = "|" + strings.Join(segs, "|") + "|"
	}
}
