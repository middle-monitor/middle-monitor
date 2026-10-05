package services

import (
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// histogramPointCost is how many series documents a data point becomes once stored, as
// convertMetricToSeriesPoints writes them. Types it does not store cost nothing.
func histogramPointCost(dp *metricspb.HistogramDataPoint) int {
	if dp.Sum != nil {
		return 2
	}
	return 1
}

// CountMetricPoints is the number of series documents a request would store.
func CountMetricPoints(req *colmetricspb.ExportMetricsServiceRequest) int {
	n := 0
	for _, rm := range req.ResourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				switch d := m.Data.(type) {
				case *metricspb.Metric_Gauge:
					n += len(d.Gauge.DataPoints)
				case *metricspb.Metric_Sum:
					n += len(d.Sum.DataPoints)
				case *metricspb.Metric_Histogram:
					for _, dp := range d.Histogram.DataPoints {
						n += histogramPointCost(dp)
					}
				}
			}
		}
	}
	return n
}

// TrimMetricPoints keeps the first points of a request up to budget, in request
// order, and drops the rest along with whatever they leave empty.
func TrimMetricPoints(req *colmetricspb.ExportMetricsServiceRequest, budget int) {
	left := budget
	keepRM := req.ResourceMetrics[:0]
	for _, rm := range req.ResourceMetrics {
		keepSM := rm.ScopeMetrics[:0]
		for _, sm := range rm.ScopeMetrics {
			keepM := sm.Metrics[:0]
			for _, m := range sm.Metrics {
				if trimMetric(m, &left) {
					keepM = append(keepM, m)
				}
			}
			sm.Metrics = keepM
			if len(keepM) > 0 {
				keepSM = append(keepSM, sm)
			}
		}
		rm.ScopeMetrics = keepSM
		if len(keepSM) > 0 {
			keepRM = append(keepRM, rm)
		}
	}
	req.ResourceMetrics = keepRM
}

// trimMetric cuts a metric's points to what is left and reports whether any remain.
func trimMetric(m *metricspb.Metric, left *int) bool {
	switch d := m.Data.(type) {
	case *metricspb.Metric_Gauge:
		k := min(len(d.Gauge.DataPoints), *left)
		d.Gauge.DataPoints = d.Gauge.DataPoints[:k]
		*left -= k
		return k > 0
	case *metricspb.Metric_Sum:
		k := min(len(d.Sum.DataPoints), *left)
		d.Sum.DataPoints = d.Sum.DataPoints[:k]
		*left -= k
		return k > 0
	case *metricspb.Metric_Histogram:
		kept := d.Histogram.DataPoints[:0]
		for _, dp := range d.Histogram.DataPoints {
			if cost := histogramPointCost(dp); cost <= *left {
				kept = append(kept, dp)
				*left -= cost
			}
		}
		d.Histogram.DataPoints = kept
		return len(kept) > 0
	}
	// Not stored either way: keeping it changes nothing and costs nothing.
	return true
}
