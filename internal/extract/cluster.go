package extract

import (
	"sort"
	"strings"
)

const (
	strongCooccurrenceThreshold = 2
	minClusterSubjects          = 3
)

// ClusterFact is the minimal fact shape needed for topic clustering.
type ClusterFact struct {
	ID         int64
	MemoryID   int64
	Subject    string
	Confidence float64
}

// TopicCluster is a detected topical community of related subjects/facts.
type TopicCluster struct {
	Name          string
	Aliases       []string
	TopSubjects   []string
	Subjects      []string
	SubjectKeys   []string
	FactIDs       []int64
	FactCount     int
	AvgConfidence float64
	Cohesion      float64
}

// ClusterBuildResult contains all detected clusters and summary metadata.
type ClusterBuildResult struct {
	Clusters           []TopicCluster
	UnclusteredFactIDs []int64
	TotalSubjects      int
}

// BuildTopicClusters detects topic communities from fact co-occurrence by memory.
//
// Algorithm:
//  1. Build subject co-occurrence edges from shared memories.
//  2. Find connected components using "strong" co-occurrence edges (weight >= 2).
//  3. Merge tiny components (<3 subjects) into nearest neighbors by edge weight.
//  4. Name/score clusters and return deterministic ordering.
func BuildTopicClusters(facts []ClusterFact) ClusterBuildResult {
	subjectDisplay := make(map[string]string)
	subjectFreq := make(map[string]int)
	subjectConfSum := make(map[string]float64)
	subjectFactIDs := make(map[string][]int64)
	memorySubjects := make(map[int64]map[string]struct{})

	unclustered := make([]int64, 0)

	for _, fact := range facts {
		subjectKey := normalizeSubjectKey(fact.Subject)
		if subjectKey == "" {
			if fact.ID > 0 {
				unclustered = append(unclustered, fact.ID)
			}
			continue
		}

		if _, ok := subjectDisplay[subjectKey]; !ok {
			subjectDisplay[subjectKey] = strings.TrimSpace(fact.Subject)
		}
		subjectFreq[subjectKey]++
		subjectConfSum[subjectKey] += fact.Confidence
		if fact.ID > 0 {
			subjectFactIDs[subjectKey] = append(subjectFactIDs[subjectKey], fact.ID)
		}

		if fact.MemoryID > 0 {
			if _, ok := memorySubjects[fact.MemoryID]; !ok {
				memorySubjects[fact.MemoryID] = make(map[string]struct{})
			}
			memorySubjects[fact.MemoryID][subjectKey] = struct{}{}
		}
	}

	edges := buildCooccurrenceWeights(memorySubjects)
	components := connectedComponents(subjectDisplay, edges, strongCooccurrenceThreshold)
	components = mergeSmallComponents(components, edges)

	clusters := make([]TopicCluster, 0, len(components))
	for _, component := range components {
		if len(component) == 0 {
			continue
		}

		subjectKeys := mapKeys(component)
		sortSubjectsByFrequency(subjectKeys, subjectFreq, subjectDisplay)

		subjects := make([]string, 0, len(subjectKeys))
		for _, key := range subjectKeys {
			subjects = append(subjects, subjectDisplay[key])
		}

		name := ""
		if len(subjects) > 0 {
			name = subjects[0]
		}

		aliases := make([]string, 0)
		if len(subjects) > 1 {
			aliases = append(aliases, subjects[1:]...)
			if len(aliases) > 8 {
				aliases = aliases[:8]
			}
		}

		topSubjects := subjects
		if len(topSubjects) > 5 {
			topSubjects = topSubjects[:5]
		}

		factIDSet := make(map[int64]struct{})
		factCount := 0
		confSum := 0.0
		for _, key := range subjectKeys {
			factCount += subjectFreq[key]
			confSum += subjectConfSum[key]
			for _, id := range subjectFactIDs[key] {
				if id <= 0 {
					continue
				}
				factIDSet[id] = struct{}{}
			}
		}

		factIDs := make([]int64, 0, len(factIDSet))
		for id := range factIDSet {
			factIDs = append(factIDs, id)
		}
		sort.Slice(factIDs, func(i, j int) bool { return factIDs[i] < factIDs[j] })

		avgConfidence := 0.0
		if factCount > 0 {
			avgConfidence = confSum / float64(factCount)
		}

		clusters = append(clusters, TopicCluster{
			Name:          name,
			Aliases:       aliases,
			TopSubjects:   append([]string(nil), topSubjects...),
			Subjects:      subjects,
			SubjectKeys:   append([]string(nil), subjectKeys...),
			FactIDs:       factIDs,
			FactCount:     factCount,
			AvgConfidence: avgConfidence,
			Cohesion:      componentCohesion(subjectKeys, edges),
		})
	}

	sort.Slice(clusters, func(i, j int) bool {
		if clusters[i].FactCount != clusters[j].FactCount {
			return clusters[i].FactCount > clusters[j].FactCount
		}
		if clusters[i].Cohesion != clusters[j].Cohesion {
			return clusters[i].Cohesion > clusters[j].Cohesion
		}
		return strings.ToLower(clusters[i].Name) < strings.ToLower(clusters[j].Name)
	})

	sort.Slice(unclustered, func(i, j int) bool { return unclustered[i] < unclustered[j] })

	return ClusterBuildResult{
		Clusters:           clusters,
		UnclusteredFactIDs: unclustered,
		TotalSubjects:      len(subjectDisplay),
	}
}

