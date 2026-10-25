package main

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Exercise actual filesystem inheritance: creation modes must not mask the
// named reader's permissions, and media files must not inherit execute access.
func TestUploadInheritsReaderACL(t *testing.T) {
	for _, tool := range []string{"setfacl", "getfacl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s unavailable", tool)
		}
	}
	root := t.TempDir()
	readerID := strconv.Itoa(os.Getuid())
	if output, err := exec.Command("setfacl", "-m", "d:u::rwx,d:u:"+readerID+":rx,d:g::---,d:m::rx,d:o::---", root).CombinedOutput(); err != nil {
		t.Fatalf("set default ACL: %v: %s", err, output)
	}
	handler := routesWithUploadDir(root)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/api/folders", strings.NewReader(`{"name":"movie"}`)))
	if response.Code != 201 {
		t.Fatalf("create folder: %d %s", response.Code, response.Body.String())
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "movie.mp4")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte("video"))
	writer.Close()
	request := httptest.NewRequest("POST", "/api/uploads?path=movie", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 201 {
		t.Fatalf("upload: %d %s", response.Code, response.Body.String())
	}
	for _, tt := range []struct{ path, mask string }{
		{filepath.Join(root, "movie"), "mask::r-x"},
		{filepath.Join(root, "movie", "movie.mp4"), "mask::r--"},
	} {
		output, err := exec.Command("getfacl", "-cpn", tt.path).CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		acl := string(output)
		if !strings.Contains(acl, "user:"+readerID+":r-x") || !strings.Contains(acl, tt.mask) || !strings.Contains(acl, "other::---") {
			t.Fatalf("unexpected inherited reader ACL for %s:\n%s", tt.path, acl)
		}
	}
}
