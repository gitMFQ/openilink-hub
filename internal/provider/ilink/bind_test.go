package ilink

import "testing"

func TestRedirectPollBase(t *testing.T) {
	cases := []struct {
		name         string
		current      string
		status       string
		redirectHost string
		want         string
	}{
		{
			name:         "redirect switches host",
			status:       "scaned_but_redirect",
			redirectHost: "mock2.ilink",
			want:         "https://mock2.ilink",
		},
		{
			name:    "redirect without host keeps base",
			current: "https://old.ilink",
			status:  "scaned_but_redirect",
			want:    "https://old.ilink",
		},
		{
			name:    "other status keeps base",
			current: "https://old.ilink",
			status:  "scaned",
			want:    "https://old.ilink",
		},
		{
			name:    "terminal status keeps base",
			current: "https://old.ilink",
			status:  "confirmed",
			want:    "https://old.ilink",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redirectPollBase(tc.current, tc.status, tc.redirectHost); got != tc.want {
				t.Errorf("redirectPollBase(%q, %q, %q) = %q, want %q",
					tc.current, tc.status, tc.redirectHost, got, tc.want)
			}
		})
	}
}
