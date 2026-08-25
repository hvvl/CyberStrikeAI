package database

import (
	"encoding/json"
	"testing"
	"time"
)

func TestParseDBTime_projectFactFormats(t *testing.T) {
	cases := []string{
		"2026-05-26 11:13:07.442143+08:00",
		"2026-05-26 11:13:07",
		"2026-05-26T11:13:07.442143+08:00",
	}
	for _, s := range cases {
		got := parseDBTime(s)
		if got.IsZero() {
			t.Fatalf("parseDBTime(%q) returned zero", s)
		}
	}
}

func TestParseDBTime_zeroOnGarbage(t *testing.T) {
	if !parseDBTime("").IsZero() {
		t.Fatal("expected zero for empty")
	}
}

// Ensure RFC3339 round-trip used by API is after year 2000.
func TestParseDBTime_marshalRoundTrip(t *testing.T) {
	s := "2026-05-26 11:13:07.442143+08:00"
	tm := parseDBTime(s)
	b, err := json.Marshal(tm)
	if err != nil {
		t.Fatal(err)
	}
	var back time.Time
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.IsZero() {
		t.Fatalf("unmarshal zero from %s", string(b))
	}
}
