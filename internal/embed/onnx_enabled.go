//go:build cgo

package embed

import (
	"context"
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/sugarme/tokenizer"
	"github.com/sugarme/tokenizer/pretrained"
	ort "github.com/yalue/onnxruntime_go"
)

var (
	embedORTInitOnce sync.Once
	embedORTInitErr  error
)

type onnxEmbedder struct {
	name         string
	spec         ONNXModelSpec
	files        ONNXModelFiles
	tokenizer    *tokenizer.Tokenizer
	session      *ort.DynamicAdvancedSession
	batchSize    int
	maxSeqLen    int
	padTokenID   int64
	sessionMutex sync.Mutex
}

func NewONNXEmbedder(cfg *EmbedConfig) (Embedder, error) {
	spec, err := ResolveONNXModelSpec(cfg.Model)
	if err != nil {
		return nil, err
	}

	files, err := EnsureONNXModel(context.Background(), spec)
	if err != nil {
		return nil, err
	}

	libPath := strings.TrimSpace(DetectORTLibraryPath())
	if libPath == "" {
		return nil, fmt.Errorf("%w: set ONNXRUNTIME_SHARED_LIBRARY_PATH or install the shared library", ErrORTUnavailable)
	}
	if err := ensureORTEnvironment(libPath); err != nil {
		return nil, err
	}

	tok, err := pretrained.FromFile(files.TokenizerPath)
	if err != nil {
		return nil, fmt.Errorf("load embed tokenizer: %w", err)
	}
	tok.WithPadding(nil)
	tok.WithTruncation(&tokenizer.TruncationParams{
		MaxLength: spec.MaxSequenceLen,
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
	threads := minInt(runtime.NumCPU(), 4)
	if threads < 1 {
		threads = 1
	}
	_ = sessionOptions.SetIntraOpNumThreads(threads)
	_ = sessionOptions.SetInterOpNumThreads(1)

	session, err := ort.NewDynamicAdvancedSession(
		files.ModelPath,
		[]string{"input_ids", "attention_mask", "token_type_ids"},
		[]string{"last_hidden_state"},
		sessionOptions,
	)
	if err != nil {
		return nil, fmt.Errorf("load embed model: %w", err)
	}

	padTokenID := int64(0)
	if id, ok := tok.TokenToId("[PAD]"); ok {
		padTokenID = int64(id)
	} else if id, ok := tok.TokenToId("<pad>"); ok {
		padTokenID = int64(id)
	}

	cfg.Model = spec.Key
	cfg.ModelPath = files.ModelPath
	cfg.Local = true
	cfg.WillDownload = false
	cfg.dimensions = spec.Dimensions

	return &onnxEmbedder{
		name:       spec.DisplayName,
		spec:       spec,
		files:      files,
		tokenizer:  tok,
		session:    session,
		batchSize:  32,
		maxSeqLen:  spec.MaxSequenceLen,
		padTokenID: padTokenID,
	}, nil
}

func (o *onnxEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("empty text")
	}
	embeddings, err := o.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(embeddings) != 1 {
		return nil, fmt.Errorf("expected 1 embedding, got %d", len(embeddings))
	}
	return embeddings[0], nil
}

func (o *onnxEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	nonEmptyTexts := make([]string, 0, len(texts))
	indexMap := make([]int, 0, len(texts))
	for i, text := range texts {
		if strings.TrimSpace(text) != "" {
			nonEmptyTexts = append(nonEmptyTexts, text)
			indexMap = append(indexMap, i)
		}
	}
	if len(nonEmptyTexts) == 0 {
		return make([][]float32, len(texts)), nil
	}

	nonEmptyEmbeddings := make([][]float32, 0, len(nonEmptyTexts))
	for start := 0; start < len(nonEmptyTexts); start += o.batchSize {
		end := start + o.batchSize
		if end > len(nonEmptyTexts) {
			end = len(nonEmptyTexts)
		}
		batchEmbeddings, err := o.embedBatch(ctx, nonEmptyTexts[start:end])
		if err != nil {
			return nil, err
		}
		nonEmptyEmbeddings = append(nonEmptyEmbeddings, batchEmbeddings...)
	}

	result := make([][]float32, len(texts))
	for i, embedding := range nonEmptyEmbeddings {
		if i < len(indexMap) {
			result[indexMap[i]] = embedding
		}
	}
	return result, nil
}

func (o *onnxEmbedder) Dimensions() int {
	return o.spec.Dimensions
}

func (o *onnxEmbedder) HealthCheck(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	if o == nil || o.session == nil || o.tokenizer == nil {
		return fmt.Errorf("onnx embedder not initialized")
	}
	return nil
}

func (o *onnxEmbedder) Close() error {
	if o == nil || o.session == nil {
		return nil
	}
	return o.session.Destroy()
}

func (o *onnxEmbedder) embedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	encodings := make([]*tokenizer.Encoding, 0, len(texts))
	maxLen := 1
	for _, text := range texts {
		encoding, err := o.tokenizer.EncodeSingle(strings.TrimSpace(text), true)
		if err != nil {
			return nil, fmt.Errorf("encode text: %w", err)
		}
		if encoding.Len() > o.maxSeqLen {
			if _, err := encoding.Truncate(o.maxSeqLen, 0); err != nil {
				return nil, fmt.Errorf("truncate embed tokens: %w", err)
			}
		}
		if encoding.Len() > maxLen {
			maxLen = encoding.Len()
		}
		encodings = append(encodings, encoding)
	}

	idsData := make([]int64, len(texts)*maxLen)
	maskData := make([]int64, len(texts)*maxLen)
	typeData := make([]int64, len(texts)*maxLen)
	for i := range idsData {
		idsData[i] = o.padTokenID
	}

	for row, encoding := range encodings {
		ids := encoding.GetIds()
		mask := encoding.GetAttentionMask()
		typeIDs := encoding.GetTypeIds()
		for col, id := range ids {
			offset := row*maxLen + col
			idsData[offset] = int64(id)
		}
		for col, value := range mask {
			offset := row*maxLen + col
			maskData[offset] = int64(value)
		}
		for col, value := range typeIDs {
			offset := row*maxLen + col
			typeData[offset] = int64(value)
		}
	}

	inputShape := ort.NewShape(int64(len(texts)), int64(maxLen))
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

	tokenTypeIDs, err := ort.NewTensor(inputShape, typeData)
	if err != nil {
		return nil, fmt.Errorf("create token_type_ids tensor: %w", err)
	}
	defer tokenTypeIDs.Destroy()

	outputs := []ort.Value{nil}
	o.sessionMutex.Lock()
	err = o.session.Run([]ort.Value{inputIDs, attentionMask, tokenTypeIDs}, outputs)
	o.sessionMutex.Unlock()
	if err != nil {
		return nil, fmt.Errorf("run embed session: %w", err)
	}
	defer func() {
		for _, output := range outputs {
			if output != nil {
				_ = output.Destroy()
			}
		}
	}()

	hiddenState, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("unexpected embed output type %T", outputs[0])
	}

	shape := hiddenState.GetShape()
	if len(shape) != 3 {
		return nil, fmt.Errorf("unexpected embed output shape %v", shape)
	}
	if int(shape[0]) != len(texts) {
		return nil, fmt.Errorf("unexpected embed batch size %d", shape[0])
	}

	hiddenSize := int(shape[2])
	if hiddenSize != o.spec.Dimensions {
		return nil, fmt.Errorf("unexpected embed hidden size %d", hiddenSize)
	}

	return meanPoolAndNormalize(hiddenState.GetData(), maskData, len(texts), maxLen, hiddenSize), nil
}

