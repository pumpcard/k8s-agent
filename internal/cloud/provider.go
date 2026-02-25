package cloud

import "strings"

// Provider parses cloud providerID to extract instance ID, zone, and optionally project ID.
// Each cloud (AWS, GCP, Azure) implements this interface.
type Provider interface {
	Name() string
	Prefix() string
	Parse(providerID string) (instanceID, zone string)
	// ProjectID returns the cloud project ID when applicable (e.g. GCP). Empty for AWS/Azure.
	ProjectID(providerID string) string
}

// Registry holds cloud providers by prefix. Thread-safe for reads after init.
var registry = make(map[string]Provider)

// Register adds a provider. Call from init() of each provider package.
func Register(p Provider) {
	registry[p.Prefix()] = p
}

// Parse dispatches to the matching provider. Returns (providerName, instanceID, zone).
func Parse(providerID string) (providerName, instanceID, zone string) {
	if providerID != "" {
		for prefix, p := range registry {
			if strings.HasPrefix(providerID, prefix) {
				instanceID, zone = p.Parse(providerID)
				return p.Name(), instanceID, zone
			}
		}
	}
	return "", "", ""
}

// ProjectID returns the cloud project ID for the given providerID when applicable (e.g. GCP).
// Returns empty string for AWS, Azure, or unknown provider.
func ProjectID(providerID string) string {
	if providerID == "" {
		return ""
	}
	for prefix, p := range registry {
		if strings.HasPrefix(providerID, prefix) {
			return p.ProjectID(providerID)
		}
	}
	return ""
}

// ZoneToRegion derives region from zone.
// Supports AWS-style (us-west-2a -> us-west-2) and GCP-style (us-central1-a -> us-central1).
func ZoneToRegion(zone string) string {
	if zone == "" {
		return ""
	}
	// GCP: zone is like us-central1-a; strip trailing "-" + single letter.
	if i := strings.LastIndex(zone, "-"); i >= 0 && i+1 < len(zone) {
		suffix := zone[i+1:]
		if len(suffix) == 1 && suffix[0] >= 'a' && suffix[0] <= 'z' {
			return zone[:i]
		}
	}
	// AWS: zone is like us-west-2a; strip trailing single letter.
	if idx := len(zone) - 1; idx >= 0 && zone[idx] >= 'a' && zone[idx] <= 'z' {
		return zone[:idx]
	}
	return zone
}
