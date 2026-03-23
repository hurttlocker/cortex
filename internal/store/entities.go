package store

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const (
	EntityTypePerson  = "person"
	EntityTypePlace   = "place"
	EntityTypeOrg     = "org"
	EntityTypeConcept = "concept"
)

var (
	entityPronouns = map[string]struct{}{
		"he": {}, "him": {}, "his": {}, "she": {}, "her": {}, "hers": {},
		"they": {}, "them": {}, "their": {}, "theirs": {}, "we": {}, "us": {},
		"our": {}, "ours": {}, "i": {}, "me": {}, "my": {}, "mine": {},
		"you": {}, "your": {}, "yours": {}, "it": {}, "its": {},
	}
	entityNumericRE     = regexp.MustCompile(`^\d+$`)
	entityWhitespaceRE  = regexp.MustCompile(`\s+`)
	entityOrgMarkerRE   = regexp.MustCompile(`(?i)\b(inc|corp|llc|ltd|company|group|foundation|bank|agency|institute|university)\b`)
	entityPlaceMarkerRE = regexp.MustCompile(`(?i)\b(city|state|county|island|river|mountain|lake|park|street|avenue|road|district)\b`)
	entityPersonNameRE  = regexp.MustCompile(`^[A-Z][a-z]+(?: [A-Z][a-z]+){0,2}$`)
	entitySessionRE     = regexp.MustCompile(`(?i)^session\s+\d+\s*[-:]`)
	entitySpeakerLineRE = regexp.MustCompile(`(?i)^\s*([a-z][a-z' -]{0,40})\s*\(d\d+`)
	entityQuotedTrimCut = "\"'`.,;:!?()[]{}<>"
)

func nullableInt64Value(v int64) interface{} {
	if v <= 0 {
		return nil
	}
	return v
}

func normalizeEntityName(raw string) string {
	name := strings.TrimSpace(raw)
	name = strings.Trim(name, entityQuotedTrimCut)
	name = entityWhitespaceRE.ReplaceAllString(name, " ")
	return strings.TrimSpace(name)
}

func normalizeEntityLookupKey(raw string) string {
	return strings.ToLower(normalizeEntityName(raw))
}

func isPronounEntityName(name string) bool {
	_, ok := entityPronouns[normalizeEntityLookupKey(name)]
	return ok
}

func classifyUnresolvedEntityReason(name string) string {
	normalized := normalizeEntityName(name)
	switch {
	case normalized == "":
		return "empty_name"
	case isPronounEntityName(normalized):
		return "pronoun_without_context"
	case entityNumericRE.MatchString(normalized):
		return "numeric_identifier"
	default:
		return "unresolved"
	}
}

func shouldCreateEntityForName(name string) bool {
	normalized := normalizeEntityName(name)
	if normalized == "" || isPronounEntityName(normalized) || entityNumericRE.MatchString(normalized) {
		return false
	}
	runes := []rune(normalized)
	if len(runes) == 1 {
		return unicode.IsUpper(runes[0]) || unicode.IsDigit(runes[0])
	}
	return true
}

func shouldPersistAlias(alias string, entity *Entity) bool {
	normalized := normalizeEntityName(alias)
	if entity == nil || normalized == "" || strings.EqualFold(normalized, entity.CanonicalName) {
		return false
	}
	if isPronounEntityName(normalized) || entityNumericRE.MatchString(normalized) {
		return false
	}
	runes := []rune(normalized)
	if len(runes) == 1 {
		return unicode.IsUpper(runes[0])
	}
	return true
}

func guessEntityType(name string, fact *Fact) string {
	if fact != nil {
		switch strings.ToLower(strings.TrimSpace(fact.FactType)) {
		case "location":
			return EntityTypePlace
		case "identity":
			if entityPersonNameRE.MatchString(name) {
				return EntityTypePerson
			}
		}
	}

	switch {
	case entityOrgMarkerRE.MatchString(name):
		return EntityTypeOrg
	case entityPlaceMarkerRE.MatchString(name):
		return EntityTypePlace
	case entityPersonNameRE.MatchString(name):
		return EntityTypePerson
	default:
		return EntityTypeConcept
	}
}

