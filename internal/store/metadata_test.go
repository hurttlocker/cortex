package store

import (
	"context"
	"testing"
)

func TestMetadataRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	meta := &Metadata{
		SessionKey:   "agent:main:main",
		Channel:      "discord",
		ChannelID:    "1473406695219658964",
		ChannelName:  "#x",
		AgentID:      "main",
		AgentName:    "mister",
		Model:        "anthropic/claude-opus-4-6",
		InputTokens:  8200,
		OutputTokens: 4250,
		MessageCount: 4,
		Surface:      "discord",
		ChatType:     "channel",
	}

	id, err := s.AddMemory(ctx, &Memory{
		Content:     "Test memory with metadata",
		SourceFile:  "test.md",
		ContentHash: "meta-test-hash-001",
		Metadata:    meta,
	})
	if err != nil {
		t.Fatalf("AddMemory with metadata: %v", err)
	}

	got, err := s.GetMemory(ctx, id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}

	if got.Metadata == nil {
		t.Fatal("metadata is nil after round-trip")
	}

	if got.Metadata.SessionKey != "agent:main:main" {
		t.Errorf("session_key = %q, want %q", got.Metadata.SessionKey, "agent:main:main")
	}
	if got.Metadata.AgentID != "main" {
		t.Errorf("agent_id = %q, want %q", got.Metadata.AgentID, "main")
	}
	if got.Metadata.Channel != "discord" {
		t.Errorf("channel = %q, want %q", got.Metadata.Channel, "discord")
	}
	if got.Metadata.Model != "anthropic/claude-opus-4-6" {
		t.Errorf("model = %q, want %q", got.Metadata.Model, "anthropic/claude-opus-4-6")
	}
	if got.Metadata.InputTokens != 8200 {
		t.Errorf("input_tokens = %d, want %d", got.Metadata.InputTokens, 8200)
	}
}

func TestMetadataNil(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, err := s.AddMemory(ctx, &Memory{
		Content:     "Memory without metadata",
		SourceFile:  "test.md",
		ContentHash: "no-meta-hash-001",
	})
	if err != nil {
		t.Fatalf("AddMemory without metadata: %v", err)
	}

	got, err := s.GetMemory(ctx, id)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}

	if got.Metadata != nil {
		t.Errorf("expected nil metadata, got %+v", got.Metadata)
	}
}

func TestParseMetadataJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantNil bool
		wantErr bool
		check   func(*Metadata) bool
	}{
		{
			name:    "empty string",
			input:   "",
			wantNil: true,
		},
		{
			name:  "valid JSON",
			input: `{"agent_id": "sage", "channel": "telegram"}`,
			check: func(m *Metadata) bool {
				return m.AgentID == "sage" && m.Channel == "telegram"
			},
		},
		{
			name:    "invalid JSON",
			input:   `{bad json`,
			wantErr: true,
		},
		{
			name:  "partial fields",
			input: `{"model": "gpt-4o", "input_tokens": 5000}`,
			check: func(m *Metadata) bool {
				return m.Model == "gpt-4o" && m.InputTokens == 5000
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMetadataJSON(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantNil {
				if got != nil {
					t.Errorf("expected nil, got %+v", got)
				}
				return
			}
			if tt.check != nil && !tt.check(got) {
				t.Errorf("check failed for %+v", got)
			}
		})
	}
}

func TestBuildMetadataPrefix(t *testing.T) {
	tests := []struct {
		name string
		meta *Metadata
		want string
	}{
		{
			name: "nil metadata",
			meta: nil,
			want: "",
		},
		{
			name: "full metadata",
			meta: &Metadata{
				AgentID:        "mister",
				Channel:        "discord",
				ChannelName:    "#x",
				Model:          "opus-4.6",
				TimestampStart: "2026-02-18T11:43:00Z",
			},
			want: "agent:mister channel:discord channel:#x model:opus-4.6 date:2026-02-18\n",
		},
		{
			name: "agent only",
			meta: &Metadata{AgentID: "sage"},
			want: "agent:sage\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildMetadataPrefix(tt.meta)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestListMemoriesWithMetadataFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Add memories with different agents
	for _, tc := range []struct {
		content string
		hash    string
		agentID string
		channel string
	}{
		{"Mister trading analysis", "meta-filter-1", "main", "discord"},
		{"Sage research report", "meta-filter-2", "sage", "discord"},
		{"Hawk security scan", "meta-filter-3", "hawk", "telegram"},
		{"No metadata memory", "meta-filter-4", "", ""},
	} {
		meta := (*Metadata)(nil)
		if tc.agentID != "" {
			meta = &Metadata{AgentID: tc.agentID, Channel: tc.channel}
		}
		_, err := s.AddMemory(ctx, &Memory{
			Content:     tc.content,
			SourceFile:  "test.md",
			ContentHash: tc.hash,
			Metadata:    meta,
		})
		if err != nil {
			t.Fatalf("AddMemory: %v", err)
		}
	}

	// Filter by agent
	memories, err := s.ListMemories(ctx, ListOpts{Limit: 10, Agent: "sage"})
	if err != nil {
		t.Fatalf("ListMemories with agent filter: %v", err)
	}
	if len(memories) != 1 {
		t.Errorf("expected 1 result for agent=sage, got %d", len(memories))
	}

	// Filter by channel
	memories, err = s.ListMemories(ctx, ListOpts{Limit: 10, Channel: "telegram"})
	if err != nil {
		t.Fatalf("ListMemories with channel filter: %v", err)
	}
	if len(memories) != 1 {
		t.Errorf("expected 1 result for channel=telegram, got %d", len(memories))
	}
}

func TestSearchFTSWithMetadata_NullMemoryClass(t *testing.T) {
	s := newTestStore(t)
	ss := s.(*SQLiteStore)
	ctx := context.Background()

	id, err := s.AddMemory(ctx, &Memory{
		Content:     "metadata search null class regression",
		SourceFile:  "meta-null.md",
		ContentHash: HashContentOnly("metadata search null class regression"),
		Metadata:    &Metadata{AgentID: "main", Channel: "discord"},
	})
	if err != nil {
		t.Fatalf("AddMemory: %v", err)
	}

	if _, err := ss.db.ExecContext(ctx, `UPDATE memories SET memory_class = NULL WHERE id = ?`, id); err != nil {
		t.Fatalf("setting NULL memory_class: %v", err)
	}

	results, err := ss.SearchFTSWithMetadata(ctx, "null class", 10, "", MetadataSearchFilters{Agent: "main"})
	if err != nil {
		t.Fatalf("SearchFTSWithMetadata with NULL memory_class error: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if results[0].Memory.MemoryClass != "" {
		t.Fatalf("expected empty class for NULL memory_class, got %q", results[0].Memory.MemoryClass)
	}
}
