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

func (cfg apiConfig) getObjectURL(key string) string {
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, key)
}

func getObjectKeyPrefix(aspectRatio string) string {
	if aspectRatio == landscape {
		return "landscape"
	}
	if aspectRatio == portrait {
		return "portrait"
	}
	return other
}

func getVideoAspectRatio(filePath string) (string, error) {
	type parameters struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
	}

	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)

	buf := bytes.NewBuffer(make([]byte, 0))
	cmd.Stdout = buf

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("error running ffprobe: %w", err)
	}

	params := parameters{}
	if err := json.Unmarshal(buf.Bytes(), &params); err != nil {
		return "", fmt.Errorf("error unmarshalling ffprobe output: %w", err)
	}
	if len(params.Streams) == 0 {
		return "", fmt.Errorf("no metadata found in video file")
	}

	aspectRatio := calcAspectRatio(params.Streams[0].Width, params.Streams[0].Height)
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
