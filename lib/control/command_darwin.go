package control

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func commandWithBase(ctx context.Context, cmdStr string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	return cmd
}

func terminateProcess(cmd *exec.Cmd, timeout time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}

	// Stdout/Stderr fallback
	if cmd.Stdout == nil {
		cmd.Stdout = io.Discard
	}
	if cmd.Stderr == nil {
		cmd.Stderr = io.Discard
	}

	// PGID 획득
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		log.Printf("terminateProcess: failed to get pgid of pid %d: %v", cmd.Process.Pid, err)
		return
	}

	// SIGINT 전송 (음수 PGID 사용)
	fmt.Println("terminateProcess: sending SIGINT to pgid", pgid)
	if err := syscall.Kill(-pgid, syscall.SIGINT); err != nil {
		log.Printf("terminateProcess: failed to send SIGINT to pgid %d: %v", pgid, err)
	}

	// Wait 비동기로 호출
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	// timeout 대기
	select {
	case err := <-done:
		log.Printf("terminateProcess: pgid %d exited after SIGINT: %v", pgid, err)
	case <-time.After(timeout):
		log.Printf("terminateProcess: pgid %d did not exit after %s; sending SIGKILL", pgid, timeout)

		// SIGKILL 전송
		fmt.Println("terminateProcess: sending SIGKILL to pgid", pgid)
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
			log.Printf("terminateProcess: failed to send SIGKILL to pgid %d: %v", pgid, err)
		}

		// Wait 회수
		err := <-done
		if err != nil {
			log.Printf("terminateProcess: pgid %d killed with error: %v", pgid, err)
		} else {
			log.Printf("terminateProcess: pgid %d killed cleanly", pgid)
		}
	}
}
