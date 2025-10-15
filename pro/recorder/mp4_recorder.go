package recorder

import (
	"bytes"
	"fmt"
	"os"
	"sync"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h265"
	"github.com/yapingcat/gomedia/go-mp4"

	"github.com/bluenviron/mediamtx/internal/codecprocessor"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/stream"
	"github.com/bluenviron/mediamtx/internal/unit"
)

// MP4Recorder records stream to standard MP4 format using gomedia library.
type MP4Recorder struct {
	Stream   *stream.Stream
	FilePath string
	Parent   logger.Writer
	ErrorCh  chan<- error // 错误通道，用于通知外部录制错误

	file         *os.File
	muxer        *mp4.Movmuxer
	reader       *stream.Reader
	videoTrack   uint32
	hasVideo     bool
	initialized  bool
	mutex        sync.Mutex
	dtsExtractor interface{}

	terminate chan struct{}
	done      chan struct{}
}

// Initialize initializes the MP4 recorder.
func (r *MP4Recorder) Initialize() error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if r.initialized {
		return nil
	}

	// Create MP4 file
	file, err := os.OpenFile(r.FilePath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed to create MP4 file: %w", err)
	}
	r.file = file

	// Create MP4 muxer
	muxer, err := mp4.CreateMp4Muxer(file)
	if err != nil {
		file.Close()
		return fmt.Errorf("failed to create MP4 muxer: %w", err)
	}
	r.muxer = muxer

	// Create stream reader
	r.reader = &stream.Reader{
		SkipBytesSent: true,
		Parent:        r,
	}

	// Setup callbacks for each media/format in the stream
	for _, media := range r.Stream.Desc.Medias {
		for _, forma := range media.Formats {
			r.setupTrack(media, forma)
		}
	}

	// Add reader to stream
	r.Stream.AddReader(r.reader)

	r.terminate = make(chan struct{})
	r.done = make(chan struct{})
	r.initialized = true

	r.Log(logger.Info, "MP4 recorder initialized for %s", r.FilePath)

	go r.run()

	return nil
}

// Close closes the MP4 recorder.
func (r *MP4Recorder) Close() {
	r.mutex.Lock()
	if !r.initialized {
		r.mutex.Unlock()
		return
	}
	r.mutex.Unlock()

	close(r.terminate)
	<-r.done

	r.mutex.Lock()
	defer r.mutex.Unlock()

	// Remove reader from stream
	if r.Stream != nil && r.reader != nil {
		r.Stream.RemoveReader(r.reader)
	}

	// Write MP4 trailer
	if r.muxer != nil {
		r.muxer.WriteTrailer()
	}

	// Close file
	if r.file != nil {
		r.file.Close()
	}

	r.initialized = false
	r.Log(logger.Info, "MP4 recorder closed for %s", r.FilePath)
}

// Log implements logger.Writer.
func (r *MP4Recorder) Log(level logger.Level, format string, args ...interface{}) {
	r.Parent.Log(level, "[mp4-recorder] "+format, args...)
}

func (r *MP4Recorder) run() {
	defer close(r.done)

	select {
	case err := <-r.reader.Error():
		r.Log(logger.Error, "reader error: %v", err)
		// 通知外部发生了错误
		if r.ErrorCh != nil {
			select {
			case r.ErrorCh <- err:
			default:
				// 通道已满或已关闭，忽略
			}
		}
	case <-r.terminate:
	}
}

func (r *MP4Recorder) setupTrack(media *description.Media, forma format.Format) {
	switch forma := forma.(type) {
	case *format.H264:
		sps, pps := forma.SafeParams()
		if sps == nil || pps == nil {
			sps = codecprocessor.H264DefaultSPS
			pps = codecprocessor.H264DefaultPPS
		}

		r.reader.OnData(media, forma, r.onH264)

	case *format.H265:
		vps, sps, pps := forma.SafeParams()
		if vps == nil || sps == nil || pps == nil {
			vps = codecprocessor.H265DefaultVPS
			sps = codecprocessor.H265DefaultSPS
			pps = codecprocessor.H265DefaultPPS
		}

		r.reader.OnData(media, forma, r.onH265)
	}
}

