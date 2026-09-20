package live_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

func liveBaseURL(t *testing.T) string {
	t.Helper()

	base := strings.TrimRight(os.Getenv("LIVE_API_BASE"), "/")
	if base == "" {
		base = "http://127.0.0.1:8080"
	}

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(base + "/v1/auth/.well-known/jwks.json")
	if err != nil {
		t.Skipf("live server unavailable at %s: %v", base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Skipf("live server unavailable at %s: status %d", base, resp.StatusCode)
	}
	return base
}

func doLiveRequest(t *testing.T, client *http.Client, method, url string, body []byte, accessToken string) *http.Response {
	t.Helper()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, url, reader)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}

	resp, err := client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = resp.Body.Close()
	})
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, target interface{}) {
	t.Helper()
	defer resp.Body.Close()
	require.NoError(t, json.NewDecoder(resp.Body).Decode(target))
}
