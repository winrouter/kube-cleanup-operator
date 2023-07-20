package controller

import (
	"context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"log"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
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


func deletePodHandler(c clientset.Interface, emitEventFunc func(types.NamespacedName)) func(ctx context.Context, args *WorkArgs) error {
	return func(ctx context.Context, args *WorkArgs) error {
		ns := args.NamespacedName.Namespace
		name := args.NamespacedName.Name
		klog.FromContext(ctx).Info("NoExecuteTaintManager is deleting pod", "pod", args.NamespacedName.String())
		if emitEventFunc != nil {
			emitEventFunc(args.NamespacedName)
		}
		var err error
		for i := 0; i < retries; i++ {
			err = addConditionAndDeletePod(ctx, c, name, ns)
			if err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		return err
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
					if isWhiteListStoragePro(sc.Provisioner) {
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

func isWhiteListStoragePro(provisioner string) bool {
	allowed := []string {
		"cinder.csi.openstack.org",
	}
	for _, item := range allowed {
		if item == provisioner {
			return true
		}
	}
	return false
}