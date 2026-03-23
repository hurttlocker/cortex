//go:build cgo

package rerank

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"

	"github.com/sugarme/tokenizer"
	"github.com/sugarme/tokenizer/pretrained"
	ort "github.com/yalue/onnxruntime_go"
)

var (
	ortInitOnce sync.Once
	ortInitErr  error
	ortLibPath  string
)

type onnxCrossEncoder struct {
	name         string
	tokenizer    *tokenizer.Tokenizer
	session      *ort.DynamicAdvancedSession
	batchSize    int
	maxSeqLen    int
	padTokenID   int64
	sessionMutex sync.Mutex
}

func NewONNXScorer(cfg Config) (Scorer, error) {
	spec := cfg.Spec
	if spec.Repo == "" {
		spec = DefaultModelSpec()
	}
	if cfg.Files.ModelPath == "" {
		files, err := ResolveModelFiles(spec)
		if err != nil {
			return nil, err
		}
		cfg.Files = files
	}
	if !ModelReady(cfg.Files) {
		return nil, fmt.Errorf("%w: run `cortex rerank-setup` first", ErrModelNotReady)
	}
	libPath := strings.TrimSpace(cfg.LibraryPath)
	if libPath == "" {
		libPath = DetectORTLibraryPath()
	}
	if libPath == "" {
		return nil, fmt.Errorf("%w: set ONNXRUNTIME_SHARED_LIBRARY_PATH or install the shared library", ErrORTUnavailable)
	}
	if err := ensureORTEnvironment(libPath); err != nil {
		return nil, err
	}

	tok, err := pretrained.FromFile(cfg.Files.TokenizerPath)
	if err != nil {
		return nil, fmt.Errorf("load reranker tokenizer: %w", err)
	}
	maxSeqLen := cfg.MaxSequenceLength
	if maxSeqLen <= 0 {
		maxSeqLen = spec.MaxSequenceLen
	}
	tok.WithTruncation(&tokenizer.TruncationParams{
		MaxLength: maxSeqLen,
		Strategy:  tokenizer.LongestFirst,
		Stride:    0,
	})

	sessionOptions, err := ort.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("create onnx session options: %w", err)
	}
	defer sessionOptions.Destroy()
	_ = sessionOptions.SetGraphOptimizationLevel(ort.GraphOptimizationLevelEnableAll)
	_ = sessionOptions.SetExecutionMode(ort.ExecutionModeSequential)
	threads := min(runtime.NumCPU(), 4)
	if threads < 1 {
		threads = 1
	}
	_ = sessionOptions.SetIntraOpNumThreads(threads)
	_ = sessionOptions.SetInterOpNumThreads(1)

	session, err := ort.NewDynamicAdvancedSession(
		cfg.Files.ModelPath,
		[]string{"input_ids", "attention_mask"},
		[]string{"logits"},
		sessionOptions,
	)
	if err != nil {
		return nil, fmt.Errorf("load reranker model: %w", err)
	}

	padTokenID := int64(0)
	if id, ok := tok.TokenToId("<pad>"); ok {
		padTokenID = int64(id)
	}

	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 8
	}

	return &onnxCrossEncoder{
		name:       spec.DisplayName,
		tokenizer:  tok,
		session:    session,
		batchSize:  batchSize,
		maxSeqLen:  maxSeqLen,
		padTokenID: padTokenID,
	}, nil
}

func (o *onnxCrossEncoder) Name() string {
	return o.name
}

func (o *onnxCrossEncoder) Available() bool {
	return o != nil && o.session != nil && o.tokenizer != nil
}

func (o *onnxCrossEncoder) Close() error {
	if o == nil || o.session == nil {
		return nil
	}
	return o.session.Destroy()
}

func (o *onnxCrossEncoder) Score(ctx context.Context, query string, docs []string) ([]float64, error) {
	if !o.Available() {
		return nil, fmt.Errorf("%w: scorer not initialized", ErrORTUnavailable)
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	scores := make([]float64, 0, len(docs))
	for start := 0; start < len(docs); start += o.batchSize {
		end := start + o.batchSize
		if end > len(docs) {
			end = len(docs)
		}
		batchScores, err := o.scoreBatch(ctx, query, docs[start:end])
		if err != nil {
			return nil, err
		}
		scores = append(scores, batchScores...)
	}
	return scores, nil
}

func (o *onnxCrossEncoder) scoreBatch(ctx context.Context, query string, docs []string) ([]float64, error) {
	encodings := make([]*tokenizer.Encoding, 0, len(docs))
	maxLen := 1
	for _, doc := range docs {
		encoding, err := o.tokenizer.EncodePair(query, strings.TrimSpace(doc), true)
		if err != nil {
			return nil, fmt.Errorf("encode query/document pair: %w", err)
		}
		if encoding.Len() > o.maxSeqLen {
			if _, err := encoding.Truncate(o.maxSeqLen, 0); err != nil {
				return nil, fmt.Errorf("truncate reranker tokens: %w", err)
			}
		}
		if encoding.Len() > maxLen {
			maxLen = encoding.Len()
		}
		encodings = append(encodings, encoding)
	}

	idsData := make([]int64, len(docs)*maxLen)
	maskData := make([]int64, len(docs)*maxLen)
	for i := range idsData {
		idsData[i] = o.padTokenID
	}

	for row, encoding := range encodings {
		ids := encoding.GetIds()
		mask := encoding.GetAttentionMask()
		for col, id := range ids {
			offset := row*maxLen + col
			idsData[offset] = int64(id)
		}
		for col, value := range mask {
			offset := row*maxLen + col
			maskData[offset] = int64(value)
		}
	}

	inputShape := ort.NewShape(int64(len(docs)), int64(maxLen))
	inputIDs, err := ort.NewTensor(inputShape, idsData)
	if err != nil {
		return nil, fmt.Errorf("create input_ids tensor: %w", err)
	}
	defer inputIDs.Destroy()
	attentionMask, err := ort.NewTensor(inputShape, maskData)
	if err != nil {
		return nil, fmt.Errorf("create attention_mask tensor: %w", err)
	}
	defer attentionMask.Destroy()

	outputs := []ort.Value{nil}
	o.sessionMutex.Lock()
	err = o.session.Run([]ort.Value{inputIDs, attentionMask}, outputs)
	o.sessionMutex.Unlock()
	if err != nil {
		return nil, fmt.Errorf("run reranker session: %w", err)
	}
	defer func() {
		for _, output := range outputs {
			if output != nil {
				_ = output.Destroy()
			}
		}
	}()

	logits, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("unexpected reranker output type %T", outputs[0])
	}
	data := logits.GetData()
	if len(data) != len(docs) {
		return nil, fmt.Errorf("reranker logits length mismatch: got %d want %d", len(data), len(docs))
	}

	scores := make([]float64, 0, len(data))
	for _, value := range data {
		scores = append(scores, float64(value))
	}
	return scores, nil
}

func ensureORTEnvironment(libPath string) error {
	ortInitOnce.Do(func() {
		ortLibPath = libPath
		ort.SetSharedLibraryPath(libPath)
		ortInitErr = ort.InitializeEnvironment(ort.WithLogLevelWarning())
	})
	if ortInitErr != nil {
		return fmt.Errorf("%w: %v", ErrORTUnavailable, ortInitErr)
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
