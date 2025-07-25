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

package inferenceservice

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"time"

	kedav1alpha1 "github.com/kedacore/keda/v2/apis/keda/v1alpha1"
	otelv1beta1 "github.com/open-telemetry/opentelemetry-operator/apis/v1beta1"
	apierr "k8s.io/apimachinery/pkg/api/errors"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	routev1 "github.com/openshift/api/route/v1"
	"google.golang.org/protobuf/proto"
	appsv1 "k8s.io/api/apps/v1"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kserve/kserve/pkg/constants"
	"github.com/kserve/kserve/pkg/controller/v1beta1/inferenceservice/components"
	v1beta1utils "github.com/kserve/kserve/pkg/controller/v1beta1/inferenceservice/utils"
	"github.com/kserve/kserve/pkg/utils"
)

var _ = Describe("v1beta1 inference service controller", func() {
	// Define utility constants for object names and testing timeouts/durations and intervals.
	const (
		timeout  = time.Second * 60
		interval = time.Millisecond * 250
		domain   = "example.com"
	)
	defaultResource := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("1"),
			corev1.ResourceMemory: resource.MustParse("2Gi"),
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("1"),
			corev1.ResourceMemory: resource.MustParse("2Gi"),
		},
	}
	kserveGateway := types.NamespacedName{Name: "kserve-ingress-gateway", Namespace: "kserve"}

	configs := map[string]string{
		"explainers": `{
				"alibi": {
					"image": "kserve/alibi-explainer",
					"defaultImageVersion": "latest"
				}
			}`,
		"ingress": `{
				"enableGatewayApi": true,
				"kserveIngressGateway": "kserve/kserve-ingress-gateway",
				"ingressGateway": "knative-serving/knative-ingress-gateway",
				"localGateway": "knative-serving/knative-local-gateway",
				"localGatewayService": "knative-local-gateway.istio-system.svc.cluster.local",
				"additionalIngressDomains": ["additional.example.com"]
			}`,
		"storageInitializer": `{
				"image" : "kserve/storage-initializer:latest",
				"memoryRequest": "100Mi",
				"memoryLimit": "1Gi",
				"cpuRequest": "100m",
				"cpuLimit": "1",
				"CaBundleConfigMapName": "",
				"caBundleVolumeMountPath": "/etc/ssl/custom-certs",
				"enableDirectPvcVolumeMount": false
			}`,
	}
	Context("When creating inference service with raw kube predictor", func() {
		It("Should have httproute/service/deployment/httproute created", func() {
			By("By creating a new InferenceService")
			// Create configmap
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.InferenceServiceConfigMapName,
					Namespace: constants.KServeNamespace,
				},
				Data: configs,
			}
			Expect(k8sClient.Create(context.TODO(), configMap)).NotTo(HaveOccurred())
			defer k8sClient.Delete(context.TODO(), configMap)
			// Create ServingRuntime
			servingRuntime := &v1alpha1.ServingRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "tf-serving-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.ServingRuntimeSpec{
					SupportedModelFormats: []v1alpha1.SupportedModelFormat{
						{
							Name:       "tensorflow",
							Version:    proto.String("1"),
							AutoSelect: proto.Bool(true),
						},
					},
					ServingRuntimePodSpec: v1alpha1.ServingRuntimePodSpec{
						Containers: []corev1.Container{
							{
								Name:    "kserve-container",
								Image:   "tensorflow/serving:1.14.0",
								Command: []string{"/usr/bin/tensorflow_model_server"},
								Args: []string{
									"--port=9000",
									"--rest_api_port=8080",
									"--model_base_path=/mnt/models",
									"--rest_api_timeout_in_ms=60000",
								},
								Resources: defaultResource,
							},
						},
					},
					Disabled: proto.Bool(false),
				},
			}
			k8sClient.Create(context.TODO(), servingRuntime)
			defer k8sClient.Delete(context.TODO(), servingRuntime)
			serviceName := "raw-foo"
			expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: "default"}}
			serviceKey := expectedRequest.NamespacedName
			storageUri := "s3://test/mnist/export"
			qty := resource.MustParse("10Gi")
			ctx := context.Background()
			isvc := &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: string(constants.AutoscalerClassHPA),
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
							MinReplicas:    ptr.To(int32(1)),
							MaxReplicas:    3,
							TimeoutSeconds: ptr.To(int64(30)),
							AutoScaling: &v1beta1.AutoScalingSpec{
								Metrics: []v1beta1.MetricsSpec{
									{
										Type: v1beta1.ResourceMetricSourceType,
										Resource: &v1beta1.ResourceMetricSource{
											Name: v1beta1.ResourceMetricMemory,
											Target: v1beta1.MetricTarget{
												Type:         v1beta1.AverageValueMetricType,
												AverageValue: &qty,
											},
										},
									},
								},
							},
						},
						Tensorflow: &v1beta1.TFServingSpec{
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI:     &storageUri,
								RuntimeVersion: proto.String("1.14.0"),
								Container: corev1.Container{
									Name:      constants.InferenceServiceContainerName,
									Resources: defaultResource,
								},
							},
						},
					},
				},
			}
			isvc.DefaultInferenceService(nil, nil, &v1beta1.SecurityConfig{AutoMountServiceAccountToken: false}, nil)
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			defer k8sClient.Delete(ctx, isvc)

			inferenceService := &v1beta1.InferenceService{}

			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, inferenceService)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			actualDeployment := &appsv1.Deployment{}
			predictorDeploymentKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorDeploymentKey, actualDeployment) }, timeout).
				Should(Succeed())
			var replicas int32 = 1
			var revisionHistory int32 = 10
			var progressDeadlineSeconds int32 = 600
			var gracePeriod int64 = 30
			expectedDeployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorDeploymentKey.Name,
					Namespace: predictorDeploymentKey.Namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "isvc." + predictorDeploymentKey.Name,
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Name:      predictorDeploymentKey.Name,
							Namespace: "default",
							Labels: map[string]string{
								"app":                                 "isvc." + predictorDeploymentKey.Name,
								constants.KServiceComponentLabel:      constants.Predictor.String(),
								constants.InferenceServicePodLabelKey: serviceName,
							},
							Annotations: map[string]string{
								constants.OpenshiftServingCertAnnotation:                   serviceName + "-predictor" + constants.ServingCertSecretSuffix,
								constants.StorageInitializerSourceUriInternalAnnotationKey: *isvc.Spec.Predictor.Model.StorageURI,
								constants.DeploymentMode:                                   string(constants.RawDeployment),
								constants.AutoscalerClass:                                  string(constants.AutoscalerClassHPA),
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Image: "tensorflow/serving:" +
										*isvc.Spec.Predictor.Model.RuntimeVersion,
									Name:    constants.InferenceServiceContainerName,
									Command: []string{v1beta1.TensorflowEntrypointCommand},
									Args: []string{
										"--port=" + v1beta1.TensorflowServingGRPCPort,
										"--rest_api_port=" + v1beta1.TensorflowServingRestPort,
										"--model_base_path=" + constants.DefaultModelLocalMountPath,
										"--rest_api_timeout_in_ms=60000",
									},
									Resources: defaultResource,
									ReadinessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											TCPSocket: &corev1.TCPSocketAction{
												Port: intstr.IntOrString{
													IntVal: 8080,
												},
											},
										},
										InitialDelaySeconds: 0,
										TimeoutSeconds:      1,
										PeriodSeconds:       10,
										SuccessThreshold:    1,
										FailureThreshold:    3,
									},
									TerminationMessagePath:   "/dev/termination-log",
									TerminationMessagePolicy: "File",
									ImagePullPolicy:          "IfNotPresent",
								},
							},
							SchedulerName:                 "default-scheduler",
							RestartPolicy:                 "Always",
							TerminationGracePeriodSeconds: &gracePeriod,
							DNSPolicy:                     "ClusterFirst",
							SecurityContext: &corev1.PodSecurityContext{
								SELinuxOptions:      nil,
								WindowsOptions:      nil,
								RunAsUser:           nil,
								RunAsGroup:          nil,
								RunAsNonRoot:        nil,
								SupplementalGroups:  nil,
								FSGroup:             nil,
								Sysctls:             nil,
								FSGroupChangePolicy: nil,
								SeccompProfile:      nil,
							},
							AutomountServiceAccountToken: proto.Bool(false),
						},
					},
					Strategy: appsv1.DeploymentStrategy{
						Type: "RollingUpdate",
						RollingUpdate: &appsv1.RollingUpdateDeployment{
							MaxUnavailable: &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
							MaxSurge:       &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
						},
					},
					RevisionHistoryLimit:    &revisionHistory,
					ProgressDeadlineSeconds: &progressDeadlineSeconds,
				},
			}
			Expect(actualDeployment.Spec).To(BeComparableTo(expectedDeployment.Spec))

			// check service
			actualService := &corev1.Service{}
			predictorServiceKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorServiceKey, actualService) }, timeout).
				Should(Succeed())

			expectedService := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorServiceKey.Name,
					Namespace: predictorServiceKey.Namespace,
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       constants.PredictorServiceName(serviceName),
							Protocol:   "TCP",
							Port:       80,
							TargetPort: intstr.IntOrString{Type: 0, IntVal: 8080, StrVal: ""},
						},
					},
					Type:            "ClusterIP",
					SessionAffinity: "None",
					Selector: map[string]string{
						"app": "isvc." + constants.PredictorServiceName(serviceName),
					},
				},
			}
			actualService.Spec.ClusterIP = ""
			actualService.Spec.ClusterIPs = nil
			actualService.Spec.IPFamilies = nil
			actualService.Spec.IPFamilyPolicy = nil
			actualService.Spec.InternalTrafficPolicy = nil
			Expect(actualService.Spec).To(BeComparableTo(expectedService.Spec))

			// check isvc status
			updatedDeployment := actualDeployment.DeepCopy()
			updatedDeployment.Status.Conditions = []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: corev1.ConditionTrue,
				},
			}
			Expect(k8sClient.Status().Update(context.TODO(), updatedDeployment)).NotTo(HaveOccurred())

			// check http route
			actualTopLevelHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualTopLevelHttpRoute)
			}, timeout).Should(Succeed())
			topLevelHost := fmt.Sprintf("%s-%s.%s", serviceKey.Name, serviceKey.Namespace, "example.com")
			expectedTopLevelHttpRoute := gatewayapiv1.HTTPRoute{
				Spec: gatewayapiv1.HTTPRouteSpec{
					Hostnames: []gatewayapiv1.Hostname{gatewayapiv1.Hostname(topLevelHost), gatewayapiv1.Hostname(fmt.Sprintf("%s-%s.additional.example.com", serviceKey.Name, serviceKey.Namespace))},
					Rules: []gatewayapiv1.HTTPRouteRule{
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.FallbackPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(predictorServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("30s")),
							},
						},
					},
					CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
						ParentRefs: []gatewayapiv1.ParentReference{
							{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
						},
					},
				},
			}
			Expect(actualTopLevelHttpRoute.Spec).To(BeComparableTo(expectedTopLevelHttpRoute.Spec))

			actualPredictorHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      predictorServiceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualPredictorHttpRoute)
			}, timeout).Should(Succeed())
			predictorHost := fmt.Sprintf("%s-%s.%s", predictorServiceKey.Name, serviceKey.Namespace, "example.com")
			expectedPredictorHttpRoute := gatewayapiv1.HTTPRoute{
				Spec: gatewayapiv1.HTTPRouteSpec{
					Hostnames: []gatewayapiv1.Hostname{gatewayapiv1.Hostname(predictorHost)},
					Rules: []gatewayapiv1.HTTPRouteRule{
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.FallbackPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(predictorServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("30s")),
							},
						},
					},
					CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
						ParentRefs: []gatewayapiv1.ParentReference{
							{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
						},
					},
				},
			}
			Expect(actualPredictorHttpRoute.Spec).To(BeComparableTo(expectedPredictorHttpRoute.Spec))

			// Mark the Ingress as accepted to make isvc ready
			httpRouteStatus := gatewayapiv1.HTTPRouteStatus{
				RouteStatus: gatewayapiv1.RouteStatus{
					Parents: []gatewayapiv1.RouteParentStatus{
						{
							ParentRef: gatewayapiv1.ParentReference{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
							ControllerName: "istio.io/gateway-controller",
							Conditions: []metav1.Condition{
								{
									Type:               string(gatewayapiv1.ListenerConditionAccepted),
									Status:             metav1.ConditionTrue,
									Reason:             "Accepted",
									Message:            "Route was valid",
									LastTransitionTime: metav1.Now(),
								},
							},
						},
					},
				},
			}
			actualPredictorHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(context.Background(), actualPredictorHttpRoute)).NotTo(HaveOccurred())
			actualTopLevelHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(context.Background(), actualTopLevelHttpRoute)).NotTo(HaveOccurred())

			// verify if InferenceService status is updated
			expectedIsvcStatus := v1beta1.InferenceServiceStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:   v1beta1.IngressReady,
							Status: "True",
						},
						{
							Type:   v1beta1.PredictorReady,
							Status: "True",
						},
						{
							Type:   apis.ConditionReady,
							Status: "True",
						},
						{
							Type:     v1beta1.Stopped,
							Status:   "False",
							Severity: apis.ConditionSeverityInfo,
						},
					},
				},
				URL: &apis.URL{
					Scheme: "http",
					Host:   "raw-foo-default.example.com",
				},
				Address: &duckv1.Addressable{
					URL: &apis.URL{
						Scheme: "http",
						Host:   fmt.Sprintf("%s-predictor.%s.svc.cluster.local", serviceKey.Name, serviceKey.Namespace),
					},
				},
				Components: map[v1beta1.ComponentType]v1beta1.ComponentStatusSpec{
					v1beta1.PredictorComponent: {
						LatestCreatedRevision: "",
						// mocking it, see pkg/apis/serving/v1beta1/inference_service_status.go#344
						// uncomment when it gets supported by ODH
						// URL: &apis.URL{
						// 	Scheme: "http",
						// 	Host:   "raw-foo-predictor-default.example.com",
						// },
					},
				},
				ModelStatus: v1beta1.ModelStatus{
					TransitionStatus:    "InProgress",
					ModelRevisionStates: &v1beta1.ModelRevisionStates{TargetModelState: "Pending"},
				},
				DeploymentMode: string(constants.RawDeployment),
			}
			Eventually(func() string {
				isvc := &v1beta1.InferenceService{}
				if err := k8sClient.Get(context.TODO(), serviceKey, isvc); err != nil {
					return err.Error()
				}
				return cmp.Diff(&expectedIsvcStatus, &isvc.Status, cmpopts.IgnoreTypes(apis.VolatileTime{}))
			}, timeout).Should(BeEmpty())

			// check HPA
			var minReplicas int32 = 1
			var maxReplicas int32 = 3
			var stabilizationWindowSeconds int32 = 0
			selectPolicy := autoscalingv2.MaxChangePolicySelect
			actualHPA := &autoscalingv2.HorizontalPodAutoscaler{}
			predictorHPAKey := types.NamespacedName{
				Name:      predictorServiceKey.Name,
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorHPAKey, actualHPA) }, timeout).
				Should(Succeed())
			expectedHPA := &autoscalingv2.HorizontalPodAutoscaler{
				Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
					ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       predictorServiceKey.Name,
					},
					MinReplicas: &minReplicas,
					MaxReplicas: maxReplicas,
					Metrics: []autoscalingv2.MetricSpec{
						{
							Type: autoscalingv2.ResourceMetricSourceType,
							Resource: &autoscalingv2.ResourceMetricSource{
								Name: corev1.ResourceMemory,
								Target: autoscalingv2.MetricTarget{
									Type:         autoscalingv2.AverageValueMetricType,
									AverageValue: &qty,
								},
							},
						},
					},
					Behavior: &autoscalingv2.HorizontalPodAutoscalerBehavior{
						ScaleUp: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: &stabilizationWindowSeconds,
							SelectPolicy:               &selectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Pods",
									Value:         4,
									PeriodSeconds: 15,
								},
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
						ScaleDown: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: nil,
							SelectPolicy:               &selectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
					},
				},
			}
			Expect(actualHPA.Spec).To(BeComparableTo(expectedHPA.Spec))
		})

		It("Should have httproute/service/deployment/hpa created with DeploymentStrategy", func() {
			By("By creating a new InferenceService with DeploymentStrategy in PredictorSpec")
			// Create configmap
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.InferenceServiceConfigMapName,
					Namespace: constants.KServeNamespace,
				},
				Data: configs,
			}
			Expect(k8sClient.Create(context.TODO(), configMap)).NotTo(HaveOccurred())
			defer k8sClient.Delete(context.TODO(), configMap)

			// Create ServingRuntime
			servingRuntime := &v1alpha1.ServingRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "tf-serving-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.ServingRuntimeSpec{
					SupportedModelFormats: []v1alpha1.SupportedModelFormat{
						{
							Name:       "tensorflow",
							Version:    proto.String("1"),
							AutoSelect: proto.Bool(true),
						},
					},
					ServingRuntimePodSpec: v1alpha1.ServingRuntimePodSpec{
						Containers: []corev1.Container{
							{
								Name:    "kserve-container",
								Image:   "tensorflow/serving:1.14.0",
								Command: []string{"/usr/bin/tensorflow_model_server"},
								Args: []string{
									"--port=9000",
									"--rest_api_port=8080",
									"--model_base_path=/mnt/models",
									"--rest_api_timeout_in_ms=60000",
								},
								Resources: defaultResource,
							},
						},
					},
					Disabled: proto.Bool(false),
				},
			}
			k8sClient.Create(context.TODO(), servingRuntime)
			defer k8sClient.Delete(context.TODO(), servingRuntime)
			serviceName := "raw-foo-customized"
			expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: "default"}}
			serviceKey := expectedRequest.NamespacedName
			storageUri := "s3://test/mnist/export"
			predictorDeploymentKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			var replicas int32 = 1
			var revisionHistory int32 = 10
			var progressDeadlineSeconds int32 = 600
			var gracePeriod int64 = 30
			ctx := context.Background()
			var cpuUtilization int32 = 75

			isvc := &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: string(constants.AutoscalerClassHPA),
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
							MinReplicas: ptr.To(int32(1)),
							MaxReplicas: 3,
							DeploymentStrategy: &appsv1.DeploymentStrategy{
								Type: appsv1.RecreateDeploymentStrategyType,
							},
							AutoScaling: &v1beta1.AutoScalingSpec{
								Metrics: []v1beta1.MetricsSpec{
									{
										Type: v1beta1.ResourceMetricSourceType,
										Resource: &v1beta1.ResourceMetricSource{
											Name: v1beta1.ResourceMetricCPU,
											Target: v1beta1.MetricTarget{
												Type:               v1beta1.UtilizationMetricType,
												AverageUtilization: &cpuUtilization,
											},
										},
									},
								},
							},
						},
						Tensorflow: &v1beta1.TFServingSpec{
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI:     &storageUri,
								RuntimeVersion: proto.String("1.14.0"),
								Container: corev1.Container{
									Name:      constants.InferenceServiceContainerName,
									Resources: defaultResource,
								},
							},
						},
					},
				},
			}
			isvc.DefaultInferenceService(nil, nil, &v1beta1.SecurityConfig{AutoMountServiceAccountToken: false}, nil)
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			defer k8sClient.Delete(ctx, isvc)

			inferenceService := &v1beta1.InferenceService{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, inferenceService)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			actualDeployment := &appsv1.Deployment{}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorDeploymentKey, actualDeployment) }, timeout).
				Should(Succeed())

			expectedDeployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorDeploymentKey.Name,
					Namespace: predictorDeploymentKey.Namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "isvc." + predictorDeploymentKey.Name,
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Name:      predictorDeploymentKey.Name,
							Namespace: "default",
							Labels: map[string]string{
								"app":                                 "isvc." + predictorDeploymentKey.Name,
								constants.KServiceComponentLabel:      constants.Predictor.String(),
								constants.InferenceServicePodLabelKey: serviceName,
							},
							Annotations: map[string]string{
								constants.OpenshiftServingCertAnnotation:                   serviceName + "-predictor" + constants.ServingCertSecretSuffix,
								constants.StorageInitializerSourceUriInternalAnnotationKey: *isvc.Spec.Predictor.Model.StorageURI,
								constants.DeploymentMode:                                   string(constants.RawDeployment),
								constants.AutoscalerClass:                                  string(constants.AutoscalerClassHPA),
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Image: "tensorflow/serving:" +
										*isvc.Spec.Predictor.Model.RuntimeVersion,
									Name:    constants.InferenceServiceContainerName,
									Command: []string{v1beta1.TensorflowEntrypointCommand},
									Args: []string{
										"--port=" + v1beta1.TensorflowServingGRPCPort,
										"--rest_api_port=" + v1beta1.TensorflowServingRestPort,
										"--model_base_path=" + constants.DefaultModelLocalMountPath,
										"--rest_api_timeout_in_ms=60000",
									},
									Resources: defaultResource,
									ReadinessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											TCPSocket: &corev1.TCPSocketAction{
												Port: intstr.IntOrString{
													IntVal: 8080,
												},
											},
										},
										InitialDelaySeconds: 0,
										TimeoutSeconds:      1,
										PeriodSeconds:       10,
										SuccessThreshold:    1,
										FailureThreshold:    3,
									},
									TerminationMessagePath:   "/dev/termination-log",
									TerminationMessagePolicy: "File",
									ImagePullPolicy:          "IfNotPresent",
								},
							},
							SchedulerName:                 "default-scheduler",
							RestartPolicy:                 "Always",
							TerminationGracePeriodSeconds: &gracePeriod,
							DNSPolicy:                     "ClusterFirst",
							SecurityContext: &corev1.PodSecurityContext{
								SELinuxOptions:      nil,
								WindowsOptions:      nil,
								RunAsUser:           nil,
								RunAsGroup:          nil,
								RunAsNonRoot:        nil,
								SupplementalGroups:  nil,
								FSGroup:             nil,
								Sysctls:             nil,
								FSGroupChangePolicy: nil,
								SeccompProfile:      nil,
							},
							AutomountServiceAccountToken: proto.Bool(false),
						},
					},
					// This is now customized and different from defaults set via `setDefaultDeploymentSpec`.
					Strategy: appsv1.DeploymentStrategy{
						Type: appsv1.RecreateDeploymentStrategyType,
					},
					RevisionHistoryLimit:    &revisionHistory,
					ProgressDeadlineSeconds: &progressDeadlineSeconds,
				},
			}
			Expect(actualDeployment.Spec).To(BeComparableTo(expectedDeployment.Spec))

			// check isvc status
			updatedDeployment := actualDeployment.DeepCopy()
			updatedDeployment.Status.Conditions = []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: corev1.ConditionTrue,
				},
			}
			Expect(k8sClient.Status().Update(context.TODO(), updatedDeployment)).NotTo(HaveOccurred())

			// check service
			actualService := &corev1.Service{}
			predictorServiceKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorServiceKey, actualService) }, timeout).
				Should(Succeed())

			expectedService := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorServiceKey.Name,
					Namespace: predictorServiceKey.Namespace,
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       constants.PredictorServiceName(serviceName),
							Protocol:   "TCP",
							Port:       80,
							TargetPort: intstr.IntOrString{Type: 0, IntVal: 8080, StrVal: ""},
						},
					},
					Type:            "ClusterIP",
					SessionAffinity: "None",
					Selector: map[string]string{
						"app": "isvc." + constants.PredictorServiceName(serviceName),
					},
				},
			}
			actualService.Spec.ClusterIP = ""
			actualService.Spec.ClusterIPs = nil
			actualService.Spec.IPFamilies = nil
			actualService.Spec.IPFamilyPolicy = nil
			actualService.Spec.InternalTrafficPolicy = nil
			Expect(actualService.Spec).To(BeComparableTo(expectedService.Spec))

			// check http route
			actualToplevelHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualToplevelHttpRoute)
			}, timeout).
				Should(Succeed())
			topLevelHost := fmt.Sprintf("%s-%s.%s", serviceKey.Name, serviceKey.Namespace, "example.com")
			expectedToplevelHttpRoute := gatewayapiv1.HTTPRoute{
				Spec: gatewayapiv1.HTTPRouteSpec{
					Hostnames: []gatewayapiv1.Hostname{gatewayapiv1.Hostname(topLevelHost), gatewayapiv1.Hostname(fmt.Sprintf("%s-%s.additional.example.com", serviceKey.Name, serviceKey.Namespace))},
					Rules: []gatewayapiv1.HTTPRouteRule{
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.FallbackPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(predictorServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("60s")),
							},
						},
					},
					CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
						ParentRefs: []gatewayapiv1.ParentReference{
							{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
						},
					},
				},
			}
			Expect(actualToplevelHttpRoute.Spec).To(BeComparableTo(expectedToplevelHttpRoute.Spec))

			actualPredictorHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      predictorServiceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualPredictorHttpRoute)
			}, timeout).
				Should(Succeed())
			predictorHost := fmt.Sprintf("%s-%s.%s", predictorServiceKey.Name, serviceKey.Namespace, "example.com")
			expectedPredictorHttpRoute := gatewayapiv1.HTTPRoute{
				Spec: gatewayapiv1.HTTPRouteSpec{
					Hostnames: []gatewayapiv1.Hostname{gatewayapiv1.Hostname(predictorHost)},
					Rules: []gatewayapiv1.HTTPRouteRule{
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.FallbackPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(predictorServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("60s")),
							},
						},
					},
					CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
						ParentRefs: []gatewayapiv1.ParentReference{
							{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
						},
					},
				},
			}
			Expect(actualPredictorHttpRoute.Spec).To(BeComparableTo(expectedPredictorHttpRoute.Spec))

			// Mark the Ingress as accepted to make isvc ready
			httpRouteStatus := gatewayapiv1.HTTPRouteStatus{
				RouteStatus: gatewayapiv1.RouteStatus{
					Parents: []gatewayapiv1.RouteParentStatus{
						{
							ParentRef: gatewayapiv1.ParentReference{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
							ControllerName: "istio.io/gateway-controller",
							Conditions: []metav1.Condition{
								{
									Type:               string(gatewayapiv1.ListenerConditionAccepted),
									Status:             metav1.ConditionTrue,
									Reason:             "Accepted",
									Message:            "Route was valid",
									LastTransitionTime: metav1.Now(),
								},
							},
						},
					},
				},
			}
			actualPredictorHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(context.Background(), actualPredictorHttpRoute)).NotTo(HaveOccurred())
			actualToplevelHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(context.Background(), actualToplevelHttpRoute)).NotTo(HaveOccurred())

			// verify if InferenceService status is updated
			expectedIsvcStatus := v1beta1.InferenceServiceStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:   v1beta1.IngressReady,
							Status: "True",
						},
						{
							Type:   v1beta1.PredictorReady,
							Status: "True",
						},
						{
							Type:   apis.ConditionReady,
							Status: "True",
						},
						{
							Type:     v1beta1.Stopped,
							Status:   "False",
							Severity: apis.ConditionSeverityInfo,
						},
					},
				},
				URL: &apis.URL{
					Scheme: "http",
					Host:   "raw-foo-customized-default.example.com",
				},
				Address: &duckv1.Addressable{
					URL: &apis.URL{
						Scheme: "http",
						Host:   fmt.Sprintf("%s-predictor.%s.svc.cluster.local", serviceKey.Name, serviceKey.Namespace),
					},
				},
				Components: map[v1beta1.ComponentType]v1beta1.ComponentStatusSpec{
					v1beta1.PredictorComponent: {
						LatestCreatedRevision: "",
						// mocking it, see pkg/apis/serving/v1beta1/inference_service_status.go#344
						// uncomment when it gets supported by ODH
						// URL: &apis.URL{
						// 	Scheme: "http",
						// 	Host:   "raw-foo-customized-predictor-default.example.com",
						// },
					},
				},
				ModelStatus: v1beta1.ModelStatus{
					TransitionStatus:    "InProgress",
					ModelRevisionStates: &v1beta1.ModelRevisionStates{TargetModelState: "Pending"},
				},
				DeploymentMode: string(constants.RawDeployment),
			}
			// Check that the ISVC was updated
			actualIsvc := &v1beta1.InferenceService{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, actualIsvc)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			// Check that the inference service is ready
			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, actualIsvc)
				if err != nil {
					return false
				}
				return actualIsvc.Status.IsConditionReady(v1beta1.PredictorReady)
			}, timeout, interval).Should(BeTrue(), "The predictor should be ready")

			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, actualIsvc)
				if err != nil {
					return false
				}
				return actualIsvc.Status.IsConditionReady(v1beta1.IngressReady)
			}, timeout, interval).Should(BeTrue(), "The ingress should be ready")
			diff := cmp.Diff(&expectedIsvcStatus, &actualIsvc.Status, cmpopts.IgnoreTypes(apis.VolatileTime{}))
			Expect(diff).To(BeEmpty())

			// check HPA
			var minReplicas int32 = 1
			var maxReplicas int32 = 3
			var stabilizationWindowSeconds int32 = 0
			selectPolicy := autoscalingv2.MaxChangePolicySelect
			actualHPA := &autoscalingv2.HorizontalPodAutoscaler{}
			predictorHPAKey := types.NamespacedName{
				Name:      predictorServiceKey.Name,
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorHPAKey, actualHPA) }, timeout).
				Should(Succeed())
			expectedHPA := &autoscalingv2.HorizontalPodAutoscaler{
				Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
					ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       predictorServiceKey.Name,
					},
					MinReplicas: &minReplicas,
					MaxReplicas: maxReplicas,
					Metrics: []autoscalingv2.MetricSpec{
						{
							Type: autoscalingv2.ResourceMetricSourceType,
							Resource: &autoscalingv2.ResourceMetricSource{
								Name: corev1.ResourceCPU,
								Target: autoscalingv2.MetricTarget{
									Type:               autoscalingv2.UtilizationMetricType,
									AverageUtilization: &cpuUtilization,
								},
							},
						},
					},
					Behavior: &autoscalingv2.HorizontalPodAutoscalerBehavior{
						ScaleUp: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: &stabilizationWindowSeconds,
							SelectPolicy:               &selectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Pods",
									Value:         4,
									PeriodSeconds: 15,
								},
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
						ScaleDown: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: nil,
							SelectPolicy:               &selectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
					},
				},
			}
			Expect(actualHPA.Spec).To(BeComparableTo(expectedHPA.Spec))
		})
		It("Should have httproute/service/deployment created", func() {
			By("By creating a new InferenceService with AutoscalerClass None")
			// Create configmap
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.InferenceServiceConfigMapName,
					Namespace: constants.KServeNamespace,
				},
				Data: configs,
			}
			Expect(k8sClient.Create(context.TODO(), configMap)).NotTo(HaveOccurred())
			defer k8sClient.Delete(context.TODO(), configMap)
			// Create ServingRuntime
			servingRuntime := &v1alpha1.ServingRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "tf-serving-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.ServingRuntimeSpec{
					SupportedModelFormats: []v1alpha1.SupportedModelFormat{
						{
							Name:       "tensorflow",
							Version:    proto.String("1"),
							AutoSelect: proto.Bool(true),
						},
					},
					ServingRuntimePodSpec: v1alpha1.ServingRuntimePodSpec{
						Containers: []corev1.Container{
							{
								Name:    "kserve-container",
								Image:   "tensorflow/serving:1.14.0",
								Command: []string{"/usr/bin/tensorflow_model_server"},
								Args: []string{
									"--port=9000",
									"--rest_api_port=8080",
									"--model_base_path=/mnt/models",
									"--rest_api_timeout_in_ms=60000",
								},
								Resources: defaultResource,
							},
						},
					},
					Disabled: proto.Bool(false),
				},
			}
			k8sClient.Create(context.TODO(), servingRuntime)
			defer k8sClient.Delete(context.TODO(), servingRuntime)
			serviceName := "raw-foo-2"
			expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: "default"}}
			serviceKey := expectedRequest.NamespacedName
			storageUri := "s3://test/mnist/export"
			ctx := context.Background()
			isvc := &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: string(constants.AutoscalerClassNone),
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						Tensorflow: &v1beta1.TFServingSpec{
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI:     &storageUri,
								RuntimeVersion: proto.String("1.14.0"),
								Container: corev1.Container{
									Name:      constants.InferenceServiceContainerName,
									Resources: defaultResource,
								},
							},
						},
					},
				},
			}
			isvc.DefaultInferenceService(nil, nil, &v1beta1.SecurityConfig{AutoMountServiceAccountToken: false}, nil)
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			defer k8sClient.Delete(ctx, isvc)

			inferenceService := &v1beta1.InferenceService{}

			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, inferenceService)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			actualDeployment := &appsv1.Deployment{}
			predictorDeploymentKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorDeploymentKey, actualDeployment) }, timeout).
				Should(Succeed())
			var replicas int32 = 1
			var revisionHistory int32 = 10
			var progressDeadlineSeconds int32 = 600
			var gracePeriod int64 = 30
			expectedDeployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorDeploymentKey.Name,
					Namespace: predictorDeploymentKey.Namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "isvc." + predictorDeploymentKey.Name,
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Name:      predictorDeploymentKey.Name,
							Namespace: "default",
							Labels: map[string]string{
								"app":                                 "isvc." + predictorDeploymentKey.Name,
								constants.KServiceComponentLabel:      constants.Predictor.String(),
								constants.InferenceServicePodLabelKey: serviceName,
							},
							Annotations: map[string]string{
								constants.OpenshiftServingCertAnnotation:                   serviceName + "-predictor" + constants.ServingCertSecretSuffix,
								constants.StorageInitializerSourceUriInternalAnnotationKey: *isvc.Spec.Predictor.Model.StorageURI,
								constants.DeploymentMode:                                   string(constants.RawDeployment),
								constants.AutoscalerClass:                                  string(constants.AutoscalerClassNone),
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Image: "tensorflow/serving:" +
										*isvc.Spec.Predictor.Model.RuntimeVersion,
									Name:    constants.InferenceServiceContainerName,
									Command: []string{v1beta1.TensorflowEntrypointCommand},
									Args: []string{
										"--port=" + v1beta1.TensorflowServingGRPCPort,
										"--rest_api_port=" + v1beta1.TensorflowServingRestPort,
										"--model_base_path=" + constants.DefaultModelLocalMountPath,
										"--rest_api_timeout_in_ms=60000",
									},
									Resources: defaultResource,
									ReadinessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											TCPSocket: &corev1.TCPSocketAction{
												Port: intstr.IntOrString{
													IntVal: 8080,
												},
											},
										},
										InitialDelaySeconds: 0,
										TimeoutSeconds:      1,
										PeriodSeconds:       10,
										SuccessThreshold:    1,
										FailureThreshold:    3,
									},
									TerminationMessagePath:   "/dev/termination-log",
									TerminationMessagePolicy: "File",
									ImagePullPolicy:          "IfNotPresent",
								},
							},
							SchedulerName:                 "default-scheduler",
							RestartPolicy:                 "Always",
							TerminationGracePeriodSeconds: &gracePeriod,
							DNSPolicy:                     "ClusterFirst",
							SecurityContext: &corev1.PodSecurityContext{
								SELinuxOptions:      nil,
								WindowsOptions:      nil,
								RunAsUser:           nil,
								RunAsGroup:          nil,
								RunAsNonRoot:        nil,
								SupplementalGroups:  nil,
								FSGroup:             nil,
								Sysctls:             nil,
								FSGroupChangePolicy: nil,
								SeccompProfile:      nil,
							},
							AutomountServiceAccountToken: proto.Bool(false),
						},
					},
					Strategy: appsv1.DeploymentStrategy{
						Type: "RollingUpdate",
						RollingUpdate: &appsv1.RollingUpdateDeployment{
							MaxUnavailable: &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
							MaxSurge:       &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
						},
					},
					RevisionHistoryLimit:    &revisionHistory,
					ProgressDeadlineSeconds: &progressDeadlineSeconds,
				},
			}
			Expect(actualDeployment.Spec).To(BeComparableTo(expectedDeployment.Spec))

			// check service
			actualService := &corev1.Service{}
			predictorServiceKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorServiceKey, actualService) }, timeout).
				Should(Succeed())

			expectedService := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorServiceKey.Name,
					Namespace: predictorServiceKey.Namespace,
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       "raw-foo-2-predictor",
							Protocol:   "TCP",
							Port:       80,
							TargetPort: intstr.IntOrString{Type: 0, IntVal: 8080, StrVal: ""},
						},
					},
					Type:            "ClusterIP",
					SessionAffinity: "None",
					Selector: map[string]string{
						"app": "isvc.raw-foo-2-predictor",
					},
				},
			}
			actualService.Spec.ClusterIP = ""
			actualService.Spec.ClusterIPs = nil
			actualService.Spec.IPFamilies = nil
			actualService.Spec.IPFamilyPolicy = nil
			actualService.Spec.InternalTrafficPolicy = nil
			Expect(actualService.Spec).To(BeComparableTo(expectedService.Spec))

			// check isvc status
			updatedDeployment := actualDeployment.DeepCopy()
			updatedDeployment.Status.Conditions = []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: corev1.ConditionTrue,
				},
			}
			Expect(k8sClient.Status().Update(context.TODO(), updatedDeployment)).NotTo(HaveOccurred())

			// check http Route
			actualToplevelHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualToplevelHttpRoute)
			}, timeout).
				Should(Succeed())
			topLevelHost := fmt.Sprintf("%s-%s.%s", serviceKey.Name, serviceKey.Namespace, "example.com")
			expectedToplevelHttpRoute := gatewayapiv1.HTTPRoute{
				Spec: gatewayapiv1.HTTPRouteSpec{
					Hostnames: []gatewayapiv1.Hostname{gatewayapiv1.Hostname(topLevelHost), gatewayapiv1.Hostname(fmt.Sprintf("%s-%s.additional.example.com", serviceKey.Name, serviceKey.Namespace))},
					Rules: []gatewayapiv1.HTTPRouteRule{
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.FallbackPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(predictorServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("60s")),
							},
						},
					},
					CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
						ParentRefs: []gatewayapiv1.ParentReference{
							{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
						},
					},
				},
			}
			Expect(actualToplevelHttpRoute.Spec).To(BeComparableTo(expectedToplevelHttpRoute.Spec))

			actualPredictorHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      predictorServiceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualPredictorHttpRoute)
			}, timeout).
				Should(Succeed())
			predictorHost := fmt.Sprintf("%s-%s.%s", predictorServiceKey.Name, serviceKey.Namespace, "example.com")
			expectedPredictorHttpRoute := gatewayapiv1.HTTPRoute{
				Spec: gatewayapiv1.HTTPRouteSpec{
					Hostnames: []gatewayapiv1.Hostname{gatewayapiv1.Hostname(predictorHost)},
					Rules: []gatewayapiv1.HTTPRouteRule{
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.FallbackPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(predictorServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("60s")),
							},
						},
					},
					CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
						ParentRefs: []gatewayapiv1.ParentReference{
							{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
						},
					},
				},
			}
			Expect(actualPredictorHttpRoute.Spec).To(BeComparableTo(expectedPredictorHttpRoute.Spec))

			// Mark the Ingress as accepted to make isvc ready
			httpRouteStatus := gatewayapiv1.HTTPRouteStatus{
				RouteStatus: gatewayapiv1.RouteStatus{
					Parents: []gatewayapiv1.RouteParentStatus{
						{
							ParentRef: gatewayapiv1.ParentReference{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
							ControllerName: "istio.io/gateway-controller",
							Conditions: []metav1.Condition{
								{
									Type:               string(gatewayapiv1.ListenerConditionAccepted),
									Status:             metav1.ConditionTrue,
									Reason:             "Accepted",
									Message:            "Route was valid",
									LastTransitionTime: metav1.Now(),
								},
							},
						},
					},
				},
			}
			actualPredictorHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(context.Background(), actualPredictorHttpRoute)).NotTo(HaveOccurred())
			actualToplevelHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(context.Background(), actualToplevelHttpRoute)).NotTo(HaveOccurred())

			// verify if InferenceService status is updated
			expectedIsvcStatus := v1beta1.InferenceServiceStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:   v1beta1.IngressReady,
							Status: "True",
						},
						{
							Type:   v1beta1.PredictorReady,
							Status: "True",
						},
						{
							Type:   apis.ConditionReady,
							Status: "True",
						},
						{
							Type:     v1beta1.Stopped,
							Status:   "False",
							Severity: apis.ConditionSeverityInfo,
						},
					},
				},
				URL: &apis.URL{
					Scheme: "http",
					Host:   "raw-foo-2-default.example.com",
				},
				Address: &duckv1.Addressable{
					URL: &apis.URL{
						Scheme: "http",
						Host:   fmt.Sprintf("%s-predictor.%s.svc.cluster.local", serviceKey.Name, serviceKey.Namespace),
					},
				},
				Components: map[v1beta1.ComponentType]v1beta1.ComponentStatusSpec{
					v1beta1.PredictorComponent: {
						LatestCreatedRevision: "",
						// mocking it, see pkg/apis/serving/v1beta1/inference_service_status.go#344
						// uncomment when it gets supported by ODH
						// URL: &apis.URL{
						// 	Scheme: "http",
						// 	Host:   "raw-foo-2-predictor-default.example.com",
						// },
					},
				},
				ModelStatus: v1beta1.ModelStatus{
					TransitionStatus:    "InProgress",
					ModelRevisionStates: &v1beta1.ModelRevisionStates{TargetModelState: "Pending"},
				},
				DeploymentMode: string(constants.RawDeployment),
			}
			Eventually(func() string {
				isvc := &v1beta1.InferenceService{}
				if err := k8sClient.Get(context.TODO(), serviceKey, isvc); err != nil {
					return err.Error()
				}
				return cmp.Diff(&expectedIsvcStatus, &isvc.Status, cmpopts.IgnoreTypes(apis.VolatileTime{}), cmpopts.IgnoreFields(v1beta1.InferenceServiceStatus{}, "DeploymentMode"))
			}, timeout).Should(BeEmpty())

			// check HPA is not created
			actualHPA := &autoscalingv2.HorizontalPodAutoscaler{}
			predictorHPAKey := types.NamespacedName{
				Name:      constants.DefaultPredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorHPAKey, actualHPA) }, timeout).
				Should(HaveOccurred())

			// Replica should not be nil and it should be set to minReplicas if it was set.
			updated_isvc := &v1beta1.InferenceService{}

			Eventually(func() error {
				return k8sClient.Get(ctx, serviceKey, updated_isvc)
			}, timeout, interval).Should(Succeed())
			if updated_isvc.Labels == nil {
				updated_isvc.Labels = make(map[string]string)
			}
			updated_isvc.Spec.Predictor.ComponentExtensionSpec = v1beta1.ComponentExtensionSpec{
				MinReplicas: ptr.To(int32(2)),
			}
			Expect(k8sClient.Update(context.TODO(), updated_isvc)).NotTo(HaveOccurred())

			updatedDeployment_isvc_updated := &appsv1.Deployment{}
			Eventually(func() bool {
				if err := k8sClient.Get(context.TODO(), predictorDeploymentKey, updatedDeployment_isvc_updated); err == nil {
					return updatedDeployment_isvc_updated.Spec.Replicas != nil && *updatedDeployment_isvc_updated.Spec.Replicas == 2
				} else {
					return false
				}
			}, timeout, interval).Should(BeTrue())
		})
	})
	Context("When updating ISVC envs", func() {
		It("Should reconcile the deployment if isvc envs are updated", func() {
			defaultEnvs := []corev1.EnvVar{
				{
					Name:  "env1",
					Value: "val1",
				},
				{
					Name:  "env2",
					Value: "val2",
				},
				{
					Name:  "env3",
					Value: "val3",
				},
			}

			// Create configmap
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.InferenceServiceConfigMapName,
					Namespace: constants.KServeNamespace,
				},
				Data: configs,
			}
			Expect(k8sClient.Create(context.TODO(), configMap)).NotTo(HaveOccurred())
			defer k8sClient.Delete(context.TODO(), configMap)
			// Create ServingRuntime
			servingRuntime := &v1alpha1.ServingRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "tf-serving-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.ServingRuntimeSpec{
					SupportedModelFormats: []v1alpha1.SupportedModelFormat{
						{
							Name:       "tensorflow",
							Version:    proto.String("1"),
							AutoSelect: proto.Bool(true),
						},
					},
					ServingRuntimePodSpec: v1alpha1.ServingRuntimePodSpec{
						Containers: []corev1.Container{
							{
								Name:    "kserve-container",
								Image:   "tensorflow/serving:1.14.0",
								Command: []string{"/usr/bin/tensorflow_model_server"},
								Args: []string{
									"--port=9000",
									"--rest_api_port=8080",
									"--model_base_path=/mnt/models",
									"--rest_api_timeout_in_ms=60000",
								},
								Resources: defaultResource,
							},
						},
					},
					Disabled: proto.Bool(false),
				},
			}
			k8sClient.Create(context.TODO(), servingRuntime)
			defer k8sClient.Delete(context.TODO(), servingRuntime)
			serviceName := "raw-test-env"
			expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: "default"}}
			serviceKey := expectedRequest.NamespacedName
			// create isvc
			storageUri := "s3://test/mnist/export"
			isvcOriginal := &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
					Annotations: map[string]string{
						"serving.kserve.io/deploymentMode":              "RawDeployment",
						"serving.kserve.io/autoscalerClass":             "hpa",
						"serving.kserve.io/metrics":                     "cpu",
						"serving.kserve.io/targetUtilizationPercentage": "75",
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
							MinReplicas: ptr.To(int32(1)),
							MaxReplicas: 3,
						},
						Tensorflow: &v1beta1.TFServingSpec{
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI:     &storageUri,
								RuntimeVersion: proto.String("1.14.0"),
								Container: corev1.Container{
									Name:      constants.InferenceServiceContainerName,
									Resources: defaultResource,
									Env:       defaultEnvs,
								},
							},
						},
					},
				},
			}

			isvcOriginal.DefaultInferenceService(nil, nil, &v1beta1.SecurityConfig{AutoMountServiceAccountToken: false}, nil)
			Expect(k8sClient.Create(context.TODO(), isvcOriginal)).Should(Succeed())

			inferenceService := &v1beta1.InferenceService{}
			Eventually(func() bool {
				err := k8sClient.Get(context.TODO(), serviceKey, inferenceService)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			deployed1 := &appsv1.Deployment{}
			predictorDeploymentKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), predictorDeploymentKey, deployed1)
			}, timeout, interval).Should(Succeed())
			Expect(deployed1.Spec.Template.Spec.Containers[0].Env).To(ContainElements(defaultEnvs))

			// Now, update the isvc with new env
			newEnvs := []corev1.EnvVar{
				{
					Name:  "newEnv1",
					Value: "newValue1",
				},
				{
					Name:  "newEnv2",
					Value: "delete",
				},
			}

			// Update the isvc to add new envs
			fmt.Fprintln(GinkgoWriter, "### Adding new envs")
			isvcUpdated1 := &v1beta1.InferenceService{}
			Eventually(func() bool {
				// get the latest deployed version
				if err := k8sClient.Get(context.TODO(), serviceKey, inferenceService); err != nil {
					return false
				}

				isvcUpdated1 = inferenceService.DeepCopy()
				isvcUpdated1.Spec.Predictor.Model.Env = append(isvcUpdated1.Spec.Predictor.Model.Env, newEnvs...)
				if err1 := k8sClient.Update(context.TODO(), isvcUpdated1); err1 != nil {
					return false
				}
				return true
			}, timeout, interval).Should(BeTrue())

			// The deployment should be reconciled
			deployed2 := &appsv1.Deployment{}
			appendedEnvs := append(defaultEnvs, newEnvs...)
			Eventually(func() []corev1.EnvVar {
				_ = k8sClient.Get(context.TODO(), predictorDeploymentKey, deployed2)
				return deployed2.Spec.Template.Spec.Containers[0].Env
			}, timeout, interval).Should(ContainElements(appendedEnvs))

			// Now remove the default envs and update the isvc
			fmt.Fprintln(GinkgoWriter, "### Removing default envs")
			isvcUpdated2 := &v1beta1.InferenceService{}
			Eventually(func() bool {
				// get the latest deployed version
				if err := k8sClient.Get(context.TODO(), serviceKey, isvcUpdated1); err != nil {
					return false
				}

				isvcUpdated2 = isvcUpdated1.DeepCopy()
				isvcUpdated2.Spec.Predictor.Model.Env = newEnvs
				// Make sure the default envs were removed before updating the isvc
				Expect(isvcUpdated2.Spec.Predictor.Model.Env).ToNot(ContainElements(defaultEnvs))

				if err1 := k8sClient.Update(context.TODO(), isvcUpdated2); err1 != nil {
					return false
				}
				return true
			}, timeout, interval).Should(BeTrue())

			deployed3 := &appsv1.Deployment{}
			Eventually(func() []corev1.EnvVar {
				_ = k8sClient.Get(context.TODO(), predictorDeploymentKey, deployed3)
				return deployed3.Spec.Template.Spec.Containers[0].Env
			}, timeout, interval).Should(Not(ContainElements(defaultEnvs)))

			Expect(deployed3.Spec.Template.Spec.Containers[0].Env).ToNot(ContainElement(HaveField("Value", "env_marked_for_deletion")))
			Expect(deployed3.Spec.Template.Spec.Containers[0].Env).To(ContainElements(newEnvs))
		})
	})
	Context("When creating inference service with raw kube predictor and `serving.kserve.io/stop`", func() {
		// --- Default values ---
		configs := map[string]string{
			"explainers": `{
				"alibi": {
					"image": "kserve/alibi-explainer",
					"defaultImageVersion": "latest"
				}
			}`,
			"ingress": `{
				"enableGatewayApi": true,
				"kserveIngressGateway": "kserve/kserve-ingress-gateway",
				"ingressGateway": "knative-serving/knative-ingress-gateway",
				"localGateway": "knative-serving/knative-local-gateway",
				"localGatewayService": "knative-local-gateway.istio-system.svc.cluster.local",
				"additionalIngressDomains": ["additional.example.com"]
			}`,
			"storageInitializer": `{
				"image" : "kserve/storage-initializer:latest",
				"memoryRequest": "100Mi",
				"memoryLimit": "1Gi",
				"cpuRequest": "100m",
				"cpuLimit": "1",
				"CaBundleConfigMapName": "",
				"caBundleVolumeMountPath": "/etc/ssl/custom-certs",
				"enableDirectPvcVolumeMount": false
			}`,
		}

		createInferenceServiceConfigMap := func() *corev1.ConfigMap {
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.InferenceServiceConfigMapName,
					Namespace: constants.KServeNamespace,
				},
				Data: maps.Clone(configs),
			}
			return configMap
		}

		createServingRuntime := func(namespace string, name string) *v1alpha1.ServingRuntime {
			servingRuntime := &v1alpha1.ServingRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace,
				},
				Spec: v1alpha1.ServingRuntimeSpec{
					SupportedModelFormats: []v1alpha1.SupportedModelFormat{
						{
							Name:       "tensorflow",
							Version:    proto.String("1"),
							AutoSelect: proto.Bool(true),
						},
					},
					ServingRuntimePodSpec: v1alpha1.ServingRuntimePodSpec{
						Containers: []corev1.Container{
							{
								Name:    "kserve-container",
								Image:   "tensorflow/serving:1.14.0",
								Command: []string{"/usr/bin/tensorflow_model_server"},
								Args: []string{
									"--port=9000",
									"--rest_api_port=8080",
									"--model_base_path=/mnt/models",
									"--rest_api_timeout_in_ms=60000",
								},
								Resources: defaultResource,
							},
						},
					},
					Disabled: proto.Bool(false),
				},
			}

			return servingRuntime
		}

		defaultIsvc := func(serviceKey types.NamespacedName, storageUri string, autoscaler string, qty resource.Quantity) *v1beta1.InferenceService {
			predictor := v1beta1.PredictorSpec{
				ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
					MinReplicas:    ptr.To(int32(1)),
					MaxReplicas:    3,
					TimeoutSeconds: ptr.To(int64(30)),
					AutoScaling: &v1beta1.AutoScalingSpec{
						Metrics: []v1beta1.MetricsSpec{
							{
								Type: v1beta1.ResourceMetricSourceType,
								Resource: &v1beta1.ResourceMetricSource{
									Name: v1beta1.ResourceMetricMemory,
									Target: v1beta1.MetricTarget{
										Type:         v1beta1.AverageValueMetricType,
										AverageValue: &qty,
									},
								},
							},
						},
					},
				},
				Tensorflow: &v1beta1.TFServingSpec{
					PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
						StorageURI:     &storageUri,
						RuntimeVersion: proto.String("1.14.0"),
						Container: corev1.Container{
							Name:      constants.InferenceServiceContainerName,
							Resources: defaultResource,
						},
					},
				},
			}
			isvc := &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: autoscaler,
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: predictor,
				},
			}

			isvc.DefaultInferenceService(nil, nil, &v1beta1.SecurityConfig{AutoMountServiceAccountToken: false}, nil)
			return isvc
		}

		// --- Reusable Check Functions ---
		// Wait for the ISVC to exist.
		expectIsvcToExist := func(ctx context.Context, serviceKey types.NamespacedName) v1beta1.InferenceService {
			// Check that the ISVC was updated
			updatedIsvc := &v1beta1.InferenceService{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, updatedIsvc)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			return *updatedIsvc
		}

		// Wait for the Deployment to exist and then set the DeploymentAvailable condition to true
		expectDeploymentToBeReady := func(ctx context.Context, predictorKey types.NamespacedName) {
			actualDeployment := &appsv1.Deployment{}
			Eventually(func() error { return k8sClient.Get(ctx, predictorKey, actualDeployment) }, timeout).
				Should(Succeed())

			updatedDeployment := actualDeployment.DeepCopy()
			updatedDeployment.Status.Conditions = []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: corev1.ConditionTrue,
				},
			}
			Expect(k8sClient.Status().Update(ctx, updatedDeployment)).NotTo(HaveOccurred())
		}

		// Waits for the http route to be ready
		// Note: top level route uses serviceKey, predictor route uses predictorKey
		expectHttpRouteToBeReady := func(ctx context.Context, objKey types.NamespacedName) {
			actualHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(ctx, objKey, actualHttpRoute)
			}, timeout).Should(Succeed())

			// Mark the route as accepted to make isvc ready
			httpRouteStatus := gatewayapiv1.HTTPRouteStatus{
				RouteStatus: gatewayapiv1.RouteStatus{
					Parents: []gatewayapiv1.RouteParentStatus{
						{
							ParentRef: gatewayapiv1.ParentReference{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
							ControllerName: "istio.io/gateway-controller",
							Conditions: []metav1.Condition{
								{
									Type:               string(gatewayapiv1.ListenerConditionAccepted),
									Status:             metav1.ConditionTrue,
									Reason:             "Accepted",
									Message:            "Route was valid",
									LastTransitionTime: metav1.Now(),
								},
							},
						},
					},
				},
			}
			actualHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(ctx, actualHttpRoute)).NotTo(HaveOccurred())
		}

		// Wait for the InferenceService's Stopped condition to be false.
		expectIsvcFalseStoppedStatus := func(ctx context.Context, serviceKey types.NamespacedName) {
			// Check that the stopped condition is false
			updatedIsvc := &v1beta1.InferenceService{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, updatedIsvc)
				if err == nil {
					stopped_cond := updatedIsvc.Status.GetCondition(v1beta1.Stopped)
					if stopped_cond != nil && stopped_cond.Status == corev1.ConditionFalse {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue(), "The stopped condition should be set to false")
		}

		// Wait for the InferenceService's PredictorReady and IngressReady condition.
		expectIsvcReadyStatus := func(ctx context.Context, serviceKey types.NamespacedName) {
			updatedIsvc := &v1beta1.InferenceService{}
			// Check that the inference service is ready
			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, updatedIsvc)
				if err != nil {
					return false
				}
				readyCond := updatedIsvc.Status.GetCondition(v1beta1.PredictorReady)
				return readyCond != nil && readyCond.Status == corev1.ConditionTrue
			}, timeout, interval).Should(BeTrue(), "The predictor should be ready")

			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, updatedIsvc)
				if err != nil {
					return false
				}
				readyCond := updatedIsvc.Status.GetCondition(v1beta1.IngressReady)
				return readyCond != nil && readyCond.Status == corev1.ConditionTrue
			}, timeout, interval).Should(BeTrue(), "The ingress should be ready")
		}

		// Wait for the InferenceService's TransformerReady condition.
		expectIsvcTransformerReadyStatus := func(ctx context.Context, serviceKey types.NamespacedName) {
			updatedIsvc := &v1beta1.InferenceService{}
			// Check that the transformer is ready
			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, updatedIsvc)
				if err != nil {
					return false
				}
				readyCond := updatedIsvc.Status.GetCondition(v1beta1.TransformerReady)
				return readyCond != nil && readyCond.Status == corev1.ConditionTrue
			}, timeout, interval).Should(BeTrue(), "The transformer should be ready")
		}

		// Wait for the InferenceService's ExplainerReady condition.
		expectIsvcExplainerReadyStatus := func(ctx context.Context, serviceKey types.NamespacedName) {
			updatedIsvc := &v1beta1.InferenceService{}
			// Check that the explainer is ready
			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, updatedIsvc)
				if err != nil {
					return false
				}
				readyCond := updatedIsvc.Status.GetCondition(v1beta1.ExplainerReady)
				return readyCond != nil && readyCond.Status == corev1.ConditionTrue
			}, timeout, interval).Should(BeTrue(), "The explainer should be ready")
		}

		// Wait for the InferenceService's Stopped condition to be true.
		expectIsvcTrueStoppedStatus := func(ctx context.Context, serviceKey types.NamespacedName) {
			// Check that the ISVC status reflects that it is stopped
			updatedIsvc := &v1beta1.InferenceService{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, updatedIsvc)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, updatedIsvc)
				if err == nil {
					stopped_cond := updatedIsvc.Status.GetCondition(v1beta1.Stopped)
					if stopped_cond != nil && stopped_cond.Status == corev1.ConditionTrue {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue(), "The stopped condition should be set to true")
		}

		// Waits for any Kubernestes object to be found
		expectResourceToExist := func(ctx context.Context, obj client.Object, objKey types.NamespacedName) {
			Eventually(func() bool {
				err := k8sClient.Get(ctx, objKey, obj)
				return err == nil
			}, timeout, interval).Should(BeTrue(), "%T %s should exist", obj, objKey.Name)
		}

		// Checks that any Kubernetes object does not exist
		expectResourceDoesNotExist := func(ctx context.Context, obj client.Object, objKey types.NamespacedName) {
			Consistently(func() bool {
				err := k8sClient.Get(ctx, objKey, obj)
				return apierr.IsNotFound(err)
			}, time.Second*10, interval).Should(BeTrue(), "%T %s should not be created", obj, objKey.Name)
		}

		// Wait for any Kubernetes object to be not found.
		expectResourceToBeDeleted := func(ctx context.Context, obj client.Object, objKey types.NamespacedName) {
			Eventually(func() bool {
				err := k8sClient.Get(ctx, objKey, obj)
				return apierr.IsNotFound(err)
			}, timeout, interval).Should(BeTrue(), "%T %s should be deleted", obj, objKey.Name)
		}

		Describe("inference service only", func() {
			It("Should keep the httproute/service/deployment/hpa created when the annotation is set to false", func() {
				ctx, cancel := context.WithCancel(context.Background())
				DeferCleanup(cancel)

				// Config map
				configMap := createInferenceServiceConfigMap()
				Expect(k8sClient.Create(context.Background(), configMap)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, configMap)

				// Setup values
				serviceName := "stop-false-isvc"
				serviceNamespace := "default"
				expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: serviceNamespace}}
				serviceKey := expectedRequest.NamespacedName
				storageUri := "s3://test/mnist/export"
				qty := resource.MustParse("10Gi")
				predictorKey := types.NamespacedName{
					Name:      constants.PredictorServiceName(serviceKey.Name),
					Namespace: serviceKey.Namespace,
				}

				// Serving runtime
				servingRuntime := createServingRuntime(serviceKey.Namespace, "tf-serving-raw")
				Expect(k8sClient.Create(context.Background(), servingRuntime)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, servingRuntime)

				// Define InferenceService
				isvc := defaultIsvc(serviceKey, storageUri, string(constants.AutoscalerClassHPA), qty)
				isvc.Annotations[constants.StopAnnotationKey] = "false"
				Expect(k8sClient.Create(context.Background(), isvc)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, isvc)

				expectIsvcToExist(ctx, serviceKey)

				// Check the deployment
				expectDeploymentToBeReady(context.Background(), predictorKey)

				// check the service
				expectResourceToExist(context.Background(), &corev1.Service{}, predictorKey)

				// top level http route
				expectHttpRouteToBeReady(context.Background(), serviceKey)
				// predictor http route
				expectHttpRouteToBeReady(context.Background(), predictorKey)

				// check the HPA
				expectResourceToExist(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, predictorKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the stopped condition is false
				expectIsvcFalseStoppedStatus(ctx, serviceKey)

				// Check that the inference service is ready
				expectIsvcReadyStatus(ctx, serviceKey)
			})

			It("Should keep the ingress/service/deployment/keda/otel created when gateway api is disabled and the annotation is set to false", func() {
				ctx, cancel := context.WithCancel(context.Background())
				DeferCleanup(cancel)

				// Config map
				configMap := createInferenceServiceConfigMap()
				configMap.Data["ingress"] = `{
					"enableGatewayAPI": false,
					"ingressGateway": "knative-serving/knative-ingress-gateway",
					"localGateway": "knative-serving/knative-local-gateway",
					"localGatewayService": "knative-local-gateway.istio-system.svc.cluster.local"
				}`
				configMap.Data["opentelemetryCollector"] = `{
					"scrapeInterval": "5s",
					"metricReceiverEndpoint": "keda-otel-scaler.keda.svc:4317",
					"metricScalerEndpoint": "keda-otel-scaler.keda.svc:4318"
				}`
				Expect(k8sClient.Create(context.Background(), configMap)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, configMap)

				// Setup values
				serviceName := "stop-false-ingress-isvc"
				serviceNamespace := "default"
				expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: serviceNamespace}}
				serviceKey := expectedRequest.NamespacedName
				storageUri := "s3://test/mnist/export"
				qty := resource.MustParse("10Gi")
				predictorKey := types.NamespacedName{
					Name:      constants.PredictorServiceName(serviceKey.Name),
					Namespace: serviceKey.Namespace,
				}

				// Serving runtime
				servingRuntime := createServingRuntime(serviceKey.Namespace, "tf-serving-raw")
				Expect(k8sClient.Create(context.Background(), servingRuntime)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, servingRuntime)

				// Define InferenceService
				isvc := defaultIsvc(serviceKey, storageUri, string(constants.AutoscalerClassKeda), qty)
				isvc.Annotations[constants.StopAnnotationKey] = "false"
				isvc.Annotations["sidecar.opentelemetry.io/inject"] = "true"
				isvc.Spec.Predictor.ComponentExtensionSpec.AutoScaling = &v1beta1.AutoScalingSpec{
					Metrics: []v1beta1.MetricsSpec{
						{
							Type: v1beta1.PodMetricSourceType,
							PodMetric: &v1beta1.PodMetricSource{
								Metric: v1beta1.PodMetrics{
									Backend:     v1beta1.OpenTelemetryBackend,
									MetricNames: []string{"process_cpu_seconds_total"},
									Query:       "avg(process_cpu_seconds_total)",
								},
								Target: v1beta1.MetricTarget{
									Type:  v1beta1.ValueMetricType,
									Value: &resource.Quantity{},
								},
							},
						},
					},
				}
				Expect(k8sClient.Create(context.Background(), isvc)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, isvc)

				expectIsvcToExist(ctx, serviceKey)

				// Check the OpenTelemetry Collector
				expectResourceToExist(context.Background(), &otelv1beta1.OpenTelemetryCollector{}, predictorKey)

				// Check the deployment
				expectDeploymentToBeReady(context.Background(), predictorKey)

				// Check the service
				expectResourceToExist(context.Background(), &corev1.Service{}, predictorKey)

				// Check ingress
				expectResourceToExist(context.Background(), &netv1.Ingress{}, serviceKey)

				// Check KEDA
				expectResourceToExist(context.Background(), &kedav1alpha1.ScaledObject{}, predictorKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the stopped condition is false
				expectIsvcFalseStoppedStatus(ctx, serviceKey)

				// Check that the inference service is ready
				expectIsvcReadyStatus(ctx, serviceKey)
			})

			It("Should not create the httproute/service/deployment/hpa when the annotation is set to true", func() {
				ctx, cancel := context.WithCancel(context.Background())
				DeferCleanup(cancel)

				// Config map
				configMap := createInferenceServiceConfigMap()
				Expect(k8sClient.Create(context.Background(), configMap)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, configMap)

				// Setup values
				serviceName := "stop-true-isvc"
				serviceNamespace := "default"
				expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: serviceNamespace}}
				serviceKey := expectedRequest.NamespacedName
				storageUri := "s3://test/mnist/export"
				qty := resource.MustParse("10Gi")
				predictorKey := types.NamespacedName{
					Name:      constants.PredictorServiceName(serviceKey.Name),
					Namespace: serviceKey.Namespace,
				}

				// Serving runtime
				servingRuntime := createServingRuntime(serviceKey.Namespace, "tf-serving-raw")
				Expect(k8sClient.Create(context.Background(), servingRuntime)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, servingRuntime)

				// Define InferenceService
				isvc := defaultIsvc(serviceKey, storageUri, string(constants.AutoscalerClassHPA), qty)
				isvc.Annotations[constants.StopAnnotationKey] = "true"
				Expect(k8sClient.Create(context.Background(), isvc)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, isvc)

				expectIsvcToExist(ctx, serviceKey)

				// Check that the deployment was not created
				expectResourceDoesNotExist(context.Background(), &appsv1.Deployment{}, predictorKey)

				// check that the service was not created
				expectResourceDoesNotExist(context.Background(), &corev1.Service{}, predictorKey)

				// check that the http routes were not created
				// top level http route
				expectResourceDoesNotExist(context.Background(), &gatewayapiv1.HTTPRoute{}, serviceKey)
				// predictor http route
				expectResourceDoesNotExist(context.Background(), &gatewayapiv1.HTTPRoute{}, predictorKey)

				// check that the HPA was not created
				expectResourceDoesNotExist(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, predictorKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the ISVC status reflects that it is stopped
				expectIsvcTrueStoppedStatus(ctx, serviceKey)
			})

			It("Should not create the ingress/service/deployment/keda/otel when gateway api is disabled and the annotation is set to true", func() {
				ctx, cancel := context.WithCancel(context.Background())
				DeferCleanup(cancel)

				// Config map
				configMap := createInferenceServiceConfigMap()
				configMap.Data["ingress"] = `{
					"enableGatewayAPI": false,
					"ingressGateway": "knative-serving/knative-ingress-gateway",
					"localGateway": "knative-serving/knative-local-gateway",
					"localGatewayService": "knative-local-gateway.istio-system.svc.cluster.local"
				}`
				configMap.Data["opentelemetryCollector"] = `{
					"scrapeInterval": "5s",
					"metricReceiverEndpoint": "keda-otel-scaler.keda.svc:4317",
					"metricScalerEndpoint": "keda-otel-scaler.keda.svc:4318"
				}`
				Expect(k8sClient.Create(context.Background(), configMap)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, configMap)

				// Setup values
				serviceName := "stop-true-ingress-isvc"
				serviceNamespace := "default"
				expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: serviceNamespace}}
				serviceKey := expectedRequest.NamespacedName
				storageUri := "s3://test/mnist/export"
				qty := resource.MustParse("10Gi")
				predictorKey := types.NamespacedName{
					Name:      constants.PredictorServiceName(serviceKey.Name),
					Namespace: serviceKey.Namespace,
				}

				// Serving runtime
				servingRuntime := createServingRuntime(serviceKey.Namespace, "tf-serving-raw")
				Expect(k8sClient.Create(context.Background(), servingRuntime)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, servingRuntime)

				// Define InferenceService
				isvc := defaultIsvc(serviceKey, storageUri, string(constants.AutoscalerClassKeda), qty)
				isvc.Annotations[constants.StopAnnotationKey] = "true"
				isvc.Annotations["sidecar.opentelemetry.io/inject"] = "true"
				isvc.Spec.Predictor.ComponentExtensionSpec.AutoScaling = &v1beta1.AutoScalingSpec{
					Metrics: []v1beta1.MetricsSpec{
						{
							Type: v1beta1.PodMetricSourceType,
							PodMetric: &v1beta1.PodMetricSource{
								Metric: v1beta1.PodMetrics{
									Backend:     v1beta1.OpenTelemetryBackend,
									MetricNames: []string{"process_cpu_seconds_total"},
									Query:       "avg(process_cpu_seconds_total)",
								},
								Target: v1beta1.MetricTarget{
									Type:  v1beta1.ValueMetricType,
									Value: &resource.Quantity{},
								},
							},
						},
					},
				}
				Expect(k8sClient.Create(context.Background(), isvc)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, isvc)

				expectIsvcToExist(ctx, serviceKey)

				// Check that the OpenTelemetry Collector was not created
				expectResourceDoesNotExist(context.Background(), &otelv1beta1.OpenTelemetryCollector{}, predictorKey)

				// Check that the deployment was not created
				expectResourceDoesNotExist(context.Background(), &appsv1.Deployment{}, predictorKey)

				// check that the service was not created
				expectResourceDoesNotExist(context.Background(), &corev1.Service{}, predictorKey)

				// Check that the ingress was not created
				expectResourceDoesNotExist(context.Background(), &netv1.Ingress{}, serviceKey)

				// Check that the KEDA autoscaler was not created
				expectResourceDoesNotExist(context.Background(), &kedav1alpha1.ScaledObject{}, predictorKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the ISVC status reflects that it is stopped
				expectIsvcTrueStoppedStatus(ctx, serviceKey)
			})

			It("Should delete the httproute/service/deployment/hpa when the annotation is updated to true on an existing ISVC", func() {
				ctx, cancel := context.WithCancel(context.Background())
				DeferCleanup(cancel)

				// Config map
				configMap := createInferenceServiceConfigMap()
				Expect(k8sClient.Create(context.Background(), configMap)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, configMap)

				// Setup values
				serviceName := "stop-edit-true-isvc"
				serviceNamespace := "default"
				expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: serviceNamespace}}
				serviceKey := expectedRequest.NamespacedName
				storageUri := "s3://test/mnist/export"
				qty := resource.MustParse("10Gi")
				predictorKey := types.NamespacedName{
					Name:      constants.PredictorServiceName(serviceKey.Name),
					Namespace: serviceKey.Namespace,
				}

				// Serving runtime
				servingRuntime := createServingRuntime(serviceKey.Namespace, "tf-serving-raw")
				Expect(k8sClient.Create(context.Background(), servingRuntime)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, servingRuntime)

				// Define InferenceService
				isvc := defaultIsvc(serviceKey, storageUri, string(constants.AutoscalerClassHPA), qty)
				isvc.Annotations[constants.StopAnnotationKey] = "false"
				Expect(k8sClient.Create(context.Background(), isvc)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, isvc)

				expectIsvcToExist(ctx, serviceKey)

				// Check the deployment
				expectDeploymentToBeReady(ctx, predictorKey)

				// check the service
				expectResourceToExist(context.Background(), &corev1.Service{}, predictorKey)

				// check the http routes
				// top level http route
				expectHttpRouteToBeReady(context.Background(), serviceKey)
				// predictor http route
				expectHttpRouteToBeReady(context.Background(), predictorKey)

				// check the HPA
				expectResourceToExist(context.Background(), &autoscalingv2.HorizontalPodAutoscaler{}, predictorKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the stopped condition is false
				expectIsvcFalseStoppedStatus(ctx, serviceKey)

				// Check that the inference service is ready
				expectIsvcReadyStatus(ctx, serviceKey)

				// Stop the inference service
				updatedIsvc := expectIsvcToExist(ctx, serviceKey)
				stoppedIsvc := updatedIsvc.DeepCopy()
				stoppedIsvc.Annotations[constants.StopAnnotationKey] = "true"
				Expect(k8sClient.Update(ctx, stoppedIsvc)).NotTo(HaveOccurred())

				// Check that the deployment was deleted
				expectResourceToBeDeleted(context.Background(), &appsv1.Deployment{}, predictorKey)

				// check that the service was deleted
				expectResourceToBeDeleted(context.Background(), &corev1.Service{}, predictorKey)

				// check that the http routes were deleted
				// top level http route
				expectResourceToBeDeleted(context.Background(), &gatewayapiv1.HTTPRoute{}, serviceKey)
				// predictor http route
				expectResourceToBeDeleted(context.Background(), &gatewayapiv1.HTTPRoute{}, predictorKey)

				// check that the HPA was deleted
				expectResourceToBeDeleted(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, predictorKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the ISVC status reflects that it is stopped
				expectIsvcTrueStoppedStatus(ctx, serviceKey)
			})

			It("Should delete the ingress/service/deployment/keda/otel when gateway api is disabled and the annotation is updated to true on an existing ISVC", func() {
				ctx, cancel := context.WithCancel(context.Background())
				DeferCleanup(cancel)

				// Config map
				configMap := createInferenceServiceConfigMap()
				configMap.Data["ingress"] = `{
					"enableGatewayAPI": false,
					"ingressGateway": "knative-serving/knative-ingress-gateway",
					"localGateway": "knative-serving/knative-local-gateway",
					"localGatewayService": "knative-local-gateway.istio-system.svc.cluster.local"
				}`
				configMap.Data["opentelemetryCollector"] = `{
					"scrapeInterval": "5s",
					"metricReceiverEndpoint": "keda-otel-scaler.keda.svc:4317",
					"metricScalerEndpoint": "keda-otel-scaler.keda.svc:4318"
				}`
				Expect(k8sClient.Create(context.Background(), configMap)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, configMap)

				// Setup values
				serviceName := "stop-edit-ingress-isvc"
				serviceNamespace := "default"
				expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: serviceNamespace}}
				serviceKey := expectedRequest.NamespacedName
				storageUri := "s3://test/mnist/export"
				qty := resource.MustParse("10Gi")
				predictorKey := types.NamespacedName{
					Name:      constants.PredictorServiceName(serviceKey.Name),
					Namespace: serviceKey.Namespace,
				}

				// Serving runtime
				servingRuntime := createServingRuntime(serviceKey.Namespace, "tf-serving-raw")
				Expect(k8sClient.Create(context.Background(), servingRuntime)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, servingRuntime)

				// Define InferenceService
				isvc := defaultIsvc(serviceKey, storageUri, string(constants.AutoscalerClassKeda), qty)
				isvc.Annotations[constants.StopAnnotationKey] = "false"
				isvc.Annotations["sidecar.opentelemetry.io/inject"] = "true"
				isvc.Spec.Predictor.ComponentExtensionSpec.AutoScaling = &v1beta1.AutoScalingSpec{
					Metrics: []v1beta1.MetricsSpec{
						{
							Type: v1beta1.PodMetricSourceType,
							PodMetric: &v1beta1.PodMetricSource{
								Metric: v1beta1.PodMetrics{
									Backend:     v1beta1.OpenTelemetryBackend,
									MetricNames: []string{"process_cpu_seconds_total"},
									Query:       "avg(process_cpu_seconds_total)",
								},
								Target: v1beta1.MetricTarget{
									Type:  v1beta1.ValueMetricType,
									Value: &resource.Quantity{},
								},
							},
						},
					},
				}
				Expect(k8sClient.Create(context.Background(), isvc)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, isvc)

				expectIsvcToExist(ctx, serviceKey)

				// Check the OpenTelemetry Collector
				expectResourceToExist(context.Background(), &otelv1beta1.OpenTelemetryCollector{}, predictorKey)

				// Check the deployment
				expectDeploymentToBeReady(context.Background(), predictorKey)

				// Check the service
				expectResourceToExist(context.Background(), &corev1.Service{}, predictorKey)

				// Check ingress
				expectResourceToExist(context.Background(), &netv1.Ingress{}, serviceKey)

				// Check KEDA
				expectResourceToExist(context.Background(), &kedav1alpha1.ScaledObject{}, predictorKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the stopped condition is false
				expectIsvcFalseStoppedStatus(ctx, serviceKey)

				// Check that the inference service is ready
				expectIsvcReadyStatus(ctx, serviceKey)

				// Stop the inference service
				updatedIsvc := expectIsvcToExist(ctx, serviceKey)
				stoppedIsvc := updatedIsvc.DeepCopy()
				stoppedIsvc.Annotations[constants.StopAnnotationKey] = "true"
				Expect(k8sClient.Update(ctx, stoppedIsvc)).NotTo(HaveOccurred())

				// Check that the OpenTelemetry Collector was deleted
				expectResourceToBeDeleted(context.Background(), &otelv1beta1.OpenTelemetryCollector{}, predictorKey)

				// Check that the deployment was deleted
				expectResourceToBeDeleted(context.Background(), &appsv1.Deployment{}, predictorKey)

				// check that the service was deleted
				expectResourceToBeDeleted(context.Background(), &corev1.Service{}, predictorKey)

				// Check that the ingress was deleted
				expectResourceToBeDeleted(context.Background(), &netv1.Ingress{}, serviceKey)

				// Check that the KEDA autoscaler was deleted
				expectResourceToBeDeleted(context.Background(), &kedav1alpha1.ScaledObject{}, predictorKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the ISVC status reflects that it is stopped
				expectIsvcTrueStoppedStatus(ctx, serviceKey)
			})

			It("Should create the httproute/service/deployment/hpa when the annotation is updated to false on an existing ISVC that is stopped", func() {
				ctx, cancel := context.WithCancel(context.Background())
				DeferCleanup(cancel)

				// Config map
				configMap := createInferenceServiceConfigMap()
				Expect(k8sClient.Create(context.Background(), configMap)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, configMap)

				// Setup values
				serviceName := "stop-edit-false-isvc"
				serviceNamespace := "default"
				expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: serviceNamespace}}
				serviceKey := expectedRequest.NamespacedName
				storageUri := "s3://test/mnist/export"
				qty := resource.MustParse("10Gi")
				predictorKey := types.NamespacedName{
					Name:      constants.PredictorServiceName(serviceKey.Name),
					Namespace: serviceKey.Namespace,
				}

				// Serving runtime
				servingRuntime := createServingRuntime(serviceKey.Namespace, "tf-serving-raw")
				Expect(k8sClient.Create(context.Background(), servingRuntime)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, servingRuntime)

				// Define InferenceService
				isvc := defaultIsvc(serviceKey, storageUri, string(constants.AutoscalerClassHPA), qty)
				isvc.Annotations[constants.StopAnnotationKey] = "true"
				Expect(k8sClient.Create(context.Background(), isvc)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, isvc)

				expectIsvcToExist(ctx, serviceKey)

				// Check that the deployment was not created
				expectResourceDoesNotExist(context.Background(), &appsv1.Deployment{}, predictorKey)

				// check that the service was not created
				expectResourceDoesNotExist(context.Background(), &corev1.Service{}, predictorKey)

				// check that the http routes were not created
				// top level http route
				expectResourceDoesNotExist(context.Background(), &gatewayapiv1.HTTPRoute{}, serviceKey)
				// predictor http route
				expectResourceDoesNotExist(context.Background(), &gatewayapiv1.HTTPRoute{}, predictorKey)

				// check that the predictor HPA was not created
				expectResourceDoesNotExist(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, predictorKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the ISVC status reflects that it is stopped
				expectIsvcTrueStoppedStatus(ctx, serviceKey)

				// Resume the inference service
				updatedIsvc := expectIsvcToExist(ctx, serviceKey)
				stoppedIsvc := updatedIsvc.DeepCopy()
				stoppedIsvc.Annotations[constants.StopAnnotationKey] = "false"
				Expect(k8sClient.Update(ctx, stoppedIsvc)).NotTo(HaveOccurred())

				// Check the deployment
				expectDeploymentToBeReady(context.Background(), predictorKey)

				// check the service
				expectResourceToExist(context.Background(), &corev1.Service{}, predictorKey)

				// top level http route
				expectHttpRouteToBeReady(context.Background(), serviceKey)
				// predictor http route
				expectHttpRouteToBeReady(context.Background(), predictorKey)

				// check the HPA
				expectResourceToExist(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, predictorKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the stopped condition is false
				expectIsvcFalseStoppedStatus(ctx, serviceKey)

				// Check that the inference service is ready
				expectIsvcReadyStatus(ctx, serviceKey)
			})

			It("Should create the ingress/service/deployment/keda/otel when gateway api is disabled when the annotation is updated to false on an existing ISVC that is stopped", func() {
				ctx, cancel := context.WithCancel(context.Background())
				DeferCleanup(cancel)

				// Config map
				configMap := createInferenceServiceConfigMap()
				configMap.Data["ingress"] = `{
					"enableGatewayAPI": false,
					"ingressGateway": "knative-serving/knative-ingress-gateway",
					"localGateway": "knative-serving/knative-local-gateway",
					"localGatewayService": "knative-local-gateway.istio-system.svc.cluster.local"
				}`
				configMap.Data["opentelemetryCollector"] = `{
					"scrapeInterval": "5s",
					"metricReceiverEndpoint": "keda-otel-scaler.keda.svc:4317",
					"metricScalerEndpoint": "keda-otel-scaler.keda.svc:4318"
				}`
				Expect(k8sClient.Create(context.Background(), configMap)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, configMap)

				// Setup values
				serviceName := "stop-edit-false-ingress-isvc"
				serviceNamespace := "default"
				expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: serviceNamespace}}
				serviceKey := expectedRequest.NamespacedName
				storageUri := "s3://test/mnist/export"
				qty := resource.MustParse("10Gi")
				predictorKey := types.NamespacedName{
					Name:      constants.PredictorServiceName(serviceKey.Name),
					Namespace: serviceKey.Namespace,
				}

				// Serving runtime
				servingRuntime := createServingRuntime(serviceKey.Namespace, "tf-serving-raw")
				Expect(k8sClient.Create(context.Background(), servingRuntime)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, servingRuntime)

				// Define InferenceService
				isvc := defaultIsvc(serviceKey, storageUri, string(constants.AutoscalerClassKeda), qty)
				isvc.Annotations[constants.StopAnnotationKey] = "true"
				isvc.Annotations["sidecar.opentelemetry.io/inject"] = "true"
				isvc.Spec.Predictor.ComponentExtensionSpec.AutoScaling = &v1beta1.AutoScalingSpec{
					Metrics: []v1beta1.MetricsSpec{
						{
							Type: v1beta1.PodMetricSourceType,
							PodMetric: &v1beta1.PodMetricSource{
								Metric: v1beta1.PodMetrics{
									Backend:     v1beta1.OpenTelemetryBackend,
									MetricNames: []string{"process_cpu_seconds_total"},
									Query:       "avg(process_cpu_seconds_total)",
								},
								Target: v1beta1.MetricTarget{
									Type:  v1beta1.ValueMetricType,
									Value: &resource.Quantity{},
								},
							},
						},
					},
				}
				Expect(k8sClient.Create(context.Background(), isvc)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, isvc)

				expectIsvcToExist(ctx, serviceKey)

				// Check that the OpenTelemetry Collector was not created
				expectResourceDoesNotExist(context.Background(), &otelv1beta1.OpenTelemetryCollector{}, predictorKey)

				// Check that the deployment was not created
				expectResourceDoesNotExist(context.Background(), &appsv1.Deployment{}, predictorKey)

				// check that the service was not created
				expectResourceDoesNotExist(context.Background(), &corev1.Service{}, predictorKey)

				// Check that the ingress was not created
				expectResourceDoesNotExist(context.Background(), &netv1.Ingress{}, serviceKey)

				// Check that the KEDA autoscaler was not created
				expectResourceDoesNotExist(context.Background(), &kedav1alpha1.ScaledObject{}, predictorKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the ISVC status reflects that it is stopped
				expectIsvcTrueStoppedStatus(ctx, serviceKey)

				// Resume the inference service
				updatedIsvc := expectIsvcToExist(ctx, serviceKey)
				stoppedIsvc := updatedIsvc.DeepCopy()
				stoppedIsvc.Annotations[constants.StopAnnotationKey] = "false"
				Expect(k8sClient.Update(ctx, stoppedIsvc)).NotTo(HaveOccurred())

				// Check the OpenTelemetry Collector
				expectResourceToExist(context.Background(), &otelv1beta1.OpenTelemetryCollector{}, predictorKey)

				// Check the deployment
				expectDeploymentToBeReady(context.Background(), predictorKey)

				// Check the service
				expectResourceToExist(context.Background(), &corev1.Service{}, predictorKey)

				// Check ingress
				expectResourceToExist(context.Background(), &netv1.Ingress{}, serviceKey)

				// Check KEDA
				expectResourceToExist(context.Background(), &kedav1alpha1.ScaledObject{}, predictorKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the stopped condition is false
				expectIsvcFalseStoppedStatus(ctx, serviceKey)

				// Check that the inference service is ready
				expectIsvcReadyStatus(ctx, serviceKey)
			})
		})

		Describe("inference service with a transformer", func() {
			// --- Default values ---
			defaultTransformerIsvc := func(serviceKey types.NamespacedName, storageUri string, autoscaler string, qty resource.Quantity) *v1beta1.InferenceService {
				predictor := v1beta1.PredictorSpec{
					ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
						MinReplicas:    ptr.To(int32(1)),
						MaxReplicas:    3,
						TimeoutSeconds: ptr.To(int64(30)),
						AutoScaling: &v1beta1.AutoScalingSpec{
							Metrics: []v1beta1.MetricsSpec{
								{
									Type: v1beta1.ResourceMetricSourceType,
									Resource: &v1beta1.ResourceMetricSource{
										Name: v1beta1.ResourceMetricMemory,
										Target: v1beta1.MetricTarget{
											Type:         v1beta1.AverageValueMetricType,
											AverageValue: &qty,
										},
									},
								},
							},
						},
					},
					Tensorflow: &v1beta1.TFServingSpec{
						PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
							StorageURI:     &storageUri,
							RuntimeVersion: proto.String("1.14.0"),
							Container: corev1.Container{
								Name:      constants.InferenceServiceContainerName,
								Resources: defaultResource,
							},
						},
					},
				}
				transformer := &v1beta1.TransformerSpec{
					ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
						MinReplicas:    ptr.To(int32(1)),
						MaxReplicas:    1,
						ScaleTarget:    ptr.To(int32(80)),
						TimeoutSeconds: ptr.To(int64(30)),
					},
					PodSpec: v1beta1.PodSpec{
						Containers: []corev1.Container{
							{
								Image:     "transformer:v1",
								Resources: defaultResource,
								Args: []string{
									"--port=8080",
								},
							},
						},
					},
				}
				isvc := &v1beta1.InferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      serviceKey.Name,
						Namespace: serviceKey.Namespace,
						Annotations: map[string]string{
							constants.DeploymentMode:  string(constants.RawDeployment),
							constants.AutoscalerClass: autoscaler,
						},
					},
					Spec: v1beta1.InferenceServiceSpec{
						Predictor:   predictor,
						Transformer: transformer,
					},
				}

				isvc.DefaultInferenceService(nil, nil, &v1beta1.SecurityConfig{AutoMountServiceAccountToken: false}, nil)
				return isvc
			}

			It("Should keep the transformer httproute/service/deployment/hpa created when the annotation is set to false", func() {
				ctx, cancel := context.WithCancel(context.Background())
				DeferCleanup(cancel)

				// Config map
				configMap := createInferenceServiceConfigMap()
				Expect(k8sClient.Create(context.Background(), configMap)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, configMap)

				// Setup values
				serviceName := "stop-transformer-false-isvc"
				serviceNamespace := "default"
				expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: serviceNamespace}}
				serviceKey := expectedRequest.NamespacedName
				storageUri := "s3://test/mnist/export"
				qty := resource.MustParse("10Gi")
				predictorKey := types.NamespacedName{
					Name:      constants.PredictorServiceName(serviceKey.Name),
					Namespace: serviceKey.Namespace,
				}
				transformerKey := types.NamespacedName{
					Name:      constants.TransformerServiceName(serviceName),
					Namespace: serviceKey.Namespace,
				}

				// Serving runtime
				servingRuntime := createServingRuntime(serviceKey.Namespace, "tf-serving-raw")
				Expect(k8sClient.Create(context.Background(), servingRuntime)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, servingRuntime)

				// Define InferenceService
				isvc := defaultTransformerIsvc(serviceKey, storageUri, string(constants.AutoscalerClassHPA), qty)
				isvc.Annotations[constants.StopAnnotationKey] = "false"
				Expect(k8sClient.Create(context.Background(), isvc)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, isvc)

				expectIsvcToExist(ctx, serviceKey)

				// Check the predictor deployment
				expectDeploymentToBeReady(context.Background(), predictorKey)
				// Check the transformer deployment
				expectDeploymentToBeReady(context.Background(), transformerKey)

				// check the predictor service
				expectResourceToExist(context.Background(), &corev1.Service{}, predictorKey)
				// check the transformer service
				expectResourceToExist(context.Background(), &corev1.Service{}, transformerKey)

				// top level http route
				expectHttpRouteToBeReady(context.Background(), serviceKey)
				// predictor http route
				expectHttpRouteToBeReady(context.Background(), predictorKey)
				// transformer http route
				expectHttpRouteToBeReady(context.Background(), transformerKey)

				// check the predictor HPA
				expectResourceToExist(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, predictorKey)
				// check the transformer HPA
				expectResourceToExist(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, transformerKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the stopped condition is false
				expectIsvcFalseStoppedStatus(ctx, serviceKey)

				// Check that the inference service is ready
				expectIsvcReadyStatus(ctx, serviceKey)
				expectIsvcTransformerReadyStatus(ctx, serviceKey)
			})

			It("Should not create the transformer httproute/service/deployment/hpa when the annotation is set to true", func() {
				ctx, cancel := context.WithCancel(context.Background())
				DeferCleanup(cancel)

				// Config map
				configMap := createInferenceServiceConfigMap()
				Expect(k8sClient.Create(context.Background(), configMap)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, configMap)

				// Setup values
				serviceName := "stop-transformer-true-isvc"
				serviceNamespace := "default"
				expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: serviceNamespace}}
				serviceKey := expectedRequest.NamespacedName
				storageUri := "s3://test/mnist/export"
				qty := resource.MustParse("10Gi")
				predictorKey := types.NamespacedName{
					Name:      constants.PredictorServiceName(serviceKey.Name),
					Namespace: serviceKey.Namespace,
				}
				transformerKey := types.NamespacedName{
					Name:      constants.TransformerServiceName(serviceName),
					Namespace: serviceKey.Namespace,
				}

				// Serving runtime
				servingRuntime := createServingRuntime(serviceKey.Namespace, "tf-serving-raw")
				Expect(k8sClient.Create(context.Background(), servingRuntime)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, servingRuntime)

				// Define InferenceService
				isvc := defaultTransformerIsvc(serviceKey, storageUri, string(constants.AutoscalerClassHPA), qty)
				isvc.Annotations[constants.StopAnnotationKey] = "true"
				Expect(k8sClient.Create(context.Background(), isvc)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, isvc)

				expectIsvcToExist(ctx, serviceKey)

				// Check that the predictor deployment was not created
				expectResourceDoesNotExist(context.Background(), &appsv1.Deployment{}, predictorKey)
				// Check that the transformer deployment was not created
				expectResourceDoesNotExist(context.Background(), &appsv1.Deployment{}, transformerKey)

				// check that the predictor service was not created
				expectResourceDoesNotExist(context.Background(), &corev1.Service{}, predictorKey)
				// check that the transformer service was not created
				expectResourceDoesNotExist(context.Background(), &corev1.Service{}, transformerKey)

				// check that the http routes were not created
				// top level http route
				expectResourceDoesNotExist(context.Background(), &gatewayapiv1.HTTPRoute{}, serviceKey)
				// predictor http route
				expectResourceDoesNotExist(context.Background(), &gatewayapiv1.HTTPRoute{}, predictorKey)
				// transformer http route
				expectResourceDoesNotExist(context.Background(), &gatewayapiv1.HTTPRoute{}, transformerKey)

				// check that the predictor HPA was not created
				expectResourceDoesNotExist(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, predictorKey)
				// check that the transformer HPA was not created
				expectResourceDoesNotExist(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, transformerKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the ISVC status reflects that it is stopped
				expectIsvcTrueStoppedStatus(ctx, serviceKey)
			})

			It("Should delete the transformer httproute/service/deployment/hpa when the annotation is updated to true on an existing ISVC", func() {
				ctx, cancel := context.WithCancel(context.Background())
				DeferCleanup(cancel)

				// Config map
				configMap := createInferenceServiceConfigMap()
				Expect(k8sClient.Create(context.Background(), configMap)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, configMap)

				// Setup values
				serviceName := "stop-transformer-edit-true-isvc"
				serviceNamespace := "default"
				expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: serviceNamespace}}
				serviceKey := expectedRequest.NamespacedName
				storageUri := "s3://test/mnist/export"
				qty := resource.MustParse("10Gi")
				predictorKey := types.NamespacedName{
					Name:      constants.PredictorServiceName(serviceKey.Name),
					Namespace: serviceKey.Namespace,
				}
				transformerKey := types.NamespacedName{
					Name:      constants.TransformerServiceName(serviceName),
					Namespace: serviceKey.Namespace,
				}

				// Serving runtime
				servingRuntime := createServingRuntime(serviceKey.Namespace, "tf-serving-raw")
				Expect(k8sClient.Create(context.Background(), servingRuntime)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, servingRuntime)

				// Define InferenceService
				isvc := defaultTransformerIsvc(serviceKey, storageUri, string(constants.AutoscalerClassHPA), qty)
				isvc.Annotations[constants.StopAnnotationKey] = "false"
				Expect(k8sClient.Create(context.Background(), isvc)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, isvc)

				expectIsvcToExist(ctx, serviceKey)

				// Check the predictor deployment
				expectDeploymentToBeReady(ctx, predictorKey)
				// Check the transformer deployment
				expectDeploymentToBeReady(ctx, transformerKey)

				// check the predictor service
				expectResourceToExist(context.Background(), &corev1.Service{}, predictorKey)
				// check the transformer service
				expectResourceToExist(context.Background(), &corev1.Service{}, transformerKey)

				// top level http route
				expectHttpRouteToBeReady(context.Background(), serviceKey)
				// predictor http route
				expectHttpRouteToBeReady(context.Background(), predictorKey)
				// transformer http route
				expectHttpRouteToBeReady(context.Background(), transformerKey)

				// check the predictor HPA
				expectResourceToExist(context.Background(), &autoscalingv2.HorizontalPodAutoscaler{}, predictorKey)
				// check the transformer HPA
				expectResourceToExist(context.Background(), &autoscalingv2.HorizontalPodAutoscaler{}, transformerKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the stopped condition is false
				expectIsvcFalseStoppedStatus(ctx, serviceKey)

				// Check that the inference service is ready
				expectIsvcReadyStatus(ctx, serviceKey)
				expectIsvcTransformerReadyStatus(ctx, serviceKey)

				// Stop the inference service
				updatedIsvc := expectIsvcToExist(ctx, serviceKey)
				stoppedIsvc := updatedIsvc.DeepCopy()
				stoppedIsvc.Annotations[constants.StopAnnotationKey] = "true"
				Expect(k8sClient.Update(ctx, stoppedIsvc)).NotTo(HaveOccurred())

				// Check that the predictor deployment was deleted
				expectResourceToBeDeleted(context.Background(), &appsv1.Deployment{}, predictorKey)
				// Check that the transformer deployment was deleted
				expectResourceToBeDeleted(context.Background(), &appsv1.Deployment{}, transformerKey)

				// check that the predictor service was deleted
				expectResourceToBeDeleted(context.Background(), &corev1.Service{}, predictorKey)
				// check that the transformer service was deleted
				expectResourceToBeDeleted(context.Background(), &corev1.Service{}, transformerKey)

				// check that the http routes were deleted
				// top level http route
				expectResourceToBeDeleted(context.Background(), &gatewayapiv1.HTTPRoute{}, serviceKey)
				// predictor http route
				expectResourceToBeDeleted(context.Background(), &gatewayapiv1.HTTPRoute{}, predictorKey)
				// transformer http route
				expectResourceToBeDeleted(context.Background(), &gatewayapiv1.HTTPRoute{}, transformerKey)

				// check that the predictor HPA was deleted
				expectResourceToBeDeleted(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, predictorKey)
				// check that the transformer HPA was deleted
				expectResourceToBeDeleted(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, transformerKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the ISVC status reflects that it is stopped
				expectIsvcTrueStoppedStatus(ctx, serviceKey)
			})

			It("Should create the transformer httproute/service/deployment/hpa when the annotation is updated to false on an existing ISVC that is stopped", func() {
				ctx, cancel := context.WithCancel(context.Background())
				DeferCleanup(cancel)

				// Config map
				configMap := createInferenceServiceConfigMap()
				Expect(k8sClient.Create(context.Background(), configMap)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, configMap)

				// Setup values
				serviceName := "stop-transformer-edit-false-isvc"
				serviceNamespace := "default"
				expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: serviceNamespace}}
				serviceKey := expectedRequest.NamespacedName
				storageUri := "s3://test/mnist/export"
				qty := resource.MustParse("10Gi")
				predictorKey := types.NamespacedName{
					Name:      constants.PredictorServiceName(serviceKey.Name),
					Namespace: serviceKey.Namespace,
				}
				transformerKey := types.NamespacedName{
					Name:      constants.TransformerServiceName(serviceName),
					Namespace: serviceKey.Namespace,
				}

				// Serving runtime
				servingRuntime := createServingRuntime(serviceKey.Namespace, "tf-serving-raw")
				Expect(k8sClient.Create(context.Background(), servingRuntime)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, servingRuntime)

				// Define InferenceService
				isvc := defaultTransformerIsvc(serviceKey, storageUri, string(constants.AutoscalerClassHPA), qty)
				isvc.Annotations[constants.StopAnnotationKey] = "true"
				Expect(k8sClient.Create(context.Background(), isvc)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, isvc)

				expectIsvcToExist(ctx, serviceKey)

				// Check that the predictor deployment was not created
				expectResourceDoesNotExist(context.Background(), &appsv1.Deployment{}, predictorKey)
				// Check that the transformer deployment was not created
				expectResourceDoesNotExist(context.Background(), &appsv1.Deployment{}, transformerKey)

				// check that the predictor service was not created
				expectResourceDoesNotExist(context.Background(), &corev1.Service{}, predictorKey)
				// check that the transformer service was not created
				expectResourceDoesNotExist(context.Background(), &corev1.Service{}, transformerKey)

				// check that the http routes were not created
				// top level http route
				expectResourceDoesNotExist(context.Background(), &gatewayapiv1.HTTPRoute{}, serviceKey)
				// predictor http route
				expectResourceDoesNotExist(context.Background(), &gatewayapiv1.HTTPRoute{}, predictorKey)
				// transformer http route
				expectResourceDoesNotExist(context.Background(), &gatewayapiv1.HTTPRoute{}, transformerKey)

				// check that the predictor HPA was not created
				expectResourceDoesNotExist(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, predictorKey)
				// check that the transformer HPA was not created
				expectResourceDoesNotExist(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, transformerKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the ISVC status reflects that it is stopped
				expectIsvcTrueStoppedStatus(ctx, serviceKey)

				// Resume the inference service
				updatedIsvc := expectIsvcToExist(ctx, serviceKey)
				stoppedIsvc := updatedIsvc.DeepCopy()
				stoppedIsvc.Annotations[constants.StopAnnotationKey] = "false"
				Expect(k8sClient.Update(ctx, stoppedIsvc)).NotTo(HaveOccurred())

				// Check the predictor deployment
				expectDeploymentToBeReady(context.Background(), predictorKey)
				// Check the transformer deployment
				expectDeploymentToBeReady(context.Background(), transformerKey)

				// check the predictor service
				expectResourceToExist(context.Background(), &corev1.Service{}, predictorKey)
				// check the transformer service
				expectResourceToExist(context.Background(), &corev1.Service{}, transformerKey)

				// top level http route
				expectHttpRouteToBeReady(context.Background(), serviceKey)
				// predictor http route
				expectHttpRouteToBeReady(context.Background(), predictorKey)
				// transformer http route
				expectHttpRouteToBeReady(context.Background(), transformerKey)

				// check the predictor HPA
				expectResourceToExist(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, predictorKey)
				// check the transformer HPA
				expectResourceToExist(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, transformerKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the stopped condition is false
				expectIsvcFalseStoppedStatus(ctx, serviceKey)

				// Check that the inference service is ready
				expectIsvcReadyStatus(ctx, serviceKey)
				expectIsvcTransformerReadyStatus(ctx, serviceKey)
			})
		})

		Describe("inference service with an explainer", func() {
			// --- Default values ---
			defaultExplainerIsvc := func(serviceKey types.NamespacedName, storageUri string, autoscaler string, qty resource.Quantity) *v1beta1.InferenceService {
				predictor := v1beta1.PredictorSpec{
					ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
						MinReplicas:    ptr.To(int32(1)),
						MaxReplicas:    3,
						TimeoutSeconds: ptr.To(int64(30)),
						AutoScaling: &v1beta1.AutoScalingSpec{
							Metrics: []v1beta1.MetricsSpec{
								{
									Type: v1beta1.ResourceMetricSourceType,
									Resource: &v1beta1.ResourceMetricSource{
										Name: v1beta1.ResourceMetricMemory,
										Target: v1beta1.MetricTarget{
											Type:         v1beta1.AverageValueMetricType,
											AverageValue: &qty,
										},
									},
								},
							},
						},
					},
					Tensorflow: &v1beta1.TFServingSpec{
						PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
							StorageURI:     &storageUri,
							RuntimeVersion: proto.String("1.14.0"),
							Container: corev1.Container{
								Name:      constants.InferenceServiceContainerName,
								Resources: defaultResource,
							},
						},
					},
				}
				explainer := &v1beta1.ExplainerSpec{
					ART: &v1beta1.ARTExplainerSpec{
						Type: v1beta1.ARTSquareAttackExplainer,
						ExplainerExtensionSpec: v1beta1.ExplainerExtensionSpec{
							Config: map[string]string{"nb_classes": "10"},
							Container: corev1.Container{
								Name:      constants.InferenceServiceContainerName,
								Resources: defaultResource,
							},
							RuntimeVersion: proto.String("latest"),
						},
					},
					ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
						MinReplicas:    ptr.To(int32(1)),
						MaxReplicas:    2,
						ScaleTarget:    ptr.To(int32(80)),
						TimeoutSeconds: ptr.To(int64(30)),
					},
				}
				isvc := &v1beta1.InferenceService{
					ObjectMeta: metav1.ObjectMeta{
						Name:      serviceKey.Name,
						Namespace: serviceKey.Namespace,
						Annotations: map[string]string{
							constants.DeploymentMode:  string(constants.RawDeployment),
							constants.AutoscalerClass: autoscaler,
						},
					},
					Spec: v1beta1.InferenceServiceSpec{
						Predictor: predictor,
						Explainer: explainer,
					},
				}

				isvc.DefaultInferenceService(nil, nil, &v1beta1.SecurityConfig{AutoMountServiceAccountToken: false}, nil)
				return isvc
			}

			It("Should keep the explainer httproute/service/deployment/hpa created when the annotation is set to false", func() {
				ctx, cancel := context.WithCancel(context.Background())
				DeferCleanup(cancel)

				// Config map
				configMap := createInferenceServiceConfigMap()
				Expect(k8sClient.Create(context.Background(), configMap)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, configMap)

				// Setup values
				serviceName := "stop-explainer-false-isvc"
				serviceNamespace := "default"
				expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: serviceNamespace}}
				serviceKey := expectedRequest.NamespacedName
				storageUri := "s3://test/mnist/export"
				qty := resource.MustParse("10Gi")
				predictorKey := types.NamespacedName{
					Name:      constants.PredictorServiceName(serviceKey.Name),
					Namespace: serviceKey.Namespace,
				}
				explainerKey := types.NamespacedName{
					Name:      constants.ExplainerServiceName(serviceName),
					Namespace: serviceKey.Namespace,
				}

				// Serving runtime
				servingRuntime := createServingRuntime(serviceKey.Namespace, "tf-serving-raw")
				Expect(k8sClient.Create(context.Background(), servingRuntime)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, servingRuntime)

				// Define InferenceService
				isvc := defaultExplainerIsvc(serviceKey, storageUri, string(constants.AutoscalerClassHPA), qty)
				isvc.Annotations[constants.StopAnnotationKey] = "false"
				Expect(k8sClient.Create(context.Background(), isvc)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, isvc)

				expectIsvcToExist(ctx, serviceKey)

				// Check the predictor deployment
				expectDeploymentToBeReady(context.Background(), predictorKey)
				// Check the explainer deployment
				expectDeploymentToBeReady(context.Background(), explainerKey)

				// check the predictor service
				expectResourceToExist(context.Background(), &corev1.Service{}, predictorKey)
				// check the explainer service
				expectResourceToExist(context.Background(), &corev1.Service{}, explainerKey)

				// top level http route
				expectHttpRouteToBeReady(context.Background(), serviceKey)
				// predictor http route
				expectHttpRouteToBeReady(context.Background(), predictorKey)
				// explainer http route
				expectHttpRouteToBeReady(context.Background(), explainerKey)

				// check the predictor HPA
				expectResourceToExist(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, predictorKey)
				// check the explainer HPA
				expectResourceToExist(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, explainerKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the stopped condition is false
				expectIsvcFalseStoppedStatus(ctx, serviceKey)

				// Check that the inference service is ready
				expectIsvcReadyStatus(ctx, serviceKey)
				expectIsvcExplainerReadyStatus(ctx, serviceKey)
			})

			It("Should not create the explainer httproute/service/deployment/hpa when the annotation is set to true", func() {
				ctx, cancel := context.WithCancel(context.Background())
				DeferCleanup(cancel)

				// Config map
				configMap := createInferenceServiceConfigMap()
				Expect(k8sClient.Create(context.Background(), configMap)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, configMap)

				// Setup values
				serviceName := "stop-explainer-true-isvc"
				serviceNamespace := "default"
				expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: serviceNamespace}}
				serviceKey := expectedRequest.NamespacedName
				storageUri := "s3://test/mnist/export"
				qty := resource.MustParse("10Gi")
				predictorKey := types.NamespacedName{
					Name:      constants.PredictorServiceName(serviceKey.Name),
					Namespace: serviceKey.Namespace,
				}
				explainerKey := types.NamespacedName{
					Name:      constants.ExplainerServiceName(serviceName),
					Namespace: serviceKey.Namespace,
				}

				// Serving runtime
				servingRuntime := createServingRuntime(serviceKey.Namespace, "tf-serving-raw")
				Expect(k8sClient.Create(context.Background(), servingRuntime)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, servingRuntime)

				// Define InferenceService
				isvc := defaultExplainerIsvc(serviceKey, storageUri, string(constants.AutoscalerClassHPA), qty)
				isvc.Annotations[constants.StopAnnotationKey] = "true"
				Expect(k8sClient.Create(context.Background(), isvc)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, isvc)

				expectIsvcToExist(ctx, serviceKey)

				// Check that the predictor deployment was not created
				expectResourceDoesNotExist(context.Background(), &appsv1.Deployment{}, predictorKey)
				// Check that the explainer deployment was not created
				expectResourceDoesNotExist(context.Background(), &appsv1.Deployment{}, explainerKey)

				// check that the predictor service was not created
				expectResourceDoesNotExist(context.Background(), &corev1.Service{}, predictorKey)
				// check that the explainer service was not created
				expectResourceDoesNotExist(context.Background(), &corev1.Service{}, explainerKey)

				// check that the http routes were not created
				// top level http route
				expectResourceDoesNotExist(context.Background(), &gatewayapiv1.HTTPRoute{}, serviceKey)
				// predictor http route
				expectResourceDoesNotExist(context.Background(), &gatewayapiv1.HTTPRoute{}, predictorKey)
				// explainer http route
				expectResourceDoesNotExist(context.Background(), &gatewayapiv1.HTTPRoute{}, explainerKey)

				// check that the predictor HPA was not created
				expectResourceDoesNotExist(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, predictorKey)
				// check that the explainer HPA was not created
				expectResourceDoesNotExist(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, explainerKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the ISVC status reflects that it is stopped
				expectIsvcTrueStoppedStatus(ctx, serviceKey)
			})

			It("Should delete the explainer httproute/service/deployment/hpa when the annotation is updated to true on an existing ISVC", func() {
				ctx, cancel := context.WithCancel(context.Background())
				DeferCleanup(cancel)

				// Config map
				configMap := createInferenceServiceConfigMap()
				Expect(k8sClient.Create(context.Background(), configMap)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, configMap)

				// Setup values
				serviceName := "stop-explainer-edit-true-isvc"
				serviceNamespace := "default"
				expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: serviceNamespace}}
				serviceKey := expectedRequest.NamespacedName
				storageUri := "s3://test/mnist/export"
				qty := resource.MustParse("10Gi")
				predictorKey := types.NamespacedName{
					Name:      constants.PredictorServiceName(serviceKey.Name),
					Namespace: serviceKey.Namespace,
				}
				explainerKey := types.NamespacedName{
					Name:      constants.ExplainerServiceName(serviceName),
					Namespace: serviceKey.Namespace,
				}

				// Serving runtime
				servingRuntime := createServingRuntime(serviceKey.Namespace, "tf-serving-raw")
				Expect(k8sClient.Create(context.Background(), servingRuntime)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, servingRuntime)

				// Define InferenceService
				isvc := defaultExplainerIsvc(serviceKey, storageUri, string(constants.AutoscalerClassHPA), qty)
				isvc.Annotations[constants.StopAnnotationKey] = "false"
				Expect(k8sClient.Create(context.Background(), isvc)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, isvc)

				expectIsvcToExist(ctx, serviceKey)

				// Check the predictor deployment
				expectDeploymentToBeReady(ctx, predictorKey)
				// Check the explainer deployment
				expectDeploymentToBeReady(ctx, explainerKey)

				// check the predictor service
				expectResourceToExist(context.Background(), &corev1.Service{}, predictorKey)
				// check the explainer service
				expectResourceToExist(context.Background(), &corev1.Service{}, explainerKey)

				// top level http route
				expectHttpRouteToBeReady(context.Background(), serviceKey)
				// predictor http route
				expectHttpRouteToBeReady(context.Background(), predictorKey)
				// explainer http route
				expectHttpRouteToBeReady(context.Background(), explainerKey)

				// check the predictor HPA
				expectResourceToExist(context.Background(), &autoscalingv2.HorizontalPodAutoscaler{}, predictorKey)
				// check the explainer HPA
				expectResourceToExist(context.Background(), &autoscalingv2.HorizontalPodAutoscaler{}, explainerKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the stopped condition is false
				expectIsvcFalseStoppedStatus(ctx, serviceKey)

				// Check that the inference service is ready
				expectIsvcReadyStatus(ctx, serviceKey)
				expectIsvcExplainerReadyStatus(ctx, serviceKey)

				// Stop the inference service
				updatedIsvc := expectIsvcToExist(ctx, serviceKey)
				stoppedIsvc := updatedIsvc.DeepCopy()
				stoppedIsvc.Annotations[constants.StopAnnotationKey] = "true"
				Expect(k8sClient.Update(ctx, stoppedIsvc)).NotTo(HaveOccurred())

				// Check that the predictor deployment was deleted
				expectResourceToBeDeleted(context.Background(), &appsv1.Deployment{}, predictorKey)
				// Check that the explainer deployment was deleted
				expectResourceToBeDeleted(context.Background(), &appsv1.Deployment{}, explainerKey)

				// check that the predictor service was deleted
				expectResourceToBeDeleted(context.Background(), &corev1.Service{}, predictorKey)
				// check that the explainer service was deleted
				expectResourceToBeDeleted(context.Background(), &corev1.Service{}, explainerKey)

				// check that the http routes were deleted
				// top level http route
				expectResourceToBeDeleted(context.Background(), &gatewayapiv1.HTTPRoute{}, serviceKey)
				// predictor http route
				expectResourceToBeDeleted(context.Background(), &gatewayapiv1.HTTPRoute{}, predictorKey)
				// explainer http route
				expectResourceToBeDeleted(context.Background(), &gatewayapiv1.HTTPRoute{}, explainerKey)

				// check that the predictor HPA was deleted
				expectResourceToBeDeleted(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, predictorKey)
				// check that the explainer HPA was deleted
				expectResourceToBeDeleted(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, explainerKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the ISVC status reflects that it is stopped
				expectIsvcTrueStoppedStatus(ctx, serviceKey)
			})

			It("Should create the explainer httproute/service/deployment/hpa when the annotation is updated to false on an existing ISVC that is stopped", func() {
				ctx, cancel := context.WithCancel(context.Background())
				DeferCleanup(cancel)

				// Config map
				configMap := createInferenceServiceConfigMap()
				Expect(k8sClient.Create(context.Background(), configMap)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, configMap)

				// Setup values
				serviceName := "stop-explainer-edit-false-isvc"
				serviceNamespace := "default"
				expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: serviceNamespace}}
				serviceKey := expectedRequest.NamespacedName
				storageUri := "s3://test/mnist/export"
				qty := resource.MustParse("10Gi")
				predictorKey := types.NamespacedName{
					Name:      constants.PredictorServiceName(serviceKey.Name),
					Namespace: serviceKey.Namespace,
				}
				explainerKey := types.NamespacedName{
					Name:      constants.ExplainerServiceName(serviceName),
					Namespace: serviceKey.Namespace,
				}

				// Serving runtime
				servingRuntime := createServingRuntime(serviceKey.Namespace, "tf-serving-raw")
				Expect(k8sClient.Create(context.Background(), servingRuntime)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, servingRuntime)

				// Define InferenceService
				isvc := defaultExplainerIsvc(serviceKey, storageUri, string(constants.AutoscalerClassHPA), qty)
				isvc.Annotations[constants.StopAnnotationKey] = "true"
				Expect(k8sClient.Create(context.Background(), isvc)).NotTo(HaveOccurred())
				defer k8sClient.Delete(ctx, isvc)

				expectIsvcToExist(ctx, serviceKey)

				// Check that the predictor deployment was not created
				expectResourceDoesNotExist(context.Background(), &appsv1.Deployment{}, predictorKey)
				// Check that the explainer deployment was not created
				expectResourceDoesNotExist(context.Background(), &appsv1.Deployment{}, explainerKey)

				// check that the predictor service was not created
				expectResourceDoesNotExist(context.Background(), &corev1.Service{}, predictorKey)
				// check that the explainer service was not created
				expectResourceDoesNotExist(context.Background(), &corev1.Service{}, explainerKey)

				// check that the http routes were not created
				// top level http route
				expectResourceDoesNotExist(context.Background(), &gatewayapiv1.HTTPRoute{}, serviceKey)
				// predictor http route
				expectResourceDoesNotExist(context.Background(), &gatewayapiv1.HTTPRoute{}, predictorKey)
				// explainer http route
				expectResourceDoesNotExist(context.Background(), &gatewayapiv1.HTTPRoute{}, explainerKey)

				// check that the predictor HPA was not created
				expectResourceDoesNotExist(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, predictorKey)
				// check that the explainer HPA was not created
				expectResourceDoesNotExist(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, explainerKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the ISVC status reflects that it is stopped
				expectIsvcTrueStoppedStatus(ctx, serviceKey)

				// Resume the inference service
				updatedIsvc := expectIsvcToExist(ctx, serviceKey)
				stoppedIsvc := updatedIsvc.DeepCopy()
				stoppedIsvc.Annotations[constants.StopAnnotationKey] = "false"
				Expect(k8sClient.Update(ctx, stoppedIsvc)).NotTo(HaveOccurred())

				// Check the predictor deployment
				expectDeploymentToBeReady(context.Background(), predictorKey)
				// Check the explainer deployment
				expectDeploymentToBeReady(context.Background(), explainerKey)

				// check the predictor service
				expectResourceToExist(context.Background(), &corev1.Service{}, predictorKey)
				// check the explainer service
				expectResourceToExist(context.Background(), &corev1.Service{}, explainerKey)

				// top level http route
				expectHttpRouteToBeReady(context.Background(), serviceKey)
				// predictor http route
				expectHttpRouteToBeReady(context.Background(), predictorKey)
				// explainer http route
				expectHttpRouteToBeReady(context.Background(), explainerKey)

				// check the predictor HPA
				expectResourceToExist(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, predictorKey)
				// check the explainer HPA
				expectResourceToExist(ctx, &autoscalingv2.HorizontalPodAutoscaler{}, explainerKey)

				// Check that the ISVC was updated
				expectIsvcToExist(ctx, serviceKey)

				// Check that the stopped condition is false
				expectIsvcFalseStoppedStatus(ctx, serviceKey)

				// Check that the inference service is ready
				expectIsvcReadyStatus(ctx, serviceKey)
				expectIsvcExplainerReadyStatus(ctx, serviceKey)
			})
		})
	})
	Context("When creating inference service with raw kube predictor and ingress creation disabled", func() {
		configs := map[string]string{
			"explainers": `{
	             "alibi": {
	                "image": "kfserving/alibi-explainer",
			      "defaultImageVersion": "latest"
	             }
	          }`,
			"ingress": `{
			   "kserveIngressGateway": "kserve/kserve-ingress-gateway",
               "ingressGateway": "knative-serving/knative-ingress-gateway",
               "localGateway": "knative-serving/knative-local-gateway",
               "localGatewayService": "knative-local-gateway.istio-system.svc.cluster.local",
               "ingressDomain": "example.com",
			   "additionalIngressDomains": ["additional.example.com"],
			   "disableIngressCreation": true
            }`,
		}

		It("Should have service/deployment/hpa created and http route should not be created", func() {
			By("By creating a new InferenceService")
			// Create configmap
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.InferenceServiceConfigMapName,
					Namespace: constants.KServeNamespace,
				},
				Data: configs,
			}
			Expect(k8sClient.Create(context.TODO(), configMap)).NotTo(HaveOccurred())
			defer k8sClient.Delete(context.TODO(), configMap)
			// Create ServingRuntime
			servingRuntime := &v1alpha1.ServingRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "tf-serving-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.ServingRuntimeSpec{
					SupportedModelFormats: []v1alpha1.SupportedModelFormat{
						{
							Name:       "tensorflow",
							Version:    proto.String("1"),
							AutoSelect: proto.Bool(true),
						},
					},
					ServingRuntimePodSpec: v1alpha1.ServingRuntimePodSpec{
						Containers: []corev1.Container{
							{
								Name:    "kserve-container",
								Image:   "tensorflow/serving:1.14.0",
								Command: []string{"/usr/bin/tensorflow_model_server"},
								Args: []string{
									"--port=9000",
									"--rest_api_port=8080",
									"--model_base_path=/mnt/models",
									"--rest_api_timeout_in_ms=60000",
								},
								Resources: defaultResource,
							},
						},
					},
					Disabled: proto.Bool(false),
				},
			}
			k8sClient.Create(context.TODO(), servingRuntime)
			defer k8sClient.Delete(context.TODO(), servingRuntime)
			// Create InferenceService
			serviceName := "raw-foo-no-ingress-class"
			expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: "default"}}
			serviceKey := expectedRequest.NamespacedName
			storageUri := "s3://test/mnist/export"
			ctx := context.Background()
			isvc := &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: string(constants.AutoscalerClassHPA),
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
							MinReplicas: ptr.To(int32(1)),
							MaxReplicas: 3,
							AutoScaling: &v1beta1.AutoScalingSpec{
								Metrics: []v1beta1.MetricsSpec{
									{
										Type: v1beta1.ResourceMetricSourceType,
										Resource: &v1beta1.ResourceMetricSource{
											Name: v1beta1.ResourceMetricCPU,
											Target: v1beta1.MetricTarget{
												Type:               v1beta1.UtilizationMetricType,
												AverageUtilization: ptr.To(int32(75)),
											},
										},
									},
								},
							},
						},
						Tensorflow: &v1beta1.TFServingSpec{
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI:     &storageUri,
								RuntimeVersion: proto.String("1.14.0"),
								Container: corev1.Container{
									Name:      constants.InferenceServiceContainerName,
									Resources: defaultResource,
								},
							},
						},
					},
				},
			}
			isvc.DefaultInferenceService(nil, nil, &v1beta1.SecurityConfig{AutoMountServiceAccountToken: false}, nil)
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			defer k8sClient.Delete(ctx, isvc)

			inferenceService := &v1beta1.InferenceService{}

			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, inferenceService)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			actualDeployment := &appsv1.Deployment{}
			predictorDeploymentKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorDeploymentKey, actualDeployment) }, timeout).
				Should(Succeed())
			var replicas int32 = 1
			var revisionHistory int32 = 10
			var progressDeadlineSeconds int32 = 600
			var gracePeriod int64 = 30
			expectedDeployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorDeploymentKey.Name,
					Namespace: predictorDeploymentKey.Namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "isvc." + predictorDeploymentKey.Name,
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Name:      predictorDeploymentKey.Name,
							Namespace: "default",
							Labels: map[string]string{
								"app":                                 "isvc." + predictorDeploymentKey.Name,
								constants.KServiceComponentLabel:      constants.Predictor.String(),
								constants.InferenceServicePodLabelKey: serviceName,
							},
							Annotations: map[string]string{
								constants.StorageInitializerSourceUriInternalAnnotationKey: *isvc.Spec.Predictor.Model.StorageURI,
								constants.DeploymentMode:                                   string(constants.RawDeployment),
								constants.AutoscalerClass:                                  string(constants.AutoscalerClassHPA),
								constants.OpenshiftServingCertAnnotation:                   predictorDeploymentKey.Name + constants.ServingCertSecretSuffix,
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Image: "tensorflow/serving:" +
										*isvc.Spec.Predictor.Model.RuntimeVersion,
									Name:    constants.InferenceServiceContainerName,
									Command: []string{v1beta1.TensorflowEntrypointCommand},
									Args: []string{
										"--port=" + v1beta1.TensorflowServingGRPCPort,
										"--rest_api_port=" + v1beta1.TensorflowServingRestPort,
										"--model_base_path=" + constants.DefaultModelLocalMountPath,
										"--rest_api_timeout_in_ms=60000",
									},
									Resources: defaultResource,
									ReadinessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											TCPSocket: &corev1.TCPSocketAction{
												Port: intstr.IntOrString{
													IntVal: 8080,
												},
											},
										},
										InitialDelaySeconds: 0,
										TimeoutSeconds:      1,
										PeriodSeconds:       10,
										SuccessThreshold:    1,
										FailureThreshold:    3,
									},
									TerminationMessagePath:   "/dev/termination-log",
									TerminationMessagePolicy: "File",
									ImagePullPolicy:          "IfNotPresent",
								},
							},
							SchedulerName:                 "default-scheduler",
							RestartPolicy:                 "Always",
							TerminationGracePeriodSeconds: &gracePeriod,
							DNSPolicy:                     "ClusterFirst",
							SecurityContext: &corev1.PodSecurityContext{
								SELinuxOptions:      nil,
								WindowsOptions:      nil,
								RunAsUser:           nil,
								RunAsGroup:          nil,
								RunAsNonRoot:        nil,
								SupplementalGroups:  nil,
								FSGroup:             nil,
								Sysctls:             nil,
								FSGroupChangePolicy: nil,
								SeccompProfile:      nil,
							},
							AutomountServiceAccountToken: proto.Bool(false),
						},
					},
					Strategy: appsv1.DeploymentStrategy{
						Type: "RollingUpdate",
						RollingUpdate: &appsv1.RollingUpdateDeployment{
							MaxUnavailable: &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
							MaxSurge:       &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
						},
					},
					RevisionHistoryLimit:    &revisionHistory,
					ProgressDeadlineSeconds: &progressDeadlineSeconds,
				},
			}
			Expect(actualDeployment.Spec).To(BeComparableTo(expectedDeployment.Spec))

			// check service
			actualService := &corev1.Service{}
			predictorServiceKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorServiceKey, actualService) }, timeout).
				Should(Succeed())

			expectedService := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorServiceKey.Name,
					Namespace: predictorServiceKey.Namespace,
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       constants.PredictorServiceName(serviceName),
							Protocol:   "TCP",
							Port:       80,
							TargetPort: intstr.IntOrString{Type: 0, IntVal: 8080, StrVal: ""},
						},
					},
					Type:            "ClusterIP",
					SessionAffinity: "None",
					Selector: map[string]string{
						"app": "isvc." + constants.PredictorServiceName(serviceName),
					},
				},
			}
			actualService.Spec.ClusterIP = ""
			actualService.Spec.ClusterIPs = nil
			actualService.Spec.IPFamilies = nil
			actualService.Spec.IPFamilyPolicy = nil
			actualService.Spec.InternalTrafficPolicy = nil
			Expect(actualService.Spec).To(BeComparableTo(expectedService.Spec))

			// check isvc status
			updatedDeployment := actualDeployment.DeepCopy()
			updatedDeployment.Status.Conditions = []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: corev1.ConditionTrue,
				},
			}
			Expect(k8sClient.Status().Update(context.TODO(), updatedDeployment)).NotTo(HaveOccurred())

			// check ingress not created
			actualToplevelHttpRoute := &gatewayapiv1.HTTPRoute{}
			Consistently(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualToplevelHttpRoute)
			}, timeout).
				Should(Not(Succeed()))
			actualPredictorHttpRoute := &gatewayapiv1.HTTPRoute{}
			Consistently(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      predictorServiceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualPredictorHttpRoute)
			}, timeout).
				Should(Not(Succeed()))

			// verify if InferenceService status is updated
			expectedIsvcStatus := v1beta1.InferenceServiceStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:   v1beta1.IngressReady,
							Status: "True",
						},
						{
							Type:   v1beta1.PredictorReady,
							Status: "True",
						},
						{
							Type:   apis.ConditionReady,
							Status: "True",
						},
						{
							Type:     v1beta1.Stopped,
							Status:   "False",
							Severity: apis.ConditionSeverityInfo,
						},
					},
				},
				URL: &apis.URL{
					Scheme: "http",
					Host:   fmt.Sprintf("%s-predictor.%s.svc.cluster.local", serviceName, serviceKey.Namespace),
				},
				Address: &duckv1.Addressable{
					URL: &apis.URL{
						Scheme: "http",
						Host:   fmt.Sprintf("%s-predictor.%s.svc.cluster.local", serviceKey.Name, serviceKey.Namespace),
					},
				},
				Components: map[v1beta1.ComponentType]v1beta1.ComponentStatusSpec{
					v1beta1.PredictorComponent: {
						LatestCreatedRevision: "",
						// mocking it, see pkg/apis/serving/v1beta1/inference_service_status.go#344
						// uncomment when it gets supported by ODH
						// URL: &apis.URL{
						// 	Scheme: "http",
						// 	Host:   serviceName + "-predictor-default.example.com",
						// },
					},
				},
				ModelStatus: v1beta1.ModelStatus{
					TransitionStatus:    "InProgress",
					ModelRevisionStates: &v1beta1.ModelRevisionStates{TargetModelState: "Pending"},
				},
				DeploymentMode: string(constants.RawDeployment),
			}
			Eventually(func() string {
				isvc := &v1beta1.InferenceService{}
				if err := k8sClient.Get(context.TODO(), serviceKey, isvc); err != nil {
					return err.Error()
				}
				return cmp.Diff(&expectedIsvcStatus, &isvc.Status, cmpopts.IgnoreTypes(apis.VolatileTime{}))
			}, timeout).Should(BeEmpty())

			// check HPA
			var minReplicas int32 = 1
			var maxReplicas int32 = 3
			var cpuUtilization int32 = 75
			var stabilizationWindowSeconds int32 = 0
			selectPolicy := autoscalingv2.MaxChangePolicySelect
			actualHPA := &autoscalingv2.HorizontalPodAutoscaler{}
			predictorHPAKey := types.NamespacedName{
				Name:      predictorServiceKey.Name,
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorHPAKey, actualHPA) }, timeout).
				Should(Succeed())
			expectedHPA := &autoscalingv2.HorizontalPodAutoscaler{
				Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
					ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       predictorServiceKey.Name,
					},
					MinReplicas: &minReplicas,
					MaxReplicas: maxReplicas,
					Metrics: []autoscalingv2.MetricSpec{
						{
							Type: autoscalingv2.ResourceMetricSourceType,
							Resource: &autoscalingv2.ResourceMetricSource{
								Name: corev1.ResourceCPU,
								Target: autoscalingv2.MetricTarget{
									Type:               "Utilization",
									AverageUtilization: &cpuUtilization,
								},
							},
						},
					},
					Behavior: &autoscalingv2.HorizontalPodAutoscalerBehavior{
						ScaleUp: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: &stabilizationWindowSeconds,
							SelectPolicy:               &selectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Pods",
									Value:         4,
									PeriodSeconds: 15,
								},
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
						ScaleDown: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: nil,
							SelectPolicy:               &selectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
					},
				},
			}
			Expect(actualHPA.Spec).To(BeComparableTo(expectedHPA.Spec))
		})
	})
	Context("When creating inference service with raw kube predictor with domain template", func() {
		configs := map[string]string{
			"explainers": `{
	             "alibi": {
	                "image": "kfserving/alibi-explainer",
			      "defaultImageVersion": "latest"
	             }
	          }`,
			"ingress": `{
			   "enableGatewayApi": true,
			   "kserveIngressGateway": "kserve/kserve-ingress-gateway",
               "ingressGateway": "knative-serving/knative-ingress-gateway",
               "localGateway": "knative-serving/knative-local-gateway",
               "localGatewayService": "knative-local-gateway.istio-system.svc.cluster.local",
               "ingressDomain": "example.com",
               "domainTemplate": "{{ .Name }}.{{ .Namespace }}.{{ .IngressDomain }}",
			   "additionalIngressDomains": ["additional.example.com"]
            }`,
		}

		It("Should have httproute/service/deployment/hpa created", func() {
			By("By creating a new InferenceService")
			// Create configmap
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.InferenceServiceConfigMapName,
					Namespace: constants.KServeNamespace,
				},
				Data: configs,
			}
			Expect(k8sClient.Create(context.TODO(), configMap)).NotTo(HaveOccurred())
			defer k8sClient.Delete(context.TODO(), configMap)
			// Create ServingRuntime
			servingRuntime := &v1alpha1.ServingRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "tf-serving-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.ServingRuntimeSpec{
					SupportedModelFormats: []v1alpha1.SupportedModelFormat{
						{
							Name:       "tensorflow",
							Version:    proto.String("1"),
							AutoSelect: proto.Bool(true),
						},
					},
					ServingRuntimePodSpec: v1alpha1.ServingRuntimePodSpec{
						Containers: []corev1.Container{
							{
								Name:    "kserve-container",
								Image:   "tensorflow/serving:1.14.0",
								Command: []string{"/usr/bin/tensorflow_model_server"},
								Args: []string{
									"--port=9000",
									"--rest_api_port=8080",
									"--model_base_path=/mnt/models",
									"--rest_api_timeout_in_ms=60000",
								},
								Resources: defaultResource,
							},
						},
					},
					Disabled: proto.Bool(false),
				},
			}
			k8sClient.Create(context.TODO(), servingRuntime)
			defer k8sClient.Delete(context.TODO(), servingRuntime)
			// Create InferenceService
			serviceName := "model"
			expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: "default"}}
			serviceKey := expectedRequest.NamespacedName
			storageUri := "s3://test/mnist/export"
			ctx := context.Background()
			isvc := &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: string(constants.AutoscalerClassHPA),
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
							MinReplicas: ptr.To(int32(1)),
							MaxReplicas: 3,
							AutoScaling: &v1beta1.AutoScalingSpec{
								Metrics: []v1beta1.MetricsSpec{
									{
										Type: v1beta1.ResourceMetricSourceType,
										Resource: &v1beta1.ResourceMetricSource{
											Name: v1beta1.ResourceMetricCPU,
											Target: v1beta1.MetricTarget{
												Type:               v1beta1.UtilizationMetricType,
												AverageUtilization: ptr.To(int32(75)),
											},
										},
									},
								},
							},
						},
						Tensorflow: &v1beta1.TFServingSpec{
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI:     &storageUri,
								RuntimeVersion: proto.String("1.14.0"),
								Container: corev1.Container{
									Name:      constants.InferenceServiceContainerName,
									Resources: defaultResource,
								},
							},
						},
					},
				},
			}
			isvc.DefaultInferenceService(nil, nil, &v1beta1.SecurityConfig{AutoMountServiceAccountToken: false}, nil)
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			defer k8sClient.Delete(ctx, isvc)

			inferenceService := &v1beta1.InferenceService{}

			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, inferenceService)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			actualDeployment := &appsv1.Deployment{}
			predictorDeploymentKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorDeploymentKey, actualDeployment) }, timeout).
				Should(Succeed())
			var replicas int32 = 1
			var revisionHistory int32 = 10
			var progressDeadlineSeconds int32 = 600
			var gracePeriod int64 = 30
			expectedDeployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorDeploymentKey.Name,
					Namespace: predictorDeploymentKey.Namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "isvc." + predictorDeploymentKey.Name,
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Name:      predictorDeploymentKey.Name,
							Namespace: "default",
							Labels: map[string]string{
								"app":                                 "isvc." + predictorDeploymentKey.Name,
								constants.KServiceComponentLabel:      constants.Predictor.String(),
								constants.InferenceServicePodLabelKey: serviceName,
							},
							Annotations: map[string]string{
								constants.StorageInitializerSourceUriInternalAnnotationKey: *isvc.Spec.Predictor.Model.StorageURI,
								constants.DeploymentMode:                                   string(constants.RawDeployment),
								constants.AutoscalerClass:                                  string(constants.AutoscalerClassHPA),
								constants.OpenshiftServingCertAnnotation:                   predictorDeploymentKey.Name + constants.ServingCertSecretSuffix,
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Image: "tensorflow/serving:" +
										*isvc.Spec.Predictor.Model.RuntimeVersion,
									Name:    constants.InferenceServiceContainerName,
									Command: []string{v1beta1.TensorflowEntrypointCommand},
									Args: []string{
										"--port=" + v1beta1.TensorflowServingGRPCPort,
										"--rest_api_port=" + v1beta1.TensorflowServingRestPort,
										"--model_base_path=" + constants.DefaultModelLocalMountPath,
										"--rest_api_timeout_in_ms=60000",
									},
									Resources: defaultResource,
									ReadinessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											TCPSocket: &corev1.TCPSocketAction{
												Port: intstr.IntOrString{
													IntVal: 8080,
												},
											},
										},
										InitialDelaySeconds: 0,
										TimeoutSeconds:      1,
										PeriodSeconds:       10,
										SuccessThreshold:    1,
										FailureThreshold:    3,
									},
									TerminationMessagePath:   "/dev/termination-log",
									TerminationMessagePolicy: "File",
									ImagePullPolicy:          "IfNotPresent",
								},
							},
							SchedulerName:                 "default-scheduler",
							RestartPolicy:                 "Always",
							TerminationGracePeriodSeconds: &gracePeriod,
							DNSPolicy:                     "ClusterFirst",
							SecurityContext: &corev1.PodSecurityContext{
								SELinuxOptions:      nil,
								WindowsOptions:      nil,
								RunAsUser:           nil,
								RunAsGroup:          nil,
								RunAsNonRoot:        nil,
								SupplementalGroups:  nil,
								FSGroup:             nil,
								Sysctls:             nil,
								FSGroupChangePolicy: nil,
								SeccompProfile:      nil,
							},
							AutomountServiceAccountToken: proto.Bool(false),
						},
					},
					Strategy: appsv1.DeploymentStrategy{
						Type: "RollingUpdate",
						RollingUpdate: &appsv1.RollingUpdateDeployment{
							MaxUnavailable: &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
							MaxSurge:       &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
						},
					},
					RevisionHistoryLimit:    &revisionHistory,
					ProgressDeadlineSeconds: &progressDeadlineSeconds,
				},
			}
			Expect(actualDeployment.Spec).To(BeComparableTo(expectedDeployment.Spec))

			// check service
			actualService := &corev1.Service{}
			predictorServiceKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorServiceKey, actualService) }, timeout).
				Should(Succeed())

			expectedService := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorServiceKey.Name,
					Namespace: predictorServiceKey.Namespace,
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       constants.PredictorServiceName(serviceName),
							Protocol:   "TCP",
							Port:       80,
							TargetPort: intstr.IntOrString{Type: 0, IntVal: 8080, StrVal: ""},
						},
					},
					Type:            "ClusterIP",
					SessionAffinity: "None",
					Selector: map[string]string{
						"app": "isvc." + constants.PredictorServiceName(serviceName),
					},
				},
			}
			actualService.Spec.ClusterIP = ""
			actualService.Spec.ClusterIPs = nil
			actualService.Spec.IPFamilies = nil
			actualService.Spec.IPFamilyPolicy = nil
			actualService.Spec.InternalTrafficPolicy = nil
			Expect(actualService.Spec).To(BeComparableTo(expectedService.Spec))

			// check isvc status
			updatedDeployment := actualDeployment.DeepCopy()
			updatedDeployment.Status.Conditions = []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: corev1.ConditionTrue,
				},
			}
			Expect(k8sClient.Status().Update(context.TODO(), updatedDeployment)).NotTo(HaveOccurred())

			// check http route
			actualToplevelHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualToplevelHttpRoute)
			}, timeout).
				Should(Succeed())
			topLevelHost := fmt.Sprintf("%s.%s.%s", serviceKey.Name, serviceKey.Namespace, "example.com")
			expectedToplevelHttpRoute := gatewayapiv1.HTTPRoute{
				Spec: gatewayapiv1.HTTPRouteSpec{
					Hostnames: []gatewayapiv1.Hostname{gatewayapiv1.Hostname(topLevelHost), gatewayapiv1.Hostname(fmt.Sprintf("%s.%s.additional.example.com", serviceKey.Name, serviceKey.Namespace))},
					Rules: []gatewayapiv1.HTTPRouteRule{
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.FallbackPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(predictorServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("60s")),
							},
						},
					},
					CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
						ParentRefs: []gatewayapiv1.ParentReference{
							{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
						},
					},
				},
			}
			Expect(actualToplevelHttpRoute.Spec).To(BeComparableTo(expectedToplevelHttpRoute.Spec))

			actualPredictorHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      predictorServiceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualPredictorHttpRoute)
			}, timeout).
				Should(Succeed())
			predictorHost := fmt.Sprintf("%s.%s.%s", predictorServiceKey.Name, serviceKey.Namespace, "example.com")
			expectedPredictorHttpRoute := gatewayapiv1.HTTPRoute{
				Spec: gatewayapiv1.HTTPRouteSpec{
					Hostnames: []gatewayapiv1.Hostname{gatewayapiv1.Hostname(predictorHost)},
					Rules: []gatewayapiv1.HTTPRouteRule{
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.FallbackPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(predictorServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("60s")),
							},
						},
					},
					CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
						ParentRefs: []gatewayapiv1.ParentReference{
							{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
						},
					},
				},
			}
			Expect(actualPredictorHttpRoute.Spec).To(BeComparableTo(expectedPredictorHttpRoute.Spec))

			// Mark the Ingress as accepted to make isvc ready
			httpRouteStatus := gatewayapiv1.HTTPRouteStatus{
				RouteStatus: gatewayapiv1.RouteStatus{
					Parents: []gatewayapiv1.RouteParentStatus{
						{
							ParentRef: gatewayapiv1.ParentReference{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
							ControllerName: "istio.io/gateway-controller",
							Conditions: []metav1.Condition{
								{
									Type:               string(gatewayapiv1.ListenerConditionAccepted),
									Status:             metav1.ConditionTrue,
									Reason:             "Accepted",
									Message:            "Route was valid",
									LastTransitionTime: metav1.Now(),
								},
							},
						},
					},
				},
			}
			actualPredictorHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(context.Background(), actualPredictorHttpRoute)).NotTo(HaveOccurred())
			actualToplevelHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(context.Background(), actualToplevelHttpRoute)).NotTo(HaveOccurred())

			// verify if InferenceService status is updated
			expectedIsvcStatus := v1beta1.InferenceServiceStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:   v1beta1.IngressReady,
							Status: "True",
						},
						{
							Type:   v1beta1.PredictorReady,
							Status: "True",
						},
						{
							Type:   apis.ConditionReady,
							Status: "True",
						},
						{
							Type:     v1beta1.Stopped,
							Status:   "False",
							Severity: apis.ConditionSeverityInfo,
						},
					},
				},
				URL: &apis.URL{
					Scheme: "http",
					Host:   fmt.Sprintf("%s.%s.%s", serviceName, serviceKey.Namespace, domain),
				},
				Address: &duckv1.Addressable{
					URL: &apis.URL{
						Scheme: "http",
						Host:   fmt.Sprintf("%s-predictor.%s.svc.cluster.local", serviceKey.Name, serviceKey.Namespace),
					},
				},
				Components: map[v1beta1.ComponentType]v1beta1.ComponentStatusSpec{
					v1beta1.PredictorComponent: {
						LatestCreatedRevision: "",
						// URL: &apis.URL{
						//	Scheme: "http",
						//	Host:   fmt.Sprintf("%s-predictor.%s.%s", serviceName, serviceKey.Namespace, domain),
						// },
					},
				},
				ModelStatus: v1beta1.ModelStatus{
					TransitionStatus:    "InProgress",
					ModelRevisionStates: &v1beta1.ModelRevisionStates{TargetModelState: "Pending"},
				},
				DeploymentMode: string(constants.RawDeployment),
			}
			Eventually(func() string {
				isvc := &v1beta1.InferenceService{}
				if err := k8sClient.Get(context.TODO(), serviceKey, isvc); err != nil {
					return err.Error()
				}
				return cmp.Diff(&expectedIsvcStatus, &isvc.Status, cmpopts.IgnoreTypes(apis.VolatileTime{}))
			}, timeout).Should(BeEmpty())

			// check HPA
			var minReplicas int32 = 1
			var maxReplicas int32 = 3
			var cpuUtilization int32 = 75
			var stabilizationWindowSeconds int32 = 0
			selectPolicy := autoscalingv2.MaxChangePolicySelect
			actualHPA := &autoscalingv2.HorizontalPodAutoscaler{}
			predictorHPAKey := types.NamespacedName{
				Name:      predictorServiceKey.Name,
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorHPAKey, actualHPA) }, timeout).
				Should(Succeed())
			expectedHPA := &autoscalingv2.HorizontalPodAutoscaler{
				Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
					ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       predictorServiceKey.Name,
					},
					MinReplicas: &minReplicas,
					MaxReplicas: maxReplicas,
					Metrics: []autoscalingv2.MetricSpec{
						{
							Type: autoscalingv2.ResourceMetricSourceType,
							Resource: &autoscalingv2.ResourceMetricSource{
								Name: corev1.ResourceCPU,
								Target: autoscalingv2.MetricTarget{
									Type:               "Utilization",
									AverageUtilization: &cpuUtilization,
								},
							},
						},
					},
					Behavior: &autoscalingv2.HorizontalPodAutoscalerBehavior{
						ScaleUp: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: &stabilizationWindowSeconds,
							SelectPolicy:               &selectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Pods",
									Value:         4,
									PeriodSeconds: 15,
								},
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
						ScaleDown: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: nil,
							SelectPolicy:               &selectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
					},
				},
			}
			Expect(actualHPA.Spec).To(BeComparableTo(expectedHPA.Spec))
		})
	})
	Context("When creating inference service with raw kube predictor and transformer", func() {
		configs := map[string]string{
			"explainers": `{
				"alibi": {
					"image": "kserve/alibi-explainer",
					"defaultImageVersion": "latest"
				}
			}`,
			"ingress": `{
				"enableGatewayApi": true,
				"kserveIngressGateway": "kserve/kserve-ingress-gateway",
				"ingressGateway": "knative-serving/knative-ingress-gateway",
				"localGateway": "knative-serving/knative-local-gateway",
				"localGatewayService": "knative-local-gateway.istio-system.svc.cluster.local",
				"additionalIngressDomains": ["additional.example.com"]
			}`,
			"storageInitializer": `{
				"image" : "kserve/storage-initializer:latest",
				"memoryRequest": "100Mi",
				"memoryLimit": "1Gi",
				"cpuRequest": "100m",
				"cpuLimit": "1",
				"CaBundleConfigMapName": "",
				"caBundleVolumeMountPath": "/etc/ssl/custom-certs",
				"enableDirectPvcVolumeMount": false
			}`,
		}

		It("Should have httproute/service/deployment/hpa created for transformer and predictor", func() {
			By("By creating a new InferenceService")
			// Create configmap
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.InferenceServiceConfigMapName,
					Namespace: constants.KServeNamespace,
				},
				Data: configs,
			}
			Expect(k8sClient.Create(context.TODO(), configMap)).NotTo(HaveOccurred())
			defer k8sClient.Delete(context.TODO(), configMap)
			// Create ServingRuntime
			servingRuntime := &v1alpha1.ServingRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "tf-serving-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.ServingRuntimeSpec{
					SupportedModelFormats: []v1alpha1.SupportedModelFormat{
						{
							Name:       "tensorflow",
							Version:    proto.String("1"),
							AutoSelect: proto.Bool(true),
						},
					},
					ServingRuntimePodSpec: v1alpha1.ServingRuntimePodSpec{
						Containers: []corev1.Container{
							{
								Name:    "kserve-container",
								Image:   "tensorflow/serving:1.14.0",
								Command: []string{"/usr/bin/tensorflow_model_server"},
								Args: []string{
									"--port=9000",
									"--rest_api_port=8080",
									"--model_base_path=/mnt/models",
									"--rest_api_timeout_in_ms=60000",
								},
								Resources: defaultResource,
							},
						},
					},
					Disabled: proto.Bool(false),
				},
			}
			k8sClient.Create(context.TODO(), servingRuntime)
			defer k8sClient.Delete(context.TODO(), servingRuntime)
			serviceName := "raw-foo-trans"
			namespace := "default"
			expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: namespace}}
			serviceKey := expectedRequest.NamespacedName
			predictorServiceKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceName),
				Namespace: namespace,
			}
			transformerServiceKey := types.NamespacedName{
				Name:      constants.TransformerServiceName(serviceName),
				Namespace: namespace,
			}
			storageUri := "s3://test/mnist/export"
			var minReplicas int32 = 1
			var maxReplicas int32 = 3
			var transformerMinReplicas int32 = 1
			var transformerMaxReplicas int32 = 2
			var transformerCpuUtilization int32 = 80
			var transformerStabilizationWindowSeconds int32 = 0
			transformerSelectPolicy := autoscalingv2.MaxChangePolicySelect
			ctx := context.Background()
			isvc := &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: string(constants.AutoscalerClassHPA),
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
							MinReplicas: ptr.To(minReplicas),
							MaxReplicas: maxReplicas,
							AutoScaling: &v1beta1.AutoScalingSpec{
								Metrics: []v1beta1.MetricsSpec{
									{
										Type: v1beta1.ResourceMetricSourceType,
										Resource: &v1beta1.ResourceMetricSource{
											Name: v1beta1.ResourceMetricCPU,
											Target: v1beta1.MetricTarget{
												Type:               v1beta1.UtilizationMetricType,
												AverageUtilization: ptr.To(int32(75)),
											},
										},
									},
								},
							},
						},
						Tensorflow: &v1beta1.TFServingSpec{
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI:     &storageUri,
								RuntimeVersion: proto.String("1.14.0"),
								Container: corev1.Container{
									Name:      constants.InferenceServiceContainerName,
									Resources: defaultResource,
								},
							},
						},
					},
					Transformer: &v1beta1.TransformerSpec{
						ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
							MinReplicas:    ptr.To(transformerMinReplicas),
							MaxReplicas:    transformerMaxReplicas,
							ScaleTarget:    ptr.To(transformerCpuUtilization),
							TimeoutSeconds: ptr.To(int64(30)),
						},
						PodSpec: v1beta1.PodSpec{
							Containers: []corev1.Container{
								{
									Image:     "transformer:v1",
									Resources: defaultResource,
									Args: []string{
										"--port=8080",
									},
								},
							},
						},
					},
				},
			}
			isvc.DefaultInferenceService(nil, nil, &v1beta1.SecurityConfig{AutoMountServiceAccountToken: false}, nil)
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			defer k8sClient.Delete(ctx, isvc)

			inferenceService := &v1beta1.InferenceService{}

			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, inferenceService)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			// check predictor deployment
			actualPredictorDeployment := &appsv1.Deployment{}
			predictorDeploymentKey := types.NamespacedName{
				Name:      predictorServiceKey.Name,
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorDeploymentKey, actualPredictorDeployment) }, timeout).
				Should(Succeed())
			var replicas int32 = 1
			var revisionHistory int32 = 10
			var progressDeadlineSeconds int32 = 600
			var gracePeriod int64 = 30
			expectedPredictorDeployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorDeploymentKey.Name,
					Namespace: predictorDeploymentKey.Namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "isvc." + predictorDeploymentKey.Name,
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Name:      predictorDeploymentKey.Name,
							Namespace: "default",
							Labels: map[string]string{
								"app":                                 "isvc." + predictorDeploymentKey.Name,
								constants.KServiceComponentLabel:      constants.Predictor.String(),
								constants.InferenceServicePodLabelKey: serviceName,
							},
							Annotations: map[string]string{
								constants.StorageInitializerSourceUriInternalAnnotationKey: *isvc.Spec.Predictor.Model.StorageURI,
								constants.DeploymentMode:                                   string(constants.RawDeployment),
								constants.AutoscalerClass:                                  string(constants.AutoscalerClassHPA),
								constants.OpenshiftServingCertAnnotation:                   predictorDeploymentKey.Name + constants.ServingCertSecretSuffix,
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Image: "tensorflow/serving:" +
										*isvc.Spec.Predictor.Model.RuntimeVersion,
									Name:    constants.InferenceServiceContainerName,
									Command: []string{v1beta1.TensorflowEntrypointCommand},
									Args: []string{
										"--port=" + v1beta1.TensorflowServingGRPCPort,
										"--rest_api_port=" + v1beta1.TensorflowServingRestPort,
										"--model_base_path=" + constants.DefaultModelLocalMountPath,
										"--rest_api_timeout_in_ms=60000",
									},
									Resources: defaultResource,
									ReadinessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											TCPSocket: &corev1.TCPSocketAction{
												Port: intstr.IntOrString{
													IntVal: 8080,
												},
											},
										},
										InitialDelaySeconds: 0,
										TimeoutSeconds:      1,
										PeriodSeconds:       10,
										SuccessThreshold:    1,
										FailureThreshold:    3,
									},
									TerminationMessagePath:   "/dev/termination-log",
									TerminationMessagePolicy: "File",
									ImagePullPolicy:          "IfNotPresent",
								},
							},
							SchedulerName:                 "default-scheduler",
							RestartPolicy:                 "Always",
							TerminationGracePeriodSeconds: &gracePeriod,
							DNSPolicy:                     "ClusterFirst",
							SecurityContext: &corev1.PodSecurityContext{
								SELinuxOptions:      nil,
								WindowsOptions:      nil,
								RunAsUser:           nil,
								RunAsGroup:          nil,
								RunAsNonRoot:        nil,
								SupplementalGroups:  nil,
								FSGroup:             nil,
								Sysctls:             nil,
								FSGroupChangePolicy: nil,
								SeccompProfile:      nil,
							},
							AutomountServiceAccountToken: proto.Bool(false),
						},
					},
					Strategy: appsv1.DeploymentStrategy{
						Type: "RollingUpdate",
						RollingUpdate: &appsv1.RollingUpdateDeployment{
							MaxUnavailable: &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
							MaxSurge:       &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
						},
					},
					RevisionHistoryLimit:    &revisionHistory,
					ProgressDeadlineSeconds: &progressDeadlineSeconds,
				},
			}
			Expect(actualPredictorDeployment.Spec).To(BeComparableTo(expectedPredictorDeployment.Spec))

			// check transformer deployment
			actualTransformerDeployment := &appsv1.Deployment{}
			transformerDeploymentKey := types.NamespacedName{
				Name:      transformerServiceKey.Name,
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), transformerDeploymentKey, actualTransformerDeployment)
			}, timeout).
				Should(Succeed())
			expectedTransformerDeployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      transformerDeploymentKey.Name,
					Namespace: transformerDeploymentKey.Namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "isvc." + transformerDeploymentKey.Name,
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Name:      transformerDeploymentKey.Name,
							Namespace: "default",
							Labels: map[string]string{
								"app":                                 "isvc." + transformerDeploymentKey.Name,
								constants.KServiceComponentLabel:      constants.Transformer.String(),
								constants.InferenceServicePodLabelKey: serviceName,
							},
							Annotations: map[string]string{
								constants.DeploymentMode:                 string(constants.RawDeployment),
								constants.AutoscalerClass:                string(constants.AutoscalerClassHPA),
								constants.OpenshiftServingCertAnnotation: transformerDeploymentKey.Name + constants.ServingCertSecretSuffix,
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Image: "transformer:v1",
									Name:  constants.InferenceServiceContainerName,
									Args: []string{
										"--port=8080",
										"--model_name",
										serviceKey.Name,
										"--predictor_host",
										fmt.Sprintf("%s.%s", predictorServiceKey.Name, predictorServiceKey.Namespace),
										"--http_port",
										"8080",
									},
									Resources: defaultResource,
									ReadinessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											TCPSocket: &corev1.TCPSocketAction{
												Port: intstr.IntOrString{
													IntVal: 8080,
												},
											},
										},
										InitialDelaySeconds: 0,
										TimeoutSeconds:      1,
										PeriodSeconds:       10,
										SuccessThreshold:    1,
										FailureThreshold:    3,
									},
									TerminationMessagePath:   "/dev/termination-log",
									TerminationMessagePolicy: "File",
									ImagePullPolicy:          "IfNotPresent",
								},
							},
							SchedulerName:                 "default-scheduler",
							RestartPolicy:                 "Always",
							TerminationGracePeriodSeconds: &gracePeriod,
							DNSPolicy:                     "ClusterFirst",
							SecurityContext: &corev1.PodSecurityContext{
								SELinuxOptions:      nil,
								WindowsOptions:      nil,
								RunAsUser:           nil,
								RunAsGroup:          nil,
								RunAsNonRoot:        nil,
								SupplementalGroups:  nil,
								FSGroup:             nil,
								Sysctls:             nil,
								FSGroupChangePolicy: nil,
								SeccompProfile:      nil,
							},
							AutomountServiceAccountToken: proto.Bool(false),
						},
					},
					Strategy: appsv1.DeploymentStrategy{
						Type: "RollingUpdate",
						RollingUpdate: &appsv1.RollingUpdateDeployment{
							MaxUnavailable: &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
							MaxSurge:       &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
						},
					},
					RevisionHistoryLimit:    &revisionHistory,
					ProgressDeadlineSeconds: &progressDeadlineSeconds,
				},
			}
			Expect(actualTransformerDeployment.Spec).To(BeComparableTo(expectedTransformerDeployment.Spec))

			// check predictor service
			actualPredictorService := &corev1.Service{}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorServiceKey, actualPredictorService) }, timeout).
				Should(Succeed())
			expectedPredictorService := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorServiceKey.Name,
					Namespace: predictorServiceKey.Namespace,
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       predictorServiceKey.Name,
							Protocol:   "TCP",
							Port:       80,
							TargetPort: intstr.IntOrString{Type: 0, IntVal: 8080, StrVal: ""},
						},
					},
					Type:            "ClusterIP",
					SessionAffinity: "None",
					Selector: map[string]string{
						"app": "isvc." + predictorServiceKey.Name,
					},
				},
			}
			actualPredictorService.Spec.ClusterIP = ""
			actualPredictorService.Spec.ClusterIPs = nil
			actualPredictorService.Spec.IPFamilies = nil
			actualPredictorService.Spec.IPFamilyPolicy = nil
			actualPredictorService.Spec.InternalTrafficPolicy = nil
			Expect(actualPredictorService.Spec).To(BeComparableTo(expectedPredictorService.Spec))

			// check transformer service
			actualTransformerService := &corev1.Service{}
			Eventually(func() error { return k8sClient.Get(context.TODO(), transformerServiceKey, actualTransformerService) }, timeout).
				Should(Succeed())
			expectedTransformerService := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      transformerServiceKey.Name,
					Namespace: transformerServiceKey.Namespace,
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       transformerServiceKey.Name,
							Protocol:   "TCP",
							Port:       80,
							TargetPort: intstr.IntOrString{Type: 0, IntVal: 8080, StrVal: ""},
						},
					},
					Type:            "ClusterIP",
					SessionAffinity: "None",
					Selector: map[string]string{
						"app": "isvc." + transformerServiceKey.Name,
					},
				},
			}
			actualTransformerService.Spec.ClusterIP = ""
			actualTransformerService.Spec.ClusterIPs = nil
			actualTransformerService.Spec.IPFamilies = nil
			actualTransformerService.Spec.IPFamilyPolicy = nil
			actualTransformerService.Spec.InternalTrafficPolicy = nil
			Expect(actualTransformerService.Spec).To(BeComparableTo(expectedTransformerService.Spec))

			// update deployment status to make isvc ready
			updatedPredictorDeployment := actualPredictorDeployment.DeepCopy()
			updatedPredictorDeployment.Status.Conditions = []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: corev1.ConditionTrue,
				},
			}
			Expect(k8sClient.Status().Update(context.TODO(), updatedPredictorDeployment)).NotTo(HaveOccurred())
			updatedTransformerDeployment := actualTransformerDeployment.DeepCopy()
			updatedTransformerDeployment.Status.Conditions = []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: corev1.ConditionTrue,
				},
			}
			Expect(k8sClient.Status().Update(context.TODO(), updatedTransformerDeployment)).NotTo(HaveOccurred())

			// check http route
			actualToplevelHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualToplevelHttpRoute)
			}, timeout).
				Should(Succeed())
			topLevelHost := fmt.Sprintf("%s-%s.%s", serviceKey.Name, serviceKey.Namespace, "example.com")
			expectedToplevelHttpRoute := gatewayapiv1.HTTPRoute{
				Spec: gatewayapiv1.HTTPRouteSpec{
					Hostnames: []gatewayapiv1.Hostname{gatewayapiv1.Hostname(topLevelHost), gatewayapiv1.Hostname(fmt.Sprintf("%s-%s.additional.example.com", serviceKey.Name, serviceKey.Namespace))},
					Rules: []gatewayapiv1.HTTPRouteRule{
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.FallbackPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(transformerServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("30s")),
							},
						},
					},
					CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
						ParentRefs: []gatewayapiv1.ParentReference{
							{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
						},
					},
				},
			}
			Expect(actualToplevelHttpRoute.Spec).To(BeComparableTo(expectedToplevelHttpRoute.Spec))

			actualPredictorHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      predictorServiceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualPredictorHttpRoute)
			}, timeout).
				Should(Succeed())
			predictorHost := fmt.Sprintf("%s-%s.%s", predictorServiceKey.Name, serviceKey.Namespace, "example.com")
			expectedPredictorHttpRoute := gatewayapiv1.HTTPRoute{
				Spec: gatewayapiv1.HTTPRouteSpec{
					Hostnames: []gatewayapiv1.Hostname{gatewayapiv1.Hostname(predictorHost)},
					Rules: []gatewayapiv1.HTTPRouteRule{
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.FallbackPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(predictorServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("60s")),
							},
						},
					},
					CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
						ParentRefs: []gatewayapiv1.ParentReference{
							{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
						},
					},
				},
			}
			Expect(actualPredictorHttpRoute.Spec).To(BeComparableTo(expectedPredictorHttpRoute.Spec))

			actualTransformerHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      transformerServiceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualTransformerHttpRoute)
			}, timeout).
				Should(Succeed())
			transformerHost := fmt.Sprintf("%s-%s.%s", transformerServiceKey.Name, serviceKey.Namespace, "example.com")
			expectedTransformerHttpRoute := gatewayapiv1.HTTPRoute{
				Spec: gatewayapiv1.HTTPRouteSpec{
					Hostnames: []gatewayapiv1.Hostname{gatewayapiv1.Hostname(transformerHost)},
					Rules: []gatewayapiv1.HTTPRouteRule{
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.FallbackPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(transformerServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("30s")),
							},
						},
					},
					CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
						ParentRefs: []gatewayapiv1.ParentReference{
							{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
						},
					},
				},
			}
			Expect(actualTransformerHttpRoute.Spec).To(BeComparableTo(expectedTransformerHttpRoute.Spec))

			// Mark the Ingress as accepted to make isvc ready
			httpRouteStatus := gatewayapiv1.HTTPRouteStatus{
				RouteStatus: gatewayapiv1.RouteStatus{
					Parents: []gatewayapiv1.RouteParentStatus{
						{
							ParentRef: gatewayapiv1.ParentReference{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
							ControllerName: "istio.io/gateway-controller",
							Conditions: []metav1.Condition{
								{
									Type:               string(gatewayapiv1.ListenerConditionAccepted),
									Status:             metav1.ConditionTrue,
									Reason:             "Accepted",
									Message:            "Route was valid",
									LastTransitionTime: metav1.Now(),
								},
							},
						},
					},
				},
			}
			actualPredictorHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(context.Background(), actualPredictorHttpRoute)).NotTo(HaveOccurred())
			actualTransformerHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(context.Background(), actualTransformerHttpRoute)).NotTo(HaveOccurred())
			actualToplevelHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(context.Background(), actualToplevelHttpRoute)).NotTo(HaveOccurred())

			// verify if InferenceService status is updated
			expectedIsvcStatus := v1beta1.InferenceServiceStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:   v1beta1.IngressReady,
							Status: "True",
						},
						{
							Type:   v1beta1.PredictorReady,
							Status: "True",
						},
						{
							Type:   apis.ConditionReady,
							Status: "True",
						},
						{
							Type:     v1beta1.Stopped,
							Status:   "False",
							Severity: apis.ConditionSeverityInfo,
						},
						{
							Type:     v1beta1.TransformerReady,
							Status:   "True",
							Severity: "Info",
						},
					},
				},
				URL: &apis.URL{
					Scheme: "http",
					Host:   fmt.Sprintf("%s-%s.example.com", serviceKey.Name, serviceKey.Namespace),
				},
				Address: &duckv1.Addressable{
					URL: &apis.URL{
						Scheme: "http",
						Host:   fmt.Sprintf("%s.%s.svc.cluster.local", transformerServiceKey.Name, serviceKey.Namespace),
					},
				},
				Components: map[v1beta1.ComponentType]v1beta1.ComponentStatusSpec{
					v1beta1.PredictorComponent: {
						LatestCreatedRevision: "",
						// URL: &apis.URL{
						//	Scheme: "http",
						//	Host:   fmt.Sprintf("%s-%s.example.com", predictorServiceKey.Name, serviceKey.Namespace),
						// },
					},
					v1beta1.TransformerComponent: {
						LatestCreatedRevision: "",
						// URL: &apis.URL{
						// 	Scheme: "http",
						// 	Host:   fmt.Sprintf("%s-%s.example.com", transformerServiceKey.Name, serviceKey.Namespace),
						// },
					},
				},
				ModelStatus: v1beta1.ModelStatus{
					TransitionStatus:    "InProgress",
					ModelRevisionStates: &v1beta1.ModelRevisionStates{TargetModelState: "Pending"},
				},
				DeploymentMode: string(constants.RawDeployment),
			}
			Eventually(func() string {
				isvc := &v1beta1.InferenceService{}
				if err := k8sClient.Get(context.TODO(), serviceKey, isvc); err != nil {
					return err.Error()
				}
				return cmp.Diff(&expectedIsvcStatus, &isvc.Status, cmpopts.IgnoreTypes(apis.VolatileTime{}))
			}, timeout).Should(BeEmpty())

			// check predictor HPA
			var cpuUtilization int32 = 75
			var stabilizationWindowSeconds int32 = 0
			selectPolicy := autoscalingv2.MaxChangePolicySelect
			actualPredictorHPA := &autoscalingv2.HorizontalPodAutoscaler{}
			predictorHPAKey := types.NamespacedName{
				Name:      predictorServiceKey.Name,
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorHPAKey, actualPredictorHPA) }, timeout).
				Should(Succeed())
			expectedPredictorHPA := &autoscalingv2.HorizontalPodAutoscaler{
				Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
					ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       predictorServiceKey.Name,
					},
					MinReplicas: &minReplicas,
					MaxReplicas: maxReplicas,
					Metrics: []autoscalingv2.MetricSpec{
						{
							Type: autoscalingv2.ResourceMetricSourceType,
							Resource: &autoscalingv2.ResourceMetricSource{
								Name: corev1.ResourceCPU,
								Target: autoscalingv2.MetricTarget{
									Type:               "Utilization",
									AverageUtilization: &cpuUtilization,
								},
							},
						},
					},
					Behavior: &autoscalingv2.HorizontalPodAutoscalerBehavior{
						ScaleUp: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: &stabilizationWindowSeconds,
							SelectPolicy:               &selectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Pods",
									Value:         4,
									PeriodSeconds: 15,
								},
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
						ScaleDown: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: nil,
							SelectPolicy:               &selectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
					},
				},
			}
			Expect(actualPredictorHPA.Spec).To(BeComparableTo(expectedPredictorHPA.Spec))

			// check transformer HPA
			actualTransformerHPA := &autoscalingv2.HorizontalPodAutoscaler{}
			transformerHPAKey := types.NamespacedName{
				Name:      transformerServiceKey.Name,
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), transformerHPAKey, actualTransformerHPA) }, timeout).
				Should(Succeed())
			expectedTransformerHPA := &autoscalingv2.HorizontalPodAutoscaler{
				Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
					ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       transformerServiceKey.Name,
					},
					MinReplicas: &transformerMinReplicas,
					MaxReplicas: transformerMaxReplicas,
					Metrics: []autoscalingv2.MetricSpec{
						{
							Type: autoscalingv2.ResourceMetricSourceType,
							Resource: &autoscalingv2.ResourceMetricSource{
								Name: corev1.ResourceCPU,
								Target: autoscalingv2.MetricTarget{
									Type:               "Utilization",
									AverageUtilization: &transformerCpuUtilization,
								},
							},
						},
					},
					Behavior: &autoscalingv2.HorizontalPodAutoscalerBehavior{
						ScaleUp: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: &transformerStabilizationWindowSeconds,
							SelectPolicy:               &transformerSelectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Pods",
									Value:         4,
									PeriodSeconds: 15,
								},
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
						ScaleDown: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: nil,
							SelectPolicy:               &transformerSelectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
					},
				},
			}
			Expect(actualTransformerHPA.Spec).To(BeComparableTo(expectedTransformerHPA.Spec))
		})
	})
	Context("When creating inference service with raw kube predictor and explainer", func() {
		configs := map[string]string{
			"explainers": `{
				"art": {
					"image": "kserve/art-explainer",
					"defaultImageVersion": "latest"
				}
			}`,
			"ingress": `{
				"enableGatewayApi": true,
				"kserveIngressGateway": "kserve/kserve-ingress-gateway",
				"ingressGateway": "knative-serving/knative-ingress-gateway",
				"localGateway": "knative-serving/knative-local-gateway",
				"localGatewayService": "knative-local-gateway.istio-system.svc.cluster.local",
				"additionalIngressDomains": ["additional.example.com"]
			}`,
			"storageInitializer": `{
				"image" : "kserve/storage-initializer:latest",
				"memoryRequest": "100Mi",
				"memoryLimit": "1Gi",
				"cpuRequest": "100m",
				"cpuLimit": "1",
				"CaBundleConfigMapName": "",
				"caBundleVolumeMountPath": "/etc/ssl/custom-certs",
				"enableDirectPvcVolumeMount": false
			}`,
		}

		It("Should have httproute/service/deployment/hpa created for explainer and predictor", func() {
			By("By creating a new InferenceService")
			// Create configmap
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.InferenceServiceConfigMapName,
					Namespace: constants.KServeNamespace,
				},
				Data: configs,
			}
			Expect(k8sClient.Create(context.TODO(), configMap)).NotTo(HaveOccurred())
			defer k8sClient.Delete(context.TODO(), configMap)
			// Create ServingRuntime
			servingRuntime := &v1alpha1.ServingRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "tf-serving-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.ServingRuntimeSpec{
					SupportedModelFormats: []v1alpha1.SupportedModelFormat{
						{
							Name:       "tensorflow",
							Version:    proto.String("1"),
							AutoSelect: proto.Bool(true),
						},
					},
					ServingRuntimePodSpec: v1alpha1.ServingRuntimePodSpec{
						Containers: []corev1.Container{
							{
								Name:    "kserve-container",
								Image:   "tensorflow/serving:1.14.0",
								Command: []string{"/usr/bin/tensorflow_model_server"},
								Args: []string{
									"--port=9000",
									"--rest_api_port=8080",
									"--model_base_path=/mnt/models",
									"--rest_api_timeout_in_ms=60000",
								},
								Resources: defaultResource,
							},
						},
					},
					Disabled: proto.Bool(false),
				},
			}
			k8sClient.Create(context.TODO(), servingRuntime)
			defer k8sClient.Delete(context.TODO(), servingRuntime)
			serviceName := "raw-foo-exp"
			namespace := "default"
			expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: namespace}}
			serviceKey := expectedRequest.NamespacedName
			predictorServiceKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceName),
				Namespace: namespace,
			}
			explainerServiceKey := types.NamespacedName{
				Name:      constants.ExplainerServiceName(serviceName),
				Namespace: namespace,
			}
			storageUri := "s3://test/mnist/export"
			var minReplicas int32 = 1
			var maxReplicas int32 = 3
			var explainerMinReplicas int32 = 1
			var explainerMaxReplicas int32 = 2
			var explainerCpuUtilization int32 = 80
			var explainerStabilizationWindowSeconds int32 = 0
			ExplainerSelectPolicy := autoscalingv2.MaxChangePolicySelect
			ctx := context.Background()
			isvc := &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: string(constants.AutoscalerClassHPA),
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
							MinReplicas: ptr.To(minReplicas),
							MaxReplicas: maxReplicas,
							AutoScaling: &v1beta1.AutoScalingSpec{
								Metrics: []v1beta1.MetricsSpec{
									{
										Type: v1beta1.ResourceMetricSourceType,
										Resource: &v1beta1.ResourceMetricSource{
											Name: v1beta1.ResourceMetricCPU,
											Target: v1beta1.MetricTarget{
												Type:               v1beta1.UtilizationMetricType,
												AverageUtilization: ptr.To(int32(75)),
											},
										},
									},
								},
							},
						},
						Tensorflow: &v1beta1.TFServingSpec{
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI:     &storageUri,
								RuntimeVersion: proto.String("1.14.0"),
								Container: corev1.Container{
									Name:      constants.InferenceServiceContainerName,
									Resources: defaultResource,
								},
							},
						},
					},
					Explainer: &v1beta1.ExplainerSpec{
						ART: &v1beta1.ARTExplainerSpec{
							Type: v1beta1.ARTSquareAttackExplainer,
							ExplainerExtensionSpec: v1beta1.ExplainerExtensionSpec{
								Config: map[string]string{"nb_classes": "10"},
								Container: corev1.Container{
									Name:      constants.InferenceServiceContainerName,
									Resources: defaultResource,
								},
							},
						},
						ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
							MinReplicas:    ptr.To(explainerMinReplicas),
							MaxReplicas:    explainerMaxReplicas,
							ScaleTarget:    ptr.To(explainerCpuUtilization),
							TimeoutSeconds: ptr.To(int64(30)),
						},
					},
				},
			}
			isvcConfigMap, err := v1beta1.GetInferenceServiceConfigMap(context.Background(), clientset)
			Expect(err).NotTo(HaveOccurred())
			isvcConfig, err := v1beta1.NewInferenceServicesConfig(isvcConfigMap)
			Expect(err).NotTo(HaveOccurred())
			isvc.DefaultInferenceService(isvcConfig, nil, &v1beta1.SecurityConfig{AutoMountServiceAccountToken: false}, nil)
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			defer k8sClient.Delete(ctx, isvc)

			inferenceService := &v1beta1.InferenceService{}

			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, inferenceService)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			// check predictor deployment
			actualPredictorDeployment := &appsv1.Deployment{}
			predictorDeploymentKey := types.NamespacedName{
				Name:      predictorServiceKey.Name,
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorDeploymentKey, actualPredictorDeployment) }, timeout).
				Should(Succeed())
			var replicas int32 = 1
			var revisionHistory int32 = 10
			var progressDeadlineSeconds int32 = 600
			var gracePeriod int64 = 30
			expectedPredictorDeployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorDeploymentKey.Name,
					Namespace: predictorDeploymentKey.Namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "isvc." + predictorDeploymentKey.Name,
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Name:      predictorDeploymentKey.Name,
							Namespace: "default",
							Labels: map[string]string{
								"app":                                 "isvc." + predictorDeploymentKey.Name,
								constants.KServiceComponentLabel:      constants.Predictor.String(),
								constants.InferenceServicePodLabelKey: serviceName,
							},
							Annotations: map[string]string{
								constants.StorageInitializerSourceUriInternalAnnotationKey: *isvc.Spec.Predictor.Model.StorageURI,
								constants.DeploymentMode:                                   string(constants.RawDeployment),
								constants.AutoscalerClass:                                  string(constants.AutoscalerClassHPA),
								constants.OpenshiftServingCertAnnotation:                   predictorDeploymentKey.Name + constants.ServingCertSecretSuffix,
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Image: "tensorflow/serving:" +
										*isvc.Spec.Predictor.Model.RuntimeVersion,
									Name:    constants.InferenceServiceContainerName,
									Command: []string{v1beta1.TensorflowEntrypointCommand},
									Args: []string{
										"--port=" + v1beta1.TensorflowServingGRPCPort,
										"--rest_api_port=" + v1beta1.TensorflowServingRestPort,
										"--model_base_path=" + constants.DefaultModelLocalMountPath,
										"--rest_api_timeout_in_ms=60000",
									},
									Resources: defaultResource,
									ReadinessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											TCPSocket: &corev1.TCPSocketAction{
												Port: intstr.IntOrString{
													IntVal: 8080,
												},
											},
										},
										InitialDelaySeconds: 0,
										TimeoutSeconds:      1,
										PeriodSeconds:       10,
										SuccessThreshold:    1,
										FailureThreshold:    3,
									},
									TerminationMessagePath:   "/dev/termination-log",
									TerminationMessagePolicy: "File",
									ImagePullPolicy:          "IfNotPresent",
								},
							},
							SchedulerName:                 "default-scheduler",
							RestartPolicy:                 "Always",
							TerminationGracePeriodSeconds: &gracePeriod,
							DNSPolicy:                     "ClusterFirst",
							SecurityContext: &corev1.PodSecurityContext{
								SELinuxOptions:      nil,
								WindowsOptions:      nil,
								RunAsUser:           nil,
								RunAsGroup:          nil,
								RunAsNonRoot:        nil,
								SupplementalGroups:  nil,
								FSGroup:             nil,
								Sysctls:             nil,
								FSGroupChangePolicy: nil,
								SeccompProfile:      nil,
							},
							AutomountServiceAccountToken: proto.Bool(false),
						},
					},
					Strategy: appsv1.DeploymentStrategy{
						Type: "RollingUpdate",
						RollingUpdate: &appsv1.RollingUpdateDeployment{
							MaxUnavailable: &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
							MaxSurge:       &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
						},
					},
					RevisionHistoryLimit:    &revisionHistory,
					ProgressDeadlineSeconds: &progressDeadlineSeconds,
				},
			}
			Expect(actualPredictorDeployment.Spec).To(BeComparableTo(expectedPredictorDeployment.Spec))

			// check Explainer deployment
			actualExplainerDeployment := &appsv1.Deployment{}
			explainerDeploymentKey := types.NamespacedName{
				Name:      explainerServiceKey.Name,
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), explainerDeploymentKey, actualExplainerDeployment)
			}, timeout).
				Should(Succeed())
			expectedExplainerDeployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      explainerDeploymentKey.Name,
					Namespace: explainerDeploymentKey.Namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "isvc." + explainerDeploymentKey.Name,
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Name:      explainerDeploymentKey.Name,
							Namespace: "default",
							Labels: map[string]string{
								"app":                                 "isvc." + explainerDeploymentKey.Name,
								constants.KServiceComponentLabel:      constants.Explainer.String(),
								constants.InferenceServicePodLabelKey: serviceName,
							},
							Annotations: map[string]string{
								constants.DeploymentMode:                 string(constants.RawDeployment),
								constants.AutoscalerClass:                string(constants.AutoscalerClassHPA),
								constants.OpenshiftServingCertAnnotation: explainerDeploymentKey.Name + constants.ServingCertSecretSuffix,
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Image: "kserve/art-explainer:latest",
									Name:  constants.InferenceServiceContainerName,
									Args: []string{
										"--model_name",
										serviceKey.Name,
										"--http_port",
										"8080",
										"--predictor_host",
										fmt.Sprintf("%s.%s", predictorServiceKey.Name, predictorServiceKey.Namespace),
										"--adversary_type",
										"SquareAttack",
										"--nb_classes",
										"10",
									},
									Resources: defaultResource,
									ReadinessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											TCPSocket: &corev1.TCPSocketAction{
												Port: intstr.IntOrString{
													IntVal: 8080,
												},
											},
										},
										InitialDelaySeconds: 0,
										TimeoutSeconds:      1,
										PeriodSeconds:       10,
										SuccessThreshold:    1,
										FailureThreshold:    3,
									},
									TerminationMessagePath:   "/dev/termination-log",
									TerminationMessagePolicy: "File",
									ImagePullPolicy:          "IfNotPresent",
								},
							},
							SchedulerName:                 "default-scheduler",
							RestartPolicy:                 "Always",
							TerminationGracePeriodSeconds: &gracePeriod,
							DNSPolicy:                     "ClusterFirst",
							SecurityContext: &corev1.PodSecurityContext{
								SELinuxOptions:      nil,
								WindowsOptions:      nil,
								RunAsUser:           nil,
								RunAsGroup:          nil,
								RunAsNonRoot:        nil,
								SupplementalGroups:  nil,
								FSGroup:             nil,
								Sysctls:             nil,
								FSGroupChangePolicy: nil,
								SeccompProfile:      nil,
							},
							AutomountServiceAccountToken: proto.Bool(false),
						},
					},
					Strategy: appsv1.DeploymentStrategy{
						Type: "RollingUpdate",
						RollingUpdate: &appsv1.RollingUpdateDeployment{
							MaxUnavailable: &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
							MaxSurge:       &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
						},
					},
					RevisionHistoryLimit:    &revisionHistory,
					ProgressDeadlineSeconds: &progressDeadlineSeconds,
				},
			}
			Expect(actualExplainerDeployment.Spec).To(BeComparableTo(expectedExplainerDeployment.Spec))

			// check predictor service
			actualPredictorService := &corev1.Service{}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorServiceKey, actualPredictorService) }, timeout).
				Should(Succeed())
			expectedPredictorService := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorServiceKey.Name,
					Namespace: predictorServiceKey.Namespace,
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       predictorServiceKey.Name,
							Protocol:   "TCP",
							Port:       80,
							TargetPort: intstr.IntOrString{Type: 0, IntVal: 8080, StrVal: ""},
						},
					},
					Type:            "ClusterIP",
					SessionAffinity: "None",
					Selector: map[string]string{
						"app": "isvc." + predictorServiceKey.Name,
					},
				},
			}
			actualPredictorService.Spec.ClusterIP = ""
			actualPredictorService.Spec.ClusterIPs = nil
			actualPredictorService.Spec.IPFamilies = nil
			actualPredictorService.Spec.IPFamilyPolicy = nil
			actualPredictorService.Spec.InternalTrafficPolicy = nil
			Expect(actualPredictorService.Spec).To(BeComparableTo(expectedPredictorService.Spec))

			// check Explainer service
			actualExplainerService := &corev1.Service{}
			Eventually(func() error { return k8sClient.Get(context.TODO(), explainerServiceKey, actualExplainerService) }, timeout).
				Should(Succeed())
			expectedExplainerService := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      explainerServiceKey.Name,
					Namespace: explainerServiceKey.Namespace,
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       explainerServiceKey.Name,
							Protocol:   "TCP",
							Port:       80,
							TargetPort: intstr.IntOrString{Type: 0, IntVal: 8080, StrVal: ""},
						},
					},
					Type:            "ClusterIP",
					SessionAffinity: "None",
					Selector: map[string]string{
						"app": "isvc." + explainerServiceKey.Name,
					},
				},
			}
			actualExplainerService.Spec.ClusterIP = ""
			actualExplainerService.Spec.ClusterIPs = nil
			actualExplainerService.Spec.IPFamilies = nil
			actualExplainerService.Spec.IPFamilyPolicy = nil
			actualExplainerService.Spec.InternalTrafficPolicy = nil
			Expect(actualExplainerService.Spec).To(BeComparableTo(expectedExplainerService.Spec))

			// update deployment status to make isvc ready
			updatedPredictorDeployment := actualPredictorDeployment.DeepCopy()
			updatedPredictorDeployment.Status.Conditions = []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: corev1.ConditionTrue,
				},
			}
			Expect(k8sClient.Status().Update(context.TODO(), updatedPredictorDeployment)).NotTo(HaveOccurred())
			updatedExplainerDeployment := actualExplainerDeployment.DeepCopy()
			updatedExplainerDeployment.Status.Conditions = []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: corev1.ConditionTrue,
				},
			}
			Expect(k8sClient.Status().Update(context.TODO(), updatedExplainerDeployment)).NotTo(HaveOccurred())

			// check http route
			actualToplevelHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualToplevelHttpRoute)
			}, timeout).Should(Succeed())
			topLevelHost := fmt.Sprintf("%s-%s.%s", serviceKey.Name, serviceKey.Namespace, "example.com")
			expectedToplevelHttpRoute := gatewayapiv1.HTTPRoute{
				Spec: gatewayapiv1.HTTPRouteSpec{
					Hostnames: []gatewayapiv1.Hostname{gatewayapiv1.Hostname(topLevelHost), gatewayapiv1.Hostname(fmt.Sprintf("%s-%s.additional.example.com", serviceKey.Name, serviceKey.Namespace))},
					Rules: []gatewayapiv1.HTTPRouteRule{
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.ExplainPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(explainerServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("30s")),
							},
						},
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.FallbackPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(predictorServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("60s")),
							},
						},
					},
					CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
						ParentRefs: []gatewayapiv1.ParentReference{
							{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
						},
					},
				},
			}
			Expect(actualToplevelHttpRoute.Spec).To(BeComparableTo(expectedToplevelHttpRoute.Spec))

			actualPredictorHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      predictorServiceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualPredictorHttpRoute)
			}, timeout).
				Should(Succeed())
			predictorHost := fmt.Sprintf("%s-%s.%s", predictorServiceKey.Name, serviceKey.Namespace, "example.com")
			expectedPredictorHttpRoute := gatewayapiv1.HTTPRoute{
				Spec: gatewayapiv1.HTTPRouteSpec{
					Hostnames: []gatewayapiv1.Hostname{gatewayapiv1.Hostname(predictorHost)},
					Rules: []gatewayapiv1.HTTPRouteRule{
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.FallbackPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(predictorServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("60s")),
							},
						},
					},
					CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
						ParentRefs: []gatewayapiv1.ParentReference{
							{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
						},
					},
				},
			}
			Expect(actualPredictorHttpRoute.Spec).To(BeComparableTo(expectedPredictorHttpRoute.Spec))

			actualExplainerHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      explainerServiceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualExplainerHttpRoute)
			}, timeout).
				Should(Succeed())
			explainerHost := fmt.Sprintf("%s-%s.%s", explainerServiceKey.Name, serviceKey.Namespace, "example.com")
			expectedExplainerHttpRoute := gatewayapiv1.HTTPRoute{
				Spec: gatewayapiv1.HTTPRouteSpec{
					Hostnames: []gatewayapiv1.Hostname{gatewayapiv1.Hostname(explainerHost)},
					Rules: []gatewayapiv1.HTTPRouteRule{
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.FallbackPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(explainerServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("30s")),
							},
						},
					},
					CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
						ParentRefs: []gatewayapiv1.ParentReference{
							{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
						},
					},
				},
			}
			Expect(actualExplainerHttpRoute.Spec).To(BeComparableTo(expectedExplainerHttpRoute.Spec))

			// Mark the Ingress as accepted to make isvc ready
			httpRouteStatus := gatewayapiv1.HTTPRouteStatus{
				RouteStatus: gatewayapiv1.RouteStatus{
					Parents: []gatewayapiv1.RouteParentStatus{
						{
							ParentRef: gatewayapiv1.ParentReference{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
							ControllerName: "istio.io/gateway-controller",
							Conditions: []metav1.Condition{
								{
									Type:               string(gatewayapiv1.ListenerConditionAccepted),
									Status:             metav1.ConditionTrue,
									Reason:             "Accepted",
									Message:            "Route was valid",
									LastTransitionTime: metav1.Now(),
								},
							},
						},
					},
				},
			}
			actualPredictorHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(context.Background(), actualPredictorHttpRoute)).NotTo(HaveOccurred())
			actualExplainerHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(context.Background(), actualExplainerHttpRoute)).NotTo(HaveOccurred())
			actualToplevelHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(context.Background(), actualToplevelHttpRoute)).NotTo(HaveOccurred())

			// verify if InferenceService status is updated
			expectedIsvcStatus := v1beta1.InferenceServiceStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:     v1beta1.ExplainerReady,
							Status:   "True",
							Severity: "Info",
						},
						{
							Type:   v1beta1.IngressReady,
							Status: "True",
						},
						{
							Type:   v1beta1.PredictorReady,
							Status: "True",
						},
						{
							Type:   apis.ConditionReady,
							Status: "True",
						},
						{
							Type:     v1beta1.Stopped,
							Status:   "False",
							Severity: apis.ConditionSeverityInfo,
						},
					},
				},
				URL: &apis.URL{
					Scheme: "http",
					Host:   fmt.Sprintf("%s-%s.example.com", serviceKey.Name, serviceKey.Namespace),
				},
				Address: &duckv1.Addressable{
					URL: &apis.URL{
						Scheme: "http",
						Host:   fmt.Sprintf("%s.%s.svc.cluster.local", predictorServiceKey.Name, serviceKey.Namespace),
					},
				},
				Components: map[v1beta1.ComponentType]v1beta1.ComponentStatusSpec{
					v1beta1.PredictorComponent: {
						LatestCreatedRevision: "",
						// URL: &apis.URL{
						// 	Scheme: "http",
						// 	Host:   fmt.Sprintf("%s-%s.example.com", predictorServiceKey.Name, serviceKey.Namespace),
						// },
					},
					v1beta1.ExplainerComponent: {
						LatestCreatedRevision: "",
						// URL: &apis.URL{
						// 	Scheme: "http",
						// 	Host:   fmt.Sprintf("%s-%s.example.com", explainerServiceKey.Name, serviceKey.Namespace),
						// },
					},
				},
				ModelStatus: v1beta1.ModelStatus{
					TransitionStatus:    "InProgress",
					ModelRevisionStates: &v1beta1.ModelRevisionStates{TargetModelState: "Pending"},
				},
				DeploymentMode: string(constants.RawDeployment),
			}
			Eventually(func() string {
				isvc := &v1beta1.InferenceService{}
				if err := k8sClient.Get(context.TODO(), serviceKey, isvc); err != nil {
					return err.Error()
				}
				return cmp.Diff(&expectedIsvcStatus, &isvc.Status, cmpopts.IgnoreTypes(apis.VolatileTime{}))
			}, timeout).Should(BeEmpty())

			// check predictor HPA
			var cpuUtilization int32 = 75
			var stabilizationWindowSeconds int32 = 0
			selectPolicy := autoscalingv2.MaxChangePolicySelect
			actualPredictorHPA := &autoscalingv2.HorizontalPodAutoscaler{}
			predictorHPAKey := types.NamespacedName{
				Name:      predictorServiceKey.Name,
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorHPAKey, actualPredictorHPA) }, timeout).
				Should(Succeed())
			expectedPredictorHPA := &autoscalingv2.HorizontalPodAutoscaler{
				Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
					ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       predictorServiceKey.Name,
					},
					MinReplicas: &minReplicas,
					MaxReplicas: maxReplicas,
					Metrics: []autoscalingv2.MetricSpec{
						{
							Type: autoscalingv2.ResourceMetricSourceType,
							Resource: &autoscalingv2.ResourceMetricSource{
								Name: corev1.ResourceCPU,
								Target: autoscalingv2.MetricTarget{
									Type:               "Utilization",
									AverageUtilization: &cpuUtilization,
								},
							},
						},
					},
					Behavior: &autoscalingv2.HorizontalPodAutoscalerBehavior{
						ScaleUp: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: &stabilizationWindowSeconds,
							SelectPolicy:               &selectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Pods",
									Value:         4,
									PeriodSeconds: 15,
								},
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
						ScaleDown: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: nil,
							SelectPolicy:               &selectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
					},
				},
			}
			Expect(actualPredictorHPA.Spec).To(BeComparableTo(expectedPredictorHPA.Spec))

			// check Explainer HPA
			actualExplainerHPA := &autoscalingv2.HorizontalPodAutoscaler{}
			explainerHPAKey := types.NamespacedName{
				Name:      explainerServiceKey.Name,
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), explainerHPAKey, actualExplainerHPA) }, timeout).
				Should(Succeed())
			expectedExplainerHPA := &autoscalingv2.HorizontalPodAutoscaler{
				Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
					ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       explainerServiceKey.Name,
					},
					MinReplicas: &explainerMinReplicas,
					MaxReplicas: explainerMaxReplicas,
					Metrics: []autoscalingv2.MetricSpec{
						{
							Type: autoscalingv2.ResourceMetricSourceType,
							Resource: &autoscalingv2.ResourceMetricSource{
								Name: corev1.ResourceCPU,
								Target: autoscalingv2.MetricTarget{
									Type:               "Utilization",
									AverageUtilization: &explainerCpuUtilization,
								},
							},
						},
					},
					Behavior: &autoscalingv2.HorizontalPodAutoscalerBehavior{
						ScaleUp: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: &explainerStabilizationWindowSeconds,
							SelectPolicy:               &ExplainerSelectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Pods",
									Value:         4,
									PeriodSeconds: 15,
								},
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
						ScaleDown: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: nil,
							SelectPolicy:               &ExplainerSelectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
					},
				},
			}
			Expect(actualExplainerHPA.Spec).To(BeComparableTo(expectedExplainerHPA.Spec))
		})
	})
	Context("When creating inference service with raw kube path based routing predictor", func() {
		configs := map[string]string{
			"explainers": `{
				"alibi": {
					"image": "kserve/alibi-explainer",
					"defaultImageVersion": "latest"
				}
			}`,
			"ingress": `{
				"enableGatewayApi": true,
				"kserveIngressGateway": "kserve/kserve-ingress-gateway",
				"ingressGateway": "knative-serving/knative-ingress-gateway",
				"localGateway": "knative-serving/knative-local-gateway",
				"localGatewayService": "knative-local-gateway.istio-system.svc.cluster.local",
				"additionalIngressDomains": ["additional.example.com"],
				"ingressDomain": "example.com",
				"pathTemplate": "/serving/{{ .Namespace }}/{{ .Name }}"
			}`,
			"storageInitializer": `{
				"image" : "kserve/storage-initializer:latest",
				"memoryRequest": "100Mi",
				"memoryLimit": "1Gi",
				"cpuRequest": "100m",
				"cpuLimit": "1",
				"CaBundleConfigMapName": "",
				"caBundleVolumeMountPath": "/etc/ssl/custom-certs",
				"enableDirectPvcVolumeMount": false
			}`,
		}

		It("Should have httproute/service/deployment/hpa created", func() {
			By("By creating a new InferenceService")
			// Create configmap
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.InferenceServiceConfigMapName,
					Namespace: constants.KServeNamespace,
				},
				Data: configs,
			}
			Expect(k8sClient.Create(context.TODO(), configMap)).NotTo(HaveOccurred())
			defer k8sClient.Delete(context.TODO(), configMap)
			// Create ServingRuntime
			servingRuntime := &v1alpha1.ServingRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "tf-serving-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.ServingRuntimeSpec{
					SupportedModelFormats: []v1alpha1.SupportedModelFormat{
						{
							Name:       "tensorflow",
							Version:    proto.String("1"),
							AutoSelect: proto.Bool(true),
						},
					},
					ServingRuntimePodSpec: v1alpha1.ServingRuntimePodSpec{
						Containers: []corev1.Container{
							{
								Name:    "kserve-container",
								Image:   "tensorflow/serving:1.14.0",
								Command: []string{"/usr/bin/tensorflow_model_server"},
								Args: []string{
									"--port=9000",
									"--rest_api_port=8080",
									"--model_base_path=/mnt/models",
									"--rest_api_timeout_in_ms=60000",
								},
								Resources: defaultResource,
							},
						},
					},
					Disabled: proto.Bool(false),
				},
			}
			k8sClient.Create(context.TODO(), servingRuntime)
			defer k8sClient.Delete(context.TODO(), servingRuntime)
			serviceName := "raw-foo-path"
			expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: "default"}}
			serviceKey := expectedRequest.NamespacedName
			storageUri := "s3://test/mnist/export"
			ctx := context.Background()
			isvc := &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: string(constants.AutoscalerClassHPA),
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
							MinReplicas:    ptr.To(int32(1)),
							MaxReplicas:    3,
							TimeoutSeconds: ptr.To(int64(30)),
							AutoScaling: &v1beta1.AutoScalingSpec{
								Metrics: []v1beta1.MetricsSpec{
									{
										Type: v1beta1.ResourceMetricSourceType,
										Resource: &v1beta1.ResourceMetricSource{
											Name: v1beta1.ResourceMetricCPU,
											Target: v1beta1.MetricTarget{
												Type:               v1beta1.UtilizationMetricType,
												AverageUtilization: ptr.To(int32(75)),
											},
										},
									},
								},
							},
						},
						Tensorflow: &v1beta1.TFServingSpec{
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI:     &storageUri,
								RuntimeVersion: proto.String("1.14.0"),
								Container: corev1.Container{
									Name:      constants.InferenceServiceContainerName,
									Resources: defaultResource,
								},
							},
						},
					},
				},
			}
			isvc.DefaultInferenceService(nil, nil, &v1beta1.SecurityConfig{AutoMountServiceAccountToken: false}, nil)
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			defer k8sClient.Delete(ctx, isvc)

			inferenceService := &v1beta1.InferenceService{}

			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, inferenceService)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			actualDeployment := &appsv1.Deployment{}
			predictorDeploymentKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorDeploymentKey, actualDeployment) }, timeout).
				Should(Succeed())
			var replicas int32 = 1
			var revisionHistory int32 = 10
			var progressDeadlineSeconds int32 = 600
			var gracePeriod int64 = 30
			expectedDeployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorDeploymentKey.Name,
					Namespace: predictorDeploymentKey.Namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "isvc." + predictorDeploymentKey.Name,
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Name:      predictorDeploymentKey.Name,
							Namespace: "default",
							Labels: map[string]string{
								"app":                                 "isvc." + predictorDeploymentKey.Name,
								constants.KServiceComponentLabel:      constants.Predictor.String(),
								constants.InferenceServicePodLabelKey: serviceName,
							},
							Annotations: map[string]string{
								constants.StorageInitializerSourceUriInternalAnnotationKey: *isvc.Spec.Predictor.Model.StorageURI,
								constants.DeploymentMode:                                   string(constants.RawDeployment),
								constants.AutoscalerClass:                                  string(constants.AutoscalerClassHPA),
								constants.OpenshiftServingCertAnnotation:                   predictorDeploymentKey.Name + constants.ServingCertSecretSuffix,
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Image: "tensorflow/serving:" +
										*isvc.Spec.Predictor.Model.RuntimeVersion,
									Name:    constants.InferenceServiceContainerName,
									Command: []string{v1beta1.TensorflowEntrypointCommand},
									Args: []string{
										"--port=" + v1beta1.TensorflowServingGRPCPort,
										"--rest_api_port=" + v1beta1.TensorflowServingRestPort,
										"--model_base_path=" + constants.DefaultModelLocalMountPath,
										"--rest_api_timeout_in_ms=60000",
									},
									Resources: defaultResource,
									ReadinessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											TCPSocket: &corev1.TCPSocketAction{
												Port: intstr.IntOrString{
													IntVal: 8080,
												},
											},
										},
										InitialDelaySeconds: 0,
										TimeoutSeconds:      1,
										PeriodSeconds:       10,
										SuccessThreshold:    1,
										FailureThreshold:    3,
									},
									TerminationMessagePath:   "/dev/termination-log",
									TerminationMessagePolicy: "File",
									ImagePullPolicy:          "IfNotPresent",
								},
							},
							SchedulerName:                 "default-scheduler",
							RestartPolicy:                 "Always",
							TerminationGracePeriodSeconds: &gracePeriod,
							DNSPolicy:                     "ClusterFirst",
							SecurityContext: &corev1.PodSecurityContext{
								SELinuxOptions:      nil,
								WindowsOptions:      nil,
								RunAsUser:           nil,
								RunAsGroup:          nil,
								RunAsNonRoot:        nil,
								SupplementalGroups:  nil,
								FSGroup:             nil,
								Sysctls:             nil,
								FSGroupChangePolicy: nil,
								SeccompProfile:      nil,
							},
							AutomountServiceAccountToken: proto.Bool(false),
						},
					},
					Strategy: appsv1.DeploymentStrategy{
						Type: "RollingUpdate",
						RollingUpdate: &appsv1.RollingUpdateDeployment{
							MaxUnavailable: &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
							MaxSurge:       &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
						},
					},
					RevisionHistoryLimit:    &revisionHistory,
					ProgressDeadlineSeconds: &progressDeadlineSeconds,
				},
			}
			Expect(actualDeployment.Spec).To(BeComparableTo(expectedDeployment.Spec))

			// check service
			actualService := &corev1.Service{}
			predictorServiceKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorServiceKey, actualService) }, timeout).
				Should(Succeed())

			expectedService := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorServiceKey.Name,
					Namespace: predictorServiceKey.Namespace,
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       constants.PredictorServiceName(serviceName),
							Protocol:   "TCP",
							Port:       80,
							TargetPort: intstr.IntOrString{Type: 0, IntVal: 8080, StrVal: ""},
						},
					},
					Type:            "ClusterIP",
					SessionAffinity: "None",
					Selector: map[string]string{
						"app": "isvc." + constants.PredictorServiceName(serviceName),
					},
				},
			}
			actualService.Spec.ClusterIP = ""
			actualService.Spec.ClusterIPs = nil
			actualService.Spec.IPFamilies = nil
			actualService.Spec.IPFamilyPolicy = nil
			actualService.Spec.InternalTrafficPolicy = nil
			Expect(actualService.Spec).To(BeComparableTo(expectedService.Spec))

			// check isvc status
			updatedDeployment := actualDeployment.DeepCopy()
			updatedDeployment.Status.Conditions = []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: corev1.ConditionTrue,
				},
			}
			Expect(k8sClient.Status().Update(context.TODO(), updatedDeployment)).NotTo(HaveOccurred())

			// check http route
			actualToplevelHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualToplevelHttpRoute)
			}, timeout).
				Should(Succeed())
			topLevelHost := fmt.Sprintf("%s-%s.%s", serviceKey.Name, serviceKey.Namespace, "example.com")
			prefixUrlPath := fmt.Sprintf("/serving/%s/%s", serviceKey.Namespace, serviceKey.Name)
			expectedToplevelHttpRoute := gatewayapiv1.HTTPRoute{
				Spec: gatewayapiv1.HTTPRouteSpec{
					Hostnames: []gatewayapiv1.Hostname{gatewayapiv1.Hostname(topLevelHost), gatewayapiv1.Hostname(fmt.Sprintf("%s-%s.additional.example.com", serviceKey.Name, serviceKey.Namespace)), "example.com"},
					Rules: []gatewayapiv1.HTTPRouteRule{
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.FallbackPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(predictorServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("30s")),
							},
						},
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(prefixUrlPath + "/"),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(predictorServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("30s")),
							},
						},
					},
					CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
						ParentRefs: []gatewayapiv1.ParentReference{
							{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
						},
					},
				},
			}
			Expect(actualToplevelHttpRoute.Spec).To(BeComparableTo(expectedToplevelHttpRoute.Spec))

			actualPredictorHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      predictorServiceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualPredictorHttpRoute)
			}, timeout).
				Should(Succeed())
			predictorHost := fmt.Sprintf("%s-%s.%s", predictorServiceKey.Name, serviceKey.Namespace, "example.com")
			expectedPredictorHttpRoute := gatewayapiv1.HTTPRoute{
				Spec: gatewayapiv1.HTTPRouteSpec{
					Hostnames: []gatewayapiv1.Hostname{gatewayapiv1.Hostname(predictorHost)},
					Rules: []gatewayapiv1.HTTPRouteRule{
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.FallbackPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(predictorServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("30s")),
							},
						},
					},
					CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
						ParentRefs: []gatewayapiv1.ParentReference{
							{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
						},
					},
				},
			}
			Expect(actualPredictorHttpRoute.Spec).To(BeComparableTo(expectedPredictorHttpRoute.Spec))

			// Mark the Ingress as accepted to make isvc ready
			httpRouteStatus := gatewayapiv1.HTTPRouteStatus{
				RouteStatus: gatewayapiv1.RouteStatus{
					Parents: []gatewayapiv1.RouteParentStatus{
						{
							ParentRef: gatewayapiv1.ParentReference{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
							ControllerName: "istio.io/gateway-controller",
							Conditions: []metav1.Condition{
								{
									Type:               string(gatewayapiv1.ListenerConditionAccepted),
									Status:             metav1.ConditionTrue,
									Reason:             "Accepted",
									Message:            "Route was valid",
									LastTransitionTime: metav1.Now(),
								},
							},
						},
					},
				},
			}
			actualPredictorHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(context.Background(), actualPredictorHttpRoute)).NotTo(HaveOccurred())
			actualToplevelHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(context.Background(), actualToplevelHttpRoute)).NotTo(HaveOccurred())

			// verify if InferenceService status is updated
			expectedIsvcStatus := v1beta1.InferenceServiceStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:   v1beta1.IngressReady,
							Status: "True",
						},
						{
							Type:   v1beta1.PredictorReady,
							Status: "True",
						},
						{
							Type:   apis.ConditionReady,
							Status: "True",
						},
						{
							Type:     v1beta1.Stopped,
							Status:   "False",
							Severity: apis.ConditionSeverityInfo,
						},
					},
				},
				URL: &apis.URL{
					Scheme: "http",
					Host:   fmt.Sprintf("%s-%s.example.com", serviceKey.Name, serviceKey.Namespace),
				},
				Address: &duckv1.Addressable{
					URL: &apis.URL{
						Scheme: "http",
						Host:   fmt.Sprintf("%s-predictor.%s.svc.cluster.local", serviceKey.Name, serviceKey.Namespace),
					},
				},
				Components: map[v1beta1.ComponentType]v1beta1.ComponentStatusSpec{
					v1beta1.PredictorComponent: {
						LatestCreatedRevision: "",
						// URL: &apis.URL{
						// 	Scheme: "http",
						// 	Host:   fmt.Sprintf("%s-%s.example.com", predictorServiceKey.Name, serviceKey.Namespace),
						// },
					},
				},
				ModelStatus: v1beta1.ModelStatus{
					TransitionStatus:    "InProgress",
					ModelRevisionStates: &v1beta1.ModelRevisionStates{TargetModelState: "Pending"},
				},
				DeploymentMode: string(constants.RawDeployment),
			}
			Eventually(func() string {
				isvc := &v1beta1.InferenceService{}
				if err := k8sClient.Get(context.TODO(), serviceKey, isvc); err != nil {
					return err.Error()
				}
				return cmp.Diff(&expectedIsvcStatus, &isvc.Status, cmpopts.IgnoreTypes(apis.VolatileTime{}))
			}, timeout).Should(BeEmpty())

			// check HPA
			var minReplicas int32 = 1
			var maxReplicas int32 = 3
			var cpuUtilization int32 = 75
			var stabilizationWindowSeconds int32 = 0
			selectPolicy := autoscalingv2.MaxChangePolicySelect
			actualHPA := &autoscalingv2.HorizontalPodAutoscaler{}
			predictorHPAKey := types.NamespacedName{
				Name:      predictorServiceKey.Name,
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorHPAKey, actualHPA) }, timeout).
				Should(Succeed())
			expectedHPA := &autoscalingv2.HorizontalPodAutoscaler{
				Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
					ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       predictorServiceKey.Name,
					},
					MinReplicas: &minReplicas,
					MaxReplicas: maxReplicas,
					Metrics: []autoscalingv2.MetricSpec{
						{
							Type: autoscalingv2.ResourceMetricSourceType,
							Resource: &autoscalingv2.ResourceMetricSource{
								Name: corev1.ResourceCPU,
								Target: autoscalingv2.MetricTarget{
									Type:               autoscalingv2.UtilizationMetricType,
									AverageUtilization: &cpuUtilization,
								},
							},
						},
					},
					Behavior: &autoscalingv2.HorizontalPodAutoscalerBehavior{
						ScaleUp: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: &stabilizationWindowSeconds,
							SelectPolicy:               &selectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Pods",
									Value:         4,
									PeriodSeconds: 15,
								},
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
						ScaleDown: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: nil,
							SelectPolicy:               &selectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
					},
				},
			}
			Expect(actualHPA.Spec).To(BeComparableTo(expectedHPA.Spec))
		})
	})
	Context("When creating inference service with raw kube path based routing predictor and transformer", func() {
		configs := map[string]string{
			"explainers": `{
				"alibi": {
					"image": "kserve/alibi-explainer",
					"defaultImageVersion": "latest"
				}
			}`,
			"ingress": `{
				"enableGatewayApi": true,
				"kserveIngressGateway": "kserve/kserve-ingress-gateway",
				"ingressGateway": "knative-serving/knative-ingress-gateway",
				"localGateway": "knative-serving/knative-local-gateway",
				"localGatewayService": "knative-local-gateway.istio-system.svc.cluster.local",
				"additionalIngressDomains": ["additional.example.com"],
				"ingressDomain": "example.com",
				"pathTemplate": "/serving/{{ .Namespace }}/{{ .Name }}"
			}`,
			"storageInitializer": `{
				"image" : "kserve/storage-initializer:latest",
				"memoryRequest": "100Mi",
				"memoryLimit": "1Gi",
				"cpuRequest": "100m",
				"cpuLimit": "1",
				"CaBundleConfigMapName": "",
				"caBundleVolumeMountPath": "/etc/ssl/custom-certs",
				"enableDirectPvcVolumeMount": false
			}`,
		}

		It("Should have ingress/service/deployment/hpa created for transformer and predictor", func() {
			By("By creating a new InferenceService")
			// Create configmap
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.InferenceServiceConfigMapName,
					Namespace: constants.KServeNamespace,
				},
				Data: configs,
			}
			Expect(k8sClient.Create(context.TODO(), configMap)).NotTo(HaveOccurred())
			defer k8sClient.Delete(context.TODO(), configMap)
			// Create ServingRuntime
			servingRuntime := &v1alpha1.ServingRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "tf-serving-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.ServingRuntimeSpec{
					SupportedModelFormats: []v1alpha1.SupportedModelFormat{
						{
							Name:       "tensorflow",
							Version:    proto.String("1"),
							AutoSelect: proto.Bool(true),
						},
					},
					ServingRuntimePodSpec: v1alpha1.ServingRuntimePodSpec{
						Containers: []corev1.Container{
							{
								Name:    "kserve-container",
								Image:   "tensorflow/serving:1.14.0",
								Command: []string{"/usr/bin/tensorflow_model_server"},
								Args: []string{
									"--port=9000",
									"--rest_api_port=8080",
									"--model_base_path=/mnt/models",
									"--rest_api_timeout_in_ms=60000",
								},
								Resources: defaultResource,
							},
						},
					},
					Disabled: proto.Bool(false),
				},
			}
			k8sClient.Create(context.TODO(), servingRuntime)
			defer k8sClient.Delete(context.TODO(), servingRuntime)
			serviceName := "raw-foo-trans-path"
			namespace := "default"
			expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: namespace}}
			serviceKey := expectedRequest.NamespacedName
			predictorServiceKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceName),
				Namespace: namespace,
			}
			transformerServiceKey := types.NamespacedName{
				Name:      constants.TransformerServiceName(serviceName),
				Namespace: namespace,
			}
			storageUri := "s3://test/mnist/export"
			var minReplicas int32 = 1
			var maxReplicas int32 = 3
			var transformerMinReplicas int32 = 1
			var transformerMaxReplicas int32 = 2
			var transformerCpuUtilization int32 = 80
			var transformerStabilizationWindowSeconds int32 = 0
			transformerSelectPolicy := autoscalingv2.MaxChangePolicySelect
			ctx := context.Background()
			isvc := &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: string(constants.AutoscalerClassHPA),
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
							MinReplicas: ptr.To(minReplicas),
							MaxReplicas: maxReplicas,
							AutoScaling: &v1beta1.AutoScalingSpec{
								Metrics: []v1beta1.MetricsSpec{
									{
										Type: v1beta1.ResourceMetricSourceType,
										Resource: &v1beta1.ResourceMetricSource{
											Name: v1beta1.ResourceMetricCPU,
											Target: v1beta1.MetricTarget{
												Type: v1beta1.UtilizationMetricType,
											},
										},
									},
								},
							},
						},
						Tensorflow: &v1beta1.TFServingSpec{
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI:     &storageUri,
								RuntimeVersion: proto.String("1.14.0"),
								Container: corev1.Container{
									Name:      constants.InferenceServiceContainerName,
									Resources: defaultResource,
								},
							},
						},
					},
					Transformer: &v1beta1.TransformerSpec{
						ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
							MinReplicas:    ptr.To(transformerMinReplicas),
							MaxReplicas:    transformerMaxReplicas,
							ScaleTarget:    ptr.To(transformerCpuUtilization),
							TimeoutSeconds: ptr.To(int64(30)),
						},
						PodSpec: v1beta1.PodSpec{
							Containers: []corev1.Container{
								{
									Image:     "transformer:v1",
									Resources: defaultResource,
									Args: []string{
										"--port=8080",
									},
								},
							},
						},
					},
				},
			}
			isvcConfigMap, err := v1beta1.GetInferenceServiceConfigMap(context.Background(), clientset)
			Expect(err).NotTo(HaveOccurred())
			isvcConfig, err := v1beta1.NewInferenceServicesConfig(isvcConfigMap)
			Expect(err).NotTo(HaveOccurred())
			isvc.DefaultInferenceService(isvcConfig, nil, &v1beta1.SecurityConfig{AutoMountServiceAccountToken: false}, nil)
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			defer k8sClient.Delete(ctx, isvc)

			inferenceService := &v1beta1.InferenceService{}

			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, inferenceService)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			// check predictor deployment
			actualPredictorDeployment := &appsv1.Deployment{}
			predictorDeploymentKey := types.NamespacedName{
				Name:      predictorServiceKey.Name,
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorDeploymentKey, actualPredictorDeployment) }, timeout).
				Should(Succeed())
			var replicas int32 = 1
			var revisionHistory int32 = 10
			var progressDeadlineSeconds int32 = 600
			var gracePeriod int64 = 30
			expectedPredictorDeployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorDeploymentKey.Name,
					Namespace: predictorDeploymentKey.Namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "isvc." + predictorDeploymentKey.Name,
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Name:      predictorDeploymentKey.Name,
							Namespace: "default",
							Labels: map[string]string{
								"app":                                 "isvc." + predictorDeploymentKey.Name,
								constants.KServiceComponentLabel:      constants.Predictor.String(),
								constants.InferenceServicePodLabelKey: serviceName,
							},
							Annotations: map[string]string{
								constants.StorageInitializerSourceUriInternalAnnotationKey: *isvc.Spec.Predictor.Model.StorageURI,
								constants.DeploymentMode:                                   string(constants.RawDeployment),
								constants.AutoscalerClass:                                  string(constants.AutoscalerClassHPA),
								constants.OpenshiftServingCertAnnotation:                   predictorDeploymentKey.Name + constants.ServingCertSecretSuffix,
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Image: "tensorflow/serving:" +
										*isvc.Spec.Predictor.Model.RuntimeVersion,
									Name:    constants.InferenceServiceContainerName,
									Command: []string{v1beta1.TensorflowEntrypointCommand},
									Args: []string{
										"--port=" + v1beta1.TensorflowServingGRPCPort,
										"--rest_api_port=" + v1beta1.TensorflowServingRestPort,
										"--model_base_path=" + constants.DefaultModelLocalMountPath,
										"--rest_api_timeout_in_ms=60000",
									},
									Resources: defaultResource,
									ReadinessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											TCPSocket: &corev1.TCPSocketAction{
												Port: intstr.IntOrString{
													IntVal: 8080,
												},
											},
										},
										InitialDelaySeconds: 0,
										TimeoutSeconds:      1,
										PeriodSeconds:       10,
										SuccessThreshold:    1,
										FailureThreshold:    3,
									},
									TerminationMessagePath:   "/dev/termination-log",
									TerminationMessagePolicy: "File",
									ImagePullPolicy:          "IfNotPresent",
								},
							},
							SchedulerName:                 "default-scheduler",
							RestartPolicy:                 "Always",
							TerminationGracePeriodSeconds: &gracePeriod,
							DNSPolicy:                     "ClusterFirst",
							SecurityContext: &corev1.PodSecurityContext{
								SELinuxOptions:      nil,
								WindowsOptions:      nil,
								RunAsUser:           nil,
								RunAsGroup:          nil,
								RunAsNonRoot:        nil,
								SupplementalGroups:  nil,
								FSGroup:             nil,
								Sysctls:             nil,
								FSGroupChangePolicy: nil,
								SeccompProfile:      nil,
							},
							AutomountServiceAccountToken: proto.Bool(false),
						},
					},
					Strategy: appsv1.DeploymentStrategy{
						Type: "RollingUpdate",
						RollingUpdate: &appsv1.RollingUpdateDeployment{
							MaxUnavailable: &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
							MaxSurge:       &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
						},
					},
					RevisionHistoryLimit:    &revisionHistory,
					ProgressDeadlineSeconds: &progressDeadlineSeconds,
				},
			}
			Expect(actualPredictorDeployment.Spec).To(BeComparableTo(expectedPredictorDeployment.Spec))

			// check transformer deployment
			actualTransformerDeployment := &appsv1.Deployment{}
			transformerDeploymentKey := types.NamespacedName{
				Name:      transformerServiceKey.Name,
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), transformerDeploymentKey, actualTransformerDeployment)
			}, timeout).
				Should(Succeed())
			expectedTransformerDeployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      transformerDeploymentKey.Name,
					Namespace: transformerDeploymentKey.Namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "isvc." + transformerDeploymentKey.Name,
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Name:      transformerDeploymentKey.Name,
							Namespace: "default",
							Labels: map[string]string{
								"app":                                 "isvc." + transformerDeploymentKey.Name,
								constants.KServiceComponentLabel:      constants.Transformer.String(),
								constants.InferenceServicePodLabelKey: serviceName,
							},
							Annotations: map[string]string{
								constants.DeploymentMode:                 string(constants.RawDeployment),
								constants.AutoscalerClass:                string(constants.AutoscalerClassHPA),
								constants.OpenshiftServingCertAnnotation: transformerDeploymentKey.Name + constants.ServingCertSecretSuffix,
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Image: "transformer:v1",
									Name:  constants.InferenceServiceContainerName,
									Args: []string{
										"--port=8080",
										"--model_name",
										serviceKey.Name,
										"--predictor_host",
										fmt.Sprintf("%s.%s", predictorServiceKey.Name, predictorServiceKey.Namespace),
										"--http_port",
										"8080",
									},
									Resources: defaultResource,
									ReadinessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											TCPSocket: &corev1.TCPSocketAction{
												Port: intstr.IntOrString{
													IntVal: 8080,
												},
											},
										},
										InitialDelaySeconds: 0,
										TimeoutSeconds:      1,
										PeriodSeconds:       10,
										SuccessThreshold:    1,
										FailureThreshold:    3,
									},
									TerminationMessagePath:   "/dev/termination-log",
									TerminationMessagePolicy: "File",
									ImagePullPolicy:          "IfNotPresent",
								},
							},
							SchedulerName:                 "default-scheduler",
							RestartPolicy:                 "Always",
							TerminationGracePeriodSeconds: &gracePeriod,
							DNSPolicy:                     "ClusterFirst",
							SecurityContext: &corev1.PodSecurityContext{
								SELinuxOptions:      nil,
								WindowsOptions:      nil,
								RunAsUser:           nil,
								RunAsGroup:          nil,
								RunAsNonRoot:        nil,
								SupplementalGroups:  nil,
								FSGroup:             nil,
								Sysctls:             nil,
								FSGroupChangePolicy: nil,
								SeccompProfile:      nil,
							},
							AutomountServiceAccountToken: proto.Bool(false),
						},
					},
					Strategy: appsv1.DeploymentStrategy{
						Type: "RollingUpdate",
						RollingUpdate: &appsv1.RollingUpdateDeployment{
							MaxUnavailable: &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
							MaxSurge:       &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
						},
					},
					RevisionHistoryLimit:    &revisionHistory,
					ProgressDeadlineSeconds: &progressDeadlineSeconds,
				},
			}
			Expect(actualTransformerDeployment.Spec).To(BeComparableTo(expectedTransformerDeployment.Spec))

			// check predictor service
			actualPredictorService := &corev1.Service{}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorServiceKey, actualPredictorService) }, timeout).
				Should(Succeed())
			expectedPredictorService := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorServiceKey.Name,
					Namespace: predictorServiceKey.Namespace,
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       predictorServiceKey.Name,
							Protocol:   "TCP",
							Port:       80,
							TargetPort: intstr.IntOrString{Type: 0, IntVal: 8080, StrVal: ""},
						},
					},
					Type:            "ClusterIP",
					SessionAffinity: "None",
					Selector: map[string]string{
						"app": "isvc." + predictorServiceKey.Name,
					},
				},
			}
			actualPredictorService.Spec.ClusterIP = ""
			actualPredictorService.Spec.ClusterIPs = nil
			actualPredictorService.Spec.IPFamilies = nil
			actualPredictorService.Spec.IPFamilyPolicy = nil
			actualPredictorService.Spec.InternalTrafficPolicy = nil
			Expect(actualPredictorService.Spec).To(BeComparableTo(expectedPredictorService.Spec))

			// check transformer service
			actualTransformerService := &corev1.Service{}
			Eventually(func() error { return k8sClient.Get(context.TODO(), transformerServiceKey, actualTransformerService) }, timeout).
				Should(Succeed())
			expectedTransformerService := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      transformerServiceKey.Name,
					Namespace: transformerServiceKey.Namespace,
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       transformerServiceKey.Name,
							Protocol:   "TCP",
							Port:       80,
							TargetPort: intstr.IntOrString{Type: 0, IntVal: 8080, StrVal: ""},
						},
					},
					Type:            "ClusterIP",
					SessionAffinity: "None",
					Selector: map[string]string{
						"app": "isvc." + transformerServiceKey.Name,
					},
				},
			}
			actualTransformerService.Spec.ClusterIP = ""
			actualTransformerService.Spec.ClusterIPs = nil
			actualTransformerService.Spec.IPFamilies = nil
			actualTransformerService.Spec.IPFamilyPolicy = nil
			actualTransformerService.Spec.InternalTrafficPolicy = nil
			Expect(actualTransformerService.Spec).To(BeComparableTo(expectedTransformerService.Spec))

			// update deployment status to make isvc ready
			updatedPredictorDeployment := actualPredictorDeployment.DeepCopy()
			updatedPredictorDeployment.Status.Conditions = []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: corev1.ConditionTrue,
				},
			}
			Expect(k8sClient.Status().Update(context.TODO(), updatedPredictorDeployment)).NotTo(HaveOccurred())
			updatedTransformerDeployment := actualTransformerDeployment.DeepCopy()
			updatedTransformerDeployment.Status.Conditions = []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: corev1.ConditionTrue,
				},
			}
			Expect(k8sClient.Status().Update(context.TODO(), updatedTransformerDeployment)).NotTo(HaveOccurred())

			// check http route
			actualToplevelHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualToplevelHttpRoute)
			}, timeout).
				Should(Succeed())
			topLevelHost := fmt.Sprintf("%s-%s.%s", serviceKey.Name, serviceKey.Namespace, "example.com")
			prefixUrlPath := fmt.Sprintf("/serving/%s/%s", serviceKey.Namespace, serviceKey.Name)
			expectedToplevelHttpRoute := gatewayapiv1.HTTPRoute{
				Spec: gatewayapiv1.HTTPRouteSpec{
					Hostnames: []gatewayapiv1.Hostname{gatewayapiv1.Hostname(topLevelHost), gatewayapiv1.Hostname(fmt.Sprintf("%s-%s.additional.example.com", serviceKey.Name, serviceKey.Namespace)), "example.com"},
					Rules: []gatewayapiv1.HTTPRouteRule{
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.FallbackPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(transformerServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("30s")),
							},
						},
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(prefixUrlPath + "/"),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(transformerServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("30s")),
							},
						},
					},
					CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
						ParentRefs: []gatewayapiv1.ParentReference{
							{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
						},
					},
				},
			}
			Expect(actualToplevelHttpRoute.Spec).To(BeComparableTo(expectedToplevelHttpRoute.Spec))

			actualPredictorHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      predictorServiceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualPredictorHttpRoute)
			}, timeout).
				Should(Succeed())
			predictorHost := fmt.Sprintf("%s-%s.%s", predictorServiceKey.Name, serviceKey.Namespace, "example.com")
			expectedPredictorHttpRoute := gatewayapiv1.HTTPRoute{
				Spec: gatewayapiv1.HTTPRouteSpec{
					Hostnames: []gatewayapiv1.Hostname{gatewayapiv1.Hostname(predictorHost)},
					Rules: []gatewayapiv1.HTTPRouteRule{
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.FallbackPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(predictorServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("60s")),
							},
						},
					},
					CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
						ParentRefs: []gatewayapiv1.ParentReference{
							{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
						},
					},
				},
			}
			Expect(actualPredictorHttpRoute.Spec).To(BeComparableTo(expectedPredictorHttpRoute.Spec))

			actualTransformerHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      transformerServiceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualTransformerHttpRoute)
			}, timeout).
				Should(Succeed())
			transformerHost := fmt.Sprintf("%s-%s.%s", transformerServiceKey.Name, serviceKey.Namespace, "example.com")
			expectedTransformerHttpRoute := gatewayapiv1.HTTPRoute{
				Spec: gatewayapiv1.HTTPRouteSpec{
					Hostnames: []gatewayapiv1.Hostname{gatewayapiv1.Hostname(transformerHost)},
					Rules: []gatewayapiv1.HTTPRouteRule{
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.FallbackPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(transformerServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("30s")),
							},
						},
					},
					CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
						ParentRefs: []gatewayapiv1.ParentReference{
							{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
						},
					},
				},
			}
			Expect(actualTransformerHttpRoute.Spec).To(BeComparableTo(expectedTransformerHttpRoute.Spec))

			// Mark the Ingress as accepted to make isvc ready
			httpRouteStatus := gatewayapiv1.HTTPRouteStatus{
				RouteStatus: gatewayapiv1.RouteStatus{
					Parents: []gatewayapiv1.RouteParentStatus{
						{
							ParentRef: gatewayapiv1.ParentReference{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
							ControllerName: "istio.io/gateway-controller",
							Conditions: []metav1.Condition{
								{
									Type:               string(gatewayapiv1.ListenerConditionAccepted),
									Status:             metav1.ConditionTrue,
									Reason:             "Accepted",
									Message:            "Route was valid",
									LastTransitionTime: metav1.Now(),
								},
							},
						},
					},
				},
			}
			actualPredictorHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(context.Background(), actualPredictorHttpRoute)).NotTo(HaveOccurred())
			actualTransformerHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(context.Background(), actualTransformerHttpRoute)).NotTo(HaveOccurred())
			actualToplevelHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(context.Background(), actualToplevelHttpRoute)).NotTo(HaveOccurred())

			// verify if InferenceService status is updated
			expectedIsvcStatus := v1beta1.InferenceServiceStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:   v1beta1.IngressReady,
							Status: "True",
						},
						{
							Type:   v1beta1.PredictorReady,
							Status: "True",
						},
						{
							Type:   apis.ConditionReady,
							Status: "True",
						},
						{
							Type:     v1beta1.Stopped,
							Status:   "False",
							Severity: apis.ConditionSeverityInfo,
						},
						{
							Type:     v1beta1.TransformerReady,
							Status:   "True",
							Severity: "Info",
						},
					},
				},
				URL: &apis.URL{
					Scheme: "http",
					Host:   fmt.Sprintf("%s-%s.example.com", serviceKey.Name, serviceKey.Namespace),
				},
				Address: &duckv1.Addressable{
					URL: &apis.URL{
						Scheme: "http",
						Host:   fmt.Sprintf("%s.%s.svc.cluster.local", transformerServiceKey.Name, serviceKey.Namespace),
					},
				},
				Components: map[v1beta1.ComponentType]v1beta1.ComponentStatusSpec{
					v1beta1.PredictorComponent: {
						LatestCreatedRevision: "",
						// URL: &apis.URL{
						// 	Scheme: "http",
						// 	Host:   fmt.Sprintf("%s-%s.example.com", predictorServiceKey.Name, serviceKey.Namespace),
						// },
					},
					v1beta1.TransformerComponent: {
						LatestCreatedRevision: "",
						// URL: &apis.URL{
						// 	Scheme: "http",
						// 	Host:   fmt.Sprintf("%s-%s.example.com", transformerServiceKey.Name, serviceKey.Namespace),
						// },
					},
				},
				ModelStatus: v1beta1.ModelStatus{
					TransitionStatus:    "InProgress",
					ModelRevisionStates: &v1beta1.ModelRevisionStates{TargetModelState: "Pending"},
				},
				DeploymentMode: string(constants.RawDeployment),
			}
			Eventually(func() string {
				isvc := &v1beta1.InferenceService{}
				if err := k8sClient.Get(context.TODO(), serviceKey, isvc); err != nil {
					return err.Error()
				}
				return cmp.Diff(&expectedIsvcStatus, &isvc.Status, cmpopts.IgnoreTypes(apis.VolatileTime{}))
			}, timeout).Should(BeEmpty())

			// check predictor HPA
			var defaultCpuUtilization int32 = 80
			var stabilizationWindowSeconds int32 = 0
			selectPolicy := autoscalingv2.MaxChangePolicySelect
			actualPredictorHPA := &autoscalingv2.HorizontalPodAutoscaler{}
			predictorHPAKey := types.NamespacedName{
				Name:      predictorServiceKey.Name,
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorHPAKey, actualPredictorHPA) }, timeout).
				Should(Succeed())
			expectedPredictorHPA := &autoscalingv2.HorizontalPodAutoscaler{
				Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
					ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       predictorServiceKey.Name,
					},
					MinReplicas: &minReplicas,
					MaxReplicas: maxReplicas,
					Metrics: []autoscalingv2.MetricSpec{
						{
							Type: autoscalingv2.ResourceMetricSourceType,
							Resource: &autoscalingv2.ResourceMetricSource{
								Name: corev1.ResourceCPU,
								Target: autoscalingv2.MetricTarget{
									Type:               autoscalingv2.UtilizationMetricType,
									AverageUtilization: &defaultCpuUtilization,
								},
							},
						},
					},
					Behavior: &autoscalingv2.HorizontalPodAutoscalerBehavior{
						ScaleUp: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: &stabilizationWindowSeconds,
							SelectPolicy:               &selectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Pods",
									Value:         4,
									PeriodSeconds: 15,
								},
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
						ScaleDown: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: nil,
							SelectPolicy:               &selectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
					},
				},
			}
			Expect(actualPredictorHPA.Spec).To(BeComparableTo(expectedPredictorHPA.Spec))

			// check transformer HPA
			actualTransformerHPA := &autoscalingv2.HorizontalPodAutoscaler{}
			transformerHPAKey := types.NamespacedName{
				Name:      transformerServiceKey.Name,
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), transformerHPAKey, actualTransformerHPA) }, timeout).
				Should(Succeed())
			expectedTransformerHPA := &autoscalingv2.HorizontalPodAutoscaler{
				Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
					ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       transformerServiceKey.Name,
					},
					MinReplicas: &transformerMinReplicas,
					MaxReplicas: transformerMaxReplicas,
					Metrics: []autoscalingv2.MetricSpec{
						{
							Type: autoscalingv2.ResourceMetricSourceType,
							Resource: &autoscalingv2.ResourceMetricSource{
								Name: corev1.ResourceCPU,
								Target: autoscalingv2.MetricTarget{
									Type:               "Utilization",
									AverageUtilization: &transformerCpuUtilization,
								},
							},
						},
					},
					Behavior: &autoscalingv2.HorizontalPodAutoscalerBehavior{
						ScaleUp: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: &transformerStabilizationWindowSeconds,
							SelectPolicy:               &transformerSelectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Pods",
									Value:         4,
									PeriodSeconds: 15,
								},
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
						ScaleDown: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: nil,
							SelectPolicy:               &transformerSelectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
					},
				},
			}
			Expect(actualTransformerHPA.Spec).To(BeComparableTo(expectedTransformerHPA.Spec))
		})
	})
	Context("When creating inference service with raw kube path based routing predictor and explainer", func() {
		configs := map[string]string{
			"explainers": `{
				"art": {
					"image": "kserve/art-explainer",
					"defaultImageVersion": "latest"
				}
			}`,
			"ingress": `{
				"enableGatewayApi": true,
				"kserveIngressGateway": "kserve/kserve-ingress-gateway",
				"ingressGateway": "knative-serving/knative-ingress-gateway",
				"localGateway": "knative-serving/knative-local-gateway",
				"localGatewayService": "knative-local-gateway.istio-system.svc.cluster.local",
				"additionalIngressDomains": ["additional.example.com"],
				"ingressDomain": "example.com",
				"pathTemplate": "/serving/{{ .Namespace }}/{{ .Name }}"
			}`,
			"storageInitializer": `{
				"image" : "kserve/storage-initializer:latest",
				"memoryRequest": "100Mi",
				"memoryLimit": "1Gi",
				"cpuRequest": "100m",
				"cpuLimit": "1",
				"CaBundleConfigMapName": "",
				"caBundleVolumeMountPath": "/etc/ssl/custom-certs",
				"enableDirectPvcVolumeMount": false
			}`,
		}

		It("Should have httproute/service/deployment/hpa created for explainer and predictor", func() {
			By("By creating a new InferenceService")
			// Create configmap
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.InferenceServiceConfigMapName,
					Namespace: constants.KServeNamespace,
				},
				Data: configs,
			}
			Expect(k8sClient.Create(context.TODO(), configMap)).NotTo(HaveOccurred())
			defer k8sClient.Delete(context.TODO(), configMap)
			// Create ServingRuntime
			servingRuntime := &v1alpha1.ServingRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "tf-serving-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.ServingRuntimeSpec{
					SupportedModelFormats: []v1alpha1.SupportedModelFormat{
						{
							Name:       "tensorflow",
							Version:    proto.String("1"),
							AutoSelect: proto.Bool(true),
						},
					},
					ServingRuntimePodSpec: v1alpha1.ServingRuntimePodSpec{
						Containers: []corev1.Container{
							{
								Name:    "kserve-container",
								Image:   "tensorflow/serving:1.14.0",
								Command: []string{"/usr/bin/tensorflow_model_server"},
								Args: []string{
									"--port=9000",
									"--rest_api_port=8080",
									"--model_base_path=/mnt/models",
									"--rest_api_timeout_in_ms=60000",
								},
								Resources: defaultResource,
							},
						},
					},
					Disabled: proto.Bool(false),
				},
			}
			k8sClient.Create(context.TODO(), servingRuntime)
			defer k8sClient.Delete(context.TODO(), servingRuntime)
			serviceName := "raw-foo-exp-path"
			namespace := "default"
			expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: namespace}}
			serviceKey := expectedRequest.NamespacedName
			predictorServiceKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceName),
				Namespace: namespace,
			}
			explainerServiceKey := types.NamespacedName{
				Name:      constants.ExplainerServiceName(serviceName),
				Namespace: namespace,
			}
			storageUri := "s3://test/mnist/export"
			var minReplicas int32 = 1
			var maxReplicas int32 = 3
			var explainerMinReplicas int32 = 1
			var explainerMaxReplicas int32 = 2
			var explainerCpuUtilization int32 = 80
			var explainerStabilizationWindowSeconds int32 = 0
			ExplainerSelectPolicy := autoscalingv2.MaxChangePolicySelect
			ctx := context.Background()
			isvc := &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: string(constants.AutoscalerClassHPA),
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
							MinReplicas: ptr.To(minReplicas),
							MaxReplicas: maxReplicas,
							AutoScaling: &v1beta1.AutoScalingSpec{
								Metrics: []v1beta1.MetricsSpec{
									{
										Type: v1beta1.ResourceMetricSourceType,
										Resource: &v1beta1.ResourceMetricSource{
											Name: v1beta1.ResourceMetricCPU,
											Target: v1beta1.MetricTarget{
												Type:               v1beta1.UtilizationMetricType,
												AverageUtilization: ptr.To(int32(75)),
											},
										},
									},
								},
							},
						},
						Tensorflow: &v1beta1.TFServingSpec{
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI:     &storageUri,
								RuntimeVersion: proto.String("1.14.0"),
								Container: corev1.Container{
									Name:      constants.InferenceServiceContainerName,
									Resources: defaultResource,
								},
							},
						},
					},
					Explainer: &v1beta1.ExplainerSpec{
						ART: &v1beta1.ARTExplainerSpec{
							Type: v1beta1.ARTSquareAttackExplainer,
							ExplainerExtensionSpec: v1beta1.ExplainerExtensionSpec{
								Config: map[string]string{"nb_classes": "10"},
								Container: corev1.Container{
									Name:      constants.InferenceServiceContainerName,
									Resources: defaultResource,
								},
							},
						},
						ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
							MinReplicas:    ptr.To(explainerMinReplicas),
							MaxReplicas:    explainerMaxReplicas,
							ScaleTarget:    ptr.To(explainerCpuUtilization),
							TimeoutSeconds: ptr.To(int64(30)),
						},
					},
				},
			}
			isvcConfigMap, err := v1beta1.GetInferenceServiceConfigMap(context.Background(), clientset)
			Expect(err).NotTo(HaveOccurred())
			isvcConfig, err := v1beta1.NewInferenceServicesConfig(isvcConfigMap)
			Expect(err).NotTo(HaveOccurred())
			isvc.DefaultInferenceService(isvcConfig, nil, &v1beta1.SecurityConfig{AutoMountServiceAccountToken: false}, nil)
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			defer k8sClient.Delete(ctx, isvc)

			inferenceService := &v1beta1.InferenceService{}

			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, inferenceService)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			// check predictor deployment
			actualPredictorDeployment := &appsv1.Deployment{}
			predictorDeploymentKey := types.NamespacedName{
				Name:      predictorServiceKey.Name,
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorDeploymentKey, actualPredictorDeployment) }, timeout).
				Should(Succeed())
			var replicas int32 = 1
			var revisionHistory int32 = 10
			var progressDeadlineSeconds int32 = 600
			var gracePeriod int64 = 30
			expectedPredictorDeployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorDeploymentKey.Name,
					Namespace: predictorDeploymentKey.Namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "isvc." + predictorDeploymentKey.Name,
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Name:      predictorDeploymentKey.Name,
							Namespace: "default",
							Labels: map[string]string{
								"app":                                 "isvc." + predictorDeploymentKey.Name,
								constants.KServiceComponentLabel:      constants.Predictor.String(),
								constants.InferenceServicePodLabelKey: serviceName,
							},
							Annotations: map[string]string{
								constants.StorageInitializerSourceUriInternalAnnotationKey: *isvc.Spec.Predictor.Model.StorageURI,
								constants.DeploymentMode:                                   string(constants.RawDeployment),
								constants.AutoscalerClass:                                  string(constants.AutoscalerClassHPA),
								constants.OpenshiftServingCertAnnotation:                   predictorDeploymentKey.Name + constants.ServingCertSecretSuffix,
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Image: "tensorflow/serving:" +
										*isvc.Spec.Predictor.Model.RuntimeVersion,
									Name:    constants.InferenceServiceContainerName,
									Command: []string{v1beta1.TensorflowEntrypointCommand},
									Args: []string{
										"--port=" + v1beta1.TensorflowServingGRPCPort,
										"--rest_api_port=" + v1beta1.TensorflowServingRestPort,
										"--model_base_path=" + constants.DefaultModelLocalMountPath,
										"--rest_api_timeout_in_ms=60000",
									},
									Resources: defaultResource,
									ReadinessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											TCPSocket: &corev1.TCPSocketAction{
												Port: intstr.IntOrString{
													IntVal: 8080,
												},
											},
										},
										InitialDelaySeconds: 0,
										TimeoutSeconds:      1,
										PeriodSeconds:       10,
										SuccessThreshold:    1,
										FailureThreshold:    3,
									},
									TerminationMessagePath:   "/dev/termination-log",
									TerminationMessagePolicy: "File",
									ImagePullPolicy:          "IfNotPresent",
								},
							},
							SchedulerName:                 "default-scheduler",
							RestartPolicy:                 "Always",
							TerminationGracePeriodSeconds: &gracePeriod,
							DNSPolicy:                     "ClusterFirst",
							SecurityContext: &corev1.PodSecurityContext{
								SELinuxOptions:      nil,
								WindowsOptions:      nil,
								RunAsUser:           nil,
								RunAsGroup:          nil,
								RunAsNonRoot:        nil,
								SupplementalGroups:  nil,
								FSGroup:             nil,
								Sysctls:             nil,
								FSGroupChangePolicy: nil,
								SeccompProfile:      nil,
							},
							AutomountServiceAccountToken: proto.Bool(false),
						},
					},
					Strategy: appsv1.DeploymentStrategy{
						Type: "RollingUpdate",
						RollingUpdate: &appsv1.RollingUpdateDeployment{
							MaxUnavailable: &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
							MaxSurge:       &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
						},
					},
					RevisionHistoryLimit:    &revisionHistory,
					ProgressDeadlineSeconds: &progressDeadlineSeconds,
				},
			}
			Expect(actualPredictorDeployment.Spec).To(BeComparableTo(expectedPredictorDeployment.Spec))

			// check Explainer deployment
			actualExplainerDeployment := &appsv1.Deployment{}
			explainerDeploymentKey := types.NamespacedName{
				Name:      explainerServiceKey.Name,
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), explainerDeploymentKey, actualExplainerDeployment)
			}, timeout).
				Should(Succeed())
			expectedExplainerDeployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      explainerDeploymentKey.Name,
					Namespace: explainerDeploymentKey.Namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "isvc." + explainerDeploymentKey.Name,
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Name:      explainerDeploymentKey.Name,
							Namespace: "default",
							Labels: map[string]string{
								"app":                                 "isvc." + explainerDeploymentKey.Name,
								constants.KServiceComponentLabel:      constants.Explainer.String(),
								constants.InferenceServicePodLabelKey: serviceName,
							},
							Annotations: map[string]string{
								constants.DeploymentMode:                 string(constants.RawDeployment),
								constants.AutoscalerClass:                string(constants.AutoscalerClassHPA),
								constants.OpenshiftServingCertAnnotation: explainerDeploymentKey.Name + constants.ServingCertSecretSuffix,
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Image: "kserve/art-explainer:latest",
									Name:  constants.InferenceServiceContainerName,
									Args: []string{
										"--model_name",
										serviceKey.Name,
										"--http_port",
										"8080",
										"--predictor_host",
										fmt.Sprintf("%s.%s", predictorServiceKey.Name, predictorServiceKey.Namespace),
										"--adversary_type",
										"SquareAttack",
										"--nb_classes",
										"10",
									},
									Resources: defaultResource,
									ReadinessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											TCPSocket: &corev1.TCPSocketAction{
												Port: intstr.IntOrString{
													IntVal: 8080,
												},
											},
										},
										InitialDelaySeconds: 0,
										TimeoutSeconds:      1,
										PeriodSeconds:       10,
										SuccessThreshold:    1,
										FailureThreshold:    3,
									},
									TerminationMessagePath:   "/dev/termination-log",
									TerminationMessagePolicy: "File",
									ImagePullPolicy:          "IfNotPresent",
								},
							},
							SchedulerName:                 "default-scheduler",
							RestartPolicy:                 "Always",
							TerminationGracePeriodSeconds: &gracePeriod,
							DNSPolicy:                     "ClusterFirst",
							SecurityContext: &corev1.PodSecurityContext{
								SELinuxOptions:      nil,
								WindowsOptions:      nil,
								RunAsUser:           nil,
								RunAsGroup:          nil,
								RunAsNonRoot:        nil,
								SupplementalGroups:  nil,
								FSGroup:             nil,
								Sysctls:             nil,
								FSGroupChangePolicy: nil,
								SeccompProfile:      nil,
							},
							AutomountServiceAccountToken: proto.Bool(false),
						},
					},
					Strategy: appsv1.DeploymentStrategy{
						Type: "RollingUpdate",
						RollingUpdate: &appsv1.RollingUpdateDeployment{
							MaxUnavailable: &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
							MaxSurge:       &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
						},
					},
					RevisionHistoryLimit:    &revisionHistory,
					ProgressDeadlineSeconds: &progressDeadlineSeconds,
				},
			}
			Expect(actualExplainerDeployment.Spec).To(BeComparableTo(expectedExplainerDeployment.Spec))

			// check predictor service
			actualPredictorService := &corev1.Service{}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorServiceKey, actualPredictorService) }, timeout).
				Should(Succeed())
			expectedPredictorService := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorServiceKey.Name,
					Namespace: predictorServiceKey.Namespace,
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       predictorServiceKey.Name,
							Protocol:   "TCP",
							Port:       80,
							TargetPort: intstr.IntOrString{Type: 0, IntVal: 8080, StrVal: ""},
						},
					},
					Type:            "ClusterIP",
					SessionAffinity: "None",
					Selector: map[string]string{
						"app": "isvc." + predictorServiceKey.Name,
					},
				},
			}
			actualPredictorService.Spec.ClusterIP = ""
			actualPredictorService.Spec.ClusterIPs = nil
			actualPredictorService.Spec.IPFamilies = nil
			actualPredictorService.Spec.IPFamilyPolicy = nil
			actualPredictorService.Spec.InternalTrafficPolicy = nil
			Expect(actualPredictorService.Spec).To(BeComparableTo(expectedPredictorService.Spec))

			// check Explainer service
			actualExplainerService := &corev1.Service{}
			Eventually(func() error { return k8sClient.Get(context.TODO(), explainerServiceKey, actualExplainerService) }, timeout).
				Should(Succeed())
			expectedExplainerService := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      explainerServiceKey.Name,
					Namespace: explainerServiceKey.Namespace,
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       explainerServiceKey.Name,
							Protocol:   "TCP",
							Port:       80,
							TargetPort: intstr.IntOrString{Type: 0, IntVal: 8080, StrVal: ""},
						},
					},
					Type:            "ClusterIP",
					SessionAffinity: "None",
					Selector: map[string]string{
						"app": "isvc." + explainerServiceKey.Name,
					},
				},
			}
			actualExplainerService.Spec.ClusterIP = ""
			actualExplainerService.Spec.ClusterIPs = nil
			actualExplainerService.Spec.IPFamilies = nil
			actualExplainerService.Spec.IPFamilyPolicy = nil
			actualExplainerService.Spec.InternalTrafficPolicy = nil
			Expect(actualExplainerService.Spec).To(BeComparableTo(expectedExplainerService.Spec))

			// update deployment status to make isvc ready
			updatedPredictorDeployment := actualPredictorDeployment.DeepCopy()
			updatedPredictorDeployment.Status.Conditions = []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: corev1.ConditionTrue,
				},
			}
			Expect(k8sClient.Status().Update(context.TODO(), updatedPredictorDeployment)).NotTo(HaveOccurred())
			updatedExplainerDeployment := actualExplainerDeployment.DeepCopy()
			updatedExplainerDeployment.Status.Conditions = []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: corev1.ConditionTrue,
				},
			}
			Expect(k8sClient.Status().Update(context.TODO(), updatedExplainerDeployment)).NotTo(HaveOccurred())

			// check http route
			actualToplevelHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualToplevelHttpRoute)
			}, timeout).
				Should(Succeed())
			topLevelHost := fmt.Sprintf("%s-%s.%s", serviceKey.Name, serviceKey.Namespace, "example.com")
			prefixUrlPath := fmt.Sprintf("/serving/%s/%s", serviceKey.Namespace, serviceKey.Name)
			expectedToplevelHttpRoute := gatewayapiv1.HTTPRoute{
				Spec: gatewayapiv1.HTTPRouteSpec{
					Hostnames: []gatewayapiv1.Hostname{gatewayapiv1.Hostname(topLevelHost), gatewayapiv1.Hostname(fmt.Sprintf("%s-%s.additional.example.com", serviceKey.Name, serviceKey.Namespace)), "example.com"},
					Rules: []gatewayapiv1.HTTPRouteRule{
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.ExplainPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(explainerServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("30s")),
							},
						},
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.FallbackPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(predictorServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("60s")),
							},
						},
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(prefixUrlPath + constants.PathBasedExplainPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(explainerServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("30s")),
							},
						},
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(prefixUrlPath + "/"),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(predictorServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("60s")),
							},
						},
					},
					CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
						ParentRefs: []gatewayapiv1.ParentReference{
							{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
						},
					},
				},
			}
			Expect(actualToplevelHttpRoute.Spec).To(BeComparableTo(expectedToplevelHttpRoute.Spec))

			actualPredictorHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      predictorServiceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualPredictorHttpRoute)
			}, timeout).
				Should(Succeed())
			predictorHost := fmt.Sprintf("%s-%s.%s", predictorServiceKey.Name, serviceKey.Namespace, "example.com")
			expectedPredictorHttpRoute := gatewayapiv1.HTTPRoute{
				Spec: gatewayapiv1.HTTPRouteSpec{
					Hostnames: []gatewayapiv1.Hostname{gatewayapiv1.Hostname(predictorHost)},
					Rules: []gatewayapiv1.HTTPRouteRule{
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.FallbackPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(predictorServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("60s")),
							},
						},
					},
					CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
						ParentRefs: []gatewayapiv1.ParentReference{
							{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
						},
					},
				},
			}
			Expect(actualPredictorHttpRoute.Spec).To(BeComparableTo(expectedPredictorHttpRoute.Spec))

			actualExplainerHttpRoute := &gatewayapiv1.HTTPRoute{}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), types.NamespacedName{
					Name:      explainerServiceKey.Name,
					Namespace: serviceKey.Namespace,
				}, actualExplainerHttpRoute)
			}, timeout).
				Should(Succeed())
			explainerHost := fmt.Sprintf("%s-%s.%s", explainerServiceKey.Name, serviceKey.Namespace, "example.com")
			expectedExplainerHttpRoute := gatewayapiv1.HTTPRoute{
				Spec: gatewayapiv1.HTTPRouteSpec{
					Hostnames: []gatewayapiv1.Hostname{gatewayapiv1.Hostname(explainerHost)},
					Rules: []gatewayapiv1.HTTPRouteRule{
						{
							Matches: []gatewayapiv1.HTTPRouteMatch{
								{
									Path: &gatewayapiv1.HTTPPathMatch{
										Type:  ptr.To(gatewayapiv1.PathMatchRegularExpression),
										Value: ptr.To(constants.FallbackPrefix()),
									},
								},
							},
							Filters: []gatewayapiv1.HTTPRouteFilter{
								{
									Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
									RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
										Set: []gatewayapiv1.HTTPHeader{
											{
												Name:  constants.IsvcNameHeader,
												Value: serviceKey.Name,
											},
											{
												Name:  constants.IsvcNamespaceHeader,
												Value: serviceKey.Namespace,
											},
										},
									},
								},
							},
							BackendRefs: []gatewayapiv1.HTTPBackendRef{
								{
									BackendRef: gatewayapiv1.BackendRef{
										BackendObjectReference: gatewayapiv1.BackendObjectReference{
											Group:     (*gatewayapiv1.Group)(ptr.To("")),
											Kind:      ptr.To(gatewayapiv1.Kind(constants.ServiceKind)),
											Name:      gatewayapiv1.ObjectName(explainerServiceKey.Name),
											Namespace: (*gatewayapiv1.Namespace)(ptr.To(serviceKey.Namespace)),
											Port:      (*gatewayapiv1.PortNumber)(ptr.To(int32(constants.CommonDefaultHttpPort))),
										},
										Weight: ptr.To(int32(1)),
									},
								},
							},
							Timeouts: &gatewayapiv1.HTTPRouteTimeouts{
								Request: ptr.To(gatewayapiv1.Duration("30s")),
							},
						},
					},
					CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
						ParentRefs: []gatewayapiv1.ParentReference{
							{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
						},
					},
				},
			}
			Expect(actualExplainerHttpRoute.Spec).To(BeComparableTo(expectedExplainerHttpRoute.Spec))

			// Mark the Ingress as accepted to make isvc ready
			httpRouteStatus := gatewayapiv1.HTTPRouteStatus{
				RouteStatus: gatewayapiv1.RouteStatus{
					Parents: []gatewayapiv1.RouteParentStatus{
						{
							ParentRef: gatewayapiv1.ParentReference{
								Name:      gatewayapiv1.ObjectName(kserveGateway.Name),
								Kind:      ptr.To(gatewayapiv1.Kind(constants.GatewayKind)),
								Group:     (*gatewayapiv1.Group)(&gatewayapiv1.GroupVersion.Group),
								Namespace: ptr.To(gatewayapiv1.Namespace(kserveGateway.Namespace)),
							},
							ControllerName: "istio.io/gateway-controller",
							Conditions: []metav1.Condition{
								{
									Type:               string(gatewayapiv1.ListenerConditionAccepted),
									Status:             metav1.ConditionTrue,
									Reason:             "Accepted",
									Message:            "Route was valid",
									LastTransitionTime: metav1.Now(),
								},
							},
						},
					},
				},
			}
			actualPredictorHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(context.Background(), actualPredictorHttpRoute)).NotTo(HaveOccurred())
			actualExplainerHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(context.Background(), actualExplainerHttpRoute)).NotTo(HaveOccurred())
			actualToplevelHttpRoute.Status = httpRouteStatus
			Expect(k8sClient.Status().Update(context.Background(), actualToplevelHttpRoute)).NotTo(HaveOccurred())

			// verify if InferenceService status is updated
			expectedIsvcStatus := v1beta1.InferenceServiceStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:     v1beta1.ExplainerReady,
							Status:   "True",
							Severity: "Info",
						},
						{
							Type:   v1beta1.IngressReady,
							Status: "True",
						},
						{
							Type:   v1beta1.PredictorReady,
							Status: "True",
						},
						{
							Type:   apis.ConditionReady,
							Status: "True",
						},
						{
							Type:     v1beta1.Stopped,
							Status:   "False",
							Severity: apis.ConditionSeverityInfo,
						},
					},
				},
				URL: &apis.URL{
					Scheme: "http",
					Host:   fmt.Sprintf("%s-%s.example.com", serviceKey.Name, serviceKey.Namespace),
				},
				Address: &duckv1.Addressable{
					URL: &apis.URL{
						Scheme: "http",
						Host:   fmt.Sprintf("%s.%s.svc.cluster.local", predictorServiceKey.Name, serviceKey.Namespace),
					},
				},
				Components: map[v1beta1.ComponentType]v1beta1.ComponentStatusSpec{
					v1beta1.PredictorComponent: {
						LatestCreatedRevision: "",
						// URL: &apis.URL{
						// 	Scheme: "http",
						// 	Host:   fmt.Sprintf("%s-%s.example.com", predictorServiceKey.Name, serviceKey.Namespace),
						// },
					},
					v1beta1.ExplainerComponent: {
						LatestCreatedRevision: "",
						// URL: &apis.URL{
						// 	Scheme: "http",
						// 	Host:   fmt.Sprintf("%s-%s.example.com", explainerServiceKey.Name, serviceKey.Namespace),
						// },
					},
				},
				ModelStatus: v1beta1.ModelStatus{
					TransitionStatus:    "InProgress",
					ModelRevisionStates: &v1beta1.ModelRevisionStates{TargetModelState: "Pending"},
				},
				DeploymentMode: string(constants.RawDeployment),
			}
			Eventually(func() string {
				isvc := &v1beta1.InferenceService{}
				if err := k8sClient.Get(context.TODO(), serviceKey, isvc); err != nil {
					return err.Error()
				}
				return cmp.Diff(&expectedIsvcStatus, &isvc.Status, cmpopts.IgnoreTypes(apis.VolatileTime{}))
			}, timeout).Should(BeEmpty())

			// check predictor HPA
			var cpuUtilization int32 = 75
			var stabilizationWindowSeconds int32 = 0
			selectPolicy := autoscalingv2.MaxChangePolicySelect
			actualPredictorHPA := &autoscalingv2.HorizontalPodAutoscaler{}
			predictorHPAKey := types.NamespacedName{
				Name:      predictorServiceKey.Name,
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorHPAKey, actualPredictorHPA) }, timeout).
				Should(Succeed())
			expectedPredictorHPA := &autoscalingv2.HorizontalPodAutoscaler{
				Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
					ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       predictorServiceKey.Name,
					},
					MinReplicas: &minReplicas,
					MaxReplicas: maxReplicas,
					Metrics: []autoscalingv2.MetricSpec{
						{
							Type: autoscalingv2.ResourceMetricSourceType,
							Resource: &autoscalingv2.ResourceMetricSource{
								Name: corev1.ResourceCPU,
								Target: autoscalingv2.MetricTarget{
									Type:               "Utilization",
									AverageUtilization: &cpuUtilization,
								},
							},
						},
					},
					Behavior: &autoscalingv2.HorizontalPodAutoscalerBehavior{
						ScaleUp: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: &stabilizationWindowSeconds,
							SelectPolicy:               &selectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Pods",
									Value:         4,
									PeriodSeconds: 15,
								},
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
						ScaleDown: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: nil,
							SelectPolicy:               &selectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
					},
				},
			}
			Expect(actualPredictorHPA.Spec).To(BeComparableTo(expectedPredictorHPA.Spec))

			// check Explainer HPA
			actualExplainerHPA := &autoscalingv2.HorizontalPodAutoscaler{}
			explainerHPAKey := types.NamespacedName{
				Name:      explainerServiceKey.Name,
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), explainerHPAKey, actualExplainerHPA) }, timeout).
				Should(Succeed())
			expectedExplainerHPA := &autoscalingv2.HorizontalPodAutoscaler{
				Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
					ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       explainerServiceKey.Name,
					},
					MinReplicas: &explainerMinReplicas,
					MaxReplicas: explainerMaxReplicas,
					Metrics: []autoscalingv2.MetricSpec{
						{
							Type: autoscalingv2.ResourceMetricSourceType,
							Resource: &autoscalingv2.ResourceMetricSource{
								Name: corev1.ResourceCPU,
								Target: autoscalingv2.MetricTarget{
									Type:               "Utilization",
									AverageUtilization: &explainerCpuUtilization,
								},
							},
						},
					},
					Behavior: &autoscalingv2.HorizontalPodAutoscalerBehavior{
						ScaleUp: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: &explainerStabilizationWindowSeconds,
							SelectPolicy:               &ExplainerSelectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Pods",
									Value:         4,
									PeriodSeconds: 15,
								},
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
						ScaleDown: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: nil,
							SelectPolicy:               &ExplainerSelectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
					},
				},
			}
			Expect(actualExplainerHPA.Spec).To(BeComparableTo(expectedExplainerHPA.Spec))
		})
	})
	Context("When creating inference service with raw kube predictor with gateway api disabled", func() {
		configs := map[string]string{
			"explainers": `{
				"alibi": {
					"image": "kserve/alibi-explainer",
					"defaultImageVersion": "latest"
				}
			}`,
			"ingress": `{
				"enableGatewayAPI": false,
				"ingressGateway": "knative-serving/knative-ingress-gateway",
				"localGateway": "knative-serving/knative-local-gateway",
				"localGatewayService": "knative-local-gateway.istio-system.svc.cluster.local"
			}`,
			"storageInitializer": `{
				"image" : "kserve/storage-initializer:latest",
				"memoryRequest": "100Mi",
				"memoryLimit": "1Gi",
				"cpuRequest": "100m",
				"cpuLimit": "1",
				"CaBundleConfigMapName": "",
				"caBundleVolumeMountPath": "/etc/ssl/custom-certs",
				"enableDirectPvcVolumeMount": false
			}`,
			"opentelemetryCollector": `{
				"scrapeInterval": "5s",
				"metricReceiverEndpoint": "keda-otel-scaler.keda.svc:4317",
				"metricScalerEndpoint": "keda-otel-scaler.keda.svc:4318"
			}`,
		}
		It("Should have KEDA ScaledObject created", func() {
			By("By creating a new InferenceService")
			// Create configmap
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.InferenceServiceConfigMapName,
					Namespace: constants.KServeNamespace,
				},
				Data: configs,
			}
			Expect(k8sClient.Create(context.TODO(), configMap)).NotTo(HaveOccurred())
			defer k8sClient.Delete(context.TODO(), configMap)
			// Create ServingRuntime
			servingRuntime := &v1alpha1.ServingRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "tf-serving-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.ServingRuntimeSpec{
					SupportedModelFormats: []v1alpha1.SupportedModelFormat{
						{
							Name:       "tensorflow",
							Version:    proto.String("1"),
							AutoSelect: proto.Bool(true),
						},
					},
					ServingRuntimePodSpec: v1alpha1.ServingRuntimePodSpec{
						Containers: []corev1.Container{
							{
								Name:    "kserve-container",
								Image:   "tensorflow/serving:1.14.0",
								Command: []string{"/usr/bin/tensorflow_model_server"},
								Args: []string{
									"--port=9000",
									"--rest_api_port=8080",
									"--model_base_path=/mnt/models",
									"--rest_api_timeout_in_ms=60000",
								},
								Resources: defaultResource,
							},
						},
					},
					Disabled: proto.Bool(false),
				},
			}
			k8sClient.Create(context.TODO(), servingRuntime)
			defer k8sClient.Delete(context.TODO(), servingRuntime)
			serviceName := "raw-foo-1"
			expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: "default"}}
			serviceKey := expectedRequest.NamespacedName
			storageUri := "s3://test/mnist/export"
			qty := resource.MustParse("10Gi")
			ctx := context.Background()
			isvc := &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: string(constants.AutoscalerClassKeda),
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
							MinReplicas: ptr.To(int32(1)),
							MaxReplicas: 3,
							AutoScaling: &v1beta1.AutoScalingSpec{
								Metrics: []v1beta1.MetricsSpec{
									{
										Type: v1beta1.ResourceMetricSourceType,
										Resource: &v1beta1.ResourceMetricSource{
											Name: v1beta1.ResourceMetricMemory,
											Target: v1beta1.MetricTarget{
												Type:         v1beta1.AverageValueMetricType,
												AverageValue: &qty,
											},
										},
									},
								},
							},
						},
						Tensorflow: &v1beta1.TFServingSpec{
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI:     &storageUri,
								RuntimeVersion: proto.String("1.14.0"),
								Container: corev1.Container{
									Name:      constants.InferenceServiceContainerName,
									Resources: defaultResource,
								},
							},
						},
					},
				},
			}
			isvc.DefaultInferenceService(nil, nil, nil, nil)
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())

			inferenceService := &v1beta1.InferenceService{}

			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, inferenceService)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			actualScaledObject := &kedav1alpha1.ScaledObject{}

			predictorSObjectKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorSObjectKey, actualScaledObject) }, timeout).
				Should(Succeed())

			expectedScaledobject := &kedav1alpha1.ScaledObject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorSObjectKey.Name,
					Namespace: predictorSObjectKey.Namespace,
				},
				Spec: kedav1alpha1.ScaledObjectSpec{
					ScaleTargetRef: &kedav1alpha1.ScaleTarget{
						Name: predictorSObjectKey.Name,
					},
					Triggers: []kedav1alpha1.ScaleTriggers{
						{
							Type: "memory",
							Metadata: map[string]string{
								"value": "10Gi",
							},
							MetricType: autoscalingv2.AverageValueMetricType,
						},
					},
					MinReplicaCount: proto.Int32(1),
					MaxReplicaCount: proto.Int32(3),
				},
			}
			Expect(actualScaledObject.Spec).To(Equal(expectedScaledobject.Spec))

			// check service
			actualService := &corev1.Service{}
			predictorServiceKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorServiceKey, actualService) }, timeout).
				Should(Succeed())

			expectedService := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorServiceKey.Name,
					Namespace: predictorServiceKey.Namespace,
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       constants.PredictorServiceName(serviceName),
							Protocol:   "TCP",
							Port:       80,
							TargetPort: intstr.IntOrString{Type: 0, IntVal: 8080, StrVal: ""},
						},
					},
					Type:            "ClusterIP",
					SessionAffinity: "None",
					Selector: map[string]string{
						"app": "isvc." + constants.PredictorServiceName(serviceName),
					},
				},
			}
			actualService.Spec.ClusterIP = ""
			actualService.Spec.ClusterIPs = nil
			actualService.Spec.IPFamilies = nil
			actualService.Spec.IPFamilyPolicy = nil
			actualService.Spec.InternalTrafficPolicy = nil
			Expect(actualService.Spec).To(Equal(expectedService.Spec))

			// check isvc status
			updatedScaledObject := actualScaledObject.DeepCopy()
			updatedScaledObject.Status.Conditions = []kedav1alpha1.Condition{
				{
					Type:   kedav1alpha1.ConditionReady,
					Status: kedav1alpha1.GetInitializedConditions().GetActiveCondition().Status,
				},
			}
			Expect(k8sClient.Status().Update(context.TODO(), updatedScaledObject)).NotTo(HaveOccurred())
		})

		It("Should have OpenTelemetry Collector created", func() {
			By("By creating a new InferenceService")
			// Create configmap
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.InferenceServiceConfigMapName,
					Namespace: constants.KServeNamespace,
				},
				Data: configs,
			}
			Expect(k8sClient.Create(context.TODO(), configMap)).NotTo(HaveOccurred())
			defer k8sClient.Delete(context.TODO(), configMap)
			// Create ServingRuntime
			servingRuntime := &v1alpha1.ServingRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "tf-serving-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.ServingRuntimeSpec{
					SupportedModelFormats: []v1alpha1.SupportedModelFormat{
						{
							Name:       "tensorflow",
							Version:    proto.String("1"),
							AutoSelect: proto.Bool(true),
						},
					},
					ServingRuntimePodSpec: v1alpha1.ServingRuntimePodSpec{
						Containers: []corev1.Container{
							{
								Name:    "kserve-container",
								Image:   "tensorflow/serving:1.14.0",
								Command: []string{"/usr/bin/tensorflow_model_server"},
								Args: []string{
									"--port=9000",
									"--rest_api_port=8080",
									"--model_base_path=/mnt/models",
									"--rest_api_timeout_in_ms=60000",
								},
								Resources: defaultResource,
							},
						},
					},
					Disabled: proto.Bool(false),
				},
			}
			k8sClient.Create(context.TODO(), servingRuntime)
			defer k8sClient.Delete(context.TODO(), servingRuntime)
			serviceName := "raw-foo-3"
			expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: "default"}}
			serviceKey := expectedRequest.NamespacedName
			storageUri := "s3://test/mnist/export"
			ctx := context.Background()
			isvc := &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
					Annotations: map[string]string{
						constants.DeploymentMode:          string(constants.RawDeployment),
						constants.AutoscalerClass:         string(constants.AutoscalerClassKeda),
						"sidecar.opentelemetry.io/inject": "true",
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
							MinReplicas: ptr.To(int32(1)),
							MaxReplicas: 3,
							AutoScaling: &v1beta1.AutoScalingSpec{
								Metrics: []v1beta1.MetricsSpec{
									{
										Type: v1beta1.PodMetricSourceType,
										PodMetric: &v1beta1.PodMetricSource{
											Metric: v1beta1.PodMetrics{
												Backend:     v1beta1.OpenTelemetryBackend,
												MetricNames: []string{"process_cpu_seconds_total"},
												Query:       "avg(process_cpu_seconds_total)",
											},
											Target: v1beta1.MetricTarget{
												Type:  v1beta1.ValueMetricType,
												Value: &resource.Quantity{},
											},
										},
									},
								},
							},
						},

						Tensorflow: &v1beta1.TFServingSpec{
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI:     &storageUri,
								RuntimeVersion: proto.String("1.14.0"),
								Container: corev1.Container{
									Name:      constants.InferenceServiceContainerName,
									Resources: defaultResource,
								},
							},
						},
					},
				},
			}
			isvc.DefaultInferenceService(nil, nil, nil, nil)
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())

			inferenceService := &v1beta1.InferenceService{}

			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, inferenceService)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			actualOTelCollector := &otelv1beta1.OpenTelemetryCollector{}

			predictorSObjectKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorSObjectKey, actualOTelCollector) }, timeout).
				Should(Succeed())

			expectedOTelCollector := &otelv1beta1.OpenTelemetryCollector{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorSObjectKey.Name,
					Namespace: predictorSObjectKey.Namespace,
				},
				Spec: otelv1beta1.OpenTelemetryCollectorSpec{
					Mode: "sidecar",
					Config: otelv1beta1.Config{
						Receivers: otelv1beta1.AnyConfig{Object: map[string]interface{}{
							"prometheus": map[string]interface{}{
								"config": map[string]interface{}{
									"scrape_configs": []interface{}{
										map[string]interface{}{
											"job_name":        "otel-collector",
											"scrape_interval": "5s",
											"static_configs": []interface{}{
												map[string]interface{}{
													"targets": []interface{}{"localhost:8080"},
												},
											},
										},
									},
								},
							},
						}},
						Processors: &otelv1beta1.AnyConfig{Object: map[string]interface{}{
							"filter/ottl": map[string]interface{}{
								"error_mode": "ignore",
								"metrics": map[string]interface{}{
									"metric": []interface{}{
										`name != "process_cpu_seconds_total"`,
									},
								},
							},
						}},
						Exporters: otelv1beta1.AnyConfig{Object: map[string]interface{}{
							"otlp": map[string]interface{}{
								"endpoint":    "keda-otel-scaler.keda.svc:4317",
								"compression": "none",
								"tls": map[string]interface{}{
									"insecure": true,
								},
							},
						}},
						Service: otelv1beta1.Service{
							Pipelines: map[string]*otelv1beta1.Pipeline{
								"metrics": {
									Receivers:  []string{"prometheus"},
									Processors: []string{"filter/ottl"},
									Exporters:  []string{"otlp"},
								},
							},
						},
					},
				},
			}
			Expect(actualOTelCollector.Spec.Config).To(BeComparableTo(expectedOTelCollector.Spec.Config))
		})

		It("Should have ingress/service/deployment/hpa created", func() {
			By("By creating a new InferenceService")
			// Create configmap
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.InferenceServiceConfigMapName,
					Namespace: constants.KServeNamespace,
				},
				Data: configs,
			}
			Expect(k8sClient.Create(context.TODO(), configMap)).NotTo(HaveOccurred())
			defer k8sClient.Delete(context.TODO(), configMap)
			// Create ServingRuntime
			servingRuntime := &v1alpha1.ServingRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "tf-serving-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.ServingRuntimeSpec{
					SupportedModelFormats: []v1alpha1.SupportedModelFormat{
						{
							Name:       "tensorflow",
							Version:    proto.String("1"),
							AutoSelect: proto.Bool(true),
						},
					},
					ServingRuntimePodSpec: v1alpha1.ServingRuntimePodSpec{
						Containers: []corev1.Container{
							{
								Name:    "kserve-container",
								Image:   "tensorflow/serving:1.14.0",
								Command: []string{"/usr/bin/tensorflow_model_server"},
								Args: []string{
									"--port=9000",
									"--rest_api_port=8080",
									"--model_base_path=/mnt/models",
									"--rest_api_timeout_in_ms=60000",
								},
								Resources: defaultResource,
							},
						},
					},
					Disabled: proto.Bool(false),
				},
			}
			k8sClient.Create(context.TODO(), servingRuntime)
			defer k8sClient.Delete(context.TODO(), servingRuntime)
			serviceName := "raw-foo"
			expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: "default"}}
			serviceKey := expectedRequest.NamespacedName
			storageUri := "s3://test/mnist/export"
			ctx := context.Background()
			isvc := &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: string(constants.AutoscalerClassHPA),
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
							MinReplicas: ptr.To(int32(1)),
							MaxReplicas: 3,
							AutoScaling: &v1beta1.AutoScalingSpec{
								Metrics: []v1beta1.MetricsSpec{
									{
										Type: v1beta1.ResourceMetricSourceType,
										Resource: &v1beta1.ResourceMetricSource{
											Name: v1beta1.ResourceMetricCPU,
											Target: v1beta1.MetricTarget{
												Type:               v1beta1.UtilizationMetricType,
												AverageUtilization: ptr.To(int32(75)),
											},
										},
									},
								},
							},
						},
						Tensorflow: &v1beta1.TFServingSpec{
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI:     &storageUri,
								RuntimeVersion: proto.String("1.14.0"),
								Container: corev1.Container{
									Name:      constants.InferenceServiceContainerName,
									Resources: defaultResource,
								},
							},
						},
					},
				},
			}
			isvc.DefaultInferenceService(nil, nil, &v1beta1.SecurityConfig{AutoMountServiceAccountToken: false}, nil)
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())

			inferenceService := &v1beta1.InferenceService{}

			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, inferenceService)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			actualDeployment := &appsv1.Deployment{}
			predictorDeploymentKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorDeploymentKey, actualDeployment) }, timeout).
				Should(Succeed())
			var replicas int32 = 1
			var revisionHistory int32 = 10
			var progressDeadlineSeconds int32 = 600
			var gracePeriod int64 = 30
			expectedDeployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorDeploymentKey.Name,
					Namespace: predictorDeploymentKey.Namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "isvc." + predictorDeploymentKey.Name,
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Name:      predictorDeploymentKey.Name,
							Namespace: "default",
							Labels: map[string]string{
								"app":                                 "isvc." + predictorDeploymentKey.Name,
								constants.KServiceComponentLabel:      constants.Predictor.String(),
								constants.InferenceServicePodLabelKey: serviceName,
							},
							Annotations: map[string]string{
								constants.StorageInitializerSourceUriInternalAnnotationKey: *isvc.Spec.Predictor.Model.StorageURI,
								constants.DeploymentMode:                                   string(constants.RawDeployment),
								constants.AutoscalerClass:                                  string(constants.AutoscalerClassHPA),
								constants.OpenshiftServingCertAnnotation:                   predictorDeploymentKey.Name + constants.ServingCertSecretSuffix,
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Image: "tensorflow/serving:" +
										*isvc.Spec.Predictor.Model.RuntimeVersion,
									Name:    constants.InferenceServiceContainerName,
									Command: []string{v1beta1.TensorflowEntrypointCommand},
									Args: []string{
										"--port=" + v1beta1.TensorflowServingGRPCPort,
										"--rest_api_port=" + v1beta1.TensorflowServingRestPort,
										"--model_base_path=" + constants.DefaultModelLocalMountPath,
										"--rest_api_timeout_in_ms=60000",
									},
									Resources: defaultResource,
									ReadinessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											TCPSocket: &corev1.TCPSocketAction{
												Port: intstr.IntOrString{
													IntVal: 8080,
												},
											},
										},
										InitialDelaySeconds: 0,
										TimeoutSeconds:      1,
										PeriodSeconds:       10,
										SuccessThreshold:    1,
										FailureThreshold:    3,
									},
									TerminationMessagePath:   "/dev/termination-log",
									TerminationMessagePolicy: "File",
									ImagePullPolicy:          "IfNotPresent",
								},
							},
							SchedulerName:                 "default-scheduler",
							RestartPolicy:                 "Always",
							TerminationGracePeriodSeconds: &gracePeriod,
							DNSPolicy:                     "ClusterFirst",
							SecurityContext: &corev1.PodSecurityContext{
								SELinuxOptions:      nil,
								WindowsOptions:      nil,
								RunAsUser:           nil,
								RunAsGroup:          nil,
								RunAsNonRoot:        nil,
								SupplementalGroups:  nil,
								FSGroup:             nil,
								Sysctls:             nil,
								FSGroupChangePolicy: nil,
								SeccompProfile:      nil,
							},
							AutomountServiceAccountToken: proto.Bool(false),
						},
					},
					Strategy: appsv1.DeploymentStrategy{
						Type: "RollingUpdate",
						RollingUpdate: &appsv1.RollingUpdateDeployment{
							MaxUnavailable: &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
							MaxSurge:       &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
						},
					},
					RevisionHistoryLimit:    &revisionHistory,
					ProgressDeadlineSeconds: &progressDeadlineSeconds,
				},
			}
			Expect(actualDeployment.Spec).To(Equal(expectedDeployment.Spec))

			// check service
			actualService := &corev1.Service{}
			predictorServiceKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorServiceKey, actualService) }, timeout).
				Should(Succeed())

			expectedService := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorServiceKey.Name,
					Namespace: predictorServiceKey.Namespace,
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       constants.PredictorServiceName(serviceName),
							Protocol:   "TCP",
							Port:       80,
							TargetPort: intstr.IntOrString{Type: 0, IntVal: 8080, StrVal: ""},
						},
					},
					Type:            "ClusterIP",
					SessionAffinity: "None",
					Selector: map[string]string{
						"app": "isvc." + constants.PredictorServiceName(serviceName),
					},
				},
			}
			actualService.Spec.ClusterIP = ""
			actualService.Spec.ClusterIPs = nil
			actualService.Spec.IPFamilies = nil
			actualService.Spec.IPFamilyPolicy = nil
			actualService.Spec.InternalTrafficPolicy = nil
			Expect(actualService.Spec).To(Equal(expectedService.Spec))

			// check isvc status
			updatedDeployment := actualDeployment.DeepCopy()
			updatedDeployment.Status.Conditions = []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: corev1.ConditionTrue,
				},
			}
			Expect(k8sClient.Status().Update(context.TODO(), updatedDeployment)).NotTo(HaveOccurred())

			// check ingress
			pathType := netv1.PathTypePrefix
			actualIngress := &netv1.Ingress{}
			predictorIngressKey := types.NamespacedName{
				Name:      serviceKey.Name,
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorIngressKey, actualIngress) }, timeout).
				Should(Succeed())
			expectedIngress := netv1.Ingress{
				Spec: netv1.IngressSpec{
					Rules: []netv1.IngressRule{
						{
							Host: fmt.Sprintf("%s-%s.%s", serviceName, serviceKey.Namespace, domain),
							IngressRuleValue: netv1.IngressRuleValue{
								HTTP: &netv1.HTTPIngressRuleValue{
									Paths: []netv1.HTTPIngressPath{
										{
											Path:     "/",
											PathType: &pathType,
											Backend: netv1.IngressBackend{
												Service: &netv1.IngressServiceBackend{
													Name: serviceName + "-predictor",
													Port: netv1.ServiceBackendPort{
														Number: 80,
													},
												},
											},
										},
									},
								},
							},
						},
						{
							Host: fmt.Sprintf("%s-predictor-%s.%s", serviceName, serviceKey.Namespace, domain),
							IngressRuleValue: netv1.IngressRuleValue{
								HTTP: &netv1.HTTPIngressRuleValue{
									Paths: []netv1.HTTPIngressPath{
										{
											Path:     "/",
											PathType: &pathType,
											Backend: netv1.IngressBackend{
												Service: &netv1.IngressServiceBackend{
													Name: serviceName + "-predictor",
													Port: netv1.ServiceBackendPort{
														Number: 80,
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(actualIngress.Spec).To(Equal(expectedIngress.Spec))
			// verify if InferenceService status is updated
			expectedIsvcStatus := v1beta1.InferenceServiceStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:   v1beta1.IngressReady,
							Status: "True",
						},
						{
							Type:   v1beta1.PredictorReady,
							Status: "True",
						},
						{
							Type:   apis.ConditionReady,
							Status: "True",
						},
						{
							Type:     v1beta1.Stopped,
							Status:   "False",
							Severity: apis.ConditionSeverityInfo,
						},
					},
				},
				URL: &apis.URL{
					Scheme: "http",
					Host:   fmt.Sprintf("%s-predictor.%s.svc.cluster.local", serviceKey.Name, serviceKey.Namespace),
				},
				Address: &duckv1.Addressable{
					URL: &apis.URL{
						Scheme: "http",
						Host:   fmt.Sprintf("%s-predictor.%s.svc.cluster.local", serviceKey.Name, serviceKey.Namespace),
					},
				},
				Components: map[v1beta1.ComponentType]v1beta1.ComponentStatusSpec{
					v1beta1.PredictorComponent: {
						LatestCreatedRevision: "",
						// URL: &apis.URL{
						// 	Scheme: "http",
						// 	Host:   "raw-foo-predictor-default.example.com",
						// },
					},
				},
				ModelStatus: v1beta1.ModelStatus{
					TransitionStatus:    "InProgress",
					ModelRevisionStates: &v1beta1.ModelRevisionStates{TargetModelState: "Pending"},
				},
				DeploymentMode: string(constants.RawDeployment),
			}
			Eventually(func() string {
				isvc := &v1beta1.InferenceService{}
				if err := k8sClient.Get(context.TODO(), serviceKey, isvc); err != nil {
					return err.Error()
				}
				return cmp.Diff(&expectedIsvcStatus, &isvc.Status, cmpopts.IgnoreTypes(apis.VolatileTime{}))
			}, timeout).Should(BeEmpty())

			// check HPA
			var minReplicas int32 = 1
			var maxReplicas int32 = 3
			var cpuUtilization int32 = 75
			var stabilizationWindowSeconds int32 = 0
			selectPolicy := autoscalingv2.MaxChangePolicySelect
			actualHPA := &autoscalingv2.HorizontalPodAutoscaler{}
			predictorHPAKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorHPAKey, actualHPA) }, timeout).
				Should(Succeed())
			expectedHPA := &autoscalingv2.HorizontalPodAutoscaler{
				Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
					ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
						APIVersion: "apps/v1",
						Kind:       "Deployment",
						Name:       constants.PredictorServiceName(serviceKey.Name),
					},
					MinReplicas: &minReplicas,
					MaxReplicas: maxReplicas,
					Metrics: []autoscalingv2.MetricSpec{
						{
							Type: autoscalingv2.ResourceMetricSourceType,
							Resource: &autoscalingv2.ResourceMetricSource{
								Name: corev1.ResourceCPU,
								Target: autoscalingv2.MetricTarget{
									Type:               autoscalingv2.UtilizationMetricType,
									AverageUtilization: &cpuUtilization,
								},
							},
						},
					},
					Behavior: &autoscalingv2.HorizontalPodAutoscalerBehavior{
						ScaleUp: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: &stabilizationWindowSeconds,
							SelectPolicy:               &selectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Pods",
									Value:         4,
									PeriodSeconds: 15,
								},
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
						ScaleDown: &autoscalingv2.HPAScalingRules{
							StabilizationWindowSeconds: nil,
							SelectPolicy:               &selectPolicy,
							Policies: []autoscalingv2.HPAScalingPolicy{
								{
									Type:          "Percent",
									Value:         100,
									PeriodSeconds: 15,
								},
							},
						},
					},
				},
			}
			Expect(actualHPA.Spec).To(Equal(expectedHPA.Spec))
		})
	})
	Context("When creating an inferenceservice with raw kube predictor and ODH auth enabled", func() {
		configs := map[string]string{
			"oauthProxy":         `{"image": "registry.redhat.io/openshift4/ose-oauth-proxy@sha256:8507daed246d4d367704f7d7193233724acf1072572e1226ca063c066b858ecf", "memoryRequest": "64Mi", "memoryLimit": "128Mi", "cpuRequest": "100m", "cpuLimit": "200m"}`,
			"ingress":            `{"ingressGateway": "knative-serving/knative-ingress-gateway", "ingressService": "test-destination", "localGateway": "knative-serving/knative-local-gateway", "localGatewayService": "knative-local-gateway.istio-system.svc.cluster.local"}`,
			"storageInitializer": `{"image": "kserve/storage-initializer:latest", "memoryRequest": "100Mi", "memoryLimit": "1Gi", "cpuRequest": "100m", "cpuLimit": "1", "CaBundleConfigMapName": "", "caBundleVolumeMountPath": "/etc/ssl/custom-certs", "enableDirectPvcVolumeMount": false}`,
		}

		It("Should have ingress/service/deployment/hpa created", func() {
			By("By creating a new InferenceService")
			// Create configmap
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.InferenceServiceConfigMapName,
					Namespace: constants.KServeNamespace,
				},
				Data: configs,
			}
			Expect(k8sClient.Create(context.TODO(), configMap)).NotTo(HaveOccurred())
			defer k8sClient.Delete(context.TODO(), configMap)
			// Create ServingRuntime
			servingRuntime := &v1alpha1.ServingRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "tf-serving-raw",
					Namespace: "default",
				},
				Spec: v1alpha1.ServingRuntimeSpec{
					SupportedModelFormats: []v1alpha1.SupportedModelFormat{
						{
							Name:       "tensorflow",
							Version:    proto.String("1"),
							AutoSelect: proto.Bool(true),
						},
					},
					ServingRuntimePodSpec: v1alpha1.ServingRuntimePodSpec{
						Containers: []corev1.Container{
							{
								Name:    "kserve-container",
								Image:   "tensorflow/serving:1.14.0",
								Command: []string{"/usr/bin/tensorflow_model_server"},
								Args: []string{
									"--port=9000",
									"--rest_api_port=8080",
									"--model_base_path=/mnt/models",
									"--rest_api_timeout_in_ms=60000",
								},
								Resources: defaultResource,
							},
						},
					},
					Disabled: proto.Bool(false),
				},
			}
			k8sClient.Create(context.TODO(), servingRuntime)
			defer k8sClient.Delete(context.TODO(), servingRuntime)
			serviceName := "raw-auth"
			expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: "default"}}
			serviceKey := expectedRequest.NamespacedName
			storageUri := "s3://test/mnist/export"
			ctx := context.Background()
			isvc := &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
					Annotations: map[string]string{
						"serving.kserve.io/deploymentMode": "RawDeployment",
						constants.ODHKserveRawAuth:         "true",
					},
					Labels: map[string]string{
						constants.NetworkVisibility: constants.ODHRouteEnabled,
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
							MinReplicas: ptr.To(int32(1)),
							MaxReplicas: 3,
						},
						Tensorflow: &v1beta1.TFServingSpec{
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI:     &storageUri,
								RuntimeVersion: proto.String("1.14.0"),
								Container: corev1.Container{
									Name:      constants.InferenceServiceContainerName,
									Resources: defaultResource,
								},
							},
						},
					},
				},
			}
			isvc.DefaultInferenceService(nil, nil, &v1beta1.SecurityConfig{AutoMountServiceAccountToken: false}, nil)
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())

			inferenceService := &v1beta1.InferenceService{}

			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceKey, inferenceService)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			actualDeployment := &appsv1.Deployment{}
			predictorDeploymentKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error {
				return k8sClient.Get(context.TODO(), predictorDeploymentKey, actualDeployment)
			}, timeout).Should(Succeed())
			var replicas int32 = 1
			var revisionHistory int32 = 10
			var progressDeadlineSeconds int32 = 600
			var gracePeriod int64 = 30
			expectedDeployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorDeploymentKey.Name,
					Namespace: predictorDeploymentKey.Namespace,
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "isvc." + predictorDeploymentKey.Name,
						},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Name:      predictorDeploymentKey.Name,
							Namespace: "default",
							Labels: map[string]string{
								"app":                                 "isvc." + predictorDeploymentKey.Name,
								constants.KServiceComponentLabel:      constants.Predictor.String(),
								constants.InferenceServicePodLabelKey: serviceName,
								"serving.kserve.io/inferenceservice":  serviceName,
								constants.NetworkVisibility:           constants.ODHRouteEnabled,
							},
							Annotations: map[string]string{
								constants.StorageInitializerSourceUriInternalAnnotationKey: *isvc.Spec.Predictor.Model.StorageURI,
								"serving.kserve.io/deploymentMode":                         "RawDeployment",
								constants.ODHKserveRawAuth:                                 "true",
								"service.beta.openshift.io/serving-cert-secret-name":       predictorDeploymentKey.Name + constants.ServingCertSecretSuffix,
							},
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Image: "tensorflow/serving:" +
										*isvc.Spec.Predictor.Model.RuntimeVersion,
									Name:    constants.InferenceServiceContainerName,
									Command: []string{v1beta1.TensorflowEntrypointCommand},
									Args: []string{
										"--port=" + v1beta1.TensorflowServingGRPCPort,
										"--rest_api_port=" + v1beta1.TensorflowServingRestPort,
										"--model_base_path=" + constants.DefaultModelLocalMountPath,
										"--rest_api_timeout_in_ms=60000",
									},
									VolumeMounts: []corev1.VolumeMount{
										{
											Name:      "proxy-tls",
											MountPath: "/etc/tls/private",
										},
									},
									Resources: defaultResource,
									ReadinessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											TCPSocket: &corev1.TCPSocketAction{
												Port: intstr.IntOrString{
													IntVal: 8080,
												},
											},
										},
										InitialDelaySeconds: 0,
										TimeoutSeconds:      1,
										PeriodSeconds:       10,
										SuccessThreshold:    1,
										FailureThreshold:    3,
									},
									TerminationMessagePath:   "/dev/termination-log",
									TerminationMessagePolicy: "File",
									ImagePullPolicy:          "IfNotPresent",
								},
								{
									Name:  "oauth-proxy",
									Image: constants.OauthProxyImage,
									Args: []string{
										`--https-address=:8443`,
										`--provider=openshift`,
										`--skip-provider-button`,
										`--openshift-service-account=default`,
										`--upstream=http://localhost:8080`,
										`--tls-cert=/etc/tls/private/tls.crt`,
										`--tls-key=/etc/tls/private/tls.key`,
										// omit cookie secret arg in unit test as it is generated randomly
										// `--cookie-secret=SECRET`,
										`--openshift-delegate-urls={"/": {"namespace": "` + serviceKey.Namespace + `", "resource": "inferenceservices", "group": "serving.kserve.io", "name": "` + serviceName + `", "verb": "get"}}`,
										`--openshift-sar={"namespace": "` + serviceKey.Namespace + `", "resource": "inferenceservices", "group": "serving.kserve.io", "name": "` + serviceName + `", "verb": "get"}`,
									},
									Ports: []corev1.ContainerPort{
										{
											ContainerPort: constants.OauthProxyPort,
											Name:          "https",
											Protocol:      corev1.ProtocolTCP,
										},
									},
									LivenessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											HTTPGet: &corev1.HTTPGetAction{
												Path:   "/oauth/healthz",
												Port:   intstr.FromInt(constants.OauthProxyPort),
												Scheme: corev1.URISchemeHTTPS,
											},
										},
										InitialDelaySeconds: 30,
										TimeoutSeconds:      1,
										PeriodSeconds:       5,
										SuccessThreshold:    1,
										FailureThreshold:    3,
									},
									ReadinessProbe: &corev1.Probe{
										ProbeHandler: corev1.ProbeHandler{
											HTTPGet: &corev1.HTTPGetAction{
												Path:   "/oauth/healthz",
												Port:   intstr.FromInt(constants.OauthProxyPort),
												Scheme: corev1.URISchemeHTTPS,
											},
										},
										InitialDelaySeconds: 5,
										TimeoutSeconds:      1,
										PeriodSeconds:       5,
										SuccessThreshold:    1,
										FailureThreshold:    3,
									},
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse(constants.OauthProxyResourceCPULimit),
											corev1.ResourceMemory: resource.MustParse(constants.OauthProxyResourceMemoryLimit),
										},
										Requests: corev1.ResourceList{
											corev1.ResourceCPU:    resource.MustParse(constants.OauthProxyResourceCPURequest),
											corev1.ResourceMemory: resource.MustParse(constants.OauthProxyResourceMemoryRequest),
										},
									},
									VolumeMounts: []corev1.VolumeMount{
										{
											Name:      "proxy-tls",
											MountPath: "/etc/tls/private",
										},
									},
									TerminationMessagePath:   "/dev/termination-log",
									TerminationMessagePolicy: "File",
									ImagePullPolicy:          "IfNotPresent",
								},
							},
							Volumes: []corev1.Volume{
								{
									Name: "proxy-tls",
									VolumeSource: corev1.VolumeSource{
										Secret: &corev1.SecretVolumeSource{
											SecretName:  predictorDeploymentKey.Name + constants.ServingCertSecretSuffix,
											DefaultMode: func(i int32) *int32 { return &i }(420),
										},
									},
								},
							},
							SchedulerName:                 "default-scheduler",
							RestartPolicy:                 "Always",
							TerminationGracePeriodSeconds: &gracePeriod,
							DNSPolicy:                     "ClusterFirst",
							SecurityContext: &corev1.PodSecurityContext{
								SELinuxOptions:      nil,
								WindowsOptions:      nil,
								RunAsUser:           nil,
								RunAsGroup:          nil,
								RunAsNonRoot:        nil,
								SupplementalGroups:  nil,
								FSGroup:             nil,
								Sysctls:             nil,
								FSGroupChangePolicy: nil,
								SeccompProfile:      nil,
							},
							// ODH override. See : https://issues.redhat.com/browse/RHOAIENG-19904
							AutomountServiceAccountToken: proto.Bool(true),
						},
					},
					Strategy: appsv1.DeploymentStrategy{
						Type: "RollingUpdate",
						RollingUpdate: &appsv1.RollingUpdateDeployment{
							MaxUnavailable: &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
							MaxSurge:       &intstr.IntOrString{Type: 1, IntVal: 0, StrVal: "25%"},
						},
					},
					RevisionHistoryLimit:    &revisionHistory,
					ProgressDeadlineSeconds: &progressDeadlineSeconds,
				},
			}
			// remove the cookie-secret arg from the generated deployment for comparison
			cleanedDep := actualDeployment.DeepCopy()
			actualDep := v1beta1utils.RemoveCookieSecretArg(*cleanedDep)
			Expect(actualDep.Spec).To(Equal(expectedDeployment.Spec))

			// check service
			actualService := &corev1.Service{}
			predictorServiceKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorServiceKey, actualService) }, timeout).
				Should(Succeed())

			expectedService := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      predictorServiceKey.Name,
					Namespace: predictorServiceKey.Namespace,
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Name:       "https",
							Protocol:   "TCP",
							Port:       8443,
							TargetPort: intstr.IntOrString{Type: intstr.String, StrVal: "https"},
						},
					},
					Type:            "ClusterIP",
					SessionAffinity: "None",
					Selector: map[string]string{
						"app": "isvc." + constants.PredictorServiceName(serviceName),
					},
				},
			}
			actualService.Spec.ClusterIP = ""
			actualService.Spec.ClusterIPs = nil
			actualService.Spec.IPFamilies = nil
			actualService.Spec.IPFamilyPolicy = nil
			actualService.Spec.InternalTrafficPolicy = nil
			Expect(actualService.Spec).To(Equal(expectedService.Spec))

			route := &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
					Labels: map[string]string{
						"inferenceservice-name": serviceName,
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion:         "serving.kserve.io/v1beta1",
							Kind:               "InferenceService",
							Name:               serviceKey.Name,
							UID:                isvc.GetUID(),
							Controller:         proto.Bool(true),
							BlockOwnerDeletion: proto.Bool(true),
						},
					},
				},
				Spec: routev1.RouteSpec{
					Host: "raw-auth-default.example.com",
					To: routev1.RouteTargetReference{
						Kind:   "Service",
						Name:   predictorServiceKey.Name,
						Weight: proto.Int32(100),
					},
					Port: &routev1.RoutePort{
						TargetPort: intstr.FromInt(8443),
					},
					TLS: &routev1.TLSConfig{
						Termination:                   routev1.TLSTerminationReencrypt,
						InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyRedirect,
					},
					WildcardPolicy: routev1.WildcardPolicyNone,
				},
			}
			Expect(k8sClient.Create(context.TODO(), route)).Should(Succeed())
			route.Status = routev1.RouteStatus{
				Ingress: []routev1.RouteIngress{
					{
						Host: "raw-auth-default.example.com",
						Conditions: []routev1.RouteIngressCondition{
							{
								Type:   routev1.RouteAdmitted,
								Status: corev1.ConditionTrue,
							},
						},
					},
				},
			}
			Expect(k8sClient.Status().Update(ctx, route)).Should(Succeed())

			// check isvc status
			updatedDeployment := actualDeployment.DeepCopy()
			updatedDeployment.Status.Conditions = []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: corev1.ConditionTrue,
				},
			}
			Expect(k8sClient.Status().Update(context.TODO(), updatedDeployment)).NotTo(HaveOccurred())

			// verify if InferenceService status is updated
			expectedIsvcStatus := v1beta1.InferenceServiceStatus{
				Status: duckv1.Status{
					Conditions: duckv1.Conditions{
						{
							Type:   v1beta1.IngressReady,
							Status: "True",
						},
						{
							Type:   v1beta1.PredictorReady,
							Status: "True",
						},
						{
							Type:   apis.ConditionReady,
							Status: "True",
						},
						{
							Type:     v1beta1.Stopped,
							Status:   "False",
							Severity: apis.ConditionSeverityInfo,
						},
					},
				},
				URL: &apis.URL{
					Scheme: "https",
					Host:   "raw-auth-default.example.com",
				},
				Address: &duckv1.Addressable{
					URL: &apis.URL{
						Scheme: "https",
						Host:   fmt.Sprintf("%s-predictor.%s.svc.cluster.local:8443", serviceKey.Name, serviceKey.Namespace),
					},
				},
				Components: map[v1beta1.ComponentType]v1beta1.ComponentStatusSpec{
					v1beta1.PredictorComponent: {
						LatestCreatedRevision: "",
						// URL: &apis.URL{
						//	Scheme: "http",
						//	Host:   "raw-auth-predictor-default.example.com",
						// },
					},
				},
				ModelStatus: v1beta1.ModelStatus{
					TransitionStatus:    "InProgress",
					ModelRevisionStates: &v1beta1.ModelRevisionStates{TargetModelState: "Pending"},
				},
				DeploymentMode: "RawDeployment",
			}
			Eventually(func() string {
				isvc := &v1beta1.InferenceService{}
				if err := k8sClient.Get(context.TODO(), serviceKey, isvc); err != nil {
					return err.Error()
				}
				return cmp.Diff(&expectedIsvcStatus, &isvc.Status, cmpopts.IgnoreTypes(apis.VolatileTime{}))
			}, timeout).Should(BeEmpty())
		})
	})
	Context("When creating inference service with raw kube predictor with workerSpec", func() {
		var (
			serviceKey types.NamespacedName
			storageUri string
			isvc       *v1beta1.InferenceService
		)

		isvcNamespace := constants.KServeNamespace
		actualDefaultDeployment := &appsv1.Deployment{}
		actualWorkerDeployment := &appsv1.Deployment{}

		BeforeEach(func() {
			ctx := context.Background()
			storageUri = "pvc://llama-3-8b-pvc/hf/8b_instruction_tuned"

			// Create a ConfigMap
			configs := map[string]string{
				"ingress": `{
            		"ingressGateway": "knative-serving/knative-ingress-gateway",
            		"localGateway": "knative-serving/knative-local-gateway",
            		"localGatewayService": "knative-local-gateway.istio-system.svc.cluster.local"
        		}`,
				"storageInitializer": `{
            		"image" : "kserve/storage-initializer:latest",
           	 		"memoryRequest": "100Mi",
            		"memoryLimit": "1Gi",
            		"cpuRequest": "100m",
            		"cpuLimit": "1",
            		"CaBundleConfigMapName": "",
            		"caBundleVolumeMountPath": "/etc/ssl/custom-certs",
            		"enableDirectPvcVolumeMount": false
        		}`,
			}
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.InferenceServiceConfigMapName,
					Namespace: constants.KServeNamespace,
				},
				Data: configs,
			}
			Expect(k8sClient.Create(ctx, configMap)).NotTo(HaveOccurred())
			DeferCleanup(func() {
				k8sClient.Delete(ctx, configMap)
			})

			// Create a ServingRuntime
			servingRuntime := &v1alpha1.ServingRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "huggingface-server-multinode",
					Namespace: isvcNamespace,
				},
				Spec: v1alpha1.ServingRuntimeSpec{
					SupportedModelFormats: []v1alpha1.SupportedModelFormat{
						{
							Name:       "huggingface",
							Version:    proto.String("2"),
							AutoSelect: proto.Bool(true),
						},
					},
					ServingRuntimePodSpec: v1alpha1.ServingRuntimePodSpec{
						Containers: []corev1.Container{
							{
								Name:  constants.InferenceServiceContainerName,
								Image: "kserve/huggingfaceserver:latest-gpu",
								Command: []string{
									"bash",
									"-c",
									"python3 -m huggingfaceserver --model_name=${MODEL_NAME} --model_dir=${MODEL} --tensor-parallel-size=${TENSOR_PARALLEL_SIZE} --pipeline-parallel-size=${PIPELINE_PARALLEL_SIZE}",
								},
								Args: []string{
									"--model_name={{.Name}}",
								},
								Resources: defaultResource,
							},
						},
					},
					WorkerSpec: &v1alpha1.WorkerSpec{
						PipelineParallelSize: ptr.To(2),
						TensorParallelSize:   ptr.To(1),
						ServingRuntimePodSpec: v1alpha1.ServingRuntimePodSpec{
							Containers: []corev1.Container{
								{
									Name:  constants.WorkerContainerName,
									Image: "kserve/huggingfaceserver:latest-gpu",
									Command: []string{
										"bash",
										"-c",
										"ray start --address=$RAY_HEAD_ADDRESS --block",
									},
									Resources: defaultResource,
								},
							},
						},
					},
					Disabled: proto.Bool(false),
				},
			}
			Expect(k8sClient.Create(ctx, servingRuntime)).NotTo(HaveOccurred())
			DeferCleanup(func() {
				k8sClient.Delete(ctx, servingRuntime)
			})
		})
		It("Should have services/deployments for head/worker without an autoscaler when workerSpec is set in isvc", func() {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			By("creating a new InferenceService")
			isvcName := "raw-huggingface-multinode-1"
			predictorDeploymentName := constants.PredictorServiceName(isvcName)
			workerDeploymentName := constants.PredictorWorkerServiceName(isvcName)
			serviceKey = types.NamespacedName{Name: isvcName, Namespace: isvcNamespace}

			isvc = &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      isvcName,
					Namespace: isvcNamespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: string(constants.AutoscalerClassNone),
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						Model: &v1beta1.ModelSpec{
							ModelFormat: v1beta1.ModelFormat{
								Name: "huggingface",
							},
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI: &storageUri,
							},
						},
						WorkerSpec: &v1beta1.WorkerSpec{},
					},
				},
			}
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			DeferCleanup(func() {
				k8sClient.Delete(ctx, isvc)
			})

			// Verify inferenceService is created
			inferenceService := &v1beta1.InferenceService{}
			Eventually(func() bool {
				return k8sClient.Get(ctx, serviceKey, inferenceService) == nil
			}, timeout, interval).Should(BeTrue())

			// Verify if predictor deployment (default deployment) is created
			Eventually(func() bool {
				return k8sClient.Get(ctx, types.NamespacedName{Name: predictorDeploymentName, Namespace: isvcNamespace}, actualDefaultDeployment) == nil
			}, timeout, interval).Should(BeTrue())

			// Verify if worker node deployment is created.
			Eventually(func() bool {
				return k8sClient.Get(ctx, types.NamespacedName{Name: workerDeploymentName, Namespace: isvcNamespace}, actualWorkerDeployment) == nil
			}, timeout, interval).Should(BeTrue())

			// Verify head deployments environment variables
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.PipelineParallelSizeEnvName, "2")
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.TensorParallelSizeEnvName, "1")
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.RayNodeCountEnvName, "2")

			// Verify worker deployments environment variables
			verifyEnvKeyValueDeployments(actualWorkerDeployment, constants.RayNodeCountEnvName, "2")

			// Verify gpu resources for head/worker nodes
			verifyGPUResourceSizeDeployment(actualDefaultDeployment, actualWorkerDeployment, "1", "1", constants.NvidiaGPUResourceType, constants.NvidiaGPUResourceType)

			// Verify worker node replicas
			Expect(actualWorkerDeployment.Spec.Replicas).Should(Equal(ptr.To(int32(1))))

			// Check Services
			actualService := &corev1.Service{}
			headServiceName := constants.GetHeadServiceName(isvcName+"-predictor", "1")
			defaultServiceName := isvcName + "-predictor"
			expectedHeadServiceName := types.NamespacedName{Name: headServiceName, Namespace: isvcNamespace}
			expectedDefaultServiceName := types.NamespacedName{Name: defaultServiceName, Namespace: isvcNamespace}

			// Verify if head service is created
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, expectedHeadServiceName, actualService); err != nil {
					return false
				}
				return true
			}, timeout, interval).Should(BeTrue())

			Expect(actualService.Spec.ClusterIP).Should(Equal("None"))
			Expect(actualService.Spec.PublishNotReadyAddresses).Should(BeTrue())

			// Verify if predictor service (default service) is created
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, expectedDefaultServiceName, actualService); err != nil {
					return false
				}
				return true
			}, timeout, interval).Should(BeTrue())

			// Verify there if the default autoscaler(HPA) is not created.
			actualHPA := &autoscalingv2.HorizontalPodAutoscaler{}
			predictorHPAKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(isvcName),
				Namespace: isvcNamespace,
			}

			Eventually(func() error {
				err := k8sClient.Get(context.TODO(), predictorHPAKey, actualHPA)
				if err != nil && apierr.IsNotFound(err) {
					return nil
				}
				return fmt.Errorf("expected IsNotFound error, but got %w", err)
			}, timeout).Should(Succeed())
		})
		It("Should use WorkerSpec.PipelineParallelSize value in isvc when it is set", func() {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			By("By creating a new InferenceService")
			isvcName := "raw-huggingface-multinode-4"
			predictorDeploymentName := constants.PredictorServiceName(isvcName)
			workerDeploymentName := constants.PredictorWorkerServiceName(isvcName)
			serviceKey = types.NamespacedName{Name: isvcName, Namespace: isvcNamespace}
			// Create a infereceService
			isvc = &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      isvcName,
					Namespace: isvcNamespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: string(constants.AutoscalerClassNone),
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						Model: &v1beta1.ModelSpec{
							ModelFormat: v1beta1.ModelFormat{
								Name: "huggingface",
							},
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI: &storageUri,
							},
						},
						WorkerSpec: &v1beta1.WorkerSpec{
							PipelineParallelSize: ptr.To(5),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			DeferCleanup(func() {
				k8sClient.Delete(ctx, isvc)
			})

			// Verify inferenceService is created
			inferenceService := &v1beta1.InferenceService{}
			Eventually(func() bool {
				return k8sClient.Get(ctx, serviceKey, inferenceService) == nil
			}, timeout, interval).Should(BeTrue())

			// Verify if predictor deployment (default deployment) is created
			Eventually(func() bool {
				return k8sClient.Get(ctx, types.NamespacedName{Name: predictorDeploymentName, Namespace: isvcNamespace}, actualDefaultDeployment) == nil
			}, timeout, interval).Should(BeTrue())

			// Verify if worker node deployment is created.
			Eventually(func() bool {
				return k8sClient.Get(ctx, types.NamespacedName{Name: workerDeploymentName, Namespace: isvcNamespace}, actualWorkerDeployment) == nil
			}, timeout, interval).Should(BeTrue())

			// Verify head deployments details
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.PipelineParallelSizeEnvName, "5")
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.TensorParallelSizeEnvName, "1")
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.RayNodeCountEnvName, "5")

			// Verify worker deployments details
			verifyEnvKeyValueDeployments(actualWorkerDeployment, constants.RayNodeCountEnvName, "5")

			// Verify gpu resources for head/worker nodes
			verifyGPUResourceSizeDeployment(actualDefaultDeployment, actualWorkerDeployment, "1", "1", constants.NvidiaGPUResourceType, constants.NvidiaGPUResourceType)

			// Verify worker node replicas
			Expect(actualWorkerDeployment.Spec.Replicas).Should(Equal(ptr.To(int32(4))))
		})
		It("Should use WorkerSpec.TensorParallelSize value in isvc when it is set", func() {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			By("creating a new InferenceService")
			isvcName := "raw-huggingface-multinode-5"
			predictorDeploymentName := constants.PredictorServiceName(isvcName)
			workerDeploymentName := constants.PredictorWorkerServiceName(isvcName)
			serviceKey = types.NamespacedName{Name: isvcName, Namespace: isvcNamespace}

			// Create a infereceService
			isvc = &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      isvcName,
					Namespace: isvcNamespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: string(constants.AutoscalerClassNone),
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						Model: &v1beta1.ModelSpec{
							ModelFormat: v1beta1.ModelFormat{
								Name: "huggingface",
							},
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI: &storageUri,
							},
						},
						WorkerSpec: &v1beta1.WorkerSpec{
							TensorParallelSize: ptr.To(16),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			DeferCleanup(func() {
				k8sClient.Delete(ctx, isvc)
			})

			// Verify if predictor deployment (default deployment) is created
			Eventually(func() bool {
				return k8sClient.Get(ctx, types.NamespacedName{Name: predictorDeploymentName, Namespace: isvcNamespace}, actualDefaultDeployment) == nil
			}, timeout, interval).Should(BeTrue())

			// Verify if worker node deployment is created.
			Eventually(func() bool {
				return k8sClient.Get(ctx, types.NamespacedName{Name: workerDeploymentName, Namespace: isvcNamespace}, actualWorkerDeployment) == nil
			}, timeout, interval).Should(BeTrue())

			// Verify head deployments environment variables
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.PipelineParallelSizeEnvName, "2")
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.TensorParallelSizeEnvName, "16")
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.RayNodeCountEnvName, "32")

			// Verify worker deployments environment variables
			verifyEnvKeyValueDeployments(actualWorkerDeployment, constants.RayNodeCountEnvName, "32")

			// Verify gpu resources for head/worker nodes
			verifyGPUResourceSizeDeployment(actualDefaultDeployment, actualWorkerDeployment, "1", "1", constants.NvidiaGPUResourceType, constants.NvidiaGPUResourceType)

			// Verify worker node replicas
			Expect(actualWorkerDeployment.Spec.Replicas).Should(Equal(ptr.To(int32(31))))
		})
		It("Should use head container GPU resource value in isvc when it is set", func() {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			By("creating a new InferenceService")
			isvcName := "raw-huggingface-multinode-5-1"
			predictorDeploymentName := constants.PredictorServiceName(isvcName)
			workerDeploymentName := constants.PredictorWorkerServiceName(isvcName)
			serviceKey = types.NamespacedName{Name: isvcName, Namespace: isvcNamespace}

			// Create a infereceService
			isvc = &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      isvcName,
					Namespace: isvcNamespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: string(constants.AutoscalerClassNone),
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						Model: &v1beta1.ModelSpec{
							ModelFormat: v1beta1.ModelFormat{
								Name: "huggingface",
							},
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI: &storageUri,
								Container: corev1.Container{
									Name:  constants.InferenceServiceContainerName,
									Image: "kserve/huggingfaceserver:latest-gpu",
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											constants.IntelGPUResourceType: resource.MustParse("2"),
										},
										Requests: corev1.ResourceList{
											constants.IntelGPUResourceType: resource.MustParse("2"),
										},
									},
								},
							},
						},
						WorkerSpec: &v1beta1.WorkerSpec{
							TensorParallelSize: ptr.To(4),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			DeferCleanup(func() {
				k8sClient.Delete(ctx, isvc)
			})

			// Verify if predictor deployment (default deployment) is created
			Eventually(func() bool {
				return k8sClient.Get(ctx, types.NamespacedName{Name: predictorDeploymentName, Namespace: isvcNamespace}, actualDefaultDeployment) == nil
			}, timeout, interval).Should(BeTrue())

			// Verify if worker node deployment is created.
			Eventually(func() bool {
				return k8sClient.Get(ctx, types.NamespacedName{Name: workerDeploymentName, Namespace: isvcNamespace}, actualWorkerDeployment) == nil
			}, timeout, interval).Should(BeTrue())

			// Verify head deployments environment variables
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.PipelineParallelSizeEnvName, "2")
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.TensorParallelSizeEnvName, "4")
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.RayNodeCountEnvName, "7")

			// Verify worker deployments environment variables
			verifyEnvKeyValueDeployments(actualWorkerDeployment, constants.RayNodeCountEnvName, "7")

			// Verify gpu resources for head/worker nodes
			verifyGPUResourceSizeDeployment(actualDefaultDeployment, actualWorkerDeployment, "2", "1", constants.IntelGPUResourceType, constants.NvidiaGPUResourceType)

			// Verify worker node replicas
			Expect(actualWorkerDeployment.Spec.Replicas).Should(Equal(ptr.To(int32(6))))
		})
		It("Should use worker container GPU resource value in isvc when it is set", func() {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			By("creating a new InferenceService")
			isvcName := "raw-huggingface-multinode-5-2"
			predictorDeploymentName := constants.PredictorServiceName(isvcName)
			workerDeploymentName := constants.PredictorWorkerServiceName(isvcName)
			serviceKey = types.NamespacedName{Name: isvcName, Namespace: isvcNamespace}

			// Create a infereceService
			isvc = &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      isvcName,
					Namespace: isvcNamespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: string(constants.AutoscalerClassNone),
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						Model: &v1beta1.ModelSpec{
							ModelFormat: v1beta1.ModelFormat{
								Name: "huggingface",
							},
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI: &storageUri,
							},
						},
						WorkerSpec: &v1beta1.WorkerSpec{
							TensorParallelSize: ptr.To(4),
							PodSpec: v1beta1.PodSpec{
								Containers: []corev1.Container{
									{
										Name:  constants.WorkerContainerName,
										Image: "kserve/huggingfaceserver:latest-gpu",
										Resources: corev1.ResourceRequirements{
											Limits: corev1.ResourceList{
												constants.IntelGPUResourceType: resource.MustParse("2"),
											},
											Requests: corev1.ResourceList{
												constants.IntelGPUResourceType: resource.MustParse("2"),
											},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			DeferCleanup(func() {
				k8sClient.Delete(ctx, isvc)
			})

			// Verify if predictor deployment (default deployment) is created
			Eventually(func() bool {
				return k8sClient.Get(ctx, types.NamespacedName{Name: predictorDeploymentName, Namespace: isvcNamespace}, actualDefaultDeployment) == nil
			}, timeout, interval).Should(BeTrue())

			// Verify if worker node deployment is created.
			Eventually(func() bool {
				return k8sClient.Get(ctx, types.NamespacedName{Name: workerDeploymentName, Namespace: isvcNamespace}, actualWorkerDeployment) == nil
			}, timeout, interval).Should(BeTrue())

			// Verify head deployments environment variables
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.PipelineParallelSizeEnvName, "2")
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.TensorParallelSizeEnvName, "4")
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.RayNodeCountEnvName, "4")

			// Verify worker deployments environment variables
			verifyEnvKeyValueDeployments(actualWorkerDeployment, constants.RayNodeCountEnvName, "4")

			// Verify gpu resources for head/worker nodes
			verifyGPUResourceSizeDeployment(actualDefaultDeployment, actualWorkerDeployment, "2", "2", constants.NvidiaGPUResourceType, constants.IntelGPUResourceType)

			// Verify worker node replicas
			Expect(actualWorkerDeployment.Spec.Replicas).Should(Equal(ptr.To(int32(3))))
		})
		It("Should run head node only, worker node replicas should be set 0 when head node gpu count is equal to total required gpu", func() {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			By("creating a new InferenceService")
			isvcName := "raw-huggingface-multinode-5-3"
			predictorDeploymentName := constants.PredictorServiceName(isvcName)
			workerDeploymentName := constants.PredictorWorkerServiceName(isvcName)
			serviceKey = types.NamespacedName{Name: isvcName, Namespace: isvcNamespace}

			// Create a infereceService
			isvc = &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      isvcName,
					Namespace: isvcNamespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: string(constants.AutoscalerClassNone),
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						Model: &v1beta1.ModelSpec{
							ModelFormat: v1beta1.ModelFormat{
								Name: "huggingface",
							},
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI: &storageUri,
								Container: corev1.Container{
									Name:  constants.InferenceServiceContainerName,
									Image: "kserve/huggingfaceserver:latest-gpu",
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											constants.IntelGPUResourceType: resource.MustParse("8"),
										},
										Requests: corev1.ResourceList{
											constants.IntelGPUResourceType: resource.MustParse("8"),
										},
									},
								},
							},
						},
						WorkerSpec: &v1beta1.WorkerSpec{
							TensorParallelSize: ptr.To(4),
							PodSpec: v1beta1.PodSpec{
								Containers: []corev1.Container{
									{
										Name:  constants.WorkerContainerName,
										Image: "kserve/huggingfaceserver:latest-gpu",
										Resources: corev1.ResourceRequirements{
											Limits: corev1.ResourceList{
												constants.IntelGPUResourceType: resource.MustParse("2"),
											},
											Requests: corev1.ResourceList{
												constants.IntelGPUResourceType: resource.MustParse("2"),
											},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			DeferCleanup(func() {
				k8sClient.Delete(ctx, isvc)
			})

			// Verify if predictor deployment (default deployment) is created
			Eventually(func() bool {
				return k8sClient.Get(ctx, types.NamespacedName{Name: predictorDeploymentName, Namespace: isvcNamespace}, actualDefaultDeployment) == nil
			}, timeout, interval).Should(BeTrue())

			// Verify if worker node deployment is created.
			Eventually(func() bool {
				return k8sClient.Get(ctx, types.NamespacedName{Name: workerDeploymentName, Namespace: isvcNamespace}, actualWorkerDeployment) == nil
			}, timeout, interval).Should(BeTrue())

			// Verify head deployments environment variables
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.PipelineParallelSizeEnvName, "2")
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.TensorParallelSizeEnvName, "4")
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.RayNodeCountEnvName, "1")

			// Verify worker deployments environment variables
			verifyEnvKeyValueDeployments(actualWorkerDeployment, constants.RayNodeCountEnvName, "1")

			// Verify gpu resources for head/worker nodes
			verifyGPUResourceSizeDeployment(actualDefaultDeployment, actualWorkerDeployment, "8", "0", constants.IntelGPUResourceType, constants.IntelGPUResourceType)

			// Verify worker node replicas
			Expect(actualWorkerDeployment.Spec.Replicas).Should(Equal(ptr.To(int32(0))))
		})
		It("Should run head node only, worker node replicas should be set 0 when worker node gpu count is equal to total required gpu", func() {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			By("creating a new InferenceService")
			isvcName := "raw-huggingface-multinode-5-4"
			predictorDeploymentName := constants.PredictorServiceName(isvcName)
			workerDeploymentName := constants.PredictorWorkerServiceName(isvcName)
			serviceKey = types.NamespacedName{Name: isvcName, Namespace: isvcNamespace}

			// Create a infereceService
			isvc = &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      isvcName,
					Namespace: isvcNamespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: string(constants.AutoscalerClassNone),
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						Model: &v1beta1.ModelSpec{
							ModelFormat: v1beta1.ModelFormat{
								Name: "huggingface",
							},
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI: &storageUri,
								Container: corev1.Container{
									Name:  constants.InferenceServiceContainerName,
									Image: "kserve/huggingfaceserver:latest-gpu",
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											constants.IntelGPUResourceType: resource.MustParse("2"),
										},
										Requests: corev1.ResourceList{
											constants.IntelGPUResourceType: resource.MustParse("2"),
										},
									},
								},
							},
						},
						WorkerSpec: &v1beta1.WorkerSpec{
							TensorParallelSize: ptr.To(4),
							PodSpec: v1beta1.PodSpec{
								Containers: []corev1.Container{
									{
										Name:  constants.WorkerContainerName,
										Image: "kserve/huggingfaceserver:latest-gpu",
										Resources: corev1.ResourceRequirements{
											Limits: corev1.ResourceList{
												constants.IntelGPUResourceType: resource.MustParse("8"),
											},
											Requests: corev1.ResourceList{
												constants.IntelGPUResourceType: resource.MustParse("8"),
											},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			DeferCleanup(func() {
				k8sClient.Delete(ctx, isvc)
			})

			// Verify if predictor deployment (default deployment) is created
			Eventually(func() bool {
				return k8sClient.Get(ctx, types.NamespacedName{Name: predictorDeploymentName, Namespace: isvcNamespace}, actualDefaultDeployment) == nil
			}, timeout, interval).Should(BeTrue())

			// Verify if worker node deployment is created.
			Eventually(func() bool {
				return k8sClient.Get(ctx, types.NamespacedName{Name: workerDeploymentName, Namespace: isvcNamespace}, actualWorkerDeployment) == nil
			}, timeout, interval).Should(BeTrue())

			// Verify head deployments environment variables
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.PipelineParallelSizeEnvName, "2")
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.TensorParallelSizeEnvName, "4")
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.RayNodeCountEnvName, "1")

			// Verify worker deployments environment variables
			verifyEnvKeyValueDeployments(actualWorkerDeployment, constants.RayNodeCountEnvName, "1")

			// Verify gpu resources for head/worker nodes
			verifyGPUResourceSizeDeployment(actualDefaultDeployment, actualWorkerDeployment, "8", "0", constants.IntelGPUResourceType, constants.IntelGPUResourceType)

			// Verify worker node replicas
			Expect(actualWorkerDeployment.Spec.Replicas).Should(Equal(ptr.To(int32(0))))
		})
		It("Should return error if total required gpu count is less than what it should be assigned with head/worker", func() {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			By("creating a new InferenceService")
			isvcName := "raw-huggingface-multinode-5-5"
			predictorDeploymentName := constants.PredictorServiceName(isvcName)
			workerDeploymentName := constants.PredictorWorkerServiceName(isvcName)
			serviceKey = types.NamespacedName{Name: isvcName, Namespace: isvcNamespace}

			// Create a infereceService
			isvc = &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      isvcName,
					Namespace: isvcNamespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: string(constants.AutoscalerClassNone),
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						Model: &v1beta1.ModelSpec{
							ModelFormat: v1beta1.ModelFormat{
								Name: "huggingface",
							},
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI: &storageUri,
								Container: corev1.Container{
									Name:  constants.InferenceServiceContainerName,
									Image: "kserve/huggingfaceserver:latest-gpu",
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											constants.IntelGPUResourceType: resource.MustParse("2"),
										},
										Requests: corev1.ResourceList{
											constants.IntelGPUResourceType: resource.MustParse("2"),
										},
									},
								},
							},
						},
						WorkerSpec: &v1beta1.WorkerSpec{
							PipelineParallelSize: ptr.To(1),
							TensorParallelSize:   ptr.To(7),
							PodSpec: v1beta1.PodSpec{
								Containers: []corev1.Container{
									{
										Name:  constants.WorkerContainerName,
										Image: "kserve/huggingfaceserver:latest-gpu",
										Resources: corev1.ResourceRequirements{
											Limits: corev1.ResourceList{
												constants.IntelGPUResourceType: resource.MustParse("4"),
											},
											Requests: corev1.ResourceList{
												constants.IntelGPUResourceType: resource.MustParse("4"),
											},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			DeferCleanup(func() {
				k8sClient.Delete(ctx, isvc)
			})

			updatedIsvc := &v1beta1.InferenceService{}
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, serviceKey, updatedIsvc); err == nil {
					for _, condition := range updatedIsvc.Status.Status.Conditions {
						if condition.Type == v1beta1.PredictorReady && condition.Reason == v1beta1.InvalidGPUAllocation && condition.Message == fmt.Sprintf(components.ErrRayClusterInsufficientGPUs, 7, 2, 4) {
							return true
						}
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())

			// Verify if predictor deployment (default deployment) is not created
			Consistently(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: predictorDeploymentName, Namespace: isvcNamespace}, actualDefaultDeployment)
			}, time.Second*10, interval).ShouldNot(Succeed())

			// Verify if worker node deployment is not created.
			Consistently(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: workerDeploymentName, Namespace: isvcNamespace}, actualWorkerDeployment)
			}, time.Second*10, interval).ShouldNot(Succeed())
		})
		It("Should deploy even if it assigns more GPUs than the total required GPU resource count.", func() {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			By("creating a new InferenceService")
			isvcName := "raw-huggingface-multinode-5-6"
			predictorDeploymentName := constants.PredictorServiceName(isvcName)
			workerDeploymentName := constants.PredictorWorkerServiceName(isvcName)
			serviceKey = types.NamespacedName{Name: isvcName, Namespace: isvcNamespace}

			// Create a infereceService
			isvc = &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      isvcName,
					Namespace: isvcNamespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: string(constants.AutoscalerClassNone),
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						Model: &v1beta1.ModelSpec{
							ModelFormat: v1beta1.ModelFormat{
								Name: "huggingface",
							},
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI: &storageUri,
								Container: corev1.Container{
									Name:  constants.InferenceServiceContainerName,
									Image: "kserve/huggingfaceserver:latest-gpu",
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											constants.IntelGPUResourceType: resource.MustParse("4"),
										},
										Requests: corev1.ResourceList{
											constants.IntelGPUResourceType: resource.MustParse("4"),
										},
									},
								},
							},
						},
						WorkerSpec: &v1beta1.WorkerSpec{
							TensorParallelSize: ptr.To(16),
							PodSpec: v1beta1.PodSpec{
								Containers: []corev1.Container{
									{
										Name:  constants.WorkerContainerName,
										Image: "kserve/huggingfaceserver:latest-gpu",
										Resources: corev1.ResourceRequirements{
											Limits: corev1.ResourceList{
												constants.IntelGPUResourceType: resource.MustParse("3"),
											},
											Requests: corev1.ResourceList{
												constants.IntelGPUResourceType: resource.MustParse("3"),
											},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			DeferCleanup(func() {
				k8sClient.Delete(ctx, isvc)
			})

			// Verify if predictor deployment (default deployment) is created
			Eventually(func() bool {
				return k8sClient.Get(ctx, types.NamespacedName{Name: predictorDeploymentName, Namespace: isvcNamespace}, actualDefaultDeployment) == nil
			}, timeout, interval).Should(BeTrue())

			// Verify if worker node deployment is created.
			Eventually(func() bool {
				return k8sClient.Get(ctx, types.NamespacedName{Name: workerDeploymentName, Namespace: isvcNamespace}, actualWorkerDeployment) == nil
			}, timeout, interval).Should(BeTrue())

			// Verify head deployments environment variables
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.PipelineParallelSizeEnvName, "2")
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.TensorParallelSizeEnvName, "16")
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.RayNodeCountEnvName, "11")

			// Verify worker deployments environment variables
			verifyEnvKeyValueDeployments(actualWorkerDeployment, constants.RayNodeCountEnvName, "11")

			// Verify gpu resources for head/worker nodes
			verifyGPUResourceSizeDeployment(actualDefaultDeployment, actualWorkerDeployment, "4", "3", constants.IntelGPUResourceType, constants.IntelGPUResourceType)

			// Verify worker node replicas
			Expect(actualWorkerDeployment.Spec.Replicas).Should(Equal(ptr.To(int32(10))))
		})
		It("Should not set nil to replicas when multinode isvc(none autoscaler) is updated", func() {
			ctx, cancel := context.WithCancel(context.Background())
			DeferCleanup(cancel)
			By("creating a new InferenceService")
			isvcName := "raw-huggingface-multinode-6"
			predictorDeploymentName := constants.PredictorServiceName(isvcName)
			workerDeploymentName := constants.PredictorWorkerServiceName(isvcName)
			serviceKey = types.NamespacedName{Name: isvcName, Namespace: isvcNamespace}

			// Create a infereceService
			isvc = &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      isvcName,
					Namespace: isvcNamespace,
					Annotations: map[string]string{
						constants.DeploymentMode:  string(constants.RawDeployment),
						constants.AutoscalerClass: string(constants.AutoscalerClassNone),
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						Model: &v1beta1.ModelSpec{
							ModelFormat: v1beta1.ModelFormat{
								Name: "huggingface",
							},
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI: &storageUri,
							},
						},
						WorkerSpec: &v1beta1.WorkerSpec{
							PipelineParallelSize: ptr.To(3),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			DeferCleanup(func() {
				k8sClient.Delete(ctx, isvc)
			})

			// Verify if predictor deployment (default deployment) is created
			Eventually(func() bool {
				return k8sClient.Get(ctx, types.NamespacedName{Name: predictorDeploymentName, Namespace: isvcNamespace}, actualDefaultDeployment) == nil
			}, timeout, interval).Should(BeTrue())

			// Verify if worker node deployment is created.
			Eventually(func() bool {
				return k8sClient.Get(ctx, types.NamespacedName{Name: workerDeploymentName, Namespace: isvcNamespace}, actualWorkerDeployment) == nil
			}, timeout, interval).Should(BeTrue())

			// Verify head deployments environment variables
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.PipelineParallelSizeEnvName, "3")
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.TensorParallelSizeEnvName, "1")
			verifyEnvKeyValueDeployments(actualDefaultDeployment, constants.RayNodeCountEnvName, "3")

			// Verify worker deployments environment variables
			verifyEnvKeyValueDeployments(actualWorkerDeployment, constants.RayNodeCountEnvName, "3")

			// Verify gpu resources for head/worker nodes
			verifyGPUResourceSizeDeployment(actualDefaultDeployment, actualWorkerDeployment, "1", "1", constants.NvidiaGPUResourceType, constants.NvidiaGPUResourceType)

			// Verify worker Node replicas
			Expect(actualWorkerDeployment.Spec.Replicas).Should(Equal(ptr.To(int32(2))))

			// Update a infereceService
			By("updating the InferenceService")
			updatedIsvc := &v1beta1.InferenceService{}
			k8sClient.Get(ctx, types.NamespacedName{Name: isvc.Name, Namespace: isvcNamespace}, updatedIsvc)
			// Add label to isvc to create a new rs
			if updatedIsvc.Labels == nil {
				updatedIsvc.Labels = make(map[string]string)
			}
			updatedIsvc.Labels["newLabel"] = "test"

			err := k8sClient.Update(ctx, updatedIsvc)
			Expect(err).ShouldNot(HaveOccurred(), "Failed to update InferenceService with new label")

			// Verify if predictor deployment (default deployment) has replicas
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: predictorDeploymentName, Namespace: isvcNamespace}, actualDefaultDeployment); err == nil {
					return actualDefaultDeployment.Spec.Replicas != nil && *actualDefaultDeployment.Spec.Replicas == 1
				}
				return false
			}, timeout, interval).Should(BeTrue())

			// Verify if worker node deployment has replicas
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: workerDeploymentName, Namespace: isvcNamespace}, actualWorkerDeployment); err == nil {
					return actualWorkerDeployment.Spec.Replicas != nil && *actualWorkerDeployment.Spec.Replicas == 2
				}
				return false
			}, timeout, interval).Should(BeTrue())
		})
	})
	Context("When creating an inference service with modelcar and raw deployment", func() {
		configs := map[string]string{
			"ingress": `{
				"enableGatewayApi": true,
				"kserveIngressGateway": "kserve/kserve-ingress-gateway",
				"ingressGateway": "knative-serving/knative-ingress-gateway",
				"localGateway": "knative-serving/knative-local-gateway",
				"localGatewayService": "knative-local-gateway.istio-system.svc.cluster.local",
				"additionalIngressDomains": ["additional.example.com"]
			}`,
			"storageInitializer": `{
				"image" : "kserve/storage-initializer:latest",
				"memoryRequest": "100Mi",
				"memoryLimit": "1Gi",
				"cpuRequest": "100m",
				"cpuLimit": "1",
				"CaBundleConfigMapName": "",
				"caBundleVolumeMountPath": "/etc/ssl/custom-certs",
				"enableDirectPvcVolumeMount": false
			}`,
		}

		It("Should only have the ImagePullSecrets that are specified in the InferenceService", func() {
			By("Updating an InferenceService with a new ImagePullSecret and checking the deployment")
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      constants.InferenceServiceConfigMapName,
					Namespace: constants.KServeNamespace,
				},
				Data: configs,
			}
			Expect(k8sClient.Create(context.TODO(), configMap)).NotTo(HaveOccurred())
			defer k8sClient.Delete(context.TODO(), configMap)

			servingRuntime := &v1alpha1.ServingRuntime{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "vllm-runtime",
					Namespace: constants.KServeNamespace,
				},
				Spec: v1alpha1.ServingRuntimeSpec{
					SupportedModelFormats: []v1alpha1.SupportedModelFormat{
						{
							AutoSelect: proto.Bool(true),
							Name:       "vLLM",
						},
					},
					ServingRuntimePodSpec: v1alpha1.ServingRuntimePodSpec{
						Containers: []corev1.Container{
							{
								Name:    constants.InferenceServiceContainerName,
								Image:   "kserve/vllm:latest",
								Command: []string{"bash", "-c"},
								Args: []string{
									"python2 -m vllm --model_name=${MODEL_NAME} --model_dir=${MODEL} --tensor-parallel-size=${TENSOR_PARALLEL_SIZE} --pipeline-parallel-size=${PIPELINE_PARALLEL_SIZE}",
								},
								Resources: defaultResource,
							},
						},
					},
					Disabled: proto.Bool(false),
				},
			}

			k8sClient.Create(context.TODO(), servingRuntime)
			defer k8sClient.Delete(context.TODO(), servingRuntime)
			serviceName := "modelcar-raw-deployment"
			expectedRequest := reconcile.Request{NamespacedName: types.NamespacedName{Name: serviceName, Namespace: constants.KServeNamespace}}
			serviceKey := expectedRequest.NamespacedName
			storageUri := "oci://test/mnist/export"
			ctx := context.Background()
			isvc := &v1beta1.InferenceService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      serviceKey.Name,
					Namespace: serviceKey.Namespace,
					Annotations: map[string]string{
						"serving.kserve.io/deploymentMode":              "RawDeployment",
						"serving.kserve.io/autoscalerClass":             "hpa",
						"serving.kserve.io/metrics":                     "cpu",
						"serving.kserve.io/targetUtilizationPercentage": "75",
					},
				},
				Spec: v1beta1.InferenceServiceSpec{
					Predictor: v1beta1.PredictorSpec{
						ComponentExtensionSpec: v1beta1.ComponentExtensionSpec{
							MinReplicas: ptr.To(int32(1)),
							MaxReplicas: 2,
						},
						PodSpec: v1beta1.PodSpec{
							ImagePullSecrets: []corev1.LocalObjectReference{
								{Name: "isvc-image-pull-secret"},
							},
						},
						Model: &v1beta1.ModelSpec{
							ModelFormat: v1beta1.ModelFormat{
								Name: "vLLM",
							},
							PredictorExtensionSpec: v1beta1.PredictorExtensionSpec{
								StorageURI:     &storageUri,
								RuntimeVersion: proto.String("0.14.0"),
								Container: corev1.Container{
									Name: constants.InferenceServiceContainerName,
									Resources: corev1.ResourceRequirements{
										Limits: corev1.ResourceList{
											constants.NvidiaGPUResourceType: resource.MustParse("1"),
										},
										Requests: corev1.ResourceList{
											constants.NvidiaGPUResourceType: resource.MustParse("1"),
										},
									},
								},
							},
						},
					},
				},
			}

			isvc.DefaultInferenceService(nil, nil, &v1beta1.SecurityConfig{AutoMountServiceAccountToken: false}, nil)
			Expect(k8sClient.Create(ctx, isvc)).Should(Succeed())
			defer k8sClient.Delete(ctx, isvc)

			inferenceService := &v1beta1.InferenceService{}

			Eventually(func() bool {
				return k8sClient.Get(ctx, serviceKey, inferenceService) == nil
			}, timeout, interval).Should(BeTrue())

			actualDeployment := &appsv1.Deployment{}
			predictorDeploymentKey := types.NamespacedName{
				Name:      constants.PredictorServiceName(serviceKey.Name),
				Namespace: serviceKey.Namespace,
			}
			Eventually(func() error { return k8sClient.Get(context.TODO(), predictorDeploymentKey, actualDeployment) }, timeout, interval).
				Should(Succeed())

			Expect(actualDeployment.Spec.Template.Spec.ImagePullSecrets).To(HaveLen(1))
			Expect(actualDeployment.Spec.Template.Spec.ImagePullSecrets[0].Name).To(Equal("isvc-image-pull-secret"))

			Expect(k8sClient.Get(ctx, serviceKey, inferenceService)).Should(Succeed())
			updateForInferenceService := inferenceService.DeepCopy()
			updateForInferenceService.Spec.Predictor.PodSpec.ImagePullSecrets = []corev1.LocalObjectReference{
				{Name: "new-image-pull-secret"},
			}
			expectedImagePullSecrets := updateForInferenceService.Spec.Predictor.PodSpec.ImagePullSecrets
			Eventually(func() error {
				return k8sClient.Update(ctx, updateForInferenceService)
			}, timeout, interval).Should(Succeed())

			updatedDeployment := &appsv1.Deployment{}
			Eventually(func() (bool, error) {
				if err := k8sClient.Get(ctx, predictorDeploymentKey, updatedDeployment); err != nil {
					return false, err
				}
				if len(updatedDeployment.Spec.Template.Spec.ImagePullSecrets) != 1 {
					return false, nil
				}
				return reflect.DeepEqual(updatedDeployment.Spec.Template.Spec.ImagePullSecrets, expectedImagePullSecrets), nil
			}, timeout, interval).Should(BeTrue())
		})
	})
})

func verifyEnvKeyValueDeployments(actualDefaultDeployment *appsv1.Deployment, envKey string, expectedEnvValue string) {
	// default deployment
	if envValue, exists := utils.GetEnvVarValue(actualDefaultDeployment.Spec.Template.Spec.Containers[0].Env, envKey); exists {
		Expect(envValue).Should(Equal(expectedEnvValue))
	} else {
		Fail(envKey + " environment variable is not set")
	}
}

func verifyGPUResourceSizeDeployment(actualDefaultDeployment *appsv1.Deployment, actualWorkerDeployment *appsv1.Deployment, targetHeadGPUResourceSize, targetWorkerGPUResourceSize string, headGpuResourceType corev1.ResourceName, workerGpuResourceType corev1.ResourceName) {
	headGpuResourceQuantity := resource.MustParse(targetHeadGPUResourceSize)
	workerGpuResourceQuantity := resource.MustParse(targetWorkerGPUResourceSize)

	// head node deployment
	Expect(actualDefaultDeployment.Spec.Template.Spec.Containers[0].Resources.Limits[headGpuResourceType]).Should(Equal(headGpuResourceQuantity))
	Expect(actualDefaultDeployment.Spec.Template.Spec.Containers[0].Resources.Requests[headGpuResourceType]).Should(Equal(headGpuResourceQuantity))

	// worker node deployment
	Expect(actualWorkerDeployment.Spec.Template.Spec.Containers[0].Resources.Limits[workerGpuResourceType]).Should(Equal(workerGpuResourceQuantity))
	Expect(actualWorkerDeployment.Spec.Template.Spec.Containers[0].Resources.Requests[workerGpuResourceType]).Should(Equal(workerGpuResourceQuantity))
}
