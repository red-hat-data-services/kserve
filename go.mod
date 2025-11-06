module github.com/kserve/kserve

go 1.23.0

toolchain go1.23.8

require (
	cloud.google.com/go/storage v1.50.0
	github.com/aws/aws-sdk-go v1.44.264
	github.com/cloudevents/sdk-go v1.2.0
	github.com/fsnotify/fsnotify v1.6.0
	github.com/getkin/kin-openapi v0.116.0
	github.com/go-logr/logr v1.4.2
	github.com/gofrs/uuid/v5 v5.0.0
	github.com/gogo/protobuf v1.3.2
	github.com/golang/protobuf v1.5.4
	github.com/google/go-cmp v0.7.0
	github.com/google/uuid v1.6.0
	github.com/googleapis/google-cloud-go-testing v0.0.0-20210719221736-1c9a4c676720
	github.com/json-iterator/go v1.1.12
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/onsi/ginkgo/v2 v2.9.2
	github.com/onsi/gomega v1.27.6
	github.com/pkg/errors v0.9.1
	github.com/spf13/cobra v1.7.0
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.10.0
	github.com/tidwall/gjson v1.14.4
	go.uber.org/zap v1.26.0
	gomodules.xyz/jsonpatch/v2 v2.4.0
	google.golang.org/api v0.227.0
	google.golang.org/protobuf v1.36.6
	istio.io/api v0.0.0-20200715212100-dbf5277541ef
	istio.io/client-go v0.0.0-20201005161859-d8818315d678
	k8s.io/api v0.27.6
	k8s.io/apimachinery v0.27.6
	k8s.io/client-go v0.27.6
	k8s.io/code-generator v0.27.6
	k8s.io/klog v1.0.0
	k8s.io/kube-openapi v0.0.0-20230515203736-54b630e78af5
	knative.dev/networking v0.0.0-20231017124814-2a7676e912b7
	knative.dev/pkg v0.0.0-20231023151236-29775d7c9e5c
	knative.dev/serving v0.39.3
	sigs.k8s.io/controller-runtime v0.14.6
	sigs.k8s.io/yaml v1.3.0
)

