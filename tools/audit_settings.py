#!/usr/bin/env python3
"""ELF placement/visible closure checks. NOT device or brownout qualification."""
import argparse, json, re, struct, subprocess, tempfile
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('elf');p.add_argument('--objdump',required=True);p.add_argument('--out',required=True);a=p.parse_args()
elf=Path(a.elf);b=elf.read_bytes();out=Path(a.out);out.mkdir(parents=True,exist_ok=True)
assert b[:5]==b'\x7fELF\x01' and b[5]==1
shoff=struct.unpack_from('<I',b,32)[0];entsize,nsects=struct.unpack_from('<HH',b,46)
sections=[struct.unpack_from('<10I',b,shoff+i*entsize) for i in range(nsects)]
def read(addr,n):
 for s in sections:
  if s[1]==1 and s[3]<=addr and addr+n<=s[3]+s[5]:return b[s[4]+addr-s[3]:s[4]+addr-s[3]+n]
 raise AssertionError('unmapped ELF bytes '+hex(addr))
text=subprocess.check_output(['readelf','-sW',str(elf)],text=True)
syms={}
for line in text.splitlines():
 cols=line.split()
 if len(cols)>=8 and cols[0].endswith(':') and re.fullmatch('[0-9a-fA-F]+',cols[1]):syms[cols[7]]=(int(cols[1],16),int(cols[2]))
addr,size=syms['critical'];assert 0x40378000<=addr<0x403e0000
assert 0x3fc88000<=syms['transfer'][0]<0x3fceb710
assert syms['settings_pending']==(0x50001000,4096)
rtc=[s for s in sections if s[3]==0x50001000];assert len(rtc)==1 and rtc[0][1]==8 and rtc[0][5]==4096 # SHT_NOBITS
assert syms['_settings_start'][0]==0x1fe000 and syms['_settings_end'][0]==0x200000
assert syms['_irom_end'][0]-0x42000000+48<=0x1fe000
assert syms['settings_vectors'][0]%1024==0
assert 0x40378000<=syms['_vector_table'][0]<0x403e0000
assert 0x40378000<=syms['_xt_alloca_exc'][0]<0x403e0000
# LLVM's .xt.prop metadata can incorrectly mark merged IRAM as literal data.
# Decode extracted code bytes without that metadata; verify literal contents
# against the original ELF, never against a synthetic instruction listing.
def disasm(name,addr,n):
 raw=out/(name+'.raw');raw.write_bytes(read(addr,n))
 d=subprocess.check_output([a.objdump,'-D','-b','binary','-m','xtensa','--adjust-vma='+hex(addr),str(raw)],text=True)
 (out/(name+'.txt')).write_text(d);return d
d=disasm('critical',addr,size)
assert 'rsil' in d and 'rsr.vecbase' in d and d.count('wsr.vecbase')==2 and 'wsr.ps' in d
lits=[int(x,16) for x in re.findall(r'l32r\s+a\d+, 0x([0-9a-f]+)',d)]
assert lits and all(0x40378000<=x<0x403e0000 for x in lits)
rom={syms[x][0]:x for x in ['Cache_Suspend_ICache','Cache_Suspend_DCache','Cache_Resume_ICache','Cache_Resume_DCache','Cache_Invalidate_ICache_All','Cache_Invalidate_DCache_All','esp_rom_spiflash_read','esp_rom_spiflash_write','esp_rom_spiflash_erase_sector','esp_rom_spiflash_unlock']}
values={x:struct.unpack('<I',read(x,4))[0] for x in lits}
assert set(rom)<=set(values.values())
allowed=set(rom)|{syms['transfer'][0],syms['settings_vectors'][0],0x600c4130,0xfff,0xfff000,0x1000}
assert set(values.values())<=allowed, 'unexpected cache-off literal dependency'
# All direct wrapper branches remain inside the wrapper. All calls are callx8
# with ROM literals loaded into a8; there are no compiler helper/Go calls.
assert 'call0' not in d and 'call4' not in d and 'call8\t' not in d
assert len(re.findall(r'callx8\s+a8',d))==9
v=syms['settings_vectors'][0]
# Decode each vector independently; zero padding between vectors must not shift
# the disassembler's instruction alignment or skip the next entry's first byte.
for offset in [0,0x40,0x80,0xc0,0x100,0x140]:
 vd=disasm('vector-'+hex(offset),v+offset,3)
 assert hex(syms['_vector_table'][0]+offset) in vd, 'window vector does not delegate to IRAM'
vd=disasm('user-vector',v+0x340,15)
assert hex(syms['_xt_alloca_exc'][0]) in vd
assert 'callx' not in vd and 'l32r' not in vd
assert 'rsr.exccause' in vd and 'wsr.excsave1' in vd
fault=disasm('fault-reset',syms['settings_fault'][0],51)
assert 'call' not in fault and 'l32r' not in fault and 's32i' in fault and 'memw' in fault
report={'elf':str(elf),'critical_iram':hex(addr),'critical_bytes':size,'internal_transfer':hex(syms['transfer'][0]),'rtc_noload':hex(syms['settings_pending'][0]),'vector_table_iram':hex(v),'visible_literal_loads':len(lits),'ROM_targets':{name:hex(addr) for addr,name in rom.items()},'settings':['0x1fe000','0x200000'],'ROM_default_callback_guard_required':True,'retention_static_layout_verified':True,'retention_on_device_verified':False,'runtime_qualified':False}
(out/'report.json').write_text(json.dumps(report,indent=2)+'\n');print(json.dumps(report,indent=2))
