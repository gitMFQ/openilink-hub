package main

import "testing"

func TestDisplayURL(t *testing.T) {
	cases := map[string]string{
		":9800":             "http://localhost:9800",
		"0.0.0.0:9800":      "http://localhost:9800",
		"127.0.0.1:19901":   "http://127.0.0.1:19901",
		"[::]:9800":         "http://localhost:9800",
		"hub.example.com:9": "http://hub.example.com:9",
	}
	for in, want := range cases {
		if got := displayURL(in); got != want {
			t.Errorf("displayURL(%q) = %q, want %q", in, got, want)
		}
	}
}
