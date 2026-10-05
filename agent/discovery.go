package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const defaultDiscoveryInterval = 30 * time.Second

// TagLabel turns a Nomad tag into a label: the tag "fqdn:app.example.com" with
// prefix "fqdn:" becomes the label fqdn=app.example.com.
type TagLabel struct {
	Prefix string `yaml:"prefix"`
	Label  string `yaml:"label"`
}

// NomadConfig discovers targets from Nomad's own service registry. One block
// describes one family of services: each piece of software decides where it
// serves its metrics, so a cluster needs several.
type NomadConfig struct {
	Address     string     `yaml:"address"`      // e.g. http://localhost:4646
	Namespace   string     `yaml:"namespace"`    // empty means Nomad's default
	Region      string     `yaml:"region"`       // empty means the region of the server contacted
	Token       string     `yaml:"token"`        // ACL token, optional
	TokenFile   string     `yaml:"token_file"`   // ACL token read from a file
	Tag         string     `yaml:"tag"`          // only services carrying this tag; empty means all
	Service     string     `yaml:"service"`      // regex on the service name; empty means all
	MetricsPath string     `yaml:"metrics_path"` // default /metrics
	Scheme      string     `yaml:"scheme"`       // default http
	Port        int        `yaml:"port"`         // overrides the registered port
	TagLabels   []TagLabel `yaml:"tag_labels"`

	// Added to every series of the targets this block discovers.
	Labels map[string]string `yaml:"labels"`
	// TLS presented to the Nomad API itself, not to the discovered targets: a
	// cluster API is routinely served behind a private CA or a self-signed cert.
	TLSConfig *TLSConfig `yaml:"tls_config"`
}

// NomadConfigs accepts either a single block or a list of them, so configs
// written against the single-block form keep working.
type NomadConfigs []NomadConfig

func (n *NomadConfigs) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.MappingNode:
		var single NomadConfig
		if err := node.Decode(&single); err != nil {
			return err
		}
		// An empty address is how the single-block form used to mean "off".
		if single.Address == "" {
			*n = nil
			return nil
		}
		*n = NomadConfigs{single}
		return nil
	case yaml.SequenceNode:
		var list []NomadConfig
		if err := node.Decode(&list); err != nil {
			return err
		}
		*n = list
		return nil
	default:
		return errNomadShape
	}
}

// compile resolves the regex of a block once, so a bad expression is a config
// error rather than a discovery that silently matches nothing.
func (c NomadConfig) compile() (*regexp.Regexp, error) {
	if c.Service == "" {
		return nil, nil
	}
	re, err := regexp.Compile(c.Service)
	if err != nil {
		return nil, &regexpError{Field: "nomad.service", Expr: c.Service, Err: err}
	}
	return re, nil
}

// HTTPSDConfig discovers targets from any HTTP endpoint answering the Prometheus
// http_sd format. It is the escape hatch for orchestrators the agent does not
// know about: the list is produced on the operator's side.
type HTTPSDConfig struct {
	URL             string            `yaml:"url"`
	MetricsPath     string            `yaml:"metrics_path"` // default /metrics
	Scheme          string            `yaml:"scheme"`       // default http
	Labels          map[string]string `yaml:"labels"`
	BearerToken     string            `yaml:"bearer_token"`
	BearerTokenFile string            `yaml:"bearer_token_file"`
	Headers         map[string]string `yaml:"headers"`
	TLSConfig       *TLSConfig        `yaml:"tls_config"`
}

