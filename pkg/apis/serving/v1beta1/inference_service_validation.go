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

package v1beta1

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"k8s.io/apimachinery/pkg/runtime"
	"knative.dev/serving/pkg/apis/autoscaling"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/kserve/kserve/pkg/apis/serving/v1alpha1"
	"github.com/kserve/kserve/pkg/constants"
	"github.com/kserve/kserve/pkg/utils"
)

// regular expressions for validation of isvc name
const (
	IsvcNameFmt                         string = "[a-z]([-a-z0-9]*[a-z0-9])?"
	StorageUriPresentInTransformerError string = "storage uri should not be specified in transformer container"
)

var (
	// logger for the validation webhook.
	validatorLogger = logf.Log.WithName("inferenceservice-v1beta1-validation-webhook")
	// regular expressions for validation of isvc name
	IsvcRegexp = regexp.MustCompile("^" + IsvcNameFmt + "$")
)

// +kubebuilder:object:generate=false
// +k8s:deepcopy-gen=false
// +k8s:openapi-gen=false
// InferenceServiceValidator is responsible for validating the InferenceService resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false and +k8s:deepcopy-gen=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type InferenceServiceValidator struct {
	Client client.Client
}

// +kubebuilder:webhook:verbs=create;update;delete,path=/validate-inferenceservices,mutating=false,failurePolicy=fail,groups=serving.kserve.io,resources=inferenceservices,versions=v1beta1,name=inferenceservice.kserve-webhook-server.validator
var _ webhook.CustomValidator = &InferenceServiceValidator{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (v *InferenceServiceValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	isvc, err := convertToInferenceService(obj)
	if err != nil {
		validatorLogger.Error(err, "Unable to convert object to InferenceService")
		return nil, err
	}
	validatorLogger.Info("validate create", "name", isvc.Name)
	return validateInferenceService(isvc)
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (v *InferenceServiceValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	isvc, err := convertToInferenceService(newObj)
	if err != nil {
		validatorLogger.Error(err, "Unable to convert object to InferenceService")
		return nil, err
	}
	oldIsvc, err := convertToInferenceService(oldObj)
	if err != nil {
		validatorLogger.Error(err, "Unable to convert object to InferenceService")
	}
	validatorLogger.Info("validate update", "name", isvc.Name)
	err = validateDeploymentMode(isvc, oldIsvc)
	if err != nil {
		return nil, err
	}
	return validateInferenceService(isvc)
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (v *InferenceServiceValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	isvc, err := convertToInferenceService(obj)
	if err != nil {
		validatorLogger.Error(err, "Unable to convert object to InferenceService")
		return nil, err
	}
	validatorLogger.Info("validate delete", "name", isvc.Name)
	return v.validateInferenceServiceReferences(ctx, isvc)
}

// validateInferenceServiceReferences checks if there are any InferenceGraphs that are referencing the given
// InferenceService in isvc argument, and returns an error if there are references to it.
func (v *InferenceServiceValidator) validateInferenceServiceReferences(ctx context.Context, isvc *InferenceService) (admission.Warnings, error) {
	igList := v1alpha1.InferenceGraphList{}
	err := v.Client.List(ctx, &igList, client.InNamespace(isvc.GetNamespace()))
	if err != nil {
		return admission.Warnings{}, fmt.Errorf("failed to fetch list of InferenceGraphs: %w", err)
	}

	var isvcReferences []string
	for _, ig := range igList.Items {
	node_loop:
		for _, igNode := range ig.Spec.Nodes {
			for _, step := range igNode.Steps {
				if step.ServiceName == isvc.GetName() {
					isvcReferences = append(isvcReferences, ig.GetName())
					break node_loop
				}
			}
		}
	}

	if len(isvcReferences) != 0 {
		return admission.Warnings{}, fmt.Errorf("InferenceService [%s] is being used in the following InferenceGraphs: %s", isvc.GetName(), strings.Join(isvcReferences, ", "))
	}

	return admission.Warnings{}, nil
}

// GetIntReference returns the pointer for the integer input
func GetIntReference(number int) *int {
	num := number
	return &num
}

func validateInferenceService(isvc *InferenceService) (admission.Warnings, error) {
	var allWarnings admission.Warnings
	annotations := isvc.Annotations

	if err := validateInferenceServiceName(isvc); err != nil {
		return allWarnings, err
	}

	if err := validateInferenceServiceAutoscaler(isvc); err != nil {
		return allWarnings, err
	}

	if err := validateAutoscalerTargetUtilizationPercentage(isvc); err != nil {
		return allWarnings, err
	}

	if err := validateMultiNodeVariables(isvc); err != nil {
		return allWarnings, err
	}

	if err := validateCollocationStorageURI(isvc.Spec.Predictor); err != nil {
		return allWarnings, err
	}

	for _, component := range []Component{
		&isvc.Spec.Predictor,
		isvc.Spec.Transformer,
		isvc.Spec.Explainer,
	} {
		if !reflect.ValueOf(component).IsNil() {
			if err := validateExactlyOneImplementation(component); err != nil {
				return allWarnings, err
			}
			if err := utils.FirstNonNilError([]error{
				component.GetImplementation().Validate(),
				component.GetExtensions().Validate(),
				validateAutoScalingCompExtension(annotations, component.GetExtensions()),
			}); err != nil {
				return allWarnings, err
			}
		}
	}
	return allWarnings, nil
}

// validateMultiNodeVariables validates when there is workerSpec set in isvc
func validateMultiNodeVariables(isvc *InferenceService) error {
	if isvc.Spec.Predictor.WorkerSpec != nil {
		if len(isvc.Spec.Predictor.WorkerSpec.Containers) > 1 {
			return fmt.Errorf(DisallowedMultipleContainersInWorkerSpecError, isvc.Name)
		}
		if isvc.Spec.Predictor.Model != nil {
			if _, exists := utils.GetEnvVarValue(isvc.Spec.Predictor.Model.PredictorExtensionSpec.Container.Env, constants.PipelineParallelSizeEnvName); exists {
				return fmt.Errorf(DisallowedWorkerSpecPipelineParallelSizeEnvError, isvc.Name)
			}
			if _, exists := utils.GetEnvVarValue(isvc.Spec.Predictor.Model.PredictorExtensionSpec.Container.Env, constants.TensorParallelSizeEnvName); exists {
				return fmt.Errorf(DisallowedWorkerSpecTensorParallelSizeEnvError, isvc.Name)
			}

			customGPUResourceTypes := isvc.GetAnnotations()[constants.CustomGPUResourceTypesAnnotationKey]
			if customGPUResourceTypes != "" {
				if !utils.IsValidCustomGPUArray(customGPUResourceTypes) {
					return fmt.Errorf(InvalidCustomGPUTypesAnnotationFormatError, isvc.Name, constants.CustomGPUResourceTypesAnnotationKey)
				}
			}

			if utils.IsUnknownGpuResourceType(isvc.Spec.Predictor.Model.Resources, customGPUResourceTypes) {
				return fmt.Errorf(InvalidUnknownGPUTypeError, isvc.Name)
			}

			if isvc.Spec.Predictor.Model.StorageURI == nil {
				return fmt.Errorf(MissingStorageURI, isvc.Name)
			} else {
				storageProtocol := strings.Split(*isvc.Spec.Predictor.Model.StorageURI, "://")[0]
				if storageProtocol != "pvc" && storageProtocol != "oci" {
					return fmt.Errorf(InvalidNotSupportedStorageURIProtocolError, isvc.Name, storageProtocol)
				}
			}
			if isvc.GetAnnotations()[constants.AutoscalerClass] != string(constants.AutoscalerClassExternal) {
				return fmt.Errorf(InvalidAutoScalerError, isvc.Name, isvc.GetAnnotations()[constants.AutoscalerClass])
			}
		}

		// WorkerSpec.PipelineParallelSize should not be less than 2 (head + worker)
		if pps := isvc.Spec.Predictor.WorkerSpec.PipelineParallelSize; pps != nil && *pps < 2 {
			return fmt.Errorf(InvalidWorkerSpecPipelineParallelSizeValueError, isvc.Name, strconv.Itoa(*pps))
		}

		// WorkerSpec.TensorParallelSize should not be less than 1.
		if tps := isvc.Spec.Predictor.WorkerSpec.TensorParallelSize; tps != nil && *tps < 1 {
			return fmt.Errorf(InvalidWorkerSpecTensorParallelSizeValueError, isvc.Name, strconv.Itoa(*tps))
		}

		if isvc.Spec.Predictor.WorkerSpec.Containers != nil {
			for _, container := range isvc.Spec.Predictor.WorkerSpec.Containers {
				if utils.IsUnknownGpuResourceType(container.Resources, isvc.GetAnnotations()[constants.CustomGPUResourceTypesAnnotationKey]) {
					return fmt.Errorf(InvalidUnknownGPUTypeError, isvc.Name)
				}
			}
		}
	}
	return nil
}

// Validate scaling options component extensions
func validateAutoScalingCompExtension(annotations map[string]string, compExtSpec *ComponentExtensionSpec) error {
	deploymentMode := annotations["serving.kserve.io/deploymentMode"]
	annotationClass := annotations[autoscaling.ClassAnnotationKey]
	if deploymentMode == string(constants.RawDeployment) || annotationClass == string(autoscaling.HPA) {
		return validateScalingHPACompExtension(compExtSpec)
	}

	return validateScalingKPACompExtension(compExtSpec)
}

// Validation of isvc name
func validateInferenceServiceName(isvc *InferenceService) error {
	if !IsvcRegexp.MatchString(isvc.Name) {
		return fmt.Errorf(InvalidISVCNameFormatError, isvc.Name, IsvcNameFmt)
	}
	return nil
}

// Validation of isvc autoscaler class
func validateInferenceServiceAutoscaler(isvc *InferenceService) error {
	annotations := isvc.ObjectMeta.Annotations
	value, ok := annotations[constants.AutoscalerClass]
	class := constants.AutoscalerClassType(value)
	if ok {
		for _, item := range constants.AutoscalerAllowedClassList {
			if class == item {
				switch class {
				case constants.AutoscalerClassHPA:
					if metric, ok := annotations[constants.AutoscalerMetrics]; ok {
						return validateHPAMetrics(ScaleMetric(metric))
					} else {
						return nil
					}
				case constants.AutoscalerClassExternal:
					return nil
				default:
					return fmt.Errorf("unknown autoscaler class [%s]", class)
				}
			}
		}
		return fmt.Errorf("[%s] is not a supported autoscaler class type", value)
	}

	return nil
}

// Validate of autoscaler HPA metrics
func validateHPAMetrics(metric ScaleMetric) error {
	for _, item := range constants.AutoscalerAllowedMetricsList {
		if item == constants.AutoscalerMetricsType(metric) {
			return nil
		}
	}
	return fmt.Errorf("[%s] is not a supported metric", metric)
}

// Validate of autoscaler targetUtilizationPercentage
func validateAutoscalerTargetUtilizationPercentage(isvc *InferenceService) error {
	annotations := isvc.ObjectMeta.Annotations
	if value, ok := annotations[constants.TargetUtilizationPercentage]; ok {
		t, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("the target utilization percentage should be a [1-100] integer")
		} else if t < 1 || t > 100 {
			return fmt.Errorf("the target utilization percentage should be a [1-100] integer")
		}
	}

	return nil
}

func validateScalingHPACompExtension(compExtSpec *ComponentExtensionSpec) error {
	metric := MetricCPU
	if compExtSpec.ScaleMetric != nil {
		metric = *compExtSpec.ScaleMetric
	}

	err := validateHPAMetrics(metric)

	if err != nil {
		return err
	}

	if compExtSpec.ScaleTarget != nil {
		target := *compExtSpec.ScaleTarget
		if metric == MetricCPU && target < 1 || target > 100 {
			return fmt.Errorf("the target utilization percentage should be a [1-100] integer")
		}

		if metric == MetricMemory && target < 1 {
			return fmt.Errorf("the target memory should be greater than 1 MiB")
		}
	}

	return nil
}

func validateKPAMetrics(metric ScaleMetric) error {
	for _, item := range constants.AutoScalerKPAMetricsAllowedList {
		if item == constants.AutoScalerKPAMetricsType(metric) {
			return nil
		}
	}
	return fmt.Errorf("[%s] is not a supported metric", metric)
}

func validateScalingKPACompExtension(compExtSpec *ComponentExtensionSpec) error {
	if compExtSpec.DeploymentStrategy != nil {
		return fmt.Errorf("customizing deploymentStrategy is only supported for raw deployment mode")
	}
	metric := MetricConcurrency
	if compExtSpec.ScaleMetric != nil {
		metric = *compExtSpec.ScaleMetric
	}

	err := validateKPAMetrics(metric)

	if err != nil {
		return err
	}

	if compExtSpec.ScaleTarget != nil {
		target := *compExtSpec.ScaleTarget

		if metric == MetricRPS && target < 1 {
			return fmt.Errorf("the target for rps should be greater than 1")
		}
	}

	return nil
}

// validates if transformer container has storage uri or not in collocation of predictor and transformer scenario
func validateCollocationStorageURI(predictorSpec PredictorSpec) error {
	for _, container := range predictorSpec.Containers {
		if container.Name == constants.TransformerContainerName {
			for _, env := range container.Env {
				if env.Name == constants.CustomSpecStorageUriEnvVarKey {
					return errors.New(StorageUriPresentInTransformerError)
				}
			}
			break
		}
	}
	return nil
}

// validates if the deploymentMode specified in the annotation is not different from the one recorded in the status
func validateDeploymentMode(newIsvc *InferenceService, oldIsvc *InferenceService) error {
	statusDeploymentMode := oldIsvc.Status.DeploymentMode
	if len(statusDeploymentMode) != 0 {
		annotations := newIsvc.Annotations
		annotationDeploymentMode, ok := annotations[constants.DeploymentMode]
		if ok && annotationDeploymentMode != statusDeploymentMode {
			return fmt.Errorf("update rejected: deploymentMode cannot be changed from '%s' to '%s'", statusDeploymentMode, annotationDeploymentMode)
		}
	}
	return nil
}

// Convert runtime.Object into InferenceService
func convertToInferenceService(obj runtime.Object) (*InferenceService, error) {
	isvc, ok := obj.(*InferenceService)
	if !ok {
		return nil, fmt.Errorf("expected an InferenceService object but got %T", obj)
	}
	return isvc, nil
}
