package aws

import (
	"context"
	"os"
	"strings"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/ec2/imds"

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

const imdsTimeout = 2 * time.Second

// ResolveAccountID tries multiple sources to discover the AWS account ID.
// Priority: AWS_ROLE_ARN (IRSA) > EKS_ACCOUNT_ID env var > IMDS identity document.
// Returns the account ID and the source it was resolved from, or empty strings if
// all sources fail.
func (Provider) ResolveAccountID(ctx context.Context) (accountID, source string) {
	if arn := os.Getenv("AWS_ROLE_ARN"); arn != "" {
		parts := strings.Split(arn, ":")
		if len(parts) >= 5 && parts[4] != "" {
			return parts[4], "AWS_ROLE_ARN"
		}
	}

	if id := strings.TrimSpace(os.Getenv("EKS_ACCOUNT_ID")); id != "" {
		return id, "EKS_ACCOUNT_ID"
	}

	ctx, cancel := context.WithTimeout(ctx, imdsTimeout)
	defer cancel()
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return "", ""
	}
	client := imds.NewFromConfig(cfg)
	resp, err := client.GetInstanceIdentityDocument(ctx, &imds.GetInstanceIdentityDocumentInput{})
	if err != nil {
		return "", ""
	}
	if resp.AccountID != "" {
		return resp.AccountID, "IMDS"
	}
	return "", ""
}
