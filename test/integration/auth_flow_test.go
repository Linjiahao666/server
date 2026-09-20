package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	rediscontainer "github.com/testcontainers/testcontainers-go/modules/redis"
)

func TestAuthFlow(t *testing.T) {
	ctx := context.Background()
	env := setupTestServer(ctx, t)
	defer env.Cleanup()

	username := fmt.Sprintf("user_%d", time.Now().UnixNano())
	password := "password123"

	registerBody, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	registerResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/auth/register", registerBody, "")
	require.Equal(t, http.StatusCreated, registerResp.StatusCode)

	var registered userResponse
	decodeJSON(t, registerResp, &registered)
	require.Equal(t, username, registered.Username)

	conflictResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/auth/register", registerBody, "")
	require.Equal(t, http.StatusConflict, conflictResp.StatusCode)
	var conflictErr apiError
	decodeJSON(t, conflictResp, &conflictErr)
	require.Equal(t, "AUTH_USERNAME_TAKEN", conflictErr.Error.Code)

	loginBody, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	loginResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/auth/login", loginBody, "")
	require.Equal(t, http.StatusOK, loginResp.StatusCode)

	var loginTokens tokenResponse
	decodeJSON(t, loginResp, &loginTokens)
	require.Equal(t, "Bearer", loginTokens.TokenType)
	require.Equal(t, 900, loginTokens.ExpiresIn)
	require.NotEmpty(t, loginTokens.AccessToken)
	require.NotEmpty(t, loginTokens.RefreshToken)

	meResp := doRequest(t, env.Client, env.Router, http.MethodGet, "/v1/auth/me", nil, loginTokens.AccessToken)
	require.Equal(t, http.StatusOK, meResp.StatusCode)

	refreshBody, _ := json.Marshal(map[string]string{
		"refresh_token": loginTokens.RefreshToken,
	})
	refreshResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/auth/refresh", refreshBody, "")
	require.Equal(t, http.StatusOK, refreshResp.StatusCode)

	var refreshTokens tokenResponse
	decodeJSON(t, refreshResp, &refreshTokens)
	require.NotEmpty(t, refreshTokens.AccessToken)
	require.Equal(t, loginTokens.RefreshToken, refreshTokens.RefreshToken)

	jwksResp := doRequest(t, env.Client, env.Router, http.MethodGet, "/v1/auth/.well-known/jwks.json", nil, "")
	require.Equal(t, http.StatusOK, jwksResp.StatusCode)

	var jwks map[string]interface{}
	decodeJSON(t, jwksResp, &jwks)
	require.NotEmpty(t, jwks["keys"])

	logoutResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/auth/logout", nil, loginTokens.AccessToken)
	require.Equal(t, http.StatusNoContent, logoutResp.StatusCode)

	revokedResp := doRequest(t, env.Client, env.Router, http.MethodGet, "/v1/auth/me", nil, loginTokens.AccessToken)
	requireAuthInvalidToken(t, revokedResp)

	refreshAfterLogoutBody, _ := json.Marshal(map[string]string{
		"refresh_token": loginTokens.RefreshToken,
	})
	refreshAfterLogoutResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/auth/refresh", refreshAfterLogoutBody, "")
	requireAuthInvalidToken(t, refreshAfterLogoutResp)
}

func TestAuthLogoutIsolatesSessions(t *testing.T) {
	ctx := context.Background()
	env := setupTestServer(ctx, t)
	defer env.Cleanup()

	username := fmt.Sprintf("user_%d", time.Now().UnixNano())
	password := "password123"
	first := registerAndLogin(t, env, username, password)
	second := loginUser(t, env, username, password)

	logoutResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/auth/logout", nil, first.AccessToken)
	require.Equal(t, http.StatusNoContent, logoutResp.StatusCode)

	firstRefreshBody, _ := json.Marshal(map[string]string{
		"refresh_token": first.RefreshToken,
	})
	firstRefreshResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/auth/refresh", firstRefreshBody, "")
	requireAuthInvalidToken(t, firstRefreshResp)

	secondRefreshBody, _ := json.Marshal(map[string]string{
		"refresh_token": second.RefreshToken,
	})
	secondRefreshResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/auth/refresh", secondRefreshBody, "")
	require.Equal(t, http.StatusOK, secondRefreshResp.StatusCode)

	var secondTokens tokenResponse
	decodeJSON(t, secondRefreshResp, &secondTokens)
	require.Equal(t, second.RefreshToken, secondTokens.RefreshToken)

	third := loginUser(t, env, username, password)
	logoutAllResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/auth/logout-all", nil, secondTokens.AccessToken)
	require.Equal(t, http.StatusNoContent, logoutAllResp.StatusCode)

	refreshAfterAllResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/auth/refresh", secondRefreshBody, "")
	requireAuthInvalidToken(t, refreshAfterAllResp)

	thirdRefreshBody, _ := json.Marshal(map[string]string{
		"refresh_token": third.RefreshToken,
	})
	thirdRefreshResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/auth/refresh", thirdRefreshBody, "")
	requireAuthInvalidToken(t, thirdRefreshResp)
}

func loginUser(t *testing.T, env testEnv, username, password string) tokenResponse {
	t.Helper()

	loginBody, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	loginResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/auth/login", loginBody, "")
	require.Equal(t, http.StatusOK, loginResp.StatusCode)

	var tokens tokenResponse
	decodeJSON(t, loginResp, &tokens)
	return tokens
}

func requireAuthInvalidToken(t *testing.T, resp *http.Response) {
	t.Helper()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	var body apiError
	decodeJSON(t, resp, &body)
	require.Equal(t, "AUTH_INVALID_TOKEN", body.Error.Code)
}

func TestBlacklistKeyFormat(t *testing.T) {
	ctx := context.Background()
	redisURL := os.Getenv("TEST_REDIS_URL")
	var terminate func()

	if redisURL == "" {
		var setupErr error
		var redisContainer *rediscontainer.RedisContainer
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					setupErr = fmt.Errorf("%v", recovered)
				}
			}()

			container, err := rediscontainer.Run(ctx, "redis:7-alpine")
			if err != nil {
				setupErr = err
				return
			}
			redisContainer = container
			redisURL, err = redisContainer.ConnectionString(ctx)
			if err != nil {
				setupErr = err
			}
		}()

		if setupErr != nil {
			t.Skipf("testcontainers unavailable, set TEST_REDIS_URL: %v", setupErr)
		}

		terminate = func() {
			require.NoError(t, testcontainers.TerminateContainer(redisContainer))
		}
	}

	opts, err := redis.ParseURL(redisURL)
	require.NoError(t, err)
	client := redis.NewClient(opts)
	defer client.Close()
	if terminate != nil {
		defer terminate()
	}

	err = client.Set(ctx, "auth:bl:test-jti", "1", time.Minute).Err()
	require.NoError(t, err)

	exists, err := client.Exists(ctx, "auth:bl:test-jti").Result()
	require.NoError(t, err)
	require.Equal(t, 1, int(exists))
}
