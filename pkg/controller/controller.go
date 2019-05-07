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

package controller

import (
	"fmt"
	pkglabels "k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/listers/core/v1"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/golang/glog"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeinformers "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"

	podservicev1alpha1 "pod-service-crd/pkg/apis/podservicecrd/v1alpha1"
	clientset "pod-service-crd/pkg/client/clientset/versioned"
	podservicescheme "pod-service-crd/pkg/client/clientset/versioned/scheme"
	informers "pod-service-crd/pkg/client/informers/externalversions"
	listers "pod-service-crd/pkg/client/listers/podservicecrd/v1alpha1"
)

const controllerAgentName = "pod-service-crd"

const (
	// SuccessSynced is used as part of the Event 'reason' when a PodService is synced
	SuccessSynced = "Synced"
	// ErrResourceExists is used as part of the Event 'reason' when a PodService fails
	// to sync due to a Pod of the same name already existing.
	ErrResourceExists = "ErrResourceExists"

	// MessageResourceExists is the message used for Events when a resource
	// fails to sync due to a Pod already existing
	MessageResourceExists = "Resource %q already exists and is not managed by PodService"
	// MessageResourceSynced is the message used for an Event fired when a PodService
	// is synced successfully
	MessageResourceSynced = "PodService synced successfully"
)

// Controller is the controller implementation for PodService resources
type Controller struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset kubernetes.Interface
	// sampleclientset is a clientset for our own API group
	podserviceclientset clientset.Interface

	podsLister v1.PodLister
	podsSynced cache.InformerSynced
	podservicesLister        listers.PodServiceLister
	podservicesSynced        cache.InformerSynced

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

// NewController returns a new sample controller
func NewController(
	kubeclientset kubernetes.Interface,
	podserviceclientset clientset.Interface,
	kubeInformerFactory kubeinformers.SharedInformerFactory,
	podserviceInformerFactory informers.SharedInformerFactory) *Controller {

	// obtain references to shared index informers for the Pod and PodService
	// types.
	podInformer := kubeInformerFactory.Core().V1().Pods()
	podserviceInformer := podserviceInformerFactory.Podservicecrd().V1alpha1().PodServices()

	// Create event broadcaster
	// Add sample-controller types to the default Kubernetes Scheme so Events can be
	// logged for sample-controller types.
	podservicescheme.AddToScheme(scheme.Scheme)
	glog.V(4).Info("Creating event broadcaster")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(glog.Infof)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: controllerAgentName})

	controller := &Controller{
		kubeclientset:     kubeclientset,
		podserviceclientset:   podserviceclientset,
		podsLister: podInformer.Lister(),
		podsSynced: podInformer.Informer().HasSynced,
		podservicesLister:        podserviceInformer.Lister(),
		podservicesSynced:        podserviceInformer.Informer().HasSynced,
		workqueue:         workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "PodServices"),
		recorder:          recorder,
	}

	glog.Info("Setting up event handlers")
	// Set up an event handler for when PodService resources change
	podserviceInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueuePodService,
		UpdateFunc: func(old, new interface{}) {
			controller.enqueuePodService(new)
		},
		//DeleteFunc: controller.enqueuePodService,
	})
	// Set up an event handler for when Pod resources change. This
	// handler will lookup the owner of the given Pod, and if it is
	// owned by a PodService resource will enqueue that PodService resource for
	// processing. This way, we don't need to implement custom logic for
	// handling Pod resources. More info on this pattern:
	// https://github.com/kubernetes/community/blob/8cafef897a22026d42f5e5bb3f104febe7e29830/contributors/devel/controllers.md
	//cache.new
	podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.handleObject,
		UpdateFunc: func(old, new interface{}) {
			newDepl := new.(*corev1.Pod)
			oldDepl := old.(*corev1.Pod)
			if newDepl.ResourceVersion == oldDepl.ResourceVersion {
				// Periodic resync will send update events for all known Pods.
				// Two different versions of the same Pod will always have different RVs.
				return
			}
			controller.handleObject(new)
		},
		DeleteFunc: controller.handleObject,
	})

	return controller
}

