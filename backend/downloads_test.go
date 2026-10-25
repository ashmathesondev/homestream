package main

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloads(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "nested")
	os.Mkdir(directory, 0700)
	os.WriteFile(filepath.Join(directory, "first.txt"), []byte("first content"), 0600)
	os.WriteFile(filepath.Join(directory, "second.txt"), []byte("second content"), 0600)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	os.WriteFile(outside, []byte("secret"), 0600)
	os.Symlink(outside, filepath.Join(directory, "link.txt"))
	handler := routesWithUploadDir(root)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if method == "POST" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	response := request("GET", "/api/downloads/first.txt?path=nested", "")
	if response.Code != 200 || response.Body.String() != "first content" || !strings.Contains(response.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("download: %d %s", response.Code, response.Body.String())
	}
	response = request("POST", "/api/download-zip?path=nested", url.Values{"name": {"first.txt", "second.txt", "first.txt"}}.Encode())
	if response.Code != 200 {
		t.Fatalf("ZIP: %d %s", response.Code, response.Body.String())
	}
	archive, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.File) != 2 {
		t.Fatalf("archive entries: %d", len(archive.File))
	}
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		expected := strings.TrimSuffix(file.Name, ".txt") + " content"
		if err != nil || string(data) != expected {
			t.Fatalf("ZIP content %s: %q %v", file.Name, data, err)
		}
	}
	for _, names := range [][]string{nil, {"../outside.txt"}, {"link.txt"}, {"missing.txt"}, {"first.txt", "missing.txt"}} {
		response := request("POST", "/api/download-zip?path=nested", url.Values{"name": names}.Encode())
		if response.Code != 400 {
			t.Errorf("unsafe ZIP selection %v: %d", names, response.Code)
		}
	}
	if response := request("GET", "/api/downloads/link.txt?path=nested", ""); response.Code != 404 {
		t.Fatal("symlink download allowed")
	}
	if response := request("GET", "/api/downloads/outside.txt?path=..", ""); response.Code != 400 {
		t.Fatal("parent traversal allowed")
	}
}
