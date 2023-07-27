package controller

import (
	"context"
	"fmt"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"log"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
)

const (
	retries              = 5

)

func podRelatedToCronJob(pod *corev1.Pod, jobStore cache.Store) bool {
	isOwnedByJob := isOwnedByJob(getPodOwnerKinds(pod))
	if !isOwnedByJob {
		return false
	}
	jobOwnerName := pod.OwnerReferences[0].Name
	jobOwner, exists, err := jobStore.GetByKey(pod.Namespace + "/" + jobOwnerName)
	if err != nil {
		log.Printf("Can't find job '%s:%s`", pod.Namespace, jobOwnerName)
	} else if exists && isOwnedByCronJob(getJobOwnerKinds(jobOwner.(*batchv1.Job))) {
		return true
	}
	return false
}

func shouldDeletePod(pod *corev1.Pod, orphaned, pending, evicted, successful, failed time.Duration) bool {
	// evicted pods, those with or without owner references, but in Evicted state
	//  - uses c.deleteEvictedAfter, this one is tricky, because there is no timestamp of eviction.
	// So, basically it will be removed as soon as discovered
	if pod.Status.Phase == corev1.PodFailed && pod.Status.Reason == "Evicted" && evicted > 0 {
		return true
	}
	owners := getPodOwnerKinds(pod)
	podFinishTime := podFinishTime(pod)
	if !podFinishTime.IsZero() {
		age := time.Since(podFinishTime)
		// orphaned pod: those that do not have any owner references
		// - uses c.deleteOrphanedAfter
		if len(owners) == 0 {
			if orphaned > 0 && age >= orphaned {
				return true
			}
		}
		// owned by job, have exactly one ownerReference present and its kind is Job
		//  - uses the c.deleteSuccessfulAfter, c.deleteFailedAfter, c.deletePendingAfter
		if isOwnedByJob(owners) {
			switch pod.Status.Phase {
			case corev1.PodSucceeded:
				if successful > 0 && age >= successful {
					return true
				}
			case corev1.PodFailed:
				if failed > 0 && age >= failed {
					return true
				}
			default:
				return false
			}
			return false
		}
	}
	if pod.Status.Phase == corev1.PodPending && pending > 0 {
		t := podLastTransitionTime(pod)
		if t.IsZero() {
			return false
		}
		if time.Now().Sub(t) >= pending {
			return true
		}
	}
	return false
}

func getPodOwnerKinds(pod *corev1.Pod) []string {
	var kinds []string
	for _, ow := range pod.OwnerReferences {
		kinds = append(kinds, ow.Kind)
	}
	return kinds
}

// isOwnedByJob returns true if and only if pod has a single owner
// and this owners kind is Job
func isOwnedByJob(ownerKinds []string) bool {
	if len(ownerKinds) == 1 && ownerKinds[0] == "Job" {
		return true
	}
	return false
}

func podLastTransitionTime(podObj *corev1.Pod) time.Time {
	for _, pc := range podObj.Status.Conditions {
		if pc.Type == corev1.PodScheduled && pc.Status == corev1.ConditionFalse {
			return pc.LastTransitionTime.Time
		}
	}
	return time.Time{}
}

func podFinishTime(podObj *corev1.Pod) time.Time {
	for _, pc := range podObj.Status.Conditions {
		// Looking for the time when pod's condition "Ready" became "false" (equals end of execution)
		if pc.Type == corev1.PodReady && pc.Status == corev1.ConditionFalse {
			return pc.LastTransitionTime.Time
		}
	}
	return time.Time{}
}


func deletePodHandler(c *Kleaner, emitEventFunc func(types.NamespacedName)) func(ctx context.Context, args *WorkArgs) error {
	return func(ctx context.Context, args *WorkArgs) error {
		ns := args.NamespacedName.Namespace
		name := args.NamespacedName.Name
		nodeName := args.NodeName
		log.Printf("CleanupManager is deleting pod %s\n", args.NamespacedName.String())

		var err error
		pod, err := c.kclient.CoreV1().Pods(ns).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}

		if pod.Spec.NodeName != nodeName {
			return nil
		}

		if emitEventFunc != nil {
			emitEventFunc(args.NamespacedName)
		}

		for i := 0; i < retries; i++ {
			log.Printf("retry to delete pod %s/%s\n", pod.Namespace, pod.Name)
			isDeleted := c.DeletePod(pod, true)
			if isDeleted {
				return nil
			}
			time.Sleep(3 * time.Second)
		}
		return fmt.Errorf("failed to delete pod %s/%s", pod.Namespace, pod.Name)
	}
}

func isNeedProcessPod(pod corev1.Pod, kc *kubernetes.Clientset) bool {
	podNamespacedName := types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name}
	if pod.OwnerReferences == nil || len(pod.OwnerReferences) == 0 {
		// pod is not owner by controller, not process
		return false
	}
	for _, ref := range pod.OwnerReferences {
		log.Printf("pod %s owner %+v", podNamespacedName, ref)
		if ref.Kind == "StatefulSet" && *ref.Controller == true {
			// pod owner by statefulset
			return true
		}
		if ref.Kind == "ReplicaSet" && *ref.Controller == true {
			var hitPVC, hitNotPVC bool
			for _, vol := range pod.Spec.Volumes {
				if vol.CSI != nil {
					// not support inline csi volume
					hitNotPVC = true
					continue
				}
				if vol.PersistentVolumeClaim != nil {
					pvcName := vol.PersistentVolumeClaim
					pvc, err := kc.CoreV1().PersistentVolumeClaims(pod.Namespace).
						Get(context.TODO(), pvcName.ClaimName, metav1.GetOptions{})
					if err != nil {
						continue
					}
					scName := pvc.Spec.StorageClassName
					sc, err := kc.StorageV1().StorageClasses().Get(context.TODO(), *scName, metav1.GetOptions{})
					if (err == nil && isWhiteListStoragePro(sc.Provisioner)) || isWhiteListFromPvcAnns(pvc) {
						hitPVC = true
					} else {
						hitNotPVC = true
					}
				}
			}

			if hitPVC && !hitNotPVC {
				// only pod owner by rs have volumes, which are all allowed force migrated
				return true
			}
		}

		// TODO: other controlled pod
	}

	return false
}

var allowed []string = []string{"cinder.csi.openstack.org"}

func isWhiteListStoragePro(provisioner string) bool {

	for _, item := range allowed {
		if item == provisioner {
			return true
		}
	}
	return false
}

func isWhiteListFromPvcAnns(pvc *corev1.PersistentVolumeClaim) bool {
	var provisioner string
	if _, ok := pvc.Annotations["volume.beta.kubernetes.io/storage-provisioner"]; ok {
		provisioner = pvc.Annotations["volume.beta.kubernetes.io/storage-provisioner"]
	}

	if _, ok := pvc.Annotations["volume.kubernetes.io/storage-provisioner"]; ok {
		provisioner = pvc.Annotations["volume.kubernetes.io/storage-provisioner"]
	}

	if provisioner == "" {
		return false
	}

	for _, item := range allowed {
		if item == provisioner {
			return true
		}
	}
	return false
}