package recorder

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/recorder"
	"github.com/bluenviron/mediamtx/internal/stream"
)

type taskParent interface {
	logger.Writer
	OnTaskComplete(pathName string)
}

// Task represents a recording task.
type Task struct {
	ID             string
	PathName       string
	Format         string // "mp4" or "ts"
	RecordPath     string
	Timeout        time.Duration
	CustomFileName string
	PathManager    defs.APIPathManager
	Parent         taskParent
	IsAutoRecord   bool // 是否为自动录制（true=自动录制，false=API调用）

	// Runtime fields
	FileName     string
	FullPath     string
	RelativePath string
	FileURL      string
	EndTime      time.Time
	StartTime    time.Time

	recorder      *recorder.Recorder // For TS format
	mp4Recorder   *MP4Recorder       // For MP4 format
	retryCount    int                // 重试次数
	maxRetries    int                // 最大重试次数
	retryInterval time.Duration      // 重试间隔

	terminate      chan struct{}
	done           chan struct{}
	stopRequested  bool       // 标记是否有明确的外部 stop 调用
	recorderErrors chan error // 录制器错误通道
}

// Start starts the recording task.
func (t *Task) Start() error {
	t.StartTime = time.Now()
	t.EndTime = t.StartTime.Add(t.Timeout)
	t.maxRetries = 100                // 最大重试100次（基本上会一直重试直到timeout）
	t.retryInterval = 5 * time.Second // 重试间隔5秒
	t.retryCount = 0
	t.stopRequested = false
	t.recorderErrors = make(chan error, 10) // 缓冲通道

	// Generate filename
	if t.CustomFileName != "" {
		ext := t.Format
		if t.Format == "ts" {
			ext = "ts"
		} else {
			ext = "mp4"
		}
		t.FileName = t.CustomFileName
		if filepath.Ext(t.FileName) == "" {
			t.FileName = t.FileName + "." + ext
		}
	} else {
		t.FileName = generateFileName(t.Format)
	}

	// Generate paths
	t.FullPath, t.RelativePath = generateFilePath(t.RecordPath, t.FileName)

	// Create directory
	dir := filepath.Dir(t.FullPath)
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	t.terminate = make(chan struct{})
	t.done = make(chan struct{})

	// Start recording goroutine (will handle retries)
	go t.run()

	t.Log(logger.Info, "recording task created for path '%s', file: %s, format: %s, timeout: %v",
		t.PathName, t.FileName, t.Format, t.Timeout)

	return nil
}

// Stop stops the recording task.
func (t *Task) Stop() {
	t.Log(logger.Info, "stopping recording for path '%s' (external stop request)", t.PathName)
	t.stopRequested = true // 标记为外部停止请求
	close(t.terminate)
	<-t.done
}

// Log implements logger.Writer.
func (t *Task) Log(level logger.Level, format string, args ...interface{}) {
	t.Parent.Log(level, format, args...)
}

