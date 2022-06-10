// Copyright 2018 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package raftlease

import (
	"time"

	"github.com/juju/clock"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	metricsNamespace = "juju_raftlease"
)

type MetricsCollector interface {
	// Describe is part of prometheus.Collector.
	Describe(ch chan<- *prometheus.Desc)

	// Collect is part of prometheus.Collector.
	Collect(ch chan<- prometheus.Metric)
}

// operationClientMetrics is a prometheus.Collector that collects metrics
// about lease store operations.
type operationClientMetrics struct {
	requests *prometheus.SummaryVec
	clock    clock.Clock
}

func NewOperationClientMetrics(clock clock.Clock) *operationClientMetrics {
	return &operationClientMetrics{
		clock: clock,
		requests: prometheus.NewSummaryVec(prometheus.SummaryOpts{
			Namespace: metricsNamespace,
			Name:      "request",
			Help:      "Request times for lease store operations in ms",
			Objectives: map[float64]float64{
				0.5:  0.05,
				0.9:  0.01,
				0.99: 0.001,
			},
		}, []string{
			"operation", // claim, extend, pin, unpin or setTime
			"result",    // success, failure, delivery timeout, response timeout, or error
		}),
	}
}

func (m operationClientMetrics) RecordOperation(operation, result string, start time.Time) {
	elapsedMS := float64(m.clock.Now().Sub(start)) / float64(time.Millisecond)
	m.requests.With(prometheus.Labels{
		"operation": operation,
		"result":    result,
	}).Observe(elapsedMS)
}
func (m operationClientMetrics) StartOperation() time.Time {
	return m.clock.Now()
}

// Describe is part of prometheus.Collector.
func (c *operationClientMetrics) Describe(ch chan<- *prometheus.Desc) {
	c.requests.Describe(ch)
}

// Collect is part of prometheus.Collector.
func (c *operationClientMetrics) Collect(ch chan<- prometheus.Metric) {
	c.requests.Collect(ch)
}

// fsmMetricsCollector is a prometheus.Collector that collects metrics
// from the backend for leases
type fsmMetricsCollector struct {
	requests    *prometheus.SummaryVec
	expirations prometheus.Gauge
	clock       clock.Clock
}

func NewFSMMetricsCollector(clock clock.Clock) *fsmMetricsCollector {
	return &fsmMetricsCollector{
		expirations: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: metricsNamespace,
			Name:      "fsm_expirations",
			Help:      "The number of leases that have not been extended before expiring",
		}),
		requests: prometheus.NewSummaryVec(prometheus.SummaryOpts{
			Namespace: metricsNamespace,
			Name:      "fsm_commands",
			Help:      "Backend processing time for lease store operations in ms",
			Objectives: map[float64]float64{
				0.5:  0.05,
				0.9:  0.01,
				0.99: 0.001,
			},
		}, []string{
			"operation", // claim, extend, pin, unpin or setTime
			"result",    // success, failure
		}),
		clock: clock,
	}
}

var _ FSMMetrics = (*fsmMetricsCollector)(nil)

func (c *fsmMetricsCollector) StartOperation() time.Time {
	return c.clock.Now()
}
func (c *fsmMetricsCollector) RecordOperation(operation, result string, start time.Time) {
	elapsedMS := float64(c.clock.Now().Sub(start)) / float64(time.Millisecond)
	c.requests.With(prometheus.Labels{
		"operation": operation,
		"result":    result,
	}).Observe(elapsedMS)
}
func (c *fsmMetricsCollector) RecordExpirations(count int) {
	c.expirations.Add(float64(count))
}

// Describe is part of prometheus.Collector.
func (c *fsmMetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	c.expirations.Describe(ch)
}

// Collect is part of prometheus.Collector.
func (c *fsmMetricsCollector) Collect(ch chan<- prometheus.Metric) {
	c.expirations.Collect(ch)
}
