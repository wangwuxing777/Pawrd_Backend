package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	HKInsuranceRAGEnabled          bool
	HKInsuranceRAGDataPath         string
	HKInsuranceRAGTopK             int
	HKInsuranceRAGRebuildOnStart   bool
	HKInsuranceRAGEmbeddingBaseURL string
	HKInsuranceRAGEmbeddingModel   string
	HKInsuranceRAGEmbeddingAPIKey  string
	HKInsuranceRAGLLMBaseURL       string
	HKInsuranceRAGLLMModel         string
	HKInsuranceRAGLLMAPIKey        string
	MapsAPIKey                     string
	DBHost                         string
	DBPort                         string
	DBUser                         string
	DBPassword                     string
	DBName                         string
	MerchantFacadeBaseURL          string
	MerchantFacadeAppKey           string
	ShopifyDomain                  string
	ShopifyStorefrontToken         string
	UseMockShopify                 bool
	StripeSecretKey                string
	StripePublishableKey           string
}

func LoadConfig() *Config {
	_ = godotenv.Load() // Ignore error if .env doesn't exist
	reportAgentEndpoint := strings.TrimSpace(os.Getenv("REPORT_AGENT_1_ENDPOINT"))
	reportAgentAPIKey := strings.TrimSpace(os.Getenv("REPORT_AGENT_1_API_KEY"))
	reportAgentBaseURL := deriveOpenAIBaseURL(reportAgentEndpoint)
	if reportAgentBaseURL == "" {
		reportAgentBaseURL = "https://api.siliconflow.cn/v1"
	}

	mapsKey := os.Getenv("MAPS_API_KEY")

	return &Config{
		HKInsuranceRAGEnabled:          getEnvOrDefault("HK_INSURANCE_RAG_ENABLED", "true") == "true",
		HKInsuranceRAGDataPath:         strings.TrimSpace(getEnvOrDefault("HK_INSURANCE_RAG_DATA_PATH", "assets/rag/hk_insurance")),
		HKInsuranceRAGTopK:             getEnvAsIntOrDefault("HK_INSURANCE_RAG_TOP_K", 6),
		HKInsuranceRAGRebuildOnStart:   getEnvOrDefault("HK_INSURANCE_RAG_REBUILD_ON_START", "false") == "true",
		HKInsuranceRAGEmbeddingBaseURL: strings.TrimSpace(getEnvOrDefault("HK_INSURANCE_RAG_EMBEDDING_BASE_URL", reportAgentBaseURL)),
		HKInsuranceRAGEmbeddingModel:   strings.TrimSpace(getEnvOrDefault("HK_INSURANCE_RAG_EMBEDDING_MODEL", "BAAI/bge-m3")),
		HKInsuranceRAGEmbeddingAPIKey:  strings.TrimSpace(getEnvOrDefault("HK_INSURANCE_RAG_EMBEDDING_API_KEY", reportAgentAPIKey)),
		HKInsuranceRAGLLMBaseURL:       strings.TrimSpace(getEnvOrDefault("HK_INSURANCE_RAG_LLM_BASE_URL", reportAgentBaseURL)),
		HKInsuranceRAGLLMModel:         strings.TrimSpace(getEnvOrDefault("HK_INSURANCE_RAG_LLM_MODEL", "stepfun-ai/Step-3.5-Flash")),
		HKInsuranceRAGLLMAPIKey:        strings.TrimSpace(getEnvOrDefault("HK_INSURANCE_RAG_LLM_API_KEY", reportAgentAPIKey)),
		MapsAPIKey:                     mapsKey,
		DBHost:                         getEnvOrDefault("DB_HOST", "localhost"),
		DBPort:                         getEnvOrDefault("DB_PORT", "5432"),
		DBUser:                         getEnvOrDefault("DB_USER", "postgres"),
		DBPassword:                     getEnvOrDefault("DB_PASSWORD", "postgres"),
		DBName:                         getEnvOrDefault("DB_NAME", "petwell"),
		MerchantFacadeBaseURL:          strings.TrimSpace(getEnvOrDefault("MERCHANT_FACADE_BASE_URL", "http://127.0.0.1:8090")),
		MerchantFacadeAppKey:           strings.TrimSpace(os.Getenv("MERCHANT_FACADE_APP_KEY")),
		ShopifyDomain:                  strings.TrimSpace(os.Getenv("SHOPIFY_DOMAIN")),
		ShopifyStorefrontToken:         strings.TrimSpace(os.Getenv("SHOPIFY_STOREFRONT_TOKEN")),
		UseMockShopify:                 os.Getenv("USE_MOCK_SHOPIFY") == "true",
		StripeSecretKey:                strings.TrimSpace(os.Getenv("STRIPE_SECRET_KEY")),
		StripePublishableKey:           strings.TrimSpace(os.Getenv("STRIPE_PUBLISHABLE_KEY")),
	}
}

// ValidateShopifyConfig checks if Shopify configuration is properly set
func (c *Config) ValidateShopifyConfig() error {
	if c.ShopifyDomain == "" {
		return fmt.Errorf("SHOPIFY_DOMAIN environment variable is required")
	}
	if c.ShopifyStorefrontToken == "" {
		return fmt.Errorf("SHOPIFY_STOREFRONT_TOKEN environment variable is required")
	}
	return nil
}

// ValidateStripeConfig checks if Stripe configuration is properly set.
func (c *Config) ValidateStripeConfig() error {
	if c.StripeSecretKey == "" {
		return fmt.Errorf("STRIPE_SECRET_KEY environment variable is required")
	}
	if c.StripePublishableKey == "" {
		return fmt.Errorf("STRIPE_PUBLISHABLE_KEY environment variable is required")
	}
	return nil
}

func getEnvOrDefault(key, defaultValue string) string {
	val := os.Getenv(key)
	if val == "" {
		return defaultValue
	}
	return val
}

func getEnvAsIntOrDefault(key string, defaultValue int) int {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(val)
	if err != nil || parsed <= 0 {
		return defaultValue
	}
	return parsed
}

func deriveOpenAIBaseURL(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	endpoint = strings.TrimSuffix(endpoint, "/")
	for _, suffix := range []string{"/chat/completions", "/completions"} {
		if strings.HasSuffix(endpoint, suffix) {
			return strings.TrimSuffix(endpoint, suffix)
		}
	}
	return endpoint
}
