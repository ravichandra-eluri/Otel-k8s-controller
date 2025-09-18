package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	otelv1alpha1 "github.com/ravichandra-eluri/otel-k8s-controller/api/v1alpha1"
)

const (
	otelCollectorFinalizer = "otel.ravichandra-eluri.io/finalizer"
	requeueAfter           = 30 * time.Second
)

// OTelCollectorReconciler reconciles an OTelCollector object
type OTelCollectorReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=otel.ravichandra-eluri.io,resources=otelcollectors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=otel.ravichandra-eluri.io,resources=otelcollectors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=otel.ravichandra-eluri.io,resources=otelcollectors/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services;configmaps,verbs=get;list;watch;create;update;patch;delete

// Reconcile is the core reconciliation loop — called whenever the desired or
// observed state of an OTelCollector resource changes.
func (r *OTelCollectorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithValues("otelcollector", req.NamespacedName)

	// 1. Fetch the OTelCollector instance
	collector := &otelv1alpha1.OTelCollector{}
	if err := r.Get(ctx, req.NamespacedName, collector); err != nil {
		if errors.IsNotFound(err) {
			// Resource deleted — nothing to do
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to fetch OTelCollector")
		return ctrl.Result{}, err
	}

	// 2. Handle deletion via finalizer
	if !collector.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, collector)
	}

	// 3. Register finalizer if not present
	if !controllerutil.ContainsFinalizer(collector, otelCollectorFinalizer) {
		controllerutil.AddFinalizer(collector, otelCollectorFinalizer)
		if err := r.Update(ctx, collector); err != nil {
			return ctrl.Result{}, err
		}
	}

	// 4. Reconcile ConfigMap (collector pipeline config)
	if err := r.reconcileConfigMap(ctx, collector); err != nil {
		logger.Error(err, "Failed to reconcile ConfigMap")
		return r.setDegradedStatus(ctx, collector, "ConfigMapFailed", err.Error())
	}

	// 5. Reconcile Deployment
	if err := r.reconcileDeployment(ctx, collector); err != nil {
		logger.Error(err, "Failed to reconcile Deployment")
		return r.setDegradedStatus(ctx, collector, "DeploymentFailed", err.Error())
	}

	// 6. Reconcile Service
	if err := r.reconcileService(ctx, collector); err != nil {
		logger.Error(err, "Failed to reconcile Service")
		return r.setDegradedStatus(ctx, collector, "ServiceFailed", err.Error())
	}

	// 7. Update status to Running
	return r.setRunningStatus(ctx, collector)
}

// reconcileConfigMap creates or updates the OTel Collector config YAML as a ConfigMap
func (r *OTelCollectorReconciler) reconcileConfigMap(ctx context.Context, collector *otelv1alpha1.OTelCollector) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      collectorConfigMapName(collector),
			Namespace: collector.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		cm.Data = map[string]string{
			"config.yaml": r.buildCollectorConfig(collector),
		}
		return controllerutil.SetControllerReference(collector, cm, r.Scheme)
	})
	return err
}

