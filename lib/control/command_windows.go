package control

import (
	"context"
	"io"
	"log"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// commandWithBase: 새 프로세스 그룹을 생성하도록 설정
func commandWithBase(ctx context.Context, cmdStr string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd", "/c", cmdStr)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}

	return cmd
}

func terminateProcess(cmd *exec.Cmd, timeout time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	if cmd.Stdout == nil {
		cmd.Stdout = io.Discard
	}
	if cmd.Stderr == nil {
		cmd.Stderr = io.Discard
	}

	// 프로세스 그룹 ID는 pid와 동일 (CREATE_NEW_PROCESS_GROUP일 경우)
	pgid := uint32(cmd.Process.Pid)
	if err := windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, pgid); err != nil {
		log.Printf("terminateProcess: failed to send CTRL_BREAK_EVENT to pgid %d: %v", pgid, err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		log.Printf("terminateProcess: pgid %d exited after CTRL_BREAK_EVENT: %v", pgid, err)
	case <-time.After(timeout):
		log.Printf("terminateProcess: pgid %d did not exit after %s; sending Kill", pgid, timeout)
		if err := cmd.Process.Kill(); err != nil {
			log.Printf("terminateProcess: failed to kill pid %d: %v", cmd.Process.Pid, err)
		}
		if err := <-done; err != nil {
			log.Printf("terminateProcess: pgid %d killed with error: %v", pgid, err)
		} else {
			log.Printf("terminateProcess: pgid %d killed cleanly", pgid)
		}
	}
}
