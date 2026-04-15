package providercatalog

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Provider struct {
	ID      string
	Name    string
	HasData bool
}

type definition struct {
	ID      string
	Name    string
	Aliases []string
}

var knownDefinitions = []definition{
	{
		ID:      "bluecross",
		Name:    "Blue Cross 藍十字",
		Aliases: []string{"bluecross", "blue_cross", "blue cross", "藍十字"},
	},
	{
		ID:      "one_degree",
		Name:    "OneDegree",
		Aliases: []string{"one_degree", "onedegree", "one degree"},
	},
	{
		ID:      "prudential",
		Name:    "Prudential 保誠",
		Aliases: []string{"prudential", "保誠", "pruchoice"},
	},
	{
		ID:      "bolttech",
		Name:    "Bolttech",
		Aliases: []string{"bolttech"},
	},
}

func KnownProviders() []Provider {
	providers := make([]Provider, 0, len(knownDefinitions))
	for _, provider := range knownDefinitions {
		providers = append(providers, Provider{
			ID:   provider.ID,
			Name: provider.Name,
		})
	}
	return providers
}

func DetectProvider(query string) string {
	lower := strings.ToLower(query)
	for _, provider := range knownDefinitions {
		for _, alias := range provider.Aliases {
			if strings.Contains(lower, alias) {
				return provider.ID
			}
		}
	}
	return ""
}

func NormalizeProviderID(raw string) string {
	value := strings.TrimSpace(strings.ToLower(raw))
	if value == "" || value == "string" || value == "all" || value == "all providers" {
		return ""
	}
	for _, provider := range knownDefinitions {
		if value == provider.ID {
			return provider.ID
		}
		for _, alias := range provider.Aliases {
			if value == alias {
				return provider.ID
			}
		}
	}
	return value
}

func DisplayName(providerID string) string {
	for _, provider := range knownDefinitions {
		if provider.ID == providerID {
			return provider.Name
		}
	}
	return providerID
}

func BuildProviderList(dataPath string) []Provider {
	discovered := discoverProviders(dataPath)
	providerIDs := make(map[string]struct{}, len(knownDefinitions)+len(discovered))
	for _, provider := range knownDefinitions {
		providerIDs[provider.ID] = struct{}{}
	}
	for providerID := range discovered {
		providerIDs[providerID] = struct{}{}
	}

	ids := make([]string, 0, len(providerIDs))
	for providerID := range providerIDs {
		ids = append(ids, providerID)
	}
	sort.Strings(ids)

	providers := make([]Provider, 0, len(ids))
	for _, providerID := range ids {
		providers = append(providers, Provider{
			ID:      providerID,
			Name:    DisplayName(providerID),
			HasData: discovered[providerID],
		})
	}
	return providers
}

func discoverProviders(dataPath string) map[string]bool {
	found := make(map[string]bool)
	if strings.TrimSpace(dataPath) == "" {
		return found
	}

	entries, err := os.ReadDir(dataPath)
	if err != nil {
		return found
	}

	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		providerID := filepath.Base(entry.Name())
		found[providerID] = true
	}

	return found
}
