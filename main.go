package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/preview/frontdoor/mgmt/2018-08-01-preview/frontdoor"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/azure/auth"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/joho/godotenv"
	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	v1beta1 "k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	namespace = "default"
)

func LogRequest() autorest.PrepareDecorator {
	return func(p autorest.Preparer) autorest.Preparer {
		return autorest.PreparerFunc(func(r *http.Request) (*http.Request, error) {
			r, err := p.Prepare(r)
			if err != nil {
				log.Println(err)
			}
			dump, _ := httputil.DumpRequestOut(r, true)
			log.Println(string(dump))
			return r, err
		})
	}
}

func LogResponse() autorest.RespondDecorator {
	return func(p autorest.Responder) autorest.Responder {
		return autorest.ResponderFunc(func(r *http.Response) error {
			err := p.Respond(r)
			if err != nil {
				log.Println(err)
			}
			dump, _ := httputil.DumpResponse(r, true)
			log.Println(string(dump))
			return err
		})
	}
}

func main() {
	ctx := context.Background()

	err := godotenv.Load()
	if err != nil {
		log.Error("Error loading .env file")
	}

	subID := os.Getenv("AZURE_SUBSCRIPTION_ID")
	resourceGroupName := os.Getenv("AZURE_RESOURCE_GROUP_NAME")
	frontDoorName := os.Getenv("AZURE_FRONTDOOR_NAME")
	// clusterName := os.Getenv("CLUSTER_NAME")
	backendPoolName := os.Getenv("BACKENDPOOL_NAME")
	frontdoorHostname := os.Getenv("AZURE_FRONTDOOR_HOSTNAME")

	resyncPeriod := 30 * time.Second
	client, _ := getClientSet()
	// create informers factory, enable and assign required informers
	infFactory := informers.NewSharedInformerFactoryWithOptions(client, resyncPeriod,
		informers.WithNamespace(namespace),
		informers.WithTweakListOptions(func(*metav1.ListOptions) {}))

	stopChan := make(chan struct{})

	ingressInformer := infFactory.Extensions().V1beta1().Ingresses().Informer()
	ingressStore := ingressInformer.GetStore()

	serviceInformer := infFactory.Core().V1().Services().Informer()
	serviceStore := serviceInformer.GetStore()

	go ingressInformer.Run(stopChan)
	go serviceInformer.Run(stopChan)

	time.Sleep(15 * time.Second)

	log.Info("Resyncing data store")
	err = ingressStore.Resync()
	if err != nil {
		panic(err)
	}

	serviceIP, err := getServiceIP(serviceStore)
	if err != nil {
		log.WithError(err).Fatal("Error getting service")
	}

	fmt.Println(serviceIP)

	fdBackendClient := frontdoor.NewBackendPoolsClient(subID)

	//create frontend Endpoint
	fdFrontendEndpointClient := frontdoor.NewFrontendEndpointsClient(subID)
	fdClient := frontdoor.NewFrontDoorsClient(subID)

	//create routing door client
	fdRoutingRulesClient := frontdoor.NewRoutingRulesClient(subID)
	fdLoadbalancerSettingsClient := frontdoor.NewLoadBalancingSettingsClient(subID)
	fdLoadbalancerSettingsClient.RequestInspector = LogRequest()
	fdLoadbalancerSettingsClient.ResponseInspector = LogResponse()
	fdHealthCheckClient := frontdoor.NewHealthProbeSettingsClient(subID)

	// create an authorizer from env vars or Azure Managed Service Idenity
	authorizer, err := auth.NewAuthorizerFromEnvironment()
	if err == nil {
		fdBackendClient.Authorizer = authorizer
		fdRoutingRulesClient.Authorizer = authorizer
		fdFrontendEndpointClient.Authorizer = authorizer
		fdLoadbalancerSettingsClient.Authorizer = authorizer
		fdHealthCheckClient.Authorizer = authorizer
		fdClient.Authorizer = authorizer
	}

	fdBackend := frontdoor.Backend{
		Address:      to.StringPtr(serviceIP),
		HTTPPort:     to.Int32Ptr(80),
		HTTPSPort:    to.Int32Ptr(443),
		EnabledState: frontdoor.EnabledStateEnumEnabled,
		Weight:       to.Int32Ptr(50),
		Priority:     to.Int32Ptr(1),
	}

	// Check if there is already a backend pool
	existingBackendPool, err := fdBackendClient.Get(ctx, resourceGroupName, frontDoorName, backendPoolName)
	if err != nil {

		lbdetails, err := fdLoadbalancerSettingsClient.ListByFrontDoor(ctx, resourceGroupName, frontDoorName)

		log.WithField("fsLbs", lbdetails.Values()).Info("Found existing lbs")

		lbSettings, err := fdLoadbalancerSettingsClient.CreateOrUpdate(ctx, resourceGroupName, frontDoorName, "loadBalancingSettings-1539281898722", frontdoor.LoadBalancingSettingsModel{
			LoadBalancingSettingsProperties: &frontdoor.LoadBalancingSettingsProperties{
				AdditionalLatencyMilliseconds: to.Int32Ptr(0),
				SampleSize:                    to.Int32Ptr(4),
				SuccessfulSamplesRequired:     to.Int32Ptr(2),
			},
			// Name: to.StringPtr("testlbname"),
			// Type: to.StringPtr("Microsoft.Network/Frontdoors/LoadBalancingSettings"),
		})
		if err != nil {
			log.WithError(err).Panic("Failed to create lb settings object")
		}

		log.WithField("lgSettings", lbSettings).Info("Created lb settings")

		// creating BackendPoolProperties
		backends := &[]frontdoor.Backend{fdBackend}

		// Create a backend pool if it doesn't already exist
		createFuture, err := fdBackendClient.CreateOrUpdate(ctx, resourceGroupName, frontDoorName, backendPoolName, frontdoor.BackendPool{
			Name: to.StringPtr(backendPoolName),
			BackendPoolProperties: &frontdoor.BackendPoolProperties{
				Backends: backends,
			},
		})

		if err != nil {
			log.WithError(err).Panic("Creating backend pool failed")
		}

		bp, err := createFuture.Result(fdBackendClient)
		if err != nil {
			log.WithError(err).Panic("Creating backend pool failed after wait")
		}

		existingBackendPool = bp

	}

	var entrypointToUser frontdoor.FrontendEndpoint
	// Get existing Entrypoints
	existingEntrypoints, err := fdFrontendEndpointClient.ListByFrontDoor(ctx, resourceGroupName, frontDoorName)
	for _, entrypoint := range existingEntrypoints.Values() {
		if *entrypoint.HostName == frontdoorHostname {
			entrypointToUser = entrypoint
		}
	}

	for _, ingressObj := range ingressStore.List() {
		ingress := ingressObj.(*v1beta1.Ingress)
		if !hasFrontdoorEnabledAnnotation(ingress.Annotations) {
			log.WithField("ingressName", ingress.Name).Info("Skipping ingress as isn't annotated")
			continue
		}

		log.WithField("ingressName", ingress.Name).Info("Found ingress for frontdoor to route")

		for i, rule := range ingress.Spec.Rules {
			log.WithField("path", rule.HTTP.Paths[0].Path).Info("Found rule for path")

			//Build all paths
			paths := make([]string, len(rule.HTTP.Paths))
			for _, path := range rule.HTTP.Paths {
				paths = append(paths, path.Path)
			}

			fdRoutingRuleProperties := frontdoor.RoutingRuleProperties{
				EnabledState:       "Enabled",
				ForwardingProtocol: "MatchRequest",
				FrontendEndpoints: &[]frontdoor.SubResource{
					{ID: entrypointToUser.ID},
				},
				BackendPool: &frontdoor.SubResource{
					ID: existingBackendPool.ID,
				},
				PatternsToMatch: &paths,
			}

			fdRoutingRule := frontdoor.RoutingRule{
				RoutingRuleProperties: &fdRoutingRuleProperties,
			}

			_, err = fdRoutingRulesClient.CreateOrUpdate(ctx, resourceGroupName, frontDoorName, fmt.Sprintf("%v-rule-%v", ingress.Name, i), fdRoutingRule)

			if err != nil {
				log.WithError(err).Fatal("Faild to create RoutingRules")
			}
		}
	}

}

func getServiceIP(serviceStore cache.Store) (string, error) {
	services := serviceStore.List()

	var serviceIP string
	for _, serviceObj := range services {
		service := serviceObj.(*v1.Service)
		if hasFrontdoorEnabledAnnotation(service.Annotations) {
			if len(service.Status.LoadBalancer.Ingress) > 0 {
				serviceIP = service.Status.LoadBalancer.Ingress[0].IP
				log.
					WithField("serviceName", service.Name).
					WithField("ip", serviceIP).
					Info("Found service for Frontdoor to use")
			}
		}
	}
	if serviceIP == "" {
		return serviceIP, fmt.Errorf("no service found with annotation 'azure/frontdoor:enabled' found")
	}

	return serviceIP, nil
}

func hasFrontdoorEnabledAnnotation(annotations map[string]string) bool {
	annotation, exists := annotations["azure/frontdoor"]
	if exists && annotation == "enabled" {
		return true
	}
	return false
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
