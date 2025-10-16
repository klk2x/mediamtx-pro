package api

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"runtime"

	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/disintegration/imaging"
)

// IsGrayscale checks if a given color is grayscale
func IsGrayscale(c color.Color) bool {
	r, g, b, _ := c.RGBA()

	// Normalize the values to 8-bit channels
	r8 := r >> 8
	g8 := g >> 8
	b8 := b >> 8

	// Define a threshold for the difference between R, G, B values to consider a pixel as grayscale
	const threshold = 10 // You can adjust this value

	return math.Abs(float64(r8-g8)) <= threshold && math.Abs(float64(r8-b8)) <= threshold
}

// AnalyzeImage checks if the image is grayscale or colorful
func AnalyzeImage(img image.Image) (int, float64) {
	bounds := img.Bounds()
	totalPixels := (bounds.Max.X - bounds.Min.X) * (bounds.Max.Y - bounds.Min.Y)
	var grayscaleCount, colorCount int

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel := img.At(x, y)
			if IsGrayscale(pixel) {
				grayscaleCount++
			} else {
				colorCount++
			}
		}
	}

	grayscalePercentage := (float64(grayscaleCount) / float64(totalPixels)) * 100
	colorPercentage := 100 - grayscalePercentage

	return grayscaleCount, colorPercentage
}

// bytesToImage converts a byte slice to an image.Image
func bytesToImage(imgBytes []byte) (image.Image, string, error) {
	imgReader := bytes.NewReader(imgBytes)

	// Decode the image
	img, format, err := image.Decode(imgReader)
	if err != nil {
		return nil, "", err
	}

	return img, format, nil
}

// Check if a color is close to digestive tract colors (simplified)
func isDigestiveTractColor(c color.Color) bool {
	r, g, b, _ := c.RGBA()

	// Convert to 8-bit values
	r8 := float64(r >> 8)
	g8 := float64(g >> 8)
	b8 := float64(b >> 8)

	// Define RGB ranges for digestive tract colors (can be adjusted)
	// General ranges for shades of pink, red, light brown, and gray

	// 消化系统内壁颜色范围示例：
	// 器官或部分	红 (R)	绿 (G)	蓝 (B)
	// 胃壁	180-255	70-180	50-140
	// 小肠内壁	160-240	60-150	40-130
	// 大肠内壁	150-230	50-140	30-120
	// 食道黏膜	200-255	70-160	50-130
	// 直肠/肛门内壁	170-240	60-140	40-120
	minR, maxR := 130.0, 240.0
	minG, maxG := 60.0, 160.0
	minB, maxB := 40.0, 130.0

	// Check if the color falls within the digestive tract color range
	if r8 >= minR && r8 <= maxR && g8 >= minG && g8 <= maxG && b8 >= minB && b8 <= maxB {
		return true
	}
	return false
}

// Check if a color is close to human skin tones
func isSkinColor(c color.Color) bool {
	r, g, b, _ := c.RGBA()

	// Convert to 8-bit values
	r8 := float64(r >> 8)
	g8 := float64(g >> 8)
	b8 := float64(b >> 8)

	// Define RGB ranges for skin tones (can be adjusted for different skin colors)
	// 中色皮肤	130-200	100-170	85-130
	minR, maxR := 130.0, 200.0
	minG, maxG := 100.0, 170.0
	minB, maxB := 85.0, 130.0

	// Check if the color falls within the skin color range
	if r8 >= minR && r8 <= maxR && g8 >= minG && g8 <= maxG && b8 >= minB && b8 <= maxB {
		return true
	}
	return false
}

