package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	rediscontainer "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Linjiahao666/server/internal/app"
	"github.com/Linjiahao666/server/internal/config"
)

type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type userResponse struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	CreatedAt string `json:"created_at"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

func TestAuthFlow(t *testing.T) {
	ctx := context.Background()
	router, cleanup := setupTestServer(ctx, t)
	defer cleanup()

	client := &http.Client{Timeout: 10 * time.Second}
	username := fmt.Sprintf("user_%d", time.Now().UnixNano())
	password := "password123"

	registerBody, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	registerResp := doRequest(t, client, router, http.MethodPost, "/v1/auth/register", registerBody, "")
	require.Equal(t, http.StatusCreated, registerResp.StatusCode)

	var registered userResponse
	decodeJSON(t, registerResp, &registered)
	require.Equal(t, username, registered.Username)

	conflictResp := doRequest(t, client, router, http.MethodPost, "/v1/auth/register", registerBody, "")
	require.Equal(t, http.StatusConflict, conflictResp.StatusCode)
	var conflictErr apiError
	decodeJSON(t, conflictResp, &conflictErr)
	require.Equal(t, "AUTH_USERNAME_TAKEN", conflictErr.Error.Code)

	loginBody, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	loginResp := doRequest(t, client, router, http.MethodPost, "/v1/auth/login", loginBody, "")
	require.Equal(t, http.StatusOK, loginResp.StatusCode)

	var loginTokens tokenResponse
	decodeJSON(t, loginResp, &loginTokens)
	require.Equal(t, "Bearer", loginTokens.TokenType)
	require.Equal(t, 900, loginTokens.ExpiresIn)
	require.NotEmpty(t, loginTokens.AccessToken)
	require.NotEmpty(t, loginTokens.RefreshToken)

	meResp := doRequest(t, client, router, http.MethodGet, "/v1/auth/me", nil, loginTokens.AccessToken)
	require.Equal(t, http.StatusOK, meResp.StatusCode)

	refreshBody, _ := json.Marshal(map[string]string{
		"refresh_token": loginTokens.RefreshToken,
	})
	refreshResp := doRequest(t, client, router, http.MethodPost, "/v1/auth/refresh", refreshBody, "")
	require.Equal(t, http.StatusOK, refreshResp.StatusCode)

	var refreshTokens tokenResponse
	decodeJSON(t, refreshResp, &refreshTokens)
	require.NotEmpty(t, refreshTokens.AccessToken)
	require.Equal(t, loginTokens.RefreshToken, refreshTokens.RefreshToken)

	jwksResp := doRequest(t, client, router, http.MethodGet, "/v1/auth/.well-known/jwks.json", nil, "")
	require.Equal(t, http.StatusOK, jwksResp.StatusCode)

	var jwks map[string]interface{}
	decodeJSON(t, jwksResp, &jwks)
	require.NotEmpty(t, jwks["keys"])

	logoutBody, _ := json.Marshal(map[string]string{
		"refresh_token": loginTokens.RefreshToken,
	})
	logoutResp := doRequest(t, client, router, http.MethodPost, "/v1/auth/logout", logoutBody, loginTokens.AccessToken)
	require.Equal(t, http.StatusNoContent, logoutResp.StatusCode)

	revokedResp := doRequest(t, client, router, http.MethodGet, "/v1/auth/me", nil, loginTokens.AccessToken)
	require.Equal(t, http.StatusUnauthorized, revokedResp.StatusCode)
	var revokedErr apiError
	decodeJSON(t, revokedResp, &revokedErr)
	require.Equal(t, "AUTH_INVALID_TOKEN", revokedErr.Error.Code)

	refreshAfterLogoutBody, _ := json.Marshal(map[string]string{
		"refresh_token": loginTokens.RefreshToken,
	})
	refreshAfterLogoutResp := doRequest(t, client, router, http.MethodPost, "/v1/auth/refresh", refreshAfterLogoutBody, "")
	require.Equal(t, http.StatusUnauthorized, refreshAfterLogoutResp.StatusCode)
}

func setupTestServer(ctx context.Context, t *testing.T) (*gin.Engine, func()) {
	gin.SetMode(gin.TestMode)

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	redisURL := os.Getenv("TEST_REDIS_URL")

	var terminateFuncs []func()

	if databaseURL == "" || redisURL == "" {
		var setupErr error
		func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					setupErr = fmt.Errorf("%v", recovered)
				}
			}()

			postgresContainer, err := postgres.Run(ctx,
				"postgres:16-alpine",
				postgres.WithDatabase("store"),
				postgres.WithUsername("store"),
				postgres.WithPassword("store"),
				testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
			)
			if err != nil {
				setupErr = err
				return
			}

			redisContainer, err := rediscontainer.Run(ctx, "redis:7-alpine")
			if err != nil {
				_ = testcontainers.TerminateContainer(postgresContainer)
				setupErr = err
				return
			}

			databaseURL, err = postgresContainer.ConnectionString(ctx, "sslmode=disable")
			if err != nil {
				setupErr = err
				return
			}

			redisURL, err = redisContainer.ConnectionString(ctx)
			if err != nil {
				setupErr = err
				return
			}

			terminateFuncs = append(terminateFuncs, func() {
				require.NoError(t, testcontainers.TerminateContainer(postgresContainer))
				require.NoError(t, testcontainers.TerminateContainer(redisContainer))
			})
		}()

		if setupErr != nil {
			t.Skipf("testcontainers unavailable, set TEST_DATABASE_URL and TEST_REDIS_URL: %v", setupErr)
		}
	}

	migrationsPath := migrationsDir()
	cfg := config.Config{
		HTTPPort:       "8080",
		DatabaseURL:    databaseURL,
		RedisURL:       redisURL,
		MigrationsPath: migrationsPath,
	}

	require.NoError(t, app.RunMigrations(cfg))

	deps, err := app.NewDependencies(cfg)
	require.NoError(t, err)

	router := app.NewRouter(deps.Handler)

	cleanup := func() {
		deps.Close()
		for _, terminate := range terminateFuncs {
			terminate()
		}
	}

	return router, cleanup
}

func migrationsDir() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "migrations"
	}
	return filepath.Join(filepath.Dir(filename), "..", "..", "migrations")
}

func doRequest(t *testing.T, client *http.Client, router *gin.Engine, method, path string, body []byte, accessToken string) *http.Response {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	resp := recorder.Result()
	t.Cleanup(func() {
		_ = resp.Body.Close()
	})
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, target interface{}) {
	defer resp.Body.Close()
	err := json.NewDecoder(resp.Body).Decode(target)
	require.NoError(t, err)
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
