package aws

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// ClusterHasAWSNode returns true if any provider in the slice is "aws".
// Used to decide whether to resolve account_id via STS (e.g. EKS).
func ClusterHasAWSNode(providers []string) bool {
	for _, p := range providers {
		if p == "aws" {
			return true
		}
	}
	return false
}

// GetAccountID returns the AWS account ID for the identity used by the default
// credential chain (env vars, IRSA, instance profile, etc.). Returns empty string
// if credentials are not available or the call fails (e.g. not running on AWS).
func GetAccountID(ctx context.Context) string {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return ""
	}
	client := sts.NewFromConfig(cfg)
	out, err := client.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return ""
	}
	if out.Account == nil {
		return ""
	}
	return *out.Account
}