// Run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *Controller) Run(threadiness int, stopCh <-chan struct{}) error {
	defer runtime.HandleCrash()
	defer c.workqueue.ShutDown()

	// Start the informer factories to begin populating the informer caches
	glog.Info("Starting PodService controller")

	// Wait for the caches to be synced before starting workers
	glog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, c.podsSynced, c.podservicesSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	glog.Info("Starting workers")
	// Launch two workers to process PodService resources
	for i := 0; i < threadiness; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	glog.Info("Started workers")
	<-stopCh
	glog.Info("Shutting down workers")

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
			runtime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		// Run the syncHandler, passing it the namespace/name string of the
		// PodService resource to be synced.
		if err := c.syncHandler(key); err != nil {
			return fmt.Errorf("error syncing '%s': %s", key, err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		c.workqueue.Forget(obj)
		glog.Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		runtime.HandleError(err)
		return true
	}

	return true
}

// syncHandler compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the PodService resource
// with the current status of the resource.
func (c *Controller) syncHandler(key string) error {
	// Convert the namespace/name string into a distinct namespace and name
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		runtime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	// Get the PodService resource with this namespace/name
	podservice, err := c.podservicesLister.PodServices(namespace).Get(name)
	if err != nil {
		// The PodService resource may no longer exist, in which case we stop
		// processing.
		if errors.IsNotFound(err) {
			runtime.HandleError(fmt.Errorf("PodService '%s' in work queue no longer exists", key))
			return nil
		}

		return err
	}

	DeploymentName := podservice.Spec.DeploymentName
	if DeploymentName == "" {
		// We choose to absorb the error here as the worker would requeue the
		// resource otherwise. Instead, the next time the resource is updated
		// the resource will be queued again.
		runtime.HandleError(fmt.Errorf("%s: deployment name must be specified", key))
		return nil
	}
	selector, _ := pkglabels.Parse("name="+DeploymentName)
	// Get the pod with the name specified in PodService.spec
	pods, err := c.podsLister.Pods(podservice.Namespace).List(selector)
	if errors.IsNotFound(err) {
		return err
		//pod, err = c.kubeclientset.AppsV1().Pods(podservice.Namespace).Create(newPod(podservice))
	}
	_, err2 := c.kubeclientset.AppsV1().Deployments(corev1.NamespaceDefault).Get(DeploymentName, metav1.GetOptions{})
	for _,pod := range pods {
		if errors.IsNotFound(err2){

			c.kubeclientset.CoreV1().Services(pod.Namespace).Delete(pod.GetName(),metav1.NewDeleteOptions(0))
			runtime.HandleError(err2)
			//return nil
		}else{
			if pod.DeletionTimestamp != nil {

				c.kubeclientset.CoreV1().Services(pod.Namespace).Delete(pod.GetName(),metav1.NewDeleteOptions(0))
			}else{
				service, err := c.kubeclientset.CoreV1().Services(pod.Namespace).Get(pod.GetName(), metav1.GetOptions{})
				service2 := newService(podservice, pod)
				if errors.IsNotFound(err){
					_, err := c.kubeclientset.CoreV1().Services(pod.Namespace).Create(service2)
					if err != nil{
						err =  err
					}
				}else{
					if !reflect.DeepEqual(service.Spec,service2.Spec)&& service2.Name!=service.Name&&service.Namespace!=service2.Namespace {
						_, err := c.kubeclientset.CoreV1().Services(pod.Namespace).Update(service2)
						if err != nil{
							err =  err
						}
					}
				}


			}
		}
	}

	// If the resource doesn't exist, we'll create it
	//if errors.IsNotFound(err) {
	//	return err
	//	//pod, err = c.kubeclientset.AppsV1().Pods(podservice.Namespace).Create(newPod(podservice))
	//}

	// If an error occurs during Get/Create, we'll requeue the item so we can
	// attempt processing again later. This could have been caused by a
	// temporary network failure, or any other transient reason.
	if err != nil {
		return err
	}

	// If the Pod is not controlled by this PodService resource, we should log
	// a warning to the event recorder and ret
	//if !metav1.IsControlledBy(deployment, podservice) {
	//	msg := fmt.Sprintf(MessageResourceExists, deployment.Name)
	//	c.recorder.Event(podservice, corev1.EventTypeWarning, ErrResourceExists, msg)
	//	return fmt.Errorf(msg)
	//}

	// If this number of the replicas on the PodService resource is specified, and the
	// number does not equal the current desired replicas on the Pod, we
	// should update the Pod resource.

	//for i,pod pod.Spec.
	//if podservice.Spec.Replicas != nil && *podservice.Spec.Replicas != *pod.Spec.Replicas {
	//	glog.V(4).Infof("PodService %s replicas: %d, pod replicas: %d", name, *foo.Spec.Replicas, *pod.Spec.Replicas)
	//	pod, err = c.kubeclientset.AppsV1().Pods(foo.Namespace).Update(newPod(foo))
	//}

	// If an error occurs during Update, we'll requeue the item so we can
	// attempt processing again later. THis could have been caused by a
	// temporary network failure, or any other transient reason.
	if err != nil {
		return err
	}

	// Finally, we update the status block of the PodService resource to reflect the
	// current state of the world


	//待修改
	//err = c.updatePodServiceStatus(podservice, deployment)



	if err != nil {
		return err
	}

	c.recorder.Event(podservice, corev1.EventTypeNormal, SuccessSynced, MessageResourceSynced)
	return nil
}

