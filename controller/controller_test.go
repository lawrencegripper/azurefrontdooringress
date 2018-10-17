package controller

import (
	"context"
	"testing"

	"github.com/lawrencegripper/azurefrontdooringress/utils"
	v1beta1 "k8s.io/api/extensions/v1beta1"
)

type DummySyncProvider struct{}

// Sync Acquire a lock and update Frontdoor with the ingress information provided
func (p *DummySyncProvider) Sync(ctx context.Context, ingressToSync []*v1beta1.Ingress) error {
	logger := utils.GetLogger(ctx)
	logger.Warn("No sync logic currently present, blocked on bug: https://github.com/Azure/azure-rest-api-specs/issues/4221")
	return nil
}

func TestControllerFindsAnnotatedService(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode.")
	}

	testCases := []struct {
		name                 string
		expectedError        bool
		expectedIngressCount int
	}{
		{
			name:                 "noannotations",
			expectedError:        true,
			expectedIngressCount: 0,
		},
		{
			name:                 "disabled",
			expectedError:        true,
			expectedIngressCount: 0,
		},
		{
			name:                 "enabled",
			expectedError:        false,
			expectedIngressCount: 2,
		},
	}

	for _, test := range testCases {
		test := test
		t.Run("Namespace:"+test.name, func(t *testing.T) {
			ingress, err := Start(context.Background(), test.name, &DummySyncProvider{})
			if err != nil {
				if test.expectedError {
					t.Logf("Expected error and got error: %+v", err)
					return
				}
				t.Logf("DIDN'T expect error and got error: %+v", err)
				t.Fail()
			}

			if len(ingress) != test.expectedIngressCount {
				t.Errorf("Expected ingress count %v but got %v", test.expectedIngressCount, len(ingress))
			}
		})
	}
}
