package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
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