// SRVConfig discovers targets from a DNS SRV record.
type SRVConfig struct {
	Name        string            `yaml:"name"`         // full record, e.g. _metrics._tcp.service.consul
	MetricsPath string            `yaml:"metrics_path"` // default /metrics
	Scheme      string            `yaml:"scheme"`       // default http
	Labels      map[string]string `yaml:"labels"`
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// nomadService is one instance in Nomad's service registry.
type nomadService struct {
	ServiceName string   `json:"ServiceName"`
	Address     string   `json:"Address"`
	Port        int      `json:"Port"`
	Tags        []string `json:"Tags"`
	AllocID     string   `json:"AllocID"`
	JobID       string   `json:"JobID"`
	Namespace   string   `json:"Namespace"`
}

// nomadServiceList is one entry of GET /v1/services.
type nomadServiceList struct {
	Namespace string `json:"Namespace"`
	Services  []struct {
		ServiceName string   `json:"ServiceName"`
		Tags        []string `json:"Tags"`
	} `json:"Services"`
}

func hasTag(tags []string, want string) bool {
	if want == "" {
		return true
	}
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}

func nomadRequest(ctx context.Context, client *http.Client, cfg NomadConfig, path string, out interface{}) error {
	url := strings.TrimSuffix(cfg.Address, "/") + path
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	token := expandEnv(cfg.Token)
	if cfg.TokenFile != "" {
		token, err = readSecretFile(cfg.TokenFile)
		if err != nil {
			return err
		}
	}
	if token != "" {
		req.Header.Set("X-Nomad-Token", token)
	}
	q := req.URL.Query()
	if cfg.Namespace != "" {
		q.Set("namespace", cfg.Namespace)
	}
	if cfg.Region != "" {
		q.Set("region", cfg.Region)
	}
	req.URL.RawQuery = q.Encode()

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &nomadRequestError{Path: path, StatusCode: resp.StatusCode}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// DiscoverNomad lists the registered services, then the instances of each one
// that carries the configured tag and matches the configured name.
func DiscoverNomad(ctx context.Context, client *http.Client, cfg NomadConfig) ([]ScrapeTarget, error) {
	if cfg.Address == "" {
		return nil, nil
	}
	serviceRE, err := cfg.compile()
	if err != nil {
		return nil, err
	}

	if cfg.TLSConfig != nil {
		client, err = clientFor(ScrapeConfig{}, ScrapeTarget{TLSConfig: cfg.TLSConfig})
		if err != nil {
			return nil, err
		}
	}

	var lists []nomadServiceList
	if err := nomadRequest(ctx, client, cfg, "/v1/services", &lists); err != nil {
		return nil, err
	}

	targets := []ScrapeTarget{}
	for _, list := range lists {
		for _, service := range list.Services {
			if !hasTag(service.Tags, cfg.Tag) {
				continue
			}
			if serviceRE != nil && !serviceRE.MatchString(service.ServiceName) {
				continue
			}
			var instances []nomadService
			if err := nomadRequest(ctx, client, cfg, "/v1/service/"+service.ServiceName, &instances); err != nil {
				// One unreadable service must not lose the others.
				continue
			}
			for _, instance := range instances {
				if instance.Address == "" {
					continue
				}
				port := instance.Port
				if cfg.Port > 0 {
					port = cfg.Port
				}
				if port == 0 {
					continue
				}
				targets = append(targets, nomadTarget(instance, port, cfg))
			}
		}
	}
	sortTargets(targets)
	return targets, nil
}

func nomadTarget(instance nomadService, port int, cfg NomadConfig) ScrapeTarget {
	address := net.JoinHostPort(instance.Address, strconv.Itoa(port))
	labels := map[string]string{
		"nomad_service": instance.ServiceName,
		"instance":      address,
	}
	// Job and allocation make a target traceable back to what Nomad is running.
	if instance.JobID != "" {
		labels["nomad_job"] = instance.JobID
	}
	if instance.AllocID != "" {
		labels["nomad_alloc"] = instance.AllocID
	}
	if instance.Namespace != "" {
		labels["nomad_namespace"] = instance.Namespace
	}
	for key, value := range cfg.Labels {
		labels[key] = value
	}
	for key, value := range tagLabels(instance.Tags, cfg.TagLabels) {
		labels[key] = value
	}
	return ScrapeTarget{
		Name:   instance.ServiceName + "/" + address,
		URL:    orDefault(cfg.Scheme, "http") + "://" + address + orDefault(cfg.MetricsPath, "/metrics"),
		Labels: labels,
	}
}

func tagLabels(tags []string, rules []TagLabel) map[string]string {
	labels := map[string]string{}
	for _, rule := range rules {
		if rule.Label == "" || rule.Prefix == "" {
			continue
		}
		for _, tag := range tags {
			if strings.HasPrefix(tag, rule.Prefix) {
				labels[rule.Label] = strings.TrimPrefix(tag, rule.Prefix)
				break
			}
		}
	}
	return labels
}

// httpSDGroup is one entry of the Prometheus http_sd response.
type httpSDGroup struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels"`
}

// DiscoverHTTP reads a target list in the Prometheus http_sd format. The
// reserved labels __metrics_path__ and __scheme__ are honoured, which is how a
// single endpoint can describe services that expose metrics in different places.
func DiscoverHTTP(ctx context.Context, client *http.Client, cfg HTTPSDConfig) ([]ScrapeTarget, error) {
	if cfg.URL == "" {
		return nil, nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", cfg.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	auth := ScrapeTarget{
		BearerToken:     cfg.BearerToken,
		BearerTokenFile: cfg.BearerTokenFile,
		Headers:         cfg.Headers,
	}
	if err := applyAuth(req, auth); err != nil {
		return nil, err
	}

	httpClient := client
	if cfg.TLSConfig != nil {
		httpClient, err = clientFor(ScrapeConfig{}, ScrapeTarget{TLSConfig: cfg.TLSConfig})
		if err != nil {
			return nil, err
		}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &httpSDError{URL: cfg.URL, StatusCode: resp.StatusCode}
	}

	var groups []httpSDGroup
	if err := json.NewDecoder(resp.Body).Decode(&groups); err != nil {
		return nil, err
	}

	targets := []ScrapeTarget{}
	for _, group := range groups {
		for _, address := range group.Targets {
			if address == "" {
				continue
			}
			scheme := orDefault(group.Labels["__scheme__"], orDefault(cfg.Scheme, "http"))
			path := orDefault(group.Labels["__metrics_path__"], orDefault(cfg.MetricsPath, "/metrics"))

			labels := map[string]string{"instance": address}
			for key, value := range cfg.Labels {
				labels[key] = value
			}
			// Double-underscore labels are directives for the scraper, not
			// dimensions of the series.
			for key, value := range group.Labels {
				if !strings.HasPrefix(key, "__") {
					labels[key] = value
				}
			}
			targets = append(targets, ScrapeTarget{
				Name:   orDefault(labels["job"], "http_sd") + "/" + address,
				URL:    scheme + "://" + address + path,
				Labels: labels,
			})
		}
	}
	sortTargets(targets)
	return targets, nil
}

// DiscoverSRV resolves a DNS SRV record into one target per answer.
func DiscoverSRV(cfg SRVConfig) ([]ScrapeTarget, error) {
	if cfg.Name == "" {
		return nil, nil
	}
	// The record is given whole, so the service/proto form is not used here.
	_, records, err := net.LookupSRV("", "", cfg.Name)
	if err != nil {
		return nil, err
	}

	scheme := orDefault(cfg.Scheme, "http")
	path := orDefault(cfg.MetricsPath, "/metrics")

	targets := make([]ScrapeTarget, 0, len(records))
	for _, record := range records {
		host := strings.TrimSuffix(record.Target, ".")
		if host == "" || record.Port == 0 {
			continue
		}
		address := net.JoinHostPort(host, strconv.Itoa(int(record.Port)))
		labels := map[string]string{"srv_record": cfg.Name, "instance": address}
		for key, value := range cfg.Labels {
			labels[key] = value
		}
		targets = append(targets, ScrapeTarget{
			Name:   cfg.Name + "/" + address,
			URL:    scheme + "://" + address + path,
			Labels: labels,
		})
	}
	sortTargets(targets)
	return targets, nil
}

// sortTargets keeps discovery output stable so an unchanged infrastructure does
// not look like a change to the manager.
func sortTargets(targets []ScrapeTarget) {
	sort.Slice(targets, func(i, j int) bool { return targets[i].Name < targets[j].Name })
}
