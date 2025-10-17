package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediamtx/internal/auth"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/stream"
	"github.com/bluenviron/mediamtx/internal/unit"
	"github.com/gin-gonic/gin"
)

// snapshotNative handles GET /v2/snapshot/native - pure Golang snapshot from MediaMTX stream
func (a *APIV2) snapshotNative(ctx *gin.Context) {
	var req apiV2SnapshotReq
	if err := ctx.ShouldBindQuery(&req); err != nil {
		a.writeError(ctx, http.StatusBadRequest, err)
		return
	}

	// Capture frame from MediaMTX stream
	imageBytes, finalReq, err := a.captureFrameFromStream(req)
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, err)
		return
	}

	// Process and respond
	a.processSnapshotResponse(ctx, imageBytes, finalReq)
}

// captureFrameFromStream captures a single frame from MediaMTX stream using native Go
func (a *APIV2) captureFrameFromStream(snapshotReq apiV2SnapshotReq) ([]byte, apiV2SnapshotReq, error) {
	// Add reader to path
	path, st, err := a.PathManager.AddReader(defs.PathAddReaderReq{
		Author: a,
		AccessRequest: defs.PathAccessRequest{
			Name:     snapshotReq.Name,
			SkipAuth: true,
			Proto:    auth.ProtocolWebRTC,
			IP:       net.IPv4(127, 0, 0, 1),
		},
	})
	if err != nil {
		return nil, snapshotReq, fmt.Errorf("failed to add reader: %w", err)
	}
	defer path.RemoveReader(defs.PathRemoveReaderReq{Author: a})

	if st == nil {
		return nil, snapshotReq, errors.New("no stream available")
	}

	// Get path configuration
	pathConf := path.SafeConf()
	if pathConf != nil {
		a.applyPathConfigDefaults(&snapshotReq, pathConf)
	}

	// Find video media/format
	videoMedia, videoFormat, err := a.findVideoTrack(st)
	if err != nil {
		return nil, snapshotReq, err
	}

	a.Log(logger.Info, "Found video track: %s", videoFormat.Codec())

	// Create frame capturer based on format
	var capturer frameCapturer
	switch forma := videoFormat.(type) {
	case *format.H264:
		capturer = &h264Capturer{format: forma}
	case *format.H265:
		capturer = &h265Capturer{format: forma}
	case *format.MJPEG:
		capturer = &mjpegCapturer{}
	default:
		return nil, snapshotReq, fmt.Errorf("unsupported video format: %s", videoFormat.Codec())
	}

	// Capture frame
	frameData, err := a.captureFrame(st, videoMedia, videoFormat, capturer)
	if err != nil {
		return nil, snapshotReq, err
	}

	return frameData, snapshotReq, nil
}

// findVideoTrack finds the first video track in the stream
func (a *APIV2) findVideoTrack(st *stream.Stream) (*description.Media, format.Format, error) {
	for _, media := range st.Desc.Medias {
		for _, forma := range media.Formats {
			switch forma.(type) {
			case *format.H264, *format.H265, *format.MJPEG:
				return media, forma, nil
			}
		}
	}
	return nil, nil, errors.New("no video track found")
}

// captureFrame captures a single frame from the stream
func (a *APIV2) captureFrame(
	st *stream.Stream,
	media *description.Media,
	forma format.Format,
	capturer frameCapturer,
) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	frameChan := make(chan []byte, 1)
	errorChan := make(chan error, 1)

	// Create reader
	reader := &stream.Reader{
		Parent: a,
	}

	// Register callback for video data
	reader.OnData(media, forma, func(u *unit.Unit) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Try to extract frame
		frameData, err := capturer.extractFrame(u)
		if err != nil {
			return nil // Skip this unit, continue waiting
		}

		if frameData != nil {
			select {
			case frameChan <- frameData:
				return fmt.Errorf("frame captured") // Signal to stop
			default:
			}
		}

		return nil
	})

	// Add reader to stream
	st.AddReader(reader)

	// Wait for frame or timeout
	select {
	case frameData := <-frameChan:
		st.RemoveReader(reader)
		return frameData, nil

	case err := <-reader.Error():
		st.RemoveReader(reader)
		if err.Error() == "frame captured" {
			// This is expected
			return <-frameChan, nil
		}
		return nil, err

	case err := <-errorChan:
		st.RemoveReader(reader)
		return nil, err

	case <-ctx.Done():
		st.RemoveReader(reader)
		return nil, errors.New("timeout waiting for frame")
	}
}

// frameCapturer is an interface for extracting frames from different video formats
type frameCapturer interface {
	extractFrame(u *unit.Unit) ([]byte, error)
}

// mjpegCapturer captures MJPEG frames
type mjpegCapturer struct{}

func (c *mjpegCapturer) extractFrame(u *unit.Unit) ([]byte, error) {
	// MJPEG is already in JPEG format
	if payload, ok := u.Payload.(unit.PayloadMJPEG); ok {
		return []byte(payload), nil
	}
	return nil, nil
}

