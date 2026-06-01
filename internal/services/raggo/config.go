package raggo

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	DataPath          string
	PersistDir        string
	DefaultMaxSources int
	MaxAllowedSources int
	LLMBaseURL        string
	LLMModel          string
	LLMAPIKey         string
	LLMTimeoutSeconds int
}

func LoadConfig() Config {
	rootDir := detectProjectRoot()

	dataPath := strings.TrimSpace(os.Getenv("HK_INSURANCE_RAG_DATA_PATH"))
	if dataPath == "" {
		dataPath = "assets/rag_normalized/hk_insurance"
	}
	if !filepath.IsAbs(dataPath) {
		dataPath = filepath.Join(rootDir, dataPath)
	}
	normalizedFallback := filepath.Join(rootDir, "assets", "rag_normalized", "hk_insurance")
	if _, err := os.Stat(dataPath); err != nil {
		if _, fallbackErr := os.Stat(normalizedFallback); fallbackErr == nil {
			dataPath = normalizedFallback
		}
	}

	maxSources := envInt("HK_INSURANCE_RAG_MAX_SOURCES", 6)
	if maxSources < 1 {
		maxSources = 6
	}

	persistDir := strings.TrimSpace(os.Getenv("HK_INSURANCE_RAG_PERSIST_DIR"))
	if persistDir == "" {
		persistDir = "artifacts/llamaindex_rag_store"
	}
	if !filepath.IsAbs(persistDir) {
		persistDir = filepath.Join(rootDir, persistDir)
	}

	return Config{
		DataPath:          dataPath,
		PersistDir:        persistDir,
		DefaultMaxSources: maxSources,
		MaxAllowedSources: maxSources,
		LLMBaseURL:        strings.TrimSpace(os.Getenv("HK_INSURANCE_RAG_LLM_BASE_URL")),
		LLMModel:          strings.TrimSpace(os.Getenv("HK_INSURANCE_RAG_LLM_MODEL")),
		LLMAPIKey:         strings.TrimSpace(os.Getenv("HK_INSURANCE_RAG_LLM_API_KEY")),
		LLMTimeoutSeconds: envInt("HK_INSURANCE_RAG_LLM_TIMEOUT_SECONDS", 45),
	}
}

func detectProjectRoot() string {
	cwd, err := os.Getwd()
	if err != nil || strings.TrimSpace(cwd) == "" {
		return "."
	}

	cur := cwd
	for {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return cwd
}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}
