package aws

import (
	"os"
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
func (Provider) AccountID(providerID string) string {
	return ""
}

// AccountIDFromRoleARN extracts the AWS account ID from the AWS_ROLE_ARN
// environment variable injected by IRSA (IAM Roles for Service Accounts).
// ARN format: arn:aws:iam::123456789012:role/my-role — account is field 5.
func AccountIDFromRoleARN() string {
	arn := os.Getenv("AWS_ROLE_ARN")
	if arn == "" {
		return ""
	}
	parts := strings.Split(arn, ":")
	if len(parts) >= 5 {
		return parts[4]
	}
	return ""
}
