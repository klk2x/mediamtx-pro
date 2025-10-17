package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthonynsimon/bild/adjust"
	"github.com/anthonynsimon/bild/transform"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	ffmpeg "github.com/u2takey/ffmpeg-go"

	"github.com/bluenviron/mediamtx/internal/auth"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
)

// ImageCopyReq represents image cropping parameters
type ImageCopyReq struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// apiV2SnapshotReq represents snapshot request parameters
type apiV2SnapshotReq struct {
	Name          string        `json:"name" form:"name" binding:"required"`
	FileType      string        `json:"fileType" form:"fileType"`           // url, file, stream (default)
	FileName      string        `json:"fileName" form:"fileName"`           // custom filename
	ImageCopy     string        `json:"imageCopy" form:"imageCopy"`         // JSON string for cropping
	ImageCopyReq  *ImageCopyReq `json:"-"`                                  // Parsed cropping params
	Brightness    int           `json:"brightness" form:"brightness"`       // -100 to 100
	Contrast      int           `json:"contrast" form:"contrast"`           // -100 to 100
	Saturation    int           `json:"saturation" form:"saturation"`       // -100 to 100
	ThumbnailSize int           `json:"thumbnailSize" form:"thumbnailSize"` // Thumbnail width (default 320)
}

// apiV2SnapshotRes represents snapshot response
type apiV2SnapshotRes struct {
	Success   bool   `json:"success"`
	FilePath  string `json:"filePath,omitempty"`
	FileURL   string `json:"fileURL,omitempty"`
	Filename  string `json:"filename,omitempty"`
	FullPath  string `json:"fullPath,omitempty"`
	Original  string `json:"original,omitempty"`
	Thumbnail string `json:"thumbnail,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
}

// getJPGData represents RPC response for device snapshot
type getJPGData struct {
	ID      string `json:"id"`
	Jsonrpc string `json:"jsonrpc"`
	Result  string `json:"result"`
}

// deviceInfo contains parsed device information from RTSP source
type deviceInfo struct {
	IPAddress  string
	StreamPath string
	StreamName string
}

// deviceType represents the type of network device
type deviceType int

const (
	deviceTypeUnknown deviceType = iota
	deviceType1                  // Simple HTTP GET to /1.jpg
	deviceType2                  // RPC + snapshot fetch
)

// snapshot handles GET /v2/snapshot - capture from network device
func (a *APIV2) snapshot(ctx *gin.Context) {
	var req apiV2SnapshotReq
	if err := ctx.ShouldBindQuery(&req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	// Parse imageCopy JSON if provided
	if req.ImageCopy != "" {
		var copyReq ImageCopyReq
		if err := json.Unmarshal([]byte(req.ImageCopy), &copyReq); err != nil {
			a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid imageCopy format: %w", err))
			return
		}
		req.ImageCopyReq = &copyReq
	}

	// Get snapshot from device
	imageBytes, finalReq, err := a.snapshotRequest(req)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	// Process and respond
	a.processSnapshotResponse(ctx, imageBytes, finalReq)
}

// snapshotStream handles GET /v2/publish/snapshot - capture using FFmpeg
func (a *APIV2) snapshotStream(ctx *gin.Context) {
	var req apiV2SnapshotReq
	if err := ctx.ShouldBindQuery(&req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	// Parse imageCopy JSON if provided
	if req.ImageCopy != "" {
		var copyReq ImageCopyReq
		if err := json.Unmarshal([]byte(req.ImageCopy), &copyReq); err != nil {
			a.writeError(ctx, http.StatusBadRequest, fmt.Errorf("invalid imageCopy format: %w", err))
			return
		}
		req.ImageCopyReq = &copyReq
	}

	// Get snapshot using FFmpeg
	imageBytes, finalReq, err := a.snapshotStreamFFmpeg(req)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	// Process and respond
	a.processSnapshotResponse(ctx, imageBytes, finalReq)
}

// parseDeviceInfo extracts device information from RTSP source URL
// Format: rtsp://192.168.3.33/stream0 or rtsp://192.168.3.33/0
func (a *APIV2) parseDeviceInfo(source string) (*deviceInfo, error) {
	parts := strings.Split(source, "/")
	if len(parts) < 3 {
		return nil, errors.New("invalid source format")
	}

	// Extract IP address (remove port if exists)
	ipAddress := strings.Split(parts[2], ":")[0]
	if net.ParseIP(ipAddress) == nil {
		return nil, fmt.Errorf("invalid IP address: %s", ipAddress)
	}

	// Extract stream path and name
	streamPath := ""
	if len(parts) > 3 {
		streamPath = parts[len(parts)-1]
	}

	streamName := filepath.Base(streamPath)
	if streamName == "" {
		streamName = "stream0" // default
	}

	return &deviceInfo{
		IPAddress:  ipAddress,
		StreamPath: streamPath,
		StreamName: streamName,
	}, nil
}

// detectDeviceType determines which device type based on source URL
func (a *APIV2) detectDeviceType(source string, info *deviceInfo) deviceType {
	// Device type 1: path ends with /0 or contains v=0
	if info.StreamPath == "0" || strings.Contains(source, "v=0") {
		return deviceType1
	}
	return deviceType2
}

// fetchSnapshotFromDevice1 fetches snapshot from device type 1 (simple HTTP GET)
func (a *APIV2) fetchSnapshotFromDevice1(info *deviceInfo) ([]byte, error) {
	remoteAddress := fmt.Sprintf("http://%s:80/1.jpg", info.IPAddress)
	a.Log(logger.Info, "Fetching snapshot from device type 1: %s", remoteAddress)

	client := &http.Client{Timeout: 1500 * time.Millisecond}

	req, err := http.NewRequest("GET", remoteAddress, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch snapshot: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device returned status: %s", resp.Status)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return bodyBytes, nil
}

// fetchSnapshotFromDevice2 fetches snapshot from device type 2 (RPC + fetch)
func (a *APIV2) fetchSnapshotFromDevice2(info *deviceInfo) ([]byte, error) {
	a.Log(logger.Info, "Fetching snapshot from device type 2 for stream: %s", info.StreamName)

	client := &http.Client{Timeout: 1500 * time.Millisecond}

	// Step 1: Call RPC to prepare snapshot
	rpcData := fmt.Sprintf(`{"jsonrpc":"2.0","method":"enc.getJPG","params":["%s"],"id":1}`, info.StreamName)
	rpcURL := fmt.Sprintf("http://%s/RPC", info.IPAddress)

	rpcReq, err := http.NewRequest("POST", rpcURL, bytes.NewBufferString(rpcData))
	if err != nil {
		return nil, fmt.Errorf("failed to create RPC request: %w", err)
	}
	rpcReq.Header.Set("Content-Type", "application/json; charset=UTF-8")

	rpcResp, err := client.Do(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("RPC request failed: %w", err)
	}
	defer rpcResp.Body.Close()

	if rpcResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("RPC returned status: %s", rpcResp.Status)
	}

	// Parse RPC response
	rpcBody, err := io.ReadAll(rpcResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read RPC response: %w", err)
	}

	var jpgData getJPGData
	if err := json.Unmarshal(rpcBody, &jpgData); err != nil {
		return nil, fmt.Errorf("failed to parse RPC response: %w", err)
	}

	a.Log(logger.Info, "RPC response - ID: %s, Result: %s", jpgData.ID, jpgData.Result)

	// Step 2: Fetch the actual snapshot
	snapURL := fmt.Sprintf("http://%s/snap/%s.jpg", info.IPAddress, info.StreamName)
	snapReq, err := http.NewRequest("GET", snapURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshot request: %w", err)
	}

	snapResp, err := client.Do(snapReq)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch snapshot: %w", err)
	}
	defer snapResp.Body.Close()

	if snapResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("snapshot returned status: %s", snapResp.Status)
	}

	bodyBytes, err := io.ReadAll(snapResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read snapshot: %w", err)
	}

	return bodyBytes, nil
}

// applyPathConfigDefaults applies default values from path configuration
func (a *APIV2) applyPathConfigDefaults(snapshotReq *apiV2SnapshotReq, pathConf *conf.Path) {
	// Apply cut defaults
	if pathConf.Cut != nil && snapshotReq.ImageCopyReq == nil {
		cut := *pathConf.Cut
		snapshotReq.ImageCopyReq = &ImageCopyReq{
			X: cut[0],
			Y: cut[1],
			W: cut[2],
			H: cut[3],
		}
	}

	// Apply color adjustment defaults
	if snapshotReq.Contrast == 0 && pathConf.Contrast != nil {
		snapshotReq.Contrast = *pathConf.Contrast
	}
	if snapshotReq.Saturation == 0 && pathConf.Saturation != nil {
		snapshotReq.Saturation = *pathConf.Saturation
	}
	if snapshotReq.Brightness == 0 && pathConf.Brightness != nil {
		snapshotReq.Brightness = *pathConf.Brightness
	}

	// Apply thumbnail size from path config
	if snapshotReq.ThumbnailSize == 0 {
		snapshotReq.ThumbnailSize = pathConf.ThumbnailSize
	}
}

// snapshotRequest captures snapshot from network device API
// This is the main orchestration function that coordinates device snapshot capture
func (a *APIV2) snapshotRequest(snapshotReq apiV2SnapshotReq) ([]byte, apiV2SnapshotReq, error) {
	// Use standard MediaMTX AddReader approach to access path
	path, _, err := a.PathManager.AddReader(defs.PathAddReaderReq{
		Author: a,
		AccessRequest: defs.PathAccessRequest{
			Name:     snapshotReq.Name,
			SkipAuth: true,
			Proto:    auth.ProtocolWebRTC, // Use any valid protocol
			IP:       net.IPv4(127, 0, 0, 1),
		},
	})
	if err != nil {
		return nil, snapshotReq, fmt.Errorf("failed to add reader: %w", err)
	}

	// Remove reader when done
	defer path.RemoveReader(defs.PathRemoveReaderReq{Author: a})

	// Get path configuration
	pathConf := path.SafeConf()
	if pathConf == nil {
		return nil, snapshotReq, fmt.Errorf("path configuration not found: %s", snapshotReq.Name)
	}

	// Get source URL
	source := pathConf.Source
	if source == "" {
		return nil, snapshotReq, errors.New("path source not configured")
	}

	// Parse device information from source URL
	deviceInfo, err := a.parseDeviceInfo(source)
	if err != nil {
		return nil, snapshotReq, err
	}

	// Detect device type and fetch snapshot
	var imageBytes []byte
	devType := a.detectDeviceType(source, deviceInfo)

	switch devType {
	case deviceType1:
		imageBytes, err = a.fetchSnapshotFromDevice1(deviceInfo)
	case deviceType2:
		imageBytes, err = a.fetchSnapshotFromDevice2(deviceInfo)
	default:
		return nil, snapshotReq, errors.New("unknown device type")
	}

	if err != nil {
		return nil, snapshotReq, err
	}

	// Apply path configuration defaults
	a.applyPathConfigDefaults(&snapshotReq, pathConf)

	return imageBytes, snapshotReq, nil
}

// snapshotStreamFFmpeg captures snapshot using FFmpeg from RTSP/RTMP stream
func (a *APIV2) snapshotStreamFFmpeg(snapshotReq apiV2SnapshotReq) ([]byte, apiV2SnapshotReq, error) {
	// Use standard MediaMTX AddReader approach to access path
	path, _, err := a.PathManager.AddReader(defs.PathAddReaderReq{
		Author: a,
		AccessRequest: defs.PathAccessRequest{
			Name:     snapshotReq.Name,
			SkipAuth: true,
			Proto:    auth.ProtocolWebRTC, // Use any valid protocol
			IP:       net.IPv4(127, 0, 0, 1),
		},
	})
	if err != nil {
		return nil, snapshotReq, fmt.Errorf("failed to add reader: %w", err)
	}

	// Remove reader when done
	defer path.RemoveReader(defs.PathRemoveReaderReq{Author: a})

	// Get path configuration
	pathConf := path.SafeConf()
	if pathConf == nil {
		return nil, snapshotReq, fmt.Errorf("path configuration not found: %s", snapshotReq.Name)
	}

	// Get record path
	a.mutex.RLock()
	recordPath := a.Conf.PathDefaults.RecordPath
	a.mutex.RUnlock()

	// Get stream URL
	source := pathConf.Source
	if source == "" {
		return nil, snapshotReq, errors.New("path source not configured")
	}

	a.Log(logger.Info, "Capturing snapshot from stream: %s", source)

	// Create temp file for snapshot
	tmpDir := filepath.Join(recordPath, "tmp")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return nil, snapshotReq, fmt.Errorf("failed to create temp directory: %w", err)
	}

	tmpFile := filepath.Join(tmpDir, fmt.Sprintf("snapshot_%s.jpg", uuid.New().String()[:8]))
	defer os.Remove(tmpFile) // Clean up temp file

	// Use FFmpeg to capture single frame
	err = ffmpeg.Input(source, ffmpeg.KwArgs{
		"rtsp_transport": "tcp",
		"timeout":        "5000000", // 5 seconds
	}).Output(tmpFile, ffmpeg.KwArgs{
		"vframes": 1,
		"q:v":     2, // High quality
	}).OverWriteOutput().Run()

	if err != nil {
		return nil, snapshotReq, fmt.Errorf("FFmpeg snapshot failed: %w", err)
	}

	// Read the captured image
	bodyBytes, err := os.ReadFile(tmpFile)
	if err != nil {
		return nil, snapshotReq, fmt.Errorf("failed to read snapshot: %w", err)
	}

	// Apply path configuration defaults
	if pathConf.Cut != nil && snapshotReq.ImageCopyReq == nil {
		cut := *pathConf.Cut
		snapshotReq.ImageCopyReq = &ImageCopyReq{
			X: cut[0],
			Y: cut[1],
			W: cut[2],
			H: cut[3],
		}
	}

	if snapshotReq.Contrast == 0 && pathConf.Contrast != nil {
		snapshotReq.Contrast = *pathConf.Contrast
	}
	if snapshotReq.Saturation == 0 && pathConf.Saturation != nil {
		snapshotReq.Saturation = *pathConf.Saturation
	}
	if snapshotReq.Brightness == 0 && pathConf.Brightness != nil {
		snapshotReq.Brightness = *pathConf.Brightness
	}

	return bodyBytes, snapshotReq, nil
}

// processSnapshotResponse processes the snapshot image and sends response
func (a *APIV2) processSnapshotResponse(ctx *gin.Context, imageBytes []byte, req apiV2SnapshotReq) {
	// Decode image
	img, format, err := image.Decode(bytes.NewReader(imageBytes))
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("failed to decode image: %w", err))
		return
	}

	a.Log(logger.Info, "Original image format: %s, size: %dx%d", format, img.Bounds().Dx(), img.Bounds().Dy())

	// Apply color adjustments
	processedImg := img
	if req.Brightness != 0 || req.Contrast != 0 || req.Saturation != 0 {
		processedImg = a.applyColorAdjustments(img, req.Brightness, req.Contrast, req.Saturation)
	}

	// Apply cropping
	croppedImg := processedImg
	if req.ImageCopyReq != nil {
		croppedImg = a.cropImage(processedImg, req.ImageCopyReq)
	}

	// Handle response based on fileType
	switch req.FileType {
	case "stream":
		// Return image stream directly
		ctx.Header("Content-Type", "image/jpeg")
		buf := new(bytes.Buffer)
		if err := jpeg.Encode(buf, croppedImg, &jpeg.Options{Quality: 95}); err != nil {
			a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("failed to encode image: %w", err))
			return
		}
		ctx.Data(http.StatusOK, "image/jpeg", buf.Bytes())

	case "url", "file":
		// Save to file and return URL or file path
		res, err := a.saveSnapshotToFile(croppedImg, req)
		if err != nil {
			a.writeError(ctx, http.StatusInternalServerError, err)
			return
		}
		ctx.JSON(http.StatusOK, res)

	default:
		// Default: return stream
		ctx.Header("Content-Type", "image/jpeg")
		buf := new(bytes.Buffer)
		if err := jpeg.Encode(buf, croppedImg, &jpeg.Options{Quality: 95}); err != nil {
			a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("failed to encode image: %w", err))
			return
		}
		ctx.Data(http.StatusOK, "image/jpeg", buf.Bytes())
	}
}

// applyColorAdjustments applies brightness, contrast, and saturation adjustments
func (a *APIV2) applyColorAdjustments(img image.Image, brightness, contrast, saturation int) image.Image {
	result := img

	// Apply brightness (-100 to 100 -> -1.0 to 1.0)
	if brightness != 0 {
		brightnessVal := float64(brightness) / 100.0
		result = adjust.Brightness(result, brightnessVal)
	}

	// Apply contrast (-100 to 100 -> -1.0 to 1.0)
	if contrast != 0 {
		contrastVal := float64(contrast) / 100.0
		result = adjust.Contrast(result, contrastVal)
	}

	// Apply saturation (-100 to 100 -> -1.0 to 1.0)
	if saturation != 0 {
		saturationVal := float64(saturation) / 100.0
		result = adjust.Saturation(result, saturationVal)
	}

	return result
}

// cropImage crops the image based on parameters
func (a *APIV2) cropImage(img image.Image, crop *ImageCopyReq) image.Image {
	bounds := img.Bounds()

	// Validate crop parameters
	if crop.X < 0 {
		crop.X = 0
	}
	if crop.Y < 0 {
		crop.Y = 0
	}
	if crop.X+crop.W > bounds.Dx() {
		crop.W = bounds.Dx() - crop.X
	}
	if crop.Y+crop.H > bounds.Dy() {
		crop.H = bounds.Dy() - crop.Y
	}

	// Crop using transform.Crop
	return transform.Crop(img, image.Rect(crop.X, crop.Y, crop.X+crop.W, crop.Y+crop.H))
}

// saveSnapshotToFile saves the snapshot to file and returns response
func (a *APIV2) saveSnapshotToFile(croppedImg image.Image, req apiV2SnapshotReq) (*apiV2SnapshotRes, error) {
	a.mutex.RLock()
	recordPath := a.Conf.PathDefaults.RecordPath
	a.mutex.RUnlock()

	// Generate filename
	timestamp := time.Now().UnixMilli()
	randomID := uuid.New().String()[:16]
	baseFilename := fmt.Sprintf("%d-%s", timestamp, randomID)

	if req.FileName != "" {
		baseFilename = req.FileName
	}

	// Create date-based directory
	dateDir := time.Now().Format("20060102")
	saveDir := filepath.Join(recordPath, dateDir)

	if err := os.MkdirAll(saveDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	// Save original/processed image
	originalFilename := baseFilename + ".jpg"
	originalPath := filepath.Join(saveDir, originalFilename)

	originalFile, err := os.Create(originalPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}
	defer originalFile.Close()

	if err := jpeg.Encode(originalFile, croppedImg, &jpeg.Options{Quality: 95}); err != nil {
		return nil, fmt.Errorf("failed to encode image: %w", err)
	}

	// Build response
	relPath := "/" + filepath.Join(dateDir, originalFilename)
	fileURL := a.PathToURL(originalPath)

	res := &apiV2SnapshotRes{
		Success:  true,
		FilePath: relPath,
		FileURL:  fileURL,
		Filename: originalFilename,
		FullPath: originalPath,
		Original: originalFilename,
		Width:    croppedImg.Bounds().Dx(),
		Height:   croppedImg.Bounds().Dy(),
	}

	// Create thumbnail only if thumbnailSize is specified
	if req.ThumbnailSize > 0 {
		thumbnailFilename := "thumbnail-" + baseFilename + ".jpg"
		thumbnailPath := filepath.Join(saveDir, thumbnailFilename)

		// Get current image dimensions
		currentWidth := croppedImg.Bounds().Dx()
		currentHeight := croppedImg.Bounds().Dy()

		// Calculate target height maintaining aspect ratio
		// thumbnailSize is always the target width
		targetWidth := req.ThumbnailSize
		targetHeight := int(float64(currentHeight) * float64(targetWidth) / float64(currentWidth))

		// Resize the image
		thumbnailImg := transform.Resize(croppedImg, targetWidth, targetHeight, transform.Lanczos)

		thumbnailFile, err := os.Create(thumbnailPath)
		if err != nil {
			a.Log(logger.Warn, "failed to create thumbnail file: %v", err)
		} else {
			defer thumbnailFile.Close()
			if err := jpeg.Encode(thumbnailFile, thumbnailImg, &jpeg.Options{Quality: 85}); err != nil {
				a.Log(logger.Warn, "failed to encode thumbnail: %v", err)
			} else {
				res.Thumbnail = thumbnailFilename
				a.Log(logger.Info, "Thumbnail created: %dx%d", thumbnailImg.Bounds().Dx(), thumbnailImg.Bounds().Dy())
			}
		}
	}

	a.Log(logger.Info, "Snapshot saved: %s", originalPath)

	return res, nil
}

// GetSnapshot implements snapshotGetter interface for health checker.
// Returns raw image bytes and content type.
func (a *APIV2) GetSnapshot(pathName string) ([]byte, string, error) {
	req := apiV2SnapshotReq{
		Name:     pathName,
		FileType: "stream", // Return raw bytes
	}

	imageBytes, _, err := a.snapshotRequest(req)
	if err != nil {
		return nil, "", err
	}

	return imageBytes, "image/jpeg", nil
}
