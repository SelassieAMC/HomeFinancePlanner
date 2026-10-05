package middleware

import (
	"bytes"
	"net/http"
	"strings"
)

// riverBackLink is the fixed pill injected into River UI's SPA shell so the
// user can always return to the planner: River's UI is third-party and offers
// no extension point. Inline styles — the injected element cannot rely on the
// SPA's stylesheet — and a slate-800 translucent pill that reads on both of
// River's themes (its body is `bg-white dark:bg-slate-900`). Fixed at the
// bottom-right keeps clear of River's header and table pagination.
const riverBackLink = `<a href="/" style="position:fixed;right:1rem;bottom:1rem;` +
	`z-index:50;background:rgba(30,41,59,.9);color:#fff;padding:.45rem .8rem;` +
	`border-radius:.5rem;font:600 .8rem system-ui,sans-serif;text-decoration:none;` +
	`opacity:.9">← Back to planner</a>`

// RiverUIBackLink injects a "back to the planner" link into River UI's SPA
// shell. River serves its index.html as the only text/html response; the
// wrapper buffers that one small response and appends the link before
// </body>. Non-navigation requests (the SPA's JSON fetches send Accept: */*,
// assets are hashed files) pass through untouched and unbuffered, as do
// compressed or malformed HTML payloads — the middleware never corrupts a
// response it cannot confidently annotate.
func RiverUIBackLink(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only the SPA shell qualifies for annotation. Everything else — JSON
		// fetches, range/full multi-part responses, non-GET/HEAD methods —
		// must pass through byte-identically, without buffering.
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) ||
			!strings.Contains(r.Header.Get("Accept"), "text/html") ||
			r.Header.Get("Range") != "" {
			next.ServeHTTP(w, r)
			return
		}

		rec := &bufferedWriter{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		header := rec.Header()
		if rec.code == http.StatusNotFound ||
			!strings.HasPrefix(header.Get("Content-Type"), "text/html") ||
			header.Get("Content-Encoding") != "" {
			rec.flush()
			return
		}
		body := rec.buf.Bytes()
		i := bytes.LastIndex(body, []byte("</body>"))
		if i < 0 {
			rec.flush()
			return
		}
		// The declared Content-Length describes the untouched shell — the
		// injected one is longer, and a stale length truncates the response
		// (the client gets a partial body, curl exit 18). Unsetting lets the
		// server recompute it. Note the delete must precede WriteHeader: Go
		// snapshots the header block at that call. (Range requests were
		// filtered out above.)
		header.Del("Content-Length")
		if rec.code != 0 {
			w.WriteHeader(rec.code)
		}
		injected := make([]byte, 0, len(body)+len(riverBackLink))
		injected = append(injected, body[:i]...)
		injected = append(injected, riverBackLink...)
		injected = append(injected, body[i:]...)
		w.Write(injected)
	})
}

// bufferedWriter buffers an upstream response so its headers and body can be
// inspected before the middleware decides to flush unchanged or inject.
type bufferedWriter struct {
	http.ResponseWriter
	code int
	buf  bytes.Buffer
}

func (b *bufferedWriter) WriteHeader(code int) {
	if b.code == 0 {
		b.code = code
	}
}

func (b *bufferedWriter) Write(p []byte) (int, error) {
	return b.buf.Write(p)
}

// flush replays the buffered response exactly as upstream produced it.
func (b *bufferedWriter) flush() {
	if b.code != 0 {
		b.ResponseWriter.WriteHeader(b.code)
	}
	b.ResponseWriter.Write(b.buf.Bytes())
}
