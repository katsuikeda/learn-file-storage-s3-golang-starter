package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	landscapeRatioWidth  = 16
	landscapeRatioHeight = 9
	portraitRatioWidth   = 9
	portraitRatioHeight  = 16
	landscape            = "16:9"
	portrait             = "9:16"
	other                = "other"
)

func detectFileMediaType(file multipart.File) (string, error) {
	// Conventionally, the first 512 bytes of a file are used to determine the file type
	const sniffLen = 512

	buf := make([]byte, sniffLen)
	n, err := io.ReadFull(file, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	// Reset the read pointer to the beginning of the file
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("failed to seek file: %w", err)
	}

	contentType := http.DetectContentType(buf[:n])
	return contentType, nil
}

func isMimeTypeMatch(headerType, actualType string) bool {
	return headerType == actualType
}

func (cfg apiConfig) ensureAssetsDir() error {
	if _, err := os.Stat(cfg.assetsRoot); os.IsNotExist(err) {
		return os.Mkdir(cfg.assetsRoot, 0755)
	}
	return nil
}

func mediaTypeToExt(mediaType string) (string, error) {
	// Speacial case for MP4 files since exts[0] for "video/mp4" will be ".m4v"
	if mediaType == "video/mp4" {
		return ".mp4", nil
	}

	exts, err := mime.ExtensionsByType(mediaType)
	if err != nil {
		return "", fmt.Errorf("error getting file extensions: %w", err)
	}
	if exts == nil {
		return "", fmt.Errorf("unsupported file type")
	}
	return exts[0], nil
}

func getAssetPath(mediaType string) (string, error) {
	ext, err := mediaTypeToExt(mediaType)
	if err != nil {
		return "", fmt.Errorf("error determing file extension: %w", err)
	}

	randomFileNameLen := 32
	randomFileNameBuffer := make([]byte, randomFileNameLen)
	if _, err := rand.Read(randomFileNameBuffer); err != nil {
		return "", fmt.Errorf("error generating random file name: %w", err)
	}

	randomFileName := base64.RawURLEncoding.EncodeToString(randomFileNameBuffer)

	return fmt.Sprintf("%s%s", randomFileName, ext), nil
}

func (cfg apiConfig) getAssetDiskPath(assetPath string) string {
	return filepath.Join(cfg.assetsRoot, assetPath)
}

func (cfg apiConfig) getAssetURL(assetPath string) string {
	return fmt.Sprintf("http://localhost:%s/assets/%s", cfg.port, assetPath)
}

//	func (cfg apiConfig) getObjectURL(key string) string {
//		return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, key)
//	}

func (cfg apiConfig) getObjectURL(key string) string {
	return fmt.Sprintf("%s,%s", cfg.s3Bucket, key)
}

func getObjectKeyPrefix(aspectRatio string) string {
	switch aspectRatio {
	case landscape:
		return "landscape"
	case portrait:
		return "portrait"
	default:
		return other
	}
}

func getVideoAspectRatio(filePath string) (string, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ffprobe error: %w", err)
	}

	var output struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		return "", fmt.Errorf("error parsing ffprobe output: %w", err)
	}
	if len(output.Streams) == 0 {
		return "", fmt.Errorf("no video streams found in ffprobe output")
	}

	aspectRatio := calcAspectRatio(output.Streams[0].Width, output.Streams[0].Height)
	return aspectRatio, nil
}

func calcAspectRatio(width int, height int) string {
	const tolerance = 0.01
	ratio := float64(width) / float64(height)
	landscapeRatio := float64(landscapeRatioWidth) / float64(landscapeRatioHeight)
	portraitRatio := float64(portraitRatioWidth) / float64(portraitRatioHeight)

	if math.Abs(ratio-landscapeRatio) < tolerance {
		return landscape
	}
	if math.Abs(ratio-portraitRatio) < tolerance {
		return portrait
	}

	return other
}

func processVideoForFastStart(filePath string) (string, error) {
	outputFilePath := filePath + ".processing"

	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", outputFilePath)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ffmpeg error: %w", err)
	}

	return outputFilePath, nil
}
