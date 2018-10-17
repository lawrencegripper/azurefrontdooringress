package sync

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/services/preview/frontdoor/mgmt/2018-08-01-preview/frontdoor"
	"github.com/Azure/go-autorest/autorest/azure/auth"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/lawrencegripper/azurefrontdooringress/utils"
	log "github.com/sirupsen/logrus"
	v1beta1 "k8s.io/api/extensions/v1beta1"
)

// Provider the interface any Syncronizers are required to meet
type Provider interface {
	Sync(ctx context.Context, ingressToSync []*v1beta1.Ingress) error
}

// Synchronizer is used to communicate with the frontdoor instance
type Synchronizer struct {
	getEndpoint            func() (frontdoor.FrontendEndpoint, error)
	getOrCreateBackendPool func() (frontdoor.BackendPool, error)
}

// Sync Acquire a lock and update Frontdoor with the ingress information provided
func (p *Synchronizer) Sync(ctx context.Context, ingressToSync []*v1beta1.Ingress) error {
	logger := utils.GetLogger(ctx)
	logger.Warn("No sync logic currently present, blocked on bug: https://github.com/Azure/azure-rest-api-specs/issues/4221")
	return nil
}

// NewFontDoorSyncer creates a new FrontDoor provider with require configuration
// for use when updating frontdoor0
func NewFontDoorSyncer(ctx context.Context, config utils.Config) (*Synchronizer, error) {
	fdSynchronizer := Synchronizer{}

	// create clients for frontdoor
	fdBackendClient := frontdoor.NewBackendPoolsClient(config.SubscriptionID)
	fdFrontendEndpointClient := frontdoor.NewFrontendEndpointsClient(config.SubscriptionID)
	fdClient := frontdoor.NewFrontDoorsClient(config.SubscriptionID)
	fdRoutingRulesClient := frontdoor.NewRoutingRulesClient(config.SubscriptionID)
	fdLoadbalancerSettingsClient := frontdoor.NewLoadBalancingSettingsClient(config.SubscriptionID)
	fdHealthCheckClient := frontdoor.NewHealthProbeSettingsClient(config.SubscriptionID)

	if config.DebugAPICalls {
		fdBackendClient.RequestInspector = logRequest()
		fdBackendClient.ResponseInspector = logResponse()
		fdFrontendEndpointClient.RequestInspector = logRequest()
		fdFrontendEndpointClient.ResponseInspector = logResponse()
		fdClient.RequestInspector = logRequest()
		fdClient.ResponseInspector = logResponse()
		fdRoutingRulesClient.RequestInspector = logRequest()
		fdRoutingRulesClient.ResponseInspector = logResponse()
		fdLoadbalancerSettingsClient.RequestInspector = logRequest()
		fdLoadbalancerSettingsClient.ResponseInspector = logResponse()
		fdHealthCheckClient.RequestInspector = logRequest()
		fdHealthCheckClient.ResponseInspector = logResponse()
	}

	// create an authorizer from env vars or Azure Managed Service Idenity
	authorizer, err := auth.NewAuthorizerFromEnvironment()
	if err == nil {
		fdBackendClient.Authorizer = authorizer
		fdFrontendEndpointClient.Authorizer = authorizer
		fdClient.Authorizer = authorizer
		fdRoutingRulesClient.Authorizer = authorizer
		fdLoadbalancerSettingsClient.Authorizer = authorizer
		fdHealthCheckClient.Authorizer = authorizer
	}

	fdBackend := frontdoor.Backend{
		Address:      to.StringPtr(config.PrimaryIngressPublicIP),
		HTTPPort:     to.Int32Ptr(80),
		HTTPSPort:    to.Int32Ptr(443),
		EnabledState: frontdoor.EnabledStateEnumEnabled,
		Weight:       to.Int32Ptr(50),
		Priority:     to.Int32Ptr(1),
	}

	fdSynchronizer.getEndpoint = func() (frontdoor.FrontendEndpoint, error) {
		existingEntrypoints, err := fdFrontendEndpointClient.ListByFrontDoor(ctx, config.ResourceGroupName, config.FrontDoorName)
		if err != nil {
			log.WithError(err).WithField("frontendEndpointName", config.FrontDoorHostname).Error("Failed to create lb settings object")
			return frontdoor.FrontendEndpoint{}, err
		}
		for _, entrypoint := range existingEntrypoints.Values() {
			if *entrypoint.HostName == config.FrontDoorHostname {
				return entrypoint, nil
			}
		}
		return frontdoor.FrontendEndpoint{}, nil
	}

	fdSynchronizer.getOrCreateBackendPool = func() (frontdoor.BackendPool, error) {
		// Check if there is already a backend pool
		existingBackendPool, err := fdBackendClient.Get(ctx, config.ResourceGroupName, config.FrontDoorName, config.BackendPoolName)
		if err != nil {
			lbdetails, err := fdLoadbalancerSettingsClient.ListByFrontDoor(ctx, config.ResourceGroupName, config.FrontDoorName)

			log.WithField("fsLbs", lbdetails.Values()).Info("Found existing lbs")

			lbSettings, err := fdLoadbalancerSettingsClient.CreateOrUpdate(ctx, config.ResourceGroupName, config.FrontDoorName, "lbsettings", frontdoor.LoadBalancingSettingsModel{
				LoadBalancingSettingsProperties: &frontdoor.LoadBalancingSettingsProperties{
					AdditionalLatencyMilliseconds: to.Int32Ptr(0),
					SampleSize:                    to.Int32Ptr(4),
					SuccessfulSamplesRequired:     to.Int32Ptr(2),
				},
			})
			if err != nil {
				log.WithError(err).Error("Failed to create lb settings object")
				return frontdoor.BackendPool{}, err
			}

			log.WithField("lgSettings", lbSettings).Info("Created lb settings")

			// creating BackendPoolProperties
			backends := &[]frontdoor.Backend{fdBackend}

			// Create a backend pool if it doesn't already exist
			createFuture, err := fdBackendClient.CreateOrUpdate(ctx, config.ResourceGroupName, config.FrontDoorName, config.BackendPoolName, frontdoor.BackendPool{
				Name: to.StringPtr(config.BackendPoolName),
				BackendPoolProperties: &frontdoor.BackendPoolProperties{
					Backends: backends,
				},
			})

			if err != nil {
				log.WithError(err).Error("Creating backend pool failed")
				return frontdoor.BackendPool{}, err
			}

			bp, err := createFuture.Result(fdBackendClient)
			if err != nil {
				log.WithError(err).Error("Creating backend pool failed after wait")
			}

			return bp, nil
		}
		return existingBackendPool, nil
	}

	return &fdSynchronizer, nil

}
