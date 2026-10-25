package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	read, write time.Time
}

func (w *deadlineRecorder) SetReadDeadline(d time.Time) error  { w.read = d; return nil }
func (w *deadlineRecorder) SetWriteDeadline(d time.Time) error { w.write = d; return nil }

func writeReloadConfig(t *testing.T, filename string, config serverConfig) {
	t.Helper()
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	// Editors commonly save by replacing the file, rather than modifying it in place.
	if err := os.WriteFile(filename+".tmp", data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filename+".tmp", filename); err != nil {
		t.Fatal(err)
	}
}

func TestConfigReload(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "config.json")
	first, second := t.TempDir(), t.TempDir()
	initial := serverConfig{first, 120}
	live := newLiveConfig(initial)
	writeReloadConfig(t, filename, serverConfig{second, 600})
	if changed, err := live.reload(filename, ""); err != nil || !changed {
		t.Fatalf("reload: %v %v", changed, err)
	}
	w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	live.ServeHTTP(w, httptest.NewRequest("POST", "/api/folders", strings.NewReader(`{"name":"new-folder"}`)))
	if w.Code != http.StatusCreated {
		t.Fatalf("folder: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(second, "new-folder")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(first, "new-folder")); !os.IsNotExist(err) {
		t.Fatal("used old directory")
	}
	if time.Until(w.read) < 599*time.Second {
		t.Fatal("timeout did not reload")
	}
	snapshot := live.current.Load()
	for _, body := range []string{`{`, `{"upload_dir":""}`, `{"upload_dir":"uploads","upload_timeout_seconds":-1}`} {
		os.WriteFile(filename, []byte(body), 0600)
		if changed, err := live.reload(filename, ""); err == nil || changed {
			t.Fatal("invalid config accepted")
		}
		if live.current.Load() != snapshot {
			t.Fatal("lost valid settings")
		}
	}
	os.Remove(filename)
	if _, err := live.reload(filename, ""); err == nil {
		t.Fatal("missing file accepted")
	}
	writeReloadConfig(t, filename, serverConfig{first, 0})
	if changed, err := live.reload(filename, second); err != nil || !changed {
		t.Fatalf("recovery: %v %v", changed, err)
	}
	if live.current.Load().config.UploadDir != second {
		t.Fatal("environment override lost")
	}
	w = &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	live.ServeHTTP(w, httptest.NewRequest("GET", "/api/health", nil))
	if !w.read.IsZero() || !w.write.IsZero() {
		t.Fatal("timeout not disabled")
	}
	if changed, err := live.reload(filename, second); err != nil || changed {
		t.Fatal("unchanged config republished")
	}
}

func TestConfigWatcherAndConcurrentRequests(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "config.json")
	initial := serverConfig{t.TempDir(), 120}
	live := newLiveConfig(initial)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); live.watch(ctx, filename, "", time.Millisecond) }()
	defer func() { cancel(); <-done }()
	var requests sync.WaitGroup
	requests.Add(1)
	go func() {
		defer requests.Done()
		for i := 0; i < 100; i++ {
			live.ServeHTTP(&deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}, httptest.NewRequest("GET", "/api/health", nil))
		}
	}()
	updated := serverConfig{initial.UploadDir, 600}
	writeReloadConfig(t, filename, updated)
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for live.current.Load().config != updated {
		select {
		case <-deadline:
			t.Fatal("watcher did not reload")
		case <-tick.C:
		}
	}
	requests.Wait()
}

func TestReloadPreservesActiveRequest(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "config.json")
	initial := serverConfig{t.TempDir(), 120}
	live := newLiveConfig(initial)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var directory string
	live.current.Store(&configSnapshot{initial, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		directory = initial.UploadDir
	})})
	w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	go func() { defer close(done); live.ServeHTTP(w, httptest.NewRequest("POST", "/api/uploads", nil)) }()
	<-entered
	oldDeadline := w.read
	updated := serverConfig{t.TempDir(), 0}
	writeReloadConfig(t, filename, updated)
	changed, err := live.reload(filename, "")
	close(release)
	<-done
	if err != nil || !changed {
		t.Fatalf("reload: %v %v", changed, err)
	}
	if directory != initial.UploadDir || w.read != oldDeadline || w.read.IsZero() {
		t.Fatal("active request settings changed")
	}
	next := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	live.ServeHTTP(next, httptest.NewRequest("GET", "/api/health", nil))
	if next.Code != 200 || !next.read.IsZero() || !next.write.IsZero() {
		t.Fatal("new request did not use reloaded settings")
	}
}

func TestUploadKeepsConfiguredDeadline(t *testing.T) {
	w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	newLiveConfig(serverConfig{t.TempDir(), 600}).ServeHTTP(w, httptest.NewRequest("POST", "/api/uploads", strings.NewReader("invalid multipart")))
	if w.read.IsZero() || w.write.IsZero() {
		t.Fatal("upload handler cleared configured deadlines")
	}
}
