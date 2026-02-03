package kubernetes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	corekube "github.com/jonwraymond/toolexec/runtime/backend/kubernetes"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type PodSpec = corekube.PodSpec
type PodResult = corekube.PodResult
type ResourceSpec = corekube.ResourceSpec
type SecuritySpec = corekube.SecuritySpec
type Logger = corekube.Logger
type PodRunner = corekube.PodRunner
type HealthChecker = corekube.HealthChecker
type ImageResolver = corekube.ImageResolver

var (
	ErrClientNotConfigured = corekube.ErrClientNotConfigured
	ErrPodCreationFailed   = corekube.ErrPodCreationFailed
	ErrPodExecutionFailed  = corekube.ErrPodExecutionFailed
)

// ClientConfig configures a Kubernetes API client.
type ClientConfig struct {
	// KubeconfigPath points to a kubeconfig file; empty uses defaults.
	KubeconfigPath string

	// Context overrides the kubeconfig context.
	Context string

	// InCluster uses in-cluster configuration when true.
	InCluster bool

	// QPS sets client-go QPS; zero uses defaults.
	QPS float32

	// Burst sets client-go burst; zero uses defaults.
	Burst int

	// PollInterval for status checks.
	PollInterval time.Duration

	// JobTTL controls TTLSecondsAfterFinished.
	JobTTL time.Duration

	// JobPrefix prefixes job names for executions.
	JobPrefix string
}

// Client implements PodRunner and HealthChecker using client-go.
type Client struct {
	clientset    kubernetes.Interface
	pollInterval time.Duration
	jobTTL       time.Duration
	jobPrefix    string
	logger       Logger
}

// NewClient creates a new Kubernetes client using the provided configuration.
func NewClient(cfg ClientConfig, logger Logger) (*Client, error) {
	var restCfg *rest.Config
	var err error

	switch {
	case cfg.InCluster:
		restCfg, err = rest.InClusterConfig()
	default:
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		if cfg.KubeconfigPath != "" {
			loadingRules.ExplicitPath = cfg.KubeconfigPath
		}
		overrides := &clientcmd.ConfigOverrides{}
		if cfg.Context != "" {
			overrides.CurrentContext = cfg.Context
		}
		restCfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	}
	if err != nil {
		return nil, err
	}

	if cfg.QPS > 0 {
		restCfg.QPS = cfg.QPS
	}
	if cfg.Burst > 0 {
		restCfg.Burst = cfg.Burst
	}

	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, err
	}

	poll := cfg.PollInterval
	if poll == 0 {
		poll = 2 * time.Second
	}
	jobTTL := cfg.JobTTL
	if jobTTL == 0 {
		jobTTL = 10 * time.Minute
	}
	jobPrefix := cfg.JobPrefix
	if jobPrefix == "" {
		jobPrefix = "toolrun"
	}

	return &Client{
		clientset:    clientset,
		pollInterval: poll,
		jobTTL:       jobTTL,
		jobPrefix:    jobPrefix,
		logger:       logger,
	}, nil
}

// Ping verifies the Kubernetes API is reachable.
func (c *Client) Ping(ctx context.Context) error {
	if c.clientset == nil {
		return ErrClientNotConfigured
	}
	_, err := c.clientset.Discovery().ServerVersion()
	return err
}

