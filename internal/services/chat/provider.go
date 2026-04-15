package chat

import "github.com/wangwuxing777/Pawrd_Backend/internal/services/providercatalog"

// DetectProvider detects a provider name from query text using keyword matching.
// Returns the provider ID (e.g., "bluecross") or "" if none detected.
func DetectProvider(query string) string {
	return providercatalog.DetectProvider(query)
}

// ResolveProvider determines the effective provider using the priority chain:
//  1. Session-level selection (user chose via UI)
//  2. Provider detected in current query text
//  3. Last mentioned provider in conversation
//  4. "" (no provider → search all)
func ResolveProvider(session *Session, query string) string {
	// Priority 1: Explicit session selection
	if session != nil && session.SelectedProvider != "" {
		return session.SelectedProvider
	}

	// Priority 2: Detect from current query
	detected := DetectProvider(query)
	if detected != "" {
		return detected
	}

	// Priority 3: Last mentioned in conversation
	if session != nil && session.LastMentionedProvider != "" {
		return session.LastMentionedProvider
	}

	// Priority 4: No provider
	return ""
}
