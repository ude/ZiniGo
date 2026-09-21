import hashlib
import os
import struct
import subprocess
import sys
import tempfile

import pikepdf
from fontTools.ttLib import TTFont

FONTFORGE = "/opt/homebrew/bin/fontforge"

def type1_stream_to_pfb(data, l1, l2, l3):
    """Wrap a raw PDF FontFile stream as a .pfb with segment headers."""
    out = bytearray()
    segs = [(1, data[:l1]), (2, data[l1:l1+l2])]
    fixed = data[l1+l2:l1+l2+l3] if l3 else b""
    if not fixed:
        fixed = (b"0" * 64 + b"\n") * 8 + b"cleartomark\n"
    segs.append((1, fixed))
    for kind, seg in segs:
        out += struct.pack("<BBI", 0x80, kind, len(seg)) + seg
    out += struct.pack("<BB", 0x80, 3)
    return bytes(out)

def convert_t1_to_cff(pfb_bytes, cache, workdir):
    key = hashlib.md5(pfb_bytes).hexdigest()
    if key in cache:
        return cache[key]
    pfb = os.path.join(workdir, key + ".pfb")
    otf = os.path.join(workdir, key + ".otf")
    with open(pfb, "wb") as f:
        f.write(pfb_bytes)
    r = subprocess.run(
        [FONTFORGE, "-lang=ff", "-c", 'Open($1); Generate($2)', pfb, otf],
        capture_output=True, text=True, timeout=120)
    if not os.path.exists(otf):
        raise RuntimeError(f"fontforge failed: {r.stderr[-300:]}")
    tt = TTFont(otf)
    cff_table = tt.reader.tables["CFF "]
    with open(otf, "rb") as f:
        f.seek(cff_table.offset)
        cff = f.read(cff_table.length)
    cache[key] = cff
    return cff

def process(in_path, out_path):
    pdf = pikepdf.open(in_path)
    cache = {}
    converted = failed = 0
    seen_fd = set()

    def handle_descriptor(fd):
        nonlocal converted, failed
        try:
            objgen = (fd.objgen if hasattr(fd, "objgen") else None)
        except Exception:
            objgen = None
        if objgen and objgen[0] != 0:
            if objgen in seen_fd:
                return
            seen_fd.add(objgen)
        ff = fd.get("/FontFile")
        if ff is None:
            return
        try:
            data = ff.read_bytes()
            l1 = int(ff.get("/Length1", 0))
            l2 = int(ff.get("/Length2", 0))
            l3 = int(ff.get("/Length3", 0))
            pfb = type1_stream_to_pfb(data, l1, l2, l3)
            cff = convert_t1_to_cff(pfb, cache, workdir)
            stream = pdf.make_stream(cff)
            stream["/Subtype"] = pikepdf.Name("/Type1C")
            fd["/FontFile3"] = stream
            del fd["/FontFile"]
            converted += 1
        except Exception as e:
            failed += 1
            print("  FAILED:", str(fd.get("/FontName", "?")), "->", str(e)[:120])

    def walk(res, depth=0):
        if res is None or depth > 4:
            return
        fdict = res.get("/Font")
        if fdict is not None:
            for _, fobj in fdict.items():
                try:
                    fd = fobj.get("/FontDescriptor")
                    if fd is not None:
                        handle_descriptor(fd)
                except Exception:
                    pass
        xobjs = res.get("/XObject")
        if xobjs is not None:
            for _, xo in xobjs.items():
                try:
                    walk(xo.get("/Resources"), depth + 1)
                except Exception:
                    pass

    with tempfile.TemporaryDirectory() as workdir_:
        global workdir
        workdir = workdir_
        for page in pdf.pages:
            walk(page.get("/Resources"))
        print(f"converted {converted} font programs ({len(cache)} unique), {failed} failed")
        pdf.save(out_path)

process(sys.argv[1], sys.argv[2])
