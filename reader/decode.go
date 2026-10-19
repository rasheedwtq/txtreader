// Package reader 提供 txt 文件的自动编码识别、流式分页解码与文本清洗。
//
// 设计要点（对应需求 #3 / #5）：
//   - 自动识别 UTF-8 / UTF-16(LE|BE) / GBK / GB18030 / Big5 / 日文 / 韩文 / 西欧等编码，
//     基于 Mozilla 通用字符集探测器（saintfish/chardet）。
//   - 流式解码：不把整个大文件载入内存，按“页（rune 数）”滚动读取，首屏秒开第一页。
//   - 缓存当前页与前一页（至多 2 页），兼顾效率与内存。
//   - 清洗：去 BOM / 控制字符 / 零宽字符，合并连续空行，行尾去空白。
package reader

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode"

	"github.com/saintfish/chardet"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	xunicode "golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// defaultPageRunes 每页字符数（约几屏），可按文件大小调整。
const defaultPageRunes = 20000

var (
	reTrailingWS = regexp.MustCompile(`[ \t]+\n`)
	reBlankLines = regexp.MustCompile(`\n{3,}`) // 3+ 换行 -> 2 换行（最多保留一个空行）
)

// Book 表示一个按需分页、流式解码的文本。
type Book struct {
	path     string
	encName  string
	enc      encoding.Encoding
	f        *os.File
	pageSize int
	cache    map[int]string
	cur      int
	reader   io.Reader
	buf      *bufio.Reader
	readerUp int // 已消费的页数；下一次 readRunes 将得到第 readerUp 页
	tail     string
	eof      bool // 解码流已到文件末尾（cur 即末页）
}

// Open 打开一个 txt 文件，自动探测编码并准备分页（只读第一页到内存）。
func Open(path string) (*Book, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	sample, err := readSample(f, 2<<20) // 采样前 2MB 用于探测编码
	if err != nil {
		f.Close()
		return nil, err
	}
	enc, name := chooseEncoding(sample)
	b := &Book{
		path:     path,
		encName:  name,
		enc:      enc,
		f:        f,
		pageSize: defaultPageRunes,
		cache:    make(map[int]string),
	}
	if err := b.resetReader(); err != nil {
		f.Close()
		return nil, err
	}
	if err := b.readUpTo(0); err != nil {
		f.Close()
		return nil, err
	}
	return b, nil
}

// Path 返回文件路径。
func (b *Book) Path() string { return b.path }

// EncName 返回探测到的编码名称（用于状态显示）。
func (b *Book) EncName() string { return b.encName }

// ReopenWith 用指定编码重新解码当前文件（编码兜底：自动探测出错时由 UI 调用）。
// 复用已打开的文件句柄，不重新 Open；重解码后游标回到首页（cur=0）。
func (b *Book) ReopenWith(enc encoding.Encoding, name string) error {
	b.enc = enc
	b.encName = name // 调用方（UI 候选列表）必须提供可读名
	b.cache = make(map[int]string)
	b.tail = ""
	b.reader = nil
	b.buf = nil
	b.eof = false
	b.cur = 0
	if err := b.resetReader(); err != nil {
		return err
	}
	if err := b.readUpTo(0); err != nil {
		return err
	}
	return nil
}

// Close 关闭底层文件。
func (b *Book) Close() error {
	if b.f != nil {
		return b.f.Close()
	}
	return nil
}

func (b *Book) resetReader() error {
	if _, err := b.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	b.reader = transform.NewReader(b.f, b.enc.NewDecoder())
	b.buf = bufio.NewReader(b.reader)
	b.readerUp = -1
	b.eof = false
	return nil
}

// readRunes 从当前解码流读取 n 个 rune（已清洗前）；第二个返回值表示流是否已到 EOF。
func (b *Book) readRunes(n int) (string, bool, error) {
	var sb strings.Builder
	sb.Grow(n * 4)
	for i := 0; i < n; i++ {
		ru, _, err := b.buf.ReadRune()
		if err == io.EOF {
			return sb.String(), true, nil
		}
		if err != nil {
			return sb.String(), false, err
		}
		sb.WriteRune(ru)
	}
	return sb.String(), false, nil
}

