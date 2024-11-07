module github.com/kserve/kserve

go 1.22.7

require (
	cloud.google.com/go/storage v1.43.0
	github.com/aws/aws-sdk-go v1.55.5
	github.com/cloudevents/sdk-go/v2 v2.15.2
	github.com/fsnotify/fsnotify v1.7.0
	github.com/getkin/kin-openapi v0.127.0
	github.com/go-logr/logr v1.4.2
	github.com/go-logr/zapr v1.3.0
	github.com/gofrs/uuid/v5 v5.3.0
	github.com/google/go-cmp v0.6.0
	github.com/google/uuid v1.6.0
	github.com/googleapis/google-cloud-go-testing v0.0.0-20210719221736-1c9a4c676720
	github.com/json-iterator/go v1.1.12
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/onsi/ginkgo/v2 v2.20.1
	github.com/onsi/gomega v1.34.2
	github.com/pkg/errors v0.9.1
	github.com/spf13/cobra v1.8.1
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.9.0
	github.com/tidwall/gjson v1.17.3
	go.uber.org/zap v1.27.0
	gomodules.xyz/jsonpatch/v2 v2.4.0
	google.golang.org/api v0.195.0
	google.golang.org/protobuf v1.34.2
	gopkg.in/go-playground/validator.v9 v9.31.0
	istio.io/api v1.23.0
	istio.io/client-go v1.23.0
	k8s.io/api v0.30.4
	k8s.io/apimachinery v0.30.4
	k8s.io/client-go v0.30.4
	k8s.io/code-generator v0.30.4
	k8s.io/component-helpers v0.30.4
	k8s.io/klog v1.0.0
	k8s.io/kube-openapi v0.0.0-20240827152857-f7e401e7b4c2
	k8s.io/utils v0.0.0-20240821151609-f90d01438635
	knative.dev/networking v0.0.0-20240815142417-37fdbdd0854b
	knative.dev/pkg v0.0.0-20240815051656-89743d9bbf7c
	knative.dev/serving v0.42.2
	sigs.k8s.io/controller-runtime v0.18.5
	sigs.k8s.io/yaml v1.4.0
)

