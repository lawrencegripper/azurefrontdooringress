package utils

// Config provides the setup used by the Frontdoor provider
type Config struct {
	ResourceGroupName      string
	FrontDoorName          string
	FrontDoorHostname      string
	ClusterName            string
	BackendPoolName        string
	PrimaryIngressPublicIP string
	SubscriptionID         string
	KubernetesNamespace    string
	DebugAPICalls          bool
	StorageAccountURL      string
	StorageAccountKey      string
}
