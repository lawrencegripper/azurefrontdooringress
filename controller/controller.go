package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lawrencegripper/azurefrontdooringress/sync"
	"github.com/lawrencegripper/azurefrontdooringress/utils"
	v1 "k8s.io/api/core/v1"
	v1beta1 "k8s.io/api/extensions/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

// Start starts the controller running, observing the K8s cluster for changes
// to ingresses in the namespace
func Start(ctx context.Context, namespace string, provider sync.Provider) ([]*v1beta1.Ingress, error) {
	log := utils.GetLogger(ctx)

	resyncPeriod := 30 * time.Second
	client, _ := getClientSet(ctx)
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
	err := ingressStore.Resync()
	if err != nil {
		log.WithError(err).Error("Error eesyncing ingress store")
		return nil, err
	}

	serviceIP, err := getServiceIP(ctx, serviceStore)
	if err != nil {
		log.WithError(err).Error("Error getting service")
		return nil, err
	}

	log.WithField("PublicIngressIP", serviceIP).Info("Located annotated external service used by primary ingress controller")

	ingressToSync := make([]*v1beta1.Ingress, 0)

	for _, ingressObj := range ingressStore.List() {
		ingress := ingressObj.(*v1beta1.Ingress)
		if !hasFrontdoorEnabledAnnotation(ingress.Annotations) {
			log.WithField("ingressName", ingress.Name).Info("Skipping ingress as isn't annotated")
			continue
		}

		log.WithField("ingressName", ingress.Name).Info("Found ingress for frontdoor to route")

		ingressToSync = append(ingressToSync, ingress)
	}

	err = provider.Sync(ctx, ingressToSync)
	if err != nil {
		log.WithError(err).Error("Failed to sync ingress")
		return nil, err
	}

	return ingressToSync, nil
}

func getServiceIP(ctx context.Context, serviceStore cache.Store) (string, error) {
	log := utils.GetLogger(ctx)

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

func getClientSet(ctx context.Context) (*kubernetes.Clientset, error) {
	log := utils.GetLogger(ctx)

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
