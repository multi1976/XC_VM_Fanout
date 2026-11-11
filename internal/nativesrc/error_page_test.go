// SPDX-License-Identifier: AGPL-3.0-or-later
// XC_VM_Fanout — https://github.com/Vateron-Media/XC_VM_Fanout
// See LICENSE and LICENSE-ADDITIONAL-TERMS.md

package nativesrc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAnErrorPageIsNotAFormatRefusal: the commoner half of the blip an empty
// 200 body is the other half of. An IPTV origin over its connection limit, or
// with an account that has just expired, answers the stream URL with 200 and an
// HTML page. Calling that a FORMAT refusal moves the channel onto its ffmpeg
// fallback for the life of the spec (remux exits ExitUnsupported and the
// supervisor's fallback is sticky) — and ffmpeg cannot play an HTML page
// either, so the move buys nothing and costs the channel its native path once
// the account comes back.
func TestAnErrorPageIsNotAFormatRefusal(t *testing.T) {
	cases := []struct {
		name, ct, body string
	}{
		{"connection limit", "text/html", "<html><body>Session limit reached.</body></html>"},
		{"login page", "text/html; charset=UTF-8", "<!DOCTYPE html>\n<html><head><title>Sign in</title></head></html>"},
		{"plain text notice", "text/plain", "MAX CONNECTIONS REACHED FOR THIS ACCOUNT"},
		{"page with no content-type", "application/octet-stream", "<html><body>account expired</body></html>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", c.ct)
				// Long enough that the sniff reads a full probe window.
				_, _ = io.WriteString(w, c.body+strings.Repeat(" ", 1024))
			}))
			defer srv.Close()

			rc, err := Open(context.Background(), srv.URL+"/live/user/pass/1", Options{})
			if err == nil {
				rc.Close()
				t.Fatal("Open accepted an error page as a stream")
			}
			if !errors.Is(err, ErrUnsupportedSource) {
				t.Fatalf("err = %v, want ErrUnsupportedSource", err)
			}
			if IsFormat(err) {
				t.Fatalf("err = %v reads as a format refusal, which pins the channel onto "+
					"ffmpeg for the life of its spec over an account blip", err)
			}
		})
	}
}

// TestAnErrorPageSaysWhy: the refusal carries the page's own words, so the log
// tells a channel the provider has removed ("Stream ID doesn't exist.") from an
// account over its limit — the first wants the source replaced, the second a
// retry. The style sheet XUI.one puts before its message is not the message,
// and a URL the page echoes stays out of the log.
func TestAnErrorPageSaysWhy(t *testing.T) {
	page := `<html><head><title>XUI.one - Debug Mode</title><link href="https://fonts.example/css" rel="stylesheet"><style>` +
		strings.Repeat("#notfound .notfound{position:absolute;left:50%}", 40) +
		`</style></head><body><div id="notfound"><div class="notfound"><h1>Stream ID doesn&#39;t exist.</h1>` +
		`<p>Requested http://host/live/user/pass/70.ts</p></div></div></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		_, _ = io.WriteString(w, page)
	}))
	defer srv.Close()

	_, err := Open(context.Background(), srv.URL+"/live/user/pass/70", Options{})
	if err == nil {
		t.Fatal("Open accepted an error page as a stream")
	}
	msg := err.Error()
	if !strings.Contains(msg, "a web page, not a stream (XUI.one - Debug Mode: Stream ID doesn't exist.") {
		t.Fatalf("err = %q, want the page title and message", msg)
	}
	if strings.Contains(msg, "position:absolute") || strings.Contains(msg, "user/pass") {
		t.Fatalf("err = %q carries the style sheet or the echoed account", msg)
	}
}
