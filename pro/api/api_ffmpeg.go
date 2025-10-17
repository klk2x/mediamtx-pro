package api

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "image/png"

	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/gin-gonic/gin"
	ffmpeg "github.com/u2takey/ffmpeg-go"
)

// Subtitle represents a single subtitle entry in an SRT file
type Subtitle struct {
	Index     int
	StartTime time.Duration
	EndTime   time.Duration
	Text      string
}
type VideoMark struct {
	Seconds float64
	Content string
	URL     string
}

type ExportMP4Config struct {
	ID         string       `json:"id" form:"id"  binding:"required"`
	InputStart float64      `json:"inputStart" form:"inputStart"  binding:"required"`
	InputEnd   float64      `json:"inputEnd" form:"inputEnd"  binding:"required"`
	ResPath    string       `json:"resPath" form:"resPath"  binding:"required"`
	VideoMarks *[]VideoMark `json:"videoMarks" form:"videoMarks"`
}
type ExportMP4Body struct {
	ExportConfig []ExportMP4Config `json:"exportConfig" form:"exportConfig"  binding:"required"`
}

// FormatSRTTime formats a time.Duration as an SRT timestamp (e.g., "00:01:20,000")
func FormatSRTTime(t time.Duration) string {
	hours := int(t.Hours())
	minutes := int(t.Minutes()) % 60
	seconds := int(t.Seconds()) % 60
	milliseconds := int(t.Milliseconds()) % 1000

	return fmt.Sprintf("%02d:%02d:%02d,%03d", hours, minutes, seconds, milliseconds)
}

// WriteSRTFile writes a list of subtitles to an SRT file
func WriteSRTFile(filename string, subtitles []Subtitle) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	for _, sub := range subtitles {
		_, err := fmt.Fprintf(file, "%d\n", sub.Index)
		if err != nil {
			return err
		}

		_, err = fmt.Fprintf(file, "%s --> %s\n", FormatSRTTime(sub.StartTime), FormatSRTTime(sub.EndTime))
		if err != nil {
			return err
		}

		_, err = fmt.Fprintf(file, "%s\n\n", sub.Text)
		if err != nil {
			return err
		}
	}

	return nil
}

// CreateListFile generates a list.txt file with video file paths for FFmpeg concatenation
func CreateListFile(filename string, videoFiles []string) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	for _, videoFile := range videoFiles {
		_, err := fmt.Fprintf(file, "file '%s'\n", videoFile)
		if err != nil {
			return err
		}
	}

	return nil
}

type VideoInfo struct {
	Streams []struct {
		// CodecType string `json:"codec_type"`
		Width  int
		Height int
	} `json:"streams"`
}

func (a *APIV2) splitVideo(betweenStart float64, betweenEnd float64, baseOutName string, idx int, inputFile string, tmpFolderPath string, btArgs ffmpeg.KwArgs) string {
	splitbetweentime := strconv.FormatFloat(betweenStart, 'f', 2, 64) + "-" + strconv.FormatFloat(betweenEnd, 'f', 2, 64)
	outBetweenFileName := baseOutName + "_split_" + strconv.Itoa(idx) + "_0______" + splitbetweentime + ".mp4"
	outBetweenFile := filepath.Join(tmpFolderPath, outBetweenFileName)

	err := ffmpeg.Input(inputFile, btArgs).
		Output(outBetweenFile, ffmpeg.KwArgs{"c": "copy"}).OverWriteOutput().ErrorToStdOut().Run()

	if err != nil {
		a.Log(logger.Error, "splitVideo", err)
	}
	return outBetweenFile
}
func (a *APIV2) PathToURL(inputPath string) string {
	a.mutex.RLock()
	recordPath := a.Conf.PathDefaults.RecordPath
	a.mutex.RUnlock()

	recordPaths := strings.Split(recordPath, "%")
	baseWorkPath := ""
	if recordPaths[0] != "" {
		baseWorkPath = recordPaths[0]
	}

	newStr := strings.Replace(inputPath, baseWorkPath, "", -1)

	return newStr
}

