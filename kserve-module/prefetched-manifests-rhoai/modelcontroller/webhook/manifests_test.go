/*
Copyright 2026.

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

package webhook

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

func TestLLMInferenceServiceMutatingWebhooksCoverServedVersions(t *testing.T) {
	crdYAML, err := os.ReadFile("../crd/external/serving.kserve.io_llminferenceservices.yaml")
	require.NoError(t, err)
	var crd apiextensionsv1.CustomResourceDefinition
	require.NoError(t, yaml.Unmarshal(crdYAML, &crd))
	served := map[string]int{}
	for _, version := range crd.Spec.Versions {
		if version.Served {
			served[version.Name] = 1
		}
	}
	require.NotEmpty(t, served)

	manifest, err := os.ReadFile("manifests.yaml")
	require.NoError(t, err)
	var config admissionregistrationv1.MutatingWebhookConfiguration
	require.NoError(t, k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifest), 4096).Decode(&config))
	assertLLMWebhookRules(t, config.Webhooks, served)

	// xKS replaces the webhook list, so it must keep the same version coverage.
	patchYAML, err := os.ReadFile("../overlays/xks/patches/mutating-webhooks-xks.yaml")
	require.NoError(t, err)
	var patches []struct {
		Op    string                                    `json:"op"`
		Path  string                                    `json:"path"`
		Value []admissionregistrationv1.MutatingWebhook `json:"value"`
	}
	require.NoError(t, yaml.Unmarshal(patchYAML, &patches))
	require.Len(t, patches, 1)
	require.Equal(t, "replace", patches[0].Op)
	require.Equal(t, "/webhooks", patches[0].Path)
	assertLLMWebhookRules(t, patches[0].Value, served)
}

func assertLLMWebhookRules(t *testing.T, webhooks []admissionregistrationv1.MutatingWebhook, served map[string]int) {
	t.Helper()
	covered := map[string]int{}
	for _, webhook := range webhooks {
		if !strings.HasPrefix(webhook.Name, "connection-llmisvc-") {
			continue
		}
		require.NotNil(t, webhook.MatchPolicy, webhook.Name)
		assert.Equal(t, admissionregistrationv1.Exact, *webhook.MatchPolicy, webhook.Name)
		require.Len(t, webhook.Rules, 1, webhook.Name)
		rule := webhook.Rules[0]
		assert.Equal(t, []string{"serving.kserve.io"}, rule.APIGroups, webhook.Name)
		assert.Equal(t, []string{"llminferenceservices"}, rule.Resources, webhook.Name)
		assert.ElementsMatch(t, []admissionregistrationv1.OperationType{
			admissionregistrationv1.Create, admissionregistrationv1.Update,
		}, rule.Operations, webhook.Name)
		require.Len(t, rule.APIVersions, 1, webhook.Name)
		assert.Equal(t, "connection-llmisvc-"+rule.APIVersions[0]+".odh-model-controller.opendatahub.io", webhook.Name)
		covered[rule.APIVersions[0]]++
	}
	assert.Equal(t, served, covered, "each served LLMInferenceService version needs exactly one Exact mutating webhook")
}
