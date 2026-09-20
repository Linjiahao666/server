package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds runtime configuration loaded from environment variables.
type Config struct {
	HTTPPort            string
	DatabaseURL         string
	RedisURL            string
	MinioEndpoint       string
	MinioPublicEndpoint string
	MinioAccessKey      string
	MinioSecretKey      string
	MinioBucket         string
	MinioUseSSL         bool
	JWTPrivateKey       string
	JWTPublicKey        string
	MigrationsPath      string
	RateLimitRegister   int
	RateLimitLogin      int
	RateLimitRefresh    int
}

const (
	defaultRegisterRateLimit = 5
	defaultLoginRateLimit    = 10
	defaultRefreshRateLimit  = 30
)

// Load reads configuration from environment variables.
func Load() (Config, error) {
	privateKey, err := readEnvOrFile("JWT_PRIVATE_KEY", "JWT_PRIVATE_KEY_FILE")
	if err != nil {
		return Config{}, err
	}
	publicKey, err := readEnvOrFile("JWT_PUBLIC_KEY", "JWT_PUBLIC_KEY_FILE")
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		HTTPPort:            getEnv("HTTP_PORT", "8080"),
		DatabaseURL:         os.Getenv("DATABASE_URL"),
		RedisURL:            os.Getenv("REDIS_URL"),
		MinioEndpoint:       getEnv("MINIO_ENDPOINT", "localhost:9000"),
		MinioPublicEndpoint: os.Getenv("MINIO_PUBLIC_ENDPOINT"),
		MinioAccessKey:      getEnv("MINIO_ACCESS_KEY", "minioadmin"),
		MinioSecretKey:      getEnv("MINIO_SECRET_KEY", "minioadmin"),
		MinioBucket:         getEnv("MINIO_BUCKET", "store"),
		MinioUseSSL:         getEnvBool("MINIO_USE_SSL", false),
		JWTPrivateKey:       privateKey,
		JWTPublicKey:        publicKey,
		MigrationsPath:      getEnv("MIGRATIONS_PATH", "migrations"),
		RateLimitRegister:   getEnvInt("RATE_LIMIT_REGISTER", defaultRegisterRateLimit),
		RateLimitLogin:      getEnvInt("RATE_LIMIT_LOGIN", defaultLoginRateLimit),
		RateLimitRefresh:    getEnvInt("RATE_LIMIT_REFRESH", defaultRefreshRateLimit),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.RedisURL == "" {
		return Config{}, fmt.Errorf("REDIS_URL is required")
	}

	return cfg, nil
}

// readEnvOrFile 优先读取环境变量，未设置时再读取对应路径文件。
func readEnvOrFile(envKey, fileKey string) (string, error) {
	if value := os.Getenv(envKey); value != "" {
		return value, nil
	}
	path := os.Getenv(fileKey)
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", fileKey, err)
	}
	return string(data), nil
}

func getEnv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func getEnvBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getEnvInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

// RegisterRateLimit returns the register limit, using the default when unset.
func (c Config) RegisterRateLimit() int {
	if c.RateLimitRegister <= 0 {
		return defaultRegisterRateLimit
	}
	return c.RateLimitRegister
}

// LoginRateLimit returns the login limit, using the default when unset.
func (c Config) LoginRateLimit() int {
	if c.RateLimitLogin <= 0 {
		return defaultLoginRateLimit
	}
	return c.RateLimitLogin
}

// RefreshRateLimit returns the refresh limit, using the default when unset.
func (c Config) RefreshRateLimit() int {
	if c.RateLimitRefresh <= 0 {
		return defaultRefreshRateLimit
	}
	return c.RateLimitRefresh
}
