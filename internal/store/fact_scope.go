package store

import "strings"

// ApplyMemoryScopeToFact copies stable scope context from a memory onto a fact.
// It is best-effort and preserves any fact fields already set by the caller.
func ApplyMemoryScopeToFact(memory *Memory, fact *Fact) {
	if memory == nil || fact == nil {
		return
	}

	if fact.ProjectID == "" {
		fact.ProjectID = strings.TrimSpace(memory.Project)
	}

	if memory.Metadata == nil {
		if fact.ObserverAgent == "" {
			fact.ObserverAgent = strings.TrimSpace(fact.AgentID)
		}
		return
	}

	if fact.AgentID == "" {
		fact.AgentID = strings.TrimSpace(memory.Metadata.AgentID)
	}
	if fact.ObserverAgent == "" {
		fact.ObserverAgent = strings.TrimSpace(memory.Metadata.AgentID)
	}
	if fact.ObservedEntity == "" {
		fact.ObservedEntity = strings.TrimSpace(memory.Metadata.ObservedEntity)
	}
	if fact.SessionID == "" {
		fact.SessionID = strings.TrimSpace(memory.Metadata.SessionID)
	}
	if fact.SessionID == "" {
		fact.SessionID = strings.TrimSpace(memory.Metadata.SessionKey)
	}
}
