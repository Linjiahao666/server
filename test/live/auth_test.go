package live_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLogoutWithoutRefreshTokenRevokesRefresh(t *testing.T) {
	base := liveBaseURL(t)
	client := &http.Client{Timeout: 30 * time.Second}

	username := fmt.Sprintf("live_%d", time.Now().UnixNano())
	password := "password123"
	registerBody, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	registerResp := doLiveRequest(t, client, http.MethodPost, base+"/v1/auth/register", registerBody, "")
	require.Equal(t, http.StatusCreated, registerResp.StatusCode)

	loginBody, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	loginResp := doLiveRequest(t, client, http.MethodPost, base+"/v1/auth/login", loginBody, "")
	require.Equal(t, http.StatusOK, loginResp.StatusCode)

	var tokens tokenResponse
	decodeJSON(t, loginResp, &tokens)
	require.NotEmpty(t, tokens.AccessToken)
	require.NotEmpty(t, tokens.RefreshToken)

	logoutResp := doLiveRequest(t, client, http.MethodPost, base+"/v1/auth/logout", nil, tokens.AccessToken)
	require.Equal(t, http.StatusNoContent, logoutResp.StatusCode)

	refreshBody, _ := json.Marshal(map[string]string{
		"refresh_token": tokens.RefreshToken,
	})
	refreshResp := doLiveRequest(t, client, http.MethodPost, base+"/v1/auth/refresh", refreshBody, "")
	require.Equal(t, http.StatusUnauthorized, refreshResp.StatusCode)

	var body apiError
	decodeJSON(t, refreshResp, &body)
	require.Equal(t, "AUTH_INVALID_TOKEN", body.Error.Code)
}
