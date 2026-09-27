package github

import (
	"archive/tar"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ulikunitz/xz"
	"naiveswitcher/pkg/common"
)

func TestGitHubDownloadAssetReplacesExistingBinary(t *testing.T) {
	const payload = "new"
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	if err := tw.WriteHeader(&tar.Header{Name: "naive", Mode: 0o755, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	xw, err := xz.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := xw.Write(archive.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := xw.Close(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(compressed.Bytes())
	}))
	defer server.Close()

	previousBase := common.BasePath
	common.BasePath = t.TempDir()
	defer func() { common.BasePath = previousBase }()
	const name = "naiveproxy-v1.0.0-test"
	path := filepath.Join(common.BasePath, name)
	if err := os.WriteFile(path, []byte("old binary is much longer"), 0o755); err != nil {
		t.Fatal(err)
	}
	gotName, err := GitHubDownloadAsset(context.Background(), server.URL+"/"+name+".tar.xz")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if gotName != name || string(got) != payload {
		t.Fatalf("name = %q, content = %q", gotName, got)
	}
}
