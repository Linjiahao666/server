// Store authentication and file HTTP service.
//
//	@title			Store Server API
//	@version		1.0
//	@description	Authentication and file APIs for the store service.
//	@BasePath		/
//	@securityDefinitions.apikey	BearerAuth
//	@in							header
//	@name						Authorization
//
//go:generate go run github.com/swaggo/swag/cmd/swag@v1.16.4 init -g main.go -d ./,../../internal --parseInternal -o ../../docs/swagger
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Linjiahao666/server/internal/app"
	"github.com/Linjiahao666/server/internal/config"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	if err := app.RunMigrations(cfg); err != nil {
		log.Fatalf("run migrations: %v", err)
	}

	deps, err := app.NewDependencies(cfg)
	if err != nil {
		log.Fatalf("init dependencies: %v", err)
	}
	defer deps.Close()

	router := app.NewRouter(deps)
	server := &http.Server{
		Addr:    ":" + cfg.HTTPPort,
		Handler: router,
	}

	go func() {
		log.Printf("listening on :%s", cfg.HTTPPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("shutdown: %v", err)
	}
}
