package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fileResponse struct {
	ID          string `json:"id"`
	OwnerID     string `json:"owner_id"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	Filename    string `json:"filename"`
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
	require.NotEmpty(t, uploaded.ID)

	metaResp := doRequest(t, env.Client, env.Router, http.MethodGet, "/v1/files/"+uploaded.ID, nil, userATokens.AccessToken)
	require.Equal(t, http.StatusOK, metaResp.StatusCode)

	var metadata fileResponse
	decodeJSON(t, metaResp, &metadata)
	require.Equal(t, uploaded.ID, metadata.ID)
	require.Equal(t, uploaded.SizeBytes, metadata.SizeBytes)

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

	multipartMetaResp := doRequest(t, env.Client, env.Router, http.MethodGet, "/v1/files/"+completed.ID, nil, userATokens.AccessToken)
	require.Equal(t, http.StatusOK, multipartMetaResp.StatusCode)

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

	deleteResp := doRequest(t, env.Client, env.Router, http.MethodDelete, "/v1/files/"+uploaded.ID, nil, userATokens.AccessToken)
	require.Equal(t, http.StatusNoContent, deleteResp.StatusCode)

	deletedMetaResp := doRequest(t, env.Client, env.Router, http.MethodGet, "/v1/files/"+uploaded.ID, nil, userATokens.AccessToken)
	require.Equal(t, http.StatusNotFound, deletedMetaResp.StatusCode)
}
