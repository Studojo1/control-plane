package k8s

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Client wraps Kubernetes client for service status queries
type Client struct {
	clientset *kubernetes.Clientset
	namespace string
}

// NewClient creates a new Kubernetes client
// Uses in-cluster config if running in Kubernetes, otherwise tries kubeconfig
func NewClient(namespace string) (*Client, error) {
	var config *rest.Config
	var err error

	// Try in-cluster config first (for production)
	config, err = rest.InClusterConfig()
	if err != nil {
		// Fallback to kubeconfig (for local development)
		home := os.Getenv("HOME")
		if home == "" {
			home = os.Getenv("USERPROFILE") // Windows
		}
		if home == "" {
			return nil, fmt.Errorf("failed to get k8s config: in-cluster config failed and HOME not set")
		}
		kubeconfig := filepath.Join(home, ".kube", "config")
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to get k8s config: %w", err)
		}
		slog.Info("using kubeconfig for k8s client", "path", kubeconfig)
	} else {
		slog.Info("using in-cluster config for k8s client")
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s clientset: %w", err)
	}

	return &Client{
		clientset: clientset,
		namespace: namespace,
	}, nil
}

// GetDeployments returns all deployments in the namespace
func (c *Client) GetDeployments(ctx context.Context) ([]appsv1.Deployment, error) {
	deployments, err := c.clientset.AppsV1().Deployments(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list deployments: %w", err)
	}
	return deployments.Items, nil
}

// GetDeployment returns a specific deployment by name
func (c *Client) GetDeployment(ctx context.Context, name string) (*appsv1.Deployment, error) {
	deployment, err := c.clientset.AppsV1().Deployments(c.namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment %s: %w", name, err)
	}
	return deployment, nil
}

// GetReplicaSets returns all replica sets for a deployment
func (c *Client) GetReplicaSets(ctx context.Context, deploymentName string) ([]appsv1.ReplicaSet, error) {
	deployment, err := c.GetDeployment(ctx, deploymentName)
	if err != nil {
		return nil, err
	}

	// Get all replica sets with matching labels
	selector := deployment.Spec.Selector
	replicaSets, err := c.clientset.AppsV1().ReplicaSets(c.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(selector),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list replica sets: %w", err)
	}

	return replicaSets.Items, nil
}

// GetPods returns pods for a deployment
func (c *Client) GetPods(ctx context.Context, deploymentName string) ([]corev1.Pod, error) {
	deployment, err := c.GetDeployment(ctx, deploymentName)
	if err != nil {
		return nil, err
	}

	selector := deployment.Spec.Selector
	pods, err := c.clientset.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(selector),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	return pods.Items, nil
}

// RollbackDeployment rolls back a deployment to a specific ReplicaSet
func (c *Client) RollbackDeployment(ctx context.Context, deploymentName string, replicaSetName string) error {
	// Get the target ReplicaSet
	rs, err := c.clientset.AppsV1().ReplicaSets(c.namespace).Get(ctx, replicaSetName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get replica set %s: %w", replicaSetName, err)
	}

	// Get the deployment
	deployment, err := c.GetDeployment(ctx, deploymentName)
	if err != nil {
		return err
	}

	// Update deployment to use the ReplicaSet's pod template
	deployment.Spec.Template = rs.Spec.Template
	deployment.Spec.RevisionHistoryLimit = int32Ptr(10) // Keep history

	_, err = c.clientset.AppsV1().Deployments(c.namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to rollback deployment: %w", err)
	}

	return nil
}

// StreamLogs streams logs from a pod
func (c *Client) StreamLogs(ctx context.Context, serviceName, podName, tailLines string, follow bool) (io.ReadCloser, error) {
	// If pod name is not provided, get the first pod for the service
	if podName == "" {
		pods, err := c.GetPods(ctx, serviceName)
		if err != nil {
			return nil, fmt.Errorf("failed to get pods for service %s: %w", serviceName, err)
		}
		if len(pods) == 0 {
			return nil, fmt.Errorf("no pods found for service %s", serviceName)
		}
		podName = pods[0].Name
	}

	// Get pod logs
	req := c.clientset.CoreV1().Pods(c.namespace).GetLogs(podName, &corev1.PodLogOptions{
		Follow:    follow,
		TailLines: int64Ptr(100),
	})

	if tailLines != "" {
		var tail int64
		fmt.Sscanf(tailLines, "%d", &tail)
		if tail > 0 {
			req = c.clientset.CoreV1().Pods(c.namespace).GetLogs(podName, &corev1.PodLogOptions{
				Follow:    follow,
				TailLines: &tail,
			})
		}
	}

	stream, err := req.Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to stream logs: %w", err)
	}

	return stream, nil
}

// ScaleDeployment scales a deployment to the specified number of replicas
func (c *Client) ScaleDeployment(ctx context.Context, deploymentName string, replicas int32) error {
	deployment, err := c.GetDeployment(ctx, deploymentName)
	if err != nil {
		return fmt.Errorf("failed to get deployment %s: %w", deploymentName, err)
	}

	deployment.Spec.Replicas = &replicas
	_, err = c.clientset.AppsV1().Deployments(c.namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to scale deployment %s: %w", deploymentName, err)
	}

	return nil
}

func int64Ptr(i int64) *int64 {
	return &i
}

func int32Ptr(i int32) *int32 {
	return &i
}

