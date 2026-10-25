package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHealth(t *testing.T) {
	w := httptest.NewRecorder()
	routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected response: %d %v", w.Code, w.Header())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["status"] != "ok" {
		t.Fatalf("unexpected health response: %s", w.Body.String())
	}
}

func TestUnknownRoute(t *testing.T) {
	w := httptest.NewRecorder()
	routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/missing", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestUploads(t *testing.T) {
	for _, test := range []struct {
		name   string
		size   int
		status int
	}{
		{"normal", 12, http.StatusCreated},
		{"empty", 0, http.StatusCreated},
		{"large video above old limit", 12 * 1024 * 1024, http.StatusCreated},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			part, err := writer.CreateFormFile("file", "../../example.txt")
			if err != nil {
				t.Fatal(err)
			}
			content := bytes.Repeat([]byte("a"), test.size)
			if _, err := part.Write(content); err != nil {
				t.Fatal(err)
			}
			writer.Close()
			request := httptest.NewRequest(http.MethodPost, "/api/uploads", &body)
			request.Header.Set("Content-Type", writer.FormDataContentType())
			response := httptest.NewRecorder()
			routesWithUploadDir(directory).ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
			entries, err := os.ReadDir(directory)
			if err != nil {
				t.Fatal(err)
			}
			if test.status != http.StatusCreated {
				if len(entries) != 0 {
					t.Fatal("rejected upload left a file")
				}
				return
			}
			if len(entries) != 1 {
				t.Fatalf("expected one saved file, got %d", len(entries))
			}
			if entries[0].Name() != "example.txt" {
				t.Fatalf("filename not preserved: %s", entries[0].Name())
			}
			saved, err := os.ReadFile(filepath.Join(directory, entries[0].Name()))
			if err != nil || !bytes.Equal(saved, content) {
				t.Fatal("saved file differs from uploaded file")
			}
		})
	}
}

func TestInvalidUpload(t *testing.T) {
	response := httptest.NewRecorder()
	routesWithUploadDir(t.TempDir()).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/uploads", bytes.NewBufferString("invalid")))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", response.Code)
	}
}

func TestManageFiles(t *testing.T) {
	directory := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "example.txt"), []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(directory, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(directory, "link")); err != nil {
		t.Fatal(err)
	}
	handler := routesWithUploadDir(directory)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/uploads", nil))
	var files []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &files); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || len(files) != 2 || files[0].Name != "example.txt" || files[0].Size != 5 {
		t.Fatalf("unexpected listing: %s", response.Body.String())
	}
	for _, test := range []struct {
		path   string
		status int
	}{
		{"link", 403}, {"folder", 409}, {"missing", 404},
		{"..%2Foutside.txt", 400}, {"..%5Coutside.txt", 400},
		{"example.txt", 204}, {"example.txt", 404},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/uploads/"+test.path, nil))
		if response.Code != test.status {
			t.Errorf("delete %s: got %d want %d", test.path, response.Code, test.status)
		}
	}
	if content, err := os.ReadFile(outside); err != nil || string(content) != "keep" {
		t.Fatal("outside file affected")
	}
	if _, err := os.Stat(filepath.Join(directory, "example.txt")); !os.IsNotExist(err) {
		t.Fatal("file was not deleted")
	}
}

func TestMissingUploadDirectory(t *testing.T) {
	response := httptest.NewRecorder()
	routesWithUploadDir(filepath.Join(t.TempDir(), "missing")).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/uploads", nil))
	if response.Code != 200 || response.Body.String() != "[]\n" {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}

func TestUploadDuplicatePreservesExistingFile(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		directory := t.TempDir()
		target := filepath.Join(directory, "report.txt")
		original := []byte("original content")
		if symlink {
			outside := filepath.Join(t.TempDir(), "outside.txt")
			if err := os.WriteFile(outside, original, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, target); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(target, original, 0600); err != nil {
			t.Fatal(err)
		}
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", "report.txt")
		if err != nil {
			t.Fatal(err)
		}
		part.Write([]byte("replacement"))
		writer.Close()
		request := httptest.NewRequest(http.MethodPost, "/api/uploads", &body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		response := httptest.NewRecorder()
		routesWithUploadDir(directory).ServeHTTP(response, request)
		if response.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d", response.Code)
		}
		saved, err := os.ReadFile(target)
		if err != nil || !bytes.Equal(saved, original) {
			t.Fatal("existing file changed")
		}
	}
}

func TestFolderNavigation(t *testing.T) {
	root := t.TempDir()
	handler := routesWithUploadDir(root)
	request := func(method, url, body string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, url, bytes.NewBufferString(body)))
		return response
	}
	for _, test := range []struct {
		url, body string
		status    int
	}{
		{"/api/folders", `{"name":"projects"}`, 201},
		{"/api/folders", `{"name":"projects"}`, 409},
		{"/api/folders?path=projects", `{"name":"notes"}`, 201},
		{"/api/folders", `{"name":"../escape"}`, 400},
		{"/api/folders?path=..", `{"name":"escape"}`, 400},
		{"/api/folders?path=missing", `{"name":"escape"}`, 404},
	} {
		if response := request("POST", test.url, test.body); response.Code != test.status {
			t.Fatalf("%s: %d %s", test.url, response.Code, response.Body.String())
		}
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "report.txt")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte("nested content"))
	writer.Close()
	upload := httptest.NewRequest("POST", "/api/uploads?path=projects%2Fnotes", &body)
	upload.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, upload)
	if response.Code != 201 {
		t.Fatalf("nested upload: %d %s", response.Code, response.Body.String())
	}
	if saved, err := os.ReadFile(filepath.Join(root, "projects", "notes", "report.txt")); err != nil || string(saved) != "nested content" {
		t.Fatal("file not saved in selected folder")
	}
	response = request("GET", "/api/uploads?path=projects%2Fnotes", "")
	if response.Code != 200 || !bytes.Contains(response.Body.Bytes(), []byte(`"name":"report.txt"`)) {
		t.Fatalf("nested listing: %s", response.Body.String())
	}
	response = request("DELETE", "/api/uploads/report.txt?path=projects%2Fnotes", "")
	if response.Code != 204 {
		t.Fatalf("nested delete: %d", response.Code)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"..", "%2Ftmp", "projects%2F..", "projects%5C..", "link"} {
		response = request("GET", "/api/uploads?path="+path, "")
		if response.Code != 400 && response.Code != 403 {
			t.Errorf("unsafe path %s: status %d", path, response.Code)
		}
	}
	response = request("POST", "/api/folders?path=link", `{"name":"escape"}`)
	if response.Code != 403 {
		t.Fatalf("symlink create: %d", response.Code)
	}
}

