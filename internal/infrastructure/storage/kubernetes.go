package storage

import (
	"context"
	"errors"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"olixops-sandbox-platform/internal/domain"
)

type Observation struct{ UID, PV, Phase string }
type Service interface {
	Ensure(context.Context, domain.Workspace) (Observation, error)
	InUse(context.Context, domain.Workspace) (bool, error)
}
type Kubernetes struct {
	core                             v1.CoreV1Interface
	dynamic                          dynamic.Interface
	cluster, namespace, storageClass string
}

func New(kubeconfig, contextName, cluster, namespace, storageClass string) (*Kubernetes, error) {
	var cfg *rest.Config
	var e error
	if kubeconfig == "" {
		cfg, e = rest.InClusterConfig()
	} else {
		if contextName == "" {
			return nil, errors.New("explicit workspace kube context is required")
		}
		rules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
		cfg, e = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{CurrentContext: contextName}).ClientConfig()
	}
	if e != nil {
		return nil, e
	}
	cfg.Timeout = 15e9
	cfg.UserAgent = "olixops-workspaces"
	core, e := v1.NewForConfig(cfg)
	if e != nil {
		return nil, e
	}
	d, e := dynamic.NewForConfig(cfg)
	if e != nil {
		return nil, e
	}
	return &Kubernetes{core, d, cluster, namespace, storageClass}, nil
}
func (k *Kubernetes) check(w domain.Workspace) error {
	if w.ClusterID != k.cluster || w.KubeNamespace != k.namespace || w.StorageClass != k.storageClass {
		return errors.New("workspace storage configuration differs from original target")
	}
	return nil
}
func (k *Kubernetes) Ensure(ctx context.Context, w domain.Workspace) (Observation, error) {
	if e := k.check(w); e != nil {
		return Observation{}, e
	}
	pvcs := k.core.PersistentVolumeClaims(k.namespace)
	p, e := pvcs.Get(ctx, w.PVCName, metav1.GetOptions{})
	if apierrors.IsNotFound(e) && w.PVCUID == "" && w.State == "Creating" {
		sc := w.StorageClass
		p, e = pvcs.Create(ctx, &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: w.PVCName, Labels: map[string]string{"app.kubernetes.io/managed-by": "olixops", "olixops.io/workspace-id": w.ID, "olixops.io/namespace-id": w.NamespaceID}}, Spec: corev1.PersistentVolumeClaimSpec{AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}, StorageClassName: &sc, Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: *resource.NewQuantity(w.RequestedBytes, resource.BinarySI)}}}}, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(e) {
			p, e = pvcs.Get(ctx, w.PVCName, metav1.GetOptions{})
		}
	}
	if e != nil {
		return Observation{}, e
	}
	o := Observation{string(p.UID), p.Spec.VolumeName, string(p.Status.Phase)}
	if p.Labels["olixops.io/workspace-id"] != w.ID || p.Labels["olixops.io/namespace-id"] != w.NamespaceID || p.Spec.StorageClassName == nil || *p.Spec.StorageClassName != w.StorageClass || len(p.OwnerReferences) > 0 || p.DeletionTimestamp != nil || p.Spec.Resources.Requests.Storage().Value() != w.RequestedBytes || (w.PVCUID != "" && w.PVCUID != string(p.UID)) {
		return o, errors.New("PVC identity/configuration changed; operator review required")
	}
	if p.Status.Phase == corev1.ClaimLost {
		return o, errors.New("PVC is Lost")
	}
	return o, nil
}

// Check every Pod and BatchSandbox, including terminating Pods. RWO is not a per-Pod mutex.
func (k *Kubernetes) InUse(ctx context.Context, w domain.Workspace) (bool, error) {
	if e := k.check(w); e != nil {
		return false, e
	}
	pods, e := k.core.Pods(k.namespace).List(ctx, metav1.ListOptions{})
	if e != nil {
		return false, e
	}
	for _, p := range pods.Items {
		for _, v := range p.Spec.Volumes {
			if v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == w.PVCName {
				return true, nil
			}
		}
	}
	batches, e := k.dynamic.Resource(schema.GroupVersionResource{Group: "sandbox.opensandbox.io", Version: "v1alpha1", Resource: "batchsandboxes"}).Namespace(k.namespace).List(ctx, metav1.ListOptions{})
	if e != nil {
		return false, fmt.Errorf("verify BatchSandbox consumers: %w", e)
	}
	for _, b := range batches.Items {
		if referencesClaim(b.Object["spec"], w.PVCName) {
			return true, nil
		}
	}
	return false, nil
}
func referencesClaim(value any, name string) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if key == "claimName" && item == name {
				return true
			}
			if referencesClaim(item, name) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if referencesClaim(item, name) {
				return true
			}
		}
	}
	return false
}