func unresolvedEntityForFact(f *Fact) *UnresolvedEntity {
	if f == nil || f.EntityID > 0 {
		return nil
	}
	candidate := entityCandidateNameForFact(f)
	reason := classifyUnresolvedEntityReason(candidate)
	if reason == "" || reason == "empty_name" {
		return nil
	}
	return &UnresolvedEntity{
		MemoryID:       f.MemoryID,
		RawName:        normalizeEntityName(candidate),
		NormalizedName: normalizeEntityLookupKey(candidate),
		Reason:         reason,
		SourceQuote:    strings.TrimSpace(f.SourceQuote),
	}
}

func (s *SQLiteStore) ResolveEntity(ctx context.Context, rawName string, opts EntityResolveOptions) (*EntityResolveResult, error) {
	name := normalizeEntityName(rawName)
	if name == "" {
		return &EntityResolveResult{RawName: rawName, Reason: "empty_name"}, nil
	}
	return s.resolveEntityName(ctx, name, opts, guessEntityType(name, nil))
}

func (s *SQLiteStore) resolveEntityForFact(ctx context.Context, fact *Fact) error {
	if fact == nil || fact.EntityID > 0 {
		return nil
	}
	name := entityCandidateNameForFact(fact)
	if name == "" {
		return nil
	}
	resolved, err := s.resolveEntityName(ctx, name, EntityResolveOptions{
		MemoryID:        fact.MemoryID,
		SourceQuote:     fact.SourceQuote,
		ObservedEntity:  fact.ObservedEntity,
		AllowCreate:     true,
		AllowPronounCtx: true,
	}, guessEntityType(name, fact))
	if err != nil {
		return err
	}
	if resolved != nil && resolved.Entity != nil {
		fact.EntityID = resolved.Entity.ID
	}
	return nil
}

func entityCandidateNameForFact(fact *Fact) string {
	if fact == nil {
		return ""
	}

	subject := normalizeEntityName(fact.Subject)
	if subject != "" && !isPronounEntityName(subject) && !looksLikeConversationSessionSubject(subject) {
		return subject
	}
	if speaker := extractConversationSpeakerName(fact.SourceQuote); speaker != "" {
		return speaker
	}
	if speaker := extractConversationSpeakerName(fact.Predicate); speaker != "" {
		return speaker
	}
	return subject
}

func looksLikeConversationSessionSubject(subject string) bool {
	return entitySessionRE.MatchString(normalizeEntityName(subject))
}

func extractConversationSpeakerName(raw string) string {
	matches := entitySpeakerLineRE.FindStringSubmatch(strings.TrimSpace(raw))
	if len(matches) < 2 {
		return ""
	}
	name := normalizeEntityName(matches[1])
	if name == "" {
		return ""
	}
	return strings.Title(strings.ToLower(name))
}