func (c *Controller) updatePodServiceStatus(podservice *podservicev1alpha1.PodService, deployment *appsv1.Deployment) error {
	// NEVER modify objects from the store. It's a read-only, local cache.
	// You can use DeepCopy() to make a deep copy of original object and modify this copy
	// Or create a copy manually for better performance
	podserviceCopy := podservice.DeepCopy()
	podserviceCopy.Status.AvailableReplicas = deployment.Status.AvailableReplicas
	// Until #38113 is merged, we must use Update instead of UpdateStatus to
	// update the Status block of the PodService resource. UpdateStatus will not
	// allow changes to the Spec of the resource, which is ideal for ensuring
	// nothing other than resource status has been updated.
	_, err := c.podserviceclientset.PodservicecrdV1alpha1().PodServices(podservice.Namespace).Update(podserviceCopy)
	return err
}

// enqueuePodService takes a PodService resource and converts it into a namespace/name
// string which is then put onto the work queue. This method should *not* be
// passed resources of any type other than PodService.
func (c *Controller) enqueuePodService(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		runtime.HandleError(err)
		return
	}
	c.workqueue.AddRateLimited(key)
}

// handleObject will take any resource implementing metav1.Object and attempt
// to find the PodService resource that 'owns' it. It does this by looking at the
// objects metadata.ownerReferences field for an appropriate OwnerReference.
// It then enqueues that PodService resource to be processed. If the object does not
// have an appropriate OwnerReference, it will simply be skipped.
func (c *Controller) handleObject(obj interface{}) {
	var object metav1.Object
	var ok bool
	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			runtime.HandleError(fmt.Errorf("error decoding object, invalid type"))
			return
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			runtime.HandleError(fmt.Errorf("error decoding object tombstone, invalid type"))
			return
		}
		glog.V(4).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
	}
	glog.V(4).Infof("Processing object: %s", object.GetName())
	if ownerRef := metav1.GetControllerOf(object); ownerRef != nil {
		// If this object is not owned by a PodService, we should not do anything more
		// with it.
		if ownerRef.Kind != "PodService" {
			return
		}

		podservice, err := c.podservicesLister.PodServices(object.GetNamespace()).Get(ownerRef.Name)
		if err != nil {
			glog.V(4).Infof("ignoring orphaned object '%s' of PodService '%s'", object.GetSelfLink(), ownerRef.Name)
			return
		}

		c.enqueuePodService(podservice)
		return
	}
}

// newPod creates a new Pod for a PodService resource. It also sets
// the appropriate OwnerReferences on the resource so handleObject can discover
// the PodService resource that 'owns' it.
func newService(podService *podservicev1alpha1.PodService, pod *corev1.Pod) *corev1.Service {
	//labels := pod.Labels
	serviceType := podService.Spec.ServiceType
	if serviceType == ""{
		serviceType = "ClusterIP"
	}
	ports := []corev1.ServicePort{}
	//containers := pod.Spec.Containers
	Ports := podService.Spec.Ports
	for _,Port := range Ports {
		//for _,containerPort := range containerPorts{
			var name string
			//port := Port.Port
			if Port.Name == ""{
				name = strings.ToLower(string(Port.Protocol))+"-"+strconv.Itoa(int(Port.TargetPort))
			}else{
				name = Port.Name

			}
			servicePort := corev1.ServicePort{
				Name:     name,
				Port:     Port.Port,
				Protocol: corev1.Protocol(Port.Protocol),
				TargetPort: intstr.IntOrString{
					Type: 0,
					IntVal: Port.TargetPort,
				},
			}
			ports = append(ports, servicePort)

		//}
	}
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:   pod.GetName(),
			Labels: pod.GetLabels(),
		},
		Spec: corev1.ServiceSpec{
			Ports: ports,
			Selector: pod.GetLabels(),
			Type: corev1.ServiceType(serviceType),
		},
	}
	return service
}
