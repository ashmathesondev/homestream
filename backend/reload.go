package main

import (
	"context"
	"log"
	"net/http"
	"sync/atomic"
	"time"
)

// Publish the settings and their routes together so each request sees one snapshot.
type configSnapshot struct {
	config  serverConfig
	handler http.Handler
}

type liveConfig struct {
	current atomic.Pointer[configSnapshot]
}

func newLiveConfig(config serverConfig) *liveConfig {
	live := &liveConfig{}
	live.current.Store(&configSnapshot{config, routesWithUploadDir(config.UploadDir)})
	return live
}

func (live *liveConfig) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	snapshot := live.current.Load()
	var deadline time.Time
	if seconds := snapshot.config.UploadTimeoutSeconds; seconds > 0 {
		deadline = time.Now().Add(time.Duration(seconds) * time.Second)
	}
	controller := http.NewResponseController(w)
	// Set deadlines per request rather than mutating an active http.Server.
	// The application serves HTTP/1 directly or behind Nginx.
	controller.SetReadDeadline(deadline)
	controller.SetWriteDeadline(deadline)
	snapshot.handler.ServeHTTP(w, r)
}

func (live *liveConfig) reload(filename, override string) (bool, error) {
	config, err := loadConfig(filename, override)
	if err != nil {
		return false, err
	}
	previous := live.current.Load()
	if config == previous.config {
		return false, nil
	}
	handler := previous.handler
	if config.UploadDir != previous.config.UploadDir {
		handler = routesWithUploadDir(config.UploadDir)
	}
	live.current.Store(&configSnapshot{config, handler})
	return true, nil
}

func (live *liveConfig) watch(ctx context.Context, filename, override string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastError := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			changed, err := live.reload(filename, override)
			if err != nil {
				if err.Error() != lastError {
					log.Printf("Configuration reload failed; keeping previous settings: %v", err)
					lastError = err.Error()
				}
				continue
			}
			if lastError != "" {
				log.Print("Configuration file is valid again")
			}
			lastError = ""
			if changed {
				config := live.current.Load().config
				log.Printf("Configuration reloaded: upload directory=%s, timeout=%ds", config.UploadDir, config.UploadTimeoutSeconds)
			}
		}
	}
}
