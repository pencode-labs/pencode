package download

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFetchVerifiesDownloadAndCache(t *testing.T) {
	const payload = "release archive"
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte(payload)))
	name := filepath.Join(t.TempDir(), "bundle", "archive")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, payload)
	}))
	defer server.Close()

	if err := fetch(name, server.URL, "wrong"); err == nil {
		t.Fatal("accepted an unverified download")
	}
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		t.Fatalf("unverified archive was published: %v", err)
	}
	if err := os.WriteFile(name, []byte("damaged cache entry"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fetch(name, server.URL, sum); err != nil {
		t.Fatal(err)
	}
	server.Close()
	if err := fetch(name, server.URL, sum); err != nil {
		t.Fatalf("could not reuse verified archive offline: %v", err)
	}
}
