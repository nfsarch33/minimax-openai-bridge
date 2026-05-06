package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/nfsarch33/minimax-openai-bridge/internal/bridge"
)

const version = "0.1.0"

func main() {
	wantVersion := flag.Bool("version", false, "Print version and exit.")
	flag.Parse()
	if *wantVersion {
		fmt.Printf("minimax-openai-bridge %s\n", version)
		return
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cfg := bridge.LoadConfig()
	server := bridge.NewServer(cfg, nil, log)
	log.Info("starting minimax-openai-bridge", "addr", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, server.Handler()); err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}
