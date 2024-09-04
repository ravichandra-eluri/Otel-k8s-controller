// Package v1alpha1 contains API Schema definitions for the otel v1alpha1 API group
// +groupName=otel.chandradevgo.io
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PipelineMode defines the OTLP pipeline mode
// +kubebuilder:validation:Enum=grpc;http
type PipelineMode string

const (
	PipelineModeGRPC PipelineMode = "grpc"
	PipelineModeHTTP PipelineMode = "http"
)

// SamplingStrategy defines head vs tail sampling
// +kubebuilder:validation:Enum=head;tail;always_on;always_off
type SamplingStrategy string

const (
	SamplingHead     SamplingStrategy = "head"
	SamplingTail     SamplingStrategy = "tail"
	SamplingAlwaysOn SamplingStrategy = "always_on"
	SamplingAlwaysOff SamplingStrategy = "always_off"
)

// OTelCollectorSpec defines the desired state of OTelCollector
type OTelCollectorSpec struct {
	// Replicas is the number of collector instances to run
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10
	Replicas int32 `json:"replicas,omitempty"`

	// Image is the OTel Collector container image
	// +kubebuilder:default="otel/opentelemetry-collector-contrib:latest"
	Image string `json:"image,omitempty"`

	// Pipeline defines the OTLP pipeline configuration
	Pipeline PipelineSpec `json:"pipeline"`

	// Sampling defines the sampling strategy
	Sampling SamplingSpec `json:"sampling,omitempty"`

	// StoreAndForward enables offline buffering when backend is unreachable
	StoreAndForward StoreAndForwardSpec `json:"storeAndForward,omitempty"`

	// ExporterEndpoint is the OTLP backend endpoint (e.g. Jaeger, GCP Trace)
	// +kubebuilder:validation:Required
	ExporterEndpoint string `json:"exporterEndpoint"`
}

// PipelineSpec configures the OTLP ingestion pipeline
type PipelineSpec struct {
	// Mode is either grpc or http
	// +kubebuilder:default=grpc
	Mode PipelineMode `json:"mode,omitempty"`

	// Port is the port the collector listens on
	// +kubebuilder:default=4317
	Port int32 `json:"port,omitempty"`

	// EnableMetrics enables the Prometheus metrics receiver
	// +kubebuilder:default=true
	EnableMetrics bool `json:"enableMetrics,omitempty"`

	// EnableTraces enables the OTLP trace receiver
	// +kubebuilder:default=true
	EnableTraces bool `json:"enableTraces,omitempty"`

	// EnableLogs enables the OTLP log receiver
	// +kubebuilder:default=false
	EnableLogs bool `json:"enableLogs,omitempty"`
}

// SamplingSpec configures the sampling strategy
type SamplingSpec struct {
	// Strategy is the sampling approach
	// +kubebuilder:default=always_on
	Strategy SamplingStrategy `json:"strategy,omitempty"`

	// SamplingRate applies to head sampling (0.0 - 1.0)
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	SamplingRate float64 `json:"samplingRate,omitempty"`

	// TailPolicies lists tail sampling policy names (latency, error_rate, etc.)
	TailPolicies []string `json:"tailPolicies,omitempty"`
}

// StoreAndForwardSpec configures offline buffering behavior
type StoreAndForwardSpec struct {
	// Enabled toggles store-and-forward mode
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// BufferPath is the volume path for buffered spans
	// +kubebuilder:default="/var/otel/buffer"
	BufferPath string `json:"bufferPath,omitempty"`

	// MaxBufferSizeMB is the max disk buffer size in MB
	// +kubebuilder:default=512
	MaxBufferSizeMB int32 `json:"maxBufferSizeMB,omitempty"`

	// RetryIntervalSeconds is how often to retry flushing buffered data
	// +kubebuilder:default=30
	RetryIntervalSeconds int32 `json:"retryIntervalSeconds,omitempty"`
}

// OTelCollectorStatus defines the observed state of OTelCollector
type OTelCollectorStatus struct {
	// Phase represents the current lifecycle phase
	// +kubebuilder:validation:Enum=Pending;Running;Degraded;Failed
	Phase string `json:"phase,omitempty"`

	// ReadyReplicas is the number of ready collector pods
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// Conditions represent the latest available observations
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastReconcileTime is the last time the controller reconciled this resource
	LastReconcileTime *metav1.Time `json:"lastReconcileTime,omitempty"`

	// ExporterReachable indicates whether the OTLP backend is reachable
	ExporterReachable bool `json:"exporterReachable,omitempty"`

	// BufferedSpanCount is the number of spans currently buffered (SAF mode)
	BufferedSpanCount int64 `json:"bufferedSpanCount,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:subresource:scale:specpath=.spec.replicas,statuspath=.status.readyReplicas
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Exporter",type=string,JSONPath=`.spec.exporterEndpoint`
// +kubebuilder:printcolumn:name="SAF",type=boolean,JSONPath=`.spec.storeAndForward.enabled`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// OTelCollector is the Schema for the otelcollectors API
type OTelCollector struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OTelCollectorSpec   `json:"spec,omitempty"`
	Status OTelCollectorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// OTelCollectorList contains a list of OTelCollector
type OTelCollectorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OTelCollector `json:"items"`
}
