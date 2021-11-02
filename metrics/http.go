// Copyright (c) OpenFaaS Author(s) 2021. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type Http struct {
	RequestsTotal            *prometheus.CounterVec
	RequestDurationHistogram *prometheus.HistogramVec
	InFlight                 prometheus.Gauge
}

func NewHttp() Http {
	h := Http{
		RequestsTotal: promauto.NewCounterVec(prometheus.CounterOpts{
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "total HTTP requests processed",
		}, []string{"code", "method"}),
		RequestDurationHistogram: promauto.NewHistogramVec(prometheus.HistogramOpts{
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "Seconds spent serving HTTP requests.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"code", "method"}),
		InFlight: promauto.NewGauge(prometheus.GaugeOpts{
			Subsystem: "http",
			Name:      "requests_in_flight",
			Help:      "total HTTP requests in-flight",
		}),
	}

	// Default to 0 for queries during graceful shutdown.
	h.InFlight.Set(0)
	return h
}
