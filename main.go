package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unsafe"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"github.com/lxn/win"
	pinyin "github.com/mozillazg/go-pinyin"
	"txtreader/reader"

	"golang.org/x/sys/windows"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	xunicode "golang.org/x/text/encoding/unicode"
)

type entry struct {
	name string
	path string
}

var (
	mw         *walk.MainWindow
	lb         *walk.ListBox
	te         *walk.TextEdit
	folderLbl  *walk.Label
	curBook    *reader.Book
	curEntries []entry
	curDir     string
	curFamily  = "微软雅黑"
	curSize    = 14
	curSpacing = 1 // 行间距等级：0 紧凑 / 1 标准 / 2 宽松 / 3 超宽
	readerFont *walk.Font
	curIsMD    bool // 当前打开的是 .md 文件（显示层走 md 美化）

	// curClean 全局清洗偏好（A1/B1/E1 拍板默认全开；.md 由 effectiveClean 强制豁免
	// Indent/BlankAll——md 美化的空行与行首缩进是块语义，见 md.go 硬约束）。
	curClean = reader.CleanOptions{Indent: true, AdFilter: true, BlankAll: true}

	// lastState 启动时的状态快照（单本进度记的就是这一份）：openAt 打开同一本
	// 书时据此回跳页码。现场 bug（2026-10-22）：CLI 双击打开不走会话恢复，
	// openAt 从第 0 页起 + showCurrent 立即 persist，把上次进度覆盖成 page=0。
	lastState appState

	tocLB      *walk.ListBox // 右侧章节目录栏（有目录时显示，无则隐藏）
	curTOC     []reader.TocEntry
	tocLoading bool // SetModel 期间屏蔽 OnCurrentIndexChanged 误跳
	hs         *walk.Splitter
)

// pyArgs 拼音转换参数（默认风格，多音字取第一个读音）。
// Fallback 必须自定义：go-pinyin 默认忽略无拼音字符（实测 "hello.txt" 键为空、
// "A文件"/"Z文件" 同键，排序退化），这里让非汉字原样保留，英文/数字走字母序。
var pyArgs = func() pinyin.Args {
	a := pinyin.NewArgs()
	a.Fallback = func(r rune, _ pinyin.Args) []string { return []string{string(r)} }
	return a
}()

// pinyinKey 返回拼音排序键：汉字转拼音字母（多音字取首读音），非汉字原样保留，
// 整体转小写。使中文按拼音、英文按字母统一排序（符合中文用户习惯）。
func pinyinKey(s string) string {
	parts := pinyin.Pinyin(s, pyArgs)
	var sb strings.Builder
	for _, p := range parts {
		if len(p) > 0 {
			sb.WriteString(p[0])
		}
	}
	return strings.ToLower(sb.String())
}

// pinyinSortKey 排序总键：先剥离前导标点再转拼音，末尾 \x00+原名 兜底。
// 前导标点（书名号《、引号「“、括号【等）不参与排序——否则《开头的键首字节
// 0xE3 大于所有字母/数字，书名号文件会整体沉底（探针实证：《三国演义》排在
// zoo.txt 之后）。剥离后《三国演义》按"三国演义"归位，与同拼音普通书名相邻。
func pinyinSortKey(s string) string {
	body := strings.TrimLeftFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if body == "" {
		body = s // 纯标点文件名：退回原名，避免空键
	}
	return pinyinKey(body) + "\x00" + s
}

// collectTxt 读取目录【当前层】的所有 .txt/.md（不递归子目录），按拼音排序。
// 只读当前层：书库按层级组织（中国/小说/作者），一次只列当前层，子目录用"打开文件夹"进；
// 既符合按层级逐层看的习惯，也避免机械盘递归大分区时冷读 IO 卡数秒。
// 目录读失败（权限/盘符未挂载）必须与"目录里真没书"区分开——原实现两者都返回空列表，
// 用户会以为书没了。
func collectTxt(dir string) ([]entry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var ents []entry
	for _, de := range des {
		if de.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(de.Name()))
		if ext != ".txt" && ext != ".md" && ext != ".epub" {
			continue // 跳过子目录与非文本书
		}
		ents = append(ents, entry{name: de.Name(), path: filepath.Join(dir, de.Name())})
	}

	// 拼音排序：中文转拼音字母（组织部长 zuzhi 排龙城 longcheng 之后，生僻字也准），
	// 英文/数字走字母序，书名号等前导标点不参与（见 pinyinSortKey）。
	// 取代旧的 Unicode 码点序（会把"龙"U+9F99 误排到最后）。
	type keyed struct {
		e entry
		k string
	}
	ks := make([]keyed, len(ents))
	for i, e := range ents {
		ks[i] = keyed{e: e, k: pinyinSortKey(e.name)}
	}
	sort.Slice(ks, func(i, j int) bool {
		return ks[i].k < ks[j].k
	})
	for i := range ks {
		ents[i] = ks[i].e
	}
	return ents, nil
}

func names() []string {
	ns := make([]string, len(curEntries))
	for i, e := range curEntries {
		ns[i] = e.name
	}
	return ns
}

// disp Win32 编辑控件只认 \r\n 才换行，显示前把清洗后的 \n 转回 \r\n。
func disp(s string) string {
	return strings.ReplaceAll(s, "\n", "\r\n")
}

// applySpacing 按行间距等级调整“段间空行”与“段内行间”的间距。
// TextEdit 是普通 EDIT 窗口类（非 RichEdit），不支持真行距，用空白行近似。
// 约定（clean 之后）：段落之间为单空行 \n\n，段落内的换行是孤立单 \n。
//   - 紧凑(0)：消除段间空行（\n\n→\n），段内不变
//   - 标准(1)：完全保持（与旧行为一致，中文观感不变）
//   - 宽松(2)：段间 2 空行、段内行后 1 空行
//   - 超宽(3)：段间 3 空行、段内行后 2 空行
//
// 关键：英文硬换行文本段落之间往往没有 \n\n（只有孤立单 \n），旧实现只认 \n\n，
// 导致英文行距无效；本实现同时作用于孤立单 \n，英文段内行间也能拉开。
// 用占位符 paraSep 先把段间 \n\n 隔离，再分别处理，避免两类换行互相干扰。
func applySpacing(s string) string {
	const paraSep = "\x01"
	switch curSpacing {
	case 0:
		return strings.ReplaceAll(s, "\n\n", "\n")
	case 1:
		return s
	}
	// 先把段间 \n\n 隔离成占位符，剩下的是段内孤立 \n
	tmp := strings.ReplaceAll(s, "\n\n", paraSep)
	var lineGap, paraGap string
	if curSpacing == 2 {
		lineGap = "\n\n"   // 段内行后 +1 空行
		paraGap = "\n\n\n" // 段间 2 空行
	} else {
		lineGap = "\n\n\n"   // 段内行后 +2 空行
		paraGap = "\n\n\n\n" // 段间 3 空行
	}
	tmp = strings.ReplaceAll(tmp, "\n", lineGap)    // 段内孤立 \n 加行距
	tmp = strings.ReplaceAll(tmp, paraSep, paraGap) // 段间 \n\n 加行距
	return tmp
}