// Process image and count digestive tract and skin-like pixels
func processImage(img image.Image) (int, int, int) {
	// 缩小到 100x100，减少像素数量
	smallImg := imaging.Resize(img, 100, 100, imaging.Lanczos)

	digestiveTractColorCount := 0
	skinColorCount := 0
	bounds := smallImg.Bounds()

	totalPixels := (bounds.Max.X - bounds.Min.X) * (bounds.Max.Y - bounds.Min.Y)

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixel := smallImg.At(x, y)
			if isDigestiveTractColor(pixel) {
				digestiveTractColorCount++
			}
		}
	}
	return digestiveTractColorCount, skinColorCount, totalPixels
}

func (a *APIV2) isGrayscale(name string) (int, error) {
	snapshotReq := apiV2SnapshotReq{
		Name: name,
	}
	imgBytes, _, err := a.snapshotRequest(snapshotReq)
	if err != nil {
		return 0, err
	}

	// Convert []byte to image.Image
	img, _, err2 := bytesToImage(imgBytes)
	if err2 != nil {
		a.Log(logger.Info, "Error converting bytes to image:", err2)
		return 0, err2
	}

	grayscalePercentage, colorPercentage := AnalyzeImage(img)

	a.Log(logger.Info, "Grayscale Pixels: %.2f%%\n", grayscalePercentage)
	a.Log(logger.Info, "Colorful Pixels: %.2f%%\n", colorPercentage)

	return grayscalePercentage, nil
}

// IsColorful 高效版：计算彩色占比
func (a *APIV2) IsColorful(name string) (int, error) {
	// 获取图片数据
	snapshotReq := apiV2SnapshotReq{Name: name}
	imgBytes, _, err := a.snapshotRequest(snapshotReq)
	if err != nil {
		return 0, err
	}

	// 解码 JPEG 为 image.Image
	img, err := bytesToImageEfficient(imgBytes)
	if err != nil {
		a.Log(logger.Info, "Error converting bytes to image:", err)
		return 0, err
	}

	// 缩小图片以减少像素数量
	resized := resizeImage(img, 100, 100) // 缩小到 100x100

	// 处理像素
	digestivePixels, skinPixels, totalPixels := processImageSample(resized, 8) // 每隔 2 像素采样一次

	// 丢掉引用，帮助 GC
	img = nil
	resized = nil
	runtime.GC() // 可选，高频调用时可注释

	// 计算百分比
	colorPercentage := (float64(digestivePixels+skinPixels) / float64(totalPixels)) * 100
	return int(math.Round(colorPercentage)), nil
}

// bytesToImageEfficient 解码 JPEG 数据
func bytesToImageEfficient(imgBytes []byte) (image.Image, error) {
	imgReader := bytes.NewReader(imgBytes)
	img, err := jpeg.Decode(imgReader)
	if err != nil {
		return nil, err
	}
	return img, nil
}

// resizeImage 将图片缩放到指定宽高
func resizeImage(src image.Image, width, height int) image.Image {
	bounds := src.Bounds()
	scaleX := float64(width) / float64(bounds.Dx())
	scaleY := float64(height) / float64(bounds.Dy())

	dst := image.NewRGBA(image.Rect(0, 0, width, height))

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			srcX := int(float64(x) / scaleX)
			srcY := int(float64(y) / scaleY)
			dst.Set(x, y, src.At(srcX, srcY))
		}
	}
	return dst
}

// processImageSample 按步长采样像素计算彩色
func processImageSample(img image.Image, step int) (int, int, int) {
	digestiveTractColorCount := 0
	skinColorCount := 0
	bounds := img.Bounds()

	totalPixels := ((bounds.Max.X - bounds.Min.X) / step) * ((bounds.Max.Y - bounds.Min.Y) / step)

	for y := bounds.Min.Y; y < bounds.Max.Y; y += step {
		for x := bounds.Min.X; x < bounds.Max.X; x += step {
			pixel := img.At(x, y)
			if isDigestiveTractColor(pixel) {
				digestiveTractColorCount++
			}
			// if isSkinColor(pixel) {
			//     skinColorCount++
			// }
		}
	}
	return digestiveTractColorCount, skinColorCount, totalPixels
}
