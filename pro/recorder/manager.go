// Package recorder contains the Pro recorder implementation.
package recorder

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/pro/deviceutil"
	"github.com/google/uuid"
)

// Manager manages recording tasks.
type Manager struct {
	RecordPath   string
	APIDomain    string
	APIAddress   string
	PathConfs    map[string]*conf.Path // path configurations for auto recording
	PathDefaults *conf.Path            // default path configuration (for webhooks)
	PathManager  defs.APIPathManager
	Parent       logger.Writer
	ColorChecker colorChecker // For smart recording

	mutex     sync.RWMutex
	tasks     map[string]*Task // key: pathName
	baseURL   string           // cached base URL
	ctx       context.Context
	ctxCancel func()
	wg        sync.WaitGroup
}

// colorChecker checks if the video has colorful content.
type colorChecker interface {
	IsColorful(pathName string) (int, error)
}

// Initialize initializes the Manager.
func (m *Manager) Initialize() error {
	m.tasks = make(map[string]*Task)

	// Build base URL for file access
	m.baseURL = conf.BuildAPIBaseURL(m.APIDomain, m.APIAddress)

	// Create context for lifecycle management
	m.ctx, m.ctxCancel = context.WithCancel(context.Background())

	// Start automatic recording monitor if PathConfs is provided
	if m.PathConfs != nil {
		m.wg.Add(1)
		go m.monitorAutoRecording()
	}

	m.Log(logger.Info, "recording manager initialized, base URL: %s", m.baseURL)
	return nil
}

// InitializeSmartRecording initializes smart recording (called after API is ready).
func (m *Manager) InitializeSmartRecording(colorChecker colorChecker) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if colorChecker != nil {
		m.ColorChecker = colorChecker
		m.Log(logger.Info, "smart recording for network capture devices enabled")
	}

	return nil
}

// Close closes the Manager.
func (m *Manager) Close() {

	// Cancel context to stop monitoring goroutine
	if m.ctxCancel != nil {
		m.ctxCancel()
	}

	// Wait for monitoring goroutine to finish
	m.wg.Wait()

	m.mutex.Lock()
	defer m.mutex.Unlock()

	for _, task := range m.tasks {
		task.Stop()
	}
	m.tasks = nil
	m.Log(logger.Info, "recording manager closed")
}

// Log implements logger.Writer.
func (m *Manager) Log(level logger.Level, format string, args ...interface{}) {
	m.Parent.Log(level, "[recorder] "+format, args...)
}

// StartRecording starts a recording task.
func (m *Manager) StartRecording(params *StartParams) (*StartResponse, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Check if task already exists
	if existingTask, exists := m.tasks[params.Name]; exists {
		// Return existing task info
		return &StartResponse{
			Existed:     true,
			Success:     true,
			ID:          existingTask.ID,
			Name:        params.Name,
			FileName:    existingTask.FileName,
			FilePath:    existingTask.RelativePath,
			FullPath:    existingTask.FullPath,
			FileURL:     existingTask.FileURL,
			TaskEndTime: existingTask.EndTime,
		}, nil
	}

	// Get path info from PathManager
	pathData, err := m.PathManager.APIPathsGet(params.Name)
	if err != nil {
		return nil, fmt.Errorf("path '%s' not found", params.Name)
	}

	// Check if path is ready (has an active stream)
	if !pathData.Ready {
		return nil, fmt.Errorf("no one is publishing to path '%s'", params.Name)
	}

	// Get path-specific configuration for sourceName (if exists)
	var pathConf *conf.Path
	if m.PathConfs != nil {
		pathConf = m.PathConfs[params.Name]
	}

	// Create new task
	task := &Task{
		ID:           uuid.New().String(),
		PathName:     params.Name,
		Format:       params.VideoFormat,
		RecordPath:   m.RecordPath,
		PathManager:  m.PathManager,
		PathConf:     pathConf,       // Path-specific config for sourceName
		PathDefaults: m.PathDefaults, // PathDefaults for webhook URL
		Parent:       m,
	}

	// Set default timeout if not specified
	if params.TaskOutMinutes <= 0 {
		params.TaskOutMinutes = 30
	}
	task.Timeout = time.Duration(params.TaskOutMinutes * float64(time.Minute))

	// Set custom filename if provided
	if params.FileName != "" {
		task.CustomFileName = params.FileName
	}

	// Initialize and start task
	err = task.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start recording: %w", err)
	}

	m.tasks[params.Name] = task

	// Generate file URL using base URL
	fileURL := m.baseURL + "/res" + task.RelativePath

	return &StartResponse{
		Existed:     false,
		Success:     true,
		ID:          task.ID,
		Name:        params.Name,
		FileName:    task.FileName,
		FilePath:    task.RelativePath,
		FullPath:    task.FullPath,
		FileURL:     fileURL,
		TaskEndTime: task.EndTime,
	}, nil
}