func (s *SQLiteStore) resolveEntityName(ctx context.Context, rawName string, opts EntityResolveOptions, entityType string) (*EntityResolveResult, error) {
	name := normalizeEntityName(rawName)
	if name == "" {
		return &EntityResolveResult{RawName: rawName, Reason: "empty_name"}, nil
	}

	if isPronounEntityName(name) {
		entity, err := s.resolvePronounEntity(ctx, opts)
		if err != nil {
			return nil, err
		}
		if entity == nil {
			return &EntityResolveResult{RawName: name, Reason: "pronoun_without_context"}, nil
		}
		return &EntityResolveResult{Entity: entity, RawName: name, Reason: "pronoun_context"}, nil
	}

	lookups := []func(context.Context, string) (*Entity, error){
		s.getEntityByCanonicalExact,
		s.getEntityByCanonicalInsensitive,
		s.getEntityByAliasExact,
		s.getEntityByAliasInsensitive,
	}

	for idx, lookup := range lookups {
		entity, err := lookup(ctx, name)
		if err != nil {
			return nil, err
		}
		if entity == nil {
			continue
		}
		if shouldPersistAlias(name, entity) {
			if err := s.upsertEntityAlias(ctx, entity.ID, name, "extracted"); err != nil {
				return nil, err
			}
		}
		reason := "exact"
		switch idx {
		case 1:
			reason = "case_insensitive"
		case 2:
			reason = "alias"
		case 3:
			reason = "alias_case_insensitive"
		}
		return &EntityResolveResult{
			Entity:     entity,
			RawName:    name,
			AliasMatch: idx >= 2,
			Reason:     reason,
		}, nil
	}

	if !opts.AllowCreate {
		return &EntityResolveResult{RawName: name, Reason: "not_found"}, nil
	}
	if !shouldCreateEntityForName(name) {
		return &EntityResolveResult{RawName: name, Reason: classifyUnresolvedEntityReason(name)}, nil
	}

	entity, err := s.createEntity(ctx, name, entityType)
	if err != nil {
		return nil, err
	}
	return &EntityResolveResult{Entity: entity, RawName: name, Reason: "created"}, nil
}

func (s *SQLiteStore) resolvePronounEntity(ctx context.Context, opts EntityResolveOptions) (*Entity, error) {
	if !opts.AllowPronounCtx {
		return nil, nil
	}

	if opts.MemoryID > 0 {
		var entityID sql.NullInt64
		err := s.db.QueryRowContext(ctx, `
			SELECT entity_id
			FROM facts
			WHERE memory_id = ?
			  AND entity_id IS NOT NULL
			  AND entity_id > 0
			ORDER BY created_at DESC, id DESC
			LIMIT 1
		`, opts.MemoryID).Scan(&entityID)
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("resolving pronoun from memory facts: %w", err)
		}
		if err == nil && entityID.Valid && entityID.Int64 > 0 {
			return s.GetEntity(ctx, entityID.Int64)
		}
	}

	observed := normalizeEntityName(opts.ObservedEntity)
	if observed == "" && opts.MemoryID > 0 {
		memory, err := s.GetMemory(ctx, opts.MemoryID)
		if err != nil {
			return nil, err
		}
		if memory != nil && memory.Metadata != nil {
			observed = normalizeEntityName(memory.Metadata.ObservedEntity)
		}
	}
	if observed == "" || isPronounEntityName(observed) {
		return nil, nil
	}

	resolved, err := s.resolveEntityName(ctx, observed, EntityResolveOptions{
		MemoryID:        opts.MemoryID,
		AllowCreate:     true,
		AllowPronounCtx: false,
	}, guessEntityType(observed, nil))
	if err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, nil
	}
	return resolved.Entity, nil
}

func (s *SQLiteStore) ListEntities(ctx context.Context, limit int, offset int) ([]EntitySummary, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			e.id,
			e.canonical_name,
			e.type,
			e.profile,
			e.created_at,
			e.updated_at,
			COUNT(DISTINCT a.id) AS alias_count,
			COUNT(DISTINCT CASE WHEN f.superseded_by IS NULL THEN f.id END) AS fact_count
		FROM entities e
		LEFT JOIN entity_aliases a ON a.entity_id = e.id
		LEFT JOIN facts f ON f.entity_id = e.id
		GROUP BY e.id
		ORDER BY LOWER(e.canonical_name), e.id
		LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("listing entities: %w", err)
	}
	defer rows.Close()

	var entities []EntitySummary
	for rows.Next() {
		var summary EntitySummary
		if err := rows.Scan(
			&summary.ID,
			&summary.CanonicalName,
			&summary.Type,
			&summary.Profile,
			&summary.CreatedAt,
			&summary.UpdatedAt,
			&summary.AliasCount,
			&summary.FactCount,
		); err != nil {
			return nil, fmt.Errorf("scanning entity summary: %w", err)
		}
		entities = append(entities, summary)
	}
	return entities, rows.Err()
}

