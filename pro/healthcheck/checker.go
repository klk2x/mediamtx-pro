// Package healthcheck implements health checking for network capture devices.
package healthcheck

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/pro/deviceutil"
)

const (
	// DeviceTypeNetworkCapture indicates a network capture card device
	DeviceTypeNetworkCapture = "network_capture"

	// Fixed health check parameters
	checkInterval     = 60 * time.Second // 固定60秒检查一次
	failureThreshold  = 6                // 固定失败6次后重启设备
	rebootTimeout     = 10 * time.Second // 重启请求超时时间
)

// Checker monitors path health and restarts capture devices when needed.
type Checker struct {
	PathConfs   map[string]*conf.Path
	PathManager pathManager
	Parent      logger.Writer

	ctx       context.Context
	ctxCancel func()
	wg        sync.WaitGroup
	mutex     sync.RWMutex
	monitors  map[string]*pathMonitor // key: pathName
}

type pathManager interface {
	APIPathsGet(name string) (*defs.APIPath, error)
}

type pathMonitor struct {
	pathName       string
	deviceIP       string
	streamName     string
	failureCount   int
	checker        *Checker
	ctx            context.Context
	ctxCancel      func()
	snapshotGetter snapshotGetter
}

type snapshotGetter interface {
	GetSnapshot(pathName string) ([]byte, string, error)
}

// Initialize initializes the Checker.
func (c *Checker) Initialize(snapshotGetter snapshotGetter) error {
	c.ctx, c.ctxCancel = context.WithCancel(context.Background())
	c.monitors = make(map[string]*pathMonitor)

	// Start monitors for all network capture device paths
	for pathName, pathConf := range c.PathConfs {
		if pathConf.DeviceType == DeviceTypeNetworkCapture {
			err := c.startMonitor(pathName, pathConf, snapshotGetter)
			if err != nil {
				c.Log(logger.Warn, "failed to start health check for path '%s': %v", pathName, err)
			}
		}
	}

	c.Log(logger.Info, "health checker initialized with %d monitors", len(c.monitors))
	return nil
}

// Close closes the Checker.
func (c *Checker) Close() {
	if c.ctxCancel != nil {
		c.ctxCancel()
	}

	c.wg.Wait()

	c.mutex.Lock()
	defer c.mutex.Unlock()

	for _, monitor := range c.monitors {
		monitor.stop()
	}

	c.monitors = nil
	c.Log(logger.Info, "health checker closed")
}

// Log implements logger.Writer.
func (c *Checker) Log(level logger.Level, format string, args ...interface{}) {
	c.Parent.Log(level, "[healthcheck] "+format, args...)
}

// ReloadPathConfs reloads path configurations.
func (c *Checker) ReloadPathConfs(pathConfs map[string]*conf.Path, snapshotGetter snapshotGetter) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Stop monitors for removed or non-capture-device paths
	for pathName, monitor := range c.monitors {
		newConf, exists := pathConfs[pathName]
		if !exists || newConf.DeviceType != DeviceTypeNetworkCapture {
			c.Log(logger.Info, "stopping health check for path '%s'", pathName)
			monitor.stop()
			delete(c.monitors, pathName)
		}
	}

	// Start monitors for new network capture device paths
	for pathName, pathConf := range pathConfs {
		if pathConf.DeviceType == DeviceTypeNetworkCapture {
			if _, exists := c.monitors[pathName]; !exists {
				err := c.startMonitor(pathName, pathConf, snapshotGetter)
				if err != nil {
					c.Log(logger.Warn, "failed to start health check for path '%s': %v", pathName, err)
				}
			}
		}
	}

	c.PathConfs = pathConfs
	c.Log(logger.Info, "health check configurations reloaded, active monitors: %d", len(c.monitors))
}

