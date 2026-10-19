# -*- coding: utf-8 -*-
"""解析 PE 资源目录，枚举 RT_ICON / RT_GROUP_ICON / RT_MANIFEST 的 ID（验证嵌入）。
注意 PE 规范：资源目录子项偏移相对资源节起始（低 31 位），不是绝对 RVA。"""
import struct, sys

path = sys.argv[1] if len(sys.argv) > 1 else r'D:\project\txt\txtreader\txtreader.exe'
data = open(path, 'rb').read()
pe = struct.unpack_from('<I', data, 0x3C)[0]
opt = pe + 24
magic = struct.unpack_from('<H', data, opt)[0]
dd_base = 112 if magic == 0x20B else 96   # PE32+ / PE32
rsrc_rva = struct.unpack_from('<I', data, opt + dd_base + 16)[0]  # DataDirectory[2]
nsec = struct.unpack_from('<H', data, pe + 6)[0]
optsize = struct.unpack_from('<H', data, pe + 20)[0]
secoff = opt + optsize
rsrc_sec_off = None
for i in range(nsec):
    o = secoff + i * 40
    va, = struct.unpack_from('<I', data, o + 12)
    if va == rsrc_rva:
        rsrc_sec_off, = struct.unpack_from('<I', data, o + 20)  # PointerToRawData
        break
assert rsrc_sec_off is not None, '.rsrc section not found'

TYPES = {1: 'RT_ICON', 3: 'RT_ICON', 14: 'RT_GROUP_ICON', 16: 'RT_VERSION', 24: 'RT_MANIFEST'}
cur_type = [None]
cur_id = [None]

def walk(off, level):
    nnamed, nid = struct.unpack_from('<HH', data, off + 12)
    for i in range(nnamed + nid):
        nameid, val = struct.unpack_from('<II', data, off + 16 + i * 8)
        if level == 0:
            cur_type[0] = nameid
        if level == 1:
            cur_id[0] = nameid
        if val & 0x80000000:
            walk(rsrc_sec_off + (val & 0x7FFFFFFF), level + 1)
        elif level == 2:
            # 数据描述结构：DataRVA(4) Size(4) Codepage(4) Reserved(4)
            desc = rsrc_sec_off + (val & 0x7FFFFFFF)
            _, dsz = struct.unpack_from('<II', data, desc)
            print(f'{TYPES.get(cur_type[0], "?"):14s} id={cur_id[0]:3d} size={dsz}')

walk(rsrc_sec_off, 0)
