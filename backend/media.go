package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type mediaTrack struct {
	Index int               `json:"index"`
	Type  string            `json:"codec_type"`
	Codec string            `json:"codec_name"`
	Tags  map[string]string `json:"tags"`
}

func textSubtitle(codec string) bool {
	switch codec {
	case "subrip", "ass", "ssa", "webvtt", "mov_text", "text":
		return true
	}
	return false
}
func mediaPath(directory string, r *http.Request) (string, error) {
	name := r.PathValue("name")
	if name == "" || strings.ContainsAny(name, "/\\\x00") || videoType(name) == "" {
		return "", fmt.Errorf("invalid video name")
	}
	target, err := filepath.Abs(filepath.Join(directory, name))
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("video not found or not a regular file")
	}
	return target, nil
}
func probeMedia(ctx context.Context, target string) ([]mediaTrack, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_streams", "-of", "json", target).Output()
	if err != nil {
		return nil, err
	}
	var result struct {
		Streams []mediaTrack `json:"streams"`
	}
	err = json.Unmarshal(data, &result)
	return result.Streams, err
}

var mediaLocks sync.Map

func mediaHandler(cacheDirectory, action string) func(string) http.HandlerFunc {
	return func(directory string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			target, err := mediaPath(directory, r)
			if err != nil {
				http.Error(w, "Video not found", 404)
				return
			}
			tracks, err := probeMedia(r.Context(), target)
			if err != nil {
				log.Printf("probe video: %v", err)
				http.Error(w, "Unable to inspect tracks. Install ffmpeg and ffprobe on the server.", 503)
				return
			}
			if action == "tracks" {
				type trackInfo struct {
					mediaTrack
					Supported bool `json:"supported"`
				}
				result := make([]trackInfo, 0)
				for _, track := range tracks {
					if track.Type == "audio" || track.Type == "subtitle" {
						result = append(result, trackInfo{track, track.Type == "audio" || textSubtitle(track.Codec)})
					}
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Cache-Control", "no-store")
				json.NewEncoder(w).Encode(result)
				return
			}
			index, err := strconv.Atoi(r.URL.Query().Get("track"))
			valid := false
			for _, track := range tracks {
				if track.Index == index && ((action == "audio" && track.Type == "audio") || (action == "subtitles" && track.Type == "subtitle" && textSubtitle(track.Codec))) {
					valid = true
				}
			}
			if err != nil || !valid {
				http.Error(w, "Unsupported track (image subtitles require video conversion)", 400)
				return
			}
			http.NewResponseController(w).SetWriteDeadline(time.Time{})
			info, err := os.Stat(target)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			key := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d|%s|%d", target, info.Size(), info.ModTime().UnixNano(), action, index))))
			extension, contentType := ".mp4", "video/mp4"
			if action == "subtitles" {
				extension, contentType = ".vtt", "text/vtt; charset=utf-8"
			}
			cached := filepath.Join(cacheDirectory, key+extension)
			lockValue, _ := mediaLocks.LoadOrStore(key, &sync.Mutex{})
			lock := lockValue.(*sync.Mutex)
			lock.Lock()
			err = func() error {
				if _, err := os.Stat(cached); err == nil {
					return nil
				}
				if err := os.MkdirAll(cacheDirectory, 0700); err != nil {
					return err
				}
				temporary, err := os.CreateTemp(cacheDirectory, "media-*")
				if err != nil {
					return err
				}
				temporary.Close()
				defer os.Remove(temporary.Name())
				args := []string{"-nostdin", "-v", "error", "-y", "-i", target}
				if action == "audio" {
					args = append(args, "-map", "0:v:0", "-map", fmt.Sprintf("0:%d", index), "-c:v", "copy", "-c:a", "aac", "-ac", "2", "-b:a", "192k", "-sn", "-movflags", "+faststart", "-f", "mp4")
				} else {
					args = append(args, "-map", fmt.Sprintf("0:%d", index), "-c:s", "webvtt", "-f", "webvtt")
				}
				args = append(args, temporary.Name())
				if output, err := exec.CommandContext(r.Context(), "ffmpeg", args...).CombinedOutput(); err != nil {
					log.Printf("convert track: %v: %.2000s", err, output)
					return err
				}
				return os.Rename(temporary.Name(), cached)
			}()
			lock.Unlock()
			if err != nil {
				http.Error(w, "Unable to prepare track. Check server FFmpeg logs and disk space.", 500)
				return
			}
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			http.ServeFile(w, r, cached)
		}
	}
}
