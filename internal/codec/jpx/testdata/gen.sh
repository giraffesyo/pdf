#!/bin/sh
# Regenerates the fixtures with OpenJPEG (opj_compress, opj_decompress;
# made with 2.5.4): sh gen.sh
set -e
cd "$(dirname "$0")"
python3 - <<'PY'
import math, random
random.seed(7)
W, H = 41, 29
def px(x, y):
    r = int(127 + 100 * math.sin(x / 5.0) + 20 * random.random())
    g = int(255 * x / (W - 1)) if y < H // 2 else int(255 * y / (H - 1))
    b = 255 if (x // 6 + y // 6) % 2 else 30
    return [max(0, min(255, v)) for v in (r, g, b)]
pix = [[px(x, y) for x in range(W)] for y in range(H)]
with open("src.ppm", "wb") as f:
    f.write(b"P6\n%d %d\n255\n" % (W, H))
    for row in pix:
        for p in row:
            f.write(bytes(p))
with open("src.pgm", "wb") as f:
    f.write(b"P5\n%d %d\n255\n" % (W, H))
    for row in pix:
        for r, g, b in row:
            f.write(bytes([(r * 3 + g * 5 + b * 2) // 10]))
PY
# name|source|options; lossy cases (-I) also get a reference decode.
while IFS='|' read -r name src opts; do
	ext=jp2
	case $name in *-j2k) ext=j2k ;; esac
	case $opts in *-n*) ;; *) opts="$opts -n 4" ;; esac # 29 rows take four levels
	# shellcheck disable=SC2086
	opj_compress -i "$src" -o "$name.$ext" $opts >/dev/null
	case $opts in *-I*) opj_decompress -i "$name.$ext" -o "$name.ref.${src##*.}" >/dev/null ;; esac
done <<'CASES'
gray|src.pgm|
rgb|src.ppm|
rgb-j2k|src.ppm|
rgb-no-mct|src.ppm|-mct 0
rgb-lossy|src.ppm|-I -r 20
gray-lossy-layers|src.pgm|-I -r 40,20,10
tiles|src.ppm|-t 16,16 -n 3
tiles-lossy|src.ppm|-t 12,10 -I -r 15 -n 2
tiles-offset|src.ppm|-t 12,14 -p RPCL -c [8,8] -d 3,5 -T 1,2 -n 2
tileparts|src.ppm|-t 16,16 -TP R -p RPCL -n 3
rlcp|src.ppm|-c [16,16],[8,8] -p RLCP -n 3
rpcl|src.ppm|-c [16,16],[8,8] -p RPCL -n 3
pcrl|src.ppm|-c [16,16],[8,8] -p PCRL -n 3
cprl|src.ppm|-c [16,16],[8,8] -p CPRL -n 3
poc|src.ppm|-POC T1=0,0,1,3,3,CPRL/T1=0,0,2,4,3,LRCP -r 20,10 -I -n 3
sop-eph|src.ppm|-SOP -EPH -r 30,10 -I
cblk-8|src.pgm|-b 8,8
levels-1|src.pgm|-n 1
levels-4|src.pgm|-n 4
bypass|src.pgm|-M 1 -b 16,16
bypass-rgb|src.ppm|-M 1
reset|src.pgm|-M 2
termall|src.pgm|-M 4
causal|src.pgm|-M 8
predterm|src.pgm|-M 16
segsym|src.pgm|-M 32
all-styles|src.ppm|-M 63 -I -r 8
roi|src.pgm|-ROI c=0,U=3
CASES
