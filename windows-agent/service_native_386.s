// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build windows,386

// Derived from golang.org/x/sys/windows/svc/sys_386.s. Go 1.10 cannot
// enter the runtime directly from the SCM-created ServiceMain thread.

// func serviceMainNative(argc uint32, argv **uint16)
TEXT ·serviceMainNative(SB),7,$0
	MOVL	argc+0(FP), AX
	MOVL	AX, ·serviceArgumentCount(SB)
	MOVL	argv+4(FP), AX
	MOVL	AX, ·serviceArgumentVector(SB)

	PUSHL	BP
	PUSHL	BX
	PUSHL	SI
	PUSHL	DI
	SUBL	$12, SP

	MOVL	·activeServiceNamePointer(SB), AX
	MOVL	AX, (SP)
	MOVL	$·serviceControlNative(SB), AX
	MOVL	AX, 4(SP)
	MOVL	$0, 8(SP)
	MOVL	·serviceRegisterHandlerAddress(SB), AX
	MOVL	SP, BP
	CALL	AX
	MOVL	BP, SP
	MOVL	AX, ·activeServiceStatusHandle(SB)

	MOVL	·serviceWorkerReadyEventHandle(SB), AX
	MOVL	AX, (SP)
	MOVL	·serviceSetEventAddress(SB), AX
	MOVL	SP, BP
	CALL	AX
	MOVL	BP, SP

	CMPL	·activeServiceStatusHandle(SB), $0
	JE	exit
	MOVL	·serviceMainDoneEventHandle(SB), AX
	MOVL	AX, (SP)
	MOVL	$-1, AX
	MOVL	AX, 4(SP)
	MOVL	·serviceWaitForSingleObjectAddress(SB), AX
	MOVL	SP, BP
	CALL	AX
	MOVL	BP, SP

exit:
	ADDL	$12, SP
	POPL	DI
	POPL	SI
	POPL	BX
	POPL	BP
	MOVL	0(SP), CX
	ADDL	$12, SP
	JMP	CX

// Keep the control callback on the dispatcher thread managed by Go.
TEXT ·serviceControlNative(SB),7,$0
	MOVL	·serviceControlCallbackPointer(SB), CX
	JMP	CX
