package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const newBinary = "new-binary-content"

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// fixture serves a release API and the asset; digest and download prefix are adjustable.
func fixture(t *testing.T, tag, digest string) (Checker, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name":%q,"html_url":"https://example.test/rel","assets":[`+
			`{"name":"kumiko-satellite-linux-amd64","browser_download_url":%q,"digest":%q}]}`,
			tag, srv.URL+"/dl/kumiko-satellite-linux-amd64", digest)
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(newBinary)) })
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return Checker{BaseURL: srv.URL + "/latest", DownloadPrefix: srv.URL + "/dl/", GOOS: "linux", GOARCH: "amd64"}, srv
}

func TestCheckVersions(t *testing.T) {
	for _, tc := range []struct {
		tag, current string
		want         bool
	}{
		{"v0.3.0", "0.2.1", true},
		{"v0.2.10", "0.2.9", true},
		{"v0.2.1", "0.2.1", false},
		{"v0.2.0", "0.2.1", false},
		{"v0.3.0", "dev", false},
		{"v0.3.0", "garbage", false},
		{"nightly", "0.2.1", false},
	} {
		c, _ := fixture(t, tc.tag, "sha256:"+sum(newBinary))
		rel, ok, err := c.Check(context.Background(), tc.current)
		if err != nil || ok != tc.want {
			t.Errorf("%s vs %s: ok=%v err=%v", tc.tag, tc.current, ok, err)
		}
		if ok && (rel.Version != "0.3.0" && rel.Version != "0.2.10" || rel.SHA256 != sum(newBinary) || rel.PageURL == "") {
			t.Errorf("release incomplete: %+v", rel)
		}
	}
}

func TestCheckRejectsBadDigestAndForeignURL(t *testing.T) {
	for _, digest := range []string{"", "md5:abcd", "sha256:zz", "sha256:abcd"} {
		c, _ := fixture(t, "v0.3.0", digest)
		if _, ok, err := c.Check(context.Background(), "0.2.1"); err == nil || ok {
			t.Errorf("digest %q must fail, ok=%v err=%v", digest, ok, err)
		}
	}
	c, _ := fixture(t, "v0.3.0", "sha256:"+sum(newBinary))
	c.DownloadPrefix = "https://github.com/CosmicDriftGameStudio/kumiko-satellite/releases/download/"
	if _, ok, err := c.Check(context.Background(), "0.2.1"); err == nil || ok {
		t.Errorf("foreign URL prefix must fail, ok=%v err=%v", ok, err)
	}
}

func TestAssetNameAndMac(t *testing.T) {
	if AssetName("windows", "amd64") != "kumiko-satellite-windows-amd64.exe" || AssetName("linux", "arm64") != "kumiko-satellite-linux-arm64" {
		t.Error("asset names")
	}
	c, _ := fixture(t, "v0.3.0", "")
	c.GOOS = "darwin"
	rel, ok, err := c.Check(context.Background(), "0.2.1")
	if err != nil || !ok || rel.AssetURL != "" || rel.PageURL == "" {
		t.Errorf("darwin only links the release page: %+v %v %v", rel, ok, err)
	}
}

func TestApplySwapsAndCleans(t *testing.T) {
	c, _ := fixture(t, "v0.3.0", "sha256:"+sum(newBinary))
	rel, ok, err := c.Check(context.Background(), "0.2.1")
	if err != nil || !ok {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "kumiko-satellite")
	mustWrite(t, exe, "old-binary")
	mustWrite(t, exe+".old", "stale")
	if err := c.Apply(context.Background(), rel, exe); err != nil {
		t.Fatal(err)
	}
	if got := read(t, exe); got != newBinary {
		t.Errorf("exe = %q", got)
	}
	if read(t, exe+".old") != "old-binary" {
		t.Error("previous exe must stay as .old")
	}
	if info, _ := os.Stat(exe); info.Mode()&0o111 == 0 {
		t.Error("exe must be executable")
	}
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Error(".new must be gone")
	}
	CleanupOld(exe)
	if _, err := os.Stat(exe + ".old"); !os.IsNotExist(err) {
		t.Error("CleanupOld keeps .old")
	}
	CleanupOld(exe)
}

func TestApplyWrongDigestKeepsOldFile(t *testing.T) {
	c, _ := fixture(t, "v0.3.0", "sha256:"+sum("something else"))
	rel, ok, err := c.Check(context.Background(), "0.2.1")
	if err != nil || !ok {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "kumiko-satellite")
	mustWrite(t, exe, "old-binary")
	err = c.Apply(context.Background(), rel, exe)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("want checksum error, got %v", err)
	}
	if read(t, exe) != "old-binary" {
		t.Error("exe must be untouched")
	}
	for _, p := range []string{exe + ".new", exe + ".old"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s must not exist", p)
		}
	}
}

func TestApplyRefusesForeignURL(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "kumiko-satellite")
	mustWrite(t, exe, "old-binary")
	err := Checker{}.Apply(context.Background(), Release{AssetURL: "https://evil.example/x", SHA256: sum("x")}, exe)
	if err == nil || read(t, exe) != "old-binary" {
		t.Errorf("err=%v", err)
	}
}

func mustWrite(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o755); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
