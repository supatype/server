package deno

import (
	"os"
	"os/exec"
	"runtime"
	"syscall"
)

// tieLifetime has the kernel kill the worker when this process dies.
//
// Pdeathsig fires when the OS thread that started the child exits, not when the
// process does. Go may end a thread under a goroutine that dies holding
// LockOSThread, and the thread that ran Start could be that one. So the
// goroutine is pinned to its thread from Start until the worker has exited,
// which keeps the thread alive for the whole of the worker's life: the caller
// runs release only after Wait returns.
func tieLifetime(cmd *exec.Cmd) (release func()) {
	runtime.LockOSThread()
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	return runtime.UnlockOSThread
}

// adoptWorker has nothing to do on Linux: tieLifetime already set it up.
func adoptWorker(*os.Process) error { return nil }
