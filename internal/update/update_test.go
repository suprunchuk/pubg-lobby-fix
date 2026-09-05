package update

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		latest, current string
		want            bool
	}{
		{latest: "v1.0.1", current: "1.0.0", want: true},
		{latest: "1.0.1", current: "v1.0.0", want: true},
		{latest: "1.0.0", current: "1.0.0", want: false},
		{latest: "v1.0.0", current: "1.0.0", want: false},
		{latest: "1.0.0", current: "1.0.1", want: false},
		{latest: "1.10.0", current: "1.9.0", want: true},
		{latest: "2.0.0", current: "1.9.9", want: true},
		{latest: "1.0.0", current: "1.0", want: false},
		{latest: "1.0.0.1", current: "1.0.0", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.latest+" vs "+tt.current, func(t *testing.T) {
			t.Parallel()
			if got := newer(tt.latest, tt.current); got != tt.want {
				t.Fatalf("newer(%q, %q) = %v, want %v", tt.latest, tt.current, got, tt.want)
			}
		})
	}
}

func TestCheckable(t *testing.T) {
	t.Parallel()

	if checkable("dev") || checkable("") || checkable("none") {
		t.Fatal("dev/empty/none should skip the update check")
	}
	if !checkable("1.0.0") || !checkable("v1.2.3") {
		t.Fatal("release versions should be checkable")
	}
}

func TestFetchLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing User-Agent")
		}
		_, _ = io.WriteString(w, `{"tag_name":"v1.2.3","html_url":"https://example.com/v1.2.3",
			"assets":[{"name":"a.zip","browser_download_url":"https://example.com/a.zip"}]}`)
	}))
	t.Cleanup(srv.Close)

	orig := apiURL
	apiURL = srv.URL
	t.Cleanup(func() { apiURL = orig })

	rel, err := fetchLatest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v1.2.3" || rel.HTMLURL != "https://example.com/v1.2.3" || len(rel.Assets) != 1 {
		t.Fatalf("got %+v", rel)
	}
}

func TestZipAsset(t *testing.T) {
	t.Parallel()

	exact := fmt.Sprintf("pubg-lobby-fix_2.0.0_%s_%s.zip", runtime.GOOS, runtime.GOARCH)
	rel := Release{
		TagName: "v2.0.0",
		Assets: []Asset{
			{Name: "checksums.txt", URL: "https://example.com/checksums.txt"},
			{Name: exact, URL: "https://example.com/" + exact},
		},
	}
	a, err := zipAsset(rel)
	if err != nil || a.Name != exact {
		t.Fatalf("exact match failed: %q, %v", a.Name, err)
	}

	// Looser naming drift is still found.
	drifted := fmt.Sprintf("pubg-lobby-fix_2.0.0_%s_%s.zip", strings.ToUpper(runtime.GOOS), runtime.GOARCH)
	rel.Assets[1] = Asset{Name: drifted, URL: "https://example.com/" + drifted}
	if a, err = zipAsset(rel); err != nil || a.Name != drifted {
		t.Fatalf("fuzzy match failed: %q, %v", a.Name, err)
	}

	rel.Assets = rel.Assets[:1]
	if _, err := zipAsset(rel); err == nil {
		t.Fatal("expected an error when no matching zip asset exists")
	}
}

func TestVerifyChecksum(t *testing.T) {
	t.Parallel()

	data := []byte("some release payload")
	sum := sha256.Sum256(data)
	sums := fmt.Sprintf("%x  pubg-lobby-fix_1.0.0_windows_amd64.zip\n%x  pubg-lobby-fix_1.0.0_windows_arm64.zip\n",
		sum, make([]byte, 32))

	if err := verifyChecksum([]byte(sums), "pubg-lobby-fix_1.0.0_windows_amd64.zip", data); err != nil {
		t.Fatalf("valid checksum rejected: %v", err)
	}
	if err := verifyChecksum([]byte(sums), "pubg-lobby-fix_1.0.0_windows_amd64.zip", []byte("tampered")); err == nil {
		t.Fatal("tampered payload accepted")
	}
	if err := verifyChecksum([]byte(sums), "missing.zip", data); err == nil {
		t.Fatal("missing checksum entry accepted")
	}
}

func TestExeFromZip(t *testing.T) {
	t.Parallel()

	zipBytes := buildZip(t, map[string]string{
		"LICENSE":            "MIT",
		"README.md":          "# pubg-lobby-fix",
		"pubg-lobby-fix.exe": "NEWBIN",
		"sub/other.exe":      "should-not-win",
	})

	got, err := exeFromZip(zipBytes)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NEWBIN" {
		t.Fatalf("extracted %q", got)
	}

	noExe := buildZip(t, map[string]string{"LICENSE": "MIT"})
	if _, err := exeFromZip(noExe); err == nil {
		t.Fatal("expected an error for a zip without an executable")
	}
}

