module github.com/kserve/kserve/qpext

go 1.23.0

toolchain go1.23.8

require (
	github.com/hashicorp/go-multierror v1.1.1
	github.com/prometheus/client_model v0.4.0
	github.com/prometheus/common v0.43.0
	github.com/stretchr/testify v1.8.1
	go.uber.org/zap v1.24.0
	knative.dev/serving v0.37.1
)

require (
	contrib.go.opencensus.io/exporter/ocagent v0.7.1-0.20200907061046-05415f1de66d // indirect
	contrib.go.opencensus.io/exporter/prometheus v0.4.2 // indirect
	contrib.go.opencensus.io/exporter/zipkin v0.1.2 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blendle/zapdriver v1.3.1 // indirect
	github.com/census-instrumentation/opencensus-proto v0.4.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/evanphx/json-patch/v5 v5.6.0 // indirect
	github.com/go-kit/log v0.2.1 // indirect
	github.com/go-logfmt/logfmt v0.6.0 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/go-cmp v0.6.0 // indirect
	github.com/google/go-containerregistry v0.15.2 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/gorilla/websocket v1.5.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.15.2 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kelseyhightower/envconfig v1.4.0 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.4 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/openzipkin/zipkin-go v0.4.1 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/client_golang v1.15.1 // indirect
	github.com/prometheus/procfs v0.9.0 // indirect
	github.com/prometheus/statsd_exporter v0.23.1 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/automaxprocs v1.5.2 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/net v0.37.0 // indirect
	golang.org/x/oauth2 v0.25.0 // indirect
	golang.org/x/sync v0.12.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
	golang.org/x/term v0.18.0 // indirect
	golang.org/x/text v0.23.0 // indirect
	golang.org/x/time v0.3.0 // indirect
	gomodules.xyz/jsonpatch/v2 v2.2.0 // indirect
	google.golang.org/api v0.122.0 // indirect
	google.golang.org/genproto v0.0.0-20250404141209-ee84b53bf3d0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20250324211829-b45e905df463 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250324211829-b45e905df463 // indirect
	google.golang.org/grpc v1.71.0 // indirect
	google.golang.org/protobuf v1.36.6 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/api v0.27.1 // indirect
	k8s.io/apimachinery v0.27.1 // indirect
	k8s.io/client-go v0.27.1 // indirect
	k8s.io/klog/v2 v2.100.1 // indirect
	k8s.io/utils v0.0.0-20230505201702-9f6742963106 // indirect
	knative.dev/control-protocol v0.0.0-20230501014357-da93cf1acdb9 // indirect
	knative.dev/networking v0.0.0-20230511122402-33636d99d870 // indirect
	knative.dev/pkg v0.0.0-20230502134655-db8a35330281 // indirect
	sigs.k8s.io/json v0.0.0-20221116044647-bc3834ca7abd // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.2.3 // indirect
	sigs.k8s.io/yaml v1.3.0 // indirect
)

// CVE-2023-45288][2.8]  golang.org/x/net/http2 Allocation of Resources Without Limits or Throttling
replace golang.org/x/net => golang.org/x/net v0.23.0
