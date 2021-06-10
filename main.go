package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	v1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

func main() {
	// assume we are running in cluster
	config, err := rest.InClusterConfig()
	if err != nil {
		// fall back to out-of-cluster config for development / debugging
		var kubeconfig *string
		if home := homedir.HomeDir(); home != "" {
			kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
		} else {
			kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
		}
		flag.Parse()

		config, err = clientcmd.BuildConfigFromFlags("", *kubeconfig)
		if err != nil {
			panic(err.Error())
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	nodeName := os.Getenv("KUBERNETES_NODE_NAME")

	for {
		err := fixStuckDeployments(*clientset, nodeName)
		if err != nil {
			panic(err.Error())
		}

		time.Sleep(30 * time.Second)
	}
}

func fixStuckDeployments(clientset kubernetes.Clientset, nodeName string) error {
	node, err := clientset.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if !node.Spec.Unschedulable {
		fmt.Print("Node is schedulable, ignoring\n")
		return nil
	}
	fmt.Print("Node is unschedulable, getting apps\n")

	deployments, err := getStuckDeployments(clientset, nodeName)
	if err != nil {
		return err
	}

	for _, deployment := range deployments {
		if _, ok := deployment.Spec.Template.Annotations["kubedrainrollout.kubernetes.io/restartedAt"]; ok {
			fmt.Printf("Detected stuck deployment %s in namespace%s, already being restarted...", deployment.Name, deployment.Namespace)
			continue
		}

		patch := []byte(fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"kubedrainrollout.kubernetes.io/restartedAt":"%s"}}}}}`, time.Now().Format(time.RFC3339)))

		fmt.Printf("Detected stuck deployment %s in namespace%s, restarting rollout...\n%s", deployment.Name, deployment.Namespace, string(patch))

		_, err := clientset.AppsV1().Deployments(deployment.Namespace).Patch(context.TODO(), deployment.Name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
		if err != nil {
			return err
		}
	}
	return nil
}

func getStuckDeployments(clientset kubernetes.Clientset, nodeName string) ([]*v1.Deployment, error) {
	var deployments []*v1.Deployment

	pods, err := clientset.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": nodeName}).String()})
	if err != nil {
		return nil, err
	}

	for _, pod := range pods.Items {
		for _, ownerReference := range pod.OwnerReferences {
			if ownerReference.Kind != "ReplicaSet" {
				continue
			}

			replicaSet, err := clientset.AppsV1().ReplicaSets(pod.Namespace).Get(context.TODO(), ownerReference.Name, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}

			for _, ownerReference := range replicaSet.OwnerReferences {
				if ownerReference.Kind != "Deployment" {
					continue
				}

				deployment, err := clientset.AppsV1().Deployments(replicaSet.Namespace).Get(context.TODO(), ownerReference.Name, metav1.GetOptions{})
				if err != nil {
					return nil, err
				}

				if *deployment.Spec.Replicas != 1 {
					continue
				}

				podDisruptionBudgets, err := clientset.PolicyV1beta1().PodDisruptionBudgets(deployment.Namespace).List(context.TODO(), metav1.ListOptions{})
				if err != nil {
					return nil, err
				}

				for _, podDisruptionBudget := range podDisruptionBudgets.Items {
					if podDisruptionBudget.Spec.MinAvailable.Type == intstr.Int && podDisruptionBudget.Spec.MinAvailable.IntVal == 1 && podDisruptionBudget.Spec.Selector.String() == deployment.Spec.Selector.String() {
						deployments = append(deployments, deployment)
					}
				}
			}
		}
	}

	return deployments, nil
}