func TestApplyUpdatesBinary(t *testing.T) {
	ctx := context.Background()
	newBin := "NEWBIN v9 payload"
	zipBytes, sums := buildRelease(t, "9.0.0", newBin)
	srv := releaseServer(t, "9.0.0", zipBytes, sums)
	t.Cleanup(srv.Close)

	orig := apiURL
	apiURL = srv.URL + "/releases/latest"
	t.Cleanup(func() { apiURL = orig })

	exe := filepath.Join(t.TempDir(), "pubg-lobby-fix.exe")
	if err := os.WriteFile(exe, []byte("OLD v1 payload"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := apply(ctx, "1.0.1", exe, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if got != "v9.0.0" {
		t.Fatalf("apply returned %q", got)
	}
	after, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != newBin {
		t.Fatalf("executable not replaced, contains %q", after)
	}
	old, err := os.ReadFile(exe + ".old")
	if err != nil {
		t.Fatal(err)
	}
	if string(old) != "OLD v1 payload" {
		t.Fatalf("previous binary not kept as .old, contains %q", old)
	}
}

func TestApplySkipsWhenCurrent(t *testing.T) {
	ctx := context.Background()
	zipBytes, sums := buildRelease(t, "1.0.1", "NEWBIN")
	srv := releaseServer(t, "1.0.1", zipBytes, sums)
	t.Cleanup(srv.Close)

	orig := apiURL
	apiURL = srv.URL + "/releases/latest"
	t.Cleanup(func() { apiURL = orig })

	exe := filepath.Join(t.TempDir(), "pubg-lobby-fix.exe")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := apply(ctx, "1.0.1", exe, slog.New(slog.DiscardHandler))
	if err != nil || got != "" {
		t.Fatalf("apply = %q, %v; want no update", got, err)
	}
	if after, _ := os.ReadFile(exe); string(after) != "OLD" {
		t.Fatalf("executable was replaced: %q", after)
	}
}

func TestApplySurvivesNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // nothing listens — the check must quietly move on

	orig := apiURL
	apiURL = srv.URL
	t.Cleanup(func() { apiURL = orig })

	exe := filepath.Join(t.TempDir(), "pubg-lobby-fix.exe")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := apply(context.Background(), "1.0.0", exe, slog.New(slog.DiscardHandler))
	if err != nil || got != "" {
		t.Fatalf("apply = %q, %v; want a silent skip", got, err)
	}
}

func TestReplaceRollsBackOnFailure(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "pubg-lobby-fix.exe")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}

	orig := renameFn
	calls := 0
	renameFn = func(from, to string) error {
		calls++
		if calls == 2 { // staging the new exe in place fails
			return errors.New("boom")
		}
		return os.Rename(from, to)
	}
	t.Cleanup(func() { renameFn = orig })

	if err := replace(exe, []byte("NEW")); err == nil {
		t.Fatal("expected the replacement to fail")
	}
	after, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != "OLD" {
		t.Fatalf("rollback failed, executable contains %q", after)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "pubg-lobby-fix.exe" {
		t.Fatalf("leftover files after rollback: %v", entries)
	}
}

// releaseServer builds the zip and matching checksums.txt for version and
// returns them.
func buildRelease(t *testing.T, version, exeContent string) ([]byte, []byte) {
	t.Helper()

	zipBytes := buildZip(t, map[string]string{"pubg-lobby-fix.exe": exeContent})
	sum := sha256.Sum256(zipBytes)
	name := fmt.Sprintf("pubg-lobby-fix_%s_%s_%s.zip", version, goOS, goArch)
	sums := fmt.Sprintf("%x  %s\n", sum, name)
	return zipBytes, []byte(sums)
}

// releaseServer serves the GitHub API shape used by the updater: the latest
// release JSON under /releases/latest and the assets under /dl/.
func releaseServer(t *testing.T, version string, zipBytes, sums []byte) *httptest.Server {
	t.Helper()

	zipName := fmt.Sprintf("pubg-lobby-fix_%s_%s_%s.zip", version, goOS, goArch)
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		fmt.Fprintf(w, `{"tag_name":"v%[1]s","html_url":"%[2]s/tag/v%[1]s",
			"assets":[
				{"name":"%[3]s","browser_download_url":"%[2]s/dl/%[3]s"},
				{"name":"checksums.txt","browser_download_url":"%[2]s/dl/checksums.txt"}
			]}`, version, base, zipName)
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/dl/") {
		case zipName:
			_, _ = w.Write(zipBytes)
		case "checksums.txt":
			_, _ = w.Write(sums)
		default:
			http.NotFound(w, r)
		}
	})
	return httptest.NewServer(mux)
}

func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// goOS and goArch mirror runtime.GOOS/GOARCH for building asset names in
// tests without importing runtime everywhere.
var (
	goOS   = runtime.GOOS
	goArch = runtime.GOARCH
)
