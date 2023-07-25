package controller

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	"log"
	"math"
	"reflect"
	"sync"
	"time"

	"github.com/VictoriaMetrics/metrics"
	backoff "github.com/cenkalti/backoff/v4"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	v1helper "k8s.io/kubernetes/pkg/apis/core/v1/helper"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/projectcalico/calico/libcalico-go/lib/apiconfig"
	libcalicoclient "github.com/projectcalico/calico/libcalico-go/lib/clientv3"
	libipam "github.com/projectcalico/calico/libcalico-go/lib/ipam"
)

func ignoreNotFound(err error) error {
	if apierrs.IsNotFound(err) {
		return nil
	}
	return err
}

func metricName(name string, namespace string) string {
	return fmt.Sprintf(`%s{namespace=%q}`, name, namespace)
}

const (
	resyncPeriod           = time.Second * 30
	podDeletedMetric       = "pods_deleted_total"
	podDeletedFailedMetric = "pods_deleted_failed_total"
	jobDeletedFailedMetric = "jobs_deleted_failed_total"
	jobDeletedMetric       = "jobs_deleted_total"
)

// CNIConfig for access cni ipmi to release ip resource
type CNIConfig struct {
	cniType  string
	paras    map[string]string
}

// Kleaner watches the kubernetes api for changes to Pods and Jobs and
// delete those according to configured timeouts
type Kleaner struct {
	podInformer cache.SharedIndexInformer
	jobInformer cache.SharedIndexInformer
	kclient     *kubernetes.Clientset

	deleteSuccessfulAfter time.Duration
	deleteFailedAfter     time.Duration
	deletePendingAfter    time.Duration
	deleteOrphanedAfter   time.Duration
	deleteEvictedAfter    time.Duration

	ignoreOwnedByCronjob bool
	
	labelSelector        string

	dryRun bool
	ctx    context.Context
	stopCh <-chan struct{}

	taintEvictionQueue *TimedWorkerQueue
	recorder              record.EventRecorder
	cniConfig             CNIConfig
	allowCSIDrivers       []string

	// keeps a map from nodeName to all noExecute taints on that Node
	taintedNodesLock sync.Mutex
	taintedNodes     map[string][]corev1.Taint
}

// NewKleaner creates a new NewKleaner
func NewKleaner(ctx context.Context, kclient *kubernetes.Clientset, namespace string, dryRun bool, deleteSuccessfulAfter,
	deleteFailedAfter, deletePendingAfter, deleteOrphanedAfter, deleteEvictedAfter time.Duration, ignoreOwnedByCronjob bool,
	labelSelector string,
	stopCh <-chan struct{}) *Kleaner {
	jobInformer := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				options.LabelSelector = labelSelector
				return kclient.BatchV1().Jobs(namespace).List(ctx, options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				options.LabelSelector = labelSelector
				return kclient.BatchV1().Jobs(namespace).Watch(ctx, options)
			},
		},
		&batchv1.Job{},
		resyncPeriod,
		cache.Indexers{},
	)
	// Create informer for watching Namespaces
	podInformer := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				options.LabelSelector = labelSelector
				return kclient.CoreV1().Pods(namespace).List(ctx, options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				options.LabelSelector = labelSelector
				return kclient.CoreV1().Pods(namespace).Watch(ctx, options)
			},
		},
		&corev1.Pod{},
		resyncPeriod,
		cache.Indexers{},
	)
	// Create informer for watching node
	factory := informers.NewSharedInformerFactory(kclient, resyncPeriod)
	nodeInformer := factory.Core().V1().Nodes().Informer()


	eventBroadcaster := record.NewBroadcaster()
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "cleanup-controller"})

	kleaner := &Kleaner{
		dryRun:                dryRun,
		kclient:               kclient,
		ctx:                   ctx,
		stopCh:                stopCh,
		deleteSuccessfulAfter: deleteSuccessfulAfter,
		deleteFailedAfter:     deleteFailedAfter,
		deletePendingAfter:    deletePendingAfter,
		deleteOrphanedAfter:   deleteOrphanedAfter,
		deleteEvictedAfter:    deleteEvictedAfter,
		ignoreOwnedByCronjob:  ignoreOwnedByCronjob,
		labelSelector:         labelSelector,
		recorder:              recorder,
		taintedNodes:          make(map[string][]corev1.Taint),
	}
	jobInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(old, new interface{}) {
			if !reflect.DeepEqual(old, new) {
				kleaner.Process(new)
			}
		},
	})
	podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(old, new interface{}) {
			if !reflect.DeepEqual(old, new) {
				kleaner.Process(new)
			}
		},
	})

	nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			log.Printf("node add\n")
			kleaner.Process(obj)
		},
		UpdateFunc: func(old, new interface{}) {
			log.Printf("node update\n")
			if reflect.DeepEqual(old, new) {
				return
			}
			newNode := new.(*corev1.Node)
			oldNode := old.(*corev1.Node)
			if diffNodeStatusReady(oldNode, newNode) {

				kleaner.Process(new)
			}
		}})

	// start informer factory
	factory.Start(stopCh)

	// start to sync and call list
	if !cache.WaitForCacheSync(stopCh, nodeInformer.HasSynced) {
		log.Fatal("Timed out waiting for caches to sync")
	}
	log.Printf("informer factory start work\n")

	kleaner.podInformer = podInformer
	kleaner.jobInformer = jobInformer

	kleaner.taintEvictionQueue = CreateWorkerQueue(deletePodHandler(kleaner, kleaner.emitPodDeletionEvent))

	return kleaner
}

