package main

import (
	"fmt"
	"os"
	"path/filepath"
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
		if ext != ".txt" && ext != ".md" {
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
	b, err := reader.Open(curEntries[idx].path)
	if err != nil {
		te.SetText(disp(fmt.Sprintf("无法打开文件：\n%s\n\n%v", curEntries[idx].path, err)))
		return
	}
	curBook = b
	curIsMD = strings.EqualFold(filepath.Ext(curEntries[idx].path), ".md")
	showCurrent()
	showTOC()
}

func showCurrent() {
	if curBook == nil {
		return
	}
	accumBase = curBook.Page()
	accumBuf = []string{curBook.Current()}
	render(curBook.Current())
	if folderLbl != nil {
		base := filepath.Dir(curBook.Path())
		folderLbl.SetText(fmt.Sprintf("文件夹：%s（%d 篇）   当前编码：%s", base, len(curEntries), curBook.EncName()))
	}
	persist() // 打开书即持久化（目录+书+页码0）
}

// persist 把当前会话状态写到 exe 同目录的 txtreader.state.json。
func persist() {
	st := appState{
		LastDir:     curDir,
		FontFamily:  curFamily,
		FontSize:    curSize,
		LineSpacing: curSpacing,
	}
	if curBook != nil {
		st.CurrentPath = curBook.Path()
		st.Page = curBook.Page()
	}
	saveState(st)
}

// restoreState 启动时恢复上次会话（目录+书+页码+外观）；目录/书不存在则优雅降级。
func restoreState() {
	st, ok := loadState()
	if !ok || st.LastDir == "" {
		return
	}
	// 先恢复外观偏好（字体/字号/行间距），后续 render 会自动应用
	if st.FontFamily != "" {
		curFamily = st.FontFamily
	}
	if st.FontSize > 0 {
		curSize = st.FontSize
	}
	if st.LineSpacing >= 0 && st.LineSpacing <= 3 {
		curSpacing = st.LineSpacing // 越界脏值不采用（缺字段时为 0=紧凑，与旧版行为一致）
	}
	if f, e := walk.NewFont(curFamily, curSize, 0); e == nil {
		readerFont = f
	}
	loadFolder(st.LastDir) // 内部已持久化 + 打开第一本（render 用上面的字体/行距）
	if st.CurrentPath == "" || curBook == nil {
		return
	}
	for i, e := range curEntries {
		if e.path == st.CurrentPath {
			lb.SetCurrentIndex(i) // 触发 openAt 打开该书
			if st.Page > 0 && curBook != nil {
				if err := curBook.Goto(st.Page); err == nil {
					showCurrent()
				}
			}
			return
		}
	}
	// 书已不在：loadFolder 已打开第一本，保持即可
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
		lb.SetCurrentIndex(0)
		openAt(0) // 兜底：事件未触发也要打开（已打开则同路径判断自动跳过）
	} else {
		te.SetText("")
		folderLbl.SetText(fmt.Sprintf("文件夹：%s（无文档）", dir))
	}
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
			render(strings.Join(accumBuf, "\n\n"))
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

// showTOC 构建当前书的章节目录，并在右侧目录栏显示/隐藏。
// 有目录（≥1 章）→ 显示栏并填充；无目录 → 隐藏栏（自动隐显）。
func showTOC() {
	if tocLB == nil {
		return
	}
	if curBook == nil {
		tocLB.SetVisible(false)
		curTOC = nil
		return
	}
	toc, err := curBook.BuildTOC()
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
	tocLoading = true
	_ = tocLB.SetModel(titles)
	tocLoading = false
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
	accumBase = curBook.Page()
	accumBuf = []string{text}
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
	accumBase int      // 缓冲首页的页码（TOC 跳转/末页后 >0）
)

// startAccumFrom 以 page 页为缓冲起点：只显示该页，之后滚到底继续向后追加。
// TOC 跳转/末页重置缓冲时使用（起点之前的内容不加载，向上滚到缓冲顶即停）。
func startAccumFrom(page int) {
	accumBase = page
	accumBuf = []string{curBook.Current()}
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
func appendPage(s string) {
	accumBuf = append(accumBuf, s)
	full := strings.Join(accumBuf, "\n\n")
	oldPos := vertScrollPos()
	te.SetText(displayText(full))
	te.SendMessage(win.WM_VSCROLL, uintptr(win.SB_THUMBPOSITION)|(uintptr(oldPos)<<16), 0)
}

// appendTick 泵：到底且还有下一页 → 追加（追加后位置不在底，不会连续误触发）。
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
					// manifest=1, folder=2, font=4, first=6, prev=8, next=10, last=12, refresh=14
					Action{Text: "打开文件夹", Image: 2, OnTriggered: func() { pickAndLoad() }},
					Action{Text: "刷新", Image: 14, OnTriggered: func() { refreshCurrent(-1) }},
					Action{Text: "选择字体", Image: 4, OnTriggered: func() { chooseFontDlg(mw) }},
					Action{Text: "切换编码", Image: 16, OnTriggered: func() { chooseEncodingDlg(mw) }},
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
	restoreState()                    // 启动恢复上次会话（目录+书+页码），缺失优雅降级
	startInfiniteScroll(stopInfinite) // 累积式加载泵：滚到底自动追加下一页
	mw.Run()
}
