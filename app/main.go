package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "/config/config.yml", "path to the vagent config file")
	flag.Parse()

	logger := log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := Load(*configPath, version)
	if err != nil {
		logger.Fatalf("load config: %v", err)
	}

	logger.Printf("loaded %d agent(s) from %s", len(cfg.Agents), *configPath)
	if err := runAgents(ctx, logger, cfg); err != nil {
		logger.Fatalf("run agents: %v", err)
	}
}

func runAgents(ctx context.Context, logger *log.Logger, cfg *Config) error {
	var wg sync.WaitGroup
	for _, agent := range cfg.Agents {
		p := newProbe(agent, logger.Writer(), logger.Flags())
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.run(ctx)
		}()
	}

	<-ctx.Done()
	wg.Wait()
	return nil
}
