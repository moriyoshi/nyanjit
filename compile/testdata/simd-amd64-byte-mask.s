0000  movdqu xmm3, [RDI]
0004  movzx ESI, SIL
0008  movd xmm4, ESI
000c  punpcklbw xmm4, xmm4
0010  pshuflw xmm4, xmm4, 0
0015  pshufd xmm4, xmm4, 0
001a  pcmpeqb xmm3, xmm4
001e  pmovmskb EAX, xmm3
0022  ret
