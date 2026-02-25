package gcp

import (
	"strings"

	"k8s-agent/internal/cloud"
)

func init() {
	cloud.Register(&Provider{})
}

// Provider implements cloud.Provider for GCP.
type Provider struct{}

func (Provider) Name() string   { return "gcp" }
func (Provider) Prefix() string { return "gce://" }

// Parse extracts instance ID and zone from GCP providerID.
// Format: gce://project-id/zone/instance-name (matches GKE node providerID).
func (Provider) Parse(providerID string) (instanceID, zone string) {
	trimmed := strings.TrimPrefix(providerID, "gce://")
	parts := strings.Split(trimmed, "/")
	if len(parts) >= 3 {
		return parts[len(parts)-1], parts[len(parts)-2]
	}
	if len(parts) == 2 {
		return parts[1], parts[0]
	}
	return "", ""
}

// ProjectID extracts the GCP project ID from providerID (gce://project-id/zone/instance-name).
func (Provider) ProjectID(providerID string) string {
	trimmed := strings.TrimPrefix(providerID, "gce://")
	parts := strings.Split(trimmed, "/")
	if len(parts) >= 3 {
		return parts[0]
	}
	return ""
}