// effectiveClean 按文件类型落地清洗口径：.md 豁免 Indent/BlankAll
// （行首缩进与空行是 md 块语义，动它会毁代码块/列表/围栏），AdFilter 跟随全局。
func effectiveClean(path string) reader.CleanOptions {
	if strings.EqualFold(filepath.Ext(path), ".md") {
		return reader.CleanOptions{AdFilter: curClean.AdFilter}
	}
	return curClean
}

// pageJoin 页间分隔符：空行全收模式下用单换行，否则单空行。
// B5：必须按当前书的实际口径取值——.md 被 effectiveClean 强制 BlankAll=false
// （空行是 md 块语义），但全局开关可能仍是开的；md 页界要维持 "\n\n" 中性边界。
// appendPage 增量追加与全量重灌两条路径必须同用本函数（不变式：增量 ≡ 全量）。
func pageJoin() string {
	if !curIsMD && curClean.BlankAll {
		return "\n"
	}
	return "\n\n"
}

// displayText 显示层总出口：md 美化 → 行距 → 换行符。
func displayText(s string) string {
	if curIsMD {
		s = mdBeautify(s)
	}
	return disp(applySpacing(s))
}

// displayRuneOffset 在显示文本里定位 title 的 rune 偏移。
// md 美化会改变每行字符数，clean 原文偏移会错行，显示层重搜最稳。
func displayRuneOffset(pageText, title string) int {
	d := displayText(pageText)
	if title != "" {
		if bi := strings.Index(d, title); bi >= 0 {
			return len([]rune(d[:bi]))
		}
	}
	return 0
}

// render 套用格式后在阅读区显示文本。所有显示出口统一走这里。
func render(s string) {
	if te == nil {
		return
	}
	te.SetText(displayText(s))
	if readerFont != nil {
		te.SetFont(readerFont)
	}
}

func openAt(idx int) {
	if idx < 0 || idx >= len(curEntries) {
		return
	}
	// 同一路径且已打开：跳过重开，避免刷新/重复点击时重置阅读进度
	if curBook != nil && curBook.Path() == curEntries[idx].path {
		return
	}
	if curBook != nil {
		curBook.Close()
		curBook = nil
	}
	var b *reader.Book
	var err error
	if strings.EqualFold(filepath.Ext(curEntries[idx].path), ".epub") {
		b, err = reader.OpenEpub(curEntries[idx].path) // EPUB：解包→内存流
	} else {
		b, err = reader.Open(curEntries[idx].path)
	}
	if err != nil {
		te.SetText(disp(fmt.Sprintf("无法打开文件：\n%s\n\n%v", curEntries[idx].path, err)))
		return
	}
	curBook = b
	curIsMD = strings.EqualFold(filepath.Ext(curEntries[idx].path), ".md")
	b.SetCleanOpts(effectiveClean(curEntries[idx].path)) // 全局清洗偏好落位（Open 默认 LegacyClean）
	// 单本进度恢复（CLI 双击/会话两条路统一走这里）：先回页，再把行级书签
	// （视口锚点）滚回第一可见行——短篇只有 1 页，页码恢复不了位置，全靠锚点。
	// 锚点搜不到（正文/字体/清洗设置变了）自然退化为页首，即旧行为。
	// 时序陷阱（现场 bug 2026-10-22）：showCurrent 内部会 persist 并刷新
	// lastState（此刻视口在文档顶部），锚点必须在 showCurrent **之前**快照，
	// 否则搜的是被覆盖后的"第一行"，永远滚回文档开头。
	remembered := lastState.CurrentPath == curEntries[idx].path
	var restorePage int
	var restoreAnchor string
	if remembered {
		restorePage = lastState.ViewPage
		restoreAnchor = lastState.ViewAnchor
		// E4（2026-10-23 审查）：旧口径 state.json 只有 page 没有 view_page，
		// 缺迁移会把老进度整本作废（条件命中但 Goto(0)）。
		if restoreAnchor == "" && lastState.ViewPage == 0 && lastState.Page > 0 {
			restorePage = lastState.Page
		}
		if lastState.Page > 0 || restoreAnchor != "" {
			_ = b.Goto(restorePage) // ViewPage==0（短篇单页）即第 0 页，合法
		}
	}
	showCurrent()
	if remembered && restoreAnchor != "" && te != nil {
		d := te.Text() // disp 形式（\r\n），与保存锚点时同源
		if bi := strings.Index(d, restoreAnchor); bi >= 0 {
			scrollCaretToTop(len([]rune(d[:bi])))
		} else {
		}
	}
	showTOC()
}

func showCurrent() {
	if curBook == nil {
		return
	}
	accumBase = curBook.Page()
	accumBuf = []string{curBook.Current()}
	dispStale = true
	render(curBook.Current())
	if folderLbl != nil {
		base := filepath.Dir(curBook.Path())
		folderLbl.SetText(fmt.Sprintf("文件夹：%s（%d 篇）   当前编码：%s", base, len(curEntries), curBook.EncName()))
	}
	persist() // 打开书即持久化（目录+书+页码0）
}

