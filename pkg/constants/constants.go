/*
Copyright 2021 The KServe Authors.

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

package constants

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"knative.dev/serving/pkg/apis/autoscaling"

	"knative.dev/pkg/network"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// KServe Constants
const (
	KServeName                       = "kserve"
	KServeAPIGroupName               = "serving.kserve.io"
	KnativeAutoscalingAPIGroupName   = "autoscaling.knative.dev"
	KnativeServingAPIGroupNamePrefix = "serving.knative"
	KnativeServingAPIGroupName       = KnativeServingAPIGroupNamePrefix + ".dev"
)

var KServeNamespace = getEnvOrDefault("POD_NAMESPACE", "kserve")

// InferenceService Constants
var (
	InferenceServiceName                  = "inferenceservice"
	InferenceServiceAPIName               = "inferenceservices"
	InferenceServicePodLabelKey           = KServeAPIGroupName + "/" + InferenceServiceName
	InferenceServiceGenerationPodLabelKey = "isvc.generation"
	InferenceServiceConfigMapName         = "inferenceservice-config"
)

// InferenceGraph Constants
const (
	RouterHeadersPropagateEnvVar = "PROPAGATE_HEADERS"
	InferenceGraphLabel          = "serving.kserve.io/inferencegraph"
	InferenceGraphAuthCRBName    = "kserve-inferencegraph-auth-verifiers"
	InferenceGraphFinalizerName  = "inferencegraph.finalizers"
)

// TrainedModel Constants
var (
	TrainedModelAllocated = KServeAPIGroupName + "/" + "trainedmodel-allocated"
)

// InferenceService MultiModel Constants
var (
	ModelConfigFileName = "models.json"
)

// Model agent Constants
const (
	AgentContainerName    = "agent"
	AgentConfigMapKeyName = "agent"
	AgentEnableFlag       = "--enable-puller"
	AgentConfigDirArgName = "--config-dir"
	AgentModelDirArgName  = "--model-dir"
)

// InferenceLogger Constants
const (
	LoggerCaBundleVolume  = "agent-ca-bundle"
	LoggerCaCertMountPath = "/etc/tls/logger"
)

// InferenceService Annotations
var (
	InferenceServiceGKEAcceleratorAnnotationKey = KServeAPIGroupName + "/gke-accelerator"
	DeploymentMode                              = KServeAPIGroupName + "/deploymentMode"
	EnableRoutingTagAnnotationKey               = KServeAPIGroupName + "/enable-tag-routing"
	AutoscalerClass                             = KServeAPIGroupName + "/autoscalerClass"
	AutoscalerMetrics                           = KServeAPIGroupName + "/metrics"
	TargetUtilizationPercentage                 = KServeAPIGroupName + "/targetUtilizationPercentage"
	InitialScaleAnnotationKey                   = KnativeAutoscalingAPIGroupName + "/initial-scale"
	MinScaleAnnotationKey                       = KnativeAutoscalingAPIGroupName + "/min-scale"
	MaxScaleAnnotationKey                       = KnativeAutoscalingAPIGroupName + "/max-scale"
	StopAnnotationKey                           = KServeAPIGroupName + "/stop"
	RollOutDurationAnnotationKey                = KnativeServingAPIGroupName + "/rollout-duration"
	KnativeOpenshiftEnablePassthroughKey        = "serving.knative.openshift.io/enablePassthrough"
	EnableMetricAggregation                     = KServeAPIGroupName + "/enable-metric-aggregation"
	SetPrometheusAnnotation                     = KServeAPIGroupName + "/enable-prometheus-scraping"
	KserveContainerPrometheusPortKey            = "prometheus.kserve.io/port"
	KServeContainerPrometheusPathKey            = "prometheus.kserve.io/path"
	PrometheusPortAnnotationKey                 = "prometheus.io/port"
	PrometheusPathAnnotationKey                 = "prometheus.io/path"
	StorageReadonlyAnnotationKey                = "storage.kserve.io/readonly"
	DefaultPrometheusPath                       = "/metrics"
	QueueProxyAggregatePrometheusMetricsPort    = 9088
	DefaultPodPrometheusPort                    = "9091"
)

// InferenceService Internal Annotations
var (
	InferenceServiceInternalAnnotationsPrefix        = "internal." + KServeAPIGroupName
	StorageInitializerSourceUriInternalAnnotationKey = InferenceServiceInternalAnnotationsPrefix + "/storage-initializer-sourceuri"
	StorageSpecAnnotationKey                         = InferenceServiceInternalAnnotationsPrefix + "/storage-spec"
	StorageSpecParamAnnotationKey                    = InferenceServiceInternalAnnotationsPrefix + "/storage-spec-param"
	StorageSpecKeyAnnotationKey                      = InferenceServiceInternalAnnotationsPrefix + "/storage-spec-key"
	LoggerInternalAnnotationKey                      = InferenceServiceInternalAnnotationsPrefix + "/logger"
	LoggerSinkUrlInternalAnnotationKey               = InferenceServiceInternalAnnotationsPrefix + "/logger-sink-url"
	LoggerModeInternalAnnotationKey                  = InferenceServiceInternalAnnotationsPrefix + "/logger-mode"
	LoggerMetadataHeadersInternalAnnotationKey       = InferenceServiceInternalAnnotationsPrefix + "/logger-metadata-headers"
	BatcherInternalAnnotationKey                     = InferenceServiceInternalAnnotationsPrefix + "/batcher"
	BatcherMaxBatchSizeInternalAnnotationKey         = InferenceServiceInternalAnnotationsPrefix + "/batcher-max-batchsize"
	BatcherMaxLatencyInternalAnnotationKey           = InferenceServiceInternalAnnotationsPrefix + "/batcher-max-latency"
	AgentShouldInjectAnnotationKey                   = InferenceServiceInternalAnnotationsPrefix + "/agent"
	AgentModelConfigVolumeNameAnnotationKey          = InferenceServiceInternalAnnotationsPrefix + "/configVolumeName"
	AgentModelConfigMountPathAnnotationKey           = InferenceServiceInternalAnnotationsPrefix + "/configMountPath"
	AgentModelDirAnnotationKey                       = InferenceServiceInternalAnnotationsPrefix + "/modelDir"
	PredictorHostAnnotationKey                       = InferenceServiceInternalAnnotationsPrefix + "/predictor-host"
	PredictorProtocolAnnotationKey                   = InferenceServiceInternalAnnotationsPrefix + "/predictor-protocol"
	LocalModelLabel                                  = InferenceServiceInternalAnnotationsPrefix + "/localmodel"
	LocalModelSourceUriAnnotationKey                 = InferenceServiceInternalAnnotationsPrefix + "/localmodel-sourceuri"
)

// kserve networking constants
const (
	NetworkVisibility              = "networking.kserve.io/visibility"
	ClusterLocalVisibility         = "cluster-local"
	ClusterLocalDomain             = "svc.cluster.local"
	IsvcNameHeader                 = "KServe-Isvc-Name"
	IsvcNamespaceHeader            = "KServe-Isvc-Namespace"
	ODHKserveRawAuth               = "security.opendatahub.io/enable-auth"
	ODHRouteEnabled                = "exposed"
	ServingCertSecretSuffix        = "-serving-cert"
	OpenshiftServingCertAnnotation = "service.beta.openshift.io/serving-cert-secret-name"
)

// StorageSpec Constants
var (
	DefaultStorageSpecSecret     = "storage-config"
	DefaultStorageSpecSecretPath = "/mnt/storage-secret" // #nosec G101
)

// Controller Constants
var (
	ControllerLabelName             = KServeName + "-controller-manager"
	DefaultIstioSidecarUID          = int64(1337)
	DefaultMinReplicas              = 1
	IstioInitContainerName          = "istio-init"
	IstioInterceptModeRedirect      = "REDIRECT"
	IstioInterceptionModeAnnotation = "sidecar.istio.io/interceptionMode"
	IstioSidecarUIDAnnotationKey    = KServeAPIGroupName + "/storage-initializer-uid"
	IstioSidecarStatusAnnotation    = "sidecar.istio.io/status"
)

type AutoscalerClassType string
type AutoscalerMetricsType string
type AutoScalerKPAMetricsType string

var (
	AutoScalerKPAMetricsRPS         AutoScalerKPAMetricsType = "rps"
	AutoScalerKPAMetricsConcurrency AutoScalerKPAMetricsType = "concurrency"
)

// Autoscaler Default Class
var (
	DefaultAutoscalerClass = AutoscalerClassHPA
)

// Autoscaler Class
var (
	AutoscalerClassHPA      AutoscalerClassType = "hpa"
	AutoscalerClassExternal AutoscalerClassType = "external"
)

// Autoscaler Metrics
var (
	AutoScalerMetricsCPU AutoscalerMetricsType = "cpu"
)

// Autoscaler Memory metrics
var (
	AutoScalerMetricsMemory AutoscalerMetricsType = "memory"
)

// Autoscaler Class Allowed List
var AutoscalerAllowedClassList = []AutoscalerClassType{
	AutoscalerClassHPA,
	AutoscalerClassExternal,
}

// Autoscaler Metrics Allowed List
var AutoscalerAllowedMetricsList = []AutoscalerMetricsType{
	AutoScalerMetricsCPU,
	AutoScalerMetricsMemory,
}

// Autoscaler KPA Metrics Allowed List
var AutoScalerKPAMetricsAllowedList = []AutoScalerKPAMetricsType{
	AutoScalerKPAMetricsConcurrency,
	AutoScalerKPAMetricsRPS,
}

// Autoscaler Default Metrics Value
var (
	DefaultCPUUtilization int32 = 80
)

// Webhook Constants
var (
	PodMutatorWebhookName              = KServeName + "-pod-mutator-webhook"
	ServingRuntimeValidatorWebhookName = KServeName + "-servingRuntime-validator-webhook"
)

// GPU Constants
const (
	NvidiaGPUResourceType = "nvidia.com/gpu"
	AmdGPUResourceType    = "amd.com/gpu"
	IntelGPUResourceType  = "intel.com/gpu"
	GaudiGPUResourceType  = "habana.ai/gaudi"
)

var (
	CustomGPUResourceTypesAnnotationKey = KServeAPIGroupName + "/gpu-resource-types"
)

var GPUResourceTypeList = []string{
	NvidiaGPUResourceType,
	AmdGPUResourceType,
	IntelGPUResourceType,
	GaudiGPUResourceType,
}

// InferenceService Environment Variables
const (
	CustomSpecStorageUriEnvVarKey                     = "STORAGE_URI"
	CustomSpecProtocolEnvVarKey                       = "PROTOCOL"
	CustomSpecMultiModelServerEnvVarKey               = "MULTI_MODEL_SERVER"
	KServeContainerPrometheusMetricsPortEnvVarKey     = "KSERVE_CONTAINER_PROMETHEUS_METRICS_PORT"
	KServeContainerPrometheusMetricsPathEnvVarKey     = "KSERVE_CONTAINER_PROMETHEUS_METRICS_PATH"
	QueueProxyAggregatePrometheusMetricsPortEnvVarKey = "AGGREGATE_PROMETHEUS_METRICS_PORT"
)

type InferenceServiceComponent string

type InferenceServiceVerb string

type InferenceServiceProtocol string

// Knative constants
const (
	AutoscalerKey               = "autoscaler"
	AutoscalerInitialScaleKey   = "initial-scale"
	AutoscalerAllowZeroScaleKey = "allow-zero-initial-scale"
	DefaultKnServingName        = "knative-serving"
	DefaultKnServingNamespace   = "knative-serving"
	KnativeLocalGateway         = "knative-serving/knative-local-gateway"
	KnativeIngressGateway       = "knative-serving/knative-ingress-gateway"
	VisibilityLabel             = "networking.knative.dev/visibility"
)

var (
	LocalGatewayHost = "knative-local-gateway.istio-system.svc." + network.GetClusterDomainName()
	IstioMeshGateway = "mesh"
)

const WorkerNodeSuffix = "worker"

// InferenceService Component enums
const (
	Predictor   InferenceServiceComponent = "predictor"
	Explainer   InferenceServiceComponent = "explainer"
	Transformer InferenceServiceComponent = "transformer"
)

// InferenceService verb enums
const (
	Predict InferenceServiceVerb = "predict"
	Explain InferenceServiceVerb = "explain"
)

// InferenceService protocol enums
const (
	ProtocolV1         InferenceServiceProtocol = "v1"
	ProtocolV2         InferenceServiceProtocol = "v2"
	ProtocolGRPCV1     InferenceServiceProtocol = "grpc-v1"
	ProtocolGRPCV2     InferenceServiceProtocol = "grpc-v2"
	ProtocolUnknown    InferenceServiceProtocol = ""
	ProtocolVersionENV                          = "PROTOCOL_VERSION"
)

// InferenceService Endpoint Ports
const (
	InferenceServiceDefaultHttpPort     = "8080"
	InferenceServiceDefaultAgentPortStr = "9081"
	InferenceServiceDefaultAgentPort    = 9081
	CommonDefaultHttpPort               = 80
	AggregateMetricsPortName            = "aggr-metric"
)

// Labels to put on kservice
const (
	KServiceComponentLabel = "component"
	KServiceModelLabel     = "model"
	KServiceEndpointLabel  = "endpoint"
	KServeWorkloadKind     = KServeAPIGroupName + "/kind"
)

// Labels for TrainedModel
const (
	ParentInferenceServiceLabel = "inferenceservice"
	InferenceServiceLabel       = "serving.kserve.io/inferenceservice"
)

// InferenceService default/canary constants
const (
	InferenceServiceDefault = "default"
	InferenceServiceCanary  = "canary"
)

// InferenceService model server args
const (
	ArgumentModelName      = "--model_name"
	ArgumentModelDir       = "--model_dir"
	ArgumentModelClassName = "--model_class_name"
	ArgumentPredictorHost  = "--predictor_host"
	ArgumentHttpPort       = "--http_port"
	ArgumentWorkers        = "--workers"
)

// InferenceService container names
const (
	InferenceServiceContainerName   = "kserve-container"
	StorageInitializerContainerName = "storage-initializer"

	// TransformerContainerName transformer container name in collocation
	TransformerContainerName = "transformer-container"

	// WorkerContainerName is for worker node container
	WorkerContainerName = "worker-container"
)

// DefaultModelLocalMountPath is where models will be mounted by the storage-initializer
const DefaultModelLocalMountPath = "/mnt/models"

// Default path to mount CA bundle configmap volume
const DefaultCaBundleVolumeMountPath = "/etc/ssl/custom-certs"

// Default name for CA bundle file
const DefaultCaBundleFileName = "cabundle.crt"

// Default CA bundle configmap name that will be created in the user namespace.
const DefaultGlobalCaBundleConfigMapName = "global-ca-bundle"

// Custom CA bundle configmap Environment Variables
const (
	CaBundleConfigMapNameEnvVarKey   = "CA_BUNDLE_CONFIGMAP_NAME"
	CaBundleVolumeMountPathEnvVarKey = "CA_BUNDLE_VOLUME_MOUNT_POINT"
)

// Multi-model InferenceService
const (
	ModelConfigVolumeName = "model-config"
	ModelDirVolumeName    = "model-dir"
	ModelConfigDir        = "/mnt/configs"
	ModelDir              = DefaultModelLocalMountPath
)

var (
	// ServiceAnnotationDisallowedList is a list of annotations that are not allowed to be propagated to Knative
	// revisions, which prevents the reconciliation loop to be triggered if the annotations is
	// configured here are used.
	ServiceAnnotationDisallowedList = []string{
		autoscaling.InitialScaleAnnotationKey,
		autoscaling.MinScaleAnnotationKey,
		autoscaling.MaxScaleAnnotationKey,
		StorageInitializerSourceUriInternalAnnotationKey,
		"kubectl.kubernetes.io/last-applied-configuration",
		"security.opendatahub.io/enable-auth",
	}
	// RevisionTemplateLabelDisallowedList is a list of labels that are not allowed to be propagated to Knative
	// revisions, which prevents the reconciliation loop to be triggered if the labels is configured here are used.
	RevisionTemplateLabelDisallowedList = []string{
		VisibilityLabel,
	}
)

// CheckResultType raw k8s deployment, resource exist check result
type CheckResultType int

const (
	CheckResultCreate  CheckResultType = 0
	CheckResultUpdate  CheckResultType = 1
	CheckResultExisted CheckResultType = 2
	CheckResultUnknown CheckResultType = 3
	CheckResultDelete  CheckResultType = 4
	CheckResultSkipped CheckResultType = 5
)

type DeploymentModeType string

const (
	Serverless          DeploymentModeType = "Serverless"
	RawDeployment       DeploymentModeType = "RawDeployment"
	ModelMeshDeployment DeploymentModeType = "ModelMesh"
)

const (
	DefaultNSKnativeServing = "knative-serving"
)

// built-in runtime servers
const (
	SKLearnServer = "kserve-sklearnserver"
	MLServer      = "kserve-mlserver"
	TFServing     = "kserve-tensorflow-serving"
	XGBServer     = "kserve-xgbserver"
	TorchServe    = "kserve-torchserve"
	TritonServer  = "kserve-tritonserver"
	PMMLServer    = "kserve-pmmlserver"
	LGBServer     = "kserve-lgbserver"
	PaddleServer  = "kserve-paddleserver"
)

const (
	ModelClassLabel = "modelClass"
	ServiceEnvelope = "serviceEnvelope"
)

// allowed model class implementation in mlserver
const (
	MLServerModelClassSKLearn  = "mlserver_sklearn.SKLearnModel"
	MLServerModelClassXGBoost  = "mlserver_xgboost.XGBoostModel"
	MLServerModelClassLightGBM = "mlserver_lightgbm.LightGBMModel"
	MLServerModelClassMLFlow   = "mlserver_mlflow.MLflowRuntime"
)

// torchserve service envelope label allowed values
const (
	ServiceEnvelopeKServe   = "kserve"
	ServiceEnvelopeKServeV2 = "kservev2"
)

// supported model type
const (
	SupportedModelSKLearn     = "sklearn"
	SupportedModelTensorflow  = "tensorflow"
	SupportedModelXGBoost     = "xgboost"
	SupportedModelPyTorch     = "pytorch"
	SupportedModelONNX        = "onnx"
	SupportedModelHuggingFace = "huggingface"
	SupportedModelPMML        = "pmml"
	SupportedModelLightGBM    = "lightgbm"
	SupportedModelPaddle      = "paddle"
	SupportedModelTriton      = "triton"
	SupportedModelMLFlow      = "mlflow"
)

// opendatahub rawDeployment Auth
const (
	OauthProxyPort                  = 8443
	OauthProxyResourceMemoryLimit   = "128Mi"
	OauthProxyResourceCPULimit      = "200m"
	OauthProxyResourceMemoryRequest = "64Mi"
	OauthProxyResourceCPURequest    = "100m"
	OauthProxyImage                 = "registry.redhat.io/openshift4/ose-oauth-proxy@sha256:8507daed246d4d367704f7d7193233724acf1072572e1226ca063c066b858ecf"
	DefaultServiceAccount           = "default"
)

type ProtocolVersion int

const (
	_ ProtocolVersion = iota
	V1
	V2
	GRPCV1
	GRPCV2
	Unknown
)

// revision label
const (
	RevisionLabel         = "serving.knative.dev/revision"
	RawDeploymentAppLabel = "app"
)

// container state reason
const (
	StateReasonRunning          = "Running"
	StateReasonCompleted        = "Completed"
	StateReasonError            = "Error"
	StateReasonCrashLoopBackOff = "CrashLoopBackOff"
)

// CRD Kinds
const (
	IstioVirtualServiceKind = "VirtualService"
	KnativeServiceKind      = "Service"
	ClusterLocalModelKind   = "ClusterLocalModel"
	KnativeServingKind      = "KnativeServing"
)

// Model Parallel Options
const (
	TensorParallelSizeEnvName   = "TENSOR_PARALLEL_SIZE"
	PipelineParallelSizeEnvName = "PIPELINE_PARALLEL_SIZE"
)

// Model Parallel Options Default value
const (
	DefaultTensorParallelSize   = "1"
	DefaultPipelineParallelSize = "2"
)

// Multi Node Labels
var (
	MultiNodeRoleLabelKey = "multinode/role"
	MultiNodeHead         = "head"
)

// OpenShift constants
const (
	OpenShiftServiceCaConfigMapName = "openshift-service-ca.crt"
)

type ResourceType string

const (
	InferenceServiceResource ResourceType = "InferenceService"
	InferenceGraphResource   ResourceType = "InferenceGraph"
)

// GetRawServiceLabel generate native service label
func GetRawServiceLabel(service string) string {
	return "isvc." + service
}

// GetRawWorkerServiceLabel generate native service label for worker
func GetRawWorkerServiceLabel(service string) string {
	return "isvc." + service + "-" + WorkerNodeSuffix
}

// GeHeadServiceName generate head service name
func GeHeadServiceName(service string, isvcGeneration string) string {
	isvcName := strings.TrimSuffix(service, "-predictor")
	return isvcName + "-" + MultiNodeHead + "-" + isvcGeneration
}

func (e InferenceServiceComponent) String() string {
	return string(e)
}

func (v InferenceServiceVerb) String() string {
	return string(v)
}

func getEnvOrDefault(key string, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// nolint: unused
func isEnvVarMatched(envVar, matchtedValue string) bool {
	return getEnvOrDefault(envVar, "") == matchtedValue
}

func InferenceServiceURL(scheme, name, namespace, domain string) string {
	return fmt.Sprintf("%s://%s.%s.%s%s", scheme, name, namespace, domain, InferenceServicePrefix(name))
}

func InferenceServiceHostName(name string, namespace string, domain string) string {
	return fmt.Sprintf("%s.%s.%s", name, namespace, domain)
}

func DefaultPredictorServiceName(name string) string {
	return name + "-" + string(Predictor) + "-" + InferenceServiceDefault
}

func PredictorServiceName(name string) string {
	return name + "-" + string(Predictor)
}

func PredictorWorkerServiceName(name string) string {
	return name + "-" + string(Predictor) + "-" + WorkerNodeSuffix
}

func CanaryPredictorServiceName(name string) string {
	return name + "-" + string(Predictor) + "-" + InferenceServiceCanary
}

func DefaultExplainerServiceName(name string) string {
	return name + "-" + string(Explainer) + "-" + InferenceServiceDefault
}

func ExplainerServiceName(name string) string {
	return name + "-" + string(Explainer)
}

func CanaryExplainerServiceName(name string) string {
	return name + "-" + string(Explainer) + "-" + InferenceServiceCanary
}

func DefaultTransformerServiceName(name string) string {
	return name + "-" + string(Transformer) + "-" + InferenceServiceDefault
}

func TransformerServiceName(name string) string {
	return name + "-" + string(Transformer)
}

func CanaryTransformerServiceName(name string) string {
	return name + "-" + string(Transformer) + "-" + InferenceServiceCanary
}

func DefaultServiceName(name string, component InferenceServiceComponent) string {
	return name + "-" + component.String() + "-" + InferenceServiceDefault
}

func CanaryServiceName(name string, component InferenceServiceComponent) string {
	return name + "-" + component.String() + "-" + InferenceServiceCanary
}

func ModelConfigName(inferenceserviceName string, shardId int) string {
	return fmt.Sprintf("modelconfig-%s-%d", inferenceserviceName, shardId)
}

func InferenceServicePrefix(name string) string {
	return fmt.Sprintf("/v1/models/%s", name)
}

func PredictPath(name string, protocol InferenceServiceProtocol) string {
	path := ""
	if protocol == ProtocolV1 {
		path = fmt.Sprintf("/v1/models/%s:predict", name)
	} else if protocol == ProtocolV2 {
		path = fmt.Sprintf("/v2/models/%s/infer", name)
	}
	return path
}

func ExplainPath(name string) string {
	return fmt.Sprintf("/v1/models/%s:explain", name)
}

func PredictPrefix() string {
	return "^/v1/models/[\\w-]+(:predict)?"
}

func ExplainPrefix() string {
	return "^/v1/models/[\\w-]+:explain$"
}

func PathBasedExplainPrefix() string {
	return "(/v1/models/[\\w-]+:explain)$"
}

func VirtualServiceHostname(name string, predictorHostName string) string {
	index := strings.Index(predictorHostName, ".")
	return name + predictorHostName[index:]
}

func PredictorURL(metadata metav1.ObjectMeta, isCanary bool) string {
	serviceName := DefaultPredictorServiceName(metadata.Name)
	if isCanary {
		serviceName = CanaryPredictorServiceName(metadata.Name)
	}
	return fmt.Sprintf("%s.%s", serviceName, metadata.Namespace)
}

func TransformerURL(metadata metav1.ObjectMeta, isCanary bool) string {
	serviceName := DefaultTransformerServiceName(metadata.Name)
	if isCanary {
		serviceName = CanaryTransformerServiceName(metadata.Name)
	}
	return fmt.Sprintf("%s.%s", serviceName, metadata.Namespace)
}

// Should only match 1..65535, but for simplicity it matches 0-99999.
const portMatch = `(?::\d{1,5})?`

// HostRegExp returns an ECMAScript regular expression to match either host or host:<any port>
// for clusterLocalHost, we will also match the prefixes.
func HostRegExp(host string) string {
	localDomainSuffix := "(?i).svc." + network.GetClusterDomainName()
	if !strings.HasSuffix(host, localDomainSuffix) {
		return exact(regexp.QuoteMeta(host) + portMatch)
	}
	prefix := regexp.QuoteMeta(strings.TrimSuffix(host, localDomainSuffix))
	clusterSuffix := regexp.QuoteMeta("(?i)." + network.GetClusterDomainName())
	svcSuffix := regexp.QuoteMeta("(?i).svc")
	return exact(prefix + optional(svcSuffix+optional(clusterSuffix)) + portMatch)
}

func exact(regexp string) string {
	return "^" + regexp + "$"
}

func optional(regexp string) string {
	return "(" + regexp + ")?"
}

func GetProtocolVersionInt(protocol InferenceServiceProtocol) ProtocolVersion {
	switch protocol {
	case ProtocolV1:
		return V1
	case ProtocolV2:
		return V2
	case ProtocolGRPCV1:
		return GRPCV1
	case ProtocolGRPCV2:
		return GRPCV2
	default:
		return Unknown
	}
}

func GetProtocolVersionString(protocol ProtocolVersion) InferenceServiceProtocol {
	switch protocol {
	case V1:
		return ProtocolV1
	case V2:
		return ProtocolV2
	case GRPCV1:
		return ProtocolGRPCV1
	case GRPCV2:
		return ProtocolGRPCV2
	default:
		return ProtocolUnknown
	}
}
