#!/usr/bin/env python3
"""Validate and optionally fix TinyGo ESP32-S3 image header. Never flashes."""
import argparse, hashlib, json, struct
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument("image");p.add_argument("--output");args=p.parse_args()
b=Path(args.image).read_bytes()
assert b[0]==0xe9 and b[23]==1 and struct.unpack_from("<H",b,12)[0]==9, "not a hashed S3 image"
assert hashlib.sha256(b[:-32]).digest()==b[-32:], "input SHA mismatch"
assert len(b)<0x1fe000, "image overlaps reserved settings"
off=24;segments=[]
for i in range(b[1]):
 addr,n=struct.unpack_from("<II",b,off);off+=8;assert off+n<=len(b)-32
 assert not (0x50000000<=addr<0x50002000), "RTC pending was emitted as a loadable image segment"
 segments.append({"address":hex(addr),"bytes":n});off+=n
if args.output:
 fixed=bytearray(b[:-32]);fixed[3]=(fixed[3]&15)|0x20
 b=bytes(fixed)+hashlib.sha256(fixed).digest();Path(args.output).write_bytes(b)
assert b[3]>>4==2, "expected explicit 4MiB flash header; use --output to patch"
print(json.dumps({"image":args.output or args.image,"bytes":len(b),"sha256":hashlib.sha256(b).hexdigest(),"flash":"4MiB","settings":["0x1fe000","0x200000"],"RTC_NOT_LOADED":True,"segments":segments,"runtime_qualified":False},indent=2))