// Run executes the given pod spec as a Kubernetes Job.
func (c *Client) Run(ctx context.Context, spec PodSpec) (PodResult, error) {
	if c.clientset == nil {
		return PodResult{}, ErrClientNotConfigured
	}
	if err := spec.Validate(); err != nil {
		return PodResult{}, err
	}

	runID, err := randomID()
	if err != nil {
		return PodResult{}, err
	}
	jobName := fmt.Sprintf("%s-%s", c.jobPrefix, runID)

	labels := map[string]string{
		"toolruntime.run": runID,
	}
	for k, v := range spec.Labels {
		labels[k] = v
	}

	container := corev1.Container{
		Name:       "runner",
		Image:      spec.Image,
		Command:    spec.Command,
		Args:       spec.Args,
		WorkingDir: spec.WorkingDir,
		Env:        toEnvVars(spec.Env),
		Resources:  toResourceRequirements(spec.Resources),
		SecurityContext: &corev1.SecurityContext{
			ReadOnlyRootFilesystem:   boolPtr(spec.Security.ReadOnlyRootfs),
			AllowPrivilegeEscalation: boolPtr(false),
			RunAsNonRoot:             boolPtr(true),
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
			},
		},
	}

	if runAsUser, ok := parseUserID(spec.Security.User); ok {
		container.SecurityContext.RunAsUser = &runAsUser
	}

	podSpec := corev1.PodSpec{
		RestartPolicy:      corev1.RestartPolicyNever,
		Containers:         []corev1.Container{container},
		ServiceAccountName: spec.ServiceAccount,
	}
	if spec.RuntimeClassName != "" {
		podSpec.RuntimeClassName = &spec.RuntimeClassName
	}

	if spec.Security.NetworkMode == "host" {
		podSpec.HostNetwork = true
	}

	if spec.Timeout > 0 {
		seconds := int64(spec.Timeout.Seconds())
		podSpec.ActiveDeadlineSeconds = &seconds
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: spec.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            int32Ptr(0),
			TTLSecondsAfterFinished: int32Ptr(int32(c.jobTTL.Seconds())),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: podSpec,
			},
		},
	}

	start := time.Now()

	created, err := c.clientset.BatchV1().Jobs(spec.Namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return PodResult{}, fmt.Errorf("%w: %v", ErrPodCreationFailed, err)
	}

	if c.logger != nil {
		c.logger.Info("kubernetes job created", "job", created.Name, "namespace", spec.Namespace)
	}

	defer func() {
		policy := metav1.DeletePropagationBackground
		_ = c.clientset.BatchV1().Jobs(spec.Namespace).Delete(context.Background(), created.Name, metav1.DeleteOptions{
			PropagationPolicy: &policy,
		})
	}()

	if err := c.waitForCompletion(ctx, spec.Namespace, created.Name); err != nil {
		return PodResult{}, err
	}

	pod, err := c.findPodForJob(ctx, spec.Namespace, created.Name)
	if err != nil {
		return PodResult{}, err
	}

	stdout, err := c.readLogs(ctx, spec.Namespace, pod.Name, "runner")
	if err != nil {
		return PodResult{}, err
	}

	exitCode := int32(0)
	if len(pod.Status.ContainerStatuses) > 0 && pod.Status.ContainerStatuses[0].State.Terminated != nil {
		exitCode = pod.Status.ContainerStatuses[0].State.Terminated.ExitCode
	}

	return PodResult{
		ExitCode: int(exitCode),
		Stdout:   stdout,
		Stderr:   "",
		Duration: time.Since(start),
	}, nil
}

func (c *Client) waitForCompletion(ctx context.Context, namespace, jobName string) error {
	for {
		job, err := c.clientset.BatchV1().Jobs(namespace).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("%w: %v", ErrPodExecutionFailed, err)
		}
		if job.Status.Succeeded > 0 {
			return nil
		}
		if job.Status.Failed > 0 {
			return fmt.Errorf("%w: job failed", ErrPodExecutionFailed)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(c.pollInterval):
		}
	}
}

func (c *Client) findPodForJob(ctx context.Context, namespace, jobName string) (*corev1.Pod, error) {
	selector := fmt.Sprintf("job-name=%s", jobName)
	pods, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, fmt.Errorf("%w: list pods: %v", ErrPodExecutionFailed, err)
	}
	if len(pods.Items) == 0 {
		return nil, fmt.Errorf("%w: no pods found for job %s", ErrPodExecutionFailed, jobName)
	}
	return &pods.Items[0], nil
}

func (c *Client) readLogs(ctx context.Context, namespace, podName, container string) (string, error) {
	req := c.clientset.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: container,
	})
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: logs: %v", ErrPodExecutionFailed, err)
	}
	defer stream.Close()

	data, err := io.ReadAll(stream)
	if err != nil {
		return "", fmt.Errorf("%w: logs read: %v", ErrPodExecutionFailed, err)
	}
	return string(data), nil
}

func toEnvVars(env []string) []corev1.EnvVar {
	out := make([]corev1.EnvVar, 0, len(env))
	for _, item := range env {
		if item == "" {
			continue
		}
		parts := strings.SplitN(item, "=", 2)
		if len(parts) == 2 {
			out = append(out, corev1.EnvVar{Name: parts[0], Value: parts[1]})
		}
	}
	return out
}

func toResourceRequirements(res ResourceSpec) corev1.ResourceRequirements {
	limits := corev1.ResourceList{}
	if res.MemoryBytes > 0 {
		limits[corev1.ResourceMemory] = *resource.NewQuantity(res.MemoryBytes, resource.BinarySI)
	}
	if res.CPUQuota > 0 {
		limits[corev1.ResourceCPU] = *resource.NewMilliQuantity(res.CPUQuota, resource.DecimalSI)
	}
	if res.DiskBytes > 0 {
		limits[corev1.ResourceEphemeralStorage] = *resource.NewQuantity(res.DiskBytes, resource.BinarySI)
	}
	return corev1.ResourceRequirements{Limits: limits, Requests: limits}
}

func parseUserID(raw string) (int64, bool) {
	if raw == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

func randomID() (string, error) {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func boolPtr(v bool) *bool {
	return &v
}

func int32Ptr(v int32) *int32 {
	return &v
}

var _ PodRunner = (*Client)(nil)
var _ HealthChecker = (*Client)(nil)
