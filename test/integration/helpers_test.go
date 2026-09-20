package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	rediscontainer "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Linjiahao666/server/internal/app"
	"github.com/Linjiahao666/server/internal/config"
	jwtmanager "github.com/Linjiahao666/server/internal/jwt"
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

type testEnv struct {
	Router  *gin.Engine
	Cleanup func()
	Client  *http.Client
	JWT     *jwtmanager.Manager
}

func setupTestServer(ctx context.Context, t *testing.T) testEnv {
	return setupTestServerWithPublicMinio(ctx, t, "")
}

func setupTestServerWithPublicMinio(ctx context.Context, t *testing.T, publicEndpoint string) testEnv {
	gin.SetMode(gin.TestMode)

	databaseURL := os.Getenv("TEST_DATABASE_URL")
	redisURL := os.Getenv("TEST_REDIS_URL")
	minioEndpoint := os.Getenv("TEST_MINIO_ENDPOINT")

	var terminateFuncs []func()

	if databaseURL == "" || redisURL == "" || minioEndpoint == "" {
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

			minioContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
				ContainerRequest: testcontainers.ContainerRequest{
					Image:        "quay.io/minio/minio:RELEASE.2025-09-07T16-13-09Z",
					ExposedPorts: []string{"9000/tcp"},
					Env: map[string]string{
						"MINIO_ROOT_USER":     "minioadmin",
						"MINIO_ROOT_PASSWORD": "minioadmin",
					},
					Cmd:        []string{"server", "/data"},
					WaitingFor: wait.ForHTTP("/minio/health/ready").WithPort("9000/tcp"),
				},
				Started: true,
			})
			if err != nil {
				_ = testcontainers.TerminateContainer(postgresContainer)
				_ = testcontainers.TerminateContainer(redisContainer)
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

			host, err := minioContainer.Host(ctx)
			if err != nil {
				setupErr = err
				return
			}
			port, err := minioContainer.MappedPort(ctx, "9000/tcp")
			if err != nil {
				setupErr = err
				return
			}
			minioEndpoint = fmt.Sprintf("%s:%s", host, port.Port())

			terminateFuncs = append(terminateFuncs, func() {
				require.NoError(t, testcontainers.TerminateContainer(postgresContainer))
				require.NoError(t, testcontainers.TerminateContainer(redisContainer))
				require.NoError(t, testcontainers.TerminateContainer(minioContainer))
			})
		}()

		if setupErr != nil {
			t.Skipf("testcontainers unavailable, set TEST_DATABASE_URL, TEST_REDIS_URL and TEST_MINIO_ENDPOINT: %v", setupErr)
		}
	}

	cfg := config.Config{
		HTTPPort:            "8080",
		DatabaseURL:         databaseURL,
		RedisURL:            redisURL,
		MinioEndpoint:       minioEndpoint,
		MinioPublicEndpoint: publicEndpoint,
		MinioAccessKey:      "minioadmin",
		MinioSecretKey:      "minioadmin",
		MinioBucket:         "store",
		MinioUseSSL:         false,
		MigrationsPath:      migrationsDir(),
	}

	require.NoError(t, app.RunMigrations(cfg))

	deps, err := app.NewDependencies(cfg)
	require.NoError(t, err)

	router := app.NewRouter(deps.AuthHandler, deps.FileHandler)

	cleanup := func() {
		deps.Close()
		for _, terminate := range terminateFuncs {
			terminate()
		}
	}

	return testEnv{
		Router:  router,
		Cleanup: cleanup,
		Client:  &http.Client{Timeout: 30 * time.Second},
		JWT:     deps.JWT,
	}
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

func doMultipartRequest(t *testing.T, client *http.Client, router *gin.Engine, path string, fieldName, filename, contentType string, data []byte, accessToken string) *http.Response {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile(fieldName, filename)
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	if contentType != "" {
		require.NoError(t, writer.WriteField("content_type", contentType))
	}
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+accessToken)

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

func registerAndLogin(t *testing.T, env testEnv, username string, password string) tokenResponse {
	registerBody, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	registerResp := doRequest(t, env.Client, env.Router, http.MethodPost, "/v1/auth/register", registerBody, "")
	require.Equal(t, http.StatusCreated, registerResp.StatusCode)

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

func uploadPart(t *testing.T, client *http.Client, uploadURL string, data []byte) string {
	req, err := http.NewRequest(http.MethodPut, uploadURL, bytes.NewReader(data))
	require.NoError(t, err)
	req.ContentLength = int64(len(data))

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	etag := resp.Header.Get("ETag")
	require.NotEmpty(t, etag)
	return etag
}
