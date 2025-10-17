package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shirou/gopsutil/v3/disk"

	"github.com/bluenviron/mediamtx/internal/logger"
)

// DiskStatus represents disk usage information
type DiskStatus struct {
	All  uint64 `json:"all"`
	Free uint64 `json:"free"`
	Used uint64 `json:"used"`
}

// apiV2DashboardRes is the response for dashboard endpoint
type apiV2DashboardRes struct {
	ID         string     `json:"id"`
	FilesCount int        `json:"filesCount"`
	JpgCount   int        `json:"jpgCount"`
	VideoCount int        `json:"videoCount"`
	PathCount  int        `json:"pathCount"`
	DiskStatus DiskStatus `json:"diskStatus"`
}

// EditFileBody represents file operation parameters
type EditFileBody struct {
	FullPath string `json:"fullPath" form:"fullPath" binding:"required"`
	Name     string `json:"name" form:"name" binding:"required"`
}

// apiV2FileListReq represents file list query parameters
type apiV2FileListReq struct {
	Date     *time.Time `json:"date" form:"date"`
	FileType *string    `json:"fileType" form:"fileType"`
	Search   *string    `json:"search" form:"search"`
}

// FileInfo represents a single file information
type FileInfo struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"modTime"`
	IsDir    bool      `json:"isDir"`
	FileType string    `json:"fileType"` // "video", "image", "other"
}

// apiV2FileListRes represents file list response
type apiV2FileListRes struct {
	Files   []FileInfo `json:"files"`
	Total   int        `json:"total"`
	Success bool       `json:"success"`
}

// SnapshotConfig represents snapshot configuration for a path
type SnapshotConfig struct {
	PathName string `json:"pathName"`
	Enabled  bool   `json:"enabled"`
	Interval int    `json:"interval"` // seconds
	Quality  int    `json:"quality"`  // 1-100
}

// dashboard handles GET /v2/dashboard
func (a *APIV2) dashboard(ctx *gin.Context) {
	a.mutex.RLock()
	recordPath := a.Conf.PathDefaults.RecordPath
	a.mutex.RUnlock()

	// Get disk status
	stat, err := disk.Usage(recordPath)
	if err != nil {
		a.Log(logger.Warn, "Failed to get disk usage: %v", err)
		stat = &disk.UsageStat{Total: 0, Free: 0, Used: 0}
	}

	// Get paths count
	pathsData, err := a.PathManager.APIPathsList()
	pathCount := 0
	if err == nil {
		pathCount = len(pathsData.Items)
	}

	// Count files in record directory
	allFiles, jpgFiles := a.countRecordFiles(recordPath)

	res := apiV2DashboardRes{
		ID:         "dashboard",
		FilesCount: len(allFiles),
		JpgCount:   len(jpgFiles),
		VideoCount: len(allFiles) - len(jpgFiles),
		PathCount:  pathCount,
		DiskStatus: DiskStatus{
			All:  stat.Total,
			Free: stat.Free,
			Used: stat.Used,
		},
	}

	ctx.JSON(http.StatusOK, gin.H{
		"success": true,
		"result":  res,
	})
}

// countRecordFiles counts all files and jpg files in record directory
func (a *APIV2) countRecordFiles(recordPath string) (allFiles []string, jpgFiles []string) {
	allFiles = []string{}
	jpgFiles = []string{}

	err := filepath.Walk(recordPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if !info.IsDir() {
			allFiles = append(allFiles, path)
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".jpg" || ext == ".jpeg" {
				jpgFiles = append(jpgFiles, path)
			}
		}
		return nil
	})

	if err != nil {
		a.Log(logger.Warn, "Failed to walk record directory: %v", err)
	}

	return allFiles, jpgFiles
}

// getRecordTask handles GET /v2/record/task/*name
func (a *APIV2) getRecordTask(ctx *gin.Context) {
	name := ctx.Param("name")
	if len(name) < 2 || name[0] != '/' {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid name"))
		return
	}
	pathName := name[1:]

	if a.RecordManager == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, fmt.Errorf("record manager not available"))
		return
	}

	// Get recording state for this path
	states := a.RecordManager.GetRecordingStates()
	endTime, exists := states[pathName]

	if !exists {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("no recording task found for path: %s", pathName))
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"success": true,
		"result": gin.H{
			"pathName":    pathName,
			"taskEndTime": endTime,
			"isRecording": true,
		},
	})
}

// getRecordTasks handles GET /v2/record/tasks
func (a *APIV2) getRecordTasks(ctx *gin.Context) {
	if a.RecordManager == nil {
		a.writeError(ctx, http.StatusServiceUnavailable, fmt.Errorf("record manager not available"))
		return
	}

	states := a.RecordManager.GetRecordingStates()

	tasks := []gin.H{}
	for pathName, endTime := range states {
		tasks = append(tasks, gin.H{
			"pathName":    pathName,
			"taskEndTime": endTime,
			"isRecording": true,
		})
	}

	ctx.JSON(http.StatusOK, gin.H{
		"success": true,
		"result": gin.H{
			"tasks": tasks,
			"total": len(tasks),
		},
	})
}

// fileRename handles POST /v2/file/rename
func (a *APIV2) fileRename(ctx *gin.Context) {
	var body EditFileBody
	if err := ctx.ShouldBindJSON(&body); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.mutex.RLock()
	recordPath := a.Conf.PathDefaults.RecordPath
	a.mutex.RUnlock()

	// Validate paths
	fullPath, err := a.validateFilePath(body.FullPath, recordPath)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	// Check if source file exists
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("file not found: %s", body.FullPath))
		return
	}

	// Build new file path
	dir := filepath.Dir(fullPath)
	newPath := filepath.Join(dir, body.Name)

	// Rename file
	if err := os.Rename(fullPath, newPath); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("failed to rename file: %w", err))
		return
	}

	a.Log(logger.Info, "File renamed: %s -> %s", fullPath, newPath)

	ctx.JSON(http.StatusOK, gin.H{
		"success": true,
		"result": gin.H{
			"oldPath": body.FullPath,
			"newPath": a.PathToURL(newPath),
		},
	})
}

// fileDel handles POST /v2/file/del
func (a *APIV2) fileDel(ctx *gin.Context) {
	var body EditFileBody
	if err := ctx.ShouldBindJSON(&body); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.mutex.RLock()
	recordPath := a.Conf.PathDefaults.RecordPath
	a.mutex.RUnlock()

	// Validate path
	fullPath, err := a.validateFilePath(body.FullPath, recordPath)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	// Check if file exists
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("file not found: %s", body.FullPath))
		return
	}

	// Delete file
	if err := os.Remove(fullPath); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("failed to delete file: %w", err))
		return
	}

	a.Log(logger.Info, "File deleted: %s", fullPath)

	ctx.JSON(http.StatusOK, gin.H{
		"success": true,
		"result": gin.H{
			"path": body.FullPath,
		},
	})
}

// fileMove handles POST /v2/file/favorite (move to favorite folder)
func (a *APIV2) fileMove(ctx *gin.Context) {
	var body EditFileBody
	if err := ctx.ShouldBindJSON(&body); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.mutex.RLock()
	recordPath := a.Conf.PathDefaults.RecordPath
	a.mutex.RUnlock()

	// Validate source path
	fullPath, err := a.validateFilePath(body.FullPath, recordPath)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	// Check if source file exists
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		a.writeError(ctx, http.StatusNotFound, fmt.Errorf("file not found: %s", body.FullPath))
		return
	}

	// Create favorite directory if not exists
	favoriteDir := filepath.Join(recordPath, "favorite")
	if err := os.MkdirAll(favoriteDir, 0755); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("failed to create favorite directory: %w", err))
		return
	}

	// Build destination path
	destPath := filepath.Join(favoriteDir, body.Name)

	// Move file
	if err := os.Rename(fullPath, destPath); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("failed to move file: %w", err))
		return
	}

	a.Log(logger.Info, "File moved to favorite: %s -> %s", fullPath, destPath)

	ctx.JSON(http.StatusOK, gin.H{
		"success": true,
		"result": gin.H{
			"oldPath": body.FullPath,
			"newPath": a.PathToURL(destPath),
		},
	})
}

// onFilesListGet handles GET /v2/record/date/files
func (a *APIV2) onFilesListGet(ctx *gin.Context) {
	var req apiV2FileListReq
	if err := ctx.ShouldBindQuery(&req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.mutex.RLock()
	recordPath := a.Conf.PathDefaults.RecordPath
	a.mutex.RUnlock()

	// Determine search directory based on date
	searchDir := recordPath
	if req.Date != nil {
		dateStr := req.Date.Format("20060102")
		searchDir = filepath.Join(recordPath, dateStr)
	}

	files := a.listFiles(searchDir, req.FileType, req.Search)

	ctx.JSON(http.StatusOK, gin.H{
		"success": true,
		"result": apiV2FileListRes{
			Files:   files,
			Total:   len(files),
			Success: true,
		},
	})
}

// onFilesFavoriteGet handles GET /v2/record/favorite/files
func (a *APIV2) onFilesFavoriteGet(ctx *gin.Context) {
	var req apiV2FileListReq
	if err := ctx.ShouldBindQuery(&req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.mutex.RLock()
	recordPath := a.Conf.PathDefaults.RecordPath
	a.mutex.RUnlock()

	// Search in favorite directory
	favoriteDir := filepath.Join(recordPath, "favorite")
	files := a.listFiles(favoriteDir, req.FileType, req.Search)

	ctx.JSON(http.StatusOK, gin.H{
		"success": true,
		"result": apiV2FileListRes{
			Files:   files,
			Total:   len(files),
			Success: true,
		},
	})
}

// listFiles lists files in a directory with optional filtering
func (a *APIV2) listFiles(dir string, fileType *string, search *string) []FileInfo {
	files := []FileInfo{}

	entries, err := os.ReadDir(dir)
	if err != nil {
		a.Log(logger.Warn, "Failed to read directory %s: %v", dir, err)
		return files
	}

	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}

		fullPath := filepath.Join(dir, entry.Name())
		fileName := entry.Name()

		// Apply search filter
		if search != nil && *search != "" {
			if !strings.Contains(strings.ToLower(fileName), strings.ToLower(*search)) {
				continue
			}
		}

		// Determine file type
		ext := strings.ToLower(filepath.Ext(fileName))
		var fType string
		if ext == ".mp4" || ext == ".ts" || ext == ".mkv" || ext == ".avi" {
			fType = "video"
		} else if ext == ".jpg" || ext == ".jpeg" || ext == ".png" {
			fType = "image"
		} else {
			fType = "other"
		}

		// Apply file type filter
		if fileType != nil && *fileType != "" && *fileType != fType {
			continue
		}

		files = append(files, FileInfo{
			Name:     fileName,
			Path:     a.PathToURL(fullPath),
			Size:     info.Size(),
			ModTime:  info.ModTime(),
			IsDir:    entry.IsDir(),
			FileType: fType,
		})
	}

	return files
}

// onPathsGet2 handles GET /v2/paths/get/*name (duplicate of onPathsGet for compatibility)
func (a *APIV2) onPathsGet2(ctx *gin.Context) {
	a.onPathsGet(ctx)
}

// PostMessage handles POST /v2/paths/message (websocket broadcast)
func (a *APIV2) PostMessage(ctx *gin.Context) {
	var message interface{}
	if err := ctx.ShouldBindJSON(&message); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	// Broadcast message to all connected WebSocket clients
	if a.wsHub != nil {
		a.wsHub.Broadcast(message)
		a.Log(logger.Info, "Message broadcast to %d WebSocket clients", a.wsHub.ClientCount())
	} else {
		a.Log(logger.Warn, "WebSocket hub not initialized, message not broadcast")
	}

	ctx.JSON(http.StatusOK, gin.H{
		"success": true,
		"result": gin.H{
			"clients": a.wsHub.ClientCount(),
		},
	})
}

// snapshotConfGet handles GET /v2/snapshot/config/*name
func (a *APIV2) snapshotConfGet(ctx *gin.Context) {
	name := ctx.Param("name")
	if len(name) < 2 || name[0] != '/' {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid name"))
		return
	}
	pathName := name[1:]

	a.mutex.RLock()
	recordPath := a.Conf.PathDefaults.RecordPath
	a.mutex.RUnlock()

	// Load snapshot config from file
	configPath := filepath.Join(recordPath, fmt.Sprintf("snapshot_%s.json", pathName))

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Return default config
			ctx.JSON(http.StatusOK, gin.H{
				"success": true,
				"result": SnapshotConfig{
					PathName: pathName,
					Enabled:  false,
					Interval: 10,
					Quality:  80,
				},
			})
			return
		}
		a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("failed to read config: %w", err))
		return
	}

	var config SnapshotConfig
	if err := json.Unmarshal(data, &config); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("failed to parse config: %w", err))
		return
	}

	ctx.JSON(http.StatusOK, gin.H{
		"success": true,
		"result":  config,
	})
}

// snapshotConfSave handles POST /v2/snapshot/config/*name
func (a *APIV2) snapshotConfSave(ctx *gin.Context) {
	name := ctx.Param("name")
	if len(name) < 2 || name[0] != '/' {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid name"))
		return
	}
	pathName := name[1:]

	var config SnapshotConfig
	if err := ctx.ShouldBindJSON(&config); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	// Ensure pathName matches
	config.PathName = pathName

	a.mutex.RLock()
	recordPath := a.Conf.PathDefaults.RecordPath
	a.mutex.RUnlock()

	// Save snapshot config to file
	configPath := filepath.Join(recordPath, fmt.Sprintf("snapshot_%s.json", pathName))

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("failed to marshal config: %w", err))
		return
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("failed to write config: %w", err))
		return
	}

	a.Log(logger.Info, "Snapshot config saved for path: %s", pathName)

	// Trigger video snapshot restart
	if err := a.PathManager.RestartVideoSnapshot(pathName); err != nil {
		a.Log(logger.Warn, "Failed to restart video snapshot for path %s: %v", pathName, err)
		// Don't fail the request, just log the warning
	} else {
		a.Log(logger.Info, "Video snapshot restarted for path: %s", pathName)
	}

	ctx.JSON(http.StatusOK, gin.H{
		"success": true,
		"result": gin.H{
			"config": config,
		},
	})
}

// proxyToDevice handles Any /v2/proxy/device/*path
func (a *APIV2) proxyToDevice(ctx *gin.Context) {
	path := ctx.Param("path")

	// Get device address from configuration or query parameter
	deviceAddr := ctx.Query("deviceAddr")
	if deviceAddr == "" {
		// TODO: Get from configuration
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("deviceAddr parameter required"))
		return
	}

	// Build target URL
	targetURL := fmt.Sprintf("http://%s/iw%s", deviceAddr, path)

	// Parse target URL
	target, err := url.Parse(targetURL)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("invalid target URL: %w", err))
		return
	}

	// Create reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(target)

	// Modify request
	proxy.Director = func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path = "/iw" + path
		req.Host = target.Host

		// Forward query parameters
		if ctx.Request.URL.RawQuery != "" {
			req.URL.RawQuery = ctx.Request.URL.RawQuery
		}

		// Forward headers
		for key, values := range ctx.Request.Header {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	}

	// Handle errors
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		a.Log(logger.Error, "Proxy error: %v", err)
		ctx.JSON(http.StatusBadGateway, gin.H{
			"error": "failed to proxy request to device",
		})
	}

	// Serve the request
	proxy.ServeHTTP(ctx.Writer, ctx.Request)
}

// validateFilePath validates and cleans a file path to prevent path traversal
func (a *APIV2) validateFilePath(userPath string, baseWorkPath string) (string, error) {
	// Clean the path
	cleanPath := filepath.Clean(userPath)

	// Remove leading slash if present
	cleanPath = strings.TrimPrefix(cleanPath, "/")

	// Check for path traversal attempts
	if strings.Contains(cleanPath, "..") {
		return "", fmt.Errorf("path traversal not allowed")
	}

	// Build full path
	fullPath := filepath.Join(baseWorkPath, cleanPath)

	// Verify the path is within baseWorkPath
	absBase, _ := filepath.Abs(baseWorkPath)
	absFull, _ := filepath.Abs(fullPath)

	if !strings.HasPrefix(absFull, absBase) {
		return "", fmt.Errorf("path outside allowed directory")
	}

	return fullPath, nil
}