// buildCollectorConfig generates the OTLP collector config YAML from the CRD spec
func (r *OTelCollectorReconciler) buildCollectorConfig(collector *otelv1alpha1.OTelCollector) string {
	spec := collector.Spec
	protocol := "grpc"
	receiverPort := spec.Pipeline.Port
	if receiverPort == 0 {
		receiverPort = 4317
	}
	if spec.Pipeline.Mode == otelv1alpha1.PipelineModeHTTP {
		protocol = "http"
		if receiverPort == 4317 {
			receiverPort = 4318
		}
	}

	safExtension := ""
	if spec.StoreAndForward.Enabled {
		bufferPath := spec.StoreAndForward.BufferPath
		if bufferPath == "" {
			bufferPath = "/var/otel/buffer"
		}
		safExtension = fmt.Sprintf(`
  file_storage:
    directory: %s
    timeout: 10s`, bufferPath)
	}

	samplingProcessor := ""
	if spec.Sampling.Strategy == otelv1alpha1.SamplingTail {
		samplingProcessor = `
  tail_sampling:
    decision_wait: 10s
    num_traces: 100
    expected_new_traces_per_sec: 10
    policies:
      - name: errors-policy
        type: status_code
        status_code: {status_codes: [ERROR]}
      - name: slow-traces-policy
        type: latency
        latency: {threshold_ms: 1000}`
	}

	_ = protocol
	return fmt.Sprintf(`# Auto-generated by otel-k8s-controller — do not edit manually
receivers:
  otlp:
    protocols:
      %s:
        endpoint: "0.0.0.0:%d"
  prometheus:
    config:
      scrape_configs:
        - job_name: 'otel-collector'
          scrape_interval: 30s
          static_configs:
            - targets: ['localhost:8888']

processors:
  batch:
    timeout: 10s
    send_batch_size: 1024
  memory_limiter:
    limit_mib: 512
    spike_limit_mib: 128
    check_interval: 5s%s

exporters:
  otlp:
    endpoint: "%s"
    tls:
      insecure: false
  logging:
    loglevel: warn

extensions:
  health_check:
    endpoint: "0.0.0.0:13133"
  pprof:
    endpoint: "0.0.0.0:1777"%s

service:
  extensions: [health_check, pprof]
  pipelines:
    traces:
      receivers: [otlp]
      processors: [memory_limiter, batch]
      exporters: [otlp, logging]
    metrics:
      receivers: [prometheus]
      processors: [batch]
      exporters: [otlp]
`,
		spec.Pipeline.Mode,
		receiverPort,
		samplingProcessor,
		spec.ExporterEndpoint,
		safExtension,
	)
}

// reconcileDeployment creates or updates the collector Deployment
func (r *OTelCollectorReconciler) reconcileDeployment(ctx context.Context, collector *otelv1alpha1.OTelCollector) error {
	image := collector.Spec.Image
	if image == "" {
		image = "otel/opentelemetry-collector-contrib:latest"
	}

	replicas := collector.Spec.Replicas
	if replicas == 0 {
		replicas = 1
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      collector.Name,
			Namespace: collector.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		deploy.Spec = appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: collectorLabels(collector),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: collectorLabels(collector),
					Annotations: map[string]string{
						"prometheus.io/scrape": "true",
						"prometheus.io/port":   "8888",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "otel-collector",
							Image: image,
							Args:  []string{"--config=/conf/config.yaml"},
							Ports: []corev1.ContainerPort{
								{Name: "otlp-grpc", ContainerPort: 4317, Protocol: corev1.ProtocolTCP},
								{Name: "otlp-http", ContainerPort: 4318, Protocol: corev1.ProtocolTCP},
								{Name: "metrics", ContainerPort: 8888, Protocol: corev1.ProtocolTCP},
								{Name: "health", ContainerPort: 13133, Protocol: corev1.ProtocolTCP},
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("100m"),
									corev1.ResourceMemory: resource.MustParse("256Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("500m"),
									corev1.ResourceMemory: resource.MustParse("512Mi"),
								},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/",
										Port: intstr.FromInt(13133),
									},
								},
								InitialDelaySeconds: 10,
								PeriodSeconds:       15,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/",
										Port: intstr.FromInt(13133),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
							},
							VolumeMounts: r.buildVolumeMounts(collector),
						},
					},
					Volumes: r.buildVolumes(collector),
				},
			},
		}
		return controllerutil.SetControllerReference(collector, deploy, r.Scheme)
	})
	return err
}

// reconcileService creates or updates the headless Service for the collector
func (r *OTelCollectorReconciler) reconcileService(ctx context.Context, collector *otelv1alpha1.OTelCollector) error {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      collector.Name,
			Namespace: collector.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Spec = corev1.ServiceSpec{
			Selector: collectorLabels(collector),
			Ports: []corev1.ServicePort{
				{Name: "otlp-grpc", Port: 4317, TargetPort: intstr.FromString("otlp-grpc"), Protocol: corev1.ProtocolTCP},
				{Name: "otlp-http", Port: 4318, TargetPort: intstr.FromString("otlp-http"), Protocol: corev1.ProtocolTCP},
				{Name: "metrics", Port: 8888, TargetPort: intstr.FromString("metrics"), Protocol: corev1.ProtocolTCP},
			},
		}
		return controllerutil.SetControllerReference(collector, svc, r.Scheme)
	})
	return err
}

