package ingest

import "testing"

func TestTimestampStartFromSourceSection(t *testing.T) {
	tests := []struct {
		name    string
		section string
		want    string
	}{
		{
			name:    "session with time and date",
			section: "Session 7 - 7:28 pm on 23 March, 2023",
			want:    "2023-03-23T19:28:00Z",
		},
		{
			name:    "date only iso",
			section: "Session 1 - 2023-05-07",
			want:    "2023-05-07T00:00:00Z",
		},
		{
			name:    "unparseable",
			section: "Trading Systems",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := timestampStartFromSourceSection(tt.section); got != tt.want {
				t.Fatalf("timestampStartFromSourceSection(%q) = %q, want %q", tt.section, got, tt.want)
			}
		})
	}
}
