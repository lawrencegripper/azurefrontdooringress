package main

import (
	"fmt"
	// "github.com/Azure/azure-sdk-for-go/services/preview/frontdoor/mgmt/2018-08-01-preview/frontdoor"
	log "github.com/sirupsen/logrus"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	// "k8s.io/client-go/tools/cache"
	v1beta1 "k8s.io/api/extensions/v1beta1"
	"k8s.io/client-go/tools/clientcmd"

	"time"

	"os"
	"path/filepath"
	// batchv1 "k8s.io/api/batch/v1"
	// apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	namespace = "default"
)

func main() {
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
	err := store.Resync()
	if err != nil {
		panic(err)
	}

	fmt.Println(store.List())

	for _, ingressObj := range store.List() {
		ingress := ingressObj.(*v1beta1.Ingress)

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