// StopRecording stops a recording task.
func (m *Manager) StopRecording(pathName string) (*StopResponse, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	task, exists := m.tasks[pathName]
	if !exists {
		return nil, fmt.Errorf("task id that does not exist")
	}

	// Stop the task
	task.Stop()

	// Generate file URL using base URL
	fileURL := m.baseURL + "/res" + task.RelativePath

	response := &StopResponse{
		Success:  true,
		Name:     pathName,
		FileName: task.FileName,
		FilePath: task.RelativePath,
		FullPath: task.FullPath,
		FileURL:  fileURL,
	}

	// Remove from map
	delete(m.tasks, pathName)

	return response, nil
}

// OnTaskComplete is called when a task completes (timeout or error).
func (m *Manager) OnTaskComplete(pathName string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if _, exists := m.tasks[pathName]; exists {
		m.Log(logger.Info, "task for path '%s' completed", pathName)
		delete(m.tasks, pathName)
	}
}

// StartParams contains parameters for starting a recording.
type StartParams struct {
	Name           string  `json:"name" binding:"required"`
	VideoFormat    string  `json:"videoFormat" binding:"required"`
	TaskOutMinutes float64 `json:"taskOutMinutes"`
	FileName       string  `json:"fileName"`
}

// StartResponse is the response for start recording request.
type StartResponse struct {
	Existed     bool      `json:"existed"`
	Success     bool      `json:"success"`
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	FileName    string    `json:"fileName"`
	FilePath    string    `json:"filePath"`
	FullPath    string    `json:"fullPath"`
	FileURL     string    `json:"fileURL"`
	TaskEndTime time.Time `json:"taskEndTime"`
}

// StopParams contains parameters for stopping a recording.
type StopParams struct {
	Name string `json:"name" binding:"required"`
}

// StopResponse is the response for stop recording request.
type StopResponse struct {
	Success  bool   `json:"success"`
	Name     string `json:"name"`
	FileName string `json:"fileName"`
	FilePath string `json:"filePath"`
	FullPath string `json:"fullPath"`
	FileURL  string `json:"fileURL"`
}

// generateFileName generates a short, unique filename with timestamp.
// Format: YYYYMMDD-HHMM-<shortid>.<ext>
func generateFileName(format string) string {
	now := time.Now()
	dateStr := now.Format("20060102-1504") // YYYYMMDD-HHMM

	// Generate short random ID (8 chars)
	id := uuid.New().String()[:8]

	ext := format
	if format == "ts" {
		ext = "ts"
	} else {
		ext = "mp4"
	}

	return fmt.Sprintf("%s-%s.%s", dateStr, id, ext)
}

// generateFilePath generates the file path structure.
// Format: /YYYYMMDD/filename.ext
func generateFilePath(recordPath, fileName string) (fullPath, relativePath string) {
	now := time.Now()
	dateDir := now.Format("20060102") // YYYYMMDD

	relativePath = filepath.Join("/", dateDir, fileName)
	fullPath = filepath.Join(recordPath, dateDir, fileName)

	return fullPath, relativePath
}

// monitorAutoRecording monitors paths and automatically starts recording for paths with record=true.
func (m *Manager) monitorAutoRecording() {
	defer m.wg.Done()

	// Check interval: every 5 seconds
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	m.Log(logger.Info, "automatic recording monitor started")

	for {
		select {
		case <-m.ctx.Done():
			m.Log(logger.Info, "automatic recording monitor stopped")
			return

		case <-ticker.C:
			m.checkAndStartAutoRecording()
		}
	}
}

// checkAndStartAutoRecording checks all paths and starts recording if needed.
func (m *Manager) checkAndStartAutoRecording() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	// Start new recording tasks for ready paths
	for pathName, pathConf := range m.PathConfs {
		// Skip if record is not enabled for this path
		if !pathConf.Record {
			continue
		}

		// Skip if task already exists
		if _, exists := m.tasks[pathName]; exists {
			continue
		}

		// Check if path is ready
		pathData, err := m.PathManager.APIPathsGet(pathName)
		if err != nil || !pathData.Ready {
			continue
		}

		// For network capture devices, check if colorful content is present
		if pathConf.DeviceType == "network_capture" {
			m.Log(logger.Info, "checking network capture device '%s' for colorful content", pathName)
			if !m.shouldStartNetworkCaptureRecording(pathName, pathConf) {
				continue
			}
		}

		// Start automatic recording
		m.Log(logger.Info, "starting automatic recording for path '%s'", pathName)

		// Get timeout from path config, default to 30 minutes
		timeout := time.Duration(pathConf.AutoRecordTaskOutDuration)
		if timeout <= 0 {
			timeout = 30 * time.Minute
		}

		// Create new task
		task := &Task{
			ID:           uuid.New().String(),
			PathName:     pathName,
			Format:       "mp4", // Auto recording uses MP4 format
			RecordPath:   m.RecordPath,
			PathManager:  m.PathManager,
			PathConf:     pathConf,       // Path-specific config for sourceName
			PathDefaults: m.PathDefaults, // PathDefaults for webhook URL
			Parent:       m,
			Timeout:      timeout,
			IsAutoRecord: true,
		}

		// Initialize and start task
		err = task.Start()
		if err != nil {
			m.Log(logger.Warn, "failed to start automatic recording for path '%s': %v", pathName, err)
			continue
		}

		m.tasks[pathName] = task

		m.Log(logger.Info, "automatic recording started for path '%s', duration: %v", pathName, timeout)
	}
}