// readUpTo 重置流并从头读取第 0..target 页（用于首次/回退场景）。
// 若文件不足 target+1 页，则落到实际末页。
func (b *Book) readUpTo(target int) error {
	if err := b.resetReader(); err != nil {
		return err
	}
	p := 0
	for ; p <= target; p++ {
		s, isEOF, err := b.readRunes(b.pageSize)
		if err != nil {
			return err
		}
		b.cache[p] = clean(s)
		b.readerUp = p + 1
		if isEOF {
			b.eof = true
			break
		}
	}
	if p < target {
		target = p // 文件提前结束，钳制到实际末页
	}
	b.cur = target
	b.prune()
	return nil
}

// Next 翻到下一页（正向续读，O(页大小)）。已在末页时原地返回当前页。
func (b *Book) Next() (string, error) {
	if b.eof { // 已在末页，不再前进（避免产生空白页）
		return b.cache[b.cur], nil
	}
	b.tail = ""
	if b.readerUp == b.cur+1 { // 正向续读下一页
		s, isEOF, err := b.readRunes(b.pageSize)
		if err != nil {
			return "", err
		}
		if isEOF && s == "" { // 恰好停在页边界：后面没有内容了
			b.eof = true
			return b.cache[b.cur], nil
		}
		b.eof = isEOF
		b.cache[b.cur+1] = clean(s)
		b.cur++
		b.readerUp = b.cur + 1
		b.prune()
		return b.cache[b.cur], nil
	}
	if err := b.readUpTo(b.cur + 1); err != nil { // 回退/错位后从头读
		return "", err
	}
	return b.cache[b.cur], nil
}

// Prev 翻到上一页（优先命中缓存）。已在首页时原地返回当前页。
func (b *Book) Prev() (string, error) {
	b.tail = ""
	if b.cur == 0 {
		return b.cache[0], nil
	}
	if s, ok := b.cache[b.cur-1]; ok {
		b.cur--
		return s, nil
	}
	if err := b.readUpTo(b.cur - 1); err != nil {
		return "", err
	}
	return b.cache[b.cur], nil
}

// First 回到第一页。
func (b *Book) First() (string, error) {
	b.tail = ""
	if _, ok := b.cache[0]; !ok {
		if err := b.readUpTo(0); err != nil {
			return "", err
		}
	}
	b.cur = 0
	return b.cache[0], nil
}

// Last 流式读到文件末尾，返回最后的（至多一页大小的）内容。O(文件大小)，仅用户显式触发。
// 游标定位到真实末页，保证之后 Prev/Next/First 状态一致。
func (b *Book) Last() (string, error) {
	if err := b.resetReader(); err != nil {
		return "", err
	}
	b.tail = ""
	total := 0
	ring := make([]rune, 0, b.pageSize+1)
	for {
		ru, _, err := b.buf.ReadRune()
		if err != nil {
			break
		}
		total++
		ring = append(ring, ru)
		if len(ring) > b.pageSize {
			ring = ring[1:]
		}
	}
	s := clean(string(ring))
	b.cache = make(map[int]string)
	b.readerUp = -1
	b.reader = nil
	b.buf = nil
	b.eof = true
	if total == 0 {
		b.cur = 0
		b.cache[0] = ""
		return "", nil
	}
	b.cur = (total - 1) / b.pageSize
	b.cache[b.cur] = s
	b.tail = s
	return s, nil
}

// Current 返回当前页文本。
func (b *Book) Current() string {
	if b.tail != "" {
		return b.tail
	}
	return b.cache[b.cur]
}

// Page 返回当前页索引（0 起），供阅读进度持久化读取。
func (b *Book) Page() int { return b.cur }

// LoadNextPage 顺序加载下一页（累积式阅读用）；没有更多页时返回空串和 false。
func (b *Book) LoadNextPage() (string, bool, error) {
	if b.eof {
		return "", false, nil
	}
	before := b.Page()
	s, err := b.Next()
	if err != nil {
		return "", false, err
	}
	if b.Page() == before { // 原地未前进 = 已到末页
		return "", false, nil
	}
	return s, true, nil
}

// Goto 跳转到指定页（0 起）；越界自动钳制到实际首页/末页。供恢复进度使用。
func (b *Book) Goto(page int) error {
	if page < 0 {
		page = 0
	}
	return b.readUpTo(page)
}

// prune 仅保留当前页与前一页（至多 2 页）。
func (b *Book) prune() {
	for k := range b.cache {
		if k < b.cur-1 || k > b.cur {
			delete(b.cache, k)
		}
	}
}

// ---- 编码识别 ----

