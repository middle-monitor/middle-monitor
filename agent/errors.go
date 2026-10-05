package main

import (
	"errors"
	"fmt"
)

var (
	errScrapeExporterMissing   = errors.New("otlp exporter not initialized")
	errScrapeBodyTooLarge      = errors.New("scrape body exceeds the size limit")
	errKeepIfLabelMissing      = errors.New("keep_if_labels needs a label name")
	errParamsNotAMapping       = errors.New("params must be a mapping of scalars or lists")
	errIncludeNotAPath         = errors.New("include must be a path or a list of paths")
	errFragmentShape           = errors.New("a scrape fragment must be a list of targets or a targets mapping")
	errNomadShape              = errors.New("nomad must be a block or a list of blocks")
	errTargetURLMissing        = errors.New("target has no url")
	errTargetSchemeUnsupported = errors.New("target url must be http or https")
	errHTTPSDURLMissing        = errors.New("http_sd needs a url")
	errTLSCAInvalid            = errors.New("ca_file holds no usable certificate")
	errTLSKeyPairIncomplete    = errors.New("cert_file and key_file go together")
	errAPIURLInvalid           = errors.New("invalid api url")
	errCPUUsageParse           = errors.New("could not parse cpu usage")
	errCallbackRegister        = errors.New("failed to register metrics callback")
	errConfigParse             = errors.New("failed to parse config file")
	errConfigRead              = errors.New("failed to read config file")
	errDfFormat                = errors.New("invalid df output format")
	errDfParse                 = errors.New("could not parse df output")
	errDiskUsageParse          = errors.New("could not parse disk usage")
	errDiskutilParse           = errors.New("could not parse diskutil output")
	errFlush                   = errors.New("failed to flush metrics")
	errFlushTimeout            = errors.New("metrics flush timed out")
	errGaugeCreate             = errors.New("failed to create gauge")
	errInstruments             = errors.New("failed to initialize instruments")
	errLoadavgFormat           = errors.New("invalid /proc/loadavg format")
	errLoadavgParse            = errors.New("could not parse loadavg")
	errLoadavgRead             = errors.New("could not read /proc/loadavg")
	errMemTotalInvalid         = errors.New("invalid memory total")
	errMemTotalMissing         = errors.New("could not find MemTotal")
	errMemsizeParse            = errors.New("could not parse memsize")
	errMeterNotInitialized     = errors.New("meter not initialized")
	errMetricExporter          = errors.New("failed to create metric exporter")
	errMetricsMarshal          = errors.New("failed to marshal metrics")
	errMetricsSend             = errors.New("failed to send metrics")
	errOTelResource            = errors.New("failed to create otel resource")
	errProcStatFormat          = errors.New("invalid /proc/stat format")
	errProcStatRead            = errors.New("could not read /proc/stat")
	errRegister                = errors.New("failed to register agent")
	errRegistrationDecode      = errors.New("failed to decode registration response")
	errRegistrationMarshal     = errors.New("failed to marshal registration")
	errRequestCreate           = errors.New("failed to create request")
)

// scrapeStatusError reports a target that answered with a non-200 status.
type scrapeStatusError struct {
	StatusCode int
}

func (e *scrapeStatusError) Error() string {
	return fmt.Sprintf("scrape target answered with status %d", e.StatusCode)
}

// nomadRequestError reports a non-200 answer from the Nomad HTTP API.
type nomadRequestError struct {
	Path       string
	StatusCode int
}

func (e *nomadRequestError) Error() string {
	return fmt.Sprintf("nomad %s: status %d", e.Path, e.StatusCode)
}

// httpSDError reports a non-200 answer from an http_sd endpoint.
type httpSDError struct {
	URL        string
	StatusCode int
}

func (e *httpSDError) Error() string {
	return fmt.Sprintf("http_sd %s: status %d", e.URL, e.StatusCode)
}

// regexpError names the config field a bad expression came from, so
// --config-check points at the line to fix.
type regexpError struct {
	Field string
	Expr  string
	Err   error
}

func (e *regexpError) Error() string {
	return fmt.Sprintf("%s %q: %v", e.Field, e.Expr, e.Err)
}

func (e *regexpError) Unwrap() error { return e.Err }

// includeError names the fragment file that could not be read or parsed.
type includeError struct {
	Path string
	Err  error
}

func (e *includeError) Error() string {
	return fmt.Sprintf("include %s: %v", e.Path, e.Err)
}

func (e *includeError) Unwrap() error { return e.Err }

// targetError names the target a configuration error belongs to.
type targetError struct {
	Name string
	Err  error
}

func (e *targetError) Error() string {
	return fmt.Sprintf("target %s: %v", e.Name, e.Err)
}

func (e *targetError) Unwrap() error { return e.Err }

// remoteConfigError reports a non-200 answer from the platform's agent config
// endpoint.
type remoteConfigError struct {
	StatusCode int
}

func (e *remoteConfigError) Error() string {
	return fmt.Sprintf("agent config endpoint: status %d", e.StatusCode)
}

// sendStatusError reports the backend refusing a metrics batch.
type sendStatusError struct {
	StatusCode int
	Body       string
}

func (e *sendStatusError) Error() string {
	return fmt.Sprintf("failed to send metrics with status %d: %s", e.StatusCode, e.Body)
}

// unsupportedOSError reports a metric the agent cannot read on this OS.
type unsupportedOSError struct {
	OS string
}

func (e *unsupportedOSError) Error() string {
	return fmt.Sprintf("unsupported os: %s", e.OS)
}

// registrationStatusError reports the backend refusing an agent registration.
type registrationStatusError struct {
	StatusCode int
}

func (e *registrationStatusError) Error() string {
	return fmt.Sprintf("registration failed with status %d", e.StatusCode)
}
