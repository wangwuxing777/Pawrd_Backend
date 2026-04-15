package providercatalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectProvider(t *testing.T) {
	tests := map[string]string{
		"Blue Cross waiting period": "bluecross",
		"藍十字 幾耐生效":                  "bluecross",
		"onedegree coverage":        "one_degree",
		"保誠 claims":                 "prudential",
		"bolttech exclusions":       "bolttech",
		"unknown provider":          "",
	}

	for query, want := range tests {
		if got := DetectProvider(query); got != want {
			t.Fatalf("DetectProvider(%q) = %q, want %q", query, got, want)
		}
	}
}

func TestNormalizeProviderID(t *testing.T) {
	tests := map[string]string{
		"blue cross":    "bluecross",
		"藍十字":           "bluecross",
		"one_degree":    "one_degree",
		"onedegree":     "one_degree",
		"保誠":            "prudential",
		"all":           "",
		"all providers": "",
		" MSIG ":        "msig",
	}

	for raw, want := range tests {
		if got := NormalizeProviderID(raw); got != want {
			t.Fatalf("NormalizeProviderID(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestBuildProviderListIncludesKnownAndDiscoveredProviders(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"bluecross", "MSIG"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}

	providers := BuildProviderList(root)
	providerMap := make(map[string]Provider, len(providers))
	for _, provider := range providers {
		providerMap[provider.ID] = provider
	}

	if _, ok := providerMap["bolttech"]; !ok {
		t.Fatal("expected known provider bolttech to be present")
	}
	if providerMap["bolttech"].HasData {
		t.Fatal("expected bolttech to have no data in temp corpus")
	}
	if !providerMap["bluecross"].HasData {
		t.Fatal("expected bluecross to be marked as having data")
	}
	if providerMap["bluecross"].Name != "Blue Cross 藍十字" {
		t.Fatalf("unexpected display name for bluecross: %q", providerMap["bluecross"].Name)
	}
	if !providerMap["MSIG"].HasData {
		t.Fatal("expected discovered provider MSIG to be marked as having data")
	}
	if providerMap["MSIG"].Name != "MSIG" {
		t.Fatalf("unexpected display name for MSIG: %q", providerMap["MSIG"].Name)
	}
}
