// Package metrics defines and exposes Prometheus metrics for nimbus.
//
// All metrics live on the default Prometheus registry, which also carries the
// standard Go runtime and process collectors. They are surfaced over HTTP by
// Handler and recorded via the helpers in this package.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const namespace = "nimbus"

var (
	// HTTP server metrics.
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "http",
		Name:      "requests_total",
		Help:      "Total number of HTTP requests processed, labeled by method, route and status code.",
	}, []string{"method", "route", "status"})

	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "http",
		Name:      "request_duration_seconds",
		Help:      "Duration of HTTP requests in seconds, labeled by method and route.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"method", "route"})

	httpRequestsInFlight = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Subsystem: "http",
		Name:      "requests_in_flight",
		Help:      "Number of HTTP requests currently being served.",
	})

	// Deployment domain metrics.
	deploymentsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "deployments_total",
		Help:      "Total number of deploy requests processed, labeled by result (success/failure).",
	}, []string{"result"})

	servicesDeployedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Name:      "services_deployed_total",
		Help:      "Total number of individual services deployed, labeled by template.",
	}, []string{"template"})

	deployDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "deploy_duration_seconds",
		Help:      "Duration of a full deploy request in seconds.",
		Buckets:   []float64{.1, .5, 1, 2.5, 5, 10, 30, 60, 120},
	})

	// Kubernetes API operation metrics.
	k8sOperationsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "kubernetes",
		Name:      "operations_total",
		Help:      "Total number of Kubernetes API operations, labeled by operation and result.",
	}, []string{"operation", "result"})

	k8sOperationDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Subsystem: "kubernetes",
		Name:      "operation_duration_seconds",
		Help:      "Duration of Kubernetes API operations in seconds, labeled by operation.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"operation"})
)

// Handler returns the HTTP handler that serves the Prometheus metrics endpoint.
func Handler() http.Handler {
	return promhttp.Handler()
}

// Middleware records request count, duration, and in-flight gauge for every
// request routed through the given mux. It must be installed via router.Use so
// that the matched route template is available for low-cardinality labels.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		httpRequestsInFlight.Inc()
		defer httpRequestsInFlight.Dec()

		mrw := &metricsResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(mrw, r)

		route := routeLabel(r)
		httpRequestsTotal.WithLabelValues(r.Method, route, strconv.Itoa(mrw.statusCode)).Inc()
		httpRequestDuration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}

// ObserveDeploy records the outcome and duration of a full deploy request.
// Call it with defer at the start of the deploy handler, flipping a success
// flag to true only on the successful return path:
//
//	start, success := time.Now(), false
//	defer func() { metrics.ObserveDeploy(start, success) }()
func ObserveDeploy(start time.Time, success bool) {
	result := "failure"
	if success {
		result = "success"
	}
	deploymentsTotal.WithLabelValues(result).Inc()
	deployDuration.Observe(time.Since(start).Seconds())
}

// ServiceDeployed increments the count of deployed services for a template.
// An empty template is reported as "custom".
func ServiceDeployed(template string) {
	if template == "" {
		template = "custom"
	}
	servicesDeployedTotal.WithLabelValues(template).Inc()
}

// ObserveK8sOp records the outcome and duration of a Kubernetes API operation.
// Call it with defer using a named error return:
//
//	func CreateThing(...) (_ *Thing, err error) {
//	    defer metrics.ObserveK8sOp("create_thing", time.Now(), &err)
//	    ...
//	}
func ObserveK8sOp(operation string, start time.Time, err *error) {
	result := "success"
	if err != nil && *err != nil {
		result = "error"
	}
	k8sOperationsTotal.WithLabelValues(operation, result).Inc()
	k8sOperationDuration.WithLabelValues(operation).Observe(time.Since(start).Seconds())
}

// metricsResponseWriter captures the response status code.
type metricsResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (mrw *metricsResponseWriter) WriteHeader(code int) {
	mrw.statusCode = code
	mrw.ResponseWriter.WriteHeader(code)
}

// routeLabel returns the matched mux route template for a request, falling back
// to a fixed value for unmatched paths so random/unknown URLs don't explode the
// metric cardinality.
func routeLabel(r *http.Request) string {
	if route := mux.CurrentRoute(r); route != nil {
		if tmpl, err := route.GetPathTemplate(); err == nil {
			return tmpl
		}
	}
	return "<unmatched>"
}
