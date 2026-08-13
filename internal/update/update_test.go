package update

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

func TestWantsDownload(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"y", "Y", " yes ", "Yes"} {
		if !wantsDownload(s) {
			t.Fatalf("wantsDownload(%q) = false", s)
		}
	}
	for _, s := range []string{"", "n", "N", "no", "q", "д"} {
		if wantsDownload(s) {
			t.Fatalf("wantsDownload(%q) = true", s)
		}
	}
}

func TestFetchLatest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("missing User-Agent")
		}
		_, _ = io.WriteString(w, `{"tag_name":"v1.2.3","html_url":"https://example.com/v1.2.3"}`)
	}))
	t.Cleanup(srv.Close)

	orig := apiURL
	apiURL = srv.URL
	t.Cleanup(func() { apiURL = orig })

	rel, err := fetchLatest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.TagName != "v1.2.3" || rel.HTMLURL != "https://example.com/v1.2.3" {
		t.Fatalf("got %+v", rel)
	}
}

func TestMaybeOfferOpensOnYes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"tag_name":"v9.0.0","html_url":"https://example.com/download"}`)
	}))
	t.Cleanup(srv.Close)

	origURL, origOpen := apiURL, opener
	apiURL = srv.URL
	var opened string
	opener = func(raw string) error { opened = raw; return nil }
	t.Cleanup(func() { apiURL, opener = origURL, origOpen })

	var out bytes.Buffer
	MaybeOffer(context.Background(), "1.0.0", strings.NewReader("y\n"), &out, slog.New(slog.DiscardHandler))
	if opened != "https://example.com/download" {
		t.Fatalf("opened %q", opened)
	}
	if !strings.Contains(out.String(), "[y/N]") {
		t.Fatalf("expected prompt, got %q", out.String())
	}
}

func TestMaybeOfferSkipsOnNo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"tag_name":"v9.0.0","html_url":"https://example.com/download"}`)
	}))
	t.Cleanup(srv.Close)

	origURL, origOpen := apiURL, opener
	apiURL = srv.URL
	opener = func(string) error { t.Fatal("opened browser"); return nil }
	t.Cleanup(func() { apiURL, opener = origURL, origOpen })

	MaybeOffer(context.Background(), "1.0.0", strings.NewReader("\n"), io.Discard, slog.New(slog.DiscardHandler))
}
