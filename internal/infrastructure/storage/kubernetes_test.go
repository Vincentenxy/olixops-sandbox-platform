package storage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/client-go/dynamic"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"olixops-sandbox-platform/internal/domain"
)

func TestPVCIdentityAndConsumerChecks(t *testing.T) {
	var requests, creates int
	mode := "original"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/namespaces/sandbox/persistentvolumeclaims/claim":
			if mode == "missing" {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`))
				return
			}
			uid := "original-uid"
			if mode == "replaced" {
				uid = "another-uid"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "v1", "kind": "PersistentVolumeClaim", "metadata": map[string]any{"name": "claim", "uid": uid, "labels": map[string]string{"olixops.io/workspace-id": "workspace", "olixops.io/namespace-id": "personal"}}, "spec": map[string]any{"storageClassName": "local-path", "resources": map[string]any{"requests": map[string]string{"storage": "10Gi"}}}, "status": map[string]any{"phase": "Bound"}})
		case "/api/v1/namespaces/sandbox/persistentvolumeclaims":
			creates++
			w.WriteHeader(500)
		case "/api/v1/namespaces/sandbox/pods":
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"PodList","items":[]}`))
		case "/apis/sandbox.opensandbox.io/v1alpha1/namespaces/sandbox/batchsandboxes":
			_, _ = w.Write([]byte(`{"apiVersion":"sandbox.opensandbox.io/v1alpha1","kind":"BatchSandboxList","items":[{"apiVersion":"sandbox.opensandbox.io/v1alpha1","kind":"BatchSandbox","metadata":{"name":"still-owned"},"spec":{"template":{"spec":{"volumes":[{"persistentVolumeClaim":{"claimName":"claim"}}]}}}}]}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer up.Close()
	cfg := &rest.Config{Host: up.URL}
	core, e := v1.NewForConfig(cfg)
	if e != nil {
		t.Fatal(e)
	}
	d, e := dynamic.NewForConfig(cfg)
	if e != nil {
		t.Fatal(e)
	}
	k := &Kubernetes{core, d, "test", "sandbox", "local-path"}
	workspace := domain.Workspace{ID: "workspace", NamespaceID: "personal", PVCName: "claim", PVCUID: "original-uid", State: "Available", RequestedBytes: 10 << 30, ClusterID: "test", KubeNamespace: "sandbox", StorageClass: "local-path"}
	o, e := k.Ensure(context.Background(), workspace)
	if e != nil || o.UID != "original-uid" {
		t.Fatal(o, e)
	}
	mode = "replaced"
	if _, e = k.Ensure(context.Background(), workspace); e == nil {
		t.Fatal("same-name PVC replacement accepted")
	}
	mode = "missing"
	if _, e = k.Ensure(context.Background(), workspace); e == nil {
		t.Fatal("missing persistent PVC not rejected")
	}
	if creates != 0 {
		t.Fatal("missing persistent PVC auto-recreated")
	}
	used, e := k.InUse(context.Background(), workspace)
	if e != nil || !used {
		t.Fatalf("CR consumer missed after Pod disappears: %v %v", used, e)
	}
	workspace.ClusterID = "wrong-cluster"
	before := requests
	if _, e = k.Ensure(context.Background(), workspace); e == nil || requests != before {
		t.Fatal("cross-cluster workspace reached API")
	}
}