require (
	bitbucket.org/creachadair/shell v0.0.7 // indirect
	cloud.google.com/go v0.115.1 // indirect
	cloud.google.com/go/auth v0.9.1 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.4 // indirect
	cloud.google.com/go/compute/metadata v0.5.0 // indirect
	cloud.google.com/go/iam v1.2.0 // indirect
	contrib.go.opencensus.io/exporter/ocagent v0.7.1-0.20200907061046-05415f1de66d // indirect
	contrib.go.opencensus.io/exporter/prometheus v0.4.2 // indirect
	github.com/Azure/go-ntlmssp v0.0.0-20220621081337-cb9428e4ac1e // indirect
	github.com/Masterminds/semver/v3 v3.2.0 // indirect
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blendle/zapdriver v1.3.1 // indirect
	github.com/boombuler/barcode v1.0.1-0.20190219062509-6c824513bacc // indirect
	github.com/bradfitz/gomemcache v0.0.0-20190329173943-551aad21a668 // indirect
	github.com/census-instrumentation/opencensus-proto v0.4.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cpuguy83/go-md2man/v2 v2.0.4 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/djherbis/buffer v1.2.0 // indirect
	github.com/djherbis/nio/v3 v3.0.1 // indirect
	github.com/editorconfig/editorconfig-core-go/v2 v2.5.1 // indirect
	github.com/emicklei/go-restful/v3 v3.12.1 // indirect
	github.com/evanphx/json-patch v5.9.0+incompatible // indirect
	github.com/evanphx/json-patch/v5 v5.9.0 // indirect
	github.com/fatih/color v1.13.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/go-asn1-ber/asn1-ber v1.5.4 // indirect
	github.com/go-kit/log v0.2.1 // indirect
	github.com/go-ldap/ldap/v3 v3.4.4 // indirect
	github.com/go-logfmt/logfmt v0.6.0 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-macaron/binding v1.2.0 // indirect
	github.com/go-macaron/cache v0.0.0-20190810181446-10f7c57e2196 // indirect
	github.com/go-macaron/captcha v0.2.0 // indirect
	github.com/go-macaron/csrf v0.0.0-20190812063352-946f6d303a4c // indirect
	github.com/go-macaron/gzip v0.0.0-20160222043647-cad1c6580a07 // indirect
	github.com/go-macaron/i18n v0.6.0 // indirect
	github.com/go-macaron/inject v0.0.0-20160627170012-d8a0b8677191 // indirect
	github.com/go-macaron/session v0.0.0-20190805070824-1a3cdc6f5659 // indirect
	github.com/go-macaron/toolbox v0.0.0-20190813233741-94defb8383c6 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/jsonreference v0.21.0 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-sql-driver/mysql v1.7.0 // indirect
	github.com/go-task/slim-sprig/v3 v3.0.0 // indirect
	github.com/go-test/deep v1.1.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/gogs/chardet v0.0.0-20150115103509-2404f7772561 // indirect
	github.com/gogs/cron v0.0.0-20171120032916-9f6c956d3e14 // indirect
	github.com/gogs/git-module v1.8.1 // indirect
	github.com/gogs/go-gogs-client v0.0.0-20200128182646-c69cb7680fd4 // indirect
	github.com/gogs/go-libravatar v0.0.0-20191106065024-33a75213d0a0 // indirect
	github.com/gogs/minwinsvc v0.0.0-20170301035411-95be6356811a // indirect
	github.com/golang-sql/civil v0.0.0-20220223132316-b832511892a9 // indirect
	github.com/golang-sql/sqlexp v0.1.0 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/gnostic-models v0.6.8 // indirect
	github.com/google/go-containerregistry v0.20.2 // indirect
	github.com/google/go-github v17.0.0+incompatible // indirect
	github.com/google/go-querystring v1.0.0 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/pprof v0.0.0-20240827171923-fa2c70bbbfe5 // indirect
	github.com/google/s2a-go v0.1.8 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.3 // indirect
	github.com/googleapis/gax-go/v2 v2.13.0 // indirect
	github.com/gorilla/css v1.0.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.22.0 // indirect
	github.com/hashicorp/errwrap v1.0.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/golang-lru v1.0.2 // indirect
	github.com/imdario/mergo v0.3.16 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/invopop/yaml v0.3.1 // indirect
	github.com/issue9/identicon v1.2.1 // indirect
	github.com/itchyny/gojq v0.12.11 // indirect
	github.com/itchyny/timefmt-go v0.1.5 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20221227161230-091c0ba34f0a // indirect
	github.com/jackc/pgx/v5 v5.3.0 // indirect
	github.com/jaytaylor/html2text v0.0.0-20190408195923-01ec452cbe43 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/jmespath/go-jmespath v0.4.1-0.20220621161143-b0104c826a24 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/klauspost/compress v1.17.9 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.16 // indirect
	github.com/mattn/go-runewidth v0.0.14 // indirect
	github.com/mattn/go-sqlite3 v2.0.3+incompatible // indirect
	github.com/mcuadros/go-version v0.0.0-20190830083331-035f6764e8d2 // indirect
	github.com/microcosm-cc/bluemonday v1.0.22 // indirect
	github.com/microsoft/go-mssqldb v0.17.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/mohae/deepcopy v0.0.0-20170929034955-c48cc78d4826 // indirect
	github.com/msteinert/pam v0.0.0-20190215180659-f29b9f28d6f9 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/nfnt/resize v0.0.0-20180221191011-83c6a9932646 // indirect
	github.com/niklasfasching/go-org v1.6.5 // indirect
	github.com/olekukonko/tablewriter v0.0.5 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/perimeterx/marshmallow v1.1.5 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/pquerna/otp v1.3.0 // indirect
	github.com/prometheus/client_golang v1.20.2 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/prometheus/common v0.57.0 // indirect
	github.com/prometheus/procfs v0.15.1 // indirect
	github.com/prometheus/statsd_exporter v0.27.1 // indirect
	github.com/rivo/uniseg v0.2.0 // indirect
	github.com/russross/blackfriday v1.6.0 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/satori/go.uuid v1.2.0 // indirect
	github.com/sergi/go-diff v1.3.1 // indirect
	github.com/sourcegraph/run v0.12.0 // indirect
	github.com/ssor/bom v0.0.0-20170718123548-6386211fdfcf // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/unknwon/cae v1.0.2 // indirect
	github.com/unknwon/com v1.0.1 // indirect
	github.com/unknwon/i18n v0.0.0-20190805065654-5c6446a380b6 // indirect
	github.com/unknwon/paginater v0.0.0-20170405233947-45e5d631308e // indirect
	github.com/urfave/cli v1.22.12 // indirect
	go.bobheadxi.dev/streamline v1.2.1 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.54.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.54.0 // indirect
	go.opentelemetry.io/otel v1.29.0 // indirect
	go.opentelemetry.io/otel/metric v1.29.0 // indirect
	go.opentelemetry.io/otel/trace v1.29.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	gogs.io/gogs v0.13.0 // indirect
	golang.org/x/crypto v0.26.0 // indirect
	golang.org/x/exp v0.0.0-20240823005443-9b4947da3948 // indirect
	golang.org/x/mod v0.20.0 // indirect
	golang.org/x/net v0.28.0 // indirect
	golang.org/x/oauth2 v0.22.0 // indirect
	golang.org/x/sync v0.8.0 // indirect
	golang.org/x/sys v0.24.0 // indirect
	golang.org/x/term v0.23.0 // indirect
	golang.org/x/text v0.17.0 // indirect
	golang.org/x/time v0.6.0 // indirect
	golang.org/x/tools v0.24.0 // indirect
	google.golang.org/genproto v0.0.0-20240827150818-7e3bb234dfed // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20240827150818-7e3bb234dfed // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240827150818-7e3bb234dfed // indirect
	google.golang.org/grpc v1.66.0 // indirect
	gopkg.in/alexcesaro/quotedprintable.v3 v3.0.0-20150716171945-2caba252f4dc // indirect
	gopkg.in/bufio.v1 v1.0.0-20140618132640-567b2bfa514e // indirect
	gopkg.in/go-playground/assert.v1 v1.2.1 // indirect
	gopkg.in/gomail.v2 v2.0.0-20160411212932-81ebce5c23df // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
	gopkg.in/macaron.v1 v1.4.0 // indirect
	gopkg.in/redis.v2 v2.3.2 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	gorm.io/driver/mysql v1.4.7 // indirect
	gorm.io/driver/postgres v1.4.8 // indirect
	gorm.io/driver/sqlite v1.4.2 // indirect
	gorm.io/driver/sqlserver v1.4.1 // indirect
	gorm.io/gorm v1.24.5 // indirect
	k8s.io/apiextensions-apiserver v0.30.4 // indirect
	k8s.io/gengo/v2 v2.0.0-20240826214909-a7b603a56eb7 // indirect
	k8s.io/klog/v2 v2.130.1 // indirect
	sigs.k8s.io/json v0.0.0-20221116044647-bc3834ca7abd // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.4.1 // indirect
	unknwon.dev/clog/v2 v2.2.0 // indirect
	xorm.io/builder v0.3.6 // indirect
	xorm.io/core v0.7.2 // indirect
	xorm.io/xorm v0.8.0 // indirect
)
