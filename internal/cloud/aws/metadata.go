package aws

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

const imdsTimeout = 2 * time.Second
const imdsBase = "http://169.254.169.254"

// instanceIdentityDocument matches the EC2 instance-identity document (subset we need).
// See https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/instance-identity-documents.html
type instanceIdentityDocument struct {
	AccountID string `json:"accountId"`
}

// AccountIDFromIMDS returns the AWS account ID from the EC2 instance metadata service.
// The agent pod must run on an EC2 node (e.g. EKS) and the node must allow metadata access (IMDS hop limit).
// Returns empty string on any failure (no credentials required).
func AccountIDFromIMDS(ctx context.Context) string {
	client := &http.Client{Timeout: imdsTimeout}

	// Prefer IMDSv2: get token then fetch document
	token, err := imdsv2Token(ctx, client)
	if err == nil && token != "" {
		if id := fetchDocumentWithToken(ctx, client, token); id != "" {
			return id
		}
	}

	// Fallback: IMDSv1 direct GET (some clusters only allow v1 or have hop limit for v2)
	return fetchDocumentV1(ctx, client)
}

func imdsv2Token(ctx context.Context, client *http.Client) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, imdsBase+"/latest/api/token", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "60")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	return strings.TrimSpace(string(b)), nil
}

func fetchDocumentWithToken(ctx context.Context, client *http.Client, token string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imdsBase+"/latest/dynamic/instance-identity/document", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("X-aws-ec2-metadata-token", token)
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var doc instanceIdentityDocument
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return ""
	}
	return doc.AccountID
}

func fetchDocumentV1(ctx context.Context, client *http.Client) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imdsBase+"/latest/dynamic/instance-identity/document", nil)
	if err != nil {
		return ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var doc instanceIdentityDocument
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return ""
	}
	return doc.AccountID
}
