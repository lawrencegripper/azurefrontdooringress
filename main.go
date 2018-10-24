package main

import (
	"context"
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"github.com/lawrencegripper/azurefrontdooringress/controller"
	"github.com/lawrencegripper/azurefrontdooringress/sync"
	"github.com/lawrencegripper/azurefrontdooringress/utils"
	log "github.com/sirupsen/logrus"
)

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Error("Error loading .env file")
	}

	syncConfig := utils.Config{
		BackendPoolName:     os.Getenv("BACKENDPOOL_NAME"),
		ResourceGroupName:   os.Getenv("AZURE_RESOURCE_GROUP_NAME"),
		SubscriptionID:      os.Getenv("AZURE_SUBSCRIPTION_ID"),
		ClusterName:         os.Getenv("CLUSTER_NAME"),
		FrontDoorName:       os.Getenv("AZURE_FRONTDOOR_NAME"),
		FrontDoorHostname:   os.Getenv("AZURE_FRONTDOOR_HOSTNAME"),
		KubernetesNamespace: os.Getenv("KUBERNETES_NAMESPACE"),
		StorageAccountURL:   os.Getenv("STORAGE_ACCOUNT_URL"),
		StorageAccountKey:   os.Getenv("STORAGE_ACCOUNT_KEY"),
	}

	logger := log.WithField("config", syncConfig)
	bgCtx := context.Background()
	ctx := utils.WithLogger(bgCtx, logger)

	fdSyncer, err := sync.NewFontDoorSyncer(ctx, syncConfig)
	if err != nil {
		logger.WithError(err).Panic("Failed to create NewFrontDoorSyncer")
	}

	// Todo: move controller logic loop into controller.
	for {
		ingress, err := controller.Start(ctx, syncConfig.KubernetesNamespace, fdSyncer)
		if err != nil {
			panic(fmt.Errorf("Failed running controller: %+v", err))
		}

		log.WithField("ingress", ingress).Info("Update ingress in frontdoor")
	}

}
