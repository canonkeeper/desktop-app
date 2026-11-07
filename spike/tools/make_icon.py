# SPDX-License-Identifier: AGPL-3.0-only
"""Writes a plain 1024x1024 PNG for `tauri icon`: the probe ships no artwork."""
import struct
import sys
import zlib

SIZE = 1024


def pixel(x, y):
    dx, dy = x - SIZE / 2, y - SIZE / 2
    if dx * dx + dy * dy < (SIZE * 0.36) ** 2:
        return (0x2A, 0x5D, 0x7A, 0xFF)
    return (0xF4, 0xEF, 0xE4, 0xFF)


def chunk(kind, data):
    body = kind + data
    return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)


rows = bytearray()
for y in range(SIZE):
    rows.append(0)
    for x in range(SIZE):
        rows.extend(pixel(x, y))
png = b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", SIZE, SIZE, 8, 6, 0, 0, 0))
png += chunk(b"IDAT", zlib.compress(bytes(rows), 9)) + chunk(b"IEND", b"")
with open(sys.argv[1], "wb") as stream:
    stream.write(png)
