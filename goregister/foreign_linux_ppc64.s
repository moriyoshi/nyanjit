#include "textflag.h"

// g.m is at byte 48 in both supported PPC64 runtime release families.
TEXT ·foreignCurrentThread(SB),NOSPLIT|NOFRAME,$0-16
 MOVD 48(g), R3
 MOVD R3, mp+0(FP)
 MOVD g, R3
 MOVD R3, gp+8(FP)
 RET

TEXT ·foreignHasCgo(SB),NOSPLIT|NOFRAME,$0-1
 MOVBZ runtime·iscgo(SB), R3
 MOVB R3, ret+0(FP)
 RET
