//go:build linux

package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestStorageSpace(t *testing.T) {
	handler := routesWithUploadDir(t.TempDir())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/storage", nil))
	if response.Code != 200 {
		t.Fatalf("storage: %d %s", response.Code, response.Body.String())
	}
	var data struct {
		Total     uint64  `json:"totalBytes"`
		Available uint64  `json:"availableBytes"`
		Percent   float64 `json:"freePercent"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data.Total == 0 || data.Available > data.Total || data.Percent != float64(data.Available)/float64(data.Total)*100 {
		t.Fatalf("invalid disk space: %+v", data)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("disk space must not be cached")
	}
	for _, path := range []string{"missing", ".."} {
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/storage?path="+path, nil))
		if response.Code == 200 {
			t.Fatalf("accepted invalid folder %q", path)
		}
	}
}

func TestStorageSpaceFailure(t *testing.T) {
	response := httptest.NewRecorder()
	storageSpace(t.TempDir()+"/missing")(response, httptest.NewRequest("GET", "/api/storage", nil))
	if response.Code != 500 {
		t.Fatalf("expected failure, got %d", response.Code)
	}
}