func (s *SQLiteStore) GetEntity(ctx context.Context, id int64) (*Entity, error) {
	return s.queryEntity(ctx, `
		SELECT id, canonical_name, type, profile, created_at, updated_at
		FROM entities
		WHERE id = ?
	`, id)
}

func (s *SQLiteStore) GetEntityByName(ctx context.Context, name string) (*Entity, error) {
	name = normalizeEntityName(name)
	if name == "" {
		return nil, nil
	}
	lookups := []func(context.Context, string) (*Entity, error){
		s.getEntityByCanonicalExact,
		s.getEntityByCanonicalInsensitive,
		s.getEntityByAliasExact,
		s.getEntityByAliasInsensitive,
	}
	for _, lookup := range lookups {
		entity, err := lookup(ctx, name)
		if err != nil {
			return nil, err
		}
		if entity != nil {
			return entity, nil
		}
	}
	return nil, nil
}

func (s *SQLiteStore) RecordUnresolvedEntity(ctx context.Context, unresolved *UnresolvedEntity) (int64, error) {
	if unresolved == nil {
		return 0, fmt.Errorf("unresolved entity is nil")
	}
	if strings.TrimSpace(unresolved.RawName) == "" {
		return 0, nil
	}
	if unresolved.NormalizedName == "" {
		unresolved.NormalizedName = normalizeEntityLookupKey(unresolved.RawName)
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO unresolved_entities (fact_id, memory_id, raw_name, normalized_name, reason, source_quote, resolved_entity_id, resolved_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, nullableInt64Value(unresolved.FactID), unresolved.MemoryID, normalizeEntityName(unresolved.RawName), unresolved.NormalizedName, unresolved.Reason, strings.TrimSpace(unresolved.SourceQuote), nullableInt64Value(unresolved.ResolvedEntityID), unresolved.ResolvedAt)
	if err != nil {
		return 0, fmt.Errorf("recording unresolved entity: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading unresolved entity id: %w", err)
	}
	return id, nil
}

func (s *SQLiteStore) GetFactsByEntityIDs(ctx context.Context, entityIDs []int64, includeSuperseded bool, limit int) ([]*Fact, error) {
	ids := uniquePositiveInt64(entityIDs)
	if len(ids) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 500
	}

	placeholders := make([]string, len(ids))
	args := make([]interface{}, 0, len(ids)+1)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}

	query := fmt.Sprintf(`
		SELECT id, memory_id, entity_id, subject, predicate, object, fact_type, confidence, decay_rate, last_reinforced, source_quote, temporal_norm, created_at, state, superseded_by, agent_id, observer_agent, observed_entity, session_id, project_id, token_estimate
		FROM facts
		WHERE entity_id IN (%s)
	`, strings.Join(placeholders, ","))
	if !includeSuperseded {
		query += " AND superseded_by IS NULL"
	}
	query += " ORDER BY created_at DESC, id DESC"
	query += fmt.Sprintf(" LIMIT %d", limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing facts by entity ids: %w", err)
	}
	defer rows.Close()

	var facts []*Fact
	for rows.Next() {
		f := &Fact{}
		var entityID sql.NullInt64
		var supersededBy sql.NullInt64
		var temporalNorm sql.NullString
		if err := rows.Scan(&f.ID, &f.MemoryID, &entityID, &f.Subject, &f.Predicate, &f.Object, &f.FactType, &f.Confidence, &f.DecayRate, &f.LastReinforced, &f.SourceQuote, &temporalNorm, &f.CreatedAt, &f.State, &supersededBy, &f.AgentID, &f.ObserverAgent, &f.ObservedEntity, &f.SessionID, &f.ProjectID, &f.TokenEstimate); err != nil {
			return nil, fmt.Errorf("scanning entity fact row: %w", err)
		}
		if entityID.Valid {
			f.EntityID = entityID.Int64
		}
		if supersededBy.Valid {
			v := supersededBy.Int64
			f.SupersededBy = &v
		}
		f.TemporalNorm = unmarshalTemporalNorm(temporalNorm)
		f.ObserverAgent = effectiveFactObserver(f)
		facts = append(facts, f)
	}
	return facts, rows.Err()
}

