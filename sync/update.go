package sync

import (
	"context"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/preview/frontdoor/mgmt/2018-08-01-preview/frontdoor"
	"github.com/Azure/go-autorest/autorest/azure/auth"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/lawrencegripper/azurefrontdooringress/utils"
	azlock "github.com/lawrencegripper/goazurelocking"
	// log "github.com/sirupsen/logrus"
	v1beta1 "k8s.io/api/extensions/v1beta1"
)

// Provider the interface any Syncronizers are required to meet
type Provider interface {
	Sync(ctx context.Context, ingressToSync []*v1beta1.Ingress) error
}

// Synchronizer is used to communicate with the frontdoor instance
type Synchronizer struct {
	getLock         func() (*azlock.Lock, error)
	getCurrentState func(context.Context) (frontdoor.FrontDoor, error)
	updateState     func(context.Context, frontdoor.FrontDoor) (frontdoor.FrontDoor, error)
	backendPool     frontdoor.BackendPool
	endPoint        frontdoor.FrontendEndpoint
	client          frontdoor.FrontDoorsClient
}

// Sync Acquire a lock and update Frontdoor with the ingress information provided
func (p *Synchronizer) Sync(ctx context.Context, ingressToSync []*v1beta1.Ingress) error {
	logger := utils.GetLogger(ctx)
	logger.Info("Starting sync of routing rules")

	lock, err := p.getLock()
	if err != nil {
		return err
	}
	defer lock.Unlock() //nolint: errcheck

	fdState, err := p.getCurrentState(ctx)
	if err != nil {
		return err
	}

	rulesToAdd := []frontdoor.RoutingRule{}

	for _, ingress := range ingressToSync {
		if ingress == nil {
			logger.Warn("nil ingress passed to sync")
			continue
		}

		for _, rule := range ingress.Spec.Rules {
			patternsToMatch := []string{}
			for _, path := range rule.HTTP.Paths {
				patternsToMatch = append(patternsToMatch, path.Path)
			}
			rulesToAdd = append(rulesToAdd, frontdoor.RoutingRule{
				Name: to.StringPtr(fmt.Sprintf("Ingress-%s", ingress.Name)),
				RoutingRuleProperties: &frontdoor.RoutingRuleProperties{
					AcceptedProtocols: &[]frontdoor.Protocol{frontdoor.HTTP, frontdoor.HTTPS},
					BackendPool: &frontdoor.SubResource{
						ID: p.backendPool.ID,
					},
					PatternsToMatch: &patternsToMatch,
					EnabledState:    frontdoor.EnabledStateEnumEnabled,
					FrontendEndpoints: &[]frontdoor.SubResource{
						{
							ID: p.endPoint.ID,
						},
					},
				},
			})
		}
	}

	if fdState.RoutingRules != nil {
		rulesDeref := *fdState.RoutingRules
		rulesDeref = append(rulesDeref, rulesToAdd...)
		fdState.RoutingRules = &rulesDeref
	} else {
		fdState.RoutingRules = &rulesToAdd
	}

	_, err = p.updateState(ctx, fdState)

	return err
}

// NewFontDoorSyncer creates a new FrontDoor provider with require configuration
// for use when updating frontdoor0
func NewFontDoorSyncer(ctx context.Context, config utils.Config) (*Synchronizer, error) {
	fdSynchronizer := Synchronizer{}

	// Create a Azure lockInstance (using blob) and lock it
	// lock on the name of the frontdoor so that
	// other ingress instances can't update while
	// this instance is making changes
	fdSynchronizer.getLock = func() (*azlock.Lock, error) {
		lock, err := azlock.NewLockInstance(ctx,
			config.StorageAccountURL,
			config.StorageAccountKey,
			config.FrontDoorName,
			time.Duration(time.Second*15))

		if err != nil {
			return nil, err
		}

		err = lock.Lock()
		if err != nil {
			return nil, err
		}
		return lock, nil
	}

	lock, err := fdSynchronizer.getLock()
	if err != nil {
		return nil, err
	}
	defer lock.Unlock() //nolint: errcheck

	// create clients for frontdoor
	fdClient := frontdoor.NewFrontDoorsClient(config.SubscriptionID)

	if config.DebugAPICalls {
		fdClient.RequestInspector = logRequest()
		fdClient.ResponseInspector = logResponse()
	}

	// create an authorizer from env vars or Azure Managed Service Idenity
	authorizer, err := auth.NewAuthorizerFromEnvironment()
	if err == nil {
		fdClient.Authorizer = authorizer
	}

	fdSynchronizer.client = fdClient

	fdSynchronizer.getCurrentState = func(ctx context.Context) (frontdoor.FrontDoor, error) {
		return fdClient.Get(ctx, config.ResourceGroupName, config.FrontDoorName)
	}

	currentConfig, err := fdSynchronizer.getCurrentState(ctx)
	if err != nil {
		return nil, err
	}

	clusterBackend := frontdoor.Backend{
		Address:      to.StringPtr(config.PrimaryIngressPublicIP),
		HTTPPort:     to.Int32Ptr(80),
		HTTPSPort:    to.Int32Ptr(443),
		EnabledState: frontdoor.EnabledStateEnumEnabled,
		Weight:       to.Int32Ptr(50),
		Priority:     to.Int32Ptr(1),
	}

	// Check for existing backend
	backendExists := false
	if currentConfig.BackendPools != nil && len(*currentConfig.BackendPools) > 0 {
		for _, pool := range *currentConfig.BackendPools {
			// Find the pool for the cluster and update
			if *pool.Name == config.ClusterName {
				backendExists = true
				addFrontdoor := append(*pool.BackendPoolProperties.Backends, clusterBackend)
				pool.BackendPoolProperties.Backends = &addFrontdoor
			}
		}
	}

	if !backendExists {
		return nil, fmt.Errorf("Frontdoor instance doesn't have a backendPool for cluster, require a configured pool named %s to exist", config.ClusterName)
	}

	// Check for existing frontend
	foundEndPoint := false
	if currentConfig.FrontendEndpoints != nil {
		for _, fe := range *currentConfig.FrontendEndpoints {
			if fe.HostName != nil && *fe.HostName == config.FrontDoorHostname {
				foundEndPoint = true
				fdSynchronizer.endPoint = fe
			}
		}
	}
	if !foundEndPoint {
		return nil, fmt.Errorf("Frontdoor instance doesn't have a frontend which matches the provided hostname, require a configured pool named %s to exist", config.FrontDoorHostname)
	}

	fdSynchronizer.updateState = func(ctx context.Context, fd frontdoor.FrontDoor) (frontdoor.FrontDoor, error) {
		updatedFd, err := fdClient.CreateOrUpdate(ctx, config.ResourceGroupName, config.FrontDoorName, fd)
		if err != nil {
			return frontdoor.FrontDoor{}, err
		}

		err = updatedFd.WaitForCompletion(ctx, fdClient.Client)
		if err != nil {
			return frontdoor.FrontDoor{}, err
		}

		res, err := updatedFd.Result(fdClient)
		if err != nil {
			return frontdoor.FrontDoor{}, err
		}
		return res, nil
	}

	state, err := fdSynchronizer.updateState(ctx, currentConfig)
	if err != nil {
		return nil, err
	}

	for _, pool := range *state.BackendPools {
		// Find the pool for the cluster and update
		if *pool.Name == config.ClusterName {
			fdSynchronizer.backendPool = pool
		}
	}

	return &fdSynchronizer, nil

}
