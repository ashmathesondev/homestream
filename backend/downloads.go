package main

import (
	"archive/zip"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func openDownload(directory, name string) (*os.File, os.FileInfo, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return nil, nil, fmt.Errorf("invalid filename")
	}
	target := filepath.Join(directory, name)
	info, err := os.Lstat(target)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("only regular files can be downloaded")
	}
	file, err := os.Open(target)
	return file, info, err
}
func downloadFile(directory string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		file, info, err := openDownload(directory, name)
		if err != nil {
			http.Error(w, "File unavailable", 404)
			return
		}
		defer file.Close()
		http.NewResponseController(w).SetWriteDeadline(time.Time{})
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, name, info.ModTime(), file)
	}
}
func downloadZIP(directory string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid selection", 400)
			return
		}
		names := r.PostForm["name"]
		if len(names) == 0 || len(names) > 200 {
			http.Error(w, "Select between 1 and 200 files", 400)
			return
		}
		type entry struct {
			file *os.File
			info os.FileInfo
			name string
		}
		entries := []entry{}
		defer func() {
			for _, entry := range entries {
				entry.file.Close()
			}
		}()
		seen := map[string]bool{}
		for _, name := range names {
			if seen[name] {
				continue
			}
			seen[name] = true
			file, info, err := openDownload(directory, name)
			if err != nil {
				http.Error(w, "A selected file is unavailable. Refresh and try again.", 400)
				return
			}
			entries = append(entries, entry{file, info, name})
		}
		http.NewResponseController(w).SetWriteDeadline(time.Time{})
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="selected-files.zip"`)
		w.Header().Set("Cache-Control", "no-store")
		archive := zip.NewWriter(w)
		for _, entry := range entries {
			header, err := zip.FileInfoHeader(entry.info)
			if err == nil {
				header.Name = entry.name
				// Videos are already compressed. Store avoids expensive recompression.
				header.Method = zip.Store
				var output io.Writer
				output, err = archive.CreateHeader(header)
				if err == nil {
					_, err = io.Copy(output, entry.file)
				}
			}
			if err != nil {
				log.Printf("ZIP download: %v", err)
				panic(http.ErrAbortHandler)
			}
		}
		if err := archive.Close(); err != nil {
			log.Printf("ZIP download: %v", err)
			panic(http.ErrAbortHandler)
		}
	}
}
