package files

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

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
//
//	@Summary		Upload a small file
//	@Description	Direct upload for objects under 10MB. The created file is ready.
//	@Tags			files
//	@Accept			multipart/form-data
//	@Produce		json
//	@Security		BearerAuth
//	@Param			file			formData	file	true	"file bytes"
//	@Param			content_type	formData	string	false	"content type"
//	@Success		201				{object}	FileView
//	@Failure		401				{object}	httpx.APIError
//	@Failure		413				{object}	httpx.APIError
//	@Router			/v1/files [post]
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
//
//	@Summary		Initiate multipart upload
//	@Description	Create a pending file and return presigned part URLs.
//	@Tags			files
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			body	body		initiateUploadRequest	true	"upload metadata"
//	@Success		201		{object}	InitiateUploadResult
//	@Failure		401		{object}	httpx.APIError
//	@Failure		422		{object}	httpx.APIError
//	@Router			/v1/files/uploads [post]
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
//
//	@Summary		Complete multipart upload
//	@Description	Mark the file ready and rewrite size_bytes from the stored object.
//	@Tags			files
//	@Accept			json
//	@Produce		json
//	@Security		BearerAuth
//	@Param			upload_id	path		string					true	"upload id"
//	@Param			body		body		completeUploadRequest	true	"uploaded parts"
//	@Success		200			{object}	FileView
//	@Failure		401			{object}	httpx.APIError
//	@Failure		403			{object}	httpx.APIError
//	@Failure		404			{object}	httpx.APIError
//	@Router			/v1/files/uploads/{upload_id}/complete [post]
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
//
//	@Summary		Abort multipart upload
//	@Description	Remove the pending file record and abort storage multipart.
//	@Tags			files
//	@Security		BearerAuth
//	@Param			upload_id	path	string	true	"upload id"
//	@Success		204
//	@Failure		401	{object}	httpx.APIError
//	@Failure		403	{object}	httpx.APIError
//	@Failure		404	{object}	httpx.APIError
//	@Router			/v1/files/uploads/{upload_id} [delete]
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
//
//	@Summary		Get file metadata
//	@Description	Owner metadata includes status pending or ready.
//	@Tags			files
//	@Produce		json
//	@Security		BearerAuth
//	@Param			file_id	path		string	true	"file id"
//	@Success		200		{object}	FileView
//	@Failure		401		{object}	httpx.APIError
//	@Failure		403		{object}	httpx.APIError
//	@Failure		404		{object}	httpx.APIError
//	@Router			/v1/files/{file_id} [get]
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
//
//	@Summary		Delete a file
//	@Description	Remove the object and its metadata.
//	@Tags			files
//	@Security		BearerAuth
//	@Param			file_id	path	string	true	"file id"
//	@Success		204
//	@Failure		401	{object}	httpx.APIError
//	@Failure		403	{object}	httpx.APIError
//	@Failure		404	{object}	httpx.APIError
//	@Router			/v1/files/{file_id} [delete]
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

// IssueFileAccessToken handles POST /v1/files/:file_id/access-token.
//
//	@Summary		Issue file-access token
//	@Description	Ready files only. Pending files return FILE_NOT_READY.
//	@Tags			files
//	@Produce		json
//	@Security		BearerAuth
//	@Param			file_id	path		string	true	"file id"
//	@Success		200		{object}	FileAccessTokenResult
//	@Failure		401		{object}	httpx.APIError
//	@Failure		403		{object}	httpx.APIError
//	@Failure		404		{object}	httpx.APIError
//	@Failure		409		{object}	httpx.APIError
//	@Router			/v1/files/{file_id}/access-token [post]
func (h *Handler) IssueFileAccessToken(c *gin.Context) {
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

	result, err := h.service.IssueFileAccessToken(c.Request.Context(), ownerID, fileID)
	if err != nil {
		switch {
		case errors.Is(err, ErrFileNotFound):
			httpx.WriteError(c, httpx.StatusNotFound, "FILE_NOT_FOUND", "file not found")
		case errors.Is(err, ErrFileForbidden):
			httpx.WriteError(c, httpx.StatusForbidden, "FILE_FORBIDDEN", "you do not own this file")
		case errors.Is(err, ErrFileNotReady):
			httpx.WriteError(c, httpx.StatusConflict, "FILE_NOT_READY", "file is not ready")
		default:
			httpx.WriteError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to issue file access token")
		}
		return
	}

	httpx.WriteJSON(c, http.StatusOK, result)
}

// RequireFileAccessAuth validates file-access JWT for content downloads.
func (h *Handler) RequireFileAccessAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := bearerToken(c.GetHeader("Authorization"))
		if token == "" {
			httpx.WriteError(c, httpx.StatusUnauthorized, "AUTH_INVALID_TOKEN", "file access token is invalid or revoked")
			return
		}

		fileID, err := uuid.Parse(c.Param("file_id"))
		if err != nil {
			httpx.WriteError(c, httpx.StatusBadRequest, "VALIDATION_FAILED", "file_id is invalid")
			return
		}

		if err := h.service.ValidateFileAccessToken(c.Request.Context(), token, fileID); err != nil {
			httpx.WriteError(c, httpx.StatusUnauthorized, "AUTH_INVALID_TOKEN", "file access token is invalid or revoked")
			return
		}

		c.Next()
	}
}

// GetContent handles GET /v1/files/:file_id/content.
//
//	@Summary		Download file content
//	@Description	Range download with a file-access token. Pending files return FILE_NOT_READY.
//	@Tags			files
//	@Produce		application/octet-stream
//	@Security		BearerAuth
//	@Param			file_id	path	string	true	"file id"
//	@Param			Range	header	string	false	"byte range"
//	@Success		200
//	@Success		206
//	@Failure		401	{object}	httpx.APIError
//	@Failure		404	{object}	httpx.APIError
//	@Failure		409	{object}	httpx.APIError
//	@Router			/v1/files/{file_id}/content [get]
func (h *Handler) GetContent(c *gin.Context) {
	fileID, err := uuid.Parse(c.Param("file_id"))
	if err != nil {
		httpx.WriteError(c, httpx.StatusBadRequest, "VALIDATION_FAILED", "file_id is invalid")
		return
	}

	result, err := h.service.GetFileContent(c.Request.Context(), fileID, c.GetHeader("Range"))
	if err != nil {
		switch {
		case errors.Is(err, ErrFileNotFound):
			httpx.WriteError(c, httpx.StatusNotFound, "FILE_NOT_FOUND", "file not found")
		case errors.Is(err, ErrFileNotReady):
			httpx.WriteError(c, httpx.StatusConflict, "FILE_NOT_READY", "file is not ready")
		case errors.Is(err, ErrRangeNotSatisfiable):
			httpx.WriteError(c, http.StatusRequestedRangeNotSatisfiable, "RANGE_NOT_SATISFIABLE", "requested range is not satisfiable")
		default:
			httpx.WriteError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read file content")
		}
		return
	}
	defer result.Reader.Close()

	c.Header("Accept-Ranges", "bytes")
	c.Header("Content-Type", result.ContentType)

	if result.Range != nil {
		c.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", result.Range.Start, result.Range.End, result.SizeBytes))
		c.Header("Content-Length", fmt.Sprintf("%d", result.Range.End-result.Range.Start+1))
		c.Status(http.StatusPartialContent)
	} else {
		c.Header("Content-Length", fmt.Sprintf("%d", result.SizeBytes))
		c.Status(http.StatusOK)
	}

	if _, err := io.Copy(c.Writer, result.Reader); err != nil {
		return
	}
}

func bearerToken(header string) string {
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
