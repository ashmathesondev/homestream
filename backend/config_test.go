package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	directory := t.TempDir()
	filename := filepath.Join(directory, "config.json")
	absolute := filepath.Join(directory, "mount", "videos")
	for _, test := range []struct {
		name, body, override, want string
		invalid                    bool
	}{
		{"relative", `{"upload_dir":"uploads"}`, "", filepath.Join(directory, "uploads"), false},
		{"absolute", `{"upload_dir":"` + filepath.ToSlash(absolute) + `"}`, "", absolute, false},
		{"override", `{"upload_dir":"uploads"}`, absolute, absolute, false},
		{"empty", `{"upload_dir":""}`, "", "", true},
		{"missing field", `{}`, "", "", true},
		{"unknown field", `{"upload_dir":"uploads","typo":true}`, "", "", true},
		{"malformed", `{`, "", "", true},
		{"extra object", `{"upload_dir":"uploads"} {}`, "", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(filename, []byte(test.body), 0600); err != nil {
				t.Fatal(err)
			}
			config, err := loadConfig(filename, test.override)
			if test.invalid {
				if err == nil {
					t.Fatal("expected configuration error")
				}
				return
			}
			if err != nil || config.UploadDir != test.want {
				t.Fatalf("config=%+v err=%v want=%s", config, err, test.want)
			}
		})
	}
	if _, err := loadConfig(filepath.Join(directory, "missing.json"), ""); err == nil {
		t.Fatal("missing config accepted")
	}
}

func TestUploadTimeoutConfig(t *testing.T) {
	for _, tt := range []struct {
		name    string
		field   string
		want    int64
		invalid bool
	}{
		{"default", "", 120, false},
		{"custom", `,"upload_timeout_seconds":600`, 600, false},
		{"disabled", `,"upload_timeout_seconds":0`, 0, false},
		{"negative", `,"upload_timeout_seconds":-1`, 0, true},
		{"fractional", `,"upload_timeout_seconds":1.5`, 0, true},
		{"string", `,"upload_timeout_seconds":"600"`, 0, true},
		{"overflow", `,"upload_timeout_seconds":9223372037`, 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(filename, []byte(`{"upload_dir":"uploads"`+tt.field+`}`), 0600); err != nil {
				t.Fatal(err)
			}
			config, err := loadConfig(filename, "")
			if tt.invalid {
				if err == nil {
					t.Fatal("expected configuration error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if config.UploadTimeoutSeconds != tt.want {
				t.Fatalf("timeout=%d want=%d", config.UploadTimeoutSeconds, tt.want)
			}
			w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
			before := time.Now()
			newServer(config).Handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/health", nil))
			if tt.want == 0 {
				if !w.read.IsZero() || !w.write.IsZero() {
					t.Fatal("deadlines should be disabled")
				}
			} else {
				minimum := before.Add(time.Duration(tt.want) * time.Second)
				maximum := time.Now().Add(time.Duration(tt.want) * time.Second)
				if w.read.Before(minimum) || w.read.After(maximum) || !w.read.Equal(w.write) {
					t.Fatalf("unexpected deadlines: %s %s", w.read, w.write)
				}
			}
		})
	}
}
