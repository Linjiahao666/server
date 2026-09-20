package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	jwtmanager "github.com/Linjiahao666/server/internal/jwt"
)

type fileResponse struct {
	ID          string `json:"id"`
	OwnerID     string `json:"owner_id"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	Filename    string `json:"filename"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
}

type initiateUploadResponse struct {
	UploadID      string `json:"upload_id"`
	FileID        string `json:"file_id"`
	PartSizeBytes int64  `json:"part_size_bytes"`
	Parts         []struct {
		PartNumber int    `json:"part_number"`
		UploadURL  string `json:"upload_url"`
	} `json:"parts"`
}

func TestFileFlow(t *testing.T) {
	ctx := context.Background()
	env := setupTestServer(ctx, t)
	defer env.Cleanup()

	password := "password123"
	userATokens := registerAndLogin(t, env, fmt.Sprintf("file_user_a_%d", time.Now().UnixNano()), password)
	userBTokens := registerAndLogin(t, env, fmt.Sprintf("file_user_b_%d", time.Now().UnixNano()), password)

	smallData := []byte("small-image-content")
	uploadResp := doMultipartRequest(t, env.Client, env.Router, "/v1/files", "file", "photo.jpg", "image/jpeg", smallData, userATokens.AccessToken)
	require.Equal(t, http.StatusCreated, uploadResp.StatusCode)

	var uploaded fileResponse
	decodeJSON(t, uploadResp, &uploaded)
	require.Equal(t, "image/jpeg", uploaded.ContentType)
	require.Equal(t, int64(len(smallData)), uploaded.SizeBytes)
	require.Equal(t, "photo.jpg", uploaded.Filename)
	require.Equal(t, "ready", uploaded.Status)
	require.NotEmpty(t, uploaded.ID)

	metaResp := doRequest(t, env.Client, env.Router, http.MethodGet, "/v1/files/"+uploaded.ID, nil, userATokens.AccessToken)
	require.Equal(t, http.StatusOK, metaResp.StatusCode)

	var metadata fileResponse
	decodeJSON(t, metaResp, &metadata)
	require.Equal(t, uploaded.ID, metadata.ID)
	require.Equal(t, uploaded.SizeBytes, metadata.SizeBytes)
	require.Equal(t, "ready", metadata.Status)

	forbiddenResp := doRequest(t, env.Client, env.Router, http.MethodGet, "/v1/files/"+uploaded.ID, nil, userBTokens.AccessToken)
	require.Equal(t, http.StatusForbidden, forbiddenResp.StatusCode)
	var forbiddenErr apiError
	decodeJSON(t, forbiddenResp, &forbiddenErr)
	require.Equal(t, "FILE_FORBIDDEN", forbiddenErr.Error.Code)

	tooLargeData := bytes.Repeat([]byte("a"), 10*1024*1024+1)
	tooLargeResp := doMultipartRequest(t, env.Client, env.Router, "/v1/files", "file", "large.bin", "application/octet-stream", tooLargeData, userATokens.AccessToken)
	require.Equal(t, http.StatusRequestEntityTooLarge, tooLargeResp.StatusCode)
	var tooLargeErr apiError
	decodeJSON(t, tooLargeResp, &tooLargeErr)
	require.Equal(t, "FILE_TOO_LARGE", tooLargeErr.Error.Code)

	partSize := int64(8 * 1024 * 1024)
	totalSize := partSize + 1024
	partOne := bytes.Repeat([]byte("1"), int(partSize))
	partTwo := bytes.Repeat([]byte("2"), 1024)

	initiateBody, _ := json.Marshal(map[string]interface{}{
		"filename":     "video.mp4",
		"content_type": "video/mp4",
		"size_bytes":   totalSize,
	})
	initiateResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/files/uploads", initiateBody, userATokens.AccessToken)
	require.Equal(t, http.StatusCreated, initiateResp.StatusCode)

	var initiated initiateUploadResponse
	decodeJSON(t, initiateResp, &initiated)
	require.Equal(t, partSize, initiated.PartSizeBytes)
	require.Len(t, initiated.Parts, 2)

	pendingMetaResp := doRequest(t, env.Client, env.Router, http.MethodGet, "/v1/files/"+initiated.FileID, nil, userATokens.AccessToken)
	require.Equal(t, http.StatusOK, pendingMetaResp.StatusCode)
	var pendingMeta fileResponse
	decodeJSON(t, pendingMetaResp, &pendingMeta)
	require.Equal(t, "pending", pendingMeta.Status)

	pendingTokenResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/files/"+initiated.FileID+"/access-token", nil, userATokens.AccessToken)
	require.Equal(t, http.StatusConflict, pendingTokenResp.StatusCode)
	var pendingTokenErr apiError
	decodeJSON(t, pendingTokenResp, &pendingTokenErr)
	require.Equal(t, "FILE_NOT_READY", pendingTokenErr.Error.Code)

	meResp := doRequest(t, env.Client, env.Router, http.MethodGet, "/v1/auth/me", nil, userATokens.AccessToken)
	require.Equal(t, http.StatusOK, meResp.StatusCode)
	var me userResponse
	decodeJSON(t, meResp, &me)
	ownerID, err := uuid.Parse(me.ID)
	require.NoError(t, err)
	pendingFileID, err := uuid.Parse(initiated.FileID)
	require.NoError(t, err)
	pendingAccess, _, _, err := env.JWT.IssueFileAccessToken(ownerID, pendingFileID)
	require.NoError(t, err)
	pendingContentResp := doContentRequest(t, env.Router, initiated.FileID, pendingAccess, "")
	require.Equal(t, http.StatusConflict, pendingContentResp.StatusCode)
	var pendingContentErr apiError
	decodeJSON(t, pendingContentResp, &pendingContentErr)
	require.Equal(t, "FILE_NOT_READY", pendingContentErr.Error.Code)

	pendingForbiddenResp := doRequest(t, env.Client, env.Router, http.MethodGet, "/v1/files/"+initiated.FileID, nil, userBTokens.AccessToken)
	require.Equal(t, http.StatusForbidden, pendingForbiddenResp.StatusCode)
	var pendingForbiddenErr apiError
	decodeJSON(t, pendingForbiddenResp, &pendingForbiddenErr)
	require.Equal(t, "FILE_FORBIDDEN", pendingForbiddenErr.Error.Code)

	pendingCrossTokenResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/files/"+initiated.FileID+"/access-token", nil, userBTokens.AccessToken)
	require.Equal(t, http.StatusForbidden, pendingCrossTokenResp.StatusCode)
	var pendingCrossTokenErr apiError
	decodeJSON(t, pendingCrossTokenResp, &pendingCrossTokenErr)
	require.Equal(t, "FILE_FORBIDDEN", pendingCrossTokenErr.Error.Code)

	etagOne := uploadPart(t, env.Client, initiated.Parts[0].UploadURL, partOne)
	etagTwo := uploadPart(t, env.Client, initiated.Parts[1].UploadURL, partTwo)

	completeBody, _ := json.Marshal(map[string]interface{}{
		"parts": []map[string]interface{}{
			{"part_number": 1, "etag": etagOne},
			{"part_number": 2, "etag": etagTwo},
		},
	})
	completeResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/files/uploads/"+initiated.UploadID+"/complete", completeBody, userATokens.AccessToken)
	require.Equal(t, http.StatusOK, completeResp.StatusCode)

	var completed fileResponse
	decodeJSON(t, completeResp, &completed)
	require.Equal(t, initiated.FileID, completed.ID)
	require.Equal(t, totalSize, completed.SizeBytes)
	require.Equal(t, "video/mp4", completed.ContentType)
	require.Equal(t, "ready", completed.Status)

	multipartMetaResp := doRequest(t, env.Client, env.Router, http.MethodGet, "/v1/files/"+completed.ID, nil, userATokens.AccessToken)
	require.Equal(t, http.StatusOK, multipartMetaResp.StatusCode)
	var multipartMeta fileResponse
	decodeJSON(t, multipartMetaResp, &multipartMeta)
	require.Equal(t, "ready", multipartMeta.Status)
	require.Equal(t, totalSize, multipartMeta.SizeBytes)

	readyTokenResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/files/"+completed.ID+"/access-token", nil, userATokens.AccessToken)
	require.Equal(t, http.StatusOK, readyTokenResp.StatusCode)
	var readyAccess fileAccessTokenResponse
	decodeJSON(t, readyTokenResp, &readyAccess)
	require.Equal(t, "Bearer", readyAccess.TokenType)
	require.Equal(t, 300, readyAccess.ExpiresIn)
	claims, err := env.JWT.ParseFileAccessToken(readyAccess.AccessToken)
	require.NoError(t, err)
	require.Contains(t, claims.Audience, jwtmanager.FileAccessAudience)

	rangeResp := doContentRequest(t, env.Router, completed.ID, readyAccess.AccessToken, "bytes=0-9")
	require.Equal(t, http.StatusPartialContent, rangeResp.StatusCode)
	require.Equal(t, fmt.Sprintf("bytes 0-9/%d", totalSize), rangeResp.Header.Get("Content-Range"))

	abortBody, _ := json.Marshal(map[string]interface{}{
		"filename":     "abort.mp4",
		"content_type": "video/mp4",
		"size_bytes":   totalSize,
	})
	abortInitiateResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/files/uploads", abortBody, userATokens.AccessToken)
	require.Equal(t, http.StatusCreated, abortInitiateResp.StatusCode)

	var abortInitiated initiateUploadResponse
	decodeJSON(t, abortInitiateResp, &abortInitiated)

	abortResp := doRequest(t, env.Client, env.Router, http.MethodDelete, "/v1/files/uploads/"+abortInitiated.UploadID, nil, userATokens.AccessToken)
	require.Equal(t, http.StatusNoContent, abortResp.StatusCode)

	abortedMetaResp := doRequest(t, env.Client, env.Router, http.MethodGet, "/v1/files/"+abortInitiated.FileID, nil, userATokens.AccessToken)
	require.Equal(t, http.StatusNotFound, abortedMetaResp.StatusCode)
	var abortedErr apiError
	decodeJSON(t, abortedMetaResp, &abortedErr)
	require.Equal(t, "FILE_NOT_FOUND", abortedErr.Error.Code)

	deleteResp := doRequest(t, env.Client, env.Router, http.MethodDelete, "/v1/files/"+uploaded.ID, nil, userATokens.AccessToken)
	require.Equal(t, http.StatusNoContent, deleteResp.StatusCode)

	deletedMetaResp := doRequest(t, env.Client, env.Router, http.MethodGet, "/v1/files/"+uploaded.ID, nil, userATokens.AccessToken)
	require.Equal(t, http.StatusNotFound, deletedMetaResp.StatusCode)
}

func TestInitiateUploadURLsUseConfiguredPublicHost(t *testing.T) {
	ctx := context.Background()
	publicHost := "files.example.test:9000"
	env := setupTestServerWithPublicMinio(ctx, t, publicHost)
	defer env.Cleanup()

	tokens := registerAndLogin(t, env, fmt.Sprintf("presign_host_%d", time.Now().UnixNano()), "password123")
	initiateBody, _ := json.Marshal(map[string]interface{}{
		"filename":     "video.mp4",
		"content_type": "video/mp4",
		"size_bytes":   10*1024*1024 + 1,
	})
	initiateResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/files/uploads", initiateBody, tokens.AccessToken)
	require.Equal(t, http.StatusCreated, initiateResp.StatusCode)

	var initiated initiateUploadResponse
	decodeJSON(t, initiateResp, &initiated)
	require.NotEmpty(t, initiated.Parts)
	for _, part := range initiated.Parts {
		parsed, err := url.Parse(part.UploadURL)
		require.NoError(t, err)
		require.Equal(t, publicHost, parsed.Host)
	}
}
