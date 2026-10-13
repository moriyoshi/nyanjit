0000  movdqu xmm3, [RSI]
0004  movdqu xmm4, [RDX]
0008  paddd xmm3, xmm4
000c  movdqu [RDI], xmm3
0010  mov EAX, 0x0
0015  ret
