#include "textflag.h"
#include "funcdata.h"

// The fixed Go assembly frame gives the runtime an unwindable boundary.
// X9 carries the stackless JIT workspace and is preserved for our Go caller.
TEXT ·call(SB),NOSPLIT,$32-32
	NO_LOCAL_POINTERS
	MOV RA, 0(SP)
	MOV X9, 8(SP)
	MOV address+0(FP), X31
	MOV args+8(FP), X30
	MOV workspace+16(FP), X9
	MOV 0(X30), X10
	MOV 8(X30), X11
	MOV 16(X30), X12
	MOV 24(X30), X13
	MOV 32(X30), X14
	MOV 40(X30), X15
	MOV 48(X30), X16
	MOV 56(X30), X17
	JALR RA, (X31)
	MOV 8(SP), X9
	MOV 0(SP), RA
	MOV X10, ret+24(FP)
	RET

TEXT ·callGo(SB),NOSPLIT,$32-32
	NO_LOCAL_POINTERS
	MOV RA, 0(SP)
	MOV X9, 8(SP)
	MOV address+0(FP), X31
	MOV args+8(FP), X30
	MOV workspace+16(FP), X9
	MOV 0(X30), X10
	MOV 8(X30), X11
	MOV 16(X30), X12
	MOV 24(X30), X13
	MOV 32(X30), X14
	MOV 40(X30), X15
	MOV 48(X30), X16
	MOV 56(X30), X17
	JALR RA, (X31)
	MOV 8(SP), X9
	MOV 0(SP), RA
	MOV X10, ret+24(FP)
	RET

TEXT ·callPair(SB),NOSPLIT,$32-40
	NO_LOCAL_POINTERS
	MOV RA, 0(SP)
	MOV X9, 8(SP)
	MOV address+0(FP), X31
	MOV args+8(FP), X30
	MOV workspace+16(FP), X9
	MOV 0(X30), X10
	MOV 8(X30), X11
	MOV 16(X30), X12
	MOV 24(X30), X13
	MOV 32(X30), X14
	MOV 40(X30), X15
	MOV 48(X30), X16
	MOV 56(X30), X17
	JALR RA, (X31)
	MOV 8(SP), X9
	MOV 0(SP), RA
	MOV X10, ret+24(FP)
	MOV X11, ret1+32(FP)
	RET
