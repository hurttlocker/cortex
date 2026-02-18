// Package ann provides Approximate Nearest Neighbor search using HNSW
// (Hierarchical Navigable Small World graphs).
//
// This is a pure Go implementation with zero CGO dependencies, following
// the algorithm from Malkov & Yashunin (2018):
// "Efficient and robust approximate nearest neighbor using Hierarchical
// Navigable Small World graphs" — https://arxiv.org/abs/1603.09320
//
// Designed for Cortex's embedding search: 768-dim vectors, 1K–100K+ scale.
// At 1K memories brute-force is fine (<10ms), but at 50K+ HNSW gives
// O(log N) query time vs O(N) brute-force.
package ann

import (
	"math"
	"math/rand"
	"sort"
	"sync"
)

// Index is an in-memory HNSW index for approximate nearest neighbor search.
type Index struct {
	mu         sync.RWMutex
	nodes      []node
	idToIdx    map[int64]int // memory ID → node index
	entryPoint int           // index of entry point node (-1 if empty)
	maxLevel   int           // current max level in the graph
	dims       int           // vector dimensionality

	// Tuning parameters
	M              int     // max connections per layer (default: 16)
	Mmax0          int     // max connections for layer 0 (default: 2*M)
	EfConstruction int     // build-time beam width (default: 200)
	EfSearch       int     // search-time beam width (default: 50)
	LevelMult      float64 // level generation multiplier: 1/ln(M)

	rng *rand.Rand
}

// node represents a single vector in the HNSW graph.
type node struct {
	id      int64       // external memory ID
	vector  []float32   // embedding vector
	friends [][]int     // friends[layer] = sorted list of neighbor node indices
	level   int         // max level for this node
}

// Result represents a search result with distance.
type Result struct {
	ID       int64   // memory ID
	Distance float32 // cosine distance (1 - similarity); lower = more similar
}

// candidate is used in the priority queue during search.
type candidate struct {
	idx  int     // node index
	dist float32 // distance to query
}

// DefaultM is the default max connections per layer.
const DefaultM = 16

// DefaultEfConstruction is the default build-time beam width.
const DefaultEfConstruction = 200

// DefaultEfSearch is the default search-time beam width.
const DefaultEfSearch = 50

// New creates a new HNSW index with the given vector dimensionality.
func New(dims int) *Index {
	return NewWithParams(dims, DefaultM, DefaultEfConstruction, DefaultEfSearch)
}

// NewWithParams creates a new HNSW index with custom parameters.
func NewWithParams(dims, m, efConstruction, efSearch int) *Index {
	if m < 2 {
		m = 2
	}
	return &Index{
		dims:           dims,
		M:              m,
		Mmax0:          2 * m,
		EfConstruction: efConstruction,
		EfSearch:       efSearch,
		LevelMult:      1.0 / math.Log(float64(m)),
		entryPoint:     -1,
		maxLevel:       -1,
		idToIdx:        make(map[int64]int),
		rng:            rand.New(rand.NewSource(42)),
	}
}

// Len returns the number of vectors in the index.
func (idx *Index) Len() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.nodes)
}

// Insert adds a vector to the index with the given external ID.
// If the ID already exists, it's a no-op.
func (idx *Index) Insert(id int64, vector []float32) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if _, exists := idx.idToIdx[id]; exists {
		return
	}

	nodeIdx := len(idx.nodes)
	level := idx.randomLevel()

	n := node{
		id:      id,
		vector:  vector,
		friends: make([][]int, level+1),
		level:   level,
	}

	idx.nodes = append(idx.nodes, n)
	idx.idToIdx[id] = nodeIdx

	// First node — just set as entry point
	if idx.entryPoint == -1 {
		idx.entryPoint = nodeIdx
		idx.maxLevel = level
		return
	}

	// Greedy search from top layer down to node's level + 1
	ep := idx.entryPoint
	for l := idx.maxLevel; l > level; l-- {
		ep = idx.greedyClosest(vector, ep, l)
	}

	// For each layer from min(level, maxLevel) down to 0:
	// search with efConstruction, select neighbors, create bidirectional links
	topLayer := level
	if topLayer > idx.maxLevel {
		topLayer = idx.maxLevel
	}

	for l := topLayer; l >= 0; l-- {
		candidates := idx.searchLayer(vector, ep, idx.EfConstruction, l)

		// Select M best neighbors
		maxConn := idx.M
		if l == 0 {
			maxConn = idx.Mmax0
		}
		neighbors := idx.selectNeighbors(candidates, maxConn)

		// Set forward links
		idx.nodes[nodeIdx].friends[l] = neighbors

		// Set reverse links (bidirectional)
		for _, neighborIdx := range neighbors {
			idx.nodes[neighborIdx].friends[l] = append(idx.nodes[neighborIdx].friends[l], nodeIdx)

			// Prune if neighbor has too many connections
			if len(idx.nodes[neighborIdx].friends[l]) > maxConn {
				idx.nodes[neighborIdx].friends[l] = idx.shrinkNeighbors(
					neighborIdx, idx.nodes[neighborIdx].friends[l], maxConn, l,
				)
			}
		}

		// Update entry point for next layer
		if len(candidates) > 0 {
			ep = candidates[0].idx
		}
	}

	// Update entry point if new node has higher level
	if level > idx.maxLevel {
		idx.entryPoint = nodeIdx
		idx.maxLevel = level
	}
}

// Search finds the K nearest neighbors to the query vector.
// Returns results sorted by distance (ascending — closest first).
func (idx *Index) Search(query []float32, k int) []Result {
	return idx.SearchEf(query, k, idx.EfSearch)
}

