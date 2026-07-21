// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build windows,amd64

// Derived from golang.org/x/sys/windows/svc/sys_amd64.s. Go 1.10 cannot
// enter the runtime directly from the SCM-created ServiceMain thread.

// func serviceMainNative(argc uint32, argv **uint16)
TEXT ·serviceMainNative(SB),7,$0
	MOVL	CX, ·serviceArgumentCount(SB)
	MOVQ	DX, ·serviceArgumentVector(SB)

	SUBQ	$32, SP

	MOVQ	·activeServiceNamePointer(SB), CX
	MOVQ	$·serviceControlNative(SB), DX
	XORQ	R8, R8
	MOVQ	·serviceRegisterHandlerAddress(SB), AX
	CALL	AX
	MOVQ	AX, ·activeServiceStatusHandle(SB)

	MOVQ	·serviceWorkerReadyEventHandle(SB), CX
	MOVQ	·serviceSetEventAddress(SB), AX
	CALL	AX

	CMPQ	·activeServiceStatusHandle(SB), $0
	JE	exit
	MOVQ	·serviceMainDoneEventHandle(SB), CX
	MOVQ	$4294967295, DX
	MOVQ	·serviceWaitForSingleObjectAddress(SB), AX
	CALL	AX

exit:
	ADDQ	$32, SP
	RET

// Keep the control callback on the dispatcher thread managed by Go.
TEXT ·serviceControlNative(SB),7,$0
	MOVQ	·serviceControlCallbackPointer(SB), AX
	JMP	AX