require (
	cel.dev/expr v0.19.2 // indirect
	cloud.google.com/go v0.120.0 // indirect
	cloud.google.com/go/auth v0.15.0 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.7 // indirect
	cloud.google.com/go/compute/metadata v0.6.0 // indirect
	cloud.google.com/go/iam v1.5.0 // indirect
	cloud.google.com/go/monitoring v1.24.1 // indirect
	contrib.go.opencensus.io/exporter/ocagent v0.7.1-0.20200907061046-05415f1de66d // indirect
	contrib.go.opencensus.io/exporter/prometheus v0.4.2 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/detectors/gcp v1.25.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric v0.50.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/internal/resourcemapping v0.50.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blendle/zapdriver v1.3.1 // indirect
	github.com/census-instrumentation/opencensus-proto v0.4.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cncf/xds/go v0.0.0-20250121191232-2f005788dc42 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/emicklei/go-restful/v3 v3.10.2 // indirect
	github.com/envoyproxy/go-control-plane/envoy v1.32.4 // indirect
	github.com/envoyproxy/protoc-gen-validate v1.2.1 // indirect
	github.com/evanphx/json-patch v5.6.0+incompatible // indirect
	github.com/evanphx/json-patch/v5 v5.7.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/go-kit/log v0.2.1 // indirect
	github.com/go-logfmt/logfmt v0.6.0 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-logr/zapr v1.2.4 // indirect
	github.com/go-openapi/jsonpointer v0.19.6 // indirect
	github.com/go-openapi/jsonreference v0.20.2 // indirect
	github.com/go-openapi/swag v0.22.3 // indirect
	github.com/go-task/slim-sprig v0.0.0-20230315185526-52ccab3ef572 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/google/gnostic v0.6.9 // indirect
	github.com/google/go-containerregistry v0.15.2 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/pprof v0.0.0-20210720184732-4bb14d4b1be1 // indirect
	github.com/google/s2a-go v0.1.9 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.6 // indirect
	github.com/googleapis/gax-go/v2 v2.14.1 // indirect
	github.com/gorilla/websocket v1.5.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.15.2 // indirect
	github.com/hashicorp/golang-lru v1.0.2 // indirect
	github.com/imdario/mergo v0.3.15 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/invopop/yaml v0.2.0 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/lightstep/tracecontext.go v0.0.0-20181129014701-1757c391b1ac // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.4 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/mohae/deepcopy v0.0.0-20170929034955-c48cc78d4826 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/perimeterx/marshmallow v1.1.4 // indirect
	github.com/planetscale/vtprotobuf v0.6.1-0.20240319094008-0393e58bdf10 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/client_golang v1.17.0 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/prometheus/common v0.44.0 // indirect
	github.com/prometheus/procfs v0.11.1 // indirect
	github.com/prometheus/statsd_exporter v0.23.1 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
	go.opentelemetry.io/contrib/detectors/gcp v1.34.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.59.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.59.0 // indirect
	go.opentelemetry.io/otel v1.35.0 // indirect
	go.opentelemetry.io/otel/metric v1.35.0 // indirect
	go.opentelemetry.io/otel/sdk v1.35.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.34.0 // indirect
	go.opentelemetry.io/otel/trace v1.35.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/crypto v0.36.0 // indirect
	golang.org/x/mod v0.17.0 // indirect
	golang.org/x/net v0.37.0 // indirect
	golang.org/x/oauth2 v0.28.0 // indirect
	golang.org/x/sync v0.12.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
	golang.org/x/term v0.30.0 // indirect
	golang.org/x/text v0.23.0 // indirect
	golang.org/x/time v0.11.0 // indirect
	golang.org/x/tools v0.21.1-0.20240508182429-e35e4ccd0d2d // indirect
	google.golang.org/genproto v0.0.0-20250404141209-ee84b53bf3d0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20250324211829-b45e905df463 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250324211829-b45e905df463 // indirect
	google.golang.org/grpc v1.71.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	istio.io/gogo-genproto v0.0.0-20190930162913-45029607206a // indirect
	k8s.io/apiextensions-apiserver v0.27.6 // indirect
	k8s.io/component-base v0.27.6 // indirect
	k8s.io/gengo v0.0.0-20221011193443-fad74ee6edd9 // indirect
	k8s.io/klog/v2 v2.100.1 // indirect
	k8s.io/utils v0.0.0-20230505201702-9f6742963106 // indirect
	sigs.k8s.io/json v0.0.0-20221116044647-bc3834ca7abd // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.2.3 // indirect
)

replace (
	// Fixes CVE-2022-21698 and CVE-2023-45142
	// this dependency comes from k8s.io/component-base@v0.28.4 and k8s.io/apiextensions-apiserver@v0.28.4
	// before removing it make sure that the next version of the related k8s dependencies contains the fix
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp => go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.44.0

	// The crypto is pulled from go/compute which is pulled by go/storage
	// this replace can be removed when version 1.36.1 of go/storage is released.
	// https://github.com/googleapis/google-cloud-go/tree/main/storage
	// Fixes CVE-2023-48795 - golang.org/x/crypto Authentication Bypass by Capture-replay
	golang.org/x/crypto => golang.org/x/crypto v0.17.0

	// pin to 0.26.4 to avoid https://github.com/kubernetes-sigs/controller-runtime/issues/2302
	k8s.io/api => k8s.io/api v0.26.4

	// Fixes github.com/elazarl/goproxy Denial of Service (DoS), the dependency was removed in v0.27.0
	// Remove below when upgrading to controller-runtime 0.15.x or apimachinery to 0.27.x
	k8s.io/apimachinery => k8s.io/apimachinery v0.27.0
	k8s.io/client-go => k8s.io/client-go v0.26.4
	k8s.io/code-generator => k8s.io/code-generator v0.26.4
)
