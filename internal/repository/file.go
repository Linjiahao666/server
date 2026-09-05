package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// File represents stored file metadata.
type File struct {
	ID          uuid.UUID
	OwnerID     uuid.UUID
	ObjectKey   string
	ContentType string
	SizeBytes   int64
	Filename    string
	CreatedAt   time.Time
}

// UploadStatus tracks multipart upload lifecycle.
type UploadStatus string

const (
	UploadStatusPending   UploadStatus = "pending"
	UploadStatusCompleted UploadStatus = "completed"
	UploadStatusAborted   UploadStatus = "aborted"
)

// Upload represents a multipart upload session.
type Upload struct {
	ID            uuid.UUID
	FileID        uuid.UUID
	OwnerID       uuid.UUID
	MinioUploadID string
	Status        UploadStatus
	CreatedAt     time.Time
}

// FileRepository persists file metadata.
type FileRepository struct {
	pool *pgxpool.Pool
}

// NewFileRepository creates a file repository.
func NewFileRepository(pool *pgxpool.Pool) *FileRepository {
	return &FileRepository{pool: pool}
}

// Create inserts a new file record.
func (r *FileRepository) Create(ctx context.Context, file File) (File, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO files (id, owner_id, object_key, content_type, size_bytes, filename)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, owner_id, object_key, content_type, size_bytes, filename, created_at
	`, file.ID, file.OwnerID, file.ObjectKey, file.ContentType, file.SizeBytes, file.Filename)

	var created File
	if err := row.Scan(
		&created.ID,
		&created.OwnerID,
		&created.ObjectKey,
		&created.ContentType,
		&created.SizeBytes,
		&created.Filename,
		&created.CreatedAt,
	); err != nil {
		return File{}, err
	}
	return created, nil
}

// FindByID loads a file by ID.
func (r *FileRepository) FindByID(ctx context.Context, id uuid.UUID) (File, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, owner_id, object_key, content_type, size_bytes, filename, created_at
		FROM files
		WHERE id = $1
	`, id)

	var file File
	if err := row.Scan(
		&file.ID,
		&file.OwnerID,
		&file.ObjectKey,
		&file.ContentType,
		&file.SizeBytes,
		&file.Filename,
		&file.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return File{}, ErrNotFound
		}
		return File{}, err
	}
	return file, nil
}

// Delete removes a file by ID.
func (r *FileRepository) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM files WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UploadRepository persists multipart upload sessions.
type UploadRepository struct {
	pool *pgxpool.Pool
}

// NewUploadRepository creates an upload repository.
func NewUploadRepository(pool *pgxpool.Pool) *UploadRepository {
	return &UploadRepository{pool: pool}
}

// Create inserts a new upload record.
func (r *UploadRepository) Create(ctx context.Context, upload Upload) (Upload, error) {
	row := r.pool.QueryRow(ctx, `
		INSERT INTO uploads (id, file_id, owner_id, minio_upload_id, status)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, file_id, owner_id, minio_upload_id, status, created_at
	`, upload.ID, upload.FileID, upload.OwnerID, upload.MinioUploadID, upload.Status)

	var created Upload
	if err := row.Scan(
		&created.ID,
		&created.FileID,
		&created.OwnerID,
		&created.MinioUploadID,
		&created.Status,
		&created.CreatedAt,
	); err != nil {
		return Upload{}, err
	}
	return created, nil
}

// FindByID loads an upload by ID.
func (r *UploadRepository) FindByID(ctx context.Context, id uuid.UUID) (Upload, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, file_id, owner_id, minio_upload_id, status, created_at
		FROM uploads
		WHERE id = $1
	`, id)

	var upload Upload
	if err := row.Scan(
		&upload.ID,
		&upload.FileID,
		&upload.OwnerID,
		&upload.MinioUploadID,
		&upload.Status,
		&upload.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Upload{}, ErrNotFound
		}
		return Upload{}, err
	}
	return upload, nil
}

// UpdateStatus sets the upload status.
func (r *UploadRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status UploadStatus) error {
	tag, err := r.pool.Exec(ctx, `UPDATE uploads SET status = $2 WHERE id = $1`, id, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// FindPendingByFileID loads a pending upload for a file.
func (r *UploadRepository) FindPendingByFileID(ctx context.Context, fileID uuid.UUID) (Upload, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, file_id, owner_id, minio_upload_id, status, created_at
		FROM uploads
		WHERE file_id = $1 AND status = $2
	`, fileID, UploadStatusPending)

	var upload Upload
	if err := row.Scan(
		&upload.ID,
		&upload.FileID,
		&upload.OwnerID,
		&upload.MinioUploadID,
		&upload.Status,
		&upload.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Upload{}, ErrNotFound
		}
		return Upload{}, err
	}
	return upload, nil
}

// Delete removes an upload by ID.
func (r *UploadRepository) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM uploads WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
