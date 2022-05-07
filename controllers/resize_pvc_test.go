package controllers

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/cybozu-go/moco/pkg/constants"
	"github.com/google/go-cmp/cmp"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	corev1ac "k8s.io/client-go/applyconfigurations/core/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
)

func TestReconcilePVC(t *testing.T) {
	tests := []struct {
		name        string
		cluster     *mocov1beta2.MySQLCluster
		setupClient func(*testing.T) client.Client
		wantSize    resource.Quantity
	}{
		{
			name:    "resize succeeded",
			cluster: newMySQLClusterWithVolumeSize(resource.MustParse("2Gi")),
			setupClient: func(t *testing.T) client.Client {
				cluster := newMySQLClusterWithVolumeSize(resource.MustParse("2Gi"))
				sts := newStatefulSetWithVolumeSize(resource.MustParse("1Gi"))
				return setupMockClient(t, cluster, sts)
			},
			wantSize: resource.MustParse("2Gi"),
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			r := &MySQLClusterReconciler{Client: tt.setupClient(t)}

			err := r.reconcilePVC(ctx, ctrl.Request{NamespacedName: types.NamespacedName{
				Namespace: tt.cluster.Namespace,
				Name:      tt.cluster.Name,
			}}, tt.cluster)
			if err != nil {
				t.Fatalf("reconcilePVC() error = %v", err)
			}

			var pvc corev1.PersistentVolumeClaim
			if err := r.Get(ctx, types.NamespacedName{Name: "mysql-data-moco-mysql-cluster-0", Namespace: tt.cluster.Namespace}, &pvc); err != nil {
				t.Fatalf("failed to get PVC: %v", err)
			}
			if !pvc.Spec.Resources.Requests.Storage().Equal(tt.wantSize) {
				t.Errorf("unexpected PVC size: got: %s, want: %s", pvc.Spec.Resources.Requests.Storage().String(), tt.wantSize.String())
			}

			var cluster mocov1beta2.MySQLCluster
			if err := r.Get(ctx, types.NamespacedName{Name: tt.cluster.Name, Namespace: tt.cluster.Namespace}, &cluster); err != nil {
				t.Fatalf("failed to get MySQLCluster: %v", err)
			}
			if len(cluster.Status.Conditions) == 0 {
				t.Fatal("MySQLCluster should have conditions")
			}

			found := false

			for _, cond := range cluster.Status.Conditions {
				if cond.Type == mocov1beta2.ConditionVolumeResized &&
					cond.Status == corev1.ConditionTrue {
					found = true
					break
				}
			}

			if !found {
				t.Error("MySQLCluster should have VolumeResized condition")
			}
		})
	}
}

func setupMockClient(t *testing.T, cluster *mocov1beta2.MySQLCluster, sts *appsv1.StatefulSet) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}
	if err := mocov1beta2.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	var pvcs []client.Object

	for _, pvc := range sts.Spec.VolumeClaimTemplates {
		pvc := pvc
		for i := int32(0); i < *sts.Spec.Replicas; i++ {
			pvc.Name = fmt.Sprintf("%s-%s-%d", pvc.Name, cluster.PrefixedName(), i)
			pvc.Namespace = cluster.Namespace
			pvc.Labels = sts.Spec.Selector.MatchLabels
			pvc.Spec.StorageClassName = pointer.String("default")
			pvcs = append(pvcs, &pvc)
		}
	}

	storageClass := &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default",
		},
		Provisioner:          "kubernetes.io/no-provisioner",
		AllowVolumeExpansion: pointer.Bool(true),
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster, sts, storageClass).
		WithObjects(pvcs...).
		Build()

	return client
}

func TestNeedResizePVC(t *testing.T) {
	tests := []struct {
		name             string
		cluster          *mocov1beta2.MySQLCluster
		sts              *appsv1.StatefulSet
		wantResizeTarget map[string]corev1.PersistentVolumeClaim
		wantResize       bool
		wantError        error
	}{
		{
			name:       "no resizing",
			cluster:    newMySQLClusterWithVolumeSize(resource.MustParse("1Gi")),
			sts:        newStatefulSetWithVolumeSize(resource.MustParse("1Gi")),
			wantResize: false,
		},
		{
			name:    "need resizing",
			cluster: newMySQLClusterWithVolumeSize(resource.MustParse("2Gi")),
			sts:     newStatefulSetWithVolumeSize(resource.MustParse("1Gi")),
			wantResizeTarget: func() map[string]corev1.PersistentVolumeClaim {
				sts := newStatefulSetWithVolumeSize(resource.MustParse("1Gi"))
				pvc := sts.Spec.VolumeClaimTemplates[0]
				m := make(map[string]corev1.PersistentVolumeClaim)
				m[pvc.Name] = pvc
				return m
			}(),
			wantResize: true,
		},
		{
			name:       "reduce volume size error",
			cluster:    newMySQLClusterWithVolumeSize(resource.MustParse("1Gi")),
			sts:        newStatefulSetWithVolumeSize(resource.MustParse("2Gi")),
			wantResize: false,
			wantError:  ErrReduceVolumeSize,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			r := &MySQLClusterReconciler{}
			resizeTarget, resize, err := r.needResizePVC(tt.cluster, tt.sts)
			if err != nil {
				if !errors.Is(err, tt.wantError) {
					t.Fatalf("want error %v, got %v", tt.wantError, err)
				}
			}

			if tt.wantResize != resize {
				t.Fatalf("want resize %v, got %v", tt.wantResize, resize)
			}

			for key, value := range tt.wantResizeTarget {
				if diff := cmp.Diff(value, resizeTarget[key]); len(diff) != 0 {
					t.Fatalf("want resize target %v, got %v", value, resizeTarget[key])
				}
			}
		})
	}
}

func newMySQLClusterWithVolumeSize(size resource.Quantity) *mocov1beta2.MySQLCluster {
	return &mocov1beta2.MySQLCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mysql-cluster",
			Namespace: "default",
		},
		Spec: mocov1beta2.MySQLClusterSpec{
			VolumeClaimTemplates: []mocov1beta2.PersistentVolumeClaim{
				{
					ObjectMeta: mocov1beta2.ObjectMeta{Name: "mysql-data"},
					Spec: mocov1beta2.PersistentVolumeClaimSpecApplyConfiguration(*corev1ac.PersistentVolumeClaimSpec().
						WithStorageClassName("default").WithResources(corev1ac.ResourceRequirements().
						WithRequests(corev1.ResourceList{corev1.ResourceStorage: size}),
					)),
				},
			},
		},
	}
}

func newStatefulSetWithVolumeSize(size resource.Quantity) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "moco-mysql-cluster",
			Namespace: "default",
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: pointer.Int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					constants.LabelAppName:      constants.AppNameMySQL,
					constants.LabelAppInstance:  "mysql-cluster",
					constants.LabelAppCreatedBy: constants.AppCreator,
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "mysql-data",
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						StorageClassName: pointer.String("default"),
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{corev1.ResourceStorage: size},
						},
					},
				},
			},
		},
	}
}
