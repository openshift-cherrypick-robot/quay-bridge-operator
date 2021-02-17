package util

import (
	"context"
	"errors"
	"fmt"
	"math"
	"text/template"
	"time"

	apis "github.com/quay/operator-utils/pkg/util/apis"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ReconcilerBase is a base struct from which all reconcilers can be derived. By doing so your finalizers will also inherir a set of utility functions
// To inherit from reconciler just build your finalizer this way:
// type MyReconciler struct {
//   util.ReconcilerBase
//   ... other optional fields ...
// }
type ReconcilerBase struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client     client.Client
	scheme     *runtime.Scheme
	restConfig *rest.Config
	recorder   record.EventRecorder
}

// NewReconcilerBase is a contructionr fucntion to create a new ReconcilerBase.
// To use ReconsicerBase as the base for you reconciler, replace the standart consturctor generated by the oiperator sdk with this:
// func newReconciler(mgr manager.Manager) reconcile.Reconciler {
// 	return &MyReconciler{
// 		ReconcilerBase: util.NewReconcilerBase(mgr.GetClient(), mgr.GetScheme(),mgr.GetConfig()),
// 	}
// }
func NewReconcilerBase(client client.Client, scheme *runtime.Scheme, restConfig *rest.Config, recorder record.EventRecorder) ReconcilerBase {
	return ReconcilerBase{
		client:     client,
		scheme:     scheme,
		restConfig: restConfig,
		recorder:   recorder,
	}
}

func (r *ReconcilerBase) IsValid(obj metav1.Object) (bool, error) {
	return true, nil
}

func (r *ReconcilerBase) IsInitialized(obj metav1.Object) bool {
	return true
}

// Reconcile is a stub function to have ReconsicerBase match the Reconciler interface. You must redefine this function
func (r *ReconcilerBase) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	return reconcile.Result{}, nil
}

// GetClient returns the underlying client
func (r *ReconcilerBase) GetClient() client.Client {
	return r.client
}

// GetRecorder returns the underlying recorder
func (r *ReconcilerBase) GetRecorder() record.EventRecorder {
	return r.recorder
}

// GetScheme returns the scheme
func (r *ReconcilerBase) GetScheme() *runtime.Scheme {
	return r.scheme
}

// GetDiscoveryClient returns a disocvery client for the current reconciler
func (r *ReconcilerBase) GetDiscoveryClient() (*discovery.DiscoveryClient, error) {
	return discovery.NewDiscoveryClientForConfig(r.restConfig)
}

// GetDynamicClientOnAPIResource returns a dynamic client on an APIResource. This client can be further namespaced.
func (r *ReconcilerBase) GetDynamicClientOnAPIResource(resource metav1.APIResource) (dynamic.NamespaceableResourceInterface, error) {
	return r.getDynamicClientOnGVR(schema.GroupVersionResource{
		Group:    resource.Group,
		Version:  resource.Version,
		Resource: resource.Kind,
	})
}

func (r *ReconcilerBase) getDynamicClientOnGVR(gkv schema.GroupVersionResource) (dynamic.NamespaceableResourceInterface, error) {
	intf, err := dynamic.NewForConfig(r.restConfig)
	if err != nil {
		log.Error(err, "unable to get dynamic client")
		return nil, err
	}
	res := intf.Resource(gkv)
	return res, nil
}

// GetDynamicClientOnUnstructured returns a dynamic client on an Unstructured type. This client can be further namespaced.
func (r *ReconcilerBase) GetDynamicClientOnUnstructured(obj unstructured.Unstructured) (dynamic.NamespaceableResourceInterface, error) {
	gvk := obj.GetObjectKind().GroupVersionKind()
	return r.getDynamicClientOnGVR(schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: gvk.Kind,
	})
}