func (r *MP4Recorder) onH264(u *unit.Unit) error {
	if u.NilPayload() {
		return nil
	}

	r.mutex.Lock()
	defer r.mutex.Unlock()

	nalus := u.Payload.(unit.PayloadH264)

	// Check if we need to initialize DTS extractor
	if r.dtsExtractor == nil {
		// Check for IDR frame
		randomAccess := false
		for _, nalu := range nalus {
			typ := h264.NALUType(nalu[0] & 0x1F)
			if typ == h264.NALUTypeIDR {
				randomAccess = true
				break
			}
		}

		if !randomAccess {
			return nil
		}

		extractor := &h264.DTSExtractor{}
		extractor.Initialize()
		r.dtsExtractor = extractor
	}

	// Extract DTS
	dts, err := r.dtsExtractor.(*h264.DTSExtractor).Extract(nalus, u.PTS)
	if err != nil {
		return err
	}

	// Add video track if not added yet
	if !r.hasVideo {
		r.videoTrack = r.muxer.AddVideoTrack(mp4.MP4_CODEC_H264)
		r.hasVideo = true
	}

	// Convert NALUs to Annex-B format for gomedia
	var buf bytes.Buffer
	for _, nalu := range nalus {
		// Write start code
		buf.Write([]byte{0, 0, 0, 1})
		// Write NALU
		buf.Write(nalu)
	}

	// Write to muxer with PTS/DTS in milliseconds
	pts := uint64(u.PTS / 90) // Convert from 90kHz to milliseconds
	dtsMs := uint64(dts / 90)

	err = r.muxer.Write(r.videoTrack, buf.Bytes(), pts, dtsMs)
	if err != nil {
		r.Log(logger.Error, "failed to write H264: %v", err)
		return err
	}

	return nil
}

func (r *MP4Recorder) onH265(u *unit.Unit) error {
	if u.NilPayload() {
		return nil
	}

	r.mutex.Lock()
	defer r.mutex.Unlock()

	nalus := u.Payload.(unit.PayloadH265)

	// Check if we need to initialize DTS extractor
	if r.dtsExtractor == nil {
		// Check for IDR/CRA frame
		randomAccess := false
		for _, nalu := range nalus {
			typ := h265.NALUType((nalu[0] >> 1) & 0b111111)
			if typ == h265.NALUType_IDR_W_RADL || typ == h265.NALUType_IDR_N_LP || typ == h265.NALUType_CRA_NUT {
				randomAccess = true
				break
			}
		}

		if !randomAccess {
			return nil
		}

		extractor := &h265.DTSExtractor{}
		extractor.Initialize()
		r.dtsExtractor = extractor
	}

	// Extract DTS
	dts, err := r.dtsExtractor.(*h265.DTSExtractor).Extract(nalus, u.PTS)
	if err != nil {
		return err
	}

	// Add video track if not added yet
	if !r.hasVideo {
		r.videoTrack = r.muxer.AddVideoTrack(mp4.MP4_CODEC_H265)
		r.hasVideo = true
	}

	// Convert NALUs to Annex-B format for gomedia
	var buf bytes.Buffer
	for _, nalu := range nalus {
		// Write start code
		buf.Write([]byte{0, 0, 0, 1})
		// Write NALU
		buf.Write(nalu)
	}

	// Write to muxer with PTS/DTS in milliseconds
	pts := uint64(u.PTS / 90) // Convert from 90kHz to milliseconds
	dtsMs := uint64(dts / 90)

	err = r.muxer.Write(r.videoTrack, buf.Bytes(), pts, dtsMs)
	if err != nil {
		r.Log(logger.Error, "failed to write H265: %v", err)
		return err
	}

	return nil
}
