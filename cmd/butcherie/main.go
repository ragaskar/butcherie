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

	"github.com/ragaskar/butcherie"
)

func main() {
	profile := flag.String("profile", "", "Profile name (required)")
	port := flag.Int("port", 31331, "Port to listen on")
	flag.Parse()

	if *profile == "" {
		fmt.Fprintln(os.Stderr, "error: --profile is required")
		flag.Usage()
		os.Exit(1)
	}

	cfg := butcherie.Config{
		Profile: *profile,
		Port:    *port,
	}

	srv := butcherie.New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fmt.Printf("Starting butcherie (profile=%s, port=%d)...\n", *profile, *port)
	fmt.Println("Waiting for Firefox to be ready...")

	if err := srv.Start(ctx); err != nil {
		log.Fatalf("failed to start: %v", err)
	}

	fmt.Printf("Ready. Listening on %s\n", srv.URI())

	// Block until SIGINT or SIGTERM.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Println("\nShutting down...")
	if err := srv.Shutdown(); err != nil {
		log.Printf("shutdown error: %v", err)
	}
}