// handleDeletion cleans up resources before the CR is deleted
func (r *OTelCollectorReconciler) handleDeletion(ctx context.Context, collector *otelv1alpha1.OTelCollector) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling deletion of OTelCollector", "name", collector.Name)

	// Owned resources (Deployment, Service, ConfigMap) are garbage collected
	// automatically via OwnerReferences — just remove the finalizer.
	controllerutil.RemoveFinalizer(collector, otelCollectorFinalizer)
	if err := r.Update(ctx, collector); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// setRunningStatus patches the status to Running
func (r *OTelCollectorReconciler) setRunningStatus(ctx context.Context, collector *otelv1alpha1.OTelCollector) (ctrl.Result, error) {
	now := metav1.Now()
	collector.Status.Phase = "Running"
	collector.Status.LastReconcileTime = &now
	collector.Status.Conditions = []metav1.Condition{
		{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "ReconcileSuccess",
			Message:            "OTelCollector is running",
			LastTransitionTime: now,
		},
	}
	if err := r.Status().Update(ctx, collector); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// setDegradedStatus patches the status to Degraded with a reason
func (r *OTelCollectorReconciler) setDegradedStatus(ctx context.Context, collector *otelv1alpha1.OTelCollector, reason, msg string) (ctrl.Result, error) {
	now := metav1.Now()
	collector.Status.Phase = "Degraded"
	collector.Status.LastReconcileTime = &now
	collector.Status.Conditions = []metav1.Condition{
		{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             reason,
			Message:            msg,
			LastTransitionTime: now,
		},
	}
	_ = r.Status().Update(ctx, collector)
	return ctrl.Result{RequeueAfter: requeueAfter}, fmt.Errorf("%s: %s", reason, msg)
}

// buildVolumeMounts returns volume mounts including optional SAF buffer
func (r *OTelCollectorReconciler) buildVolumeMounts(collector *otelv1alpha1.OTelCollector) []corev1.VolumeMount {
	mounts := []corev1.VolumeMount{
		{Name: "config", MountPath: "/conf"},
	}
	if collector.Spec.StoreAndForward.Enabled {
		bufferPath := collector.Spec.StoreAndForward.BufferPath
		if bufferPath == "" {
			bufferPath = "/var/otel/buffer"
		}
		mounts = append(mounts, corev1.VolumeMount{
			Name:      "saf-buffer",
			MountPath: bufferPath,
		})
	}
	return mounts
}

// buildVolumes returns volumes including optional SAF buffer PVC
func (r *OTelCollectorReconciler) buildVolumes(collector *otelv1alpha1.OTelCollector) []corev1.Volume {
	volumes := []corev1.Volume{
		{
			Name: "config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: collectorConfigMapName(collector),
					},
				},
			},
		},
	}
	if collector.Spec.StoreAndForward.Enabled {
		maxSize := collector.Spec.StoreAndForward.MaxBufferSizeMB
		if maxSize == 0 {
			maxSize = 512
		}
		volumes = append(volumes, corev1.Volume{
			Name: "saf-buffer",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					SizeLimit: func() *resource.Quantity {
						q := resource.MustParse(fmt.Sprintf("%dMi", maxSize))
						return &q
					}(),
				},
			},
		})
	}
	return volumes
}

// SetupWithManager registers the controller with the manager
func (r *OTelCollectorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&otelv1alpha1.OTelCollector{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}

// --- helpers ---

func collectorLabels(c *otelv1alpha1.OTelCollector) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "otel-collector",
		"app.kubernetes.io/instance":   c.Name,
		"app.kubernetes.io/managed-by": "otel-k8s-controller",
	}
}

func collectorConfigMapName(c *otelv1alpha1.OTelCollector) string {
	return fmt.Sprintf("%s-config", c.Name)
}

// getDeploymentReadyReplicas is a helper used during status syncing
func (r *OTelCollectorReconciler) getDeploymentReadyReplicas(ctx context.Context, collector *otelv1alpha1.OTelCollector) int32 {
	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: collector.Name, Namespace: collector.Namespace}, deploy); err != nil {
		return 0
	}
	return deploy.Status.ReadyReplicas
}