// startMonitor starts a health check monitor for a path.
func (c *Checker) startMonitor(pathName string, pathConf *conf.Path, snapshotGetter snapshotGetter) error {
	// Parse source URL to get device IP
	u, err := base.ParseURL(pathConf.Source)
	if err != nil {
		return fmt.Errorf("failed to parse source URL: %w", err)
	}

	if u.Host == "" {
		return fmt.Errorf("source URL has no host")
	}

	streamName := filepath.Base(u.Path)
	if streamName == "" {
		return fmt.Errorf("source URL has no stream name")
	}

	monitorCtx, monitorCancel := context.WithCancel(c.ctx)

	monitor := &pathMonitor{
		pathName:       pathName,
		deviceIP:       u.Host,
		streamName:     streamName,
		checker:        c,
		ctx:            monitorCtx,
		ctxCancel:      monitorCancel,
		snapshotGetter: snapshotGetter,
	}

	c.monitors[pathName] = monitor

	c.wg.Add(1)
	go monitor.run()

	c.Log(logger.Info, "started health check for path '%s' (device: %s, interval: %v, threshold: %d)",
		pathName, u.Host, checkInterval, failureThreshold)

	return nil
}

// run is the main monitoring loop for a path.
func (m *pathMonitor) run() {
	defer m.checker.wg.Done()

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	m.checker.Log(logger.Info, "health check monitor started for path '%s'", m.pathName)

	for {
		select {
		case <-m.ctx.Done():
			m.checker.Log(logger.Info, "health check monitor stopped for path '%s'", m.pathName)
			return

		case <-ticker.C:
			m.performCheck()
		}
	}
}

// performCheck performs a single health check.
func (m *pathMonitor) performCheck() {
	// First, check device status via GetInputStatusIsAvalible
	availableCount, err := deviceutil.GetInputStatusIsAvalible(m.deviceIP)
	if err != nil || availableCount == 0 {
		m.checker.Log(logger.Debug, "device %s not available, skipping snapshot check", m.deviceIP)
		return
	}

	// Then, check snapshot
	_, _, err = m.snapshotGetter.GetSnapshot(m.pathName)
	if err != nil {
		m.failureCount++
		m.checker.Log(logger.Warn, "health check failed for path '%s' (%d/%d): %v",
			m.pathName, m.failureCount, failureThreshold, err)

		// If failure threshold reached, reboot device
		if m.failureCount >= failureThreshold {
			m.failureCount = 0 // Reset counter
			m.checker.Log(logger.Error, "health check failure threshold reached for path '%s', rebooting device %s",
				m.pathName, m.deviceIP)

			err := m.rebootDevice()
			if err != nil {
				m.checker.Log(logger.Error, "failed to reboot device %s: %v", m.deviceIP, err)
			} else {
				m.checker.Log(logger.Info, "device %s reboot request sent successfully", m.deviceIP)
			}
		}
	} else {
		// Success, reset failure counter
		if m.failureCount > 0 {
			m.checker.Log(logger.Info, "health check recovered for path '%s', resetting failure count", m.pathName)
			m.failureCount = 0
		}
	}
}

// rebootDevice sends reboot command to the capture device.
func (m *pathMonitor) rebootDevice() error {
	baseURL := "http://" + m.deviceIP

	// Step 1: Login
	loginURL := baseURL + "/login2.php"
	formData := url.Values{}
	formData.Add("name", "admin")
	formData.Add("passwd", "admin")

	req1, err := http.NewRequest("POST", loginURL, bytes.NewBufferString(formData.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create login request: %w", err)
	}

	req1.Header.Set("Accept", "application/json")
	req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: rebootTimeout}
	resp1, err := client.Do(req1)
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}
	defer resp1.Body.Close()

	if resp1.StatusCode != http.StatusOK {
		return fmt.Errorf("login request returned status %d", resp1.StatusCode)
	}

	// Get cookies from login response
	var cookieStr string
	cookies := resp1.Cookies()
	for i, cookie := range cookies {
		if i > 0 {
			cookieStr += "; "
		}
		cookieStr += fmt.Sprintf("%s=%s", cookie.Name, cookie.Value)
	}

	// Step 2: Reboot
	rebootURL := baseURL + "/func.php?func=reboot"
	req2, err := http.NewRequest("POST", rebootURL, bytes.NewBuffer([]byte{}))
	if err != nil {
		return fmt.Errorf("failed to create reboot request: %w", err)
	}

	req2.Header.Set("Accept", "application/json")
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookieStr != "" {
		req2.Header.Set("Cookie", cookieStr)
	}

	resp2, err := client.Do(req2)
	if err != nil {
		return fmt.Errorf("reboot request failed: %w", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		return fmt.Errorf("reboot request returned status %d", resp2.StatusCode)
	}

	return nil
}

// stop stops the path monitor.
func (m *pathMonitor) stop() {
	if m.ctxCancel != nil {
		m.ctxCancel()
	}
}