// shouldStartNetworkCaptureRecording checks if a network capture device should start recording.
// It maintains state for each path to track colorful content over multiple checks.
func (m *Manager) shouldStartNetworkCaptureRecording(pathName string, pathConf *conf.Path) bool {
	// Check if we have color checker available
	if m.ColorChecker == nil {
		m.Log(logger.Warn, "color checker not available for network capture device '%s', skipping smart check", pathName)
		return true // Fallback to normal auto recording
	}

	// Get or create state for this path
	state := m.getOrCreateCaptureState(pathName)

	// Check device status first
	deviceIP, err := parseDeviceIP(pathConf.Source)
	if err != nil {
		m.Log(logger.Warn, "failed to parse device IP for '%s': %v", pathName, err)
		return false
	}

	availableCount, err := deviceutil.GetInputStatusIsAvalible(deviceIP)
	if err != nil || availableCount == 0 {
		// Device not available, reset state
		state.reset()
		return false
	}

	// Check colorful content
	colorfulVal, err := m.ColorChecker.IsColorful(pathName)
	if err != nil {
		m.Log(logger.Warn, "failed to check colorful for '%s': %v", pathName, err)
		return false
	}

	state.pingCount++
	state.colorfulValue += colorfulVal

	threshold := pathConf.RecordMinThreshold
	if threshold <= 0 {
		threshold = 1 // Default threshold
	}

	m.Log(logger.Info, "Network capture check: path=%s pingCount=%d colorfulValue=%d currentColorful=%d threshold=%d",
		pathName, state.pingCount, state.colorfulValue, colorfulVal, threshold)

	// Need 3 consecutive checks with total colorful value > threshold
	if state.pingCount >= 3 && state.colorfulValue > threshold {
		m.Log(logger.Info, "network capture device '%s' ready to record (colorful content detected)", pathName)
		state.reset() // Reset for next time
		return true
	}

	// Reset after too many checks to avoid overflow
	if state.pingCount > 12 {
		state.reset()
	}

	return false
}

// captureState tracks state for network capture devices
type captureState struct {
	pingCount     int
	colorfulValue int
}

func (s *captureState) reset() {
	s.pingCount = 0
	s.colorfulValue = 0
}

// captureStates stores state for each network capture path
var captureStates = make(map[string]*captureState)
var captureStatesMutex sync.Mutex

func (m *Manager) getOrCreateCaptureState(pathName string) *captureState {
	captureStatesMutex.Lock()
	defer captureStatesMutex.Unlock()

	if state, exists := captureStates[pathName]; exists {
		return state
	}

	state := &captureState{}
	captureStates[pathName] = state
	return state
}

// parseDeviceIP extracts device IP from source URL
func parseDeviceIP(source string) (string, error) {
	u, err := base.ParseURL(source)
	if err != nil {
		return "", fmt.Errorf("failed to parse source URL: %w", err)
	}

	if u.Host == "" {
		return "", fmt.Errorf("source URL has no host")
	}

	return u.Host, nil
}

// ReloadPathConfs reloads path configurations.
func (m *Manager) ReloadPathConfs(pathConfs map[string]*conf.Path) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.PathConfs = pathConfs
	m.Log(logger.Info, "path configurations reloaded")
}

// GetRecordingStates returns the end time for all currently recording paths.
func (m *Manager) GetRecordingStates() map[string]*time.Time {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	states := make(map[string]*time.Time)
	for pathName, task := range m.tasks {
		endTime := task.EndTime
		states[pathName] = &endTime
	}
	return states
}

// OnPathNotReady is called by pathManager when a path becomes not ready.
// This is used to stop automatic recording tasks when the stream disconnects.
func (m *Manager) OnPathNotReady(pathName string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	task, exists := m.tasks[pathName]
	if !exists {
		return
	}

	// Only stop auto-record tasks
	if !task.IsAutoRecord {
		return
	}

	m.Log(logger.Info, "path '%s' is no longer ready, stopping automatic recording", pathName)
	task.Stop()
	delete(m.tasks, pathName)

	// Reset capture state for network capture devices
	captureStatesMutex.Lock()
	if state, exists := captureStates[pathName]; exists {
		state.reset()
	}
	captureStatesMutex.Unlock()
}
