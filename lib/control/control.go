package control

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"sync"
	"time"
)

const terminateTimeout = 30 * time.Second

func CreateManager(ctx context.Context, t time.Time) *Manager {
	m := &Manager{
		q:                make(chan job, 100),
		lastAppChangedAt: t,
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case j := <-m.q:
				// same-app shortcut
				m.muLastApp.RLock()
				if m.lastApp == j.app {
					m.muLastApp.RUnlock()
					j.resultChan <- Result{}
					continue
				}
				m.muLastApp.RUnlock()

				// stale job?
				if m.lastAppChangedAt.After(j.t) {
					j.resultChan <- Result{
						Err:     errors.New("app changed"),
						ErrCode: http.StatusConflict,
					}
					continue
				}

				fmt.Println("app changed, starting new process for", j.app)
				if m.lastCmd != nil && m.lastCmd.Process != nil {
					fmt.Println("terminating previous process for", m.lastApp)
					terminateProcess(m.lastCmd, terminateTimeout)
				}
				// start the new process
				cmd := commandWithBase(ctx, j.entrypoint)
				if err := cmd.Start(); err != nil {
					fmt.Println("failed to start: " + err.Error())
					j.resultChan <- Result{Err: err, ErrCode: http.StatusInternalServerError}
					continue
				}

				initiated := false
				for i := 0; i < 10; i++ {
					// wait for HTTP readiness
					if err := waitForHTTP(fmt.Sprintf("http://%s", j.host), 5*time.Second, 1*time.Second); err != nil {
						fmt.Printf("waitForHTTP failed: %v\n", err)
						continue
					}

					initiated = true
					break
				}
				if !initiated {
					terminateProcess(cmd, terminateTimeout)
					j.resultChan <- Result{Err: errors.New("failed to start app"), ErrCode: http.StatusInternalServerError}
					continue
				}
				// update state
				m.muLastApp.Lock()
				m.lastApp = j.app
				m.muLastApp.Unlock()
				m.lastAppChangedAt = time.Now()
				m.lastCmd = cmd

				j.resultChan <- Result{}
			}
		}
	}()
	return m
}

type Manager struct {
	q                chan job
	lastApp          string
	muLastApp        sync.RWMutex
	lastAppChangedAt time.Time
	lastCmd          *exec.Cmd
}

func (m *Manager) Queue(ctx context.Context, app string, entrypoint string, host string, chResult chan Result, t time.Time) {
	m.q <- job{ctx, app, entrypoint, host, chResult, t}
}

func (m *Manager) LastApp() string {
	m.muLastApp.RLock()
	defer m.muLastApp.RUnlock()
	return m.lastApp
}

func (m *Manager) Stop() {
	if m.lastCmd != nil {
		if m.lastCmd.Process != nil {
			terminateProcess(m.lastCmd, terminateTimeout)
		}
	}
}

type job struct {
	ctx        context.Context
	app        string
	entrypoint string
	host       string
	resultChan chan Result
	t          time.Time
}

type Result struct {
	Err     error
	ErrCode int
}

func waitForHTTP(url string, timeout time.Duration, interval time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: interval}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
		}
		time.Sleep(interval)
	}
	return fmt.Errorf("timeout waiting for HTTP %s", url)
}