func (t *Task) run() {
	defer close(t.done)
	defer close(t.recorderErrors)

	// 计算总的结束时间
	absoluteEndTime := time.Now().Add(t.Timeout)

	for {
		// 检查是否已经超时
		if time.Now().After(absoluteEndTime) {
			t.Log(logger.Info, "recording timeout for path '%s'", t.PathName)
			t.Parent.OnTaskComplete(t.PathName)
			return
		}

		// 检查是否有外部停止请求
		select {
		case <-t.terminate:
			t.Log(logger.Info, "recording terminated for path '%s' (stop requested)", t.PathName)
			t.closeRecorders()
			return
		default:
		}

		// 尝试启动录制器
		err := t.startRecorder()
		if err != nil {
			t.Log(logger.Warn, "failed to start recorder for path '%s': %v", t.PathName, err)

			// 如果是外部停止请求，不重试
			if t.stopRequested {
				t.closeRecorders()
				return
			}

			// 检查是否还能重试
			if t.retryCount >= t.maxRetries {
				t.Log(logger.Error, "max retries reached for path '%s', giving up", t.PathName)
				t.Parent.OnTaskComplete(t.PathName)
				return
			}

			t.retryCount++
			t.Log(logger.Info, "will retry recording for path '%s' in %v (attempt %d/%d)",
				t.PathName, t.retryInterval, t.retryCount, t.maxRetries)

			// 等待重试间隔或超时
			select {
			case <-time.After(t.retryInterval):
				continue // 重试
			case <-t.terminate:
				t.Log(logger.Info, "recording terminated during retry wait for path '%s'", t.PathName)
				return
			}
		}

		// 录制器启动成功，等待其运行
		remainingTime := time.Until(absoluteEndTime)
		if remainingTime <= 0 {
			t.Log(logger.Info, "recording timeout for path '%s'", t.PathName)
			t.closeRecorders()
			t.Parent.OnTaskComplete(t.PathName)
			return
		}

		timeoutTimer := time.NewTimer(remainingTime)

		select {
		case <-timeoutTimer.C:
			// 正常超时结束
			t.Log(logger.Info, "recording completed (timeout) for path '%s'", t.PathName)
			t.closeRecorders()
			t.Parent.OnTaskComplete(t.PathName)
			timeoutTimer.Stop()
			return

		case err := <-t.recorderErrors:
			// 录制过程中出错
			timeoutTimer.Stop()
			t.closeRecorders()

			t.Log(logger.Error, "recorder error for path '%s': %v", t.PathName, err)

			// 如果是外部停止请求，不重试
			if t.stopRequested {
				return
			}

			// 检查是否还能重试
			if t.retryCount >= t.maxRetries {
				t.Log(logger.Error, "max retries reached for path '%s' after error, giving up", t.PathName)
				t.Parent.OnTaskComplete(t.PathName)
				return
			}

			t.retryCount++
			t.Log(logger.Info, "will retry recording for path '%s' after error in %v (attempt %d/%d)",
				t.PathName, t.retryInterval, t.retryCount, t.maxRetries)

			// 等待重试间隔
			select {
			case <-time.After(t.retryInterval):
				continue // 重试
			case <-t.terminate:
				t.Log(logger.Info, "recording terminated during error retry wait for path '%s'", t.PathName)
				return
			}

		case <-t.terminate:
			// 外部停止请求
			t.Log(logger.Info, "recording terminated for path '%s'", t.PathName)
			timeoutTimer.Stop()
			t.closeRecorders()
			return
		}
	}
}

// startRecorder 启动录制器
func (t *Task) startRecorder() error {
	// 检查路径是否准备好
	pathData, err := t.PathManager.APIPathsGet(t.PathName)
	if err != nil {
		return fmt.Errorf("path not found: %w", err)
	}

	if !pathData.Ready {
		return fmt.Errorf("path not ready (no publisher)")
	}

	// 获取流
	streamInterface, err := t.PathManager.GetStreamForRecording(t.PathName)
	if err != nil {
		return fmt.Errorf("failed to get stream: %w", err)
	}

	streamObj, ok := streamInterface.(*stream.Stream)
	if !ok {
		return fmt.Errorf("failed to cast stream object")
	}

	// 根据格式启动相应的录制器
	if t.Format == "mp4" {
		t.mp4Recorder = &MP4Recorder{
			Stream:   streamObj,
			FilePath: t.FullPath,
			Parent:   t,
			ErrorCh:  t.recorderErrors, // 传递错误通道
		}
		err = t.mp4Recorder.Initialize()
		if err != nil {
			t.mp4Recorder = nil
			return fmt.Errorf("failed to initialize MP4 recorder: %w", err)
		}
	} else {
		t.recorder = &recorder.Recorder{
			PathFormat:      t.FullPath,
			Format:          conf.RecordFormatMPEGTS,
			PartDuration:    t.Timeout,
			MaxPartSize:     conf.StringSize(100 * 1024 * 1024),
			SegmentDuration: t.Timeout,
			PathName:        t.PathName,
			Stream:          streamObj,
			Parent:          t,
		}
		t.recorder.Initialize()
	}

	t.Log(logger.Info, "recorder started successfully for path '%s'", t.PathName)
	return nil
}

// closeRecorders 关闭录制器
func (t *Task) closeRecorders() {
	if t.mp4Recorder != nil {
		t.mp4Recorder.Close()
		t.mp4Recorder = nil
	}
	if t.recorder != nil {
		t.recorder.Close()
		t.recorder = nil
	}
}