func TestRecursiveFolderDeletion(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "folder", "nested")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(nested, "file.txt")
	if err := os.WriteFile(inside, []byte("inside"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "keep.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(nested, "link")); err != nil {
		t.Fatal(err)
	}
	handler := routesWithUploadDir(root)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("DELETE", "/api/uploads/folder", nil))
	if response.Code != 409 {
		t.Fatalf("unconfirmed deletion: %d", response.Code)
	}
	if _, err := os.Stat(inside); err != nil {
		t.Fatal("unconfirmed deletion removed content")
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("DELETE", "/api/uploads/folder?recursive=true", nil))
	if response.Code != 204 {
		t.Fatalf("recursive deletion: %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "folder")); !os.IsNotExist(err) {
		t.Fatal("folder remains")
	}
	if content, err := os.ReadFile(outside); err != nil || string(content) != "keep" {
		t.Fatal("recursive deletion affected outside file")
	}
}

func TestVideoStreaming(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "movies")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "sample.mp4"), []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.mp4")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(folder, "link.mp4")); err != nil {
		t.Fatal(err)
	}
	handler := routesWithUploadDir(root)
	for _, test := range []struct {
		method, url, byteRange string
		status                 int
		body                   string
	}{
		{"GET", "/api/videos/sample.mp4?path=movies", "", 200, "0123456789"},
		{"GET", "/api/videos/sample.mp4?path=movies", "bytes=2-5", 206, "2345"},
		{"HEAD", "/api/videos/sample.mp4?path=movies", "", 200, ""},
		{"GET", "/api/videos/sample.mp4?path=movies", "bytes=20-30", 416, ""},
		{"GET", "/api/videos/link.mp4?path=movies", "", 403, ""},
		{"GET", "/api/videos/secret.mp4?path=..", "", 400, ""},
		{"GET", "/api/videos/missing.mp4?path=movies", "", 404, ""},
		{"GET", "/api/videos/file.html?path=movies", "", 415, ""},
	} {
		request := httptest.NewRequest(test.method, test.url, nil)
		if test.byteRange != "" {
			request.Header.Set("Range", test.byteRange)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Errorf("%s: got %d want %d", test.url, response.Code, test.status)
		}
		if test.status == 200 || test.status == 206 {
			if response.Body.String() != test.body {
				t.Errorf("unexpected stream: %q", response.Body.String())
			}
			if response.Header().Get("Content-Type") != "video/mp4" {
				t.Error("missing video content type")
			}
		}
		if test.status == 206 && response.Header().Get("Content-Range") != "bytes 2-5/10" {
			t.Error("incorrect content range")
		}
	}
}

func TestInterruptedUploadIsRemoved(t *testing.T) {
	directory := t.TempDir()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "interrupted.mp4")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte("incomplete video"))
	// Omit the closing boundary to simulate a broken connection.
	request := httptest.NewRequest("POST", "/api/uploads", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	routesWithUploadDir(directory).ServeHTTP(response, request)
	if response.Code < 400 {
		t.Fatalf("incomplete upload accepted: %d", response.Code)
	}
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 0 {
		t.Fatal("incomplete upload left a file")
	}
}

func TestServerVideoSelection(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "uploads")
	handler := routesWithUploadDir(directory)
	request := func(method, body string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, "/api/video-selection", bytes.NewBufferString(body)))
		return response
	}
	if response := request("GET", ""); response.Code != 200 || response.Body.String() != "[]" {
		t.Fatalf("initial selection: %d %s", response.Code, response.Body.String())
	}
	saved := `["movies/sample.mp4","other.webm"]`
	if response := request("PUT", saved); response.Code != 204 {
		t.Fatalf("save: %d %s", response.Code, response.Body.String())
	}
	// Recreate the handlers to simulate a restart or a different client.
	handler = routesWithUploadDir(directory)
	if response := request("GET", ""); response.Code != 200 || response.Body.String() != saved+"\n" {
		t.Fatalf("restored: %d %s", response.Code, response.Body.String())
	}
	for _, invalid := range []string{`["../escape.mp4"]`, `["/outside.mp4"]`, `["file.txt"]`, `null`, `{}`, `[] []`} {
		if response := request("PUT", invalid); response.Code != 400 {
			t.Errorf("invalid selection accepted: %s (%d)", invalid, response.Code)
		}
	}
	if response := request("GET", ""); response.Body.String() != saved+"\n" {
		t.Fatal("invalid request changed selection")
	}
	if response := request("PUT", "[]"); response.Code != 204 {
		t.Fatal("clear failed")
	}
	handler = routesWithUploadDir(directory)
	if response := request("GET", ""); response.Body.String() != "[]\n" {
		t.Fatal("clear not persisted")
	}
}
