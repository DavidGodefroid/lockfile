#include "textflag.h"

// func int80getpid() uintptr: getpid (20) through the i386 system call ABI.
TEXT ·int80getpid(SB), NOSPLIT, $0-8
	MOVL $20, AX
	INT $0x80
	MOVQ AX, ret+0(FP)
	RET
