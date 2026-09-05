package files

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Linjiahao666/server/internal/auth"
	"github.com/Linjiahao666/server/internal/httpx"
)

// Handler exposes file HTTP endpoints.
type Handler struct {
	service *Service
}

// NewHandler creates a file handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

type initiateUploadRequest struct {
	Filename      string `json:"filename"`
	ContentType   string `json:"content_type"`
	SizeBytes     int64  `json:"size_bytes"`
	PartSizeBytes int64  `json:"part_size_bytes"`
}

type completeUploadRequest struct {
	Parts []completePartRequest `json:"parts"`
}

type completePartRequest struct {
	PartNumber int    `json:"part_number"`
	ETag       string `json:"etag"`
}

// UploadSmall handles POST /v1/files.
func (h *Handler) UploadSmall(c *gin.Context) {
	ownerID, ok := auth.UserIDFromContext(c)
	if !ok {
		httpx.WriteError(c, httpx.StatusUnauthorized, "AUTH_INVALID_TOKEN", "access token is invalid or revoked")
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		httpx.WriteError(c, httpx.StatusBadRequest, "VALIDATION_FAILED", "file is required")
		return
	}

	contentType := c.PostForm("content_type")
	if contentType == "" {
		contentType = fileHeader.Header.Get("Content-Type")
	}

	reader, err := fileHeader.Open()
	if err != nil {
		httpx.WriteError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read upload")
		return
	}
	defer reader.Close()

	result, err := h.service.UploadSmall(c.Request.Context(), ownerID, fileHeader.Filename, contentType, reader)
	if err != nil {
		switch {
		case errors.Is(err, ErrFileTooLarge):
			httpx.WriteError(c, httpx.StatusPayloadTooLarge, "FILE_TOO_LARGE", "use multipart upload for files over 10MB")
		default:
			httpx.WriteError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to upload file")
		}
		return
	}

	httpx.WriteJSON(c, httpx.StatusCreated, result)
}

// InitiateUpload handles POST /v1/files/uploads.
func (h *Handler) InitiateUpload(c *gin.Context) {
	ownerID, ok := auth.UserIDFromContext(c)
	if !ok {
		httpx.WriteError(c, httpx.StatusUnauthorized, "AUTH_INVALID_TOKEN", "access token is invalid or revoked")
		return
	}

	var req initiateUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteError(c, httpx.StatusUnprocessableEntity, "VALIDATION_FAILED", "request body is invalid")
		return
	}

	result, err := h.service.InitiateUpload(c.Request.Context(), ownerID, InitiateUploadInput{
		Filename:      req.Filename,
		ContentType:   req.ContentType,
		SizeBytes:     req.SizeBytes,
		PartSizeBytes: req.PartSizeBytes,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrValidationFailed):
			httpx.WriteError(c, httpx.StatusUnprocessableEntity, "VALIDATION_FAILED", "filename, content_type and size_bytes are required")
		default:
			httpx.WriteError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to initiate upload")
		}
		return
	}

	httpx.WriteJSON(c, httpx.StatusCreated, result)
}

// CompleteUpload handles POST /v1/files/uploads/:upload_id/complete.
func (h *Handler) CompleteUpload(c *gin.Context) {
	ownerID, ok := auth.UserIDFromContext(c)
	if !ok {
		httpx.WriteError(c, httpx.StatusUnauthorized, "AUTH_INVALID_TOKEN", "access token is invalid or revoked")
		return
	}

	uploadID, err := uuid.Parse(c.Param("upload_id"))
	if err != nil {
		httpx.WriteError(c, httpx.StatusBadRequest, "VALIDATION_FAILED", "upload_id is invalid")
		return
	}

	var req completeUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Parts) == 0 {
		httpx.WriteError(c, httpx.StatusBadRequest, "VALIDATION_FAILED", "parts are required")
		return
	}

	parts := make([]CompletePartInput, 0, len(req.Parts))
	for _, part := range req.Parts {
		parts = append(parts, CompletePartInput{
			PartNumber: part.PartNumber,
			ETag:       part.ETag,
		})
	}

	result, err := h.service.CompleteUpload(c.Request.Context(), ownerID, uploadID, parts)
	if err != nil {
		switch {
		case errors.Is(err, ErrUploadNotFound):
			httpx.WriteError(c, httpx.StatusNotFound, "FILE_NOT_FOUND", "upload not found")
		case errors.Is(err, ErrFileForbidden):
			httpx.WriteError(c, httpx.StatusForbidden, "FILE_FORBIDDEN", "you do not own this file")
		case errors.Is(err, ErrUploadNotPending):
			httpx.WriteError(c, httpx.StatusBadRequest, "VALIDATION_FAILED", "upload is not pending")
		case errors.Is(err, ErrValidationFailed):
			httpx.WriteError(c, httpx.StatusBadRequest, "VALIDATION_FAILED", "parts are required")
		default:
			httpx.WriteError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to complete upload")
		}
		return
	}

	httpx.WriteJSON(c, http.StatusOK, result)
}

