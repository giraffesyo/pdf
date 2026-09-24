"""Generates the CMYK JPEG fixtures with Pillow: python3 gen.py"""
from PIL import Image

im = Image.new("CMYK", (16, 8))
for y in range(8):
    for x in range(16):
        im.putpixel((x, y), (200, 0, 50, 10) if x < 8 else (0, 0, 0, 255))
im.save("adobe-cmyk.jpg", quality=100, subsampling=0)

# The same file without its Adobe APP14 segment.
data = open("adobe-cmyk.jpg", "rb").read()
out, i = bytearray(data[:2]), 2
while True:
    marker, length = data[i + 1], int.from_bytes(data[i + 2 : i + 4], "big")
    if marker == 0xDA:
        out += data[i:]
        break
    if marker != 0xEE:
        out += data[i : i + 2 + length]
    i += 2 + length
open("plain-cmyk.jpg", "wb").write(out)