func (c *Kleaner) periodicCacheCheck() {
	ticker := time.NewTicker(2 * resyncPeriod)
	for {
		select {
		case <-c.stopCh:
			ticker.Stop()
			return
		case <-ticker.C:
			for _, job := range c.jobInformer.GetStore().List() {
				c.Process(job)
			}
			for _, obj := range c.podInformer.GetStore().List() {
				c.Process(obj)
			}
		}
	}
}

// Run starts the process for listening for pod changes and acting upon those changes.
func (c *Kleaner) Run() {
	log.Printf("Listening for changes...")

	go c.podInformer.Run(c.stopCh)
	go c.jobInformer.Run(c.stopCh)

	go c.periodicCacheCheck()

	<-c.stopCh
}

func (c *Kleaner) Process(obj interface{}) {
	switch t := obj.(type) {
	case *batchv1.Job:
		// skip jobs that are already in the deleting process
		if !t.DeletionTimestamp.IsZero() {
			return
		}
		if shouldDeleteJob(t, c.deleteSuccessfulAfter, c.deleteFailedAfter, c.ignoreOwnedByCronjob) {
			c.DeleteJob(t)
		}
	case *corev1.Pod:
		pod := t
		// skip pods that are already in the deleting process
		if !pod.DeletionTimestamp.IsZero() {
			return
		}
		// skip pods related to jobs created by cronjobs if `ignoreOwnedByCronjob` is set
		if c.ignoreOwnedByCronjob && podRelatedToCronJob(pod, c.jobInformer.GetStore()) {
			return
		}
		// normal cleanup flow
		if shouldDeletePod(t, c.deleteOrphanedAfter, c.deletePendingAfter, c.deleteEvictedAfter, c.deleteSuccessfulAfter, c.deleteFailedAfter) {
			c.DeletePod(t, false)
		}
	case *corev1.Node:
		node := t
		log.Printf("start to work node %s", node.Name)
		// skip nodes that are already in the deleting process
		if !node.DeletionTimestamp.IsZero() {
			return
		}
		// normal cleanup flow
		c.CleanupNode(t)


	}
}

func (c *Kleaner) DeleteJob(job *batchv1.Job) {
	if c.dryRun {
		log.Printf("dry-run: Job '%s:%s' would have been deleted", job.Namespace, job.Name)
		return
	}
	log.Printf("Deleting job '%s/%s'", job.Namespace, job.Name)
	propagation := metav1.DeletePropagationForeground
	jo := metav1.DeleteOptions{PropagationPolicy: &propagation}
	if err := c.kclient.BatchV1().Jobs(job.Namespace).Delete(c.ctx, job.Name, jo); ignoreNotFound(err) != nil {
		log.Printf("failed to delete job '%s:%s': %v", job.Namespace, job.Name, err)
		metrics.GetOrCreateCounter(metricName(jobDeletedFailedMetric, job.Namespace)).Inc()
		return
	}
	metrics.GetOrCreateCounter(metricName(jobDeletedMetric, job.Namespace)).Inc()
}