// AbortUpload handles DELETE /v1/files/uploads/:upload_id.
func (h *Handler) AbortUpload(c *gin.Context) {
	ownerID, ok := auth.UserIDFromContext(c)
	if !ok {
		httpx.WriteError(c, httpx.StatusUnauthorized, "AUTH_INVALID_TOKEN", "access token is invalid or revoked")
		return
	}

	uploadID, err := uuid.Parse(c.Param("upload_id"))
	if err != nil {
		httpx.WriteError(c, httpx.StatusBadRequest, "VALIDATION_FAILED", "upload_id is invalid")
		return
	}

	if err := h.service.AbortUpload(c.Request.Context(), ownerID, uploadID); err != nil {
		switch {
		case errors.Is(err, ErrUploadNotFound):
			httpx.WriteError(c, httpx.StatusNotFound, "FILE_NOT_FOUND", "upload not found")
		case errors.Is(err, ErrFileForbidden):
			httpx.WriteError(c, httpx.StatusForbidden, "FILE_FORBIDDEN", "you do not own this file")
		case errors.Is(err, ErrUploadNotPending):
			httpx.WriteError(c, httpx.StatusBadRequest, "VALIDATION_FAILED", "upload is not pending")
		default:
			httpx.WriteError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to abort upload")
		}
		return
	}

	httpx.NoContent(c, httpx.StatusNoContent)
}

// GetFile handles GET /v1/files/:file_id.
func (h *Handler) GetFile(c *gin.Context) {
	ownerID, ok := auth.UserIDFromContext(c)
	if !ok {
		httpx.WriteError(c, httpx.StatusUnauthorized, "AUTH_INVALID_TOKEN", "access token is invalid or revoked")
		return
	}

	fileID, err := uuid.Parse(c.Param("file_id"))
	if err != nil {
		httpx.WriteError(c, httpx.StatusBadRequest, "VALIDATION_FAILED", "file_id is invalid")
		return
	}

	result, err := h.service.GetFile(c.Request.Context(), ownerID, fileID)
	if err != nil {
		switch {
		case errors.Is(err, ErrFileNotFound):
			httpx.WriteError(c, httpx.StatusNotFound, "FILE_NOT_FOUND", "file not found")
		case errors.Is(err, ErrFileForbidden):
			httpx.WriteError(c, httpx.StatusForbidden, "FILE_FORBIDDEN", "you do not own this file")
		default:
			httpx.WriteError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load file")
		}
		return
	}

	httpx.WriteJSON(c, http.StatusOK, result)
}

// DeleteFile handles DELETE /v1/files/:file_id.
func (h *Handler) DeleteFile(c *gin.Context) {
	ownerID, ok := auth.UserIDFromContext(c)
	if !ok {
		httpx.WriteError(c, httpx.StatusUnauthorized, "AUTH_INVALID_TOKEN", "access token is invalid or revoked")
		return
	}

	fileID, err := uuid.Parse(c.Param("file_id"))
	if err != nil {
		httpx.WriteError(c, httpx.StatusBadRequest, "VALIDATION_FAILED", "file_id is invalid")
		return
	}

	if err := h.service.DeleteFile(c.Request.Context(), ownerID, fileID); err != nil {
		switch {
		case errors.Is(err, ErrFileNotFound):
			httpx.WriteError(c, httpx.StatusNotFound, "FILE_NOT_FOUND", "file not found")
		case errors.Is(err, ErrFileForbidden):
			httpx.WriteError(c, httpx.StatusForbidden, "FILE_FORBIDDEN", "you do not own this file")
		default:
			httpx.WriteError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to delete file")
		}
		return
	}

	httpx.NoContent(c, httpx.StatusNoContent)
}
