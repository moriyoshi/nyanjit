#include "textflag.h"
#include "funcdata.h"

// The fixed Go assembly frame gives the runtime an unwindable boundary.
// R23 carries the stackless JIT workspace and is preserved for our Go caller.
TEXT ·call(SB),NOSPLIT,$32-32
	NO_LOCAL_POINTERS
	MOVV R1, 0(R3)
	MOVV R23, 8(R3)
	MOVV address+0(FP), R20
	MOVV args+8(FP), R19
	MOVV workspace+16(FP), R23
	MOVV 0(R19), R4
	MOVV 8(R19), R5
	MOVV 16(R19), R6
	MOVV 24(R19), R7
	MOVV 32(R19), R8
	MOVV 40(R19), R9
	MOVV 48(R19), R10
	MOVV 56(R19), R11
	JAL (R20)
	MOVV 8(R3), R23
	MOVV 0(R3), R1
	MOVV R4, ret+24(FP)
	RET

TEXT ·callGo(SB),NOSPLIT,$32-32
	NO_LOCAL_POINTERS
	MOVV R1, 0(R3)
	MOVV R23, 8(R3)
	MOVV address+0(FP), R20
	MOVV args+8(FP), R19
	MOVV workspace+16(FP), R23
	MOVV 0(R19), R4
	MOVV 8(R19), R5
	MOVV 16(R19), R6
	MOVV 24(R19), R7
	MOVV 32(R19), R8
	MOVV 40(R19), R9
	MOVV 48(R19), R10
	MOVV 56(R19), R11
	JAL (R20)
	MOVV 8(R3), R23
	MOVV 0(R3), R1
	MOVV R4, ret+24(FP)
	RET

TEXT ·callPair(SB),NOSPLIT,$32-40
	NO_LOCAL_POINTERS
	MOVV R1, 0(R3)
	MOVV R23, 8(R3)
	MOVV address+0(FP), R20
	MOVV args+8(FP), R19
	MOVV workspace+16(FP), R23
	MOVV 0(R19), R4
	MOVV 8(R19), R5
	MOVV 16(R19), R6
	MOVV 24(R19), R7
	MOVV 32(R19), R8
	MOVV 40(R19), R9
	MOVV 48(R19), R10
	MOVV 56(R19), R11
	JAL (R20)
	MOVV 8(R3), R23
	MOVV 0(R3), R1
	MOVV R4, ret+24(FP)
	MOVV R5, ret1+32(FP)
	RET

TEXT ·syncICache(SB),NOSPLIT|NOFRAME,$0-0
	WORD $0x38728000 // ibar 0
	RET