func meanPoolAndNormalize(hiddenState []float32, attentionMask []int64, batchSize, seqLen, dims int) [][]float32 {
	embeddings := make([][]float32, 0, batchSize)
	for row := 0; row < batchSize; row++ {
		embedding := make([]float32, dims)
		tokenCount := float64(0)
		for col := 0; col < seqLen; col++ {
			if attentionMask[row*seqLen+col] == 0 {
				continue
			}
			base := (row*seqLen + col) * dims
			for i := 0; i < dims; i++ {
				embedding[i] += hiddenState[base+i]
			}
			tokenCount++
		}
		if tokenCount > 0 {
			inv := float32(1.0 / tokenCount)
			for i := range embedding {
				embedding[i] *= inv
			}
		}
		normalizeVector(embedding)
		embeddings = append(embeddings, embedding)
	}
	return embeddings
}

func normalizeVector(v []float32) {
	var sum float64
	for _, value := range v {
		sum += float64(value * value)
	}
	if sum == 0 {
		return
	}
	scale := float32(1.0 / math.Sqrt(sum))
	for i := range v {
		v[i] *= scale
	}
}

func ensureORTEnvironment(libPath string) error {
	embedORTInitOnce.Do(func() {
		ort.SetSharedLibraryPath(libPath)
		embedORTInitErr = ort.InitializeEnvironment(ort.WithLogLevelWarning())
	})
	if embedORTInitErr != nil {
		return fmt.Errorf("%w: %v", ErrORTUnavailable, embedORTInitErr)
	}
	return nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func DetectORTLibraryPath() string {
	candidates := make([]string, 0, 8)
	for _, envKey := range []string{"CORTEX_ONNXRUNTIME_PATH", "ONNXRUNTIME_SHARED_LIBRARY_PATH"} {
		if value := strings.TrimSpace(os.Getenv(envKey)); value != "" {
			candidates = append(candidates, value)
		}
	}

	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates,
			"/usr/local/lib/libonnxruntime.dylib",
			"/usr/local/opt/onnxruntime/lib/libonnxruntime.dylib",
			"/opt/homebrew/lib/libonnxruntime.dylib",
			"/opt/homebrew/opt/onnxruntime/lib/libonnxruntime.dylib",
		)
	case "linux":
		candidates = append(candidates,
			"/usr/lib/libonnxruntime.so",
			"/usr/lib/libonnxruntime.so.1.24.1",
			"/usr/local/lib/libonnxruntime.so",
			"/usr/local/lib/libonnxruntime.so.1.24.1",
		)
	case "windows":
		candidates = append(candidates, `C:\onnxruntime\onnxruntime.dll`)
	}

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}
