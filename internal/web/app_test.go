package web

import "testing"

func TestJoinURLRejectsAbsoluteRedirectPrefixes(t *testing.T) {
	for _, target := range []string{"//example.com", `/\example.com`, `\example.com`} {
		if got := joinURL("/convert", target); got != "/convert/" {
			t.Errorf("joinURL(%q, %q) = %q; want %q", "/convert", target, got, "/convert/")
		}
	}
}
