package kservemodule

import (
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/opendatahub-io/odh-platform-utilities/pkg/render/kustomize"
)

func rhoaiManifestRoot(dirName string) string {
	if dirName == KserveComponentName {
		return filepath.Join("..", "..", "..", "config")
	}
	return filepath.Join("..", "..", "prefetched-manifests-rhoai", dirName)
}

func TestRHOAIManifests_RenderForEveryComponent(t *testing.T) {
	for _, comp := range components {
		for _, sourcePath := range []string{comp.sourcePath, comp.sourcePathXKS} {
			if sourcePath == "" {
				continue
			}
			t.Run(comp.name+"/"+sourcePath, func(t *testing.T) {
				g := NewWithT(t)
				resources, err := kustomize.Render(
					filepath.Join(rhoaiManifestRoot(comp.dirName()), sourcePath),
					nil,
					kustomize.WithNamespace("redhat-ods-applications"),
				)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(resources).NotTo(BeEmpty())
			})
		}
	}
}
