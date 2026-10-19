# -*- coding: utf-8 -*-
"""生成工具栏 8 个 24x24 32bpp 透明 ICO（纯标准库，零依赖）。
产出: icons/folder.ico font.ico first.ico prev.ico next.ico last.ico refresh.ico encode.ico
rsrc 分配的资源 ID（group icon 偶数，裸图奇数）:
  manifest=1, folder=2, font=4, first=6, prev=8, next=10, last=12, refresh=14, encode=16
"""
import struct, os, math

W = H = 24
BLUE = (30, 80, 180)      # RGB
DARKYELLOW = (170, 125, 20)
YELLOW = (245, 190, 60)
WHITE = (255, 255, 255)

def new_canvas():
    return [[None] * W for _ in range(H)]  # None = 透明

def px(c, x, y, col):
    if 0 <= x < W and 0 <= y < H:
        c[y][x] = col

def rect(c, x0, y0, x1, y1, col):
    for y in range(y0, y1 + 1):
        for x in range(x0, x1 + 1):
            px(c, x, y, col)

def hline(c, x0, x1, y, col):
    rect(c, x0, y, x1, y, col)

def vline(c, x, y0, y1, col, th=1):
    rect(c, x, y0, x + th - 1, y1, col)

def tri(c, pts, col):
    ys = [p[1] for p in pts]
    for y in range(min(ys), max(ys) + 1):
        xs = []
        for i in range(3):
            x1, y1 = pts[i]
            x2, y2 = pts[(i + 1) % 3]
            if y1 == y2:
                continue
            if (y1 <= y < y2) or (y2 <= y < y1):
                xs.append(x1 + (y - y1) * (x2 - x1) / (y2 - y1))
        if len(xs) >= 2:
            a, b = int(min(xs)), int(max(xs))
            for x in range(a, b + 1):
                px(c, x, y, col)

def save_ico(path, c):
    # XOR (BGRA, bottom-up) + AND mask 全 0
    xor = bytearray()
    for y in range(H - 1, -1, -1):
        for x in range(W):
            p = c[y][x]
            if p is None:
                xor += b"\x00\x00\x00\x00"
            else:
                xor += bytes((p[2], p[1], p[0], 255))
    andmask = bytearray(H * ((W + 31) // 32 * 4))  # 全 0
    hdr = struct.pack("<IiiHHIIiiII", 40, W, H * 2, 1, 32, 0,
                      len(xor) + len(andmask), 0, 0, 0, 0)
    img = hdr + bytes(xor) + bytes(andmask)
    entry = struct.pack("<BBBBHHII", W, H, 0, 0, 1, 32, len(img), 22)
    data = struct.pack("<HHH", 0, 1, 1) + entry + img
    with open(path, "wb") as f:
        f.write(data)

# ---- 1. folder.ico：黄文件夹 ----
c = new_canvas()
rect(c, 2, 5, 10, 9, YELLOW)          # 上标签
rect(c, 2, 8, 21, 19, YELLOW)         # 主体
# 描边
hline(c, 2, 21, 5, DARKYELLOW); hline(c, 2, 21, 19, DARKYELLOW)
vline(c, 2, 5, 19, DARKYELLOW); vline(c, 21, 8, 19, DARKYELLOW)
hline(c, 2, 10, 9, DARKYELLOW)
save_ico("icons/folder.ico", c)

# ---- 2. font.ico：白色圆底 + 蓝色 A（3x5 像素字放大 3 倍）----
c = new_canvas()
rect(c, 1, 1, 22, 22, WHITE)          # 底
A = [".A.", "A.A", "AAA", "A.A", "A.A"]
ox, oy = 7, 4                          # 3 倍放大后 9x15，画布居中
for row, line in enumerate(A):
    for col, ch in enumerate(line):
        if ch == "A":
            rect(c, ox + col * 3, oy + row * 3, ox + col * 3 + 2, oy + row * 3 + 2, BLUE)
save_ico("icons/font.ico", c)

# ---- 3. first.ico：竖线 + 左三角 ----
c = new_canvas()
vline(c, 4, 4, 19, BLUE, 3)
tri(c, [(10, 12), (20, 4), (20, 19)], BLUE)
save_ico("icons/first.ico", c)

# ---- 4. prev.ico：左三角 ----
c = new_canvas()
tri(c, [(5, 12), (19, 3), (19, 21)], BLUE)
save_ico("icons/prev.ico", c)

# ---- 5. next.ico：右三角 ----
c = new_canvas()
tri(c, [(18, 12), (4, 3), (4, 21)], BLUE)
save_ico("icons/next.ico", c)

# ---- 6. last.ico：右三角 + 竖线 ----
c = new_canvas()
tri(c, [(13, 12), (3, 4), (3, 19)], BLUE)
vline(c, 17, 4, 19, BLUE, 3)
save_ico("icons/last.ico", c)

# ---- 7. refresh.ico：开口圆环 + 箭头（⟳，开口在右上）----
import math
c = new_canvas()
# 圆环：角度 -80°..190°（开口在右上 190°..280°），线宽约 2px
for deg10 in range(-800, 1900, 15):
    a = math.radians(deg10 / 10.0)
    x = 12 + 8 * math.cos(a)
    y = 12 + 8 * math.sin(a)
    for dx in (0, 1):
        for dy in (0, 1):
            px(c, int(round(x)) + dx, int(round(y)) + dy, BLUE)
# 箭头三角：指向开口右端（约 -80° 处），朝右下
tri(c, [(14, 2), (22, 6), (15, 10)], BLUE)
save_ico("icons/refresh.ico", c)

# ---- 8. encode.ico：地球（圆环 + 经纬线），代表编码/语言切换 ----
c = new_canvas()
# 圆环：半径 9，中心 (12,12)
for deg in range(0, 3600, 12):
    a = math.radians(deg / 10.0)
    x = 12 + 9 * math.cos(a)
    y = 12 + 9 * math.sin(a)
    for dx in (0, 1):
        for dy in (0, 1):
            px(c, int(round(x)) + dx, int(round(y)) + dy, BLUE)
hline(c, 4, 20, 12, BLUE)   # 赤道
vline(c, 12, 4, 20, BLUE, 1) # 本初子午线
save_ico("icons/encode.ico", c)

print("done:", os.listdir("icons"))
