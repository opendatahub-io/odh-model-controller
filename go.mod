module github.com/opendatahub-io/odh-model-controller

go 1.19

require (
	github.com/go-logr/logr v1.2.4
	github.com/kserve/kserve v0.11.0
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.27.6
	github.com/opendatahub-io/model-registry v0.0.0-20231219095158-e9b0e9d3698e
	github.com/openshift/api v3.9.0+incompatible
	github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring v0.64.1
	go.uber.org/zap v1.26.0
	google.golang.org/grpc v1.60.0
	istio.io/api v0.0.0-20230712174848-a2b2de508c88
	istio.io/client-go v1.17.4
	k8s.io/api v0.27.6
	k8s.io/apimachinery v0.27.6
	k8s.io/client-go v0.27.6
	k8s.io/utils v0.0.0-20230505201702-9f6742963106
	knative.dev/pkg v0.0.0-20231023151236-29775d7c9e5c
	maistra.io/api v0.0.0-20230417135504-0536f6c22b1c
	sigs.k8s.io/controller-runtime v0.14.6
	sigs.k8s.io/yaml v1.3.0
)

require (
	cloud.google.com/go v0.110.10 // indirect
	cloud.google.com/go/compute v1.23.3 // indirect
	cloud.google.com/go/compute/metadata v0.2.3 // indirect
	cloud.google.com/go/iam v1.1.5 // indirect
	cloud.google.com/go/storage v1.33.0 // indirect
	github.com/aws/aws-sdk-go v1.44.264 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blendle/zapdriver v1.3.1 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/emicklei/go-restful/v3 v3.10.2 // indirect
	github.com/evanphx/json-patch/v5 v5.7.0 // indirect
	github.com/fsnotify/fsnotify v1.7.0 // indirect
	github.com/go-logr/zapr v1.2.4 // indirect
	github.com/go-openapi/jsonpointer v0.19.6 // indirect
	github.com/go-openapi/jsonreference v0.20.2 // indirect
	github.com/go-openapi/swag v0.22.3 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/glog v1.2.0 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/google/gnostic v0.6.9 // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/go-containerregistry v0.15.2 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/s2a-go v0.1.7 // indirect
	github.com/google/uuid v1.5.0 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.2 // indirect
	github.com/googleapis/gax-go/v2 v2.12.0 // indirect
	github.com/googleapis/google-cloud-go-testing v0.0.0-20210719221736-1c9a4c676720 // indirect
	github.com/imdario/mergo v0.3.15 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.4 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/nxadm/tail v1.4.8 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/prometheus/client_golang v1.17.0 // indirect
	github.com/prometheus/client_model v0.4.1-0.20230718164431-9a2bf3000d16 // indirect
	github.com/prometheus/common v0.44.0 // indirect
	github.com/prometheus/procfs v0.11.1 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/crypto v0.16.0 // indirect
	golang.org/x/net v0.19.0 // indirect
	golang.org/x/oauth2 v0.13.0 // indirect
	golang.org/x/sync v0.4.0 // indirect
	golang.org/x/sys v0.15.0 // indirect
	golang.org/x/term v0.15.0 // indirect
	golang.org/x/text v0.14.0 // indirect
	golang.org/x/time v0.3.0 // indirect
	golang.org/x/xerrors v0.0.0-20220907171357-04be3eba64a2 // indirect
	gomodules.xyz/jsonpatch/v2 v2.4.0 // indirect
	google.golang.org/api v0.149.0 // indirect
	google.golang.org/appengine v1.6.8 // indirect
	google.golang.org/genproto v0.0.0-20231106174013-bbf56f31fb17 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20231030173426-d783a09b4405 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20231120223509-83a465c0220f // indirect
	google.golang.org/protobuf v1.31.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/tomb.v1 v1.0.0-20141024135613-dd632973f1e7 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/apiextensions-apiserver v0.27.6 // indirect
	k8s.io/component-base v0.27.6 // indirect
	k8s.io/klog/v2 v2.100.1 // indirect
	k8s.io/kube-openapi v0.0.0-20230515203736-54b630e78af5 // indirect
	knative.dev/networking v0.0.0-20231017124814-2a7676e912b7 // indirect
	knative.dev/serving v0.37.1 // indirect
	sigs.k8s.io/json v0.0.0-20221116044647-bc3834ca7abd // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.2.3 // indirect
)

replace (
	// Fixes CVE-2022-21698 and CVE-2023-45142
	// this dependency comes from k8s.io/component-base@v0.26.4 and k8s.io/apiextensions-apiserver@v0.26.4
	// before removing it make sure that the next version of the related k8s dependencies contains the fix
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp => go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.44.0
	// The crypto is pulled from go/compute which is pulled by go/storage
	// this replace can be removed when version 1.36.1 of go/storage is released.
	// https://github.com/googleapis/google-cloud-go/tree/main/storage
	// Fixes CVE-2023-48795 - golang.org/x/crypto Authentication Bypass by Capture-replay
	golang.org/x/crypto => golang.org/x/crypto v0.17.0
	// can be removed when the indirect depdency is in the same version or higher
	// Fixes Stack-based Buffer Overflow on protobuf
	// https://security.snyk.io/vuln/SNYK-GOLANG-GOOGLEGOLANGORGPROTOBUFENCODINGPROTOJSON-6137908
	google.golang.org/protobuf => google.golang.org/protobuf v1.32.0
	// pin to 0.26.4 to avoid https://github.com/kubernetes-sigs/controller-runtime/issues/2302
	k8s.io/api => k8s.io/api v0.26.4
	// remove when upgrade to controller-runtime 0.15.x or apimachinery to 0.27.x
	// Fixes github.com/elazarl/goproxy Denial of Service (DoS)
	// This dependency was removed from apimachinery 0.27.0
	k8s.io/apimachinery => k8s.io/apimachinery v0.27.0
	// pin to 0.26.4 to avoid https://github.com/kubernetes-sigs/controller-runtime/issues/2302
	k8s.io/client-go => k8s.io/client-go v0.26.4
	// Watch future versions of kserve where knative/serving will be updated.
	// Fixes knative.dev/serving Uncontrolled Resource Consumption
	// https://www.cve.org/CVERecord?id=CVE-2023-48713
	knative.dev/serving => knative.dev/serving v0.39.3
)
