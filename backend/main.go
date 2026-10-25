package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Allow inherited default ACLs (such as Plex read access) through the group mask.
// The parent ACL or process umask still controls access; others get no access.
func uploadFile(directory string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Stream multipart data straight to disk, regardless of file size.
		reader, err := r.MultipartReader()
		if err != nil {
			http.Error(w, "Invalid upload", http.StatusBadRequest)
			return
		}
		file, err := reader.NextPart()
		if err != nil || file.FormName() != "file" || file.FileName() == "" {
			http.Error(w, "A file is required", http.StatusBadRequest)
			return
		}
		if err := os.MkdirAll(directory, 0770); err != nil {
			log.Printf("upload: storage directory %q: %v", directory, err)
			http.Error(w, "Unable to store file", http.StatusInternalServerError)
			return
		}
		// Keep the original basename, and never overwrite an existing file or symlink.
		name := filepath.Base(strings.ReplaceAll(file.FileName(), "\\", "/"))
		if name == "" || name == "." || name == ".." || strings.ContainsRune(name, 0) {
			http.Error(w, "Invalid filename", http.StatusBadRequest)
			return
		}
		stored, err := os.OpenFile(filepath.Join(directory, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0660)
		if os.IsExist(err) {
			http.Error(w, "A file with this name already exists", http.StatusConflict)
			return
		}
		if err != nil {
			log.Printf("upload: storage directory %q: %v", directory, err)
			http.Error(w, "Unable to store file", http.StatusInternalServerError)
			return
		}
		saved := false
		defer func() {
			stored.Close()
			if !saved {
				os.Remove(stored.Name())
			}
		}()
		size, err := io.Copy(stored, file)
		if err != nil {
			log.Printf("upload: storage directory %q: %v", directory, err)
			http.Error(w, "Unable to store file", http.StatusInternalServerError)
			return
		}
		// Require the closing multipart boundary before reporting success.
		if _, err := reader.NextPart(); err != io.EOF {
			http.Error(w, "Incomplete upload or unexpected extra part", http.StatusBadRequest)
			return
		}
		if err := stored.Close(); err != nil {
			log.Printf("upload: storage directory %q: %v", directory, err)
			http.Error(w, "Unable to store file", http.StatusInternalServerError)
			return
		}
		saved = true
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"name": name, "size": size})
	}
}

// List regular files and real folders; exclude symbolic links.
func listFiles(directory string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := os.ReadDir(directory)
		if err != nil && !os.IsNotExist(err) {
			log.Printf("list uploads: %v", err)
			http.Error(w, "Unable to list files", http.StatusInternalServerError)
			return
		}
		type fileEntry struct {
			Name        string    `json:"name"`
			Size        int64     `json:"size"`
			Modified    time.Time `json:"modified"`
			IsDirectory bool      `json:"isDirectory"`
		}
		files := make([]fileEntry, 0)
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil || (!info.Mode().IsRegular() && !info.IsDir()) {
				continue
			}
			files = append(files, fileEntry{entry.Name(), info.Size(), info.ModTime(), info.IsDir()})
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(files)
	}
}

func deleteFile(directory string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") || strings.ContainsRune(name, 0) {
			http.Error(w, "Invalid filename", http.StatusBadRequest)
			return
		}
		target := filepath.Join(directory, name)
		info, err := os.Lstat(target)
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			log.Printf("delete upload: %v", err)
			http.Error(w, "Unable to inspect file", http.StatusInternalServerError)
			return
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			http.Error(w, "Only files and folders can be deleted", http.StatusForbidden)
			return
		}
		// RemoveAll removes nested symlinks themselves without following them.
		// Require explicit recursive confirmation so a file becoming a folder
		// between listing and deletion cannot silently delete its contents.
		if info.IsDir() && r.URL.Query().Get("recursive") != "true" {
			http.Error(w, "Confirm recursive folder deletion", http.StatusConflict)
			return
		}
		remove := os.Remove
		if info.IsDir() {
			remove = os.RemoveAll
		}
		if err := remove(target); err != nil {
			if os.IsNotExist(err) {
				http.NotFound(w, r)
				return
			}
			log.Printf("delete upload: %v", err)
			http.Error(w, "Unable to delete file", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// Resolve only existing, real directories beneath the configured upload root.
func inUploadFolder(root string, handler func(string) http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		relative := r.URL.Query().Get("path")
		if strings.ContainsAny(relative, "\\\x00") || strings.HasPrefix(relative, "/") {
			http.Error(w, "Invalid folder path", http.StatusBadRequest)
			return
		}
		parts := []string{}
		if relative != "" {
			parts = strings.Split(relative, "/")
			for _, part := range parts {
				if part == "" || part == "." || part == ".." {
					http.Error(w, "Invalid folder path", http.StatusBadRequest)
					return
				}
			}
		}
		if err := os.MkdirAll(root, 0770); err != nil {
			http.Error(w, "Unable to open upload folder", 500)
			return
		}
		directory := root
		for _, part := range parts {
			directory = filepath.Join(directory, part)
			info, err := os.Lstat(directory)
			if os.IsNotExist(err) {
				http.NotFound(w, r)
				return
			}
			if err != nil {
				http.Error(w, "Unable to open folder", 500)
				return
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				http.Error(w, "Folder access denied", 403)
				return
			}
		}
		handler(directory)(w, r)
	}
}

func createFolder(directory string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "Invalid folder request", 400)
			return
		}
		if strings.TrimSpace(body.Name) == "" || body.Name == "." || body.Name == ".." || strings.ContainsAny(body.Name, "/\\\x00") {
			http.Error(w, "Use a single folder name", 400)
			return
		}
		if err := os.Mkdir(filepath.Join(directory, body.Name), 0770); err != nil {
			if os.IsExist(err) {
				http.Error(w, "A file or folder with this name already exists", 409)
				return
			}
			log.Printf("create folder: %v", err)
			http.Error(w, "Unable to create folder", 500)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}
}