func (s *SQLiteStore) MergeEntities(ctx context.Context, keepEntityID, mergeEntityID int64) error {
	if keepEntityID <= 0 || mergeEntityID <= 0 {
		return fmt.Errorf("entity ids must be > 0")
	}
	if keepEntityID == mergeEntityID {
		return fmt.Errorf("cannot merge an entity into itself")
	}

	keepEntity, err := s.GetEntity(ctx, keepEntityID)
	if err != nil {
		return err
	}
	if keepEntity == nil {
		return fmt.Errorf("entity %d not found", keepEntityID)
	}
	mergeEntity, err := s.GetEntity(ctx, mergeEntityID)
	if err != nil {
		return err
	}
	if mergeEntity == nil {
		return fmt.Errorf("entity %d not found", mergeEntityID)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin entity merge: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `UPDATE facts SET entity_id = ? WHERE entity_id = ?`, keepEntityID, mergeEntityID); err != nil {
		return fmt.Errorf("reassigning facts during entity merge: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO entity_aliases (entity_id, alias, source)
		SELECT ?, alias, source
		FROM entity_aliases
		WHERE entity_id = ?
	`, keepEntityID, mergeEntityID); err != nil {
		return fmt.Errorf("reassigning aliases during entity merge: %w", err)
	}
	if shouldPersistAlias(mergeEntity.CanonicalName, keepEntity) {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO entity_aliases (entity_id, alias, source)
			VALUES (?, ?, 'manual')
		`, keepEntityID, normalizeEntityName(mergeEntity.CanonicalName)); err != nil {
			return fmt.Errorf("persisting merged canonical alias: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM entity_aliases WHERE entity_id = ?`, mergeEntityID); err != nil {
		return fmt.Errorf("cleaning merged entity aliases: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE unresolved_entities
		SET resolved_entity_id = ?, resolved_at = CURRENT_TIMESTAMP
		WHERE resolved_entity_id = ? OR normalized_name IN (
			SELECT LOWER(canonical_name) FROM entities WHERE id = ?
			UNION
			SELECT LOWER(alias) FROM entity_aliases WHERE entity_id = ?
		)
	`, keepEntityID, mergeEntityID, mergeEntityID, mergeEntityID); err != nil {
		return fmt.Errorf("updating unresolved rows during entity merge: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM entities WHERE id = ?`, mergeEntityID); err != nil {
		return fmt.Errorf("deleting merged entity: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE entities SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, keepEntityID); err != nil {
		return fmt.Errorf("touching merged entity: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit entity merge: %w", err)
	}
	_, err = s.RebuildEntityProfile(ctx, keepEntityID)
	return err
}

func (s *SQLiteStore) BackfillFactEntities(ctx context.Context, limit int) (int, int, error) {
	query := `
		SELECT id, memory_id, entity_id, subject, predicate, object, fact_type, confidence, decay_rate, last_reinforced, source_quote, temporal_norm, created_at, state, superseded_by, agent_id, observer_agent, observed_entity, session_id, project_id, token_estimate
		FROM facts
		WHERE COALESCE(entity_id, 0) = 0
		ORDER BY memory_id, id
	`
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return 0, 0, fmt.Errorf("querying facts for entity backfill: %w", err)
	}
	defer rows.Close()

	candidates := make([]*Fact, 0, 256)
	for rows.Next() {
		fact := &Fact{}
		var entityID sql.NullInt64
		var supersededBy sql.NullInt64
		var temporalNorm sql.NullString
		if err := rows.Scan(&fact.ID, &fact.MemoryID, &entityID, &fact.Subject, &fact.Predicate, &fact.Object, &fact.FactType, &fact.Confidence, &fact.DecayRate, &fact.LastReinforced, &fact.SourceQuote, &temporalNorm, &fact.CreatedAt, &fact.State, &supersededBy, &fact.AgentID, &fact.ObserverAgent, &fact.ObservedEntity, &fact.SessionID, &fact.ProjectID, &fact.TokenEstimate); err != nil {
			return 0, 0, fmt.Errorf("scanning fact for entity backfill: %w", err)
		}
		if entityID.Valid {
			fact.EntityID = entityID.Int64
		}
		if supersededBy.Valid {
			v := supersededBy.Int64
			fact.SupersededBy = &v
		}
		fact.TemporalNorm = unmarshalTemporalNorm(temporalNorm)
		fact.ObserverAgent = effectiveFactObserver(fact)
		candidates = append(candidates, fact)
	}
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("iterating entity backfill rows: %w", err)
	}

	resolvedCount := 0
	unresolvedCount := 0
	touchedEntities := make(map[int64]struct{})

	for _, fact := range candidates {
		if err := s.resolveEntityForFact(ctx, fact); err != nil {
			return resolvedCount, unresolvedCount, err
		}
		if fact.EntityID > 0 {
			if _, err := s.db.ExecContext(ctx, `UPDATE facts SET entity_id = ? WHERE id = ?`, fact.EntityID, fact.ID); err != nil {
				return resolvedCount, unresolvedCount, fmt.Errorf("updating fact %d entity_id: %w", fact.ID, err)
			}
			resolvedCount++
			touchedEntities[fact.EntityID] = struct{}{}
			continue
		}

		unresolved := unresolvedEntityForFact(fact)
		if unresolved == nil {
			continue
		}
		var existing int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM unresolved_entities WHERE fact_id = ?`, fact.ID).Scan(&existing); err != nil {
			return resolvedCount, unresolvedCount, fmt.Errorf("checking unresolved entity for fact %d: %w", fact.ID, err)
		}
		if existing == 0 {
			unresolved.FactID = fact.ID
			if _, err := s.RecordUnresolvedEntity(ctx, unresolved); err != nil {
				return resolvedCount, unresolvedCount, err
			}
			unresolvedCount++
		}
	}

	for entityID := range touchedEntities {
		if _, err := s.RebuildEntityProfile(ctx, entityID); err != nil {
			return resolvedCount, unresolvedCount, err
		}
	}
	return resolvedCount, unresolvedCount, nil
}

func (s *SQLiteStore) RebuildEntityProfile(ctx context.Context, entityID int64) (string, error) {
	entity, err := s.GetEntity(ctx, entityID)
	if err != nil {
		return "", err
	}
	if entity == nil {
		return "", fmt.Errorf("entity %d not found", entityID)
	}

	aliases, err := s.ListEntityAliases(ctx, entityID)
	if err != nil {
		return "", err
	}
	facts, err := s.GetFactsByEntityIDs(ctx, []int64{entityID}, false, 200)
	if err != nil {
		return "", err
	}
	sort.SliceStable(facts, func(i, j int) bool {
		if facts[i].Confidence == facts[j].Confidence {
			if facts[i].CreatedAt.Equal(facts[j].CreatedAt) {
				return facts[i].ID > facts[j].ID
			}
			return facts[i].CreatedAt.After(facts[j].CreatedAt)
		}
		return facts[i].Confidence > facts[j].Confidence
	})

	var buf bytes.Buffer
	buf.WriteString("# ")
	buf.WriteString(entity.CanonicalName)
	buf.WriteString("\n\n")
	buf.WriteString("- Type: ")
	buf.WriteString(entity.Type)
	buf.WriteString("\n")

	if len(aliases) > 0 {
		aliasNames := make([]string, 0, len(aliases))
		for _, alias := range aliases {
			aliasNames = append(aliasNames, alias.Alias)
		}
		sort.Slice(aliasNames, func(i, j int) bool {
			return strings.ToLower(aliasNames[i]) < strings.ToLower(aliasNames[j])
		})
		buf.WriteString("- Aliases: ")
		buf.WriteString(strings.Join(aliasNames, ", "))
		buf.WriteString("\n")
	}

	buf.WriteString("\n## Facts\n")
	if len(facts) == 0 {
		buf.WriteString("- No linked facts yet.\n")
	} else {
		for _, fact := range facts {
			subject := strings.TrimSpace(fact.Subject)
			if looksLikeConversationSessionSubject(subject) {
				subject = entity.CanonicalName
			}
			line := strings.TrimSpace(strings.Join([]string{subject, fact.Predicate, fact.Object}, " "))
			if line == "" {
				continue
			}
			buf.WriteString("- ")
			buf.WriteString(line)
			if fact.SourceQuote != "" {
				buf.WriteString("  `")
				buf.WriteString(strings.TrimSpace(fact.SourceQuote))
				buf.WriteString("`")
			}
			buf.WriteString("\n")
		}
	}

	profile := strings.TrimSpace(buf.String()) + "\n"
	if _, err := s.db.ExecContext(ctx, `
		UPDATE entities
		SET profile = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, profile, entityID); err != nil {
		return "", fmt.Errorf("updating entity profile: %w", err)
	}
	return profile, nil
}

func (s *SQLiteStore) createEntity(ctx context.Context, canonicalName string, entityType string) (*Entity, error) {
	canonicalName = normalizeEntityName(canonicalName)
	if canonicalName == "" {
		return nil, fmt.Errorf("canonical entity name is required")
	}
	if entityType == "" {
		entityType = EntityTypeConcept
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO entities (canonical_name, type)
		VALUES (?, ?)
	`, canonicalName, entityType)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return s.getEntityByCanonicalInsensitive(ctx, canonicalName)
		}
		return nil, fmt.Errorf("creating entity %q: %w", canonicalName, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("reading entity id: %w", err)
	}
	return s.GetEntity(ctx, id)
}

