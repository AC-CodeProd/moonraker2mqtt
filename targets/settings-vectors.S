// Temporary cache-off vector table. No radio/Go handlers or flash literals.
// Normal window spills delegate to TinyGo's IRAM window vectors. Any other
// exception/interrupt resets directly through MMIO, without a stack or ROM call.
.section .iram1.settings_vectors,"ax"
.balign 0x400
.global settings_vectors
settings_vectors:
.org settings_vectors + 0x0
 j _vector_table + 0x0
.org settings_vectors + 0x40
 j _vector_table + 0x40
.org settings_vectors + 0x80
 j _vector_table + 0x80
.org settings_vectors + 0xc0
 j _vector_table + 0xc0
.org settings_vectors + 0x100
 j _vector_table + 0x100
.org settings_vectors + 0x140
 j _vector_table + 0x140
.org settings_vectors + 0x180
 j settings_fault
.org settings_vectors + 0x1c0
 j settings_fault
.org settings_vectors + 0x200
 j settings_fault
.org settings_vectors + 0x240
 j settings_fault
.org settings_vectors + 0x280
 j settings_fault
.org settings_vectors + 0x2c0
 j settings_fault
.org settings_vectors + 0x300
 j settings_fault
.org settings_vectors + 0x340
 wsr a0, EXCSAVE1
 rsr a0, EXCCAUSE
 bnei a0, 5, 1f
 j _xt_alloca_exc
1: j settings_fault
.org settings_vectors + 0x3c0
 j settings_fault
settings_fault:
 // Invalidate pending marker, avoiding repeated boot-save faults.
 movi a0, 5
 slli a0, a0, 28
 addmi a0, a0, 0x1000
 movi a1, 0
 s32i a1, a0, 0
 memw
 // RTC_CNTL_OPTIONS0.SW_SYS_RST: same MMIO reset as TinyGo vectors.
 movi a0, 6
 slli a0, a0, 28
 addmi a0, a0, 0x4000
 addmi a0, a0, 0x4000
 l32i a1, a0, 0
 movi a2, 1
 slli a2, a2, 31
 or a1, a1, a2
 s32i a1, a0, 0
 memw
2: j 2b
