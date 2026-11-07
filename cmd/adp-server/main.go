// Command adp-server runs the gateway without a window, for servers and
// containers.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/learningdog1/ada_proxy/internal/app"
	"github.com/learningdog1/ada_proxy/internal/version"
)

func main() {
	a, err := app.New(app.Options{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[adp-server] startup failed: %v\n", err)
		os.Exit(1)
	}
	defer a.Close()

	cfg := a.Cfg.Get()
	ln, err := a.Listen(cfg.Host, cfg.Port)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[adp-server] cannot listen on %s:%d: %v\n", cfg.Host, cfg.Port, err)
		os.Exit(1)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-signals
		fmt.Fprintf(os.Stderr, "[adp-server] %s received, shutting down\n", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.Shutdown(ctx)
	}()

	if err := a.Serve(ln); err != nil {
		fmt.Fprintf(os.Stderr, "[adp-server] serve failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "[adp-server] %s stopped\n", version.String())
}
