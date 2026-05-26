/*
Copyright 2026 The KServe Authors.

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

package deployment

import (
	"context"
	"testing"

	"github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kserve/kserve/pkg/constants"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func predictorPodSpecWithLivenessHTTPGet() *corev1.PodSpec {
	return &corev1.PodSpec{
		Containers: []corev1.Container{{
			Name:  constants.InferenceServiceContainerName,
			Image: "docker.io/kserve/sklearnserver:v0.15.0",
			Args:  []string{"--model_name=probe-update", "--http_port=8080"},
			Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 8080, Protocol: corev1.ProtocolTCP}},
			LivenessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path:   "/v2/health/live",
						Port:   intstr.FromString("http"),
						Scheme: corev1.URISchemeHTTP,
					},
				},
				TimeoutSeconds: 5,
				PeriodSeconds:  30,
			},
		}},
	}
}

func predictorPodSpecWithLivenessExec() *corev1.PodSpec {
	return &corev1.PodSpec{
		Containers: []corev1.Container{{
			Name:  constants.InferenceServiceContainerName,
			Image: "docker.io/kserve/sklearnserver:v0.15.0",
			Args:  []string{"--model_name=probe-update", "--http_port=8080"},
			Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 8080, Protocol: corev1.ProtocolTCP}},
			LivenessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					Exec: &corev1.ExecAction{
						Command: []string{"/bin/sh", "-c", "true"},
					},
				},
				InitialDelaySeconds: 10,
				TimeoutSeconds:      10,
				PeriodSeconds:       30,
				FailureThreshold:    3,
			},
		}},
	}
}

func rawDeploymentComponentMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:      name + "-predictor",
		Namespace: "default",
		Labels: map[string]string{
			constants.DeploymentMode: string(constants.RawDeployment),
		},
	}
}

// TestSyncContainerProbesFromDesired_replacesProbes documents RHOAIENG-33695: a bad merge can leave
// both httpGet and exec set; desired template must win for each probe field.
func TestSyncContainerProbesFromDesired_replacesProbes(t *testing.T) {
	dstTemplate := &corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: constants.InferenceServiceContainerName,
				LivenessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{Path: "/v2/health/live", Port: intstr.FromString("http")},
						Exec:    &corev1.ExecAction{Command: []string{"should-not-remain"}},
					},
				},
				ReadinessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{Path: "/old", Port: intstr.FromString("http")},
					},
				},
			}},
		},
	}
	srcTemplate := &corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: constants.InferenceServiceContainerName,
				LivenessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						Exec: &corev1.ExecAction{Command: []string{"/bin/sh", "-c", "true"}},
					},
					InitialDelaySeconds: 10,
				},
				ReadinessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{Path: "/v2/health/ready", Port: intstr.FromString("http")},
					},
				},
				StartupProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						HTTPGet: &corev1.HTTPGetAction{Path: "/v2/health/ready", Port: intstr.FromString("http")},
					},
					FailureThreshold: 30,
				},
			}},
		},
	}

	syncContainerProbesFromDesired(dstTemplate, srcTemplate)
	c := &dstTemplate.Spec.Containers[0]
	if c.LivenessProbe == nil || c.LivenessProbe.Exec == nil || len(c.LivenessProbe.Exec.Command) == 0 {
		t.Fatalf("expected exec liveness, got %#v", c.LivenessProbe)
	}
	if c.LivenessProbe.HTTPGet != nil {
		t.Fatalf("expected httpGet cleared on liveness, got %#v", c.LivenessProbe.HTTPGet)
	}
	if c.ReadinessProbe == nil || c.ReadinessProbe.HTTPGet == nil || c.ReadinessProbe.HTTPGet.Path != "/v2/health/ready" {
		t.Fatalf("expected readiness from desired, got %#v", c.ReadinessProbe)
	}
	if c.StartupProbe == nil || c.StartupProbe.FailureThreshold != 30 {
		t.Fatalf("expected startup from desired, got %#v", c.StartupProbe)
	}
}

// TestDeploymentReconciler_Reconcile_updatesLivenessProbeBetweenHandlers exercises the CheckResultUpdate
// path so liveness can switch between httpGet and exec without an invalid dual-handler Pod spec
// (regression guard for RHOAIENG-33695).
func TestDeploymentReconciler_Reconcile_updatesLivenessProbeBetweenHandlers(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	const isvc = "probe-reconcile"
	meta := rawDeploymentComponentMeta(isvc)

	podHTTP := predictorPodSpecWithLivenessHTTPGet()
	depHTTP, err := createRawDefaultDeployment(meta, &v1beta1.ComponentExtensionSpec{}, podHTTP)
	if err != nil {
		t.Fatalf("createRawDefaultDeployment(http): %v", err)
	}

	cli := fake.NewClientBuilder().WithScheme(scheme).Build()

	rec := &DeploymentReconciler{
		client:         cli,
		scheme:         scheme,
		DeploymentList: []*appsv1.Deployment{depHTTP.DeepCopy()},
	}
	if _, err := rec.Reconcile(); err != nil {
		t.Fatalf("Reconcile (create): %v", err)
	}

	podExec := predictorPodSpecWithLivenessExec()
	depExec, err := createRawDefaultDeployment(meta, &v1beta1.ComponentExtensionSpec{}, podExec)
	if err != nil {
		t.Fatalf("createRawDefaultDeployment(exec): %v", err)
	}

	rec2 := &DeploymentReconciler{
		client:         cli,
		scheme:         scheme,
		DeploymentList: []*appsv1.Deployment{depExec.DeepCopy()},
	}
	if _, err := rec2.Reconcile(); err != nil {
		t.Fatalf("Reconcile (update to exec): %v", err)
	}

	var got appsv1.Deployment
	if err := cli.Get(context.Background(), types.NamespacedName{
		Namespace: depExec.Namespace,
		Name:      depExec.Name,
	}, &got); err != nil {
		t.Fatalf("Get after exec update: %v", err)
	}
	live := containerByName(&got.Spec.Template, constants.InferenceServiceContainerName).LivenessProbe
	if live == nil || live.Exec == nil || live.HTTPGet != nil {
		t.Fatalf("expected single-handler exec liveness after update, got %#v", live)
	}

	podHTTP2 := predictorPodSpecWithLivenessHTTPGet()
	depHTTP2, err := createRawDefaultDeployment(meta, &v1beta1.ComponentExtensionSpec{}, podHTTP2)
	if err != nil {
		t.Fatalf("createRawDefaultDeployment(http2): %v", err)
	}
	rec3 := &DeploymentReconciler{
		client:         cli,
		scheme:         scheme,
		DeploymentList: []*appsv1.Deployment{depHTTP2.DeepCopy()},
	}
	if _, err := rec3.Reconcile(); err != nil {
		t.Fatalf("Reconcile (update back to httpGet): %v", err)
	}
	if err := cli.Get(context.Background(), types.NamespacedName{
		Namespace: depHTTP2.Namespace,
		Name:      depHTTP2.Name,
	}, &got); err != nil {
		t.Fatalf("Get after httpGet update: %v", err)
	}
	live = containerByName(&got.Spec.Template, constants.InferenceServiceContainerName).LivenessProbe
	if live == nil || live.HTTPGet == nil || live.Exec != nil {
		t.Fatalf("expected single-handler httpGet liveness after second update, got %#v", live)
	}
}

func containerByName(template *corev1.PodTemplateSpec, name string) *corev1.Container {
	for i := range template.Spec.Containers {
		if template.Spec.Containers[i].Name == name {
			return &template.Spec.Containers[i]
		}
	}
	return nil
}
