package update

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const checkTimeout = 3 * time.Second

var (
	apiURL = "https://api.github.com/repos/suprunchuk/pubg-lobby-fix/releases/latest"
	opener = openURL
)

// Release is the subset of GitHub's release JSON we need.
type Release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// MaybeOffer checks GitHub for a newer release and, if found, asks whether to
// open the download page. Network errors are ignored. The user always chooses.
func MaybeOffer(ctx context.Context, current string, in io.Reader, out io.Writer, log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	if !checkable(current) {
		return
	}

	fetchCtx, cancel := context.WithTimeout(ctx, checkTimeout)
	rel, err := fetchLatest(fetchCtx)
	cancel()
	if err != nil {
		log.Debug("update check skipped", "err", err)
		return
	}
	if !newer(rel.TagName, current) {
		return
	}

	log.Info("update available",
		"current", current,
		"latest", rel.TagName,
		"url", rel.HTMLURL,
	)
	if !isTerminal(in) {
		return
	}

	fmt.Fprintf(out, "Open the GitHub download page? [y/N] ")
	if !wantsDownload(readLine(in)) {
		return
	}
	if err := opener(rel.HTMLURL); err != nil {
		log.Warn("could not open browser", "err", err, "url", rel.HTMLURL)
		return
	}
	log.Info("opened GitHub release page — this process keeps running")
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

func wantsDownload(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

func readLine(in io.Reader) string {
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return ""
	}
	return line
}

func isTerminal(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return true
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
