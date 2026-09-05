package files

import (
	"bytes"
	"context"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/Linjiahao666/server/internal/repository"
	"github.com/Linjiahao666/server/internal/storage"
)

// FileView is the public file metadata representation.
type FileView struct {
	ID          uuid.UUID `json:"id"`
	OwnerID     uuid.UUID `json:"owner_id"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	Filename    string    `json:"filename,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// InitiateUploadInput describes a multipart upload request.
type InitiateUploadInput struct {
	Filename       string
	ContentType    string
	SizeBytes      int64
	PartSizeBytes  int64
}

// InitiateUploadResult contains multipart upload session data.
type InitiateUploadResult struct {
	UploadID       uuid.UUID              `json:"upload_id"`
	FileID         uuid.UUID              `json:"file_id"`
	PartSizeBytes  int64                  `json:"part_size_bytes"`
	Parts          []storage.PresignedPart `json:"parts"`
}

// CompletePartInput identifies one uploaded part.
type CompletePartInput struct {
	PartNumber int
	ETag       string
}

// Service handles file workflows.
type Service struct {
	files   *repository.FileRepository
	uploads *repository.UploadRepository
	store   storage.ObjectStore
}

// NewService creates a file service.
func NewService(
	files *repository.FileRepository,
	uploads *repository.UploadRepository,
	store storage.ObjectStore,
) *Service {
	return &Service{
		files:   files,
		uploads: uploads,
		store:   store,
	}
}

// UploadSmall stores a file via direct upload.
func (s *Service) UploadSmall(
	ctx context.Context,
	ownerID uuid.UUID,
	filename, contentType string,
	reader io.Reader,
) (FileView, error) {
	data, err := readAllLimited(reader, MaxDirectUploadBytes)
	if err != nil {
		return FileView{}, err
	}

	if contentType == "" {
		contentType = "application/octet-stream"
	}

	fileID := uuid.New()
	created, err := s.files.Create(ctx, repository.File{
		ID:          fileID,
		OwnerID:     ownerID,
		ObjectKey:   objectKey(fileID),
		ContentType: contentType,
		SizeBytes:   int64(len(data)),
		Filename:    filename,
	})
	if err != nil {
		return FileView{}, err
	}

	if err := s.store.PutObject(ctx, created.ObjectKey, contentType, bytes.NewReader(data), int64(len(data))); err != nil {
		_ = s.files.Delete(ctx, created.ID)
		return FileView{}, err
	}

	return toFileView(created), nil
}

// InitiateUpload starts a multipart upload session.
func (s *Service) InitiateUpload(ctx context.Context, ownerID uuid.UUID, input InitiateUploadInput) (InitiateUploadResult, error) {
	if input.Filename == "" || input.ContentType == "" || input.SizeBytes <= 0 {
		return InitiateUploadResult{}, ErrValidationFailed
	}
	if input.SizeBytes <= MaxDirectUploadBytes {
		return InitiateUploadResult{}, ErrValidationFailed
	}

	partSize := input.PartSizeBytes
	if partSize <= 0 {
		partSize = DefaultPartSizeBytes
	}
	if partSize < MinPartSizeBytes {
		return InitiateUploadResult{}, ErrValidationFailed
	}
	if partCount(input.SizeBytes, partSize) > MaxMultipartParts {
		return InitiateUploadResult{}, ErrValidationFailed
	}

	fileID := uuid.New()
	key := objectKey(fileID)
	createdFile, err := s.files.Create(ctx, repository.File{
		ID:          fileID,
		OwnerID:     ownerID,
		ObjectKey:   key,
		ContentType: input.ContentType,
		SizeBytes:   input.SizeBytes,
		Filename:    input.Filename,
	})
	if err != nil {
		return InitiateUploadResult{}, err
	}

	minioUploadID, err := s.store.CreateMultipartUpload(ctx, key, input.ContentType)
	if err != nil {
		_ = s.files.Delete(ctx, createdFile.ID)
		return InitiateUploadResult{}, err
	}

	uploadID := uuid.New()
	createdUpload, err := s.uploads.Create(ctx, repository.Upload{
		ID:            uploadID,
		FileID:        createdFile.ID,
		OwnerID:       ownerID,
		MinioUploadID: minioUploadID,
		Status:        repository.UploadStatusPending,
	})
	if err != nil {
		_ = s.store.AbortMultipartUpload(ctx, key, minioUploadID)
		_ = s.files.Delete(ctx, createdFile.ID)
		return InitiateUploadResult{}, err
	}

	parts, err := s.store.PresignUploadParts(ctx, key, minioUploadID, partCount(input.SizeBytes, partSize), presignExpiry)
	if err != nil {
		_ = s.store.AbortMultipartUpload(ctx, key, minioUploadID)
		_ = s.uploads.Delete(ctx, createdUpload.ID)
		_ = s.files.Delete(ctx, createdFile.ID)
		return InitiateUploadResult{}, err
	}

	return InitiateUploadResult{
		UploadID:      createdUpload.ID,
		FileID:        createdFile.ID,
		PartSizeBytes: partSize,
		Parts:         parts,
	}, nil
}

// CompleteUpload finalizes a multipart upload.
func (s *Service) CompleteUpload(ctx context.Context, ownerID, uploadID uuid.UUID, parts []CompletePartInput) (FileView, error) {
	upload, err := s.uploads.FindByID(ctx, uploadID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return FileView{}, ErrUploadNotFound
		}
		return FileView{}, err
	}
	if upload.OwnerID != ownerID {
		return FileView{}, ErrFileForbidden
	}
	if upload.Status != repository.UploadStatusPending {
		return FileView{}, ErrUploadNotPending
	}
	if len(parts) == 0 {
		return FileView{}, ErrValidationFailed
	}

	file, err := s.files.FindByID(ctx, upload.FileID)
	if err != nil {
		return FileView{}, err
	}

	completedParts := make([]storage.CompletedPart, 0, len(parts))
	for _, part := range parts {
		completedParts = append(completedParts, storage.CompletedPart{
			PartNumber: part.PartNumber,
			ETag:       part.ETag,
		})
	}

	if err := s.store.CompleteMultipartUpload(ctx, file.ObjectKey, upload.MinioUploadID, completedParts); err != nil {
		return FileView{}, err
	}

	if err := s.uploads.UpdateStatus(ctx, upload.ID, repository.UploadStatusCompleted); err != nil {
		return FileView{}, err
	}

	return toFileView(file), nil
}

