package aws

import (
	"strings"

	"k8s-agent/internal/cloud"
)

func init() {
	cloud.Register(&Provider{})
}

// Provider implements cloud.Provider for AWS.
type Provider struct{}

func (Provider) Name() string   { return "aws" }
func (Provider) Prefix() string { return "aws://" }

// Parse extracts instance ID and zone from AWS providerID.
// Format: aws:///us-west-2a/i-0abc123 or aws:///us-west-2/i-0abc123
func (Provider) Parse(providerID string) (instanceID, zone string) {
	trimmed := strings.TrimPrefix(providerID, "aws:///")
	parts := strings.Split(trimmed, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-1], parts[0]
	}
	if len(parts) == 1 && strings.HasPrefix(parts[0], "i-") {
		return parts[0], ""
	}
	return "", ""
}

// ProjectID returns empty string; AWS providerID does not include project.
func (Provider) ProjectID(providerID string) string {
	return ""
}

// AccountID returns empty string; AWS providerID does not include account ID.
// Account ID can be set via node labels (e.g. custom label) or obtained from instance metadata.
func (Provider) AccountID(providerID string) string {
	return ""
}