// captureView 捕捉当前视口：第一可见行的行首文字作行级书签 + 其所在页码。
// EM_GETFIRSTVISIBLELINE 拿行号 → EM_LINEINDEX 拿行首 UTF-16 偏移 → 从
// te.Text() 切出该行（trim 后取前 24 rune）；锚点在 accumBuf 里从后往前
// 定位所在页（accumBase+j）。已知口径瑕疵：EM_LINEINDEX 是 UTF-16 单位，
// 首屏前含 emoji 时 rune 切位会错（P3' 家族），最坏结果是锚点取偏 →
// 恢复时搜不到 → 退化为页首，可接受。须在 UI 线程调用。
func captureView() (int, string) {
	if te == nil || curBook == nil {
		return 0, ""
	}
	first := int(te.SendMessage(win.EM_GETFIRSTVISIBLELINE, 0, 0))
	if first < 0 {
		first = 0
	}
	charIdx := int(te.SendMessage(win.EM_LINEINDEX, uintptr(first), 0))
	txt := te.Text()
	rs := []rune(txt)
	if charIdx < 0 || charIdx >= len(rs) {
		charIdx = 0
	}
	line := rs[charIdx:]
	for i, r := range line {
		if r == '\n' {
			line = line[:i]
			break
		}
	}
	anchor := strings.TrimSpace(string(line))
	if anchor == "" {
		// 首可见行是空行（用户停在空行处关窗，或拉到段间空白）：向下找最近的
		// 非空行作锚点——没有"标签"的纯文本/空行分隔的文档也要能记位置。
		// 恢复时会落在该行（误差 ≤ 一个空行段），好过整个锚点丢失退回页首。
		rest := rs[charIdx:]
		start := 0
		for start < len(rest) {
			end := start
			for end < len(rest) && rest[end] != '\n' {
				end++
			}
			cand := strings.TrimSpace(string(rest[start:end]))
			if cand != "" {
				anchor = cand
				break
			}
			start = end + 1
		}
	}
	if ar := []rune(anchor); len(ar) > 24 {
		anchor = string(ar[:24])
	}
	if anchor == "" {
		return curBook.Page(), ""
	}
	// 锚点所在页：按【显示偏移】映射，不用 Contains(原始页文本, 锚点)——
	// md 美化会改写行内容（表格对齐加空格、剥 **/#），美化后的锚点不是原始
	// 页文本的子串，包含匹配会误丢锚点（2026-10-22 现场：落在 SECI 表格行，
	// 锚点记成空 → 进度丢失）。te.Text() ≡ displayText(join(accumBuf, sep))
	// 是既有不变式（增量追加 ≡ 全量重灌），按每页显示长度切分即可定位。
	off := charIdx
	if off >= len(rs) {
		off = len(rs) - 1
	}
	return accumBase + dispPageOf(off), anchor // 映射失败兜底：锚点仍保留（页号可能偏，恢复时搜不到会退化页首）
}

// ---- 视口页映射前缀缓存（P1，2026-10-23 审查）----
// dispPrefix[i] = 第 i 页显示文本在 te.Text() 里的起始偏移。persist 有 8 个
// 调用点，captureView 原实现每次都对 accumBuf 全量 displayText（含 mdBeautify
// + []rune 转换），累积几十页后每次落盘都是百万字符级扫描。缓存后单次落盘
// 降为 O(log n) 二分；重建条件不满足即整体重算一次（= 旧实现单次成本）。
var (
	dispPrefix  []int
	dispStale   = true
	dispSpacing = -1                // 行距档变化 → displayText 输出变，须重建
	dispClean   reader.CleanOptions // 清洗档变化 → 同上（按值比较）
)

func dispPageOf(off int) int {
	if dispStale || dispSpacing != curSpacing || dispClean != curClean || len(dispPrefix) != len(accumBuf) {
		sepD := len([]rune(displayText(pageJoin())))
		dispPrefix = dispPrefix[:0]
		pos := 0
		for j := range accumBuf {
			dispPrefix = append(dispPrefix, pos)
			pos += len([]rune(displayText(accumBuf[j]))) + sepD
		}
		dispSpacing = curSpacing
		dispClean = curClean
		dispStale = false
	}
	// 落在最后一页之外的偏移（越界/尾页后空白）取最后一页
	j := sort.Search(len(dispPrefix), func(i int) bool { return dispPrefix[i] > off }) - 1
	if j < 0 {
		j = 0
	}
	return j
}

// persist 把当前会话状态写到 exe 同目录的 txtreader.state.json。
func persist() {
	st := appState{
		LastDir:       curDir,
		FontFamily:    curFamily,
		FontSize:      curSize,
		LineSpacing:   curSpacing,
		CleanIndent:   boolPtr(curClean.Indent),
		CleanAd:       boolPtr(curClean.AdFilter),
		CleanBlankAll: boolPtr(curClean.BlankAll),
	}
	if curBook != nil {
		st.CurrentPath = curBook.Path()
		st.Page = curBook.Page()
		st.ViewPage, st.ViewAnchor = captureView() // 行级书签：第一可见行（UI 线程）
	}
	if mw != nil { // 测试环境可能无主窗（全局 mw 被 makeTestWindow 遮蔽）
		saveWindowRect(&st)
	}
	saveState(st)
	lastState = st // 快照同步：下次 openAt 打开同一本书时能恢复到最新页码
}

func boolPtr(b bool) *bool { return &b }

// ---- 窗口状态记忆（方案《阅读进度与窗口状态记忆方案》§2）----

// saveWindowRect 记录窗口几何。用 WINDOWPLACEMENT 的 RcNormalPosition：
// 最大化时存的也是"还原尺寸"，避免下次先弹一个全屏尺寸的还原态。
func saveWindowRect(st *appState) {
	var wp win.WINDOWPLACEMENT
	wp.Length = uint32(unsafe.Sizeof(wp))
	if !win.GetWindowPlacement(mw.Handle(), &wp) {
		return
	}
	r := wp.RcNormalPosition
	st.WinX = int(r.Left)
	st.WinY = int(r.Top)
	st.WinW = int(r.Right - r.Left)
	st.WinH = int(r.Bottom - r.Top)
	if st.WinW <= 0 || st.WinH <= 0 {
		st.WinX, st.WinY, st.WinW, st.WinH = 0, 0, 0, 0 // 脏值不落
		return
	}
	st.WinMax = win.IsZoomed(mw.Handle())
}

// winRectVisible 边界检查：窗口矩形与虚拟桌面（所有显示器的外接矩形）
// 交集 ≥ 20% 面积才算可见可恢复——防拔副屏/改分辨率后窗口消失在屏幕外。
func winRectVisible(x, y, w, h int) bool {
	vx := int(win.GetSystemMetrics(win.SM_XVIRTUALSCREEN))
	vy := int(win.GetSystemMetrics(win.SM_YVIRTUALSCREEN))
	vw := int(win.GetSystemMetrics(win.SM_CXVIRTUALSCREEN))
	vh := int(win.GetSystemMetrics(win.SM_CYVIRTUALSCREEN))
	ix := min(x+w, vx+vw) - max(x, vx)
	iy := min(y+h, vy+vh) - max(y, vy)
	if ix <= 0 || iy <= 0 {
		return false
	}
	return uint64(ix)*uint64(iy)*5 >= uint64(w)*uint64(h)
}

