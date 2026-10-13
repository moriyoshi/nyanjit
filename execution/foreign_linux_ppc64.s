#include "textflag.h"
#include "funcdata.h"

TEXT ·foreignNativePointer(SB),NOSPLIT|NOFRAME,$0-8
 MOVD $·foreignNative(SB), R3
 MOVD R3, ret+0(FP)
 RET

// C ABI entry, called only by runtime.asmcgocall with the argument block in R3.
// Callback adapters find this frame through C ABI back chains, then recover
// the saved g0 independently of C code that may have repurposed R30.
TEXT ·foreignNative(SB),NOSPLIT|NOFRAME,$0
 MOVD R1, R11
 MOVDU R1, -144(R1)
 MOVD LR, R12
 MOVD R12, 16(R11)
 MOVD R2, 24(R1)
 MOVD R3, 112(R1)
 MOVD g, 128(R1)
 MOVD $0x4e59, R0
 MOVD R0, 136(R1)
 MOVD R3, R11
 MOVD 0(R11), R12
 MOVD 72(R11), R0
 CMP R0, $1
 BNE codePointer
 MOVD 8(R12), R2
 MOVD 16(R12), R0
 MOVD R0, 120(R1)
 MOVD 0(R12), R12
codePointer:
 MOVD R12, CTR
 MOVD 8(R11), R3
 MOVD 16(R11), R4
 MOVD 24(R11), R5
 MOVD 32(R11), R6
 MOVD 40(R11), R7
 MOVD 48(R11), R8
 MOVD 56(R11), R9
 MOVD 64(R11), R10
 MOVD 72(R11), R0
 CMP R0, $1
 BNE callCode
 MOVD 120(R1), R11
callCode:
 BL (CTR)
 MOVD 112(R1), R11
 MOVD R3, 80(R11)
 XOR R0, R0
 MOVD R0, 128(R1)
 MOVD R0, 136(R1)
 MOVD 24(R1), R2
 MOVD 160(R1), R12
 MOVD R12, LR
 ADD $144, R1
 XOR R0, R0
 RET