// SearchEf finds the K nearest neighbors with a custom ef (beam width).
// Higher ef = better recall but slower. ef must be >= k.
func (idx *Index) SearchEf(query []float32, k, ef int) []Result {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	if len(idx.nodes) == 0 || idx.entryPoint == -1 {
		return nil
	}

	if ef < k {
		ef = k
	}

	// Greedy descent from top layer to layer 1
	ep := idx.entryPoint
	for l := idx.maxLevel; l > 0; l-- {
		ep = idx.greedyClosest(query, ep, l)
	}

	// Search layer 0 with full ef
	candidates := idx.searchLayer(query, ep, ef, 0)

	// Take top K
	if len(candidates) > k {
		candidates = candidates[:k]
	}

	results := make([]Result, len(candidates))
	for i, c := range candidates {
		results[i] = Result{
			ID:       idx.nodes[c.idx].id,
			Distance: c.dist,
		}
	}
	return results
}

// Has returns true if the given memory ID is in the index.
func (idx *Index) Has(id int64) bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	_, exists := idx.idToIdx[id]
	return exists
}

// randomLevel generates a random level from geometric distribution.
func (idx *Index) randomLevel() int {
	r := idx.rng.Float64()
	if r == 0 {
		r = 1e-10 // avoid log(0)
	}
	return int(math.Floor(-math.Log(r) * idx.LevelMult))
}

// greedyClosest finds the single closest node to query at the given layer,
// starting from entry point ep. Used for descending through upper layers.
func (idx *Index) greedyClosest(query []float32, ep int, layer int) int {
	dist := cosineDistance(query, idx.nodes[ep].vector)

	for {
		improved := false
		if layer < len(idx.nodes[ep].friends) {
			for _, friendIdx := range idx.nodes[ep].friends[layer] {
				friendDist := cosineDistance(query, idx.nodes[friendIdx].vector)
				if friendDist < dist {
					ep = friendIdx
					dist = friendDist
					improved = true
				}
			}
		}
		if !improved {
			break
		}
	}
	return ep
}

// searchLayer performs beam search at a single layer.
// Returns up to ef candidates sorted by distance (ascending).
func (idx *Index) searchLayer(query []float32, ep int, ef int, layer int) []candidate {
	visited := make(map[int]bool)
	visited[ep] = true

	epDist := cosineDistance(query, idx.nodes[ep].vector)
	candidates := []candidate{{idx: ep, dist: epDist}} // min-heap behavior via sort
	results := []candidate{{idx: ep, dist: epDist}}     // max-heap behavior (we keep closest ef)

	for len(candidates) > 0 {
		// Pop closest candidate
		closest := candidates[0]
		candidates = candidates[1:]

		// Farthest result
		farthest := results[len(results)-1]

		// If closest candidate is farther than farthest result, we're done
		if closest.dist > farthest.dist && len(results) >= ef {
			break
		}

		// Expand neighbors
		if layer < len(idx.nodes[closest.idx].friends) {
			for _, neighborIdx := range idx.nodes[closest.idx].friends[layer] {
				if visited[neighborIdx] {
					continue
				}
				visited[neighborIdx] = true

				neighborDist := cosineDistance(query, idx.nodes[neighborIdx].vector)

				// Add if closer than farthest result or results not full
				if neighborDist < results[len(results)-1].dist || len(results) < ef {
					candidates = insertSorted(candidates, candidate{idx: neighborIdx, dist: neighborDist})
					results = insertSorted(results, candidate{idx: neighborIdx, dist: neighborDist})

					// Trim results to ef
					if len(results) > ef {
						results = results[:ef]
					}
				}
			}
		}
	}

	return results
}

// selectNeighbors picks the best maxConn neighbors from candidates.
// Uses the simple heuristic: closest by distance.
func (idx *Index) selectNeighbors(candidates []candidate, maxConn int) []int {
	if len(candidates) <= maxConn {
		neighbors := make([]int, len(candidates))
		for i, c := range candidates {
			neighbors[i] = c.idx
		}
		return neighbors
	}

	neighbors := make([]int, maxConn)
	for i := 0; i < maxConn; i++ {
		neighbors[i] = candidates[i].idx
	}
	return neighbors
}

// shrinkNeighbors prunes a neighbor list to maxConn by keeping closest.
func (idx *Index) shrinkNeighbors(nodeIdx int, neighbors []int, maxConn int, layer int) []int {
	if len(neighbors) <= maxConn {
		return neighbors
	}

	type scored struct {
		idx  int
		dist float32
	}

	scored_neighbors := make([]scored, len(neighbors))
	vec := idx.nodes[nodeIdx].vector
	for i, nIdx := range neighbors {
		scored_neighbors[i] = scored{idx: nIdx, dist: cosineDistance(vec, idx.nodes[nIdx].vector)}
	}

	sort.Slice(scored_neighbors, func(i, j int) bool {
		return scored_neighbors[i].dist < scored_neighbors[j].dist
	})

	result := make([]int, maxConn)
	for i := 0; i < maxConn; i++ {
		result[i] = scored_neighbors[i].idx
	}
	return result
}

// insertSorted inserts a candidate into a sorted slice (ascending by distance).
func insertSorted(s []candidate, c candidate) []candidate {
	i := sort.Search(len(s), func(i int) bool { return s[i].dist >= c.dist })
	s = append(s, candidate{})
	copy(s[i+1:], s[i:])
	s[i] = c
	return s
}

// cosineDistance returns 1 - cosine_similarity. Range: [0, 2], lower = more similar.
func cosineDistance(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 2.0 // max distance
	}

	var dot, normA, normB float32
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 2.0
	}

	sim := dot / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
	return 1.0 - sim
}
