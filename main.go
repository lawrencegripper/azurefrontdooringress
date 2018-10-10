package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/preview/frontdoor/mgmt/2018-08-01-preview/frontdoor"
	"github.com/Azure/go-autorest/autorest/azure/auth"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/joho/godotenv"
	log "github.com/sirupsen/logrus"
	v1beta1 "k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	namespace = "default"
)

func main() {
	ctx := context.Background()

	err := godotenv.Load()
	if err != nil {
		log.Error("Error loading .env file")
	}

	subID := os.Getenv("AZURE_SUBSCRIPTION_ID")

	resyncPeriod := 30 * time.Second
	client, _ := getClientSet()
	// create informers factory, enable and assign required informers
	infFactory := informers.NewSharedInformerFactoryWithOptions(client, resyncPeriod,
		informers.WithNamespace(namespace),
		informers.WithTweakListOptions(func(*metav1.ListOptions) {}))

	stopChan := make(chan struct{})

	informer := infFactory.Extensions().V1beta1().Ingresses().Informer()
	store := informer.GetStore()
	go informer.Run(stopChan)

	time.Sleep(15 * time.Second)

	log.Info("Resyncing data store")
	err = store.Resync()
	if err != nil {
		panic(err)
	}

	fmt.Println(store.List())

	fdBackendClient := frontdoor.NewBackendPoolsClient(subID)

	//create Backend object
	fdBackend := frontdoor.Backend{
		Address:      to.StringPtr("backend1"),
		HTTPPort:     to.Int32Ptr(80),
		HTTPSPort:    to.Int32Ptr(443),
		EnabledState: frontdoor.EnabledStateEnumDisabled,
		Weight:       to.Int32Ptr(50),
		Priority:     to.Int32Ptr(1)}

	// creating BackendPoolProperties
	slices := &[]frontdoor.Backend{fdBackend}

	subResourceHealth := &frontdoor.SubResource{
		ID: to.StringPtr("GEN-UNIQUE" + "/healthProbeSettings/healthProbeSettings1")}

	subResourceLoad := &frontdoor.SubResource{
		ID: to.StringPtr("GEN-UNIQUE" + "/healthProbeSettings/healthProbeSettings1")}

	fdbackendPoolProp := &frontdoor.BackendPoolProperties{
		ResourceState:         frontdoor.ResourceStateCreating,
		Backends:              slices,
		HealthProbeSettings:   subResourceHealth,
		LoadBalancingSettings: subResourceLoad}

	// creating BackendPool
	fdbackendPool := frontdoor.BackendPool{
		BackendPoolProperties: fdbackendPoolProp,
		Name:                  to.StringPtr("backendPool1"),
		Type:                  to.StringPtr("Custom host"),
		ID:                    to.StringPtr("1234")}

	//create frontend Endpoint
	fdFrontendEndpointClient := frontdoor.NewFrontendEndpointsClient(subID)
	fdFrontendEndpoint := frontdoor.FrontendEndpoint{
		Name: to.StringPtr("frontendEndpoint1")}

	//create routing door client
	fdRoutingDoorsClient := frontdoor.NewRoutingRulesClient(subID)

	subResourceFrontendEndpoints := frontdoor.SubResource{
		ID: to.StringPtr("frontendEndpoint1" + "/frontendEndpoints/frontendEndpoint1")}

	frontendEndpointSlices := &[]frontdoor.SubResource{subResourceFrontendEndpoints}
	subResourceBackendPool := &frontdoor.SubResource{
		ID: to.StringPtr("backendPool1" + "/backendPools/backendPool1")}

	fdRoutingRuleProperties := frontdoor.RoutingRuleProperties{
		ResourceState:      frontdoor.ResourceStateCreating,
		EnabledState:       "Enabled",
		ForwardingProtocol: "MatchRequest",
		FrontendEndpoints:  frontendEndpointSlices,
		BackendPool:        subResourceBackendPool}

	fdRoutingRule := frontdoor.RoutingRule{
		RoutingRuleProperties: &fdRoutingRuleProperties,
		Name:                  to.StringPtr("routing1")}

	// create an authorizer from env vars or Azure Managed Service Idenity
	authorizer, err := auth.NewAuthorizerFromEnvironment()
	if err == nil {
		fdBackendClient.Authorizer = authorizer
		fdRoutingDoorsClient.Authorizer = authorizer
		fdFrontendEndpointClient.Authorizer = authorizer
	}

	for _, ingressObj := range store.List() {
		ingress := ingressObj.(*v1beta1.Ingress)
		result, err := fdBackendClient.CheckFrontDoorNameAvailability(ctx, frontdoor.CheckNameAvailabilityInput{
			Name: to.StringPtr("testfdname"),
			Type: frontdoor.MicrosoftNetworkfrontDoors,
		})

		if err != nil {
			log.WithError(err).Fatal("Failed to check fd name")
		}

		fdFrontendEndpointClient.CreateOrUpdate(ctx, "resourceGroupName", "testfdname", "fdFrontendEndpoint", fdFrontendEndpoint)
		fdBackendClient.CreateOrUpdate(ctx, "resourceGroupName", "testfdname", "fdBackendPool", fdbackendPool)

		fdRoutingDoorsClient.CreateOrUpdate(ctx, "resourceGroupName", "testfdname", "fdRoutingRule", fdRoutingRule)

		fmt.Println(result)
		fmt.Println(ingress.GetName())
	}

}

func getClientSet() (*kubernetes.Clientset, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		log.WithError(err).Warn("failed getting in-cluster config attempting to use kubeconfig from homedir")
		var kubeconfig string
		if home := homeDir(); home != "" {
			kubeconfig = filepath.Join(home, ".kube", "config")
		}

		if _, err := os.Stat(kubeconfig); os.IsNotExist(err) {
			log.WithError(err).Panic("kubeconfig not found in homedir")
		}

		// use the current context in kubeconfig
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			log.WithError(err).Panic("getting kubeconf from current context")
			return nil, err
		}
	}

	// create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.WithError(err).Error("Getting clientset from config")
		return nil, err
	}

	return clientset, nil
}

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}
