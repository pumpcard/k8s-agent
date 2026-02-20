package cloud

// Labels extracts cloud-agnostic fields from standard Kubernetes node labels.
// topology.kubernetes.io/* and node.kubernetes.io/instance-type are set by most cloud providers.
func Labels(labels map[string]string) (instanceType, zone, region string) {
	if labels == nil {
		return "", "", ""
	}
	instanceType = labels["node.kubernetes.io/instance-type"]
	zone = labels["topology.kubernetes.io/zone"]
	region = labels["topology.kubernetes.io/region"]
	if instanceType == "" {
		instanceType = labels["beta.kubernetes.io/instance-type"]
	}
	if zone == "" {
		zone = labels["failure-domain.beta.kubernetes.io/zone"]
	}
	if region == "" {
		region = labels["failure-domain.beta.kubernetes.io/region"]
	}
	return instanceType, zone, region
}
