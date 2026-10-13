#include "textflag.h"
#include "funcdata.h"

// The assembler saves the caller's R29 just below SP, where an AAPCS64 callee
// may build its own frame, so unmanaged generated code runs 16 bytes lower. The
// gap holds no pointers. Its first word repeats the saved LR, because Go's
// unwinder reads a frame's return address at SP when a callee is unwound.

TEXT ·call(SB),NOSPLIT,$0-32
	NO_LOCAL_POINTERS
	MOVD address+0(FP), R1
	MOVD args+8(FP), R10
	MOVD workspace+16(FP), R8
	MOVD 0(R10), R0
	MOVD 8(R10), R1
	MOVD 16(R10), R2
	MOVD 24(R10), R3
	MOVD 32(R10), R4
	MOVD 40(R10), R5
	MOVD 48(R10), R6
	MOVD 56(R10), R7
	MOVD address+0(FP), R9
	SUB $16, RSP
	MOVD R30, 0(RSP)
	CALL (R9)
	ADD $16, RSP
	MOVD R0, ret+24(FP)
	RET

// C ABI integer pair result: low in R0 and high in R1.
TEXT ·callPair(SB),NOSPLIT,$0-40
	NO_LOCAL_POINTERS
	MOVD args+8(FP), R10
	MOVD workspace+16(FP), R8
	MOVD 0(R10), R0
	MOVD 8(R10), R1
	MOVD 16(R10), R2
	MOVD 24(R10), R3
	MOVD 32(R10), R4
	MOVD 40(R10), R5
	MOVD 48(R10), R6
	MOVD 56(R10), R7
	MOVD address+0(FP), R9
	SUB $16, RSP
	MOVD R30, 0(RSP)
	CALL (R9)
	ADD $16, RSP
	MOVD R0, ret+24(FP)
	MOVD R1, ret1+32(FP)
	RET

// Go-managed code follows Go's frame convention: its reserved top word is the
// caller's saved R29, so it is entered at this frame's SP with no gap. The
// assembler's frame record then sits in that word, where a stack copy finds
// and adjusts it.
TEXT ·callManaged(SB),NOSPLIT,$0-32
	NO_LOCAL_POINTERS
	MOVD args+8(FP), R10
	MOVD workspace+16(FP), R8
	MOVD 0(R10), R0
	MOVD 8(R10), R1
	MOVD 16(R10), R2
	MOVD 24(R10), R3
	MOVD 32(R10), R4
	MOVD 40(R10), R5
	MOVD 48(R10), R6
	MOVD 56(R10), R7
	MOVD address+0(FP), R9
	CALL (R9)
	MOVD R0, ret+24(FP)
	RET

TEXT ·callPairManaged(SB),NOSPLIT,$0-40
	NO_LOCAL_POINTERS
	MOVD args+8(FP), R10
	MOVD workspace+16(FP), R8
	MOVD 0(R10), R0
	MOVD 8(R10), R1
	MOVD 16(R10), R2
	MOVD 24(R10), R3
	MOVD 32(R10), R4
	MOVD 40(R10), R5
	MOVD 48(R10), R6
	MOVD 56(R10), R7
	MOVD address+0(FP), R9
	CALL (R9)
	MOVD R0, ret+24(FP)
	MOVD R1, ret1+32(FP)
	RET

// Flush data and instruction cache lines for [start, end). Advancing by four
// bytes is safe across the cache-line sizes supported by arm64; the cache
// maintenance instructions round each address down to the containing line.
TEXT ·flushICache(SB),NOSPLIT,$0-16
	MOVD start+0(FP), R0
	MOVD end+8(FP), R1
flushData:
	CMP R0, R1
	BHS flushDataDone
	WORD $0xd50b7b20 // DC CVAU, R0
	ADD $4, R0
	B flushData
flushDataDone:
	WORD $0xd5033b9f // DSB ISH
	MOVD start+0(FP), R0
flushInstruction:
	CMP R0, R1
	BHS flushInstructionDone
	WORD $0xd50b7520 // IC IVAU, R0
	ADD $4, R0
	B flushInstruction
flushInstructionDone:
	WORD $0xd5033b9f // DSB ISH
	WORD $0xd5033fdf // ISB
	RET