func normalizeSubjectKey(subject string) string {
	normalized := strings.ToLower(strings.TrimSpace(subject))
	if normalized == "" {
		return ""
	}
	return strings.Join(strings.Fields(normalized), " ")
}

func buildCooccurrenceWeights(memorySubjects map[int64]map[string]struct{}) map[string]map[string]int {
	edges := make(map[string]map[string]int)
	for _, subjectSet := range memorySubjects {
		subjects := mapKeys(subjectSet)
		sort.Strings(subjects)
		for i := 0; i < len(subjects)-1; i++ {
			for j := i + 1; j < len(subjects); j++ {
				a := subjects[i]
				b := subjects[j]
				if edges[a] == nil {
					edges[a] = make(map[string]int)
				}
				if edges[b] == nil {
					edges[b] = make(map[string]int)
				}
				edges[a][b]++
				edges[b][a]++
			}
		}
	}
	return edges
}

func connectedComponents(subjectDisplay map[string]string, edges map[string]map[string]int, minWeight int) []map[string]struct{} {
	subjects := make([]string, 0, len(subjectDisplay))
	for key := range subjectDisplay {
		subjects = append(subjects, key)
	}
	sort.Strings(subjects)

	visited := make(map[string]bool, len(subjects))
	components := make([]map[string]struct{}, 0)

	for _, start := range subjects {
		if visited[start] {
			continue
		}
		component := make(map[string]struct{})
		stack := []string{start}
		visited[start] = true

		for len(stack) > 0 {
			current := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			component[current] = struct{}{}

			neighbors := edges[current]
			for next, weight := range neighbors {
				if weight < minWeight || visited[next] {
					continue
				}
				visited[next] = true
				stack = append(stack, next)
			}
		}

		components = append(components, component)
	}

	return components
}

func mergeSmallComponents(components []map[string]struct{}, edges map[string]map[string]int) []map[string]struct{} {
	for {
		changed := false
		for i := 0; i < len(components); i++ {
			if len(components[i]) == 0 || len(components[i]) >= minClusterSubjects {
				continue
			}

			bestIdx := -1
			bestScore := 0
			for j := 0; j < len(components); j++ {
				if i == j || len(components[j]) == 0 {
					continue
				}
				score := componentLinkWeight(components[i], components[j], edges)
				if score > bestScore {
					bestScore = score
					bestIdx = j
					continue
				}
				if score == bestScore && score > 0 && bestIdx >= 0 {
					if len(components[j]) > len(components[bestIdx]) {
						bestIdx = j
					}
				}
			}

			if bestIdx >= 0 && bestScore > 0 {
				for subject := range components[i] {
					components[bestIdx][subject] = struct{}{}
				}
				components[i] = map[string]struct{}{}
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	merged := make([]map[string]struct{}, 0, len(components))
	for _, component := range components {
		if len(component) == 0 {
			continue
		}
		merged = append(merged, component)
	}
	return merged
}

func componentLinkWeight(a, b map[string]struct{}, edges map[string]map[string]int) int {
	total := 0
	for sa := range a {
		for sb := range b {
			total += edges[sa][sb]
		}
	}
	return total
}

func componentCohesion(subjectKeys []string, edges map[string]map[string]int) float64 {
	n := len(subjectKeys)
	if n <= 1 {
		return 1.0
	}

	possible := n * (n - 1) / 2
	if possible == 0 {
		return 1.0
	}

	actual := 0
	for i := 0; i < n-1; i++ {
		for j := i + 1; j < n; j++ {
			if edges[subjectKeys[i]][subjectKeys[j]] > 0 {
				actual++
			}
		}
	}

	return float64(actual) / float64(possible)
}

func sortSubjectsByFrequency(subjectKeys []string, subjectFreq map[string]int, subjectDisplay map[string]string) {
	sort.Slice(subjectKeys, func(i, j int) bool {
		a := subjectKeys[i]
		b := subjectKeys[j]
		if subjectFreq[a] != subjectFreq[b] {
			return subjectFreq[a] > subjectFreq[b]
		}
		return strings.ToLower(subjectDisplay[a]) < strings.ToLower(subjectDisplay[b])
	})
}

func mapKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}
