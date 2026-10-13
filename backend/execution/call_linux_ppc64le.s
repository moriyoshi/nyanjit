//go:build linux && ppc64le

#include "textflag.h"
#include "funcdata.h"

// The Go assembler provides the fixed Go frame and saves/restores LR.
// Local saves are above the largest C linkage/parameter-home area. The
// conditional eight-byte adjustment aligns the call to 16 bytes using only
// constant SP changes, which the Go assembler records in its PC/SP table.
// Copy the saved Go return PC before opening the alignment word so it remains
// at 0(SP) throughout that path. R29 carries the workspace extension.
TEXT ·call(SB),NOSPLIT,$112-32
	NO_LOCAL_POINTERS
	MOVD R29, 112(R1)
	MOVD R2, 120(R1)
	MOVD address+0(FP), R12
	MOVD args+8(FP), R11
	MOVD workspace+16(FP), R29
	MOVD 0(R11), R3
	MOVD 8(R11), R4
	MOVD 16(R11), R5
	MOVD 24(R11), R6
	MOVD 32(R11), R7
	MOVD 40(R11), R8
	MOVD 48(R11), R9
	MOVD 56(R11), R10
	XOR R0, R0
	MOVD R12, CTR
	ANDCC $15, R1, R11
	BEQ callAligned
	MOVD 0(R1), R11
	MOVD R11, -8(R1)
	ADD $-8, R1
	BL (CTR)
	ADD $8, R1
	BR callDone
callAligned:
	BL (CTR)
callDone:
	XOR R0, R0
	MOVD 112(R1), R29
	MOVD 120(R1), R2
	MOVD R3, ret+24(FP)
	RET

TEXT ·callPair(SB),NOSPLIT,$112-40
	NO_LOCAL_POINTERS
	MOVD R29, 112(R1)
	MOVD R2, 120(R1)
	MOVD address+0(FP), R12
	MOVD args+8(FP), R11
	MOVD workspace+16(FP), R29
	MOVD 0(R11), R3
	MOVD 8(R11), R4
	MOVD 16(R11), R5
	MOVD 24(R11), R6
	MOVD 32(R11), R7
	MOVD 40(R11), R8
	MOVD 48(R11), R9
	MOVD 56(R11), R10
	XOR R0, R0
	MOVD R12, CTR
	ANDCC $15, R1, R11
	BEQ pairAligned
	MOVD 0(R1), R11
	MOVD R11, -8(R1)
	ADD $-8, R1
	BL (CTR)
	ADD $8, R1
	BR pairDone
pairAligned:
	BL (CTR)
pairDone:
	XOR R0, R0
	MOVD 112(R1), R29
	MOVD 120(R1), R2
	MOVD R3, ret+24(FP)
	MOVD R4, ret1+32(FP)
	RET

// Visiting each instruction word covers every cache block without assuming
// cache-line sizes. Publication is infrequent; this conservative path can be
// shortened later using explicitly discovered cache/coherence capabilities.
TEXT ·flushICache(SB),NOSPLIT|NOFRAME,$0-16
	MOVD start+0(FP), R3
	MOVD end+8(FP), R4
	MOVD R3, R5
dcLoop:
	CMPU R5, R4
	BGE dcDone
	DCBST (R5)
	ADD $4, R5
	BR dcLoop
dcDone:
	SYNC
	MOVD R3, R5
icLoop:
	CMPU R5, R4
	BGE icDone
	ICBI (R5)
	ADD $4, R5
	BR icLoop
icDone:
	SYNC
	ISYNC
	RET
