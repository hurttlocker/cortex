package ann

import (
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// --- Helpers ---

func randomVector(dims int, rng *rand.Rand) []float32 {
	v := make([]float32, dims)
	for i := range v {
		v[i] = rng.Float32()*2 - 1 // [-1, 1]
	}
	return v
}

func bruteForceNN(query []float32, vectors [][]float32, ids []int64, k int) []Result {
	type scored struct {
		id   int64
		dist float32
	}
	var all []scored
	for i, v := range vectors {
		all = append(all, scored{id: ids[i], dist: cosineDistance(query, v)})
	}
	// Sort by distance ascending
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j].dist < all[j-1].dist; j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	if len(all) > k {
		all = all[:k]
	}
	results := make([]Result, len(all))
	for i, s := range all {
		results[i] = Result{ID: s.id, Distance: s.dist}
	}
	return results
}

// --- Core Tests ---

func TestNew(t *testing.T) {
	idx := New(768)
	if idx.dims != 768 {
		t.Errorf("dims = %d, want 768", idx.dims)
	}
	if idx.M != DefaultM {
		t.Errorf("M = %d, want %d", idx.M, DefaultM)
	}
	if idx.Len() != 0 {
		t.Errorf("Len = %d, want 0", idx.Len())
	}
}

func TestInsertAndSearch_Small(t *testing.T) {
	dims := 32
	rng := rand.New(rand.NewSource(42))
	idx := New(dims)

	// Insert 100 random vectors
	vectors := make([][]float32, 100)
	ids := make([]int64, 100)
	for i := 0; i < 100; i++ {
		vectors[i] = randomVector(dims, rng)
		ids[i] = int64(i + 1)
		idx.Insert(ids[i], vectors[i])
	}

	if idx.Len() != 100 {
		t.Fatalf("Len = %d, want 100", idx.Len())
	}

	// Search for a random query
	query := randomVector(dims, rng)
	results := idx.Search(query, 5)

	if len(results) != 5 {
		t.Fatalf("got %d results, want 5", len(results))
	}

	// Verify results are sorted by distance
	for i := 1; i < len(results); i++ {
		if results[i].Distance < results[i-1].Distance {
			t.Errorf("results not sorted: [%d].dist=%f < [%d].dist=%f",
				i, results[i].Distance, i-1, results[i-1].Distance)
		}
	}

	// Compare recall against brute force
	bfResults := bruteForceNN(query, vectors, ids, 5)
	recall := computeRecall(results, bfResults)
	if recall < 0.6 {
		t.Errorf("recall = %.2f, want >= 0.6 (HNSW: %v, BF: %v)", recall, resultIDs(results), resultIDs(bfResults))
	}
}

func TestInsertAndSearch_Medium(t *testing.T) {
	dims := 128
	n := 1000
	rng := rand.New(rand.NewSource(123))
	idx := New(dims)

	vectors := make([][]float32, n)
	ids := make([]int64, n)
	for i := 0; i < n; i++ {
		vectors[i] = randomVector(dims, rng)
		ids[i] = int64(i + 1)
		idx.Insert(ids[i], vectors[i])
	}

	// Run 10 random queries, check average recall
	totalRecall := 0.0
	queries := 10
	k := 10

	for q := 0; q < queries; q++ {
		query := randomVector(dims, rng)
		results := idx.Search(query, k)
		bfResults := bruteForceNN(query, vectors, ids, k)
		totalRecall += computeRecall(results, bfResults)
	}

	avgRecall := totalRecall / float64(queries)
	if avgRecall < 0.7 {
		t.Errorf("avg recall = %.2f, want >= 0.7", avgRecall)
	}
	t.Logf("Average recall@%d over %d queries on %d vectors: %.2f", k, queries, n, avgRecall)
}

func TestSearchEmpty(t *testing.T) {
	idx := New(32)
	results := idx.Search(randomVector(32, rand.New(rand.NewSource(1))), 5)
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestSearchSingleNode(t *testing.T) {
	idx := New(4)
	idx.Insert(42, []float32{1, 0, 0, 0})

	results := idx.Search([]float32{1, 0, 0, 0}, 5)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].ID != 42 {
		t.Errorf("ID = %d, want 42", results[0].ID)
	}
	if results[0].Distance > 0.001 {
		t.Errorf("distance = %f, want ~0 for identical vector", results[0].Distance)
	}
}

func TestDuplicateInsert(t *testing.T) {
	idx := New(4)
	idx.Insert(1, []float32{1, 0, 0, 0})
	idx.Insert(1, []float32{0, 1, 0, 0}) // duplicate ID, should be no-op
	if idx.Len() != 1 {
		t.Errorf("Len = %d, want 1 after duplicate insert", idx.Len())
	}
}

