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
	"sort"
	"testing"

	"github.com/kserve/kserve/pkg/apis/serving/v1beta1"
	"github.com/kserve/kserve/pkg/constants"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func predictorPodSpecWithContainerEnvs(envs []corev1.EnvVar) *corev1.PodSpec {
	spec := predictorPodSpecWithLivenessHTTPGet()
	spec.Containers[0].Env = envs
	return spec
}

func predictorPodSpecWithImagePullSecrets(secrets []corev1.LocalObjectReference) *corev1.PodSpec {
	spec := predictorPodSpecWithLivenessHTTPGet()
	spec.ImagePullSecrets = secrets
	return spec
}

func envVarNames(envs []corev1.EnvVar) []string {
	names := make([]string, 0, len(envs))
	for _, e := range envs {
		names = append(names, e.Name)
	}
	sort.Strings(names)
	return names
}

func imagePullSecretNames(secrets []corev1.LocalObjectReference) []string {
	names := make([]string, 0, len(secrets))
	for _, s := range secrets {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	return names
}

// TestDeploymentReconciler_Reconcile_removesContainerEnvVars verifies the two-way merge + Update path
// removes env vars that are no longer in desired (not a blind desired-spec replace).
func TestDeploymentReconciler_Reconcile_removesContainerEnvVars(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	const isvc = "env-reconcile"
	meta := rawDeploymentComponentMeta(isvc)
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()

	depInitial, err := createRawDefaultDeployment(meta, &v1beta1.ComponentExtensionSpec{}, predictorPodSpecWithContainerEnvs([]corev1.EnvVar{
		{Name: "KEEP_ME", Value: "v1"},
		{Name: "REMOVE_ME", Value: "gone"},
	}))
	if err != nil {
		t.Fatalf("createRawDefaultDeployment: %v", err)
	}

	rec := &DeploymentReconciler{
		client:         cli,
		scheme:         scheme,
		DeploymentList: []*appsv1.Deployment{depInitial.DeepCopy()},
	}
	if _, err := rec.Reconcile(); err != nil {
		t.Fatalf("Reconcile (create): %v", err)
	}

	depUpdated, err := createRawDefaultDeployment(meta, &v1beta1.ComponentExtensionSpec{}, predictorPodSpecWithContainerEnvs([]corev1.EnvVar{
		{Name: "KEEP_ME", Value: "v1"},
	}))
	if err != nil {
		t.Fatalf("createRawDefaultDeployment (update): %v", err)
	}

	rec2 := &DeploymentReconciler{
		client:         cli,
		scheme:         scheme,
		DeploymentList: []*appsv1.Deployment{depUpdated.DeepCopy()},
	}
	if _, err := rec2.Reconcile(); err != nil {
		t.Fatalf("Reconcile (update): %v", err)
	}

	var got appsv1.Deployment
	if err := cli.Get(context.Background(), types.NamespacedName{
		Namespace: depUpdated.Namespace,
		Name:      depUpdated.Name,
	}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	c := containerByName(&got.Spec.Template, constants.InferenceServiceContainerName)
	if c == nil {
		t.Fatal("kserve-container not found")
	}
	gotNames := envVarNames(c.Env)
	wantNames := []string{"KEEP_ME"}
	if len(gotNames) != len(wantNames) {
		t.Fatalf("env names: got %v, want %v (full env: %#v)", gotNames, wantNames, c.Env)
	}
	for i := range wantNames {
		if gotNames[i] != wantNames[i] {
			t.Fatalf("env names: got %v, want %v", gotNames, wantNames)
		}
	}
}

// TestDeploymentReconciler_Reconcile_updatesContainerEnvValue verifies an env value change is applied
// through merge + Update, not only removals.
func TestDeploymentReconciler_Reconcile_updatesContainerEnvValue(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	const isvc = "env-value-reconcile"
	meta := rawDeploymentComponentMeta(isvc)
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()

	depInitial, err := createRawDefaultDeployment(meta, &v1beta1.ComponentExtensionSpec{}, predictorPodSpecWithContainerEnvs([]corev1.EnvVar{
		{Name: "MODEL_ID", Value: "old"},
	}))
	if err != nil {
		t.Fatalf("createRawDefaultDeployment: %v", err)
	}

	rec := &DeploymentReconciler{
		client:         cli,
		scheme:         scheme,
		DeploymentList: []*appsv1.Deployment{depInitial.DeepCopy()},
	}
	if _, err := rec.Reconcile(); err != nil {
		t.Fatalf("Reconcile (create): %v", err)
	}

	depUpdated, err := createRawDefaultDeployment(meta, &v1beta1.ComponentExtensionSpec{}, predictorPodSpecWithContainerEnvs([]corev1.EnvVar{
		{Name: "MODEL_ID", Value: "new"},
	}))
	if err != nil {
		t.Fatalf("createRawDefaultDeployment (update): %v", err)
	}

	rec2 := &DeploymentReconciler{
		client:         cli,
		scheme:         scheme,
		DeploymentList: []*appsv1.Deployment{depUpdated.DeepCopy()},
	}
	if _, err := rec2.Reconcile(); err != nil {
		t.Fatalf("Reconcile (update): %v", err)
	}

	var got appsv1.Deployment
	if err := cli.Get(context.Background(), types.NamespacedName{
		Namespace: depUpdated.Namespace,
		Name:      depUpdated.Name,
	}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	c := containerByName(&got.Spec.Template, constants.InferenceServiceContainerName)
	if c == nil {
		t.Fatal("kserve-container not found")
	}
	var modelID string
	for _, e := range c.Env {
		if e.Name == "MODEL_ID" {
			modelID = e.Value
			break
		}
	}
	if modelID != "new" {
		t.Fatalf("MODEL_ID value: got %q, want %q (env: %#v)", modelID, "new", c.Env)
	}
}

// TestDeploymentReconciler_Reconcile_updatesImagePullSecrets verifies imagePullSecrets removed from
// desired are dropped on the live Deployment after merge + Update.
func TestDeploymentReconciler_Reconcile_updatesImagePullSecrets(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	const isvc = "pull-secret-reconcile"
	meta := rawDeploymentComponentMeta(isvc)
	cli := fake.NewClientBuilder().WithScheme(scheme).Build()

	depInitial, err := createRawDefaultDeployment(meta, &v1beta1.ComponentExtensionSpec{}, predictorPodSpecWithImagePullSecrets([]corev1.LocalObjectReference{
		{Name: "old-pull-secret"},
		{Name: "shared-pull-secret"},
	}))
	if err != nil {
		t.Fatalf("createRawDefaultDeployment: %v", err)
	}

	rec := &DeploymentReconciler{
		client:         cli,
		scheme:         scheme,
		DeploymentList: []*appsv1.Deployment{depInitial.DeepCopy()},
	}
	if _, err := rec.Reconcile(); err != nil {
		t.Fatalf("Reconcile (create): %v", err)
	}

	depUpdated, err := createRawDefaultDeployment(meta, &v1beta1.ComponentExtensionSpec{}, predictorPodSpecWithImagePullSecrets([]corev1.LocalObjectReference{
		{Name: "new-pull-secret"},
		{Name: "shared-pull-secret"},
	}))
	if err != nil {
		t.Fatalf("createRawDefaultDeployment (update): %v", err)
	}

	rec2 := &DeploymentReconciler{
		client:         cli,
		scheme:         scheme,
		DeploymentList: []*appsv1.Deployment{depUpdated.DeepCopy()},
	}
	if _, err := rec2.Reconcile(); err != nil {
		t.Fatalf("Reconcile (update): %v", err)
	}

	var got appsv1.Deployment
	if err := cli.Get(context.Background(), types.NamespacedName{
		Namespace: depUpdated.Namespace,
		Name:      depUpdated.Name,
	}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	gotNames := imagePullSecretNames(got.Spec.Template.Spec.ImagePullSecrets)
	wantNames := []string{"new-pull-secret", "shared-pull-secret"}
	if len(gotNames) != len(wantNames) {
		t.Fatalf("imagePullSecrets: got %v, want %v", gotNames, wantNames)
	}
	for i := range wantNames {
		if gotNames[i] != wantNames[i] {
			t.Fatalf("imagePullSecrets: got %v, want %v", gotNames, wantNames)
		}
	}
}
