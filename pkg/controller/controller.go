package controller

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"log"
	"reflect"
	"time"

	"github.com/VictoriaMetrics/metrics"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
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
	// start informer
	go factory.Start(stopCh)

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

	// start to sync and call list
	if !cache.WaitForCacheSync(stopCh, nodeInformer.HasSynced) {
		log.Fatal("Timed out waiting for caches to sync")
	}

	nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(old, new interface{}) {
			if reflect.DeepEqual(old, new) {
				return
			}
			newNode := new.(*corev1.Node)
			oldNode := old.(*corev1.Node)
			if diffNodeStatusReady(oldNode, newNode) {
				kleaner.Process(new)
			}
		}})

	kleaner.podInformer = podInformer
	kleaner.jobInformer = jobInformer

	kleaner.taintEvictionQueue = CreateWorkerQueue(deletePodHandler(c, tm.emitPodDeletionEvent))

	return kleaner
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
		// skip nodes that are already in the deleting process
		if !node.DeletionTimestamp.IsZero() {
			return
		}
		// normal cleanup flow
		if shouldCleanupNode(t) {
			c.CleanupNode(t)
		}

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

func (c *Kleaner) DeletePod(pod *corev1.Pod, isForce bool) {
	if c.dryRun {
		log.Printf("dry-run: Pod '%s:%s' would have been deleted", pod.Namespace, pod.Name)
		return
	}
	log.Printf("Deleting pod '%s/%s'", pod.Namespace, pod.Name)
	var po metav1.DeleteOptions
	if isForce {
		po.GracePeriodSeconds = new(int64)

		// TODO: force release pod and attachment pvc and cni
	}


	if err := c.kclient.CoreV1().Pods(pod.Namespace).Delete(c.ctx, pod.Name, po); ignoreNotFound(err) != nil {
		log.Printf("failed to delete pod '%s:%s': %v", pod.Namespace, pod.Name, err)
		metrics.GetOrCreateCounter(metricName(podDeletedFailedMetric, pod.Namespace)).Inc()
		return
	}
	metrics.GetOrCreateCounter(metricName(podDeletedMetric, pod.Namespace)).Inc()
}


func (c *Kleaner) CleanupNode(node *corev1.Node) {
	if c.dryRun {
		log.Printf("dry-run: Node %s would have been cleanup", node.Name)
		return
	}

	// get all pod -> filter pods -> add pods to


}