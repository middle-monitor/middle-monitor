package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
)

// Container labels a workload sets to steer its own collection.
const (
	labelService   = "io.middlemonitor.service"
	labelLogs      = "io.middlemonitor.logs"
	labelMultiline = "io.middlemonitor.multiline"
)

// logOrigin is what a tailed file belongs to. It becomes the OTLP resource of
// every record read from that file.
type logOrigin struct {
	service  string
	hostname string
	// Matched by the source's include and exclude.
	name      string
	attrs     map[string]string
	multiline *regexp.Regexp
	skip      bool
}

func (o *logOrigin) resource() []*commonpb.KeyValue {
	kv := []*commonpb.KeyValue{
		stringAttr("service.name", o.service),
		stringAttr("host.name", o.hostname),
	}
	keys := make([]string, 0, len(o.attrs))
	for key := range o.attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		kv = append(kv, stringAttr(key, o.attrs[key]))
	}
	return kv
}

// resolveOrigin names the owner of a file, or reports it skipped. hostService is
// the agent's own service, used for plain files with no service configured.
func resolveOrigin(src *LogSource, path, hostname, hostService string) *logOrigin {
	var origin *logOrigin
	switch src.Type {
	case logSourceDocker:
		origin = dockerOrigin(path)
	case logSourceKubernetes:
		origin = kubernetesOrigin(path)
	default:
		origin = &logOrigin{service: hostService, attrs: map[string]string{}}
	}
	return finishOrigin(src, origin, hostname)
}

// finishOrigin applies what the source configures on top of the owner it found.
func finishOrigin(src *LogSource, origin *logOrigin, hostname string) *logOrigin {
	origin.hostname = hostname
	if src.Service != "" {
		origin.service = src.Service
	}
	if origin.multiline == nil {
		origin.multiline = src.multiline
	}
	if src.Type != logSourceFile {
		if src.include != nil && !src.include.MatchString(origin.name) {
			origin.skip = true
		}
		if src.exclude != nil && src.exclude.MatchString(origin.name) {
			origin.skip = true
		}
	}
	return origin
}

// dockerOrigin reads the container's config.v2.json, next to its json-file log.
// It is the same file the Docker daemon reloads its containers from, so no API
// call or socket is needed.
func dockerOrigin(path string) *logOrigin {
	dir := filepath.Dir(path)
	id := filepath.Base(dir)
	origin := &logOrigin{name: shortID(id), service: shortID(id), attrs: map[string]string{"container.id": id}}

	var cfg struct {
		Name   string `json:"Name"`
		Config struct {
			Image  string            `json:"Image"`
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.v2.json"))
	if err != nil || json.Unmarshal(data, &cfg) != nil {
		return origin
	}

	name := strings.TrimPrefix(cfg.Name, "/")
	labels := cfg.Config.Labels
	origin.name = name
	origin.attrs["container.name"] = name
	if cfg.Config.Image != "" {
		origin.attrs["container.image.name"] = cfg.Config.Image
	}
	origin.service = firstNonEmpty(labels[labelService], labels["com.docker.compose.service"], labels["com.docker.swarm.service.name"], name)
	origin.skip = strings.EqualFold(labels[labelLogs], "false")
	if expr := labels[labelMultiline]; expr != "" {
		// A bad expression on one container must not stop the others.
		origin.multiline, _ = regexp.Compile(expr)
	}
	return origin
}

// kubernetesOrigin reads the identity kubelet encodes in the path:
// /var/log/pods/<namespace>_<pod>_<uid>/<container>/<restart>.log. Neither a
// namespace nor a pod name can hold an underscore, so the split is exact.
func kubernetesOrigin(path string) *logOrigin {
	container := filepath.Base(filepath.Dir(path))
	parts := strings.SplitN(filepath.Base(filepath.Dir(filepath.Dir(path))), "_", 3)
	origin := &logOrigin{service: container, name: container, attrs: map[string]string{"container.name": container}}
	if len(parts) != 3 {
		return origin
	}
	namespace, pod, uid := parts[0], parts[1], parts[2]
	origin.name = namespace + "/" + pod + "/" + container
	origin.attrs["k8s.namespace.name"] = namespace
	origin.attrs["k8s.pod.name"] = pod
	origin.attrs["k8s.pod.uid"] = uid
	origin.attrs["k8s.container.name"] = container
	return origin
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func stringAttr(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}}
}
