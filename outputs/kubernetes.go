package outputs

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/aquasecurity/postee/v2/layout"
	"github.com/tidwall/gjson"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
)

const (
	regoInputPrefix = "event.input"

	KubernetesLabelKey      = "labels"
	KubernetesAnnotationKey = "annotations"
)

func updateMap(old map[string]string, new map[string]string) map[string]string {
	newMap := make(map[string]string)
	for k, v := range old {
		newMap[k] = v
	}
	for k, v := range new {
		newMap[k] = v
	}
	return newMap
}

type KubernetesClient struct {
	clientset kubernetes.Interface

	Name              string
	KubeNamespace     string
	KubeConfigFile    string
	KubeLabelSelector string
	KubeActions       map[string]map[string]string
}

func (k KubernetesClient) GetName() string {
	return k.Name
}

func (k *KubernetesClient) Init() error {
	config, err := clientcmd.BuildConfigFromFlags("", k.KubeConfigFile)
	if err != nil {
		log.Println("unable to initialize kubernetes config: ", err)
		return err
	}

	k.clientset, err = kubernetes.NewForConfig(config)
	if err != nil {
		log.Println("unable to initialize kubernetes client: ", err)
		return err
	}

	return nil
}

func (k KubernetesClient) prepareInputs(input map[string]string) map[string]map[string]string {
	a := make(map[string]map[string]string)

	for key, m := range k.KubeActions {
		for id, val := range m {
			var calcVal string
			if strings.HasPrefix(val, regoInputPrefix) {
				if ok := json.Valid([]byte(input["description"])); ok { // input is json
					calcVal = gjson.Get(input["description"], strings.TrimPrefix(val, regoInputPrefix+".")).String()
				} else {
					calcVal = input["description"] // input is a string
				}
			} else {
				calcVal = val // no rego to parse
			}
			a[key] = map[string]string{id: calcVal}
		}
	}

	return a
}

func (k KubernetesClient) Send(m map[string]string) error {
	ctx := context.Background()
	actions := k.prepareInputs(m)

	// TODO: Allow configuring of resource {pod, ds, ...}
	pods, _ := k.clientset.CoreV1().Pods(k.KubeNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: k.KubeLabelSelector,
	})
	for _, pod := range pods.Items {
		if len(actions[KubernetesLabelKey]) > 0 {
			retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				pod, err := k.clientset.CoreV1().Pods(pod.GetNamespace()).Get(ctx, pod.Name, metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("failed to get updated pod for labeling: %s, err: %w", pod.Name, err)
				}

				labels := updateMap(pod.GetLabels(), actions[KubernetesLabelKey])
				pod.SetLabels(labels)
				_, err = k.clientset.CoreV1().Pods(pod.GetNamespace()).Update(ctx, pod, metav1.UpdateOptions{})
				if err != nil {
					log.Println("failed to apply labels to pod:", pod.Name, "err:", err.Error(), "retrying...")
					return err
				} else {
					log.Println("labels applied successfully to pod:", pod.Name)
				}
				return nil
			})
			if retryErr != nil {
				log.Println("failed to apply labels to pod:", pod.Name, "err:", retryErr)
			}
		}

		if len(actions[KubernetesAnnotationKey]) > 0 {
			retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				pod, err := k.clientset.CoreV1().Pods(pod.GetNamespace()).Get(ctx, pod.Name, metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("failed to get updated pod for annotating: %s, err: %w", pod.Name, err)
				}

				annotations := updateMap(pod.GetAnnotations(), actions[KubernetesAnnotationKey])
				pod.SetAnnotations(annotations)
				_, err = k.clientset.CoreV1().Pods(pod.GetNamespace()).Update(ctx, pod, metav1.UpdateOptions{})
				if err != nil {
					log.Println("failed to apply annotation to pod:", pod.Name, "err:", err.Error(), "retrying...")
					return err
				} else {
					log.Println("annotations applied successfully to pod:", pod.Name)
				}
				return nil
			})
			if retryErr != nil {
				log.Println("failed to apply annotations to pod:", pod.Name, "err:", retryErr)
			}
		}
	}
	return nil
}

func (k KubernetesClient) Terminate() error {
	log.Printf("Kubernetes output terminated\n")
	return nil
}

func (k KubernetesClient) GetLayoutProvider() layout.LayoutProvider {
	return nil
}