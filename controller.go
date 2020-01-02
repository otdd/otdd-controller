/*
Copyright 2017 The Kubernetes Authors.

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

package main

import (
	"fmt"
	"time"
	"strings"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	appsinformers "k8s.io/client-go/informers/apps/v1"
	coreinformers "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	appslisters "k8s.io/client-go/listers/apps/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"

	otddv1alpha1 "k8s.io/otdd-controller/pkg/apis/otddcontroller/v1alpha1"
	clientset "k8s.io/otdd-controller/pkg/generated/clientset/versioned"
	otddscheme "k8s.io/otdd-controller/pkg/generated/clientset/versioned/scheme"
	informers "k8s.io/otdd-controller/pkg/generated/informers/externalversions/otddcontroller/v1alpha1"
	listers "k8s.io/otdd-controller/pkg/generated/listers/otddcontroller/v1alpha1"
	pointer "github.com/xorcare/pointer"
)

const controllerAgentName = "otdd-controller"

const (
	// SuccessSynced is used as part of the Event 'reason' when a Recorder is synced
	SuccessSynced = "Synced"
	// ErrResourceExists is used as part of the Event 'reason' when a Recorder fails
	// to sync due to a Deployment of the same name already existing.
	ErrResourceExists = "ErrResourceExists"

	// MessageResourceExists is the message used for Events when a resource
	// fails to sync due to a Deployment already existing
	MessageResourceExists = "Resource %q already exists and is not managed by Recorder"
	// MessageResourceSynced is the message used for an Event fired when a Recorder 
	// is synced successfully
	MessageResourceSynced = "Recorder synced successfully"
)

// Controller is the controller implementation for Recorder resources
type Controller struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface
	// otddclientset is a clientset for our own API group
	otddclientset clientset.Interface

	deploymentsLister appslisters.DeploymentLister
	servicesLister corelisters.ServiceLister
	deploymentsSynced cache.InformerSynced
	recorderLister        listers.RecorderLister
	recorderSynced        cache.InformerSynced

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface
	// recorder is an event recorder for recording Event resources to the
	// Kubernetes API.
	recorder record.EventRecorder
}

// NewController returns a new otdd controller
func NewController(
	kubeclientset kubernetes.Interface,
	otddclientset clientset.Interface,
	deploymentInformer appsinformers.DeploymentInformer,
	serviceInformer coreinformers.ServiceInformer,
	recorderInformer informers.RecorderInformer) *Controller {

	// Create event broadcaster
	// Add otdd-controller types to the default Kubernetes Scheme so Events can be
	// logged for otdd-controller types.
	utilruntime.Must(otddscheme.AddToScheme(scheme.Scheme))
	klog.V(4).Info("Creating event broadcaster")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(klog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	controller := &Controller{
		kubeclientset:     kubeclientset,
		otddclientset:   otddclientset,
		deploymentsLister: deploymentInformer.Lister(),
		servicesLister: serviceInformer.Lister(),
		deploymentsSynced: deploymentInformer.Informer().HasSynced,
		recorderLister:        recorderInformer.Lister(),
		recorderSynced:        recorderInformer.Informer().HasSynced,
		workqueue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Recorders"),
		recorder:          recorder,
	}

	klog.Info("Setting up event handlers")
	// Set up an event handler for when Recorder resources change
	recorderInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.addOrUpdateRecorder,
		//DeleteFunc: controller.deleteRecorder,
		UpdateFunc: func(old, new interface{}) {
			controller.addOrUpdateRecorder(new)
		},
	})

	return controller
}

// Run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *Controller) Run(threadiness int, stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer c.workqueue.ShutDown()

	// Start the informer factories to begin populating the informer caches
	klog.Info("Starting Recorder controller")

	// Wait for the caches to be synced before starting workers
	klog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, c.deploymentsSynced, c.recorderSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.Info("Starting workers")
	// Launch two workers to process Recorder resources
	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	klog.Info("Started workers")
	<-stopCh
	klog.Info("Shutting down workers")

	return nil
}

// runWorker is a long-running function that will continually call the
// processNextWorkItem function in order to read and process a message on the
// workqueue.
func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
}

// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the syncHandler.
func (c *Controller) processNextWorkItem() bool {
	obj, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	// We wrap this block in a func so we can defer c.workqueue.Done.
	err := func(obj interface{}) error {
		// We call Done here so the workqueue knows we have finished
		// processing this item. We also must remember to call Forget if we
		// do not want this work item being re-queued. For example, we do
		// not call Forget if a transient error occurs, instead the item is
		// put back on the workqueue and attempted again after a back-off
		// period.
		defer c.workqueue.Done(obj)
		var key string
		var ok bool
		// We expect strings to come off the workqueue. These are of the
		// form namespace/name. We do this as the delayed nature of the
		// workqueue means the items in the informer cache may actually be
		// more up to date that when the item was initially put onto the
		// workqueue.
		if key, ok = obj.(string); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here else we'd go into a loop of attempting to
			// process a work item that is invalid.
			c.workqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		// Run the syncHandler, passing it the namespace/name string of the
		// Recorder resource to be synced.
		if err := c.syncHandler(key); err != nil {
			// Put the item back on the workqueue to handle any transient errors.
			c.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		c.workqueue.Forget(obj)
		klog.Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

// syncHandler compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the Recorder resource
// with the current status of the resource.
func (c *Controller) syncHandler(key string) error {
	// Convert the namespace/name string into a distinct namespace and name
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	// Get the Recorder resource with this namespace/name
	recorder, err := c.recorderLister.Recorders(namespace).Get(name)
	if err != nil {
		// The Recorder resource may no longer exist, in which case we stop
		// processing.
		if errors.IsNotFound(err) {
			utilruntime.HandleError(fmt.Errorf("recorder '%s' in work queue no longer exists", key))
			return nil
		}

		return err
	}

	targetDeployment := recorder.Spec.TargetDeployment
	if targetDeployment == "" {
		// We choose to absorb the error here as the worker would requeue the
		// resource otherwise. Instead, the next time the resource is updated
		// the resource will be queued again.
		utilruntime.HandleError(fmt.Errorf("%s: target deployment name must be specified", key))
		return nil
	}

	// Get the deployment with the name specified in Recorder.spec
	deployment, err := c.deploymentsLister.Deployments(recorder.Namespace).Get(targetDeployment)
	if errors.IsNotFound(err) {
		//deployment, err = c.kubeclientset.AppsV1().Deployments(recorder.Namespace).Create(newDeployment(recorder))
		utilruntime.HandleError(fmt.Errorf("%s: target deployment dose not exist.", key))
		return nil
	}

	_, redirectorErr := c.deploymentsLister.Deployments(recorder.Namespace).Get(getRedirectorDeploymentName(deployment.ObjectMeta.Name))
	_, recorderErr := c.deploymentsLister.Deployments(recorder.Namespace).Get(getRecorderDeploymentName(deployment.ObjectMeta.Name))
	_, recorderServiceErr := c.servicesLister.Services(recorder.Namespace).Get(getRecorderDeploymentName(deployment.ObjectMeta.Name))
	if !errors.IsNotFound(redirectorErr) && !errors.IsNotFound(recorderErr) && !errors.IsNotFound(recorderServiceErr) {
		klog.Info("recorder was already deployed for deployment: ",deployment.ObjectMeta.Name)
		return nil
	}

	if errors.IsNotFound(redirectorErr){
		klog.Info("creating redirector deployment: ",getRedirectorDeploymentName(deployment.ObjectMeta.Name))
		_, err = c.kubeclientset.AppsV1().Deployments(recorder.Namespace).Create(newRedirectorDeployment(recorder,deployment))
		// If an error occurs during Get/Create, we'll requeue the item so we can
		// attempt processing again later. This could have been caused by a
		// temporary network failure, or any other transient reason.
		if err != nil {
			return err
		}
	}
	if errors.IsNotFound(recorderErr){
		klog.Info("creating recorder deployment: ",getRecorderDeploymentName(deployment.ObjectMeta.Name))
		_, err = c.kubeclientset.AppsV1().Deployments(recorder.Namespace).Create(newRecorderDeployment(recorder,deployment))
		if err != nil {
			return err
		}
	}
	if errors.IsNotFound(recorderServiceErr){
		klog.Info("creating recorder service: ",getRecorderDeploymentName(deployment.ObjectMeta.Name))
		_, err = c.kubeclientset.CoreV1().Services(recorder.Namespace).Create(newRecorderService(recorder,deployment))
		if err != nil {
			return err
		}
	}

	// Finally, we update the status block of the Recorder resource to reflect the
	// current state of the world
	err = c.updateRecorderStatus(recorder, deployment)
	if err != nil {
		return err
	}

	c.recorder.Event(recorder, corev1.EventTypeNormal, SuccessSynced, MessageResourceSynced)
	return nil
}

func (c *Controller) updateRecorderStatus(recorder *otddv1alpha1.Recorder, deployment *appsv1.Deployment) error {
	// NEVER modify objects from the store. It's a read-only, local cache.
	// You can use DeepCopy() to make a deep copy of original object and modify this copy
	// Or create a copy manually for better performance
	recorderCopy := recorder.DeepCopy()
	recorderCopy.Status.Installed = 1
	// If the CustomResourceSubresources feature gate is not enabled,
	// we must use Update instead of UpdateStatus to update the Status block of the Recorder resource.
	// UpdateStatus will not allow changes to the Spec of the resource,
	// which is ideal for ensuring nothing other than resource status has been updated.
	_, err := c.otddclientset.NetworkingV1alpha1().Recorders(recorder.Namespace).Update(recorderCopy)
	return err
}

// addOrUpdateRecorder takes a Recorder resource and converts it into a namespace/name
// string which is then put onto the work queue. This method should *not* be
// passed resources of any type other than Recorder.
func (c *Controller) addOrUpdateRecorder(obj interface{}) {
	klog.Info("in addOrUpdateRecorder: ",obj)
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	}
	c.workqueue.Add(key)
}

func getRedirectorDeploymentName(targetDeployment string) string {
	return targetDeployment + "-otdd-redirector"
}

func getRecorderDeploymentName(targetDeployment string) string {
	return targetDeployment + "-otdd-recorder"
}

// create a redirector deployment by: cloning a new target deployment and retaining all labels so that it can act like another normal pod receiving requests.
// another specific otdd label is added so that the istio envoy otdd.redirector filter can be added only to this pod. 
func newRedirectorDeployment(recorder *otddv1alpha1.Recorder,deployment *appsv1.Deployment ) *appsv1.Deployment {

	redirectorDeployment := &appsv1.Deployment{
                ObjectMeta: metav1.ObjectMeta{
                        Name:      getRedirectorDeploymentName(deployment.ObjectMeta.Name),
                        Namespace: recorder.Namespace,
                        Labels: deployment.ObjectMeta.Labels,
                        OwnerReferences: []metav1.OwnerReference{
                                *metav1.NewControllerRef(recorder, otddv1alpha1.SchemeGroupVersion.WithKind("Recorder")),
                        },
                },
                Spec: appsv1.DeploymentSpec{
                        Replicas: pointer.Int32(1),
                        Selector: deployment.Spec.Selector,
                        Template: deployment.Spec.Template,
                },
        }

	if(redirectorDeployment.ObjectMeta.Labels==nil){
		redirectorDeployment.ObjectMeta.Labels = make(map[string]string)
	}
        redirectorDeployment.ObjectMeta.Labels["otdd"] = redirectorDeployment.ObjectMeta.Name
        redirectorDeployment.Spec.Selector.MatchLabels["otdd"] = redirectorDeployment.ObjectMeta.Name
        redirectorDeployment.Spec.Template.Labels["otdd"] = redirectorDeployment.ObjectMeta.Name
        //redirectorDeployment.Spec.Replicas = pointer.Int32(1)
	klog.Info("the content of newRedirectorDeployment: ",redirectorDeployment)
	return redirectorDeployment
}

// create a recorder deployment by: cloning a new target deployment and deleting all labels so that it won't receive requests.
// another specific otdd label is added so that the istio envoy otdd.recorder filter can be added only to this pod. 
func newRecorderDeployment(recorder *otddv1alpha1.Recorder,deployment *appsv1.Deployment ) *appsv1.Deployment {

	recorderDeployment := &appsv1.Deployment{
                ObjectMeta: metav1.ObjectMeta{
                        Name:      getRecorderDeploymentName(deployment.ObjectMeta.Name),
                        Namespace: recorder.Namespace,
                        OwnerReferences: []metav1.OwnerReference{
                                *metav1.NewControllerRef(recorder, otddv1alpha1.SchemeGroupVersion.WithKind("Recorder")),
                        },
                },
                Spec: appsv1.DeploymentSpec{
                        Replicas: pointer.Int32(1),
                        Selector: deployment.Spec.Selector,
                        Template: deployment.Spec.Template,
                },
	}

        recorderDeployment.ObjectMeta.Labels = map[string]string{
		"otdd":        getRecorderDeploymentName(deployment.ObjectMeta.Name),
	}
        recorderDeployment.Spec.Selector.MatchLabels = map[string]string{
		"otdd":        getRecorderDeploymentName(deployment.ObjectMeta.Name),
	}
        recorderDeployment.Spec.Template.ObjectMeta.Labels = map[string]string{
		"otdd":        getRecorderDeploymentName(deployment.ObjectMeta.Name),
	}
	return recorderDeployment

}

// another service for the recorder deployment must be deployed or the istio proxy won't start.
func newRecorderService(recorder *otddv1alpha1.Recorder,deployment *appsv1.Deployment ) *corev1.Service {

	recorderService := &corev1.Service {
                ObjectMeta: metav1.ObjectMeta{
                        Name:      getRecorderDeploymentName(deployment.ObjectMeta.Name),
                        Namespace: recorder.Namespace,
                        OwnerReferences: []metav1.OwnerReference{
                                *metav1.NewControllerRef(recorder, otddv1alpha1.SchemeGroupVersion.WithKind("Recorder")),
                        },
                },
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
                			Name:   strings.ToLower(recorder.Spec.Protocol),
					Port:       recorder.Spec.Port,
        			},
			},
		},
	}

        recorderService.ObjectMeta.Labels = map[string]string{
		"otdd":        getRecorderDeploymentName(deployment.ObjectMeta.Name),
	}
        recorderService.Spec.Selector = map[string]string{
		"otdd":        getRecorderDeploymentName(deployment.ObjectMeta.Name),
	}
	return recorderService

}
