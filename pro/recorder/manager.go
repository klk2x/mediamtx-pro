// Package recorder contains the Pro recorder implementation.
package recorder

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/google/uuid"
)

// Manager manages recording tasks.
type Manager struct {
	RecordPath  string
	APIDomain   string
	APIAddress  string
	PathConfs   map[string]*conf.Path // path configurations for auto recording
	PathManager defs.APIPathManager
	Parent      logger.Writer

	mutex      sync.RWMutex
	tasks      map[string]*Task // key: pathName
	baseURL    string           // cached base URL
	ctx        context.Context
	ctxCancel  func()
	wg         sync.WaitGroup
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

	// Create new task
	task := &Task{
		ID:          uuid.New().String(),
		PathName:    params.Name,
		Format:      params.VideoFormat,
		RecordPath:  m.RecordPath,
		PathManager: m.PathManager,
		Parent:      m,
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
	Name            string  `json:"name" binding:"required"`
	VideoFormat     string  `json:"videoFormat" binding:"required"`
	TaskOutMinutes  float64 `json:"taskOutMinutes"`
	FileName        string  `json:"fileName"`
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
