// metricseed sends custom OTLP metrics to a receiver, for exercising the metric
// series pipeline by hand. Dev tool: it emits the same protobuf payload an SDK
// would, so it goes through Kafka and the worker like real traffic.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

func stringAttr(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key:   key,
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}},
	}
}

// parseLabels reads "key=value,key=value".
func parseLabels(raw string) []*commonpb.KeyValue {
	attrs := []*commonpb.KeyValue{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		key, value, found := strings.Cut(pair, "=")
		if !found {
			fmt.Fprintf(os.Stderr, "ignoring malformed label %q\n", pair)
			continue
		}
		attrs = append(attrs, stringAttr(key, value))
	}
	return attrs
}

func main() {
	url := flag.String("url", "http://localhost:8081/v1/metrics", "receiver OTLP metrics endpoint")
	token := flag.String("token", "", "service token or org API key; without it the points land unattributed")
	name := flag.String("metric", "demo_requests_total", "metric name")
	labels := flag.String("labels", "route=/checkout,method=GET", "comma-separated key=value labels")
	service := flag.String("service", "checkout", "service.name resource attribute")
	host := flag.String("host", "seed-host", "host.name resource attribute")
	value := flag.Float64("value", 100, "base value")
	amplitude := flag.Float64("amplitude", 20, "sine amplitude around the base value")
	count := flag.Int("count", 30, "number of points")
	step := flag.Duration("step", time.Minute, "spacing between points")
	flag.Parse()

	attrs := parseLabels(*labels)
	end := time.Now().UTC()

	points := make([]*metricspb.NumberDataPoint, 0, *count)
	for i := *count - 1; i >= 0; i-- {
		ts := end.Add(-time.Duration(i) * *step)
		v := *value + *amplitude*math.Sin(float64(i)/4)
		points = append(points, &metricspb.NumberDataPoint{
			TimeUnixNano: uint64(ts.UnixNano()),
			Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: v},
			Attributes:   attrs,
		})
	}

	request := &collectormetrics.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
				stringAttr("service.name", *service),
				stringAttr("host.name", *host),
			}},
			ScopeMetrics: []*metricspb.ScopeMetrics{{
				Metrics: []*metricspb.Metric{{
					Name: *name,
					Unit: "1",
					Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: points}},
				}},
			}},
		}},
	}

	body, err := proto.Marshal(request)
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal:", err)
		os.Exit(1)
	}

	req, err := http.NewRequest("POST", *url, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "request:", err)
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	if *token != "" {
		req.Header.Set("Authorization", *token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "send:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	fmt.Printf("%s: %d point(s) -> HTTP %d\n", *name, len(points), resp.StatusCode)
	if *token == "" {
		fmt.Println("no token: the points carry no organization and stay invisible in the UI")
	}
}
