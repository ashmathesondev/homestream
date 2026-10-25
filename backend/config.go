package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type serverConfig struct {
	UploadDir            string `json:"upload_dir"`
	UploadTimeoutSeconds int64  `json:"upload_timeout_seconds"`
}

func loadConfig(filename, override string) (serverConfig, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return serverConfig{}, fmt.Errorf("read configuration: %w", err)
	}
	config := serverConfig{UploadTimeoutSeconds: 120}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, fmt.Errorf("invalid configuration: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return config, fmt.Errorf("configuration must contain a single JSON object")
	}
	if strings.TrimSpace(config.UploadDir) == "" {
		return config, fmt.Errorf("upload_dir must not be empty")
	}
	if config.UploadTimeoutSeconds < 0 || config.UploadTimeoutSeconds > int64((1<<63-1)/time.Second) {
		return config, fmt.Errorf("upload_timeout_seconds must be a non-negative integer representable as a duration")
	}
	if override != "" {
		// Preserve the existing environment override relative to the working directory.
		config.UploadDir = override
	} else if !filepath.IsAbs(config.UploadDir) {
		config.UploadDir = filepath.Join(filepath.Dir(filename), config.UploadDir)
	}
	config.UploadDir, err = filepath.Abs(config.UploadDir)
	return config, err
}