func (s *SQLiteStore) queryEntity(ctx context.Context, query string, args ...interface{}) (*Entity, error) {
	entity := &Entity{}
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&entity.ID,
		&entity.CanonicalName,
		&entity.Type,
		&entity.Profile,
		&entity.CreatedAt,
		&entity.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return entity, nil
}

func (s *SQLiteStore) getEntityByCanonicalExact(ctx context.Context, name string) (*Entity, error) {
	return s.queryEntity(ctx, `
		SELECT id, canonical_name, type, profile, created_at, updated_at
		FROM entities
		WHERE canonical_name = ?
		LIMIT 1
	`, normalizeEntityName(name))
}

func (s *SQLiteStore) getEntityByCanonicalInsensitive(ctx context.Context, name string) (*Entity, error) {
	return s.queryEntity(ctx, `
		SELECT id, canonical_name, type, profile, created_at, updated_at
		FROM entities
		WHERE canonical_name = ?
		   OR LOWER(canonical_name) = LOWER(?)
		ORDER BY CASE WHEN canonical_name = ? THEN 0 ELSE 1 END, id
		LIMIT 1
	`, normalizeEntityName(name), normalizeEntityName(name), normalizeEntityName(name))
}

func (s *SQLiteStore) getEntityByAliasExact(ctx context.Context, name string) (*Entity, error) {
	return s.queryEntity(ctx, `
		SELECT e.id, e.canonical_name, e.type, e.profile, e.created_at, e.updated_at
		FROM entities e
		JOIN entity_aliases a ON a.entity_id = e.id
		WHERE a.alias = ?
		LIMIT 1
	`, normalizeEntityName(name))
}

func (s *SQLiteStore) getEntityByAliasInsensitive(ctx context.Context, name string) (*Entity, error) {
	return s.queryEntity(ctx, `
		SELECT e.id, e.canonical_name, e.type, e.profile, e.created_at, e.updated_at
		FROM entities e
		JOIN entity_aliases a ON a.entity_id = e.id
		WHERE a.alias = ?
		   OR LOWER(a.alias) = LOWER(?)
		ORDER BY CASE WHEN a.alias = ? THEN 0 ELSE 1 END, e.id
		LIMIT 1
	`, normalizeEntityName(name), normalizeEntityName(name), normalizeEntityName(name))
}