func (c *Kleaner) DeletePod(pod *corev1.Pod, isForce bool) bool{
	if c.dryRun {
		log.Printf("dry-run: Pod '%s:%s' would have been deleted", pod.Namespace, pod.Name)
		return true
	}
	log.Printf("Deleting pod '%s/%s'", pod.Namespace, pod.Name)
	var po metav1.DeleteOptions
	if isForce {
		po.GracePeriodSeconds = new(int64)

		// TODO: force release pod and attachment pvc and cni
		if deleted := c.DeleteAttachVolume(pod); !deleted {
			log.Printf("deleteAttachVolume pod %s/%s failed\n", pod.Namespace, pod.Name)
			return false
		}

		if deleted := c.DeleteAttachNetwork(pod); !deleted {
			log.Printf("deleteAttachNetwork pod %s/%s failed\n", pod.Namespace, pod.Name)
			return false
		}
	}

	if err := c.kclient.CoreV1().Pods(pod.Namespace).Delete(c.ctx, pod.Name, po); ignoreNotFound(err) != nil {
		log.Printf("failed to delete pod '%s:%s': %v", pod.Namespace, pod.Name, err)
		metrics.GetOrCreateCounter(metricName(podDeletedFailedMetric, pod.Namespace)).Inc()
		return false
	}
	metrics.GetOrCreateCounter(metricName(podDeletedMetric, pod.Namespace)).Inc()
	return true
}

func (c *Kleaner) DeleteAttachVolume(pod *corev1.Pod) bool {
	log.Printf("start to deattach volume in pod %s\n", pod.Name)
	ns := pod.Namespace
	nodeName := pod.Spec.NodeName
	podNamespaced := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)

	if nodeName == "" {
		return true
	}

	volAttachMaps := make(map[string]storagev1.VolumeAttachment, 0)
	volAttachments, err := c.kclient.StorageV1().VolumeAttachments().List(context.TODO(),
		metav1.ListOptions{})
	if err != nil {
		log.Printf("list volAttachments failed:%v\n", err)
		return false
	}

	for _, attach := range volAttachments.Items {
		//TODO: work for inline volume
		if attach.Spec.NodeName != nodeName {
			continue
		}
		volAttachMaps[*attach.Spec.Source.PersistentVolumeName] = attach
	}

	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim == nil {
			continue
		}

		pvcName := volume.PersistentVolumeClaim.ClaimName
		pvc, err := c.kclient.CoreV1().PersistentVolumeClaims(ns).Get(context.TODO(), pvcName, metav1.GetOptions{})
		if err != nil {
			return false
		}
		pvName := pvc.Spec.VolumeName

		attach, ok := volAttachMaps[pvName]
		if !ok {
			continue
		}
		log.Printf("start to delete volumeattach %s in pod %s\n", attach.Name, podNamespaced)
		err = c.kclient.StorageV1().VolumeAttachments().Delete(context.TODO(), attach.Name, metav1.DeleteOptions{})
		if err != nil {
			log.Printf("delete volumeattach %s failed\n", attach.Name)
			return false
		}

		// TODO: wait the volumeattachment delete
		b := backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 5)
		checkDeleted := func() error {
			_, err := c.kclient.StorageV1().VolumeAttachments().Get(context.TODO(), attach.Name, metav1.GetOptions{})
			if err != nil {

				if apierrors.IsNotFound(err) {
					log.Printf("delete volumeattach %s in pod %s success\n", attach.Name, podNamespaced)
					return nil
				}
				return err
			}
			log.Printf("retry to delete volumeattach %s in pod %s volumeattachment failed and retry \n",attach.Name, podNamespaced)
			return fmt.Errorf("%s is not deleted", attach.Name)
		}

		if err = backoff.Retry(checkDeleted, b); err != nil {
			log.Printf("retry to delete pod %s volumeattachment failed \n",podNamespaced)
			return false
		}

		log.Printf("pvc %s on pod %s unattach from node %s\n", pvc.Name, pod.Name, nodeName)
	}

	return true
}

