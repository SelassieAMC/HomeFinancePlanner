package middleware

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func stubHandler(t *testing.T, ct, contentEncoding string, body []byte) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		if contentEncoding != "" {
			w.Header().Set("Content-Encoding", contentEncoding)
		}
		w.Write(body)
	})
}

func RiverUIBackLinkRequest(t *testing.T, next http.Handler, accept, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Accept", accept)
	rec := httptest.NewRecorder()
	RiverUIBackLink(next).ServeHTTP(rec, req)
	return rec
}

func TestRiverUIBackLinkInjectsIntoSPAShell(t *testing.T) {
	shell := `<!doctype html><html><body class="h-full bg-white dark:bg-slate-900"><div id="root" class="h-full"></div></body></html>`
	rec := RiverUIBackLinkRequest(t, stubHandler(t, "text/html; charset=utf-8", "", []byte(shell)), "text/html,application/xhtml+xml", "/riverui/")

	got := rec.Body.String()
	if !strings.Contains(got, riverBackLink) {
		t.Fatalf("back link not injected:\n%s", got)
	}
	if strings.Count(got, riverBackLink) != 1 {
		t.Fatalf("back link injected more than once:\n%s", got)
	}
	if i := strings.Index(got, riverBackLink); !strings.HasSuffix(got[:i], `<div id="root" class="h-full"></div>`) {
		t.Fatalf("injection not immediately before </body>:\n%s", got)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestRiverUIBackLinkPassesJSONFetchThrough(t *testing.T) {
	body := []byte(`{"jobs":[]}`)
	rec := RiverUIBackLinkRequest(t, stubHandler(t, "application/json", "", body), "*/*", "/riverui/api/jobs")
	if got := rec.Body.String(); got != string(body) {
		t.Fatalf("JSON body modified: %s", got)
	}
	if strings.Contains(rec.Body.String(), riverBackLink) {
		t.Fatal("back link present in JSON response")
	}
}

func TestRiverUIBackLinkPassesCompressedHTMLThrough(t *testing.T) {
	body := []byte("gzip-bytes</body>")
	rec := RiverUIBackLinkRequest(t, stubHandler(t, "text/html", "gzip", body), "text/html", "/riverui/")
	if got := rec.Body.String(); got != string(body) {
		t.Fatalf("compressed body modified: %s", got)
	}
}

func TestRiverUIBackLinkPassesHTMLWithoutBodyTagThrough(t *testing.T) {
	body := []byte("<!doctype html><html><head></head></html>")
	rec := RiverUIBackLinkRequest(t, stubHandler(t, "text/html", "", body), "text/html", "/riverui/")
	if got := rec.Body.String(); got != string(body) {
		t.Fatalf("bodyless HTML modified: %s", got)
	}
}

func TestRiverUIBackLinkPasses404Through(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/riverui/", nil)
	req.Header.Set("Accept", "text/html")
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found</body>"))
	})
	rec := httptest.NewRecorder()
	RiverUIBackLink(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), riverBackLink) {
		t.Fatal("back link present in 404 response")
	}
}

func TestRiverUIBackLinkAcceptsFiltering(t *testing.T) {
	calls := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++ })
	for _, accept := range []string{"*/*", "application/json", ""} {
		req := httptest.NewRequest(http.MethodGet, "/riverui/", nil)
		req.Header.Set("Accept", accept)
		rec := httptest.NewRecorder()
		RiverUIBackLink(next).ServeHTTP(rec, req)
	}
	if calls != 3 {
		t.Fatalf("next called %d times, want 3 (non-navigation paths must not wrap)", calls)
	}
}

func TestRiverUIBackLinkPreservesHeaders(t *testing.T) {
	shell := []byte("<html><body></body></html>")
	rec := RiverUIBackLinkRequest(t, stubHandler(t, "text/html; charset=utf-8", "", shell), "text/html", "/riverui")
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("content type = %q", ct)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("</body>")) {
		t.Fatalf("closing body tag lost:\n%s", rec.Body.String())
	}
}

// The upstream index response carries a Content-Length describing the
// un-injected shell; over a real connection a stale length truncates the
// longer injected body (curl exit 18, zero bytes received). Go recomputes the
// length only if the header is deleted before WriteHeader — this test pins
// the whole chain through an actual HTTP server.
func TestRiverUIBackLinkDeliversFullBodyOverARealServer(t *testing.T) {
	shell := `<!doctype html><html><body><div id="root"></div></body></html>`
	srv := httptest.NewServer(RiverUIBackLink(
		stubHandler(t, "text/html; charset=utf-8", "", []byte(shell))))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/riverui/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/html")
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}

	got := string(body)
	if !strings.Contains(got, riverBackLink) {
		t.Fatalf("back link not delivered:\n%s", got)
	}
	if !strings.HasSuffix(got, "</body></html>") {
		t.Fatalf("suffix lost — body truncated:\n%s", got)
	}
}