// AbortUpload cancels a pending multipart upload.
func (s *Service) AbortUpload(ctx context.Context, ownerID, uploadID uuid.UUID) error {
	upload, err := s.uploads.FindByID(ctx, uploadID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return ErrUploadNotFound
		}
		return err
	}
	if upload.OwnerID != ownerID {
		return ErrFileForbidden
	}
	if upload.Status != repository.UploadStatusPending {
		return ErrUploadNotPending
	}

	file, err := s.files.FindByID(ctx, upload.FileID)
	if err != nil {
		return err
	}

	if err := s.store.AbortMultipartUpload(ctx, file.ObjectKey, upload.MinioUploadID); err != nil {
		return err
	}

	if err := s.uploads.UpdateStatus(ctx, upload.ID, repository.UploadStatusAborted); err != nil {
		return err
	}
	_ = s.files.Delete(ctx, file.ID)
	return nil
}

// GetFile returns file metadata for the owner.
func (s *Service) GetFile(ctx context.Context, ownerID, fileID uuid.UUID) (FileView, error) {
	file, err := s.files.FindByID(ctx, fileID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return FileView{}, ErrFileNotFound
		}
		return FileView{}, err
	}
	if file.OwnerID != ownerID {
		return FileView{}, ErrFileForbidden
	}
	return toFileView(file), nil
}

// DeleteFile removes a file from storage and the database.
func (s *Service) DeleteFile(ctx context.Context, ownerID, fileID uuid.UUID) error {
	file, err := s.files.FindByID(ctx, fileID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return ErrFileNotFound
		}
		return err
	}
	if file.OwnerID != ownerID {
		return ErrFileForbidden
	}

	pendingUpload, err := s.uploads.FindPendingByFileID(ctx, fileID)
	if err == nil {
		if err := s.store.AbortMultipartUpload(ctx, file.ObjectKey, pendingUpload.MinioUploadID); err != nil {
			return err
		}
		_ = s.uploads.UpdateStatus(ctx, pendingUpload.ID, repository.UploadStatusAborted)
	} else if !errors.Is(err, repository.ErrNotFound) {
		return err
	}

	if err := s.store.RemoveObject(ctx, file.ObjectKey); err != nil {
		return err
	}
	return s.files.Delete(ctx, file.ID)
}

func toFileView(file repository.File) FileView {
	return FileView{
		ID:          file.ID,
		OwnerID:     file.OwnerID,
		ContentType: file.ContentType,
		SizeBytes:   file.SizeBytes,
		Filename:    file.Filename,
		CreatedAt:   file.CreatedAt,
	}
}
