// Package api contains the Pro API server.
package api

import (
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/auth"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/protocols/httpp"
	"github.com/bluenviron/mediamtx/pro/recorder"
	"github.com/bluenviron/mediamtx/pro/websocketapi"
)

type apiAuthManager interface {
	Authenticate(req *auth.Request) *auth.Error
}

type apiParent interface {
	logger.Writer
	APIConfigSet(conf *conf.Conf)
}

// APIV2 is the Pro version API server.
type APIV2 struct {
	Version        string
	Started        time.Time
	Address        string
	Encryption     bool
	ServerKey      string
	ServerCert     string
	AllowOrigin    string
	TrustedProxies conf.IPNetworks
	ReadTimeout    conf.Duration
	WriteTimeout   conf.Duration
	Conf           *conf.Conf
	AuthManager    apiAuthManager
	PathManager    defs.APIPathManager
	RTSPServer     defs.APIRTSPServer
	RTSPSServer    defs.APIRTSPServer
	RTMPServer     defs.APIRTMPServer
	RTMPSServer    defs.APIRTMPServer
	WebRTCServer   defs.APIWebRTCServer
	RecordManager   *recorder.Manager
	Parent          apiParent
	APIAuthMiddleware *APIKeyAuthMiddleware

	httpServer *httpp.Server
	wsHub      *websocketapi.Hub
	mutex      sync.RWMutex
}

// Initialize initializes the Pro API.
func (a *APIV2) Initialize() error {
	router := gin.New()
	router.SetTrustedProxies(a.TrustedProxies.ToTrustedProxies()) //nolint:errcheck

	router.Use(a.middlewareOrigin)

	// Use API token auth if enabled, otherwise use default auth
	if a.Conf.APIAuth && a.APIAuthMiddleware != nil {
		router.Use(a.APIAuthMiddleware.AuthMiddleware())
		a.Log(logger.Info, "API token authentication enabled")
	} else {
		router.Use(a.middlewareAuth)
	}

	// V2 API Group
	group := router.Group("/v2")

	// Basic endpoints
	group.GET("/info", a.onInfo)
	group.GET("/health", a.onHealth)
	group.GET("/stats", a.onStats)

	// Config endpoints
	group.GET("/config/global/get", a.onConfigGlobalGet)
	group.PATCH("/config/global/patch", a.onConfigGlobalPatch)

	// Paths endpoints (reuse from internal/api)
	group.GET("/paths/list", a.onPathsList)
	group.GET("/paths/get/*name", a.onPathsGet)
	group.GET("/paths/query", a.onPathsQuery)

	// RTSP endpoints
	if a.RTSPServer != nil {
		group.GET("/rtspconns/list", a.onRTSPConnsList)
		group.GET("/rtspsessions/list", a.onRTSPSessionsList)
	}

	// RTMP endpoints
	if a.RTMPServer != nil {
		group.GET("/rtmpconns/list", a.onRTMPConnsList)
	}

	// WebRTC endpoints
	if a.WebRTCServer != nil {
		group.GET("/webrtcsessions/list", a.onWebRTCSessionsList)
	}

	// Recording endpoints
	if a.RecordManager != nil {
		group.POST("/record/start", a.onRecordStart)
		group.POST("/record/stop", a.onRecordStop)
		group.GET("/record/task/*name", a.getRecordTask)
		group.GET("/record/tasks", a.getRecordTasks)
	}

	// Dashboard endpoint
	group.GET("/dashboard", a.dashboard)

	// File management endpoints
	group.POST("/file/rename", a.fileRename)
	group.POST("/file/del", a.fileDel)
	group.POST("/file/favorite", a.fileMove)
	group.GET("/record/date/files", a.onFilesListGet)
	group.GET("/record/favorite/files", a.onFilesFavoriteGet)

	// Path endpoints (additional)
	group.GET("/paths/get2/*name", a.onPathsGet2)
	group.POST("/paths/message", a.PostMessage)

	// WebSocket endpoint for real-time messaging
	a.wsHub = websocketapi.NewHub(a)
	go a.wsHub.Run()
	router.GET("/ws", func(c *gin.Context) {
		websocketapi.ServeWS(a.wsHub, c)
	})

	// FFmpeg export endpoint
	group.POST("/file/export/mp4", a.ExportMP4)

	// Snapshot configuration endpoints
	group.GET("/snapshot/config/*name", a.snapshotConfGet)
	group.POST("/snapshot/config/*name", a.snapshotConfSave)

	// Snapshot capture endpoints
	group.GET("/snapshot", a.snapshot)
	group.GET("/publish/snapshot", a.snapshotStream)

	// Device proxy endpoint
	group.Any("/proxy/device/*path", a.proxyToDevice)

	// Static file service for recorded files
	router.Static("/res", a.Conf.PathDefaults.RecordPath)

	// Setup static routes for admin panel and docs
	a.setupStaticRoutes(router)

	// WebRTC HTTP endpoints (WHIP/WHEP) - Use NoRoute as fallback
	// This allows WebRTC requests to be handled after all other routes
	if a.WebRTCServer != nil {
		router.NoRoute(a.onWebRTCFallback)
	}

	a.httpServer = &httpp.Server{
		Address:      a.Address,
		ReadTimeout:  time.Duration(a.ReadTimeout),
		WriteTimeout: time.Duration(a.WriteTimeout),
		Encryption:   a.Encryption,
		ServerCert:   a.ServerCert,
		ServerKey:    a.ServerKey,
		Handler:      router,
		Parent:       a,
	}
	err := a.httpServer.Initialize()
	if err != nil {
		return err
	}

	a.Log(logger.Info, "Pro API listener opened on "+a.Address)

	return nil
}

