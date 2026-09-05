package update

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	downloadTimeout = 2 * time.Minute
	// Release assets are ~3 MB; the caps are sanity guards, not the norm.
	maxZipSize  = 64 << 20
	maxListSize = 1 << 20
	oldSuffix   = ".old"

	binaryName = "pubg-lobby-fix.exe"

	// EnvUpdated is passed to the restarted process with the version it was
	// just updated to, so it skips the immediate re-check (restart-loop guard).
	EnvUpdated = "PUBG_LOBBY_FIX_UPDATED"
)

// CleanupOld removes the previous binary left behind by an earlier
// self-update. The replaced process may still be exiting, so removal is
// retried briefly; a locked leftover is harmless and cleaned on next start.
func CleanupOld() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	old := exe + oldSuffix
	for range 10 {
		if _, err := os.Stat(old); err != nil {
			return
		}
		if os.Remove(old) == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Restart launches the freshly installed executable with the same arguments
// and returns; the caller should exit immediately.
func Restart(updatedTo string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Env = append(os.Environ(), EnvUpdated+"="+updatedTo)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Start()
}

func downloadUpdate(ctx context.Context, rel Release, log *slog.Logger) ([]byte, error) {
	asset, err := zipAsset(rel)
	if err != nil {
		return nil, err
	}
	dlCtx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	log.Info("downloading update", "asset", asset.Name)
	zipBytes, err := download(dlCtx, asset.URL, maxZipSize)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", asset.Name, err)
	}

	sums, ok := assetByName(rel, "checksums.txt")
	if !ok {
		return nil, fmt.Errorf("release %s has no checksums.txt", rel.TagName)
	}
	sumBytes, err := download(dlCtx, sums.URL, maxListSize)
	if err != nil {
		return nil, fmt.Errorf("download checksums.txt: %w", err)
	}
	if err := verifyChecksum(sumBytes, asset.Name, zipBytes); err != nil {
		return nil, err
	}

	exeBin, err := exeFromZip(zipBytes)
	if err != nil {
		return nil, err
	}
	log.Info("update downloaded and verified", "bytes", len(exeBin))
	return exeBin, nil
}

// zipAsset picks the Windows zip matching the running architecture, by the
// goreleaser name pattern first, then by a looser name match.
func zipAsset(rel Release) (Asset, error) {
	want := fmt.Sprintf("pubg-lobby-fix_%s_%s_%s.zip",
		strings.TrimPrefix(rel.TagName, "v"), runtime.GOOS, runtime.GOARCH)
	for _, a := range rel.Assets {
		if strings.EqualFold(a.Name, want) {
			return a, nil
		}
	}
	for _, a := range rel.Assets {
		name := strings.ToLower(a.Name)
		if strings.HasSuffix(name, ".zip") &&
			strings.Contains(name, runtime.GOOS) &&
			strings.Contains(name, runtime.GOARCH) {
			return a, nil
		}
	}
	return Asset{}, fmt.Errorf("release %s has no %s/%s zip asset", rel.TagName, runtime.GOOS, runtime.GOARCH)
}

func assetByName(rel Release, name string) (Asset, bool) {
	for _, a := range rel.Assets {
		if strings.EqualFold(a.Name, name) {
			return a, true
		}
	}
	return Asset{}, false
}

func download(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "pubg-lobby-fix")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("bigger than the %d byte limit", limit)
	}
	return data, nil
}

// verifyChecksum checks data against the sha256 line for name in a
// goreleaser-format checksums file ("<hex>  <filename>").
func verifyChecksum(sums []byte, name string, data []byte) error {
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.EqualFold(fields[1], name) {
			want = fields[0]
		}
	}
	if want == "" {
		return fmt.Errorf("checksums.txt has no entry for %s", name)
	}
	h := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(h[:]), want) {
		return fmt.Errorf("sha256 mismatch for %s", name)
	}
	return nil
}

// exeFromZip extracts the executable from a release zip.
func exeFromZip(zipBytes []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	var file *zip.File
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, binaryName) {
			file = f
			break
		}
	}
	if file == nil {
		for _, f := range zr.File {
			if strings.HasSuffix(strings.ToLower(f.Name), ".exe") {
				file = f
				break
			}
		}
	}
	if file == nil {
		return nil, errors.New("zip archive contains no .exe")
	}
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxZipSize))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file.Name, err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%s is empty", file.Name)
	}
	return data, nil
}

// ensureWritable fails early instead of downloading into an install dir the
// process cannot write to.
func ensureWritable(dir string) error {
	probe, err := os.CreateTemp(dir, ".pubg-lobby-fix-probe-*")
	if err != nil {
		return fmt.Errorf("install dir %s is not writable — run as administrator", dir)
	}
	probe.Close()
	return os.Remove(probe.Name())
}

// renameFn exists so tests can simulate rename failures.
var renameFn = os.Rename

// replace swaps the on-disk executable for newBin. A running exe cannot be
// written or deleted on Windows, but it can be renamed out of the way; the
// .old leftover is removed by the restarted process (CleanupOld).
func replace(exe string, newBin []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(exe), ".pubg-lobby-fix-*.new")
	if err != nil {
		return fmt.Errorf("stage the new executable: %w", err)
	}
	staged := tmp.Name()
	if _, err := tmp.Write(newBin); err != nil {
		tmp.Close()
		os.Remove(staged)
		return fmt.Errorf("stage the new executable: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(staged)
		return fmt.Errorf("stage the new executable: %w", err)
	}

	old := exe + oldSuffix
	if err := renameFn(exe, old); err != nil {
		os.Remove(staged)
		return fmt.Errorf("move the running executable out of the way: %w", err)
	}
	if err := renameFn(staged, exe); err != nil {
		_ = renameFn(old, exe) // best-effort rollback
		os.Remove(staged)
		return fmt.Errorf("put the new executable in place: %w", err)
	}
	return nil
}
