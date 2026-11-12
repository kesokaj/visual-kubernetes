package deploy

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var allInstallManifests = []string{
	installPath,
	kubeSystemPath,
	lbPath,
	demoPath,
	iapPath,
}

var allowedStatelessKinds = map[string]bool{
	"Namespace":          true,
	"ServiceAccount":     true,
	"ClusterRole":        true,
	"ClusterRoleBinding": true,
	"Deployment":         true,
	"Service":            true,
	"NetworkPolicy":      true,
}

// cleanUninstallViolations guarantees that `kubectl delete --ignore-not-found -f <manifest>`
// removes every installed object cleanly without hanging on finalizers, leaving behind orphaned
// PVCs/CRDs/webhooks/APIServices, or lacking the canonical `app.kubernetes.io/name: visual-kubernetes`
// selector label across all 5 install variants.
func cleanUninstallViolations(objs []runtime.Object) []string {
	var bad []string
	if len(objs) == 0 {
		return []string{"manifest contains zero objects"}
	}
	seenNamespaced := make(map[string]bool)
	for _, o := range objs {
		gvk := o.GetObjectKind().GroupVersionKind()
		if !allowedStatelessKinds[gvk.Kind] {
			bad = append(bad, "disallowed stateful or cluster-hook kind "+gvk.Kind)
		}
		m, err := meta.Accessor(o)
		if err != nil {
			bad = append(bad, "object lacks metadata accessor")
			continue
		}
		if gvk.Kind == "Namespace" && seenNamespaced[m.GetName()] {
			bad = append(bad, "Namespace/"+m.GetName()+" appears after a namespaced object in that namespace (breaks cold-cluster kubectl apply -f)")
		}
		if m.GetNamespace() != "" {
			seenNamespaced[m.GetNamespace()] = true
		}
		if len(m.GetFinalizers()) > 0 {
			bad = append(bad, gvk.Kind+"/"+m.GetName()+" carries finalizers that block clean uninstall")
		}
		if m.GetLabels()["app.kubernetes.io/name"] != appName {
			bad = append(bad, gvk.Kind+"/"+m.GetName()+" missing label app.kubernetes.io/name="+appName)
		}
	}
	return bad
}

func TestAllManifestsAreCleanlyInstallableAndUninstallable(t *testing.T) {
	for _, path := range allInstallManifests {
		t.Run(path, func(t *testing.T) {
			muts := map[string]mutation{
				"finalizer-on-deployment": func(objs []runtime.Object) []runtime.Object {
					dep, _ := appPodIn(objs, targetNamespaceFor(path))
					dep.Finalizers = []string{"visual-kubernetes.io/keep"}
					return objs
				},
				"missing-app-label-on-clusterrole": func(objs []runtime.Object) []runtime.Object {
					delete(role(objs).Labels, "app.kubernetes.io/name")
					return objs
				},
				"stateful-pvc-added": func(objs []runtime.Object) []runtime.Object {
					pvc := &corev1.PersistentVolumeClaim{
						TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"},
						ObjectMeta: metav1.ObjectMeta{
							Name:      "data",
							Namespace: targetNamespaceFor(path),
							Labels:    map[string]string{"app.kubernetes.io/name": appName},
						},
					}
					return append(objs, pvc)
				},
			}
			if path != kubeSystemPath {
				muts["namespace-ordered-last"] = func(objs []runtime.Object) []runtime.Object {
					if len(objs) < 2 {
						return objs
					}
					reordered := append([]runtime.Object{}, objs[1:]...)
					return append(reordered, objs[0])
				}
			}
			checkFileBothWays(t, path, cleanUninstallViolations, muts)
		})
	}
}

func targetNamespaceFor(path string) string {
	if path == kubeSystemPath {
		return kubeSystemNS
	}
	return appName
}
