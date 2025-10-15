package mizvai

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/logger"
)

type VideoSnapshotServerConfig struct {
	VideoSnapshotModulePath   string `json:"videoSnapshotModulePath"`
	VideoSnapshotPipelineConf string `json:"videoSnapshotPipelineConf"`
	Source                    string `json:"source"`
}

type VideoSnapshotServer interface {
	Start() error
	Stop()
	Restart() error
	IsRunning() bool
}

type VideoSnapshotServerLauncher struct {
	conf       *VideoSnapshotServerConfig
	cmd        *exec.Cmd
	cancel     context.CancelFunc
	mu         sync.Mutex
	stopSignal chan struct{}
	parent     logger.Writer
}

func NewVideoSnapshotServerLauncher(conf *VideoSnapshotServerConfig, parent logger.Writer) VideoSnapshotServer {
	if parent != nil {
		parent.Log(logger.Info, "[mizvai] Creating VideoSnapshotServerLauncher for source: %s", conf.Source)
	}
	return &VideoSnapshotServerLauncher{
		conf:   conf,
		parent: parent,
	}
}

// Log logs a message using the parent logger if available.
func (p *VideoSnapshotServerLauncher) Log(level logger.Level, format string, args ...interface{}) {
	if p.parent != nil {
		p.parent.Log(level, "[mizvai] "+format, args...)
	}
}

func (p *VideoSnapshotServerLauncher) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.stopSignal != nil {
		p.Log(logger.Warn, "[%s] Already running, ignoring Start()", p.conf.Source)
		return nil
	}

	if p.cmd != nil && p.cmd.ProcessState != nil && !p.cmd.ProcessState.Exited() {
		p.Log(logger.Warn, "[%s] Previous process not fully exited", p.conf.Source)
		return errors.New("process not fully exited")
	}

	p.terminateProcess()

	if err := p.launch(); err != nil {
		p.Log(logger.Warn, "[%s] Error launching process: %v", p.conf.Source, err)
		return err
	}

	p.stopSignal = make(chan struct{})
	go p.monitorProcess()

	return nil
}

func (p *VideoSnapshotServerLauncher) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.Log(logger.Warn, "[%s] Stopping video snapshot server...", p.conf.Source)

	if p.stopSignal != nil {
		close(p.stopSignal)
		p.stopSignal = nil
	}

	p.terminateProcess()
}

func (p *VideoSnapshotServerLauncher) Restart() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.Log(logger.Warn, "[%s] Restarting video snapshot server manually...", p.conf.Source)

	if p.stopSignal != nil {
		close(p.stopSignal)
		p.stopSignal = nil
	}

	p.terminateProcess()

	if err := p.launch(); err != nil {
		p.Log(logger.Warn, "[%s] Failed to restart process: %v", p.conf.Source, err)
		return err
	}

	p.stopSignal = make(chan struct{})
	go p.monitorProcess()

	return nil
}

func (p *VideoSnapshotServerLauncher) terminateProcess() {
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}

	if p.cmd != nil && p.cmd.Process != nil {
		p.Log(logger.Warn, "[%s] Terminating process with PID %d", p.conf.Source, p.cmd.Process.Pid)

		// Graceful kill
		err := p.cmd.Process.Signal(os.Interrupt)
		if err != nil {
			p.Log(logger.Warn, "[%s] os.Interrupt failed: %v, using Kill()", p.conf.Source, err)
			_ = p.cmd.Process.Kill()
		}

		done := make(chan struct{})
		go func() {
			_ = p.cmd.Wait()
			close(done)
		}()

		select {
		case <-done:
			p.Log(logger.Warn, "[%s] Process exited", p.conf.Source)
		case <-time.After(3 * time.Second):
			p.Log(logger.Warn, "[%s] Timeout, forcing kill", p.conf.Source)
			_ = p.cmd.Process.Kill()
		}
	}

	p.cmd = nil
}

func (p *VideoSnapshotServerLauncher) launch() error {
	pathDir := path.Clean(p.conf.VideoSnapshotModulePath)
	pathExe := path.Join(pathDir, "snapshot.launcher")

	if _, err := os.Stat(pathExe); os.IsNotExist(err) {
		return errors.New("executable not found: " + pathExe)
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	cmd := exec.CommandContext(ctx, pathExe,
		"-pipeline", path.Join(pathDir, p.conf.VideoSnapshotPipelineConf),
		"-license", path.Join(pathDir, "license.key"),
	)
	cmd.Dir = "/"
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		p.Log(logger.Warn, "[%s] Failed to start process: %v", p.conf.Source, err)
		return err
	}

	p.cmd = cmd

	p.Log(logger.Warn, "[%s] Video snapshot server launched with PID %d", p.conf.Source, cmd.Process.Pid)
	return nil
}

func (p *VideoSnapshotServerLauncher) monitorProcess() {
	p.mu.Lock()
	cmd := p.cmd
	stopSignal := p.stopSignal
	p.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		p.Log(logger.Warn, "[%s] No process to monitor", p.conf.Source)
		return
	}

	err := cmd.Wait()

	p.mu.Lock()
	defer p.mu.Unlock()

	if stopSignal == nil {
		p.Log(logger.Warn, "[%s] Monitor exiting (called by Stop or Restart)", p.conf.Source)
		return
	}

	select {
	case <-stopSignal:
		p.Log(logger.Warn, "[%s] Monitor exiting after Stop() or Restart()", p.conf.Source)
		return
	default:
	}

	if err != nil {
		p.Log(logger.Warn, "[%s] Video snapshot server exited with error: %v", p.conf.Source, err)
	} else {
		p.Log(logger.Warn, "[%s] Video snapshot server exited normally", p.conf.Source)
	}

	p.Log(logger.Warn, "[%s] Restarting video snapshot server...", p.conf.Source)

	p.terminateProcess()

	if err := p.launch(); err != nil {
		p.Log(logger.Warn, "[%s] Failed to restart video snapshot server: %v", p.conf.Source)
		return
	}

	p.stopSignal = make(chan struct{})
	go p.monitorProcess()
}

func (p *VideoSnapshotServerLauncher) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cmd == nil || p.cmd.Process == nil {
		return false
	}

	if p.cmd.ProcessState != nil && p.cmd.ProcessState.Exited() {
		return false
	}

	return true
}
