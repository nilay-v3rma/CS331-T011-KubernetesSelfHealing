package controller

import (
	"context"
	"fmt"
	"log"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// NetworkHealthCheckController polls cluster-scoped NetworkHealthCheck objects
// and invokes one reconciliation for each object on every polling round.
type NetworkHealthCheckController struct {
	Reconciler   *Reconciler
	PollInterval time.Duration
}

// Run starts the polling loop and stops when ctx is cancelled.
func (c *NetworkHealthCheckController) Run(ctx context.Context) error {
	if c.Reconciler == nil || c.Reconciler.DynamicClient == nil {
		return fmt.Errorf("reconciler with dynamic Kubernetes client is required")
	}

	interval := c.PollInterval
	if interval <= 0 {
		interval = 15 * time.Second
	}

	if err := c.ReconcileAll(ctx); err != nil {
		log.Printf("NetworkHealthCheck polling round failed: %v", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := c.ReconcileAll(ctx); err != nil {
				log.Printf("NetworkHealthCheck polling round failed: %v", err)
			}
		}
	}
}

// ReconcileAll lists every NetworkHealthCheck and updates each status once.
func (c *NetworkHealthCheckController) ReconcileAll(ctx context.Context) error {
	resources, err := c.Reconciler.DynamicClient.Resource(networkHealthCheckGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	var firstErr error
	for i := range resources.Items {
		resource := &resources.Items[i]
		spec, err := networkHealthCheckSpecFromResource(resource)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", resource.GetName(), err)
			}
			continue
		}

		_, _, err = c.Reconciler.ReconcileNetworkHealthCheck(ctx, resource.GetName(), spec)
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", resource.GetName(), err)
		}
	}
	return firstErr
}

func networkHealthCheckSpecFromResource(resource *unstructured.Unstructured) (NetworkHealthCheckSpec, error) {
	spec := NetworkHealthCheckSpec{
		Interval:            15 * time.Second,
		ProbeTimeout:        2 * time.Second,
		ProbeCount:          5,
		ProbePayloadBytes:   1024,
		MaxConcurrentProbes: 1,
	}

	interval, found, err := unstructured.NestedString(resource.Object, "spec", "interval")
	if err != nil {
		return spec, fmt.Errorf("invalid spec.interval: %w", err)
	}
	if found && interval != "" {
		spec.Interval, err = time.ParseDuration(interval)
		if err != nil {
			return spec, fmt.Errorf("invalid spec.interval %q: %w", interval, err)
		}
	}

	probeTimeout, found, err := unstructured.NestedString(resource.Object, "spec", "probeTimeout")
	if err != nil {
		return spec, fmt.Errorf("invalid spec.probeTimeout: %w", err)
	}
	if found && probeTimeout != "" {
		spec.ProbeTimeout, err = time.ParseDuration(probeTimeout)
		if err != nil {
			return spec, fmt.Errorf("invalid spec.probeTimeout %q: %w", probeTimeout, err)
		}
	}

	spec.ProbeType, _, err = unstructured.NestedString(resource.Object, "spec", "probeType")
	if err != nil {
		return spec, fmt.Errorf("invalid spec.probeType: %w", err)
	}
	spec.Topology, _, err = unstructured.NestedString(resource.Object, "spec", "topology")
	if err != nil {
		return spec, fmt.Errorf("invalid spec.topology: %w", err)
	}

	probeCount, found, err := unstructured.NestedInt64(resource.Object, "spec", "probeCount")
	if err != nil {
		return spec, fmt.Errorf("invalid spec.probeCount: %w", err)
	}
	if found && probeCount > 0 {
		spec.ProbeCount = int(probeCount)
	}

	probePayloadBytes, found, err := unstructured.NestedInt64(resource.Object, "spec", "probePayloadBytes")
	if err != nil {
		return spec, fmt.Errorf("invalid spec.probePayloadBytes: %w", err)
	}
	if found && probePayloadBytes > 0 {
		spec.ProbePayloadBytes = int(probePayloadBytes)
	}

	maxConcurrent, found, err := unstructured.NestedInt64(resource.Object, "spec", "maxConcurrentProbes")
	if err != nil {
		return spec, fmt.Errorf("invalid spec.maxConcurrentProbes: %w", err)
	}
	if found {
		spec.MaxConcurrentProbes = int(maxConcurrent)
	}

	spec.RemediationEnabled, _, err = unstructured.NestedBool(resource.Object, "spec", "remediation", "enabled")
	if err != nil {
		return spec, fmt.Errorf("invalid spec.remediation.enabled: %w", err)
	}
	spec.RemediationDryRun, _, err = unstructured.NestedBool(resource.Object, "spec", "remediation", "dryRun")
	if err != nil {
		return spec, fmt.Errorf("invalid spec.remediation.dryRun: %w", err)
	}

	failureThreshold, found, err := unstructured.NestedInt64(resource.Object, "spec", "failureThreshold")
	if err != nil {
		return spec, fmt.Errorf("invalid spec.failureThreshold: %w", err)
	}
	if found {
		spec.FailureThreshold = int(failureThreshold)
	}

	successThreshold, found, err := unstructured.NestedInt64(resource.Object, "spec", "successThreshold")
	if err != nil {
		return spec, fmt.Errorf("invalid spec.successThreshold: %w", err)
	}
	if found {
		spec.SuccessThreshold = int(successThreshold)
	}

	return spec, nil
}
