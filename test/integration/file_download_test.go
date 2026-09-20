package integration_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	jwtmanager "github.com/Linjiahao666/server/internal/jwt"
)

type fileAccessTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

func TestFileDownloadFlow(t *testing.T) {
	ctx := context.Background()
	env := setupTestServer(ctx, t)
	defer env.Cleanup()

	password := "password123"
	userATokens := registerAndLogin(t, env, fmt.Sprintf("download_user_a_%d", time.Now().UnixNano()), password)
	userBTokens := registerAndLogin(t, env, fmt.Sprintf("download_user_b_%d", time.Now().UnixNano()), password)

	fileData := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	uploadResp := doMultipartRequest(t, env.Client, env.Router, "/v1/files", "file", "clip.mp4", "video/mp4", fileData, userATokens.AccessToken)
	require.Equal(t, http.StatusCreated, uploadResp.StatusCode)

	var uploaded fileResponse
	decodeJSON(t, uploadResp, &uploaded)

	unauthorizedResp := doContentRequest(t, env.Router, uploaded.ID, "", "")
	require.Equal(t, http.StatusUnauthorized, unauthorizedResp.StatusCode)

	forbiddenResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/files/"+uploaded.ID+"/access-token", nil, userBTokens.AccessToken)
	require.Equal(t, http.StatusForbidden, forbiddenResp.StatusCode)

	tokenResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/files/"+uploaded.ID+"/access-token", nil, userATokens.AccessToken)
	require.Equal(t, http.StatusOK, tokenResp.StatusCode)

	var fileAccess fileAccessTokenResponse
	decodeJSON(t, tokenResp, &fileAccess)
	require.Equal(t, "Bearer", fileAccess.TokenType)
	require.Equal(t, 300, fileAccess.ExpiresIn)
	require.NotEmpty(t, fileAccess.AccessToken)
	claims, err := env.JWT.ParseFileAccessToken(fileAccess.AccessToken)
	require.NoError(t, err)
	require.Contains(t, claims.Audience, jwtmanager.FileAccessAudience)

	rangeHeader := "bytes=0-9"
	rangeResp := doContentRequest(t, env.Router, uploaded.ID, fileAccess.AccessToken, rangeHeader)
	require.Equal(t, http.StatusPartialContent, rangeResp.StatusCode)
	require.Equal(t, "bytes", rangeResp.Header.Get("Accept-Ranges"))
	require.Equal(t, fmt.Sprintf("bytes 0-9/%d", len(fileData)), rangeResp.Header.Get("Content-Range"))
	require.Equal(t, "10", rangeResp.Header.Get("Content-Length"))

	rangeBody, err := io.ReadAll(rangeResp.Body)
	require.NoError(t, err)
	require.Equal(t, fileData[:10], rangeBody)

	fullResp := doContentRequest(t, env.Router, uploaded.ID, fileAccess.AccessToken, "")
	require.Equal(t, http.StatusOK, fullResp.StatusCode)

	fullBody, err := io.ReadAll(fullResp.Body)
	require.NoError(t, err)
	require.Equal(t, fileData, fullBody)

	invalidTokenResp := doContentRequest(t, env.Router, uploaded.ID, userATokens.AccessToken, rangeHeader)
	require.Equal(t, http.StatusUnauthorized, invalidTokenResp.StatusCode)
}

func doContentRequest(t *testing.T, router *gin.Engine, fileID, fileAccessToken, rangeHeader string) *http.Response {
	req := httptest.NewRequest(http.MethodGet, "/v1/files/"+fileID+"/content", nil)
	if fileAccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+fileAccessToken)
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	resp := recorder.Result()
	t.Cleanup(func() {
		_ = resp.Body.Close()
	})
	return resp
}