func readSample(f *os.File, max int) ([]byte, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	buf := make([]byte, 0, max)
	tmp := make([]byte, 32<<10)
	for len(buf) < max {
		n, err := f.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			if len(buf) > 0 {
				break
			}
			return nil, err
		}
	}
	return buf, nil
}

// chooseEncoding 返回最可能为 sample 的编码，以及探测到的名称。
// 采用“多候选 + 替换字符最少”策略，提升 GBK/UTF-8 互判的鲁棒性。
func chooseEncoding(sample []byte) (encoding.Encoding, string) {
	detected := ""
	if len(sample) > 0 {
		if res, err := chardet.NewTextDetector().DetectBest(sample); err == nil && res != nil {
			detected = res.Charset
		}
	}
	mapped := charsetToEncoding(detected)
	var cands []encoding.Encoding
	add := func(e encoding.Encoding) {
		for _, c := range cands {
			if c == e {
				return
			}
		}
		cands = append(cands, e)
	}
	if mapped != nil {
		add(mapped)
	}
	add(simplifiedchinese.GB18030)
	add(xunicode.UTF8)

	best := cands[0]
	bestBad := countBad(decodeSample(best, sample))
	for _, e := range cands[1:] {
		if bad := countBad(decodeSample(e, sample)); bad < bestBad {
			best = e
			bestBad = bad
		}
	}
	name := detected
	if mapped == nil {
		if name == "" {
			name = "未识别"
		}
		name += "（按 GBK/UTF-8 容错）"
	}
	return best, name
}

func decodeSample(e encoding.Encoding, sample []byte) string {
	r := transform.NewReader(bytes.NewReader(sample), e.NewDecoder())
	out, _ := io.ReadAll(r)
	return string(out)
}

func countBad(s string) int {
	n := 0
	for _, r := range s {
		if r == unicode.ReplacementChar {
			n++
		}
	}
	return n
}

func charsetToEncoding(name string) encoding.Encoding {
	switch strings.ToUpper(name) {
	case "UTF-8", "ASCII":
		return xunicode.UTF8
	case "UTF-16LE":
		return xunicode.UTF16(xunicode.LittleEndian, xunicode.UseBOM)
	case "UTF-16BE":
		return xunicode.UTF16(xunicode.BigEndian, xunicode.UseBOM)
	case "GB-18030", "GB18030":
		return simplifiedchinese.GB18030
	case "GBK":
		return simplifiedchinese.GBK
	case "BIG5":
		return traditionalchinese.Big5
	case "EUC-JP":
		return japanese.EUCJP
	case "SHIFT_JIS", "SHIFT-JIS":
		return japanese.ShiftJIS
	case "EUC-KR":
		return korean.EUCKR
	case "ISO-8859-1", "LATIN1":
		return charmap.ISO8859_1
	case "ISO-8859-2":
		return charmap.ISO8859_2
	case "ISO-8859-5":
		return charmap.ISO8859_5
	case "ISO-8859-15":
		return charmap.ISO8859_15
	case "WINDOWS-1250":
		return charmap.Windows1250
	case "WINDOWS-1251":
		return charmap.Windows1251
	case "WINDOWS-1252":
		return charmap.Windows1252
	case "KOI8-R":
		return charmap.KOI8R
	}
	return nil
}

// ---- 文本清洗 ----

// clean 去除乱码/怪符并规整空行，返回更易读的正文。
func clean(s string) string {
	s = strings.ReplaceAll(s, "\uFEFF", "") // BOM
	s = strings.ReplaceAll(s, "\uFFFE", "")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteRune(r)
		case r < 0x20:
			// 其它 C0 控制字符丢弃
		case r == 0x7F:
			// DEL 丢弃
		case r >= 0x80 && r <= 0x9F:
			// C1 控制字符丢弃
		case r == 0x200B || r == 0x200C || r == 0x200D || r == 0x2060:
			// 零宽字符丢弃
		case r == 0x00A0:
			b.WriteRune(' ') // NBSP -> 空格
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	out = strings.ReplaceAll(out, "\r\n", "\n")
	out = strings.ReplaceAll(out, "\r", "\n")
	out = reTrailingWS.ReplaceAllString(out, "\n")  // 行尾去空白
	out = reBlankLines.ReplaceAllString(out, "\n\n") // 合并连续空行
	out = strings.TrimSpace(out)                     // 去除首尾空行
	return out
}
