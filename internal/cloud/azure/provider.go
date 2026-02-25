package azure

import (
	"strings"

	"k8s-agent/internal/cloud"
)

func init() {
	cloud.Register(&Provider{})
}

// Provider implements cloud.Provider for Azure.
type Provider struct{}

func (Provider) Name() string   { return "azure" }
func (Provider) Prefix() string { return "azure://" }

// Parse extracts instance ID (VM name) from Azure providerID.
// Format: azure:///subscriptions/.../resourceGroups/.../providers/Microsoft.Compute/virtualMachines/vm-name
func (Provider) Parse(providerID string) (instanceID, zone string) {
	trimmed := strings.TrimPrefix(providerID, "azure:///")
	parts := strings.Split(trimmed, "/")
	for i, p := range parts {
		if strings.EqualFold(p, "virtualMachines") && i+1 < len(parts) {
			return parts[i+1], ""
		}
	}
	return "", ""
}

// ProjectID returns empty string; Azure providerID uses subscription/resource group, not project.
func (Provider) ProjectID(providerID string) string {
	return ""
}
