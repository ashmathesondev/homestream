package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedTracks(t *testing.T) {
	for _, binary := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skip("requires ffmpeg and ffprobe")
		}
	}
	root := t.TempDir()
	uploads := filepath.Join(root, "uploads")
	os.Mkdir(uploads, 0700)
	subtitle := filepath.Join(root, "test.srt")
	os.WriteFile(subtitle, []byte("1\n00:00:00,000 --> 00:00:00,900\nHello subtitles\n"), 0600)
	target := filepath.Join(uploads, "test.mkv")
	output, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=size=32x32:rate=10:duration=1", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-i", subtitle, "-map", "0:v", "-map", "1:a", "-map", "1:a", "-map", "2:s", "-c:v", "mpeg4", "-c:a", "ac3", "-c:s", "srt", target).CombinedOutput()
	if err != nil {
		t.Fatalf("fixture: %v %s", err, output)
	}
	handler := routesWithUploadDir(uploads)
	request := func(url string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", url, nil))
		return response
	}
	response := request("/api/videos/test.mkv/tracks")
	var tracks []mediaTrack
	if err := json.Unmarshal(response.Body.Bytes(), &tracks); err != nil || response.Code != 200 || len(tracks) != 3 {
		t.Fatalf("tracks: %d %s", response.Code, response.Body.String())
	}
	response = request("/api/videos/test.mkv/subtitles?track=3")
	if response.Code != 200 || !strings.Contains(response.Body.String(), "WEBVTT") || !strings.Contains(response.Body.String(), "Hello subtitles") {
		t.Fatalf("subtitles: %d %s", response.Code, response.Body.String())
	}
	response = request("/api/videos/test.mkv/audio?track=2")
	if response.Code != 200 || response.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("audio: %d %s", response.Code, response.Body.String())
	}
	converted := filepath.Join(root, "converted.mp4")
	os.WriteFile(converted, response.Body.Bytes(), 0600)
	streams, err := probeMedia(context.Background(), converted)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, stream := range streams {
		if stream.Type == "audio" {
			found = true
			if stream.Codec != "aac" {
				t.Fatalf("audio codec: %s", stream.Codec)
			}
		}
	}
	if !found {
		t.Fatal("missing converted audio")
	}
	for _, url := range []string{"/api/videos/test.mkv/audio?track=3", "/api/videos/test.mkv/subtitles?track=1", "/api/videos/test.mkv/tracks?path=.."} {
		if response := request(url); response.Code != 400 {
			t.Errorf("invalid track request %s: %d", url, response.Code)
		}
	}
}