// CreateOrUpdateResource creates a resource if it doesn't exist, and updates (overwrites it), if it exist
// if owner is not nil, the owner field os set
// if namespace is not "", the namespace field of the object is overwritten with the passed value
func (r *ReconcilerBase) CreateOrUpdateResource(owner metav1.Object, namespace string, obj metav1.Object) error {
	runtimeObj, ok := (obj).(runtime.Object)
	if !ok {
		return fmt.Errorf("is not a %T a runtime.Object", obj)
	}

	if owner != nil {
		_ = controllerutil.SetControllerReference(owner, obj, r.GetScheme())
	}
	if namespace != "" {
		obj.SetNamespace(namespace)
	}

	obj2 := unstructured.Unstructured{}
	obj2.SetKind(runtimeObj.GetObjectKind().GroupVersionKind().Kind)
	if runtimeObj.GetObjectKind().GroupVersionKind().Group != "" {
		obj2.SetAPIVersion(runtimeObj.GetObjectKind().GroupVersionKind().Group + "/" + runtimeObj.GetObjectKind().GroupVersionKind().Version)
	} else {
		obj2.SetAPIVersion(runtimeObj.GetObjectKind().GroupVersionKind().Version)
	}

	err := r.GetClient().Get(context.TODO(), types.NamespacedName{
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
	}, &obj2)

	if apierrors.IsNotFound(err) {
		err = r.GetClient().Create(context.TODO(), runtimeObj)
		if err != nil {
			log.Error(err, "unable to create object", "object", runtimeObj)
		}
		return err
	}
	if err == nil {
		obj.SetResourceVersion(obj2.GetResourceVersion())
		err = r.GetClient().Update(context.TODO(), runtimeObj)
		if err != nil {
			log.Error(err, "unable to update object", "object", runtimeObj)
		}
		return err

	}
	log.Error(err, "unable to lookup object", "object", runtimeObj)
	return err
}

// CreateOrUpdateResources operates as CreateOrUpdate, but on an array of resources
func (r *ReconcilerBase) CreateOrUpdateResources(owner metav1.Object, namespace string, objs []metav1.Object) error {
	for _, obj := range objs {
		err := r.CreateOrUpdateResource(owner, namespace, obj)
		if err != nil {
			return err
		}
	}
	return nil
}

// DeleteResource deletes an existing resource. It doesn't fail if the resource does not exist
func (r *ReconcilerBase) DeleteResource(obj metav1.Object) error {
	runtimeObj, ok := (obj).(runtime.Object)
	if !ok {
		return fmt.Errorf("is not a %T a runtime.Object", obj)
	}

	err := r.GetClient().Delete(context.TODO(), runtimeObj, nil)
	if err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "unable to delete object ", "object", runtimeObj)
		return err
	}
	return nil
}

// DeleteResources operates like DeleteResources, but on an arrays of resources
func (r *ReconcilerBase) DeleteResources(objs []metav1.Object) error {
	for _, obj := range objs {
		err := r.DeleteResource(obj)
		if err != nil {
			return err
		}
	}
	return nil
}

// CreateResourceIfNotExists create a resource if it doesn't already exists. If the resource exists it is left untouched and the functin does not fails
// if owner is not nil, the owner field os set
// if namespace is not "", the namespace field of the object is overwritten with the passed value
func (r *ReconcilerBase) CreateResourceIfNotExists(owner metav1.Object, namespace string, obj metav1.Object) error {
	runtimeObj, ok := (obj).(runtime.Object)
	if !ok {
		return fmt.Errorf("is not a %T a runtime.Object", obj)
	}

	if owner != nil {
		_ = controllerutil.SetControllerReference(owner, obj, r.GetScheme())
	}
	if namespace != "" {
		obj.SetNamespace(namespace)
	}

	err := r.GetClient().Create(context.TODO(), runtimeObj)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		log.Error(err, "unable to create object ", "object", runtimeObj)
		return err
	}
	return nil
}

// CreateResourcesIfNotExist operates as CreateResourceIfNotExists, but on an array of resources
func (r *ReconcilerBase) CreateResourcesIfNotExist(owner metav1.Object, namespace string, objs []metav1.Object) error {
	for _, obj := range objs {
		err := r.CreateResourceIfNotExists(owner, namespace, obj)
		if err != nil {
			return err
		}
	}
	return nil
}

