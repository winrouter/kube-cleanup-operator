package controller

import (
	"log"
	"reflect"

	corev1 "k8s.io/api/core/v1"
)

const (
	ForceEvict = "force-failover.ake.io"
)

func getNodeCondStatus(conditions []corev1.NodeCondition, condType corev1.NodeConditionType) corev1.ConditionStatus {
	for _, condition := range conditions {
		if condition.Type == condType {
			return condition.Status
		}
	}
	return corev1.ConditionUnknown
}

func diffNodeStatusReady(oldNode, newNode *corev1.Node) bool {
	if getNodeCondStatus(oldNode.Status.Conditions, corev1.NodeReady) != getNodeCondStatus(newNode.Status.Conditions, corev1.NodeReady) {
		log.Printf("oldNode ready:%v -> newNode ready:%v\n",
			getNodeCondStatus(oldNode.Status.Conditions, corev1.NodeReady),
			getNodeCondStatus(newNode.Status.Conditions, corev1.NodeReady))
		return true
	}

	if !reflect.DeepEqual(oldNode.Spec.Taints, newNode.Spec.Taints) {
		log.Printf("oldTaint:%+v, newTaint:%+v\n", oldNode.Status.Conditions, newNode.Status.Conditions)
		return true
	}

	if !reflect.DeepEqual(oldNode.Annotations, newNode.Annotations) {
		log.Printf("oldAnnots:%+v, newAnnota:%+v\n", oldNode.Annotations, newNode.Annotations)
		return true
	}
	return false
}

func shouldCleanupNode(node *corev1.Node) bool {
	status := getNodeCondStatus(node.Status.Conditions, corev1.NodeReady)
	if status == corev1.ConditionTrue {
		log.Printf("node %s is ready, ignore\n", node.Name)
		return false
	}

	forceEvictFlag := false
	for key, _ := range node.Annotations {
		if key == ForceEvict {
			forceEvictFlag = true
			break
		}
	}
	if !forceEvictFlag {
		log.Printf("node %s not found %s annotation, ignore\n", node.Name, ForceEvict)
		return false
	}

	log.Printf("node %s can force eviction\n", node.Name)
	return true

	/*
	node.spec
	spec:
	  podCIDR: 10.244.2.0/24
	  podCIDRs:
	  - 10.244.2.0/24
	  taints:
	  - effect: NoSchedule
	    key: node.kubernetes.io/unreachable
	    timeAdded: "2023-07-19T07:27:06Z"
	  - effect: NoExecute
	    key: node.kubernetes.io/unreachable
	    timeAdded: "2023-07-19T07:27:11Z"


	  pod.spec
	  tolerations:
	  - effect: NoSchedule
	    key: node-role.kubernetes.io/master
	  - effect: NoExecute
	    key: node.kubernetes.io/not-ready
	    operator: Exists
	    tolerationSeconds: 120
	  - effect: NoExecute
	    key: node.kubernetes.io/unreachable
	    operator: Exists
	    tolerationSeconds: 120
	*/

}

func getNoExecuteTaints(taints []corev1.Taint) []corev1.Taint {
	result := []corev1.Taint{}
	for i := range taints {
		if taints[i].Effect == corev1.TaintEffectNoExecute {
			result = append(result, taints[i])
		}
	}
	return result
}


// GetMatchingTolerations returns true and list of Tolerations matching all Taints if all are tolerated, or false otherwise.
func GetMatchingTolerations(taints []corev1.Taint, tolerations []corev1.Toleration) (bool, []corev1.Toleration, []corev1.Taint) {
	if len(taints) == 0{
		return true, []corev1.Toleration{}, []corev1.Taint{}
	}
	if len(tolerations) == 0 && len(taints) > 0 {
		return false, []corev1.Toleration{}, taints
	}
	result := []corev1.Toleration{}
	resultTaints := []corev1.Taint{}
	for i := range taints {
		tolerated := false
		for j := range tolerations {
			if tolerations[j].ToleratesTaint(&taints[i]) {
				result = append(result, tolerations[j])
				resultTaints = append(resultTaints, taints[i])
				tolerated = true
				break
			}
		}
		if !tolerated {
			return false, []corev1.Toleration{}, taints
		}
	}
	return true, result, resultTaints
}
