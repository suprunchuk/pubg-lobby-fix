package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const checkTimeout = 3 * time.Second

var apiURL = "https://api.github.com/repos/suprunchuk/pubg-lobby-fix/releases/latest"

// Release is the subset of GitHub's release JSON we need.
type Release struct {
	TagName string  `json:"tag_name"`
	HTMLURL string  `json:"html_url"`
	Assets  []Asset `json:"assets"`
}

// Asset is a single downloadable release file.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// MaybeAuto checks GitHub for a newer release and, when found, downloads it,
// verifies its sha256 checksum and replaces the running executable on disk.
// It returns the new version and the caller should restart into it.
// An unreachable GitHub or an up-to-date binary yields ("", nil); a failed
// download or installation yields an error and the old binary keeps running.
func MaybeAuto(ctx context.Context, current string, log *slog.Logger) (string, error) {
	if log == nil {
		log = slog.Default()
	}
	if !checkable(current) {
		return "", nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate the running executable: %w", err)
	}
	return apply(ctx, current, exe, log)
}

// apply is MaybeAuto against an explicit executable path (testable).
func apply(ctx context.Context, current, exe string, log *slog.Logger) (string, error) {
	if log == nil {
		log = slog.Default()
	}
	if !checkable(current) {
		return "", nil
	}

	fetchCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	rel, err := fetchLatest(fetchCtx)
	cancel()
	if err != nil {
		log.Debug("update check skipped", "err", err)
		return "", nil
	}
	if !newer(rel.TagName, current) {
		return "", nil
	}

	log.Info("update available", "current", current, "latest", rel.TagName, "url", rel.HTMLURL)

	if err := ensureWritable(filepath.Dir(exe)); err != nil {
		return "", err
	}
	newBin, err := downloadUpdate(ctx, rel, log)
	if err != nil {
		return "", err
	}
	if err := replace(exe, newBin); err != nil {
		return "", err
	}
	log.Info("update installed", "version", rel.TagName)
	return rel.TagName, nil
}

func fetchLatest(ctx context.Context) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("User-Agent", "pubg-lobby-fix")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("github: %s", resp.Status)
	}

	var rel Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return Release{}, err
	}
	if rel.TagName == "" || rel.HTMLURL == "" {
		return Release{}, fmt.Errorf("github: empty release")
	}
	return rel, nil
}

func checkable(v string) bool {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	return v != "" && v[0] >= '0' && v[0] <= '9'
}

func newer(latest, current string) bool {
	return cmpVersion(latest, current) > 0
}

func cmpVersion(a, b string) int {
	as, bs := versionParts(a), versionParts(b)
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := range n {
		var av, bv int
		if i < len(as) {
			av = as[i]
		}
		if i < len(bs) {
			bv = bs[i]
		}
		if av != bv {
			return av - bv
		}
	}
	return 0
}

func versionParts(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	fields := strings.Split(v, ".")
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		n, _ := strconv.Atoi(f)
		out = append(out, n)
	}
	return out
}
