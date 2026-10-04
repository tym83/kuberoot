package nodeapi

import (
	"context"
	"fmt"
	"net/http"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/request/headerrequest"
	"k8s.io/apiserver/pkg/authentication/request/union"
	"k8s.io/apiserver/pkg/authentication/request/x509"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/authorization/authorizerfactory"
	authorizerunion "k8s.io/apiserver/pkg/authorization/union"
	openapinamer "k8s.io/apiserver/pkg/endpoints/openapi"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/apiserver/pkg/server/dynamiccertificates"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
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

	// Aggregation into the cluster API server; all optional, the node API
	// works standalone without them.
	RequestHeaderCA   string // front proxy CA the cluster API server forwards requests with
	ClusterCertFile   string // serving certificate signed by the cluster CA, chosen by SNI
	ClusterKeyFile    string
	ClusterKubeconfig string // identity for SubjectAccessReviews against the cluster
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

	if err := configureAuth(cfg, o); err != nil {
		return err
	}
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
		"disks":            diskStorage{},
		"installations":    &installationStorage{kinit: kinit},
		"bootentries":      bootEntryStorage{},
		"upgrades":         &upgradeStorage{kinit: kinit},
	}
	if err := server.InstallAPIGroup(&group); err != nil {
		return err
	}
	go assessBoot(ctx, kinit)
	return server.PrepareRun().RunWithContext(ctx)
}

// configureAuth accepts two kinds of callers: node admins with a certificate of
// the node CA, and cluster users forwarded by the cluster API server, whose
// permissions the cluster's RBAC decides.
func configureAuth(cfg *genericapiserver.RecommendedConfig, o Options) error {
	nodeCA, err := dynamiccertificates.NewDynamicCAContentFromFile("node-client-ca", o.ClientCA)
	if err != nil {
		return fmt.Errorf("client CA: %w", err)
	}
	authenticators := []authenticator.Request{x509.NewDynamic(nodeCA.VerifyOptions, x509.CommonNameUserConversion)}
	authorizers := []authorizerunion.NamedAuthorizer{{AuthorizerName: "node-admins", Authorizer: authorizer.AuthorizerFunc(authorize)}}
	clientCAs := []dynamiccertificates.CAContentProvider{nodeCA}

	if o.RequestHeaderCA != "" {
		frontProxy, err := dynamiccertificates.NewDynamicCAContentFromFile("front-proxy-ca", o.RequestHeaderCA)
		if err != nil {
			return fmt.Errorf("request header CA: %w", err)
		}
		clientCAs = append(clientCAs, frontProxy)
		authenticators = append(authenticators, headerrequest.NewDynamicVerifyOptionsSecure(
			frontProxy.VerifyOptions,
			headerrequest.StaticStringSlice{"front-proxy-client"},
			headerrequest.StaticStringSlice{"X-Remote-User"},
			headerrequest.StaticStringSlice{"X-Remote-Uid"},
			headerrequest.StaticStringSlice{"X-Remote-Group"},
			headerrequest.StaticStringSlice{"X-Remote-Extra-"},
		))
	}
	if o.ClusterCertFile != "" {
		sni, err := dynamiccertificates.NewDynamicSNIContentFromFiles("cluster-serving", o.ClusterCertFile, o.ClusterKeyFile)
		if err != nil {
			return fmt.Errorf("cluster serving certificate: %w", err)
		}
		cfg.SecureServing.SNICerts = append(cfg.SecureServing.SNICerts, sni)
	}
	if o.ClusterKubeconfig != "" {
		delegated, err := delegatedAuthorizer(o.ClusterKubeconfig)
		if err != nil {
			return err
		}
		authorizers = append(authorizers, authorizerunion.NamedAuthorizer{AuthorizerName: "cluster", Authorizer: delegated})
	}
	cfg.SecureServing.ClientCA = dynamiccertificates.NewUnionCAContentProvider(clientCAs...)
	cfg.Authentication.Authenticator = union.New(authenticators...)
	authz, err := authorizerunion.New(authorizers...)
	if err != nil {
		return err
	}
	cfg.Authorization.Authorizer = authz
	return nil
}

// delegatedAuthorizer asks the cluster whether a forwarded user may do something.
// The kubeconfig may point at a cluster that is not up yet; checks fail until it is.
func delegatedAuthorizer(kubeconfig string) (authorizer.Authorizer, error) {
	restCfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("cluster kubeconfig: %w", err)
	}
	client, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, err
	}
	return authorizerfactory.DelegatingAuthorizerConfig{
		SubjectAccessReviewClient: client.AuthorizationV1(),
		AllowCacheTTL:             10 * time.Second,
		DenyCacheTTL:              10 * time.Second,
		WebhookRetryBackoff:       genericoptions.DefaultAuthWebhookRetryBackoff(),
	}.New()
}

// authorize lets node admins do everything and leaves everyone else to the cluster.
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