// Close closes the API.
func (a *APIV2) Close() {
	a.Log(logger.Info, "Pro API listener is closing")
	if a.wsHub != nil {
		a.wsHub.Close()
	}
	a.httpServer.Close()
}

// Log implements logger.Writer.
func (a *APIV2) Log(level logger.Level, format string, args ...interface{}) {
	a.Parent.Log(level, "[Pro API] "+format, args...)
}

// APIReaderDescribe implements defs.Reader.
func (a *APIV2) APIReaderDescribe() defs.APIPathSourceOrReader {
	return defs.APIPathSourceOrReader{
		Type: "proAPISnapshot",
		ID:   "snapshot",
	}
}

func (a *APIV2) writeError(ctx *gin.Context, status int, err error) {
	a.Log(logger.Error, err.Error())
	ctx.JSON(status, &defs.APIError{
		Error: err.Error(),
	})
}

func (a *APIV2) middlewareOrigin(ctx *gin.Context) {
	ctx.Header("Access-Control-Allow-Origin", a.AllowOrigin)
	ctx.Header("Access-Control-Allow-Credentials", "true")

	if ctx.Request.Method == http.MethodOptions &&
		ctx.Request.Header.Get("Access-Control-Request-Method") != "" {
		ctx.Header("Access-Control-Allow-Methods", "OPTIONS, GET, POST, PATCH, DELETE")
		ctx.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
		ctx.AbortWithStatus(http.StatusNoContent)
		return
	}
}

func (a *APIV2) middlewareAuth(ctx *gin.Context) {
	req := &auth.Request{
		Action:      conf.AuthActionAPI,
		Query:       ctx.Request.URL.RawQuery,
		Credentials: httpp.Credentials(ctx.Request),
		IP:          net.ParseIP(ctx.ClientIP()),
	}

	err := a.AuthManager.Authenticate(req)
	if err != nil {
		if err.AskCredentials {
			ctx.Header("WWW-Authenticate", `Basic realm="mediamtx-pro"`)
			ctx.AbortWithStatus(http.StatusUnauthorized)
			return
		}

		a.Log(logger.Info, "connection %v failed to authenticate: %v", httpp.RemoteAddr(ctx), err.Wrapped)
		<-time.After(auth.PauseAfterError)
		ctx.AbortWithStatus(http.StatusUnauthorized)
		return
	}
}

func (a *APIV2) onInfo(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, &defs.APIInfo{
		Version: "v2-pro-" + a.Version,
		Started: a.Started,
	})
}

func (a *APIV2) onHealth(ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{
		"status":  "healthy",
		"version": a.Version,
		"uptime":  time.Since(a.Started).String(),
	})
}

func (a *APIV2) onStats(ctx *gin.Context) {
	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()

	pathsData, err := a.PathManager.APIPathsList()
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	stats := gin.H{
		"version":    a.Version,
		"uptime":     time.Since(a.Started).String(),
		"pathsCount": len(pathsData.Items),
		"servers": gin.H{
			"rtsp":   a.RTSPServer != nil,
			"rtmp":   a.RTMPServer != nil,
			"webrtc": a.WebRTCServer != nil,
		},
		"config": gin.H{
			"logLevel":        c.LogLevel,
			"configuredPaths": len(c.Paths),
		},
	}

	ctx.JSON(http.StatusOK, stats)
}

func (a *APIV2) onConfigGlobalGet(ctx *gin.Context) {
	a.mutex.RLock()
	c := a.Conf
	a.mutex.RUnlock()

	ctx.JSON(http.StatusOK, c.Global())
}

func (a *APIV2) onConfigGlobalPatch(ctx *gin.Context) {
	var c conf.OptionalGlobal
	err := ctx.BindJSON(&c)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()

	newConf := a.Conf.Clone()
	newConf.PatchGlobal(&c)

	err = newConf.Validate(nil)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	a.Conf = newConf
	go a.Parent.APIConfigSet(newConf)

	ctx.Status(http.StatusOK)
}

// Reuse internal/api functions by calling PathManager directly
func (a *APIV2) onPathsList(ctx *gin.Context) {
	data, err := a.PathManager.APIPathsList()
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	data.ItemCount = len(data.Items)
	ctx.JSON(http.StatusOK, data)
}