// restoreWindowRect 恢复窗口几何：先还原尺寸，再按需最大化。
func restoreWindowRect(st appState) {
	if st.WinW <= 0 || st.WinH <= 0 || mw == nil {
		return
	}
	if !winRectVisible(st.WinX, st.WinY, st.WinW, st.WinH) {
		return // 坐标落在现有显示器之外，放弃恢复用默认值
	}
	_ = mw.SetX(st.WinX)
	_ = mw.SetY(st.WinY)
	_ = mw.SetWidth(st.WinW)
	_ = mw.SetHeight(st.WinH)
	if st.WinMax {
		win.ShowWindow(mw.Handle(), win.SW_MAXIMIZE)
	}
}

// restoreSession 恢复上次会话（目录+书）；页码恢复统一在 openAt 里按
// lastState 做（CLI 双击路径同样生效），此处不再单独 Goto。
func restoreSession(st appState) {
	if st.LastDir == "" {
		return
	}
	// 直接打开上次的书（loadFolderOpen 按路径定位），不再"先开第一本再切换"；
	// 书已不在时 indexOfPath 落回第一本，保持优雅降级（但不把旧页码套到新书上）。
	loadFolderOpen(st.LastDir, st.CurrentPath) // 内部已持久化
}

// restorePrefs 恢复外观与清洗偏好（字体/字号/行距/清洗三开关 + 阅读字体对象）。
// 独立成函数：会话恢复与 CLI 启动两条路都必须调用——CLI 路径原来整个跳过
// restoreState，导致双击打开文件时"宋体 18 号超宽行距关广告"全部回默认（B1）。
func restorePrefs(st appState) {
	if st.FontFamily != "" {
		curFamily = st.FontFamily
	}
	if st.FontSize > 0 {
		curSize = st.FontSize
	}
	if st.LineSpacing >= 0 && st.LineSpacing <= 3 {
		curSpacing = st.LineSpacing // 越界脏值不采用（缺字段时为 0=紧凑，与旧版行为一致）
	}
	// 清洗偏好：nil（旧配置文件）保持默认 A1/B1/E1 全开；非 nil 采用用户上次的选择
	if st.CleanIndent != nil {
		curClean.Indent = *st.CleanIndent
	}
	if st.CleanAd != nil {
		curClean.AdFilter = *st.CleanAd
	}
	if st.CleanBlankAll != nil {
		curClean.BlankAll = *st.CleanBlankAll
	}
	if f, e := walk.NewFont(curFamily, curSize, 0); e == nil {
		readerFont = f
	}
}

func pageNext() {
	if te == nil {
		return
	}
	te.SendMessage(win.WM_VSCROLL, uintptr(win.SB_PAGEDOWN), 0) // 下滚一屏，到底由加载泵追加
}

func pagePrev() {
	if te == nil {
		return
	}
	te.SendMessage(win.WM_VSCROLL, uintptr(win.SB_PAGEUP), 0) // 上滚一屏（前文都在缓冲里）
}

func pageFirst() {
	if curBook == nil {
		return
	}
	if _, err := curBook.First(); err == nil {
		startAccumFrom(0)
		persist() // 跳转即落盘，异常退出不丢进度
	}
}

func pageLast() {
	if curBook == nil {
		return
	}
	if _, err := curBook.Last(); err == nil {
		startAccumFrom(curBook.Page())
		persist()
	}
}

func deleteCurrent() {
	idx := lb.CurrentIndex()
	if idx < 0 || idx >= len(curEntries) {
		return
	}
	path := curEntries[idx].path
	mb := walk.MsgBox(mw, "确认删除",
		fmt.Sprintf("将把以下文件移入回收站（可在回收站恢复）：\n%s", curEntries[idx].name),
		walk.MsgBoxOKCancel|walk.MsgBoxIconQuestion)
	if mb != walk.DlgCmdOK {
		return
	}
	// 关键：先释放自己持有的句柄（流式阅读开着文件，Windows 不允许删除被占用文件）
	wasCurrent := curBook != nil && curBook.Path() == path
	if wasCurrent {
		curBook.Close()
		curBook = nil
		te.SetText("")
	}
	if err := recycleBin(path); err != nil {
		walk.MsgBox(mw, "删除失败", err.Error(), walk.MsgBoxIconError)
		if wasCurrent {
			openAt(idx) // 失败则恢复阅读现场
		}
		return
	}
	// 删除成功：删的是当前书 → 落位到被删位置（列表已收缩，原索引即"下一个文件"）；
	// 删的不是当前书 → 保持当前书选中不跳。
	if wasCurrent {
		refreshCurrent(idx)
	} else {
		refreshCurrent(-1)
	}
}

// refreshCurrent 重扫 curDir 刷新左侧列表。
// selectIdx >= 0：选中该索引（删除后传被删索引，正好落在下一个文件上）；
// selectIdx < 0：尽量保持当前打开的书（F5/刷新场景不丢进度）。
func refreshCurrent(selectIdx int) {
	if curDir == "" {
		return
	}
	ents, err := collectTxt(curDir)
	if err != nil {
		// 目录读失败：与"目录里没书"区分，别让用户以为书没了
		curEntries = nil
		_ = lb.SetModel([]string{})
		if curBook != nil {
			curBook.Close()
			curBook = nil
		}
		te.SetText("")
		folderLbl.SetText(fmt.Sprintf("文件夹：%s（读取失败：%v）", curDir, err))
		return
	}
	curEntries = ents
	_ = lb.SetModel(names())
	if len(curEntries) == 0 {
		if curBook != nil {
			curBook.Close()
			curBook = nil
		}
		te.SetText("")
		folderLbl.SetText(fmt.Sprintf("文件夹：%s（无文档）", curDir))
		return
	}
	if selectIdx >= 0 && selectIdx >= len(curEntries) {
		selectIdx = len(curEntries) - 1 // 删的是最后一个，落到新的末尾
	}
	if selectIdx >= 0 {
		lb.SetCurrentIndex(selectIdx) // 触发 openAt 自动打开
		return
	}
	if curBook != nil {
		for i, e := range curEntries {
			if e.path == curBook.Path() {
				lb.SetCurrentIndex(i) // 相同值不触发事件，阅读进度保持
				return
			}
		}
		// 当前书已被外部删掉
		curBook.Close()
		curBook = nil
		te.SetText("")
	}
	lb.SetCurrentIndex(0)
	openAt(0) // 兜底：索引未变时事件不触发（如打开失败后 F5 重试），同路径判断防重复打开
}

func loadFolder(dir string) {
	loadFolderOpen(dir, "")
}

