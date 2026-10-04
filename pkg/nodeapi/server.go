package nodeapi

import (
	"context"
	"fmt"
	"net/http"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/authentication/request/x509"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	openapinamer "k8s.io/apiserver/pkg/endpoints/openapi"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/server/dynamiccertificates"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	basecompatibility "k8s.io/component-base/compatibility"

	"github.com/tym83/kuberoot/pkg/apis/node"
	"github.com/tym83/kuberoot/pkg/apis/node/install"
	"github.com/tym83/kuberoot/pkg/generated/openapi"
	"github.com/tym83/kuberoot/pkg/supervisor"
)

// AdminGroup is the client certificate organization that may manage the node.
const AdminGroup = "kuberoot:node-admins"

type Options struct {
	NodeName   string
	BindPort   int
	CertFile   string
	KeyFile    string
	ClientCA   string
	Kubeconfig map[string]string // name -> file of cluster credentials to hand out
}

var (
	scheme = runtime.NewScheme()
	codecs = serializer.NewCodecFactory(scheme)
)

func init() {
	install.Install(scheme)
	metav1.AddToGroupVersion(scheme, schema.GroupVersion{Version: "v1"})
	unversioned := schema.GroupVersion{Group: "", Version: "v1"}
	scheme.AddUnversionedTypes(unversioned,
		&metav1.Status{}, &metav1.APIVersions{}, &metav1.APIGroupList{}, &metav1.APIGroup{}, &metav1.APIResourceList{})
}

// Run serves the node API until ctx is cancelled.
func Run(ctx context.Context, o Options) error {
	cfg := genericapiserver.NewRecommendedConfig(codecs)
	cfg.EffectiveVersion = basecompatibility.NewEffectiveVersionFromString("1.37", "", "")

	serving := genericoptions.NewSecureServingOptions().WithLoopback()
	serving.BindPort = o.BindPort
	serving.ServerCert.CertKey = genericoptions.CertKey{CertFile: o.CertFile, KeyFile: o.KeyFile}
	if err := serving.ApplyTo(&cfg.SecureServing, &cfg.LoopbackClientConfig); err != nil {
		return fmt.Errorf("serving: %w", err)
	}

	ca, err := dynamiccertificates.NewDynamicCAContentFromFile("node-client-ca", o.ClientCA)
	if err != nil {
		return fmt.Errorf("client CA: %w", err)
	}
	cfg.SecureServing.ClientCA = ca // makes the TLS handshake ask for a client certificate
	cfg.Authentication.Authenticator = x509.NewDynamic(ca.VerifyOptions, x509.CommonNameUserConversion)
	cfg.Authorization.Authorizer = authorizer.AuthorizerFunc(authorize)
	genericapiserver.AuthorizeClientBearerToken(cfg.LoopbackClientConfig, &cfg.Authentication, &cfg.Authorization)

	namer := openapinamer.NewDefinitionNamer(scheme)
	cfg.OpenAPIConfig = genericapiserver.DefaultOpenAPIConfig(openapi.GetOpenAPIDefinitions, namer)
	cfg.OpenAPIConfig.Info.Title = "kuberoot node"
	cfg.OpenAPIV3Config = genericapiserver.DefaultOpenAPIV3Config(openapi.GetOpenAPIDefinitions, namer)
	cfg.OpenAPIV3Config.Info.Title = "kuberoot node"

	chain := cfg.BuildHandlerChainFunc
	cfg.BuildHandlerChainFunc = func(h http.Handler, c *genericapiserver.Config) http.Handler {
		return chain(withRequestHost(h), c)
	}

	server, err := cfg.Complete().New("kuberoot-node", genericapiserver.NewEmptyDelegate())
	if err != nil {
		return err
	}
	kinit := supervisor.NewClient()
	group := genericapiserver.NewDefaultAPIGroupInfo(node.GroupName, scheme, metav1.ParameterCodec, codecs)
	group.VersionedResourcesStorageMap["v1alpha1"] = map[string]rest.Storage{
		"osconfigs":        newOSConfigStorage(o.NodeName),
		"nodeservices":     newServiceStorage(kinit),
		"nodeservices/log": &logStorage{kinit: kinit},
		"kubeconfigs":      &kubeconfigStorage{files: o.Kubeconfig},
	}
	if err := server.InstallAPIGroup(&group); err != nil {
		return err
	}
	return server.PrepareRun().RunWithContext(ctx)
}

// authorize lets node admins do everything and everyone else nothing.
func authorize(_ context.Context, a authorizer.Attributes) (authorizer.Decision, string, error) {
	for _, g := range a.GetUser().GetGroups() {
		if g == AdminGroup || g == "system:masters" {
			return authorizer.DecisionAllow, "", nil
		}
	}
	return authorizer.DecisionNoOpinion, "not a node admin", nil
}

func withRequestHost(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestHostKey{}, r.Host)))
	})
}
