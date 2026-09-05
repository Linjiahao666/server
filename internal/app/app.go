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
	jwtmanager "github.com/Linjiahao666/server/internal/jwt"
	"github.com/Linjiahao666/server/internal/migrate"
	"github.com/Linjiahao666/server/internal/repository"
)

// Dependencies holds shared application dependencies.
type Dependencies struct {
	Config   config.Config
	Pool     *pgxpool.Pool
	Redis    *redis.Client
	Auth     *auth.Service
	Handler  *auth.Handler
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

	userRepo := repository.NewUserRepository(pool)
	sessionRepo := repository.NewSessionRepository(pool)
	blacklistStore := blacklist.NewStore(redisClient)
	authService := auth.NewService(userRepo, sessionRepo, jwtManager, blacklistStore)
	authHandler := auth.NewHandler(authService)

	return &Dependencies{
		Config:  cfg,
		Pool:    pool,
		Redis:   redisClient,
		Auth:    authService,
		Handler: authHandler,
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
func NewRouter(handler *auth.Handler) *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery())

	authGroup := router.Group("/v1/auth")
	{
		authGroup.POST("/register", handler.Register)
		authGroup.POST("/login", handler.Login)
		authGroup.POST("/refresh", handler.Refresh)
		authGroup.GET("/.well-known/jwks.json", handler.JWKS)
		authGroup.POST("/logout", handler.RequireAuth(), handler.Logout)
		authGroup.GET("/me", handler.RequireAuth(), handler.Me)
	}

	return router
}