// loadFolderOpen 加载目录并打开指定路径的书；openPath 为空则打开第一本。
// restoreState 直传上次的书，避免"先开第一本再切换"的双开浪费
// （探针：8MB 书 Open+BuildTOC 一次约 320ms，双开白付一次还闪屏）。
func loadFolderOpen(dir, openPath string) {
	curDir = dir
	ents, err := collectTxt(dir)
	if err != nil {
		// 目录读失败（权限/盘符未挂载）：明确报失败，不伪装成"无文档"
		curEntries = nil
		_ = lb.SetModel([]string{})
		if curBook != nil {
			curBook.Close()
			curBook = nil
		}
		te.SetText("")
		folderLbl.SetText(fmt.Sprintf("文件夹：%s（读取失败：%v）", dir, err))
		return
	}
	curEntries = ents
	if err := lb.SetModel(names()); err != nil {
		return
	}
	// 先关闭旧书：SetModel 会按「当前值」恢复选中文本，若新文件夹恰好有同名文件
	// 且位置相同，索引不变、CurrentIndexChanged 不触发，只靠事件会一直显示旧书。
	if curBook != nil {
		curBook.Close()
		curBook = nil
	}
	if len(curEntries) > 0 {
		folderLbl.SetText(fmt.Sprintf("文件夹：%s（%d 篇）", dir, len(curEntries)))
		idx := indexOfPath(openPath) // 空/未命中 → 0
		lb.SetCurrentIndex(idx)
		openAt(idx) // 兜底：事件未触发也要打开（已打开则同路径判断自动跳过）
	} else {
		te.SetText("")
		folderLbl.SetText(fmt.Sprintf("文件夹：%s（无文档）", dir))
	}
}

// indexOfPath 在 curEntries 中找 path 的索引；未命中或 path 为空返回 0。
func indexOfPath(path string) int {
	if path != "" {
		for i, e := range curEntries {
			if e.path == path {
				return i
			}
		}
	}
	return 0
}

func pickAndLoad() {
	dir := chooseFolder()
	if dir == "" {
		return
	}
	loadFolder(dir)
}

