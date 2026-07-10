package main

import (
	"testing"
	"time"
)

func TestParseSinceDurationEdges(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"14d", 14 * 24 * time.Hour, false},
		{"2w", 14 * 24 * time.Hour, false},
		{"12h", 12 * time.Hour, false},
		{"0d", 0, false},
		{"1h", time.Hour, false},
		{"", 0, true},
		{"-1d", 0, true},
		{"5", 0, true},
		{"3x", 0, true},
		{"d", 0, true},
	}
	for _, tc := range cases {
		got, err := parseSinceDuration(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%q: expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected err %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%q: got %v want %v", tc.in, got, tc.want)
		}
	}
}