func videoType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".ogv", ".ogg":
		return "video/ogg"
	case ".mov":
		return "video/quicktime"
	case ".mkv":
		return "video/x-matroska"
	default:
		return ""
	}
}

func streamVideo(directory string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
			http.Error(w, "Invalid filename", 400)
			return
		}
		contentType := videoType(name)
		if contentType == "" {
			http.Error(w, "Unsupported video file", 415)
			return
		}
		target := filepath.Join(directory, name)
		info, err := os.Lstat(target)
		if os.IsNotExist(err) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "Unable to open video", 500)
			return
		}
		if !info.Mode().IsRegular() {
			http.Error(w, "Video access denied", 403)
			return
		}
		file, err := os.Open(target)
		if err != nil {
			http.Error(w, "Unable to open video", 500)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Video playback can last longer than the normal API write timeout.
		http.NewResponseController(w).SetWriteDeadline(time.Time{})
		// ServeContent supports HEAD and byte ranges for playback and seeking.
		http.ServeContent(w, r, name, info.ModTime(), file)
	}
}

// Keep shared settings outside the browsable upload directory.
func videoSelection(stateDirectory string) http.HandlerFunc {
	var mutex sync.Mutex
	return func(w http.ResponseWriter, r *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		w.Header().Set("Cache-Control", "no-store")
		target := filepath.Join(stateDirectory, "video-selection.json")
		if r.Method == http.MethodGet {
			data, err := os.ReadFile(target)
			if os.IsNotExist(err) {
				data = []byte("[]")
			} else if err != nil {
				http.Error(w, "Unable to load selection", 500)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(data)
			return
		}
		var names []string
		r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&names); err != nil || names == nil {
			http.Error(w, "Invalid selection", 400)
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			http.Error(w, "Invalid selection", 400)
			return
		}
		for _, name := range names {
			if strings.ContainsAny(name, "\\\x00") || videoType(name) == "" {
				http.Error(w, "Invalid video path", 400)
				return
			}
			for _, part := range strings.Split(name, "/") {
				if part == "" || part == "." || part == ".." {
					http.Error(w, "Invalid video path", 400)
					return
				}
			}
		}
		if err := os.MkdirAll(stateDirectory, 0700); err != nil {
			http.Error(w, "Unable to save selection", 500)
			return
		}
		temporary, err := os.CreateTemp(stateDirectory, "selection-*")
		if err != nil {
			http.Error(w, "Unable to save selection", 500)
			return
		}
		defer os.Remove(temporary.Name())
		defer temporary.Close()
		if err := json.NewEncoder(temporary).Encode(names); err != nil {
			http.Error(w, "Unable to save selection", 500)
			return
		}
		if err := temporary.Sync(); err != nil {
			http.Error(w, "Unable to save selection", 500)
			return
		}
		if err := temporary.Close(); err != nil {
			http.Error(w, "Unable to save selection", 500)
			return
		}
		if err := os.Rename(temporary.Name(), target); err != nil {
			http.Error(w, "Unable to save selection", 500)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func routes() http.Handler {
	directory := os.Getenv("UPLOAD_DIR")
	if directory == "" {
		directory = "uploads"
	}
	return routesWithUploadDir(directory)
}

func routesWithUploadDir(directory string) http.Handler {
	mux := http.NewServeMux()
	selection := videoSelection(filepath.Clean(directory) + ".state")
	mux.HandleFunc("GET /api/video-selection", selection)
	mux.HandleFunc("PUT /api/video-selection", selection)
	mux.HandleFunc("POST /api/uploads", inUploadFolder(directory, uploadFile))
	mux.HandleFunc("GET /api/storage", inUploadFolder(directory, storageSpace))
	mux.HandleFunc("GET /api/uploads", inUploadFolder(directory, listFiles))
	mux.HandleFunc("DELETE /api/uploads/{name}", inUploadFolder(directory, deleteFile))
	mux.HandleFunc("GET /api/downloads/{name}", inUploadFolder(directory, downloadFile))
	mux.HandleFunc("POST /api/download-zip", inUploadFolder(directory, downloadZIP))
	mux.HandleFunc("POST /api/folders", inUploadFolder(directory, createFolder))
	for _, action := range []string{"tracks", "audio", "subtitles"} {
		mux.HandleFunc("GET /api/videos/{name}/"+action, inUploadFolder(directory, mediaHandler(filepath.Clean(directory)+".state/media-cache", action)))
	}
	mux.HandleFunc("GET /api/videos/{name}", inUploadFolder(directory, streamVideo))
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	return mux
}

func newServer(config serverConfig) *http.Server {
	return &http.Server{
		Addr:              "127.0.0.1:8080",
		Handler:           newLiveConfig(config),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func main() {
	configPath := flag.String("config", "config.json", "Path to the server JSON configuration file")
	flag.Parse()
	config, err := loadConfig(*configPath, os.Getenv("UPLOAD_DIR"))
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Upload directory: %s", config.UploadDir)
	log.Printf("Upload timeout: %d seconds (0 disables the limit)", config.UploadTimeoutSeconds)
	server := newServer(config)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.Handler.(*liveConfig).watch(ctx, *configPath, os.Getenv("UPLOAD_DIR"), time.Second)
	log.Printf("API listening on http://%s", server.Addr)
	log.Fatal(server.ListenAndServe())
}
