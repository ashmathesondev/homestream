package main

import (
	"encoding/json"
	"log"
	"net/http"
)

func storageSpace(directory string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		total, available, err := diskSpace(directory)
		if err != nil || total == 0 {
			log.Printf("disk space for %q: total=%d error=%v", directory, total, err)
			http.Error(w, "Unable to read disk space", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			Total     uint64  `json:"totalBytes"`
			Available uint64  `json:"availableBytes"`
			Percent   float64 `json:"freePercent"`
		}{total, available, float64(available) / float64(total) * 100})
	}
}
