package k8s

import "time"

// ServiceStatus represents the status of a service from Kubernetes
type ServiceStatus struct {
	Name           string    `json:"name"`
	Status         string    `json:"status"` // healthy, unhealthy, unknown
	Version        string    `json:"version"`
	Replicas       int       `json:"replicas"`
	ReadyReplicas  int       `json:"ready_replicas"`
	LastDeployment time.Time `json:"last_deployment"`
}

// DeploymentVersion represents a deployment version from ReplicaSet history
type DeploymentVersion struct {
	Version        string    `json:"version"`
	ReplicaSetName string    `json:"replica_set_name"`
	Replicas       int       `json:"replicas"`
	ReadyReplicas int       `json:"ready_replicas"`
	CreatedAt      time.Time `json:"created_at"`
}