// h264Capturer captures H264 frames and converts to JPEG
type h264Capturer struct {
	format *format.H264
	// We would need a decoder here - for now, return error
	// In production, you'd use something like github.com/nareix/joy4 or cgo with ffmpeg
}

func (c *h264Capturer) extractFrame(u *unit.Unit) ([]byte, error) {
	// H264 decoding requires external library
	// For now, return error to indicate this needs implementation
	return nil, fmt.Errorf("H264 decoding not implemented - use FFmpeg endpoint or MJPEG format")
}

// h265Capturer captures H265 frames and converts to JPEG
type h265Capturer struct {
	format *format.H265
}

func (c *h265Capturer) extractFrame(u *unit.Unit) ([]byte, error) {
	// H265 decoding requires external library
	return nil, fmt.Errorf("H265 decoding not implemented - use FFmpeg endpoint or MJPEG format")
}

// For streams that already provide MJPEG, this is the ideal solution
// For H264/H265 streams, we have two options:
// 1. Use FFmpeg (current snapshotStreamFFmpeg implementation)
// 2. Implement pure Go decoder (complex, would need cgo or joy4-like library)

// snapshotNativeMJPEG handles continuous MJPEG stream
// This endpoint can be used as an <img src="/v2/snapshot/mjpeg?name=xxx"> in HTML
func (a *APIV2) snapshotNativeMJPEG(ctx *gin.Context) {
	pathName := ctx.Query("name")
	if pathName == "" {
		a.writeError(ctx, http.StatusBadRequest, errors.New("name parameter required"))
		return
	}

	// Add reader to path
	path, st, err := a.PathManager.AddReader(defs.PathAddReaderReq{
		Author: a,
		AccessRequest: defs.PathAccessRequest{
			Name:     pathName,
			SkipAuth: true,
			Proto:    auth.ProtocolWebRTC,
			IP:       net.IPv4(127, 0, 0, 1),
		},
	})
	if err != nil {
		a.writeError(ctx, http.StatusInternalServerError, fmt.Errorf("failed to add reader: %w", err))
		return
	}
	defer path.RemoveReader(defs.PathRemoveReaderReq{Author: a})

	if st == nil {
		a.writeError(ctx, http.StatusNotFound, errors.New("no stream available"))
		return
	}

	// Find MJPEG track
	var videoMedia *description.Media
	var mjpegFormat *format.MJPEG
	for _, media := range st.Desc.Medias {
		for _, forma := range media.Formats {
			if mjpeg, ok := forma.(*format.MJPEG); ok {
				videoMedia = media
				mjpegFormat = mjpeg
				break
			}
		}
		if mjpegFormat != nil {
			break
		}
	}

	if mjpegFormat == nil {
		a.writeError(ctx, http.StatusBadRequest, errors.New("no MJPEG track found - only MJPEG format supported for streaming"))
		return
	}

	// Set up multipart MJPEG stream
	ctx.Header("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	ctx.Header("Cache-Control", "no-cache")
	ctx.Header("Connection", "close")

	// Create reader
	reader := &stream.Reader{
		Parent: a,
	}

	done := make(chan struct{})
	defer close(done)

	// Register callback for MJPEG frames
	reader.OnData(videoMedia, mjpegFormat, func(u *unit.Unit) error {
		select {
		case <-done:
			return errors.New("stream closed")
		case <-ctx.Request.Context().Done():
			return errors.New("client disconnected")
		default:
		}

		if payload, ok := u.Payload.(unit.PayloadMJPEG); ok {
			// Write MJPEG frame in multipart format
			boundary := fmt.Sprintf("--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(payload))
			if _, err := ctx.Writer.Write([]byte(boundary)); err != nil {
				return err
			}
			if _, err := ctx.Writer.Write(payload); err != nil {
				return err
			}
			if _, err := ctx.Writer.Write([]byte("\r\n")); err != nil {
				return err
			}
			ctx.Writer.Flush()
		}

		return nil
	})

	// Add reader to stream
	st.AddReader(reader)
	defer st.RemoveReader(reader)

	a.Log(logger.Info, "Started MJPEG stream for path: %s", pathName)

	// Wait for client disconnect or error
	select {
	case <-ctx.Request.Context().Done():
		a.Log(logger.Info, "Client disconnected from MJPEG stream: %s", pathName)
	case err := <-reader.Error():
		if err != nil && err.Error() != "terminated" {
			a.Log(logger.Warn, "MJPEG stream error: %v", err)
		}
	}
}

// h264ToJPEGWithFFmpeg is a helper that uses FFmpeg to convert H264 to JPEG
// This bridges the gap between native streaming and FFmpeg conversion
func (a *APIV2) h264ToJPEGWithFFmpeg(h264Data []byte) ([]byte, error) {
	// This would require:
	// 1. Write H264 data to temp file or pipe
	// 2. Use FFmpeg to decode and encode to JPEG
	// 3. Return JPEG bytes
	// For now, return not implemented
	return nil, errors.New("H264 to JPEG conversion not implemented")
}

// Alternative approach: If the source is RTSP, we can re-encode to MJPEG
// This would be done at the stream level, not per-request
