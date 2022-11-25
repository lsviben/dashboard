/*
Copyright 2020 The Kubermatic Kubernetes Platform contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package azure

import (
	"context"
	"errors"
	"fmt"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"go.uber.org/zap"

	"k8c.io/dashboard/v2/pkg/provider"
	kubermaticv1 "k8c.io/kubermatic/v2/pkg/apis/kubermatic/v1"
	"k8c.io/kubermatic/v2/pkg/log"
)

const (
	resourceNamePrefix = "kubernetes-"

	clusterTagKey = "cluster"

	// FinalizerSecurityGroup will instruct the deletion of the security group.
	FinalizerSecurityGroup = "kubermatic.k8c.io/cleanup-azure-security-group"
	// FinalizerRouteTable will instruct the deletion of the route table.
	FinalizerRouteTable = "kubermatic.k8c.io/cleanup-azure-route-table"
	// FinalizerSubnet will instruct the deletion of the subnet.
	FinalizerSubnet = "kubermatic.k8c.io/cleanup-azure-subnet"
	// FinalizerVNet will instruct the deletion of the virtual network.
	FinalizerVNet = "kubermatic.k8c.io/cleanup-azure-vnet"
	// FinalizerResourceGroup will instruct the deletion of the resource group.
	FinalizerResourceGroup = "kubermatic.k8c.io/cleanup-azure-resource-group"
	// FinalizerAvailabilitySet will instruct the deletion of the availability set.
	FinalizerAvailabilitySet = "kubermatic.k8c.io/cleanup-azure-availability-set"

	denyAllTCPSecGroupRuleName   = "deny_all_tcp"
	denyAllUDPSecGroupRuleName   = "deny_all_udp"
	allowAllICMPSecGroupRuleName = "icmp_by_allow_all"
)

type Azure struct {
	dc                *kubermaticv1.DatacenterSpecAzure
	log               *zap.SugaredLogger
	secretKeySelector provider.SecretKeySelectorValueFunc
}

// New returns a new Azure provider.
func New(dc *kubermaticv1.Datacenter, secretKeyGetter provider.SecretKeySelectorValueFunc) (*Azure, error) {
	if dc.Spec.Azure == nil {
		return nil, errors.New("datacenter is not an Azure datacenter")
	}
	return &Azure{
		dc:                dc.Spec.Azure,
		log:               log.Logger,
		secretKeySelector: secretKeyGetter,
	}, nil
}

var _ provider.CloudProvider = &Azure{}

// Azure API doesn't allow programmatically getting the number of available fault domains in a given region.
// We must therefore hardcode these based on https://docs.microsoft.com/en-us/azure/virtual-machines/windows/manage-availability
//
// The list of region codes was generated by `az account list-locations | jq .[].id --raw-output | cut -d/ -f5 | sed -e 's/^/"/' -e 's/$/" : ,/'`.
var faultDomainsPerRegion = map[string]int32{
	"eastasia":           2,
	"southeastasia":      2,
	"centralus":          3,
	"eastus":             3,
	"eastus2":            3,
	"westus":             3,
	"northcentralus":     3,
	"southcentralus":     3,
	"northeurope":        3,
	"westeurope":         3,
	"japanwest":          2,
	"japaneast":          2,
	"brazilsouth":        2,
	"australiaeast":      2,
	"australiasoutheast": 2,
	"southindia":         2,
	"centralindia":       2,
	"westindia":          2,
	"canadacentral":      3,
	"canadaeast":         2,
	"uksouth":            2,
	"ukwest":             2,
	"westcentralus":      2,
	"westus2":            2,
	"koreacentral":       2,
	"koreasouth":         2,
}

func (a *Azure) DefaultCloudSpec(ctx context.Context, cloud *kubermaticv1.CloudSpec) error {
	if cloud.Azure == nil {
		return errors.New("no Azure cloud spec found")
	}

	if cloud.Azure.LoadBalancerSKU == "" {
		cloud.Azure.LoadBalancerSKU = kubermaticv1.AzureBasicLBSKU
	}

	return nil
}

func (a *Azure) ValidateCloudSpec(ctx context.Context, cloud kubermaticv1.CloudSpec) error {
	credentials, err := GetCredentialsForCluster(cloud, a.secretKeySelector)
	if err != nil {
		return err
	}

	credential, err := credentials.ToAzureCredential()
	if err != nil {
		return err
	}

	if cloud.Azure.ResourceGroup != "" {
		rgClient, err := getGroupsClient(credential, credentials.SubscriptionID)
		if err != nil {
			return err
		}

		if _, err = rgClient.Get(ctx, cloud.Azure.ResourceGroup, nil); err != nil {
			return err
		}
	}

	var resourceGroup = cloud.Azure.ResourceGroup
	if cloud.Azure.VNetResourceGroup != "" {
		resourceGroup = cloud.Azure.VNetResourceGroup
	}

	if cloud.Azure.VNetName != "" {
		vnetClient, err := getNetworksClient(credential, credentials.SubscriptionID)
		if err != nil {
			return err
		}

		if _, err = vnetClient.Get(ctx, resourceGroup, cloud.Azure.VNetName, nil); err != nil {
			return err
		}
	}

	if cloud.Azure.SubnetName != "" {
		subnetClient, err := getSubnetsClient(credential, credentials.SubscriptionID)
		if err != nil {
			return err
		}

		if _, err = subnetClient.Get(ctx, resourceGroup, cloud.Azure.VNetName, cloud.Azure.SubnetName, nil); err != nil {
			return err
		}
	}

	if cloud.Azure.RouteTableName != "" {
		routeTablesClient, err := getRouteTablesClient(credential, credentials.SubscriptionID)
		if err != nil {
			return err
		}

		if _, err = routeTablesClient.Get(ctx, cloud.Azure.ResourceGroup, cloud.Azure.RouteTableName, nil); err != nil {
			return err
		}
	}

	if cloud.Azure.SecurityGroup != "" {
		sgClient, err := getSecurityGroupsClient(credential, credentials.SubscriptionID)
		if err != nil {
			return err
		}

		if _, err = sgClient.Get(ctx, cloud.Azure.ResourceGroup, cloud.Azure.SecurityGroup, nil); err != nil {
			return err
		}
	}

	return nil
}

// ValidateCloudSpecUpdate verifies whether an update of cloud spec is valid and permitted.
func (a *Azure) ValidateCloudSpecUpdate(_ context.Context, oldSpec kubermaticv1.CloudSpec, newSpec kubermaticv1.CloudSpec) error {
	if oldSpec.Azure == nil || newSpec.Azure == nil {
		return errors.New("'azure' spec is empty")
	}

	// we validate that a couple of resources are not changed.
	// the exception being the provider itself updating it in case the field
	// was left empty to dynamically generate resources.

	if oldSpec.Azure.ResourceGroup != "" && oldSpec.Azure.ResourceGroup != newSpec.Azure.ResourceGroup {
		return fmt.Errorf("updating Azure resource group is not supported (was %s, updated to %s)", oldSpec.Azure.ResourceGroup, newSpec.Azure.ResourceGroup)
	}

	if oldSpec.Azure.VNetResourceGroup != "" && oldSpec.Azure.VNetResourceGroup != newSpec.Azure.VNetResourceGroup {
		return fmt.Errorf("updating Azure vnet resource group is not supported (was %s, updated to %s)", oldSpec.Azure.VNetResourceGroup, newSpec.Azure.VNetResourceGroup)
	}

	if oldSpec.Azure.VNetName != "" && oldSpec.Azure.VNetName != newSpec.Azure.VNetName {
		return fmt.Errorf("updating Azure vnet name is not supported (was %s, updated to %s)", oldSpec.Azure.VNetName, newSpec.Azure.VNetName)
	}

	if oldSpec.Azure.SubnetName != "" && oldSpec.Azure.SubnetName != newSpec.Azure.SubnetName {
		return fmt.Errorf("updating Azure subnet name is not supported (was %s, updated to %s)", oldSpec.Azure.SubnetName, newSpec.Azure.SubnetName)
	}

	if oldSpec.Azure.RouteTableName != "" && oldSpec.Azure.RouteTableName != newSpec.Azure.RouteTableName {
		return fmt.Errorf("updating Azure route table name is not supported (was %s, updated to %s)", oldSpec.Azure.RouteTableName, newSpec.Azure.RouteTableName)
	}

	if oldSpec.Azure.SecurityGroup != "" && oldSpec.Azure.SecurityGroup != newSpec.Azure.SecurityGroup {
		return fmt.Errorf("updating Azure security group is not supported (was %s, updated to %s)", oldSpec.Azure.SecurityGroup, newSpec.Azure.SecurityGroup)
	}

	if oldSpec.Azure.AvailabilitySet != "" && oldSpec.Azure.AvailabilitySet != newSpec.Azure.AvailabilitySet {
		return fmt.Errorf("updating Azure availability set is not supported (was %s, updated to %s)", oldSpec.Azure.AvailabilitySet, newSpec.Azure.AvailabilitySet)
	}

	return nil
}

type Credentials struct {
	TenantID       string
	SubscriptionID string
	ClientID       string
	ClientSecret   string
}

func (c Credentials) ToAzureCredential() (*azidentity.ClientSecretCredential, error) {
	return azidentity.NewClientSecretCredential(c.TenantID, c.ClientID, c.ClientSecret, nil)
}
