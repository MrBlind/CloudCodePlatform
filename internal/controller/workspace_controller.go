/*
Copyright 2024.

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

package controller

import (
	"context"
	"fmt"
	v12 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"time"

	ccpv1 "cloud-ide/api/v1"
)

// WorkSpaceReconciler reconciles a WorkSpace object
type WorkSpaceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

var Mode string

const (
	ModeRelease = "release"
	modDev      = "dev"
)

//+kubebuilder:rbac:groups=ccp.blind.ccp.operator,resources=workspaces,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ccp.blind.ccp.operator,resources=workspaces/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ccp.blind.ccp.operator,resources=workspaces/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the WorkSpace object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.3/pkg/reconcile
func (r *WorkSpaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	lgr := log.FromContext(ctx)

	// TODO(user): your logic here
	wp := ccpv1.WorkSpace{}
	err := r.Client.Get(context.Background(), req.NamespacedName, &wp)
	if err != nil {
		if errors.IsNotFound(err) {
			if e1 := r.deletePod(req.NamespacedName); e1 != nil {
				lgr.Error(e1, "[Delete Workspace] delete pod error:%v")
				return ctrl.Result{Requeue: true}, e1
			}

			if e2 := r.deletePVC(req.NamespacedName); e2 != nil {
				lgr.Error(e2, "[Delete Workdspace] delete pvc error:%v")
				return ctrl.Result{Requeue: true}, e2
			}

			return ctrl.Result{}, nil
		}

		lgr.Error(err, "get workspace error:%v")
		return ctrl.Result{Requeue: true}, err
	}

	switch wp.Spec.Operation {
	case ccpv1.WorkSpaceStart:
		lgr.Info("1111111111111111111", "namespaced name:", req.NamespacedName)
		err = r.createPVC(&wp, req.NamespacedName)
		if err != nil {
			lgr.Error(err, "[Start Workspace] create pvc error")
			return ctrl.Result{Requeue: true}, err
		}
		lgr.Info("create pvc success")

		err = r.createPod(&wp, req.NamespacedName)
		if err != nil {
			lgr.Error(err, "[Start Workspace] create pod error")
			return ctrl.Result{Requeue: true}, err
		}

		r.updateStatus(&wp, ccpv1.WorkspacePhaseRunning)
	case ccpv1.WorkSpaceStop:
		err = r.deletePod(req.NamespacedName)
		if err != nil {
			lgr.Error(err, "[Stop Workspace] delete pod error:")
			return ctrl.Result{Requeue: true}, err
		}

		r.updateStatus(&wp, ccpv1.WorkspacePhaseRunning)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *WorkSpaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ccpv1.WorkSpace{}).
		Owns(&v12.Pod{}, builder.WithPredicates(predicatePod)).
		Owns(&v12.PersistentVolumeClaim{}, builder.WithPredicates(predicatePVC)).
		Complete(r)
}

func (r *WorkSpaceReconciler) deletePod(key client.ObjectKey) error {
	//TODO
	exist, err := r.checkPodExist(key)
	if err != nil {
		return err
	}

	if !exist {
		return nil
	}

	pod := &v12.Pod{}
	pod.Name = key.Name
	pod.Namespace = key.Namespace

	ctx, cancelFunc := context.WithTimeout(context.Background(), time.Second*35)
	defer cancelFunc()

	err = r.Client.Delete(ctx, pod)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		klog.Errorf("delete pod error:%v", err)
		return err
	}
	return nil
}

func (r *WorkSpaceReconciler) checkPodExist(key client.ObjectKey) (bool, error) {
	pod := &v12.Pod{}
	err := r.Client.Get(context.Background(), key, pod)
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}

		klog.Errorf("get pod error:%v", err)
		return false, err
	}

	return true, nil
}

func (r *WorkSpaceReconciler) deletePVC(key client.ObjectKey) error {
	exist, err := r.checkPVCExist(key)
	if err != nil {
		return err
	}

	if !exist {
		return nil
	}

	pvc := &v12.PersistentVolumeClaim{}
	pvc.Name = key.Name
	pvc.Namespace = key.Namespace
	ctx, cancelFunc := context.WithTimeout(context.Background(), time.Second*30)
	defer cancelFunc()
	err = r.Client.Delete(ctx, pvc)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}

		klog.Errorf("delete pvc error:%v", err)
		return err
	}

	return nil
}

func (r *WorkSpaceReconciler) checkPVCExist(key client.ObjectKey) (bool, error) {
	pvc := &v12.PersistentVolumeClaim{}
	err := r.Client.Get(context.Background(), key, pvc)
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}

		klog.Errorf("get pvc error:%v", err)
		return false, err
	}

	return true, nil
}

func (r *WorkSpaceReconciler) createPVC(space *ccpv1.WorkSpace, key client.ObjectKey) error {
	exist, err := r.checkPVCExist(key)
	fmt.Println(err)
	if err != nil {
		return err
	}

	if exist {
		return nil
	}

	pvc, err := r.constructPVC(space)
	fmt.Println("pvc info:", pvc)
	if err != nil {
		klog.Errorf("construct pvc error:%v", err)
		return err
	}
	ctx, cancelFunc := context.WithTimeout(context.Background(), time.Second*30)
	defer cancelFunc()
	err = r.Client.Create(ctx, pvc)
	if err != nil {
		if errors.IsAlreadyExists(err) {
			return nil
		}

		return err
	}

	return nil

}

func (r *WorkSpaceReconciler) constructPVC(space *ccpv1.WorkSpace) (*v12.PersistentVolumeClaim, error) {
	quantity, err := resource.ParseQuantity(space.Spec.Storage)
	if err != nil {
		return nil, err
	}

	pvc := &v12.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolumeClaim",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      space.Name,
			Namespace: space.Namespace,
		},
		Spec: v12.PersistentVolumeClaimSpec{
			AccessModes: []v12.PersistentVolumeAccessMode{v12.ReadWriteMany},
			Resources: v12.ResourceRequirements{
				Limits:   v12.ResourceList{v12.ResourceStorage: quantity},
				Requests: v12.ResourceList{v12.ResourceStorage: quantity},
			},
			VolumeName: "first-pv",
		},
	}

	//这是在设置了storagename的基础上
	storageName := "nfs-csi"
	pvc.Spec.StorageClassName = &storageName
	fmt.Println("pvc 111111", pvc)

	return pvc, nil

}

func (r *WorkSpaceReconciler) createPod(space *ccpv1.WorkSpace, key client.ObjectKey) error {
	exist, err := r.checkPodExist(key)
	if err != nil {
		return err
	}

	if exist {
		return nil
	}

	pod := r.constructPod(space)
	fmt.Println("pod info:", pod)
	fmt.Println("pod info sid:", pod.ObjectMeta.Annotations["sid"])
	fmt.Println("pod info uid:", pod.ObjectMeta.Annotations["uid"])
	klog.Info("pod:", pod)

	ctx, cancelFunc := context.WithTimeout(context.Background(), time.Second*30)
	defer cancelFunc()
	err = r.Client.Create(ctx, pod)
	if err != nil {
		if errors.IsAlreadyExists(err) {
			return nil
		}

		return err
	}

	return nil
}

func (r *WorkSpaceReconciler) constructPod(space *ccpv1.WorkSpace) *v12.Pod {
	volumeName := "volume-workspace"
	pod := &v12.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      space.Name,
			Namespace: space.Namespace,
			Annotations: map[string]string{
				"sid": space.Spec.SID,
				"uid": space.Spec.UID,
			},
			Labels: map[string]string{
				"app": "ccp",
			},
		},
		Spec: v12.PodSpec{
			Volumes: []v12.Volume{
				{
					Name: volumeName,
					VolumeSource: v12.VolumeSource{
						PersistentVolumeClaim: &v12.PersistentVolumeClaimVolumeSource{
							ClaimName: space.Name,
							ReadOnly:  false,
						},
					},
				},
			},
			Containers: []v12.Container{
				{
					Name:            space.Name,
					Image:           space.Spec.Image,
					ImagePullPolicy: v12.PullIfNotPresent,
					Ports: []v12.ContainerPort{
						{
							ContainerPort: space.Spec.Port,
						},
					},
					VolumeMounts: []v12.VolumeMount{
						{
							Name:      volumeName,
							ReadOnly:  false,
							MountPath: space.Spec.MountPath,
						},
					},
				},
			},
		},
	}
	if Mode == ModeRelease {
		pod.Spec.Containers[0].Resources = v12.ResourceRequirements{
			Requests: map[v12.ResourceName]resource.Quantity{
				v12.ResourceCPU:    resource.MustParse("2"),
				v12.ResourceMemory: resource.MustParse("1Gi"),
			},
			Limits: map[v12.ResourceName]resource.Quantity{
				v12.ResourceCPU:    resource.MustParse(space.Spec.Cpu),
				v12.ResourceMemory: resource.MustParse(space.Spec.Memory),
			},
		}
	}
	return pod
}

func (r *WorkSpaceReconciler) updateStatus(wp *ccpv1.WorkSpace, phase ccpv1.WorkSpacePhase) {
	wp.Status.Phase = phase
	err := r.Client.Status().Update(context.Background(), wp)
	if err != nil {
		klog.Errorf("update status error:%v", err)
	}
}
