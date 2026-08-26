package health

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/mywebsite/construction-ai-gateway/internal/capability"
	"github.com/mywebsite/construction-ai-gateway/internal/inference"
)

const (
	StatusReady       = "ready"
	StatusNotReady    = "not_ready"
	StatusDormant     = "dormant"
	StatusAvailable   = "available"
	StatusUnavailable = "unavailable"
)

type ProbeSource interface {
	RefreshProbes(ctx context.Context, targets []inference.ModelTarget, maxAge time.Duration)
	Probe(vendor, baseURL string) (inference.EndpointProbe, bool)
}

type CapabilityLister interface {
	All() []capability.Definition
}

type CapabilityStatus struct {
	Capability string `json:"capability"`
	Status     string `json:"status"`
	Vendor     string `json:"vendor,omitempty"`
	URL        string `json:"url"`
	Model      string `json:"model"`
	Warning    string `json:"warning,omitempty"`
	Error      string `json:"error,omitempty"`
}

type Report struct {
	Status       string             `json:"status"`
	Capabilities []CapabilityStatus `json:"capabilities"`
}

type Handler struct {
	registry CapabilityLister
	probes   ProbeSource
}

func NewHandler(registry CapabilityLister, probes ProbeSource) *Handler {
	return &Handler{
		registry: registry,
		probes:   probes,
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/health", h.handleHTML)
	mux.HandleFunc("/health.json", h.handleJSON)
}

func (h *Handler) handleHTML(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	report := h.buildReport(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(httpStatus(report.Status))
	_, _ = w.Write([]byte(renderHTML(report)))
}

func (h *Handler) handleJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	report := h.buildReport(r.Context())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus(report.Status))
	_ = json.NewEncoder(w).Encode(report)
}

func (h *Handler) buildReport(ctx context.Context) Report {
	defs := h.registry.All()
	if len(defs) == 0 {
		return Report{
			Status:       StatusDormant,
			Capabilities: []CapabilityStatus{},
		}
	}

	targets := make([]inference.ModelTarget, 0, len(defs))
	for _, def := range defs {
		targets = append(targets, inference.ModelTarget{
			Vendor:  def.Vendor,
			BaseURL: def.BaseURL,
			Name:    def.Model,
		})
	}
	h.probes.RefreshProbes(ctx, targets, inference.ProbeTTL)

	statuses := make([]CapabilityStatus, 0, len(defs))
	overall := StatusReady

	for _, def := range defs {
		status := CapabilityStatus{
			Capability: def.Name,
			Vendor:     inference.NormalizeVendor(def.Vendor),
			URL:        def.BaseURL,
			Model:      def.Model,
			Status:     StatusAvailable,
		}

		probe, ok := h.probes.Probe(def.Vendor, def.BaseURL)
		if !ok || !probe.ChatOK {
			status.Status = StatusUnavailable
			overall = StatusNotReady
			if !ok {
				status.Error = "no probe result"
			} else if probe.ChatErr != nil {
				status.Error = probe.ChatErr.Error()
			} else {
				status.Error = "chat ping failed"
			}
		}

		if ok {
			if probe.CatalogErr != nil {
				status.Warning = inference.WarningCatalogUnavailable()
			} else if strings.TrimSpace(def.Model) != "" && !probe.CatalogContains(def.Model) {
				status.Warning = inference.WarningModelNotListed(def.Model)
			}
		}

		statuses = append(statuses, status)
	}

	return Report{
		Status:       overall,
		Capabilities: statuses,
	}
}

func httpStatus(overall string) int {
	if overall == StatusReady {
		return http.StatusOK
	}
	return http.StatusServiceUnavailable
}

func renderHTML(report Report) string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n<head><meta charset=\"utf-8\"><title>AI capability health</title></head>\n<body>\n")
	b.WriteString("<h1>AI capability health</h1>\n")
	if report.Status == StatusDormant {
		b.WriteString(fmt.Sprintf("<p>Overall: <span id=\"overall\" style=\"color: %s\">%s</span> (awaiting manifest)</p>\n",
			colorFor(report.Status), html.EscapeString(report.Status)))
	} else {
		b.WriteString(fmt.Sprintf("<p>Overall: <span id=\"overall\" style=\"color: %s\">%s</span></p>\n",
			colorFor(report.Status), html.EscapeString(report.Status)))
	}

	for _, item := range report.Capabilities {
		line := fmt.Sprintf("<div id=\"%s\">%s: <span style=\"color: %s\">%s</span>",
			html.EscapeString(item.Capability),
			html.EscapeString(item.Capability),
			colorFor(item.Status),
			html.EscapeString(item.Status),
		)
		if item.Warning != "" {
			line += fmt.Sprintf(" <span class=\"warning\">%s</span>", html.EscapeString(item.Warning))
		}
		line += "</div>\n"
		b.WriteString(line)
	}

	b.WriteString("</body>\n</html>\n")
	return b.String()
}

func colorFor(status string) string {
	switch status {
	case StatusReady, StatusAvailable:
		return "green"
	default:
		return "red"
	}
}

func NewHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
}

func NewServer(addr string, handler *Handler) *http.Server {
	mux := http.NewServeMux()
	handler.Register(mux)
	return NewHTTPServer(addr, mux)
}
