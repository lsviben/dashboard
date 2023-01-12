package auth

import (
	"k8c.io/dashboard/v2/pkg/handler/auth"
	"k8c.io/dashboard/v2/pkg/provider"
	authtypes "k8c.io/dashboard/v2/pkg/provider/auth/types"
	kubermaticv1 "k8c.io/kubermatic/v2/pkg/apis/kubermatic/v1"
	"k8c.io/kubermatic/v2/pkg/resources/certificates"
	"k8s.io/apimachinery/pkg/api/meta"
)

func OIDCIssuerVerifierProviderFactory(
	mapper meta.RESTMapper,
	seedKubeconfigGetter provider.SeedKubeconfigGetter,
	oidcIssuerRedirectURI string,
	oidcSkipTLSVerify bool,
	caBundle *certificates.CABundle,
) provider.OIDCIssuerVerifierGetter {
	return func(seed *kubermaticv1.Seed) (authtypes.OIDCIssuerVerifier, error) {

		// TODO map stuff
		oidc := seed.Spec.OIDCProviderConfiguration
		return auth.NewOpenIDClient(
			oidc.IssuerURL,
			oidc.IssuerClientID,
			oidc.IssuerClientSecret,
			oidcIssuerRedirectURI,
			auth.NewCombinedExtractor(
				auth.NewHeaderBearerTokenExtractor("Authorization"),
				auth.NewQueryParamBearerTokenExtractor("token"),
			),
			oidcSkipTLSVerify,
			caBundle.CertPool(),
		)
	}
}