func chooseFontDlg(owner walk.Form) {
	families := []string{"微软雅黑", "宋体", "黑体", "楷体", "仿宋", "等线", "Consolas", "Arial"}
	sizes := make([]string, 0, 33)
	for i := 8; i <= 40; i++ {
		sizes = append(sizes, strconv.Itoa(i))
	}
	spacings := []string{"紧凑", "标准", "宽松", "超宽"}
	fi := 0
	for i, f := range families {
		if f == curFamily {
			fi = i
			break
		}
	}
	si := curSize - 8
	if si < 0 {
		si = 0
	}
	if si >= len(sizes) {
		si = len(sizes) - 1
	}
	spi := curSpacing
	if spi < 0 {
		spi = 0
	}
	if spi >= len(spacings) {
		spi = len(spacings) - 1
	}

	var familyCB, sizeCB, spacingCB *walk.ComboBox
	var dlg *walk.Dialog
	// 在 OnClicked（Accept 之前）读值——对话框关闭后 ComboBox 状态会被重置
	picked := func() {
		if i := familyCB.CurrentIndex(); i >= 0 {
			curFamily = families[i]
		}
		if i := sizeCB.CurrentIndex(); i >= 0 {
			if v, e := strconv.Atoi(sizes[i]); e == nil {
				curSize = v
			}
		}
		if i := spacingCB.CurrentIndex(); i >= 0 {
			curSpacing = i
		}
		if f, e := walk.NewFont(curFamily, curSize, 0); e == nil {
			readerFont = f
		}
		// 即时重渲染：字体/行距变化作用于全部已累积内容。
		// 只渲染当前页会让视图缩回一页，且后续 appendPage 会把旧缓冲弹回来。
		if curBook != nil {
			// 记住首可见行，渲染后拉回——WM_SETTEXT 后视口/插入符回 0，
			// 不拉回的话调字号时正在读的段落会跳回缓冲顶部。
			var first int
			if te != nil {
				first = int(te.SendMessage(win.EM_GETFIRSTVISIBLELINE, 0, 0))
			}
			render(strings.Join(accumBuf, pageJoin()))
			if te != nil {
				te.SendMessage(win.WM_VSCROLL, uintptr(win.SB_THUMBPOSITION)|(uintptr(first)<<16), 0)
			}
		}
		persist() // 字体/行距偏好落盘
	}

	d := Dialog{
		AssignTo: &dlg,
		Title:    "选择字体",
		MinSize:  Size{Width: 300, Height: 200},
		Layout:   VBox{},
		Children: []Widget{
			Composite{
				Layout: Grid{Columns: 2},
				Children: []Widget{
					Label{Text: "字体："},
					ComboBox{AssignTo: &familyCB, Model: families, CurrentIndex: fi},
					Label{Text: "字号："},
					ComboBox{AssignTo: &sizeCB, Model: sizes, CurrentIndex: si},
					Label{Text: "行间距："},
					ComboBox{AssignTo: &spacingCB, Model: spacings, CurrentIndex: spi},
				},
			},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					PushButton{Text: "确定", OnClicked: func() { picked(); dlg.Accept() }},
					PushButton{Text: "取消", OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}
	if _, err := d.Run(owner); err != nil {
		return
	}
}

// cleanOptionsDlg 清洗设置对话框：段首缩进 / 广告行过滤 / 空行全收（方案 §2.4）。
// 确定即落位：改 Book 口径 → 按 rune 偏移口径回跳原页 → 重建目录 → 持久化。
// .md 自动豁免 Indent/BlankAll（勾了也不生效，effectiveClean 强制）。
func cleanOptionsDlg(owner walk.Form) {
	var cbIndent, cbAd, cbBlank *walk.CheckBox
	var dlg *walk.Dialog
	apply := func() {
		curClean.Indent = cbIndent.Checked()
		curClean.AdFilter = cbAd.Checked()
		curClean.BlankAll = cbBlank.Checked()
		if curBook == nil {
			return
		}
		// B6：SetCleanOpts 内部已按原页码 readUpTo 回位，紧随的 Goto(page) 是
		// 纯重复劳动（深页切档成本 2×O(文件)，UI 线程冻结翻倍）——删。
		// SetCleanOpts 口径未变时自带防抖早退，重复点确定也无重读。
		curBook.SetCleanOpts(effectiveClean(curBook.Path()))
		showCurrent()
		showTOC() // 广告行剔除/空行全收会改变目录扫描所见，重建
		persist()
	}
	d := Dialog{
		AssignTo: &dlg,
		Title:    "清洗设置",
		MinSize:  Size{Width: 340, Height: 220},
		Layout:   VBox{},
		Children: []Widget{
			Label{Text: "只影响显示，不修改源文件；重开即恢复。", TextColor: walk.RGB(120, 120, 120)},
			CheckBox{AssignTo: &cbIndent, Text: "段首缩进规整（统一 2 个全角空格）", Checked: curClean.Indent},
			CheckBox{AssignTo: &cbAd, Text: "过滤整行广告（求票/网址/笔趣阁等）", Checked: curClean.AdFilter},
			CheckBox{AssignTo: &cbBlank, Text: "空行全收（配合段首缩进 = 纸书排版）", Checked: curClean.BlankAll},
			Label{Text: "注：.md 文件自动豁免缩进与空行全收（保留块语义）。", TextColor: walk.RGB(120, 120, 120)},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					PushButton{Text: "确定", OnClicked: func() { apply(); dlg.Accept() }},
					PushButton{Text: "取消", OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}
	if _, err := d.Run(owner); err != nil {
		return
	}
}

// encCandidates 编码兜底的可选列表（中文书库为主，覆盖常见乱码来源）。
type encItem struct {
	name string
	enc  encoding.Encoding
}

var encCandidates = []encItem{
	{"UTF-8", xunicode.UTF8},
	{"GB18030", simplifiedchinese.GB18030},
	{"GBK", simplifiedchinese.GBK},
	{"UTF-16LE", xunicode.UTF16(xunicode.LittleEndian, xunicode.UseBOM)},
	{"UTF-16BE", xunicode.UTF16(xunicode.BigEndian, xunicode.UseBOM)},
	{"Big5", traditionalchinese.Big5},
}

// stretchSetter 是 HSplitter 布局暴露的拉伸因子接口（walk 未直接导出方法名）。
type stretchSetter interface{ SetStretchFactor(walk.Widget, int) error }

// chooseEncodingDlg 编码兜底对话框：列出候选编码，选中即用该编码重新解码当前书。
// 仅在已打开书时可用；选完回到首页重渲染，状态栏编码名同步。
func chooseEncodingDlg(owner walk.Form) {
	if curBook == nil {
		walk.MsgBox(owner, "提示", "请先打开一本书，再切换编码。", walk.MsgBoxIconInformation)
		return
	}
	// L2（2026-10-23 审查）：EPUB 解包后是 UTF-8 内存流，原文已在解包时定死，
	// 换编码重解必然乱码且无法回退——直接拦截。
	if curBook.IsEpub() {
		walk.MsgBox(owner, "提示", "EPUB 内容已解包为 UTF-8 文本，无需也无法切换编码。", walk.MsgBoxIconInformation)
		return
	}
	labels := make([]string, len(encCandidates))
	cur := -1
	for i, c := range encCandidates {
		labels[i] = c.name
		if strings.EqualFold(c.name, curBook.EncName()) {
			cur = i
		}
	}
	idx := cur
	if idx < 0 {
		idx = 0
	}
	var cb *walk.ComboBox
	var dlg *walk.Dialog
	apply := func() {
		i := cb.CurrentIndex()
		if i < 0 || i >= len(encCandidates) {
			return
		}
		item := encCandidates[i]
		if err := curBook.ReopenWith(item.enc, item.name); err != nil {
			walk.MsgBox(owner, "切换编码失败", err.Error(), walk.MsgBoxIconError)
			return
		}
		// ReopenWith 已把游标重置回第 0 页：必须重建累积缓冲，
		// 否则后续 appendPage 会把旧编码的页 Join 回来（旧乱码复活）。
		// showCurrent 同时刷新状态栏编码名并持久化（页码归 0）。
		showCurrent()
		showTOC() // 目录按新编码重建
	}
	d := Dialog{
		AssignTo: &dlg,
		Title:    "切换编码",
		MinSize:  Size{Width: 280, Height: 130},
		Layout:   VBox{},
		Children: []Widget{
			Label{Text: "选择编码（切换后从首页重新解码）："},
			ComboBox{AssignTo: &cb, Model: labels, CurrentIndex: idx},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					PushButton{Text: "确定", OnClicked: func() { apply(); dlg.Accept() }},
					PushButton{Text: "取消", OnClicked: func() { dlg.Cancel() }},
				},
			},
		},
	}
	if _, err := d.Run(owner); err != nil {
		return
	}
}

// tocGen 构建代次：每次 showTOC 递增；后台构建完成时代次不符说明
// 期间已换书/换编码，结果作废（防旧书目录灌进新书）。
var tocGen int

// showTOC 构建当前书的章节目录，并在右侧目录栏显示/隐藏。
// 有目录（≥1 章）→ 显示栏并填充；无目录 → 隐藏栏（自动隐显）。
// 构建放后台 goroutine（大书全扫实测 600ms+，机械盘更久——放 UI 线程
// 开书瞬间会卡死），期间目录栏显示"目录加载中…"占位。
func showTOC() {
	if tocLB == nil {
		return
	}
	if curBook == nil {
		tocGen++
		tocLB.SetVisible(false)
		curTOC = nil
		return
	}
	tocGen++
	gen := tocGen
	buildFn := curBook.TOCFunc() // UI 线程取快照（txt=文件参数；epub=内存构建）
	curTOC = nil
	tocLoading = true
	_ = tocLB.SetModel([]string{"目录加载中…"})
	tocLoading = false
	tocLB.SetVisible(true)
	go func() {
		toc, err := buildFn()
		// mw 已关/测试环境无主窗：直接丢弃，绝不空指针。
		// 正式运行里 showTOC 只在窗口建成后调用，mw 恒非空；此守卫兜住
		// 测试直接驱动 loadFolder/openAt 的路径（后台协程晚到时窗口已卸）。
		if mw == nil {
			return
		}
		mw.Synchronize(func() {
			applyTOCIf(gen, toc, err)
		})
	}()
}

// applyTOCIf 代次校验后落地：gen 过期（期间已换书/换编码）则丢弃结果。
// 拆出来是为了可测——Synchronize 回调里只留这一行。
func applyTOCIf(gen int, toc []reader.TocEntry, err error) {
	if gen != tocGen {
		return
	}
	applyTOC(toc, err)
}

// applyTOC 把（可能后台算好的）目录结果落到 UI；须在 UI 线程调用。
func applyTOC(toc []reader.TocEntry, err error) {
	if err != nil || len(toc) == 0 {
		tocLB.SetVisible(false)
		curTOC = nil
		return
	}
	curTOC = toc
	titles := make([]string, len(toc))
	for i, e := range toc {
		titles[i] = e.Title
	}
	// tocLoading 必须护住整个落地过程（SetModel/SetCurrentIndex/SetVisible/
	// SetStretchFactor 都可能触发 OnCurrentIndexChanged）——现场日志抓到
	// TOC 弹出后 tocJump 被异步事件拽到尾部条目的"漂移"。落地完清掉选中态，
	// 不给任何"无人点击的章节跳转"留口子。
	tocLoading = true
	defer func() { tocLoading = false }()
	_ = tocLB.SetModel(titles)
	_ = tocLB.SetCurrentIndex(-1)
	tocLB.SetVisible(true)
	if hs != nil {
		if ss, ok := hs.Layout().(stretchSetter); ok {
			_ = ss.SetStretchFactor(tocLB, 1)
		}
	}
}

// tocJump 跳转到选中的章节：定位到标题所在页 + 页内精确定位 + 滚动到第一行。
func tocJump() {
	if tocLoading || curBook == nil {
		return
	}
	idx := tocLB.CurrentIndex()
	if idx < 0 || idx >= len(curTOC) {
		return
	}
	e := curTOC[idx]
	text, _, err := curBook.SeekTo(e.Offset, e.Title)
	if err != nil {
		return
	}
	// E2（2026-10-23 审查）：缓冲被重置为跳入章节这一页，坐标系必须跟着走——
	// 否则 captureView 用旧 accumBase 定页，跳章后关窗存下错误 ViewPage，
	// 重开恢复错位，且错值污染后续整个会话的锚点页映射。
	accumBase = curBook.Page()
	accumBuf = []string{text}
	dispStale = true
	render(text)
	// 美化会改变每行字符数，在显示文本里重搜标题位置，避免错行
	scrollCaretToTop(displayRuneOffset(text, e.Title))
	if folderLbl != nil {
		base := filepath.Dir(curBook.Path())
		folderLbl.SetText(fmt.Sprintf("文件夹：%s（%d 篇）   当前编码：%s", base, len(curEntries), curBook.EncName()))
	}
	persist() // 章节跳转即落盘，异常退出不丢进度
}

// scrollCaretToTop 把 caret 移到页内 rune 偏移 off 处，并滚动使该行成为第一可见行，
// 让跳转目标（章节标题）显示在阅读区第一行。
func scrollCaretToTop(off int) {
	if te == nil {
		return
	}
	te.SendMessage(win.EM_SETSEL, uintptr(off), uintptr(off))
	line := int32(te.SendMessage(win.EM_LINEFROMCHAR, uintptr(off), 0))
	te.SendMessage(win.WM_VSCROLL, uintptr(win.SB_THUMBPOSITION)|(uintptr(line)<<16), 0)
}

// teAtBottom 判断阅读区是否已垂直滚到本页底部。
// 只有内容超过一屏（有滚动空间 NMax>NPage）且已到底才算，避免短页误判。
func teAtBottom() bool {
	if te == nil {
		return false
	}
	var si win.SCROLLINFO
	si.CbSize = uint32(unsafe.Sizeof(si))
	si.FMask = win.SIF_RANGE | win.SIF_PAGE | win.SIF_POS
	if !win.GetScrollInfo(te.Handle(), win.SB_VERT, &si) {
		return false
	}
	return int(si.NMax) > int(si.NPage) && int(si.NPos)+int(si.NPage) >= int(si.NMax)
}

// ---- 累积式无限滚动：一条滚动条读全文 ----
// 滚到底自动往后追加下一页，内容越读越长；向上滚自由回看（前文都在缓冲里）。
// 换页式方案（每页整屏替换）在页顶/页底没有滚动反馈、回翻要靠滚轮方向，
// 钩窗口过程或低级钩子都有坑；累积式彻底绕开方向问题。

var (
	accumBuf  []string // 已显示的各页文本（clean 后，\n 形式）
	accumBase int      // 缓冲首页的页码——视口锚点定位"锚点行在哪一页"的坐标系
)

// startAccumFrom 以 page 页为缓冲起点：只显示该页，之后滚到底继续向后追加。
// TOC 跳转/末页重置缓冲时使用（起点之前的内容不加载，向上滚到缓冲顶即停）。
func startAccumFrom(page int) {
	accumBase = page
	accumBuf = []string{curBook.Current()}
	dispStale = true
	render(curBook.Current())
}

// vertScrollPos 读阅读区垂直滚动位置。
func vertScrollPos() int {
	if te == nil {
		return 0
	}
	var si win.SCROLLINFO
	si.CbSize = uint32(unsafe.Sizeof(si))
	si.FMask = win.SIF_POS
	if !win.GetScrollInfo(te.Handle(), win.SB_VERT, &si) {
		return 0
	}
	return int(si.NPos)
}

// appendPage 把新页追加进阅读区，并保持用户视线（位置不动，新内容接在下方）。
//
// 纯文本走 EM_REPLACESEL 增量追加：只灌「页间隔 + 新页」，成本只与新页有关，
// 不随累积量增长。旧实现每次 Join 全缓冲 + SetText 全文重灌——WM_SETTEXT 对
// EDIT 是全文重排（探针：隐藏窗口 110 页时全量 SetText 30.4s vs 增量追加 5.6ms）。
// 页面经 clean() TrimSpace，页间 "\n\n" 不与页内换行粘连，applySpacing/disp
// 可分块等价（TestAppendPageIncremental 按四档行距锁死等价性）。
//
// MD 仍走全量重灌：mdBeautify 的围栏状态机与表格对齐跨行块，
// 围栏/表格可能跨页边界，按页增量会断状态——正确性优先。
func appendPage(s string) {
	accumBuf = append(accumBuf, s)
	dispStale = true
	oldPos := vertScrollPos()
	if curIsMD {
		te.SetText(displayText(strings.Join(accumBuf, pageJoin())))
	} else {
		appendTextTE(displayText(pageJoin() + s))
	}
	te.SendMessage(win.WM_VSCROLL, uintptr(win.SB_THUMBPOSITION)|(uintptr(oldPos)<<16), 0)
}

// appendTextTE 把 text 追加到 TextEdit 末尾（EM_SETSEL 末尾 + EM_REPLACESEL），
// 不动已有内容。末尾偏移取 WM_GETTEXTLENGTH——它与 EM_SETSEL 同为 UTF-16
// code unit 口径，页内出现 emoji 也不会错位（顺带免疫 P3）。
//
// EM_SETLIMITTEXT 必须先提限：EDIT 默认文本上限 30000 字符，WM_SETTEXT
// 绕过它（全量重灌路径从没事），EM_REPLACESEL 却受它约束——超限插入被
// 截断/拒绝（探针：默认上限 30000，提限后 10 万字符仍可增量插）。
func appendTextTE(text string) {
	te.SendMessage(win.EM_SETLIMITTEXT, uintptr(0x7FFFFFFE), 0)
	end := te.SendMessage(win.WM_GETTEXTLENGTH, 0, 0)
	te.SendMessage(win.EM_SETSEL, end, end)
	utf16, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	te.SendMessage(win.EM_REPLACESEL, 0, uintptr(unsafe.Pointer(utf16)))
	runtime.KeepAlive(utf16)
}

// appendTick 泵：到底且还有下一页 → 追加（追加后位置不在底，不会连续误触发）。
// 进度节流落盘：翻页推进后 5 秒至多 persist 一次（原实现只靠关窗兜底，
// 进程被杀会丢掉自上次落盘后的全部翻页；页码是 raw 口径，写的就是当前值）。
var lastProgressPersist time.Time

func appendTick() {
	if curBook == nil || te == nil {
		return
	}
	if !teAtBottom() {
		return
	}
	s, more, err := curBook.LoadNextPage()
	if err != nil || !more {
		return
	}
	appendPage(s)
	if now := time.Now(); now.Sub(lastProgressPersist) >= 5*time.Second {
		lastProgressPersist = now
		persist()
	}
}

// startInfiniteScroll 定时驱动 appendTick（累积式加载泵）。
func startInfiniteScroll(stop chan struct{}) {
	go func() {
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if mw == nil {
					return
				}
				mw.Synchronize(func() {
					appendTick()
				})
			}
		}
	}()
}

