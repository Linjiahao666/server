package app

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/Linjiahao666/server/internal/auth"
	"github.com/Linjiahao666/server/internal/blacklist"
	"github.com/Linjiahao666/server/internal/config"
	"github.com/Linjiahao666/server/internal/files"
	jwtmanager "github.com/Linjiahao666/server/internal/jwt"
	"github.com/Linjiahao666/server/internal/migrate"
	"github.com/Linjiahao666/server/internal/repository"
	"github.com/Linjiahao666/server/internal/storage"
)

// Dependencies holds shared application dependencies.
type Dependencies struct {
	Config      config.Config
	Pool        *pgxpool.Pool
	Redis       *redis.Client
	Auth        *auth.Service
	AuthHandler *auth.Handler
	FileHandler *files.Handler
}

// NewDependencies wires core services.
func NewDependencies(cfg config.Config) (*Dependencies, error) {
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}

	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	redisClient := redis.NewClient(redisOpts)

	jwtManager, err := jwtmanager.NewManager(cfg.JWTPrivateKey, cfg.JWTPublicKey)
	if err != nil {
		pool.Close()
		_ = redisClient.Close()
		return nil, fmt.Errorf("init jwt: %w", err)
	}

	objectStore, err := storage.NewMinioStore(cfg)
	if err != nil {
		pool.Close()
		_ = redisClient.Close()
		return nil, fmt.Errorf("init minio: %w", err)
	}

	userRepo := repository.NewUserRepository(pool)
	sessionRepo := repository.NewSessionRepository(pool)
	fileRepo := repository.NewFileRepository(pool)
	uploadRepo := repository.NewUploadRepository(pool)
	blacklistStore := blacklist.NewStore(redisClient)
	authService := auth.NewService(userRepo, sessionRepo, jwtManager, blacklistStore)
	authHandler := auth.NewHandler(authService)
	fileService := files.NewService(fileRepo, uploadRepo, objectStore, jwtManager, blacklistStore)
	fileHandler := files.NewHandler(fileService)

	return &Dependencies{
		Config:      cfg,
		Pool:        pool,
		Redis:       redisClient,
		Auth:        authService,
		AuthHandler: authHandler,
		FileHandler: fileHandler,
	}, nil
}

// Close releases resources.
func (d *Dependencies) Close() {
	if d.Pool != nil {
		d.Pool.Close()
	}
	if d.Redis != nil {
		_ = d.Redis.Close()
	}
}

// RunMigrations applies database migrations.
func RunMigrations(cfg config.Config) error {
	return migrate.Up(cfg.DatabaseURL, cfg.MigrationsPath)
}

// NewRouter builds the HTTP router.
func NewRouter(authHandler *auth.Handler, fileHandler *files.Handler) *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery())

	authGroup := router.Group("/v1/auth")
	{
		authGroup.POST("/register", authHandler.Register)
		authGroup.POST("/login", authHandler.Login)
		authGroup.POST("/refresh", authHandler.Refresh)
		authGroup.GET("/.well-known/jwks.json", authHandler.JWKS)
		authGroup.POST("/logout", authHandler.RequireAuth(), authHandler.Logout)
		authGroup.POST("/logout-all", authHandler.RequireAuth(), authHandler.LogoutAll)
		authGroup.GET("/me", authHandler.RequireAuth(), authHandler.Me)
	}

	fileGroup := router.Group("/v1/files")
	{
		authed := fileGroup.Group("", authHandler.RequireAuth())
		{
			authed.POST("", fileHandler.UploadSmall)
			authed.POST("/uploads", fileHandler.InitiateUpload)
			authed.POST("/uploads/:upload_id/complete", fileHandler.CompleteUpload)
			authed.DELETE("/uploads/:upload_id", fileHandler.AbortUpload)
			authed.GET("/:file_id", fileHandler.GetFile)
			authed.POST("/:file_id/access-token", fileHandler.IssueFileAccessToken)
			authed.DELETE("/:file_id", fileHandler.DeleteFile)
		}
		fileGroup.GET("/:file_id/content", fileHandler.RequireFileAccessAuth(), fileHandler.GetContent)
	}

	return router
}