func (a *APIV2) ExportMP4(ctx *gin.Context) {
	var editFileBody ExportMP4Body
	if err := ctx.ShouldBindJSON(&editFileBody); err != nil {
		ctx.AbortWithStatusJSON(http.StatusNotAcceptable, gin.H{"error": err.Error()})
		return
	}

	a.mutex.RLock()
	recordPath := a.Conf.PathDefaults.RecordPath
	a.mutex.RUnlock()

	recordPaths := strings.Split(recordPath, "%")
	baseWorkPath := ""
	if recordPaths[0] != "" {
		baseWorkPath = recordPaths[0]
	}
	var outfiles []string
	// var outerr error

	unixName := strconv.FormatInt(time.Now().Unix(), 10)
	tmpFolderPath := filepath.Join(baseWorkPath, "/tmp", "/", unixName)

	for idx, buildConfig := range editFileBody.ExportConfig {

		// idx 防止相同文件的截取拼接
		outfile, err := a.BuildMP4(idx, baseWorkPath, tmpFolderPath, buildConfig)

		if err != nil {
			a.Log(logger.Error, "BuildMP4:", err)
		} else {
			outfiles = append(outfiles, outfile)
		}

	}

	if len(outfiles) == 0 {
		defer ctx.JSON(http.StatusOK, gin.H{"success": false, "error": "outfiles=0"})
	} else if len(outfiles) == 1 {
		defer ctx.JSON(http.StatusOK, gin.H{
			"success": true,
			"result": gin.H{
				"outfile": a.PathToURL(outfiles[0]),
			},
		})
		// defer ctx.File(outfiles[0])
	} else if len(outfiles) > 1 {

		concatFilesName := unixName + "_concatfiles.txt"
		concatFiles := filepath.Join(tmpFolderPath, concatFilesName)
		errf := CreateListFile(concatFiles, outfiles)

		if errf != nil {
			a.Log(logger.Error, "CreateListFile", errf)
		}

		resultFileName := unixName + "_result.mp4"
		resultFile := filepath.Join(tmpFolderPath, resultFileName)

		outerr := ffmpeg.Input(concatFiles,
			ffmpeg.KwArgs{"f": "concat", "safe": 0},
		).Output(resultFile, ffmpeg.KwArgs{"c": "copy"}).OverWriteOutput().Run()

		if outerr != nil {
			defer ctx.JSON(http.StatusOK, gin.H{"success": false, "error": outerr.Error()})
		} else {
			defer ctx.JSON(http.StatusOK, gin.H{
				"success": true,
				"result": gin.H{
					"outfile": a.PathToURL(resultFile),
				},
			})
			// defer ctx.File(resultFile)
		}
	}
}

