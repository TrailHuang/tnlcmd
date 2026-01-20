package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"cmdline/internal/server"
)

var (
	port = flag.Int("port", 2323, "telnet server port")
	host = flag.String("host", "0.0.0.0", "telnet server host")
)

func main() {
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	addr := fmt.Sprintf("%s:%d", *host, *port)
	srv := server.NewTelnetServer(addr)

	go func() {
		log.Printf("Starting telnet server on %s", addr)
		if err := srv.Start(ctx); err != nil {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	select {
	case sig := <-sigCh:
		log.Printf("Received signal: %v", sig)
	case <-ctx.Done():
	}

	log.Println("Shutting down server...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := srv.Stop(shutdownCtx); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}

	log.Println("Server stopped gracefully")
}
