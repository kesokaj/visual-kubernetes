package main

import (
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mt "github.com/kesokaj/visual-kubernetes/internal/model/modeltest"
)

// managedBy returns a copy of d whose newest managedFields entry is manager's (on
// subresource, "" for the object itself): the actor a chain's derived steps name.
func managedBy(d *appsv1.Deployment, manager, subresource string) *appsv1.Deployment {
	c := d.DeepCopy()
	c.ManagedFields = []metav1.ManagedFieldsEntry{
		{Manager: "kubectl-client-side-apply", Operation: metav1.ManagedFieldsOperationUpdate, Time: &metav1.Time{Time: eventBase.Add(-24 * time.Hour)}},
		{Manager: manager, Operation: metav1.ManagedFieldsOperationUpdate, Time: &metav1.Time{Time: eventBase}, Subresource: subresource},
	}
	return c
}

// endpointSlice is the EndpointSlice of Service ns/svc listing pods as ready endpoints, in
// the order given.
func endpointSlice(ns, svc string, pods ...*corev1.Pod) *discoveryv1.EndpointSlice {
	s := &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Namespace: ns, Name: svc + "-8x2kq", Labels: mt.Labels(discoveryv1.LabelServiceName, svc)},
		AddressType: discoveryv1.AddressTypeIPv4,
	}
	s.OwnerReferences = []metav1.OwnerReference{*mt.ControllerRef("v1", "Service", svc)}
	for i, p := range pods {
		ready := true
		s.Endpoints = append(s.Endpoints, discoveryv1.Endpoint{
			Addresses:  []string{fmt.Sprintf("10.244.0.%d", 10+i)},
			Conditions: discoveryv1.EndpointConditions{Ready: &ready},
			NodeName:   &pods[i].Spec.NodeName,
			TargetRef:  &corev1.ObjectReference{Kind: "Pod", Namespace: p.Namespace, Name: p.Name},
		})
	}
	return s
}
