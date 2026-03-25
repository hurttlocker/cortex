package embed

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultModelHTTPTimeout = 10 * time.Minute

var ErrORTUnavailable = fmt.Errorf("onnx runtime unavailable")

type ONNXModelSpec struct {
	Key            string
	DisplayName    string
	Repo           string
	RemoteModel    string
	RemoteFiles    map[string]string
	Dimensions     int
	MaxSequenceLen int
	SpeedHint      string
}

type ONNXModelFiles struct {
	Dir                 string
	ModelPath           string
	TokenizerPath       string
	ConfigPath          string
	TokenizerConfigPath string
	SpecialTokensPath   string
}

func DefaultONNXModelSpec() ONNXModelSpec {
	return ONNXModelSpec{
		Key:         "all-minilm-l6-v2",
		DisplayName: "onnx/all-minilm-l6-v2",
		Repo:        "Xenova/all-MiniLM-L6-v2",
		RemoteModel: "onnx/model_int8.onnx",
		RemoteFiles: map[string]string{
			"tokenizer.json":          "tokenizer.json",
			"config.json":             "config.json",
			"tokenizer_config.json":   "tokenizer_config.json",
			"special_tokens_map.json": "special_tokens_map.json",
		},
		Dimensions:     384,
		MaxSequenceLen: 128,
		SpeedHint:      "~7 emb/s",
	}
}

func ResolveONNXModelSpec(raw string) (ONNXModelSpec, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "all-minilm", "all-minilm-l6-v2", "sentence-transformers/all-minilm-l6-v2":
		return DefaultONNXModelSpec(), nil
	default:
		return ONNXModelSpec{}, fmt.Errorf("unknown onnx embedding model %q (valid: all-minilm-l6-v2)", raw)
	}
}

func defaultEmbedModelsRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cortex", "models", "embed"), nil
}

func ResolveONNXModelFiles(spec ONNXModelSpec) (ONNXModelFiles, error) {
	root, err := defaultEmbedModelsRoot()
	if err != nil {
		return ONNXModelFiles{}, err
	}
	dir := filepath.Join(root, spec.Key)
	return ONNXModelFiles{
		Dir:                 dir,
		ModelPath:           filepath.Join(dir, "model.onnx"),
		TokenizerPath:       filepath.Join(dir, "tokenizer.json"),
		ConfigPath:          filepath.Join(dir, "config.json"),
		TokenizerConfigPath: filepath.Join(dir, "tokenizer_config.json"),
		SpecialTokensPath:   filepath.Join(dir, "special_tokens_map.json"),
	}, nil
}

func ONNXModelReady(files ONNXModelFiles) bool {
	required := []string{
		files.ModelPath,
		files.TokenizerPath,
	}
	for _, path := range required {
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			return false
		}
	}
	return true
}

func EnsureONNXModel(ctx context.Context, spec ONNXModelSpec) (ONNXModelFiles, error) {
	files, err := ResolveONNXModelFiles(spec)
	if err != nil {
		return ONNXModelFiles{}, err
	}
	if ONNXModelReady(files) {
		return files, nil
	}
	if err := os.MkdirAll(files.Dir, 0o755); err != nil {
		return ONNXModelFiles{}, fmt.Errorf("creating embed model dir: %w", err)
	}

	client := &http.Client{Timeout: defaultModelHTTPTimeout}
	targets := map[string]string{
		spec.RemoteModel: files.ModelPath,
	}
	for remote, localName := range spec.RemoteFiles {
		targets[remote] = resolveONNXRemotePath(files, localName)
	}

	for remote, local := range targets {
		if err := downloadModelFile(ctx, client, hfResolveURL(spec.Repo, remote), local); err != nil {
			return ONNXModelFiles{}, err
		}
	}

	return files, nil
}

func resolveONNXRemotePath(files ONNXModelFiles, localName string) string {
	switch filepath.Base(localName) {
	case "tokenizer.json":
		return files.TokenizerPath
	case "config.json":
		return files.ConfigPath
	case "tokenizer_config.json":
		return files.TokenizerConfigPath
	case "special_tokens_map.json":
		return files.SpecialTokensPath
	default:
		return filepath.Join(files.Dir, filepath.Base(localName))
	}
}

func hfResolveURL(repo, path string) string {
	return fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", repo, path)
}

func downloadModelFile(ctx context.Context, client *http.Client, url, dest string) error {
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
