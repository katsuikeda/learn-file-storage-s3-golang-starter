package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	const uploadLimit = 1 << 30 // 1 GB
	r.Body = http.MaxBytesReader(w, r.Body, uploadLimit)

	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Couldn't find video", err)
		return
	}
	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Not authorized to update this video", nil)
		return
	}

	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to parse form file", err)
		return
	}
	defer file.Close()

	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid Content-Type header", err)
		return
	}
	if mediaType != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Invalid file type, only MP4 is allowed", nil)
		return
	}

	actualMediaType, err := detectFileMediaType(file)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Unable to detect file type", err)
		return
	}
	if !isMimeTypeMatch(mediaType, actualMediaType) {
		respondWithError(w, http.StatusBadRequest, "Content-Type header doesn't match file type", nil)
		return
	}

	tempFile, err := os.CreateTemp("", "tubely-upload.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn not create temp file", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	if _, err := io.Copy(tempFile, file); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not write file to disk", err)
		return
	}

	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not reset file pointer", err)
		return
	}

	processedFilePath, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError,
			"Could not process video for fast start", fmt.Errorf("error processing video: %w", err))
		return
	}
	defer os.Remove(processedFilePath)

	processedFile, err := os.Open(processedFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't read processed file", fmt.Errorf("error reading processed file: %w", err))
	}
	defer processedFile.Close()

	// Reset the file pointer to be safe
	if _, err := processedFile.Seek(0, io.SeekStart); err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not reset processed file pointer", err)
		return
	}

	aspectRatio, err := getVideoAspectRatio(processedFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error determining aspect ratio", err)
		return
	}
	objPrefix := getObjectKeyPrefix(aspectRatio)

	objBaseKey, err := getAssetPath(mediaType)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not create object key", err)
		return
	}
	objKey := path.Join(objPrefix, objBaseKey)

	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      aws.String(cfg.s3Bucket),
		Key:         aws.String(objKey),
		Body:        processedFile,
		ContentType: aws.String(mediaType),
	})
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error uploading file to S3", err)
		return
	}

	videoURL := cfg.getObjectURL(objKey)
	video.VideoURL = &videoURL

	if err := cfg.db.UpdateVideo(video); err != nil {
		fmt.Printf("Debug 3 - DB Update error: %v\n", err)
		respondWithError(w, http.StatusInternalServerError, "Could not update video", err)
		return
	}

	signedVideo, err := cfg.dbVideoToSignedVideo(video)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Could not sign video URL", err)
		return
	}

	respondWithJSON(w, http.StatusOK, signedVideo)
}

func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
	// before uploading video, its videoURL should be nil
	// just return the video with no videoURL
	if video.VideoURL == nil {
		return video, nil
	}

	parts := strings.Split(*video.VideoURL, ",")
	if len(parts) != 2 {
		return video, nil
	}
	bucket, key := parts[0], parts[1]

	presignedURL, err := generatePresignedURL(cfg.s3Client, bucket, key, time.Minute*5)
	if err != nil {
		return video, err
	}

	video.VideoURL = &presignedURL
	return video, nil
}

func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
	if bucket == "" || key == "" {
		return "", errors.New("bucket and key cannot be empty")
	}

	presignClient := s3.NewPresignClient(s3Client)

	r, err := presignClient.PresignGetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(expireTime))
	if err != nil {
		return "", fmt.Errorf("error generating presigned URL: %w", err)
	}

	return r.URL, nil
}
