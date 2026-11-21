package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCleanName(t *testing.T) {
	for _, in := range []string{
		"a." + strings.Repeat("x", 300),    // a long extension
		strings.Repeat("é", 150) + ".jpg",  // two bytes a character
		"../../etc/passwd",                 // folders
		"x\n-if\n0\nclip.png",              // line breaks
		".hidden.jpg",                      // a dot file
		strings.Repeat("a", 250) + ".jpeg", // long
	} {
		out := cleanName(in)
		if len(out) > 200 || !utf8.ValidString(out) || strings.ContainsAny(out, "/\n\r") || strings.HasPrefix(out, ".") {
			t.Errorf("cleanName(%q) = %q", in, out)
		}
	}
	if got := cleanName(strings.Repeat("a", 250) + ".jpeg"); !strings.HasSuffix(got, ".jpeg") {
		t.Errorf("the extension was lost: %q", got)
	}
}

func TestCrossSite(t *testing.T) {
	req := func(method string, h map[string]string) *http.Request {
		r := httptest.NewRequest(method, "http://photos.local/api/v1/uploads", nil)
		for k, v := range h {
			r.Header.Set(k, v)
		}
		return r
	}
	for _, c := range []struct {
		r    *http.Request
		want bool
	}{
		{req("POST", map[string]string{"Sec-Fetch-Site": "same-origin"}), false},
		{req("POST", map[string]string{"Sec-Fetch-Site": "same-site"}), true},
		{req("POST", map[string]string{"Sec-Fetch-Site": "cross-site"}), true},
		{req("POST", map[string]string{"Origin": "http://photos.local"}), false},
		{req("POST", map[string]string{"Origin": "http://photos.local:8080"}), true},
		{req("POST", nil), false}, // not a browser
		{req("GET", map[string]string{"Sec-Fetch-Site": "cross-site"}), false},
	} {
		if got := crossSite(c.r); got != c.want {
			t.Errorf("%s %v: %v", c.r.Method, c.r.Header, got)
		}
	}
}
