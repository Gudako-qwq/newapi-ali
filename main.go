package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	logger := log.New(os.Stdout, "ali ", log.LstdFlags)
	config, err := loadConfig()
	if err != nil {
		logger.Fatal(err)
	}
	if config.PublicHTTP {
		logger.Print("WARNING: public HTTP enabled; browser passwords, session cookies and submitted AI credentials travel without TLS")
	}
	database, err := openDatabase(config.Database)
	if err != nil {
		logger.Fatal("cannot open application database")
	}
	defer database.Close()
	if err := bootstrapAdmin(database, config); err != nil {
		logger.Fatal(err)
	}
	app := newApp(config, database, logger)
	server := &http.Server{Addr: config.Listen, Handler: app.handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 100 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 * 1024}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 80*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	logger.Printf("listening on %s", config.Listen)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatal("HTTP server stopped unexpectedly")
	}
	<-shutdownDone
}
