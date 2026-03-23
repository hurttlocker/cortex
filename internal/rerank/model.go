package rerank

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
)

const defaultHTTPTimeoutSeconds = 600

type ModelSpec struct {
	Key            string
	DisplayName    string
	Repo           string
	RemoteModel    string
	RemoteFiles    map[string]string
	MaxSequenceLen int
}

type ModelFiles struct {
	Dir              string
	ModelPath        string
	TokenizerPath    string
	ConfigPath       string
	TokenConfigPath  string
	SpecialTokensMap string
}

type Config struct {
	Spec              ModelSpec
	Files             ModelFiles
	LibraryPath       string
	BatchSize         int
	MaxSequenceLength int
}

func DefaultModelSpec() ModelSpec {
	return ModelSpec{
		Key:         "base",
		DisplayName: "onnx-community/bge-reranker-base-ONNX:int8",
		Repo:        "onnx-community/bge-reranker-base-ONNX",
		RemoteModel: "onnx/model_int8.onnx",
		RemoteFiles: map[string]string{
			"tokenizer.json":          "tokenizer.json",
			"config.json":             "config.json",
			"tokenizer_config.json":   "tokenizer_config.json",
			"special_tokens_map.json": "special_tokens_map.json",
		},
		MaxSequenceLen: 512,
	}
}

func M3ModelSpec() ModelSpec {
	return ModelSpec{
		Key:         "m3",
		DisplayName: "onnx-community/bge-reranker-v2-m3-ONNX:int8",
		Repo:        "onnx-community/bge-reranker-v2-m3-ONNX",
		RemoteModel: "onnx/model_int8.onnx",
		RemoteFiles: map[string]string{
			"tokenizer.json":          "tokenizer.json",
			"config.json":             "config.json",
			"tokenizer_config.json":   "tokenizer_config.json",
			"special_tokens_map.json": "special_tokens_map.json",
		},
		MaxSequenceLen: 512,
	}
}

func ResolveModelSpec(raw string) (ModelSpec, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "base", "bge-reranker-base", "onnx-community/bge-reranker-base-onnx":
		return DefaultModelSpec(), nil
	case "m3", "bge-reranker-v2-m3", "onnx-community/bge-reranker-v2-m3-onnx":
		return M3ModelSpec(), nil
	default:
		return ModelSpec{}, fmt.Errorf("unknown reranker model %q (valid: base, m3)", raw)
	}
}

func DefaultModelsRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cortex", "models", "rerank"), nil
}

func ResolveModelFiles(spec ModelSpec) (ModelFiles, error) {
	root, err := DefaultModelsRoot()
	if err != nil {
		return ModelFiles{}, err
	}
	modelVariant := strings.TrimSuffix(filepath.Base(spec.RemoteModel), filepath.Ext(spec.RemoteModel))
	dir := filepath.Join(root, slugify(spec.Repo), modelVariant)
	return ModelFiles{
		Dir:              dir,
		ModelPath:        filepath.Join(dir, "model.onnx"),
		TokenizerPath:    filepath.Join(dir, "tokenizer.json"),
		ConfigPath:       filepath.Join(dir, "config.json"),
		TokenConfigPath:  filepath.Join(dir, "tokenizer_config.json"),
		SpecialTokensMap: filepath.Join(dir, "special_tokens_map.json"),
	}, nil
}

func ModelReady(files ModelFiles) bool {
	required := []string{
		files.ModelPath,
		files.TokenizerPath,
		files.ConfigPath,
	}
	for _, path := range required {
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			return false
		}
	}
	return true
}

func EnsureModel(ctx context.Context, spec ModelSpec) (ModelFiles, error) {
	files, err := ResolveModelFiles(spec)
	if err != nil {
		return ModelFiles{}, err
	}
	if ModelReady(files) {
		return files, nil
	}
	if err := os.MkdirAll(files.Dir, 0o755); err != nil {
		return ModelFiles{}, fmt.Errorf("creating reranker model dir: %w", err)
	}

	client := &http.Client{Timeout: defaultHTTPTimeoutSeconds * 1e9}
	targets := map[string]string{
		spec.RemoteModel: files.ModelPath,
	}
	for remote, local := range spec.RemoteFiles {
		switch filepath.Base(local) {
		case "tokenizer.json":
			targets[remote] = files.TokenizerPath
		case "config.json":
			targets[remote] = files.ConfigPath
		case "tokenizer_config.json":
			targets[remote] = files.TokenConfigPath
		case "special_tokens_map.json":
			targets[remote] = files.SpecialTokensMap
		}
	}

	for remote, local := range targets {
		if err := downloadFile(ctx, client, hfResolveURL(spec.Repo, remote), local); err != nil {
			return ModelFiles{}, err
		}
	}
	return files, nil
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
		candidates = append(candidates,
			`C:\onnxruntime\onnxruntime.dll`,
		)
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

func hfResolveURL(repo, path string) string {
	return fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", repo, path)
}

func downloadFile(ctx context.Context, client *http.Client, url, dest string) error {
	if info, err := os.Stat(dest); err == nil && info.Size() > 0 {
		return nil
	}

	tmp := dest + ".tmp"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build download request for %s: %w", url, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: unexpected HTTP %d", url, resp.StatusCode)
	}

	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", dest, err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", dest, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close %s: %w", dest, err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s: %w", dest, err)
	}
	return nil
}

func slugify(input string) string {
	var b strings.Builder
	for _, r := range input {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}