func TestHas(t *testing.T) {
	idx := New(4)
	idx.Insert(99, []float32{1, 0, 0, 0})
	if !idx.Has(99) {
		t.Error("Has(99) = false, want true")
	}
	if idx.Has(100) {
		t.Error("Has(100) = true, want false")
	}
}

func TestSearchEf(t *testing.T) {
	dims := 64
	n := 500
	rng := rand.New(rand.NewSource(77))
	idx := New(dims)

	vectors := make([][]float32, n)
	ids := make([]int64, n)
	for i := 0; i < n; i++ {
		vectors[i] = randomVector(dims, rng)
		ids[i] = int64(i)
		idx.Insert(ids[i], vectors[i])
	}

	query := randomVector(dims, rng)
	k := 10

	// Higher ef should give equal or better recall
	resultsLowEf := idx.SearchEf(query, k, 20)
	resultsHighEf := idx.SearchEf(query, k, 200)
	bfResults := bruteForceNN(query, vectors, ids, k)

	recallLow := computeRecall(resultsLowEf, bfResults)
	recallHigh := computeRecall(resultsHighEf, bfResults)

	t.Logf("recall@%d: ef=20 → %.2f, ef=200 → %.2f", k, recallLow, recallHigh)

	if recallHigh < recallLow {
		t.Errorf("higher ef should give equal/better recall: ef=20 → %.2f, ef=200 → %.2f", recallLow, recallHigh)
	}
}

// --- Persistence Tests ---

func TestSaveLoad(t *testing.T) {
	dims := 32
	rng := rand.New(rand.NewSource(42))
	idx := New(dims)

	// Insert vectors
	for i := 0; i < 50; i++ {
		idx.Insert(int64(i+1), randomVector(dims, rng))
	}

	// Save
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.hnsw")
	if err := idx.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Check file exists and has reasonable size
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if info.Size() < 1000 {
		t.Errorf("file too small: %d bytes", info.Size())
	}

	// Load
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify structure
	if loaded.Len() != idx.Len() {
		t.Errorf("loaded Len = %d, want %d", loaded.Len(), idx.Len())
	}
	if loaded.dims != idx.dims {
		t.Errorf("loaded dims = %d, want %d", loaded.dims, idx.dims)
	}
	if loaded.M != idx.M {
		t.Errorf("loaded M = %d, want %d", loaded.M, idx.M)
	}
	if loaded.entryPoint != idx.entryPoint {
		t.Errorf("loaded entryPoint = %d, want %d", loaded.entryPoint, idx.entryPoint)
	}

	// Verify search produces same results
	query := randomVector(dims, rng)
	origResults := idx.Search(query, 5)
	loadedResults := loaded.Search(query, 5)

	if len(origResults) != len(loadedResults) {
		t.Fatalf("result count mismatch: orig=%d, loaded=%d", len(origResults), len(loadedResults))
	}
	for i := range origResults {
		if origResults[i].ID != loadedResults[i].ID {
			t.Errorf("result[%d] ID mismatch: orig=%d, loaded=%d", i, origResults[i].ID, loadedResults[i].ID)
		}
	}
}

func TestLoadInvalidMagic(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "bad.hnsw")
	os.WriteFile(path, []byte("NOTVALID"), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid magic")
	}
}

// --- Distance Tests ---

func TestCosineDistance(t *testing.T) {
	tests := []struct {
		a, b []float32
		want float32
	}{
		{[]float32{1, 0}, []float32{1, 0}, 0},
		{[]float32{1, 0}, []float32{0, 1}, 1},
		{[]float32{1, 0}, []float32{-1, 0}, 2},
		{[]float32{}, []float32{}, 2},          // empty
		{[]float32{0, 0}, []float32{1, 0}, 2},  // zero norm
	}

	for _, tt := range tests {
		got := cosineDistance(tt.a, tt.b)
		if math.Abs(float64(got-tt.want)) > 0.001 {
			t.Errorf("cosineDistance(%v, %v) = %f, want %f", tt.a, tt.b, got, tt.want)
		}
	}
}

// --- Helpers ---

func computeRecall(predicted, truth []Result) float64 {
	truthSet := make(map[int64]bool)
	for _, r := range truth {
		truthSet[r.ID] = true
	}
	hits := 0
	for _, r := range predicted {
		if truthSet[r.ID] {
			hits++
		}
	}
	if len(truth) == 0 {
		return 1.0
	}
	return float64(hits) / float64(len(truth))
}

func resultIDs(results []Result) []int64 {
	ids := make([]int64, len(results))
	for i, r := range results {
		ids[i] = r.ID
	}
	return ids
}
