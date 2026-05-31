package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	MapsAPIKey              string
	DBHost                  string
	DBPort                  string
	DBUser                  string
	DBPassword              string
	DBName                  string
	PythonRAGBaseURL        string
	GoRAGBaseURL            string
	PythonRAGTimeoutSeconds int
	GoRAGTimeoutSeconds     int
	ChatRAGRuntime          string
	MerchantFacadeBaseURL   string
	MerchantFacadeAppKey    string
	ShopifyDomain           string
	ShopifyStorefrontToken  string
	UseMockShopify          bool
	StripeSecretKey         string
	StripePublishableKey    string
}

func LoadConfig() *Config {
	_ = godotenv.Load() // Ignore error if .env doesn't exist
	mapsKey := os.Getenv("MAPS_API_KEY")

	return &Config{
		MapsAPIKey:              mapsKey,
		DBHost:                  getEnvOrDefault("DB_HOST", "localhost"),
		DBPort:                  getEnvOrDefault("DB_PORT", "5432"),
		DBUser:                  getEnvOrDefault("DB_USER", "postgres"),
		DBPassword:              getEnvOrDefault("DB_PASSWORD", "postgres"),
		DBName:                  getEnvOrDefault("DB_NAME", "pawrd"),
		PythonRAGBaseURL:        strings.TrimSpace(getEnvOrDefault("PYTHON_RAG_BASE_URL", "http://127.0.0.1:8098")),
		GoRAGBaseURL:            strings.TrimSpace(getEnvOrDefault("GO_RAG_BASE_URL", "http://127.0.0.1:8012/api/rag/go")),
		PythonRAGTimeoutSeconds: getEnvAsIntOrDefault("PYTHON_RAG_TIMEOUT_SECONDS", 90),
		GoRAGTimeoutSeconds:     getEnvAsIntOrDefault("GO_RAG_TIMEOUT_SECONDS", 90),
		ChatRAGRuntime:          strings.ToLower(strings.TrimSpace(getEnvOrDefault("CHAT_RAG_RUNTIME", "python"))),
		MerchantFacadeBaseURL:   strings.TrimSpace(getEnvOrDefault("MERCHANT_FACADE_BASE_URL", "http://127.0.0.1:8090")),
		MerchantFacadeAppKey:    strings.TrimSpace(os.Getenv("MERCHANT_FACADE_APP_KEY")),
		ShopifyDomain:           strings.TrimSpace(os.Getenv("SHOPIFY_DOMAIN")),
		ShopifyStorefrontToken:  strings.TrimSpace(os.Getenv("SHOPIFY_STOREFRONT_TOKEN")),
		UseMockShopify:          os.Getenv("USE_MOCK_SHOPIFY") == "true",
		StripeSecretKey:         strings.TrimSpace(os.Getenv("STRIPE_SECRET_KEY")),
		StripePublishableKey:    strings.TrimSpace(os.Getenv("STRIPE_PUBLISHABLE_KEY")),
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