// go test examples/run3_test.go -v
func (a *APIV2) BuildMP4(idx int, baseWorkPath string, tmpFolderPath string, exportMP4Config ExportMP4Config) (resultFile string, err error) {
	inputStart := exportMP4Config.InputStart
	inputEnd := exportMP4Config.InputEnd
	//

	baseOutName := exportMP4Config.ID + "-" + strconv.Itoa(idx) + "-"

	// inputFile := "/Users/lele/Downloads/4k.mp4"
	// inputFile := "/Users/lele/WebstormProjects/2024/ffmpeg-go-master/examples/sample_data/4k.mp4"
	inputFile := filepath.Join(baseWorkPath, exportMP4Config.ResPath)

	a.Log(logger.Info, "splitVideo", "inputFile start rebuild:", inputFile, inputStart, inputEnd)

	if _, err := os.Stat(tmpFolderPath); os.IsNotExist(err) {
		// 必须分成两步
		// 先创建文件夹
		os.MkdirAll(tmpFolderPath, 0777)
		// 再修改权限
		os.Chmod(tmpFolderPath, 0777)
	}

	videoFiles := []string{}

	inputdata, errProbe := ffmpeg.Probe(inputFile)

	if errProbe != nil {
		a.Log(logger.Error, "get inputVideo error", errProbe)
		return resultFile, errProbe
	}

	vInfo := &VideoInfo{}
	err = json.Unmarshal([]byte(inputdata), vInfo)
	if err != nil {
		a.Log(logger.Error, "get inputVideo Parse error", err, inputdata)
		return resultFile, err
	}

	if exportMP4Config.VideoMarks != nil && len(*exportMP4Config.VideoMarks) > 0 {
		VideoMarks := *exportMP4Config.VideoMarks

		for _, mask := range VideoMarks {
			// 当第一个mask不包含全局开始
			// 从全局开始截图到第一个mask开始
			firstVideoMask := mask
			// isAppend := false
			firstStart := inputStart

			if inputStart == 0 && firstVideoMask.Seconds == 0 {
				break
			}
			if mask.Seconds > inputStart+2 {
				firstEnd := mask.Seconds - 2
				if inputStart > firstEnd {
					firstEnd = mask.Seconds
				}
				firstbtArgs := ffmpeg.KwArgs{"ss": inputStart, "to": firstEnd}
				outBetweenFile := a.splitVideo(firstStart, firstEnd, baseOutName, 0, inputFile, tmpFolderPath, firstbtArgs)
				videoFiles = append(videoFiles, outBetweenFile)
				break
			}
		}
		// 迭代结构体数组
		for idx, mask := range VideoMarks {

			// mask.Seconds = mask.Seconds - inputStart
			// 超出截取范围的蒙版
			if mask.Seconds < inputStart || mask.Seconds > inputEnd {
				continue
			}

			srtfilename := baseOutName + "_subtitle" + strconv.Itoa(idx) + ".srt"
			srtoutfile := filepath.Join(tmpFolderPath, srtfilename)

			subtitle := Subtitle{
				Index:     1,
				StartTime: 0 * time.Second,
				EndTime:   4 * time.Second,
				Text:      mask.Content,
			}

			err = WriteSRTFile(srtoutfile, []Subtitle{subtitle})

			if err != nil {
				a.Log(logger.Error, "Error writing SRT file:", err)
				return
			}

			start := mask.Seconds - 2
			end := mask.Seconds + 2
			if mask.Seconds <= 0 {
				start = 0
				end = mask.Seconds + 4
			}

			a.Log(logger.Info, "start split:", start, end)

			if mask.Content != "" {
				// 先添加字幕
				splitInput := ffmpeg.Input(inputFile, ffmpeg.KwArgs{"ss": start, "to": end})
				splittime := strconv.FormatFloat(start, 'f', 2, 64) + "-" + strconv.FormatFloat(end, 'f', 2, 64)
				outSplitFileName := baseOutName + "_split_" + strconv.Itoa(idx) + "_1______" + splittime + ".mp4"
				outSplitFile := filepath.Join(tmpFolderPath, outSplitFileName)

				err1 := splitInput.Output(outSplitFile, ffmpeg.KwArgs{"vf": "subtitles=" + srtoutfile, "c:a": "copy"}).
					OverWriteOutput().
					Run()

				if err1 != nil {
					a.Log(logger.Error, "output file", err)
				}

				// a.Log(logger.Info, "", vInfo.Streams[0].Height)
				// a.Log(logger.Info, "", vInfo.Streams[0].Width)

				// 再添加圈画
				if mask.URL != "" {
					if err1 == nil {
						outSplitFileName2 := baseOutName + "_split_" + strconv.Itoa(idx) + "_2______" + splittime + ".mp4"
						outSplitFile2 := filepath.Join(tmpFolderPath, outSplitFileName2)

						overlay := ffmpeg.Input(mask.URL).Filter("scale", ffmpeg.Args{strconv.Itoa(vInfo.Streams[0].Width) + ":" + strconv.Itoa(vInfo.Streams[0].Height)})

						err2 := ffmpeg.Filter(
							[]*ffmpeg.Stream{
								ffmpeg.Input(outSplitFile),
								overlay,
							}, "overlay", ffmpeg.Args{"0:0"}).
							Output(outSplitFile2).OverWriteOutput().ErrorToStdOut().Run()

						if err2 == nil {
							videoFiles = append(videoFiles, outSplitFile2)

						} else {
							a.Log(logger.Error, "mask.URL output file", err)
						}
					}

				} else {
					videoFiles = append(videoFiles, outSplitFile)
				}

				if idx < len(VideoMarks)-1 {

					nextMask := VideoMarks[idx+1]

					betweenStart := mask.Seconds + 2
					if mask.Seconds <= 0 {
						betweenStart = mask.Seconds + 4
					}
					betweenEnd := nextMask.Seconds - 2

					if betweenStart > betweenEnd {
						betweenStart = mask.Seconds
					}

					btArgs := ffmpeg.KwArgs{"ss": betweenStart}

					if idx <= len(VideoMarks) {
						btArgs["to"] = betweenEnd
					}
					outBetweenFile := a.splitVideo(betweenStart, betweenEnd, baseOutName, idx, inputFile, tmpFolderPath, btArgs)

					videoFiles = append(videoFiles, outBetweenFile)
				}

			}
		}

		lastIndex := len(VideoMarks) - 1

		if lastIndex >= 0 {
			lastVideoMask := VideoMarks[len(VideoMarks)-1]

			lastStart := lastVideoMask.Seconds + 2

			if lastVideoMask.Seconds <= 0 {
				lastStart = lastVideoMask.Seconds + 4
			}

			if lastStart > inputEnd {
				lastStart = lastVideoMask.Seconds
			}

			lastbtArgs := ffmpeg.KwArgs{"ss": lastStart, "to": inputEnd}
			outBetweenFile := a.splitVideo(lastStart, inputEnd, baseOutName, 0, inputFile, tmpFolderPath, lastbtArgs)
			videoFiles = append(videoFiles, outBetweenFile)
		}

	} else {
		lastbtArgs := ffmpeg.KwArgs{"ss": inputStart, "to": inputEnd}
		outBetweenFile := a.splitVideo(inputStart, inputEnd, baseOutName, 0, inputFile, tmpFolderPath, lastbtArgs)
		videoFiles = append(videoFiles, outBetweenFile)
	}

	concatFilesName := baseOutName + "_concatfiles.txt"
	concatFiles := filepath.Join(tmpFolderPath, concatFilesName)
	errf := CreateListFile(concatFiles, videoFiles)

	if errf != nil {
		a.Log(logger.Error, "CreateListFile", errf)
		return resultFile, err
	}

	resultFileName := baseOutName + "_result.mp4"
	resultFile = filepath.Join(tmpFolderPath, resultFileName)

	outerr := ffmpeg.Input(concatFiles,
		ffmpeg.KwArgs{"f": "concat", "safe": 0},
	).Output(resultFile, ffmpeg.KwArgs{"c": "copy"}).OverWriteOutput().Run()

	if outerr != nil {
		a.Log(logger.Error, "CreateListFile", outerr.Error())
		return resultFile, err
	} else {
		return resultFile, err
	}

}