func (c *Kleaner) DeleteAttachNetwork(pod *corev1.Pod) bool {
	log.Printf("start to delete attach network %s/%s\n", pod.Namespace, pod.Name)
	if _, ok := pod.Annotations["cni.projectcalico.org/podIP"]; !ok {
		return true
	}

	// TODO: only support simple ip
	ip := pod.Status.PodIP

	cfg, err := apiconfig.LoadClientConfig("")
	if err != nil {
		return false
	}

	// Create a new backend client.
	client, err := libcalicoclient.New(*cfg)
	if err != nil {
		return false
	}

	ipamClient := client.IPAM()

	opt := libipam.ReleaseOptions{Address: ip}

	// Call ReleaseIPs releases the IP and returns an empty slice as unallocatedIPs if
	// release was successful else it returns back the slice with the IP passed in.
	unallocatedIPs, err := ipamClient.ReleaseIPs(context.TODO(), opt)
	if err != nil {
		log.Printf("ReleaseIps Error: %v\n", err)
		return false
	}

	// Couldn't release the IP if the slice is not empty or IP might already be released/unassigned.
	// This is not exactly an error, so not returning it to the caller.
	if len(unallocatedIPs) != 0 {
		log.Printf("IP address %s is not assigned\n", ip)
		return false
	}

	// If unallocatedIPs slice is empty then IP was released Successfully.
	fmt.Printf("Successfully released IP address %s\n", ip)
	return true
}


func (c *Kleaner) CleanupNode(node *corev1.Node) {
	if c.dryRun {
		log.Printf("dry-run: Node %s would have been cleanup", node.Name)
		return
	}

	logger := klog.FromContext(context.TODO())

	now := time.Now()

	// get all pod -> filter pods -> add pods to
	podList, err := c.kclient.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + node.Name})
	if err != nil {
		// print log
		return
	}

	debugPodList(podList.Items)

	taints := getNoExecuteTaints(node.Spec.Taints)
	debugTaints(taints)
	// TODO: force annotation missing need to cacel work task
	if len(taints) == 0 {
		log.Printf("not found noExecuteTaint\n")
		c.taintedNodesLock.Lock()
		if _, ok := c.taintedNodes[node.Name]; !ok {
			c.taintedNodesLock.Unlock()
			return
		}
		log.Printf("delete taintNode in cache, node %s\n", "node", node.Name)
		delete(c.taintedNodes, node.Name)
		c.taintedNodesLock.Unlock()

		log.Printf("All taints were removed from the node %s. Cancelling all evictions...\n", klog.KObj(node))
		for _, pod := range podList.Items {
			log.Printf("cancel work for pod %s\n", types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name})
			c.cancelWorkWithEvent(logger, types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name})
		}
		return
	}

	func() {
		c.taintedNodesLock.Lock()
		defer c.taintedNodesLock.Unlock()
		log.Printf("Updating known taints %v on node %s\n", taints, klog.KObj(node))
		if len(taints) == 0 {
			delete(c.taintedNodes, node.Name)
		} else {
			c.taintedNodes[node.Name] = taints
		}
	}()

	if len(podList.Items) == 0 {
		return
	}

	if !shouldCleanupNode(node) {
		log.Printf("should not cleanup node %s\n", node.Name)
		return
	}

	for _, pod := range podList.Items {
		podNamespacedName := types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name}
		log.Printf("check for pod %s\n", podNamespacedName)

		if !isNeedProcessPod(pod, c.kclient) {
			// print log
			log.Printf("not need to deal with pod %s\n", podNamespacedName)
			continue
		}

		log.Printf("start to deal with pod %s\n", podNamespacedName)

		tolerations := pod.Spec.Tolerations
		allTolerated, usedTolerations := v1helper.GetMatchingTolerations(taints, tolerations)
		if !allTolerated {
			log.Printf("Not all taints are tolerated after update for pod on node %s %s %s %s\n",
				"pod", podNamespacedName.String(), "node", klog.KRef("", node.Name))
			// We're canceling scheduled work (if any), as we're going to delete the Pod right away.
			c.cancelWorkWithEvent(logger, podNamespacedName)
			c.taintEvictionQueue.AddWork(context.TODO(), NewWorkArgs(podNamespacedName.Name, podNamespacedName.Namespace), time.Now(), time.Now())
			continue
		}

		minTolerationTime := getMinTolerationTime(usedTolerations)
		log.Printf("get minTolerationTime %s %s\n", "time", minTolerationTime)
		// getMinTolerationTime returns negative value to denote infinite toleration.
		if minTolerationTime < 0 {
			log.Printf("Current tolerations for pod tolerate forever, cancelling any scheduled deletion %s %s\n",
				"pod", podNamespacedName.String())
			c.cancelWorkWithEvent(logger, podNamespacedName)
			continue
		}

		// TODO: cleanup-manager reboot, and the time is going pod teminating , always need to wait 5min
		startTime := now
		triggerTime := startTime.Add(minTolerationTime)
		scheduledEviction := c.taintEvictionQueue.GetWorkerUnsafe(podNamespacedName.String())
		if scheduledEviction != nil {
			log.Printf("found in scheduledEviction %s %s\n", "pod", podNamespacedName)
			startTime = scheduledEviction.CreatedAt
			if startTime.Add(minTolerationTime).Before(triggerTime) {
				log.Printf("triggerTime is available %s %s\n", "pod", podNamespacedName)
				continue
			}
			log.Printf("triggerTime is unavailable %s %s\n", "pod", podNamespacedName)
			c.cancelWorkWithEvent(logger, podNamespacedName)
		}
		log.Printf("add work to evictionQueue %s %s %s %s %s %s\n", "pod", podNamespacedName,
			"startTime", startTime, "triggerTime", triggerTime)
		c.taintEvictionQueue.AddWork(context.TODO(), NewWorkArgs(podNamespacedName.Name, podNamespacedName.Namespace), startTime, triggerTime)
	}
}

