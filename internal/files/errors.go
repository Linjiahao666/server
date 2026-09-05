package files

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
)

const (
	MaxDirectUploadBytes = 10 * 1024 * 1024
	DefaultPartSizeBytes = 8 * 1024 * 1024
	MinPartSizeBytes     = 5 * 1024 * 1024
	MaxMultipartParts    = 10000
	presignExpiry        = 30 * time.Minute
)

// ErrFileTooLarge indicates the upload exceeds the direct upload limit.
var ErrFileTooLarge = errors.New("file too large")

// ErrFileForbidden indicates the user does not own the file.
var ErrFileForbidden = errors.New("file forbidden")

// ErrFileNotFound indicates the file does not exist.
var ErrFileNotFound = errors.New("file not found")

// ErrUploadNotFound indicates the upload session does not exist.
var ErrUploadNotFound = errors.New("upload not found")

// ErrUploadNotPending indicates the upload is not in pending state.
var ErrUploadNotPending = errors.New("upload not pending")

// ErrValidationFailed indicates request validation failed.
var ErrValidationFailed = errors.New("validation failed")

// ErrInvalidFileAccessToken indicates the file-access token is invalid or revoked.
var ErrInvalidFileAccessToken = errors.New("invalid file access token")

func objectKey(fileID uuid.UUID) string {
	return fmt.Sprintf("files/%s", fileID.String())
}

func partCount(sizeBytes, partSizeBytes int64) int {
	count := int(sizeBytes / partSizeBytes)
	if sizeBytes%partSizeBytes != 0 {
		count++
	}
	if count < 1 {
		return 1
	}
	return count
}

func readAllLimited(reader io.Reader, limit int64) ([]byte, error) {
	limited := io.LimitReader(reader, limit+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrFileTooLarge
	}
	return data, nil
}
