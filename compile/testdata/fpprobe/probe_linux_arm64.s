#include "textflag.h"
TEXT ·Probe(SB),NOSPLIT,$0-40
 MOVD -8(RSP), R11
 MOVD R11, before+16(FP)
 MOVD argument+8(FP), R0
 MOVD address+0(FP), R9
 CALL (R9)
 MOVD R0, result+24(FP)
 MOVD -8(RSP), R11
 MOVD R11, after+32(FP)
 RET

// A frameless leaf leaves R29 as its caller's frame pointer.
TEXT ·FramePointer(SB),NOSPLIT,$0-8
	MOVD R29, ret+0(FP)
	RET

TEXT ·Load(SB),NOSPLIT,$0-16
	MOVD address+0(FP), R0
	MOVD (R0), R0
	MOVD R0, ret+8(FP)
	RET