func main() {
	readerFont, _ = walk.NewFont(curFamily, curSize, 0)

	builder := MainWindow{
		AssignTo: &mw,
		Title:    "TXT 阅读器",
		MinSize:  Size{Width: 900, Height: 600},
		Size:     Size{Width: 1200, Height: 800},
		Layout:   VBox{},
		Children: []Widget{
			ToolBar{
				Items: []MenuItem{
					// Image 为 rsrc 资源 ID（group icon 每个 .ico 占 2 个 ID：组=偶数，裸图=奇数）
					// manifest=1, folder=2, font=4, first=6, prev=8, next=10, last=12, refresh=14, encode=16
					// 清洗设置暂无图标（icons/ 无对应 .ico，补图标后加 Image 即可）
					Action{Text: "打开文件夹", Image: 2, OnTriggered: func() { pickAndLoad() }},
					Action{Text: "刷新", Image: 14, OnTriggered: func() { refreshCurrent(-1) }},
					Action{Text: "选择字体", Image: 4, OnTriggered: func() { chooseFontDlg(mw) }},
					Action{Text: "切换编码", Image: 16, OnTriggered: func() { chooseEncodingDlg(mw) }},
					Action{Text: "清洗设置", OnTriggered: func() { cleanOptionsDlg(mw) }},
					Action{Text: "首页", Image: 6, OnTriggered: func() { pageFirst() }},
					Action{Text: "上一页", Image: 8, OnTriggered: func() { pagePrev() }},
					Action{Text: "下一页", Image: 10, OnTriggered: func() { pageNext() }},
					Action{Text: "末页", Image: 12, OnTriggered: func() { pageLast() }},
				},
			},
			Label{AssignTo: &folderLbl, Text: "未选择文件夹（点“打开文件夹”开始）"},
			HSplitter{
				AssignTo: &hs,
				Children: []Widget{
					ListBox{
						AssignTo: &lb,
						Model:    []string{},
						OnCurrentIndexChanged: func() {
							openAt(lb.CurrentIndex())
						},
						OnKeyDown: func(key walk.Key) {
							switch key {
							case walk.KeyDelete:
								deleteCurrent()
							case walk.KeyF5:
								refreshCurrent(-1)
							}
						},
						ContextMenuItems: []MenuItem{
							Action{Text: "删除（移入回收站）", OnTriggered: func() { deleteCurrent() }},
						},
					},
					ListBox{
						AssignTo: &tocLB,
						Visible:  false,
						Model:    []string{},
						OnCurrentIndexChanged: func() {
							tocJump()
						},
					},
					TextEdit{
						AssignTo: &te,
						ReadOnly: true,
						VScroll:  true,
						Font:     Font{Family: curFamily, PointSize: curSize},
						OnKeyDown: func(key walk.Key) {
							switch key {
							case walk.KeyNext:
								pageNext()
							case walk.KeyPrior:
								pagePrev()
							case walk.KeyHome:
								pageFirst()
							case walk.KeyEnd:
								pageLast()
							}
						},
					},
				},
			},
		},
	}

	if err := builder.Create(); err != nil {
		walk.MsgBox(nil, "初始化失败", err.Error(), walk.MsgBoxIconError)
		return
	}
	// 书列表 : 目录栏 : 阅读区 = 1 : 1 : 5（目录栏默认隐藏，showTOC 显示时占 1）
	if ss, ok := hs.Layout().(stretchSetter); ok {
		_ = ss.SetStretchFactor(lb, 1)
		_ = ss.SetStretchFactor(te, 5)
	}
	// 退出时再存一次（兜底；平时每次翻页/换书已即时存），并停止加载泵
	stopInfinite := make(chan struct{})
	mw.Closing().Attach(func(cancel *bool, reason walk.CloseReason) {
		persist()
		close(stopInfinite)
	})
	// 命令行路径参数（README 承诺）：txtreader.exe [路径]，路径可为文件或目录。
	// 有参数 → 直接打开该路径；无参数 → 恢复上次会话。
	// B1：偏好（字体/行距/清洗）与窗口几何两条启动路都恢复——CLI 路径原来
	// 整个跳过 restoreState，双击打开文件时偏好全部回默认。
	// 卫生6：状态文件一次读取共享（原两条路各读一次）。
	lastState, _ = loadState()
	restorePrefs(lastState)
	restoreWindowRect(lastState)
	if !openCLIPath() {
		restoreSession(lastState) // 恢复上次会话（目录+书），页码由 openAt 统一恢复
	}
	startInfiniteScroll(stopInfinite) // 累积式加载泵：滚到底自动追加下一页
	mw.Run()
}

// openCLIPath 处理命令行路径参数；返回是否成功接管启动（无参数/路径无效返回 false）。
// 文件 → 打开所在目录并选中该文件；目录 → 打开该目录。
func openCLIPath() bool {
	if len(os.Args) < 2 {
		return false
	}
	p := filepath.Clean(os.Args[1]) // 统一分隔符，保证与 collectTxt 的 Join 路径可比
	fi, err := os.Stat(p)
	if err != nil {
		return false
	}
	if fi.IsDir() {
		loadFolder(p)
		return true
	}
	// 文件：打开所在目录并定位到该文件
	loadFolderOpen(filepath.Dir(p), p)
	return true
}