func (c *Kleaner) cancelWorkWithEvent(logger klog.Logger, nsName types.NamespacedName) {
	if c.taintEvictionQueue.CancelWork(logger, nsName.String()) {
		c.emitCancelPodDeletionEvent(nsName)
	}
}


func (c *Kleaner) emitPodDeletionEvent(nsName types.NamespacedName) {
	if c.recorder == nil {
		return
	}
	ref := &corev1.ObjectReference{
		Kind:      "Pod",
		Name:      nsName.Name,
		Namespace: nsName.Namespace,
	}
	c.recorder.Eventf(ref, corev1.EventTypeNormal, "TaintManagerEviction", "Marking for deletion Pod %s", nsName.String())
}


func (c *Kleaner) emitCancelPodDeletionEvent(nsName types.NamespacedName) {
	if c.recorder == nil {
		return
	}
	ref := &corev1.ObjectReference{
		Kind:      "Pod",
		Name:      nsName.Name,
		Namespace: nsName.Namespace,
	}
	c.recorder.Eventf(ref, corev1.EventTypeNormal, "TaintManagerEviction", "Cancelling deletion of Pod %s", nsName.String())
}


// getMinTolerationTime returns minimal toleration time from the given slice, or -1 if it's infinite.
func getMinTolerationTime(tolerations []corev1.Toleration) time.Duration {
	minTolerationTime := int64(math.MaxInt64)
	if len(tolerations) == 0 {
		return 0
	}

	for i := range tolerations {
		if tolerations[i].TolerationSeconds != nil {
			tolerationSeconds := *(tolerations[i].TolerationSeconds)
			if tolerationSeconds <= 0 {
				return 0
			} else if tolerationSeconds < minTolerationTime {
				minTolerationTime = tolerationSeconds
			}
		}
	}

	if minTolerationTime == int64(math.MaxInt64) {
		return -1
	}
	return time.Duration(minTolerationTime) * time.Second
}

func debugPodList(pods []corev1.Pod) {
	log.Printf("podList:\n")
	for _, pod := range pods {
		log.Printf("\t pod:%s/%s", pod.Namespace, pod.Name)
	}
}

func debugTaints(t []corev1.Taint) {
	log.Printf("taints:\n")
	for _, ta := range t {
		log.Printf("\t %v  %v\n", ta, ta.TimeAdded)
	}

}