func (a *APIV2) onPathsGet(ctx *gin.Context) {
	name := ctx.Param("name")
	if len(name) < 2 || name[0] != '/' {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid name"))
		return
	}
	pathName := name[1:]

	data, err := a.PathManager.APIPathsGet(pathName)
	if err != nil {
		a.writeError(ctx, http.StatusNotFound, err)
		return
	}

	ctx.JSON(http.StatusOK, data)
}

func (a *APIV2) onRTSPConnsList(ctx *gin.Context) {
	data, err := a.RTSPServer.APIConnsList()
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	data.ItemCount = len(data.Items)
	ctx.JSON(http.StatusOK, data)
}

func (a *APIV2) onRTSPSessionsList(ctx *gin.Context) {
	data, err := a.RTSPServer.APISessionsList()
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	data.ItemCount = len(data.Items)
	ctx.JSON(http.StatusOK, data)
}

func (a *APIV2) onRTMPConnsList(ctx *gin.Context) {
	data, err := a.RTMPServer.APIConnsList()
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	data.ItemCount = len(data.Items)
	ctx.JSON(http.StatusOK, data)
}

func (a *APIV2) onWebRTCSessionsList(ctx *gin.Context) {
	data, err := a.WebRTCServer.APISessionsList()
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	data.ItemCount = len(data.Items)
	ctx.JSON(http.StatusOK, data)
}

// ReloadConf is called by core.
func (a *APIV2) ReloadConf(conf *conf.Conf) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.Conf = conf
}

// onRecordStart handles POST /v2/record/start
func (a *APIV2) onRecordStart(ctx *gin.Context) {
	var params recorder.StartParams
	err := ctx.BindJSON(&params)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid request: %w", err))
		return
	}

	// Validate format
	if params.VideoFormat != "mp4" && params.VideoFormat != "ts" {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("videoFormat must be 'mp4' or 'ts'"))
		return
	}

	response, err := a.RecordManager.StartRecording(&params)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	ctx.JSON(http.StatusOK, response)
}

// onRecordStop handles POST /v2/record/stop
func (a *APIV2) onRecordStop(ctx *gin.Context) {
	var params recorder.StopParams
	err := ctx.BindJSON(&params)
	if err != nil {
		a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid request: %w", err))
		return
	}

	response, err := a.RecordManager.StopRecording(params.Name)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	ctx.JSON(http.StatusOK, response)
}

// PathQueryItem represents a single path item in the query response
type PathQueryItem struct {
	Name          string     `json:"name"`
	ConfName      string     `json:"confName"`
	SourceName    string     `json:"sourceName"`
	Source        string     `json:"source"`
	SourceReady   bool       `json:"sourceReady"`
	Tracks        []string   `json:"tracks"`
	BytesReceived uint64     `json:"bytesReceived"`
	Readers       []any      `json:"readers"`
	TaskEndTime   *time.Time `json:"taskEndTime"`
	Order         int        `json:"order"`
	GroupName     string     `json:"groupName"`
	ShowList      bool       `json:"showList"`
}

// PathQueryResponse is the response for paths/query endpoint
type PathQueryResponse struct {
	Result  []PathQueryItem `json:"result"`
	Success bool            `json:"success"`
}

// onPathsQuery handles GET /v2/paths/query
func (a *APIV2) onPathsQuery(ctx *gin.Context) {
	a.mutex.RLock()
	pathConfs := a.Conf.Paths
	a.mutex.RUnlock()

	// Get all paths data from PathManager
	pathsData, err := a.PathManager.APIPathsList()
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	// Get recording states from RecordManager
	recordingStates := make(map[string]*time.Time)
	if a.RecordManager != nil {
		recordingStates = a.RecordManager.GetRecordingStates()
	}

	var result []PathQueryItem
	for _, pathData := range pathsData.Items {
		// Get path configuration
		pathConf, exists := pathConfs[pathData.Name]
		if !exists {
			continue
		}

		// Get sourceName and groupName
		sourceName := ""
		if pathConf.SourceName != nil {
			sourceName = *pathConf.SourceName
		}

		groupName := ""
		if pathConf.GroupName != nil {
			groupName = *pathConf.GroupName
		}

		// Get recording end time
		var taskEndTime *time.Time
		if endTime, recording := recordingStates[pathData.Name]; recording {
			taskEndTime = endTime
		}

		item := PathQueryItem{
			Name:       pathData.Name,
			ConfName:   pathData.ConfName,
			SourceName: sourceName,
			// Source:        pathData.Source,
			SourceReady:   pathData.Ready,
			Tracks:        []string{}, // Can be populated from pathData.Tracks if needed
			BytesReceived: pathData.BytesReceived,
			Readers:       []any{}, // Can be populated from pathData.Readers if needed
			TaskEndTime:   taskEndTime,
			Order:         pathConf.Order,
			GroupName:     groupName,
			ShowList:      pathConf.ShowList,
		}

		result = append(result, item)
	}

	// Sort by order field
	for i := 0; i < len(result)-1; i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i].Order > result[j].Order {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	response := PathQueryResponse{
		Result:  result,
		Success: true,
	}

	ctx.JSON(http.StatusOK, response)
}
