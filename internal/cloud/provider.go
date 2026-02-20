package cloud

import "strings"

// Provider parses cloud providerID to extract instance ID and zone.
// Each cloud (AWS, GCP, Azure) implements this interface.
type Provider interface {
	Name() string
	Prefix() string
	Parse(providerID string) (instanceID, zone string)
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

// ZoneToRegion derives region from zone (e.g. us-west-2a -> us-west-2).
func ZoneToRegion(zone string) string {
	if zone == "" {
		return ""
	}
	if idx := len(zone) - 1; idx >= 0 && zone[idx] >= 'a' && zone[idx] <= 'z' {
		return zone[:idx]
	}
	return zone
}