// CreateOrUpdateTemplatedResources processes an initialized template expecting an array of objects as a result and the processes them with the CreateOrUpdate function
func (r *ReconcilerBase) CreateOrUpdateTemplatedResources(owner metav1.Object, namespace string, data interface{}, template *template.Template) error {
	objs, err := ProcessTemplateArray(data, template)
	if err != nil {
		log.Error(err, "error creating manifest from template")
		return err
	}
	for _, obj := range *objs {
		err = r.CreateOrUpdateResource(owner, namespace, &obj)
		if err != nil {
			return err
		}
	}
	return nil
}

// CreateIfNotExistTemplatedResources processes an initialized template expecting an array of objects as a result and then processes them with the CreateResourceIfNotExists function
func (r *ReconcilerBase) CreateIfNotExistTemplatedResources(owner metav1.Object, namespace string, data interface{}, template *template.Template) error {
	objs, err := ProcessTemplateArray(data, template)
	if err != nil {
		log.Error(err, "error creating manifest from template")
		return err
	}
	for _, obj := range *objs {
		err = r.CreateResourceIfNotExists(owner, namespace, &obj)
		if err != nil {
			return err
		}
	}
	return nil
}

// DeleteTemplatedResources processes an initialized template expecting an array of objects as a result and then processes them with the Delete function
func (r *ReconcilerBase) DeleteTemplatedResources(data interface{}, template *template.Template) error {
	objs, err := ProcessTemplateArray(data, template)
	if err != nil {
		log.Error(err, "error creating manifest from template")
		return err
	}
	for _, obj := range *objs {
		err = r.DeleteResource(&obj)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *ReconcilerBase) ManageError(obj metav1.Object, issue error) (reconcile.Result, error) {
	runtimeObj, ok := (obj).(runtime.Object)
	if !ok {
		log.Error(errors.New("not a runtime.Object"), "passed object was not a runtime.Object", "object", obj)
		return reconcile.Result{}, nil
	}
	var retryInterval time.Duration
	r.GetRecorder().Event(runtimeObj, "Warning", "ProcessingError", issue.Error())
	if reconcileStatusAware, updateStatus := (obj).(apis.ReconcileStatusAware); updateStatus {
		lastUpdate := reconcileStatusAware.GetReconcileStatus().LastUpdate.Time
		lastStatus := reconcileStatusAware.GetReconcileStatus().Status
		status := apis.ReconcileStatus{
			LastUpdate: metav1.Now(),
			Reason:     issue.Error(),
			Status:     "Failure",
		}
		reconcileStatusAware.SetReconcileStatus(status)
		err := r.GetClient().Status().Update(context.Background(), runtimeObj)
		if err != nil {
			log.Error(err, "unable to update status")
			return reconcile.Result{
				RequeueAfter: time.Second,
				Requeue:      true,
			}, nil
		}
		if lastUpdate.IsZero() || lastStatus == "Success" {
			retryInterval = time.Second
		} else {
			retryInterval = status.LastUpdate.Sub(lastUpdate).Round(time.Second)
		}
	} else {
		log.Info("object is not RecocileStatusAware, not setting status")
		retryInterval = time.Second
	}
	return reconcile.Result{
		RequeueAfter: time.Duration(math.Min(float64(retryInterval.Nanoseconds()*2), float64(time.Hour.Nanoseconds()*6))),
		Requeue:      true,
	}, nil
}

func (r *ReconcilerBase) ManageSuccess(obj metav1.Object) (reconcile.Result, error) {
	runtimeObj, ok := (obj).(runtime.Object)
	if !ok {
		log.Error(errors.New("not a runtime.Object"), "passed object was not a runtime.Object", "object", obj)
		return reconcile.Result{}, nil
	}
	if reconcileStatusAware, updateStatus := (obj).(apis.ReconcileStatusAware); updateStatus {
		status := apis.ReconcileStatus{
			LastUpdate: metav1.Now(),
			Reason:     "",
			Status:     "Success",
		}
		reconcileStatusAware.SetReconcileStatus(status)
		err := r.GetClient().Status().Update(context.Background(), runtimeObj)
		if err != nil {
			log.Error(err, "unable to update status")
			return reconcile.Result{
				RequeueAfter: time.Second,
				Requeue:      true,
			}, nil
		}
	} else {
		log.Info("object is not RecocileStatusAware, not setting status")
	}
	return reconcile.Result{}, nil
}
