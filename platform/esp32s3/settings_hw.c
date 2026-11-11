//go:build tinygo && esp32s3

#include <stdint.h>
#define RAM __attribute__((section(".iram1.settings_flash"),noinline))
#define REG(a) (*(volatile uint32_t *)(a))
extern uint32_t Cache_Suspend_ICache(void), Cache_Suspend_DCache(void);
extern void Cache_Resume_ICache(uint32_t),Cache_Resume_DCache(uint32_t);
extern void Cache_Invalidate_ICache_All(void),Cache_Invalidate_DCache_All(void);
extern int esp_rom_spiflash_read(uint32_t,uint32_t *,int32_t);
extern int esp_rom_spiflash_write(uint32_t,const uint32_t *,int32_t);
extern int esp_rom_spiflash_erase_sector(uint32_t),esp_rom_spiflash_unlock(void);
extern uint32_t rtc_get_reset_reason(uint32_t);
extern void software_reset(void);
extern uint32_t settings_vectors[];
__attribute__((section(".rtc.pending"),used)) volatile uint32_t settings_pending[1024];
__attribute__((section(".dram.settings"))) static uint32_t transfer[64];
static uint32_t sealed;
void settings_seal(void) {sealed=1;}
uint32_t settings_reason(void) {return rtc_get_reset_reason(0);}
void settings_restart(void) {__asm__ volatile("memw");software_reset();}
uint32_t settings_retained_read(uint32_t i) {return i<1024?settings_pending[i]:0;}
void settings_retained_write(uint32_t i,uint32_t v) {if(i<1024)settings_pending[i]=v;__asm__ volatile("memw");}

// The ROM reports the actual JEDEC ID but defaults to 2MiB geometry. Reserve
// settings BELOW that boundary rather than changing unqualified chip geometry.
// No ROM function pointers are installed or changed. Exact rev0 boot defaults
// eliminate every indirect flash callback in the previously audited ROM graph.
static int guard(void) {
 if(sealed || REG(0x3fceffe8u)!=0x3fcef670u || REG(0x3fceffe4u)!=0x3fcef6a4u) return -10;
 volatile uint32_t *f=(volatile uint32_t *)0x3fcef670u;
 if(f[0]!=0x18181818u||f[1]!=16||f[2]!=32)return -11;
 for(unsigned i=3;i<13;i++)if(f[i])return -12;
 volatile uint32_t *d=(volatile uint32_t *)0x3fcef6a4u;
 if(d[0]!=0x464016u || (d[1]!=0x200000u&&d[1]!=0x400000u) || d[2]!=65536||d[3]!=4096||d[4]!=256)return -13;
 // Bound this candidate to the audited S3 rev0.2 ROM (eFuse read-only fields).
 if(((REG(0x60007058u)>>24)&3u)!=0 || ((REG(0x60007058u)>>23)&1u)!=0 || ((REG(0x60007050u)>>18)&7u)!=2)return -17;
 if(!(REG(0x600c0000u)&4u))return -14; // core1 held reset
 // Refuse ALL nonzero crypt counters (even disabled configurations) and secure boot.
 if((REG(0x60007034u)&(7u<<18)) || (REG(0x60007038u)&(1u<<20)))return -15;
 // No initialized/mapped PSRAM. Never invalidate a dirty external-RAM cache.
 for(unsigned i=0;i<512;i++){uint32_t e=REG(0x600c5000u+i*4);if(!(e&(1u<<14))&&(e&(1u<<15)))return -16;}
 return 0;
}
// No C/Go heap pointers remain in this cache-off closure. Compiler emits ROM
// calls and IRAM literal pools only. Temporary VECBASE (SR 231) closes the strong IROM
// espradio_user_exception path without modifying the radio's normal handler.
RAM static int critical(uint32_t op,uint32_t addr,uint32_t n) {
 uint32_t ps,vb;
 __asm__ volatile("rsil %0, 5\n\trsr %1, 231" : "=a"(ps),"=a"(vb)::"memory");
 __asm__ volatile("wsr %0, 231\n\trsync"::"a"(settings_vectors):"memory");
 uint32_t is=Cache_Suspend_ICache();
 while((REG(0x600c4130u)&0xfffu)!=1u){} // IDF S3 CACHE_SUSPEND_WAITI workaround
 uint32_t ds=Cache_Suspend_DCache();
 while(((REG(0x600c4130u)>>12)&0xfffu)!=1u){}
 int rc;
 if(op==0)rc=esp_rom_spiflash_read(addr,transfer,n);
 else {
  rc=esp_rom_spiflash_unlock();
  if(!rc)rc=op==1?esp_rom_spiflash_write(addr,transfer,n):esp_rom_spiflash_erase_sector(addr/4096);
 }
 Cache_Invalidate_DCache_All();Cache_Invalidate_ICache_All();
 Cache_Resume_DCache(ds);Cache_Resume_ICache(is);
 __asm__ volatile("wsr %0, 231\n\trsync\n\twsr %1, ps\n\trsync"::"a"(vb),"a"(ps):"memory");
 return rc;
}
int settings_io(uint32_t op,uint32_t slot,uint32_t offset,uint32_t *buf,uint32_t n) {
 if(op>2||slot>1||offset>4096||n>256||n>4096-offset||(offset&3)||(n&3))return -1;
 if(op==2&&(offset||n))return -2;
 int rc=guard();if(rc)return rc;
 if(op==1)for(unsigned i=0;i<n/4;i++)transfer[i]=buf[i];
 rc=critical(op,0x1fe000u+slot*4096u+offset,n);
 if(!rc&&op==0)for(unsigned i=0;i<n/4;i++)buf[i]=transfer[i];
 return rc;
